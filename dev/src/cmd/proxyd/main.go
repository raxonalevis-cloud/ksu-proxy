package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"ksu-proxy/internal/appresolver"
	"ksu-proxy/internal/config"
	"ksu-proxy/internal/core"
	"ksu-proxy/internal/firewall"
	"ksu-proxy/internal/hotspot"
	"ksu-proxy/internal/supervisor"
)

func main() {
	configPath := flag.String("config", filepath.Join(config.DefaultDataDir, "config", "config.json"), "config file")
	flag.Parse()
	cmd := "status"
	if flag.NArg() > 0 {
		cmd = flag.Arg(0)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		fatal(err)
	}
	logger := newLogger(*cfg)
	ctx := context.Background()

	switch cmd {
	case "run":
		err = run(ctx, *cfg, logger)
	case "start":
		err = start(ctx, *cfg, logger)
	case "stop":
		err = stop(ctx, *cfg, logger)
	case "restart":
		_ = stop(ctx, *cfg, logger)
		err = start(ctx, *cfg, logger)
	case "reconcile":
		err = reconcile(ctx, *cfg, logger)
	case "render":
		err = render(*cfg)
	case "status":
		err = status(ctx, *cfg)
	case "list-apps":
		err = listApps(ctx, *cfg)
	default:
		err = fmt.Errorf("unknown command: %s", cmd)
	}
	if err != nil {
		fatal(err)
	}
}

func run(ctx context.Context, cfg config.Config, logger *log.Logger) error {
	// Start admin server and core services (sing-box + x-tunnel + firewall) in parallel.
	var (
		admin    *http.Server
		adminErr error
		wg       sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		admin, adminErr = startAdminServer(ctx, cfg, logger)
	}()

	singErr := start(ctx, cfg, logger)
	wg.Wait()

	if adminErr != nil && singErr == nil {
		return adminErr
	}
	if singErr != nil {
		if admin != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = admin.Shutdown(shutdownCtx)
		}
		return singErr
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = admin.Shutdown(shutdownCtx)
	}()

	ticker := time.NewTicker(time.Duration(cfg.Runtime.PollIntervalSeconds) * time.Second)
	defer ticker.Stop()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	watch := newWatchSet(cfg)
	logger.Printf("proxyd run loop started")
	for {
		select {
		case <-ticker.C:
			if reason := moduleDisabledReason(cfg); reason != "" {
				logger.Printf("%s detected, stopping", reason)
				return stop(ctx, cfg, logger)
			}
			if watch.changed(cfg.Whitelist.File) {
				logger.Printf("whitelist changed, reconciling firewall")
				if err := reconcile(ctx, cfg, logger); err != nil {
					logger.Printf("reconcile failed: %v", err)
				}
			}
			if cfg.SingBox.RestartOnConfigMod {
				restart := watch.changed(cfg.SingBox.BaseConfig) || watch.changed(cfg.XTunnel.NodesFile)
				if !restart {
					for _, p := range loadProviderPaths(cfg) {
						if watch.changed(p) {
							restart = true
							break
						}
					}
				}
				if restart {
					logger.Printf("config/provider changed, restarting cores")
					_ = stop(ctx, cfg, logger)
					if err := start(ctx, cfg, logger); err != nil {
						logger.Printf("restart failed: %v", err)
					}
				}
			}
		case sig := <-sigCh:
			logger.Printf("received %s, stopping", sig)
			return stop(ctx, cfg, logger)
		}
	}
}

func start(ctx context.Context, cfg config.Config, logger *log.Logger) (err error) {
	sup := supervisor.Supervisor{Config: cfg, Logger: logger}
	if stopErr := sup.StopAll(ctx); stopErr != nil && logger != nil {
		logger.Printf("pre-start process cleanup warning: %v", stopErr)
	}
	fw := firewall.Manager{Config: cfg, Logger: logger}
	defer func() {
		if err != nil {
			if logger != nil {
				logger.Printf("start failed, rolling back partial state: %v", err)
			}
			_ = fw.Cleanup(ctx)
			_ = sup.StopAll(ctx)
		}
	}()

	if logger != nil {
		logger.Printf("start stage: load x-tunnel nodes from %s", cfg.XTunnel.NodesFile)
	}
	nodes, err := config.LoadXTunnelNodes(cfg.XTunnel.NodesFile, cfg.XTunnel)
	if err != nil {
		return fmt.Errorf("load x-tunnel nodes from %s: %w", cfg.XTunnel.NodesFile, err)
	}
	if logger != nil {
		logger.Printf("start stage: render sing-box runtime config from %s to %s", cfg.SingBox.BaseConfig, cfg.SingBox.RuntimeConfig)
	}
	if err := render(cfg); err != nil {
		return fmt.Errorf("render sing-box runtime config: %w", err)
	}
	if logger != nil {
		logger.Printf("start stage: validate sing-box config %s", cfg.SingBox.RuntimeConfig)
	}
	if err := core.ValidateSingBox(cfg); err != nil {
		return fmt.Errorf("validate sing-box config: %w", err)
	}
	if logger != nil {
		logger.Printf("start stage: start %d x-tunnel node(s)", len(nodes))
	}
	if err := sup.StartXTunnel(ctx, nodes); err != nil {
		return fmt.Errorf("start x-tunnel: %w", err)
	}
	if logger != nil {
		logger.Printf("start stage: start sing-box")
	}
	if err := sup.StartSingBox(ctx); err != nil {
		return fmt.Errorf("start sing-box: %w", err)
	}
	time.Sleep(time.Duration(cfg.Runtime.StartGraceMillis) * time.Millisecond)
	if logger != nil {
		logger.Printf("start stage: reconcile firewall")
	}
	if err := reconcile(ctx, cfg, logger); err != nil {
		if logger != nil {
			logger.Printf("initial reconcile failed, keeping cores and admin service alive: %v", err)
		}
	}
	return nil
}

func stop(ctx context.Context, cfg config.Config, logger *log.Logger) error {
	stopCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	fw := firewall.Manager{Config: cfg, Logger: logger}
	_ = fw.Cleanup(stopCtx)
	sup := supervisor.Supervisor{Config: cfg, Logger: logger}
	err := sup.StopAll(stopCtx)
	_ = sup.StopRunDaemons(stopCtx)
	return err
}

func reconcile(ctx context.Context, cfg config.Config, logger *log.Logger) error {
	reconcileMu.Lock()
	defer reconcileMu.Unlock()
	wl, err := config.LoadWhitelist(cfg.Whitelist.File)
	if err != nil {
		return err
	}
	resolver := appresolver.New(cfg.Whitelist.CloneUserIDs)

	var uids []int
	whitelistMode := strings.ToLower(strings.TrimSpace(cfg.Whitelist.Mode))

	switch whitelistMode {
	case "blacklist":
		// 黑名单模式：所有 UID 走代理，除了黑名单中的
		blacklistInstances, err := resolver.ResolveWhitelist(ctx, wl.EnabledPackages())
		if err != nil {
			logger.Printf("app resolver warning: %v", err)
		}
		blacklistUIDs := appresolver.UIDs(blacklistInstances)

		// 获取所有已安装应用
		allInstances, err := resolver.ListInstalled(ctx)
		if err != nil {
			logger.Printf("list installed apps warning: %v", err)
		}
		allUIDs := appresolver.UIDs(allInstances)

		// 从所有 UID 中移除黑名单 UID
		blacklistSet := make(map[int]bool)
		for _, uid := range blacklistUIDs {
			blacklistSet[uid] = true
		}
		for _, uid := range allUIDs {
			if !blacklistSet[uid] {
				uids = append(uids, uid)
			}
		}
		if logger != nil {
			logger.Printf("blacklist mode: total_apps=%d blacklist_uids=%d proxy_uids=%d", len(allUIDs), len(blacklistUIDs), len(uids))
		}

	case "global":
		// 全局模式：所有 UID 走代理
		allInstances, err := resolver.ListInstalled(ctx)
		if err != nil {
			logger.Printf("list installed apps warning: %v", err)
		}
		uids = appresolver.UIDs(allInstances)
		if logger != nil {
			logger.Printf("global mode: proxy_uids=%d", len(uids))
		}

	default:
		// 白名单模式（默认）：只有指定的 UID 走代理
		instances, err := resolver.ResolveWhitelist(ctx, wl.EnabledPackages())
		if err != nil {
			logger.Printf("app resolver warning: %v", err)
		}
		uids = appresolver.UIDs(instances)
		if logger != nil {
			logger.Printf("whitelist mode: packages=%d instances=%d uids=%d", len(wl.EnabledPackages()), len(instances), len(uids))
		}
	}

	if err := writeResolvedUIDFile(cfg, nil, uids); err != nil && logger != nil {
		logger.Printf("write uid file warning: %v", err)
	}
	if len(uids) == 0 && logger != nil {
		logger.Printf("warning: no UIDs resolved for proxy")
	}
	fw := firewall.Manager{Config: cfg, Logger: logger}
	switch cfg.Capture.Mode {
	case "tproxy", "":
		return fw.ApplyTProxy(ctx, uids, hotspot.Detect(cfg.Hotspot))
	case "tun":
		return fmt.Errorf("tun firewall backend is planned but not enabled in this MVP")
	default:
		return fmt.Errorf("unknown capture mode: %s", cfg.Capture.Mode)
	}
}

func render(cfg config.Config) error {
	nodes, err := config.LoadXTunnelNodes(cfg.XTunnel.NodesFile, cfg.XTunnel)
	if err != nil {
		return err
	}
	return core.RenderSingBox(cfg, nodes)
}

func status(ctx context.Context, cfg config.Config) error {
	sup := supervisor.Supervisor{Config: cfg}
	pids, err := sup.ReadPids()
	if err != nil {
		return err
	}
	whitelist := resolveWhitelistForStatus(ctx, cfg)
	out := map[string]any{
		"capture_mode":                 cfg.Capture.Mode,
		"routing_mode":                 cfg.Routing.Mode,
		"pids":                         pids,
		"whitelist_count":              whitelist.Count,
		"whitelist_uid_file":           whitelist.UIDFile,
		"whitelist_uids":               whitelist.UIDs,
		"whitelist_resolved_instances": whitelist.Instances,
	}
	if whitelist.Error != "" {
		out["whitelist_resolve_error"] = whitelist.Error
	}
	return printJSON(out)
}

func listApps(ctx context.Context, cfg config.Config) error {
	resolver := appresolver.New(cfg.Whitelist.CloneUserIDs)
	apps, err := resolver.ListInstalled(ctx)
	if err != nil {
		return err
	}
	return printJSON(apps)
}

func newLogger(cfg config.Config) *log.Logger {
	_ = os.MkdirAll(cfg.Paths.LogDir, 0755)
	path := filepath.Join(cfg.Paths.LogDir, "proxyd.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return log.New(os.Stderr, "proxyd ", log.LstdFlags)
	}
	return log.New(file, "proxyd ", log.LstdFlags)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

type whitelistResolutionStatus struct {
	Count     int                       `json:"count"`
	UIDFile   string                    `json:"uid_file"`
	UIDs      []int                     `json:"uids"`
	Instances []whitelistInstanceStatus `json:"instances"`
	Error     string                    `json:"error,omitempty"`
}

type whitelistInstanceStatus struct {
	PackageName string `json:"package_name"`
	UserID      int    `json:"user_id"`
	UID         int    `json:"uid"`
	AppID       int    `json:"app_id"`
	Source      string `json:"source"`
}

func resolveWhitelistForStatus(ctx context.Context, cfg config.Config) whitelistResolutionStatus {
	wl, err := config.LoadWhitelist(cfg.Whitelist.File)
	if err != nil {
		return whitelistResolutionStatus{UIDFile: resolvedUIDFilePath(cfg), Error: err.Error()}
	}
	rules := wl.EnabledPackages()
	resolver := appresolver.New(cfg.Whitelist.CloneUserIDs)
	instances, err := resolver.ResolveWhitelist(ctx, rules)
	status := whitelistResolutionStatus{
		Count:     len(rules),
		UIDFile:   resolvedUIDFilePath(cfg),
		UIDs:      appresolver.UIDs(instances),
		Instances: summarizeResolvedWhitelist(instances),
	}
	if err != nil {
		status.Error = err.Error()
	}
	return status
}

func summarizeResolvedWhitelist(instances []appresolver.AppInstance) []whitelistInstanceStatus {
	out := make([]whitelistInstanceStatus, 0, len(instances))
	for _, item := range instances {
		out = append(out, whitelistInstanceStatus{
			PackageName: item.PackageName,
			UserID:      item.UserID,
			UID:         item.UID,
			AppID:       item.AppID,
			Source:      item.Source,
		})
	}
	return out
}

func resolvedUIDFilePath(cfg config.Config) string {
	return filepath.Join(cfg.Paths.RunDir, "whitelist.uids")
}

func writeResolvedUIDFile(cfg config.Config, instances []appresolver.AppInstance, uids []int) error {
	path := resolvedUIDFilePath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("# KSU Proxy resolved whitelist UIDs\n")
	b.WriteString("# generated by proxyd reconcile\n")
	b.WriteString("# package_name user_id uid app_id source\n")
	for _, item := range instances {
		fmt.Fprintf(
			&b,
			"%s user=%d uid=%d appid=%d source=%s\n",
			item.PackageName,
			item.UserID,
			item.UID,
			item.AppID,
			item.Source,
		)
	}
	if len(instances) == 0 {
		b.WriteString("# no resolved whitelist instances\n")
	}
	b.WriteString("# uid_list")
	for _, uid := range uids {
		fmt.Fprintf(&b, " %d", uid)
	}
	b.WriteByte('\n')

	return os.WriteFile(path, []byte(b.String()), 0644)
}

func logResolvedWhitelist(logger *log.Logger, instances []appresolver.AppInstance, uids []int) {
	if logger == nil {
		return
	}
	logger.Printf("whitelist resolved uid list=%v", uids)
	for _, item := range instances {
		logger.Printf(
			"whitelist instance package=%s user=%d uid=%d appid=%d source=%s",
			item.PackageName,
			item.UserID,
			item.UID,
			item.AppID,
			item.Source,
		)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "proxyd:", err)
	os.Exit(1)
}

type watchSet struct {
	mod map[string]time.Time
}

func newWatchSet(cfg config.Config) *watchSet {
	paths := []string{cfg.Whitelist.File, cfg.SingBox.BaseConfig, cfg.XTunnel.NodesFile}
	// Watch provider files from sing-box config
	for _, p := range loadProviderPaths(cfg) {
		paths = append(paths, p)
	}
	w := &watchSet{mod: make(map[string]time.Time)}
	for _, path := range paths {
		w.mod[path] = modTime(path)
	}
	return w
}

func loadProviderPaths(cfg config.Config) []string {
	raw, err := os.ReadFile(cfg.SingBox.BaseConfig)
	if err != nil {
		return nil
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil
	}
	providers, ok := root["providers"].([]any)
	if !ok {
		return nil
	}
	configDir := cfg.SingBox.ConfigDir
	var paths []string
	for _, p := range providers {
		m, ok := p.(map[string]any)
		if !ok {
			continue
		}
		rel, _ := m["path"].(string)
		if rel == "" {
			continue
		}
		// Resolve relative path
		if len(rel) > 2 && rel[:2] == "./" {
			rel = rel[2:]
		}
		abs := filepath.Join(configDir, rel)
		paths = append(paths, abs)
	}
	return paths
}

func (w *watchSet) changed(path string) bool {
	old := w.mod[path]
	now := modTime(path)
	w.mod[path] = now
	return !now.Equal(old)
}

func modTime(path string) time.Time {
	stat, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return stat.ModTime()
}
