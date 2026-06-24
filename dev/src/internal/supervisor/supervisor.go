package supervisor

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ksu-proxy/internal/config"
)

type Supervisor struct {
	Config config.Config
	Logger *log.Logger
}

type ProcessInfo struct {
	Name string `json:"name"`
	PID  int    `json:"pid"`
}

func (s Supervisor) StartSingBox(ctx context.Context) error {
	if !s.Config.SingBox.Enabled {
		return nil
	}
	logPath := filepath.Join(s.Config.Paths.LogDir, "sing-box.log")
	args := []string{"run", "-c", s.Config.SingBox.RuntimeConfig, "-D", s.Config.SingBox.ConfigDir}
	return s.start(ctx, "sing-box", s.Config.SingBox.Binary, args, logPath)
}

func (s Supervisor) StartXTunnel(ctx context.Context, nodes []config.XTunnelNode) error {
	if !s.Config.XTunnel.Enabled {
		return nil
	}
	for _, node := range nodes {
		if node.DNS == "" {
			return fmt.Errorf("x-tunnel node %s missing dns", node.Tag)
		}
		if node.ECH == "" {
			return fmt.Errorf("x-tunnel node %s missing ech host", node.Tag)
		}
		if node.FrontIPs == "" {
			return fmt.Errorf("x-tunnel node %s missing front ips", node.Tag)
		}
		if node.Token == "" {
			return fmt.Errorf("x-tunnel node %s missing token", node.Tag)
		}
		args := []string{
			"-dns", node.DNS,
			"-ech", node.ECH,
			"-f", node.Forward,
			"-l", "socks5://127.0.0.1:" + strconv.Itoa(node.ListenPort),
			"-n", strconv.Itoa(node.N),
		}
		if node.FrontIPs != "" {
			args = append(args, "-ip", node.FrontIPs)
		}
		if node.Token != "" {
			args = append(args, "-token", node.Token)
		}
		logPath := filepath.Join(s.Config.Paths.LogDir, "x-tunnel-"+sanitize(node.Tag)+".log")
		if s.Logger != nil {
			s.Logger.Printf("x-tunnel node %s listen=127.0.0.1:%d dns=%s ech=%s front=%s front_ips=%s n=%d", node.Tag, node.ListenPort, node.DNS, node.ECH, node.Forward, node.FrontIPs, node.N)
		}
		if err := s.start(ctx, "x-tunnel:"+node.Tag, s.Config.XTunnel.Binary, args, logPath); err != nil {
			return err
		}
	}
	return nil
}

func (s Supervisor) StopXTunnel(ctx context.Context) error {
	items, err := s.ReadPids()
	if err != nil {
		return err
	}
	stopped := false
	for _, item := range items {
		if !strings.HasPrefix(item.Name, "x-tunnel:") {
			continue
		}
		if item.PID <= 0 {
			continue
		}
		if s.Logger != nil {
			s.Logger.Printf("stopping %s pid=%d", item.Name, item.PID)
		}
		proc, err := os.FindProcess(item.PID)
		if err != nil {
			continue
		}
		_ = proc.Signal(syscall.SIGTERM)
		stopped = true
	}
	if stopped {
		time.Sleep(300 * time.Millisecond)
		for _, item := range items {
			if !strings.HasPrefix(item.Name, "x-tunnel:") {
				continue
			}
			proc, err := os.FindProcess(item.PID)
			if err == nil {
				_ = proc.Kill()
			}
		}
	}
	return nil
}

func (s Supervisor) StopAll(ctx context.Context) error {
	items, err := s.ReadPids()
	if err != nil {
		return err
	}
	extra := s.findCoreProcesses()
	if len(items) == 0 && len(extra) == 0 {
		_ = os.Remove(s.pidFile())
		return nil
	}
	seen := make(map[int]bool)
	for _, item := range items {
		if item.PID <= 0 {
			continue
		}
		seen[item.PID] = true
		if s.Logger != nil {
			s.Logger.Printf("stopping %s pid=%d", item.Name, item.PID)
		}
		proc, err := os.FindProcess(item.PID)
		if err != nil {
			continue
		}
		_ = proc.Signal(syscall.SIGTERM)
	}
	time.Sleep(300 * time.Millisecond)
	for _, item := range items {
		proc, err := os.FindProcess(item.PID)
		if err == nil {
			_ = proc.Kill()
		}
	}
	for _, item := range extra {
		if seen[item.PID] {
			continue
		}
		if s.Logger != nil {
			s.Logger.Printf("stopping stale %s pid=%d", item.Name, item.PID)
		}
		proc, err := os.FindProcess(item.PID)
		if err == nil {
			_ = proc.Signal(syscall.SIGTERM)
			time.Sleep(100 * time.Millisecond)
			_ = proc.Kill()
		}
	}
	if err := os.Remove(s.pidFile()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s Supervisor) StopRunDaemons(ctx context.Context) error {
	_ = ctx
	exe, _ := os.Executable()
	for _, item := range findProcesses(func(cmdline string, pid int) bool {
		if pid == os.Getpid() || exe == "" || !strings.Contains(cmdline, exe) {
			return false
		}
		return strings.Contains(cmdline, "\x00run") || strings.Contains(cmdline, " run")
	}) {
		if s.Logger != nil {
			s.Logger.Printf("stopping proxyd run daemon pid=%d", item.PID)
		}
		proc, err := os.FindProcess(item.PID)
		if err == nil {
			_ = proc.Signal(syscall.SIGTERM)
		}
	}
	return nil
}

func (s Supervisor) ReadPids() ([]ProcessInfo, error) {
	file, err := os.Open(s.pidFile())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	items := make([]ProcessInfo, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		name, pidText, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(pidText))
		if err != nil {
			continue
		}
		items = append(items, ProcessInfo{Name: name, PID: pid})
	}
	return items, scanner.Err()
}

func (s Supervisor) start(ctx context.Context, name string, binary string, args []string, logPath string) error {
	if binary == "" {
		return fmt.Errorf("%s binary path is empty", name)
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Dir = s.Config.Paths.WorkDir
	if s.Logger != nil {
		s.Logger.Printf("starting %s: %s %s", name, binary, strings.Join(maskArgs(args), " "))
	}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
	}()
	if err := s.appendPid(name, cmd.Process.Pid); err != nil {
		return err
	}
	return nil
}

func maskArgs(args []string) []string {
	out := append([]string{}, args...)
	for i := 0; i < len(out); i++ {
		switch out[i] {
		case "-token", "--token":
			if i+1 < len(out) {
				out[i+1] = "****"
				i++
			}
		}
	}
	return out
}

func (s Supervisor) appendPid(name string, pid int) error {
	if err := os.MkdirAll(s.Config.Paths.RunDir, 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(s.pidFile(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintf(file, "%s:%d\n", name, pid)
	return err
}

func (s Supervisor) pidFile() string {
	return filepath.Join(s.Config.Paths.RunDir, "proxyd.pids")
}

func (s Supervisor) findCoreProcesses() []ProcessInfo {
	matches := make([]ProcessInfo, 0)
	if s.Config.SingBox.Binary != "" {
		matches = append(matches, findProcesses(func(cmdline string, pid int) bool {
			return strings.Contains(cmdline, s.Config.SingBox.Binary)
		})...)
	}
	if s.Config.XTunnel.Binary != "" {
		matches = append(matches, findProcesses(func(cmdline string, pid int) bool {
			return strings.Contains(cmdline, s.Config.XTunnel.Binary)
		})...)
	}
	return uniqueProcessInfo(matches)
}

func findProcesses(match func(cmdline string, pid int) bool) []ProcessInfo {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	out := make([]ProcessInfo, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		cmdline := string(raw)
		if match(cmdline, pid) {
			out = append(out, ProcessInfo{Name: strings.ReplaceAll(cmdline, "\x00", " "), PID: pid})
		}
	}
	return out
}

func uniqueProcessInfo(items []ProcessInfo) []ProcessInfo {
	seen := make(map[int]bool)
	out := make([]ProcessInfo, 0, len(items))
	for _, item := range items {
		if item.PID <= 0 || seen[item.PID] {
			continue
		}
		seen[item.PID] = true
		out = append(out, item)
	}
	return out
}

func sanitize(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "node"
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "node"
	}
	return b.String()
}
