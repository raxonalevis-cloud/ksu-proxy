package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ksu-proxy/internal/appresolver"
	"ksu-proxy/internal/config"
	"ksu-proxy/internal/supervisor"
)

var (
	reconcileMu    sync.Mutex
	packageNameRe  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.]*$`)
	errBadRequest  = fmt.Errorf("bad request")
	errEmptyConfig = fmt.Errorf("admin api listen address is empty")
)

type appSummary struct {
	PackageName string `json:"package_name"`
	DisplayName string `json:"display_name"`
	Label       string `json:"label,omitempty"`
	AvatarText  string `json:"avatar_text"`
	Installed   bool   `json:"installed"`
	Enabled     bool   `json:"enabled"`
	Scope       string `json:"scope"`
	UserIDs     []int  `json:"user_ids"`
	UIDs        []int  `json:"uids"`
}

type whitelistUpdateRequest struct {
	Packages []string             `json:"packages"`
	Rules    []config.PackageRule `json:"rules"`
}

func startAdminServer(ctx context.Context, cfg config.Config, logger *log.Logger) (*http.Server, error) {
	if cfg.Runtime.AdminAPIListen == "" {
		return nil, errEmptyConfig
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handleAPIStatus(ctx, w, cfg)
	})
	mux.HandleFunc("/api/apps", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handleAPIApps(ctx, w, r, cfg)
	})
	mux.HandleFunc("/api/yacd", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handleAPIYacdStatus(ctx, w, cfg)
	})
	mux.HandleFunc("/api/whitelist", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleAPIWhitelist(w, cfg)
		case http.MethodPost:
			handleAPIWhitelistUpdate(ctx, w, r, cfg, logger)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handleAPILogs(w, r, cfg)
	})
mux.HandleFunc("/api/validate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handleAPIValidate(ctx, w, cfg)
	})
mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleAPIConfigGet(w, r, cfg)
		case http.MethodPost:
			handleAPIConfigSave(ctx, w, r, cfg)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
	mux.HandleFunc("/api/restart", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handleAPIRestart(ctx, w, cfg, logger)
	})

	listener, err := net.Listen("tcp", cfg.Runtime.AdminAPIListen)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: withCORS(mux)}
	go func() {
		if logger != nil {
			logger.Printf("admin api listening on %s", cfg.Runtime.AdminAPIListen)
		}
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed && logger != nil {
			logger.Printf("admin api stopped: %v", err)
		}
	}()
	return server, nil
}

func handleAPIStatus(ctx context.Context, w http.ResponseWriter, cfg config.Config) {
	sup := supervisor.Supervisor{Config: cfg}
	pids, err := sup.ReadPids()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	whitelist := cachedWhitelistForStatus(cfg)
	payload := map[string]any{
		"capture_mode":                 cfg.Capture.Mode,
		"routing_mode":                 cfg.Routing.Mode,
		"admin_api_listen":             cfg.Runtime.AdminAPIListen,
		"clash_api_listen":             cfg.SingBox.ClashAPIListen,
		"whitelist_count":              whitelist.Count,
		"whitelist_uid_file":           whitelist.UIDFile,
		"whitelist_uids":               whitelist.UIDs,
		"whitelist_resolved_instances": whitelist.Instances,
		"module_disabled":              moduleDisabled(cfg),
		"module_disabled_reason":       moduleDisabledReason(cfg),
		"pids":                         pids,
		"whitelist_file":               cfg.Whitelist.File,
		"sing_box_ui":                  singBoxUIURL(cfg),
		"poll_interval_sec":            cfg.Runtime.PollIntervalSeconds,
	}
	writeJSON(w, payload)
}

func cachedWhitelistForStatus(cfg config.Config) whitelistResolutionStatus {
	status := whitelistResolutionStatus{
		UIDFile: resolvedUIDFilePath(cfg),
	}
	wl, err := config.LoadWhitelist(cfg.Whitelist.File)
	if err == nil {
		status.Count = len(wl.EnabledPackages())
	}
	file, err := os.Open(status.UIDFile)
	if err != nil {
		return status
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			for _, uid := range parseCachedUIDList(line) {
				status.UIDs = appendUniqueInt(status.UIDs, uid)
			}
			continue
		}
		if item, ok := parseCachedWhitelistInstance(line); ok {
			status.Instances = append(status.Instances, item)
			status.UIDs = appendUniqueInt(status.UIDs, item.UID)
		}
	}
	sort.Ints(status.UIDs)
	return status
}

func parseCachedUIDList(line string) []int {
	if !strings.HasPrefix(line, "# uid_list") {
		return nil
	}
	fields := strings.Fields(strings.TrimPrefix(line, "# uid_list"))
	out := make([]int, 0, len(fields))
	for _, field := range fields {
		var uid int
		if _, err := fmt.Sscanf(field, "%d", &uid); err == nil && uid > 0 {
			out = appendUniqueInt(out, uid)
		}
	}
	return out
}

func parseCachedWhitelistInstance(line string) (whitelistInstanceStatus, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return whitelistInstanceStatus{}, false
	}
	item := whitelistInstanceStatus{PackageName: fields[0]}
	for _, field := range fields[1:] {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "user":
			_, _ = fmt.Sscanf(value, "%d", &item.UserID)
		case "uid":
			_, _ = fmt.Sscanf(value, "%d", &item.UID)
		case "appid":
			_, _ = fmt.Sscanf(value, "%d", &item.AppID)
		case "source":
			item.Source = value
		}
	}
	return item, item.PackageName != "" && item.UID > 0
}

func handleAPIApps(ctx context.Context, w http.ResponseWriter, r *http.Request, cfg config.Config) {
	wl, err := config.LoadWhitelist(cfg.Whitelist.File)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rules := make(map[string]config.PackageRule)
	for _, item := range wl.Packages {
		if item.PackageName != "" {
			rules[item.PackageName] = item
		}
	}

	resolver := appresolver.New(cfg.Whitelist.CloneUserIDs)
	withLabels := wantsAppLabels(r)
	instances, err := resolver.ListInstalledWithOptions(ctx, appresolver.ListOptions{ResolveLabels: withLabels})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	byPackage := make(map[string]*appSummary)
	for _, item := range instances {
		entry := byPackage[item.PackageName]
		if entry == nil {
			rule := rules[item.PackageName]
			displayName := appresolver.DisplayName(item.PackageName, item.Label)
			entry = &appSummary{
				PackageName: item.PackageName,
				DisplayName: displayName,
				Label:       item.Label,
				AvatarText:  appresolver.AvatarText(displayName, item.PackageName),
				Installed:   true,
				Enabled:     ruleEnabled(rule),
				Scope:       ruleScope(rule),
			}
			byPackage[item.PackageName] = entry
		} else if entry.Label == "" && item.Label != "" {
			entry.Label = item.Label
			entry.DisplayName = appresolver.DisplayName(item.PackageName, item.Label)
			entry.AvatarText = appresolver.AvatarText(entry.DisplayName, item.PackageName)
		}
		entry.UserIDs = appendUniqueInt(entry.UserIDs, item.UserID)
		entry.UIDs = appendUniqueInt(entry.UIDs, item.UID)
	}
	for pkg, rule := range rules {
		if _, ok := byPackage[pkg]; ok {
			continue
		}
		displayName := appresolver.DisplayName(pkg, "")
		byPackage[pkg] = &appSummary{
			PackageName: pkg,
			DisplayName: displayName,
			AvatarText:  appresolver.AvatarText(displayName, pkg),
			Installed:   false,
			Enabled:     ruleEnabled(rule),
			Scope:       ruleScope(rule),
		}
	}
	for _, item := range cachedWhitelistForStatus(cfg).Instances {
		rule, ok := rules[item.PackageName]
		if !ok || !ruleEnabled(rule) {
			continue
		}
		entry := byPackage[item.PackageName]
		if entry == nil {
			displayName := appresolver.DisplayName(item.PackageName, "")
			entry = &appSummary{
				PackageName: item.PackageName,
				DisplayName: displayName,
				AvatarText:  appresolver.AvatarText(displayName, item.PackageName),
				Installed:   true,
				Enabled:     true,
				Scope:       ruleScope(rule),
			}
			byPackage[item.PackageName] = entry
		}
		entry.UserIDs = appendUniqueInt(entry.UserIDs, item.UserID)
		entry.UIDs = appendUniqueInt(entry.UIDs, item.UID)
	}

	out := make([]appSummary, 0, len(byPackage))
	for _, item := range byPackage {
		sort.Ints(item.UserIDs)
		sort.Ints(item.UIDs)
		out = append(out, *item)
	}
	sortAppSummaries(out)
	writeJSON(w, map[string]any{"apps": out, "labels": withLabels})
}

func wantsAppLabels(r *http.Request) bool {
	value := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("labels")))
	return value == "1" || value == "true" || value == "yes"
}

func sortAppSummaries(items []appSummary) {
	sort.Slice(items, func(i, j int) bool {
		leftRank := appSortRank(items[i])
		rightRank := appSortRank(items[j])
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		leftName := strings.ToLower(items[i].DisplayName)
		rightName := strings.ToLower(items[j].DisplayName)
		if leftName != rightName {
			return leftName < rightName
		}
		return items[i].PackageName < items[j].PackageName
	})
}

func appSortRank(item appSummary) int {
	if item.Enabled && item.Installed {
		return 0
	}
	if item.Enabled {
		return 1
	}
	return 2
}

func handleAPIYacdStatus(ctx context.Context, w http.ResponseWriter, cfg config.Config) {
	uiURL := singBoxUIURL(cfg)
	payload := map[string]any{
		"url":        uiURL,
		"accessible": false,
	}
	if cfg.SingBox.ClashAPIListen == "" {
		payload["error"] = "clash api listen address is empty"
		writeJSON(w, payload)
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	transport := &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 1500 * time.Millisecond,
		}).DialContext,
	}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: 1500 * time.Millisecond}
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, uiURL, nil)
	if err != nil {
		payload["error"] = err.Error()
		writeJSON(w, payload)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		payload["error"] = err.Error()
		writeJSON(w, payload)
		return
	}
	defer resp.Body.Close()
	payload["status_code"] = resp.StatusCode
	payload["accessible"] = resp.StatusCode >= 200 && resp.StatusCode < 400
	writeJSON(w, payload)
}

func singBoxUIURL(cfg config.Config) string {
	base := strings.TrimSpace(cfg.SingBox.ClashAPIListen)
	if base == "" {
		return ""
	}
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	base = strings.TrimRight(base, "/")
	return base + "/ui/"
}

func handleAPIWhitelist(w http.ResponseWriter, cfg config.Config) {
	wl, err := config.LoadWhitelist(cfg.Whitelist.File)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, wl)
}

func handleAPIWhitelistUpdate(ctx context.Context, w http.ResponseWriter, r *http.Request, cfg config.Config, logger *log.Logger) {
	defer r.Body.Close()
	var req whitelistUpdateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errBadRequest.Error())
		return
	}

	rules, err := whitelistRulesFromRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	wl := config.WhitelistFile{Packages: rules}
	if err := config.SaveWhitelist(cfg.Whitelist.File, wl); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := reconcile(ctx, cfg, logger); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "whitelist_count": len(rules)})
}

func whitelistRulesFromRequest(req whitelistUpdateRequest) ([]config.PackageRule, error) {
	raw := req.Packages
	if len(req.Rules) > 0 {
		raw = raw[:0]
		for _, rule := range req.Rules {
			if rule.Enabled != nil && !*rule.Enabled {
				continue
			}
			raw = append(raw, rule.PackageName)
		}
	}
	seen := make(map[string]bool)
	out := make([]config.PackageRule, 0, len(raw))
	for _, pkg := range raw {
		if pkg == "" || seen[pkg] {
			continue
		}
		if !packageNameRe.MatchString(pkg) {
			return nil, fmt.Errorf("invalid package name: %s", pkg)
		}
		seen[pkg] = true
		out = append(out, config.PackageRule{PackageName: pkg, Scope: "all_instances"})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PackageName < out[j].PackageName
	})
	return out, nil
}

func moduleDisabled(cfg config.Config) bool {
	return moduleDisabledReason(cfg) != ""
}

func moduleDisabledReason(cfg config.Config) string {
	if cfg.Runtime.ModuleDir == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(cfg.Runtime.ModuleDir, "remove")); err == nil {
		return "module remove marker"
	}
	if _, err := os.Stat(filepath.Join(cfg.Runtime.ModuleDir, "disable")); err == nil {
		return "module disable marker"
	}
	return ""
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": message})
}

func handleAPILogs(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	logDir := cfg.Paths.LogDir
	if file := r.URL.Query().Get("file"); file != "" {
		clean := filepath.Clean(file)
		if strings.Contains(clean, "..") {
			writeError(w, http.StatusForbidden, "invalid file path")
			return
		}
		filePath := filepath.Join(logDir, clean)
		if !strings.HasPrefix(filePath, filepath.Clean(logDir)+string(os.PathSeparator)) && filePath != filepath.Clean(logDir) {
			writeError(w, http.StatusForbidden, "path traversal denied")
			return
		}
		lines := 200
		if l := r.URL.Query().Get("lines"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 2000 {
				lines = n
			}
		}
		content, err := readLogTail(filePath, lines)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, map[string]any{"file": clean, "content": content})
		return
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	files := make([]map[string]any, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, map[string]any{
			"name":     entry.Name(),
			"size":     info.Size(),
			"mod_time": info.ModTime().Unix(),
		})
	}
	writeJSON(w, map[string]any{"log_dir": logDir, "files": files})
}

func readLogTail(filePath string, lines int) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*64)
	scanner.Buffer(buf, 1024*64)
	ring := make([]string, 0, lines)
	for scanner.Scan() {
		ring = append(ring, scanner.Text())
		if len(ring) > lines {
			ring = ring[1:]
		}
	}
	return strings.Join(ring, "\n"), scanner.Err()
}

func handleAPIValidate(ctx context.Context, w http.ResponseWriter, cfg config.Config) {
	if !cfg.SingBox.ValidateBeforeRun {
		writeJSON(w, map[string]any{"ok": true, "message": "validation disabled in config"})
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, cfg.SingBox.Binary, "check", "-c", cfg.SingBox.RuntimeConfig, "-D", cfg.SingBox.ConfigDir)
	out, err := cmd.CombinedOutput()
	output := string(out)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "output": output})
		return
	}
	if output == "" {
		output = "config OK"
	}
	writeJSON(w, map[string]any{"ok": true, "output": output})
}

func ruleEnabled(rule config.PackageRule) bool {
	if rule.PackageName == "" {
		return false
	}
	return rule.Enabled == nil || *rule.Enabled
}

func ruleScope(rule config.PackageRule) string {
	if rule.Scope == "" {
		return "all_instances"
	}
	return rule.Scope
}

func appendUniqueInt(values []int, value int) []int {
	for _, item := range values {
		if item == value {
			return values
		}
	}
	return append(values, value)
}
func configDir(cfg config.Config) string {
	return filepath.Join(cfg.DataDir, "config")
}

var allowedConfigFiles = []string{
	"sing-box/config.json",
	"sing-box/config.example.jsonc",
	"x-tunnel/nodes.list",
	"whitelist/packages.json",
	"default-config.json",
	"config.example.jsonc",
}

func isAllowedConfigFile(relPath string) bool {
	clean := filepath.ToSlash(filepath.Clean(relPath))
	for _, allow := range allowedConfigFiles {
		if clean == filepath.ToSlash(allow) {
			return true
		}
	}
	return false
}

func handleAPIConfigGet(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	baseDir := configDir(cfg)
	if file := r.URL.Query().Get("file"); file != "" {
		clean := filepath.Clean(file)
		if strings.Contains(clean, "..") || !isAllowedConfigFile(clean) {
			writeError(w, http.StatusForbidden, "file not allowed")
			return
		}
		filePath := filepath.Join(baseDir, clean)
		if !strings.HasPrefix(filePath, filepath.Clean(baseDir)+string(os.PathSeparator)) {
			writeError(w, http.StatusForbidden, "path traversal denied")
			return
		}
		content, err := os.ReadFile(filePath)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		info, _ := os.Stat(filePath)
		writeJSON(w, map[string]any{
			"file":    clean,
			"content": string(content),
			"size":    len(content),
			"mod_time": info.ModTime().Unix(),
		})
		return
	}
	files := make([]map[string]any, 0)
	for _, rel := range allowedConfigFiles {
		filePath := filepath.Join(baseDir, rel)
		info, err := os.Stat(filePath)
		if err != nil {
			continue
		}
		files = append(files, map[string]any{
			"name":     rel,
			"size":     info.Size(),
			"mod_time": info.ModTime().Unix(),
		})
	}
	writeJSON(w, map[string]any{"config_dir": baseDir, "files": files})
}

func handleAPIConfigSave(ctx context.Context, w http.ResponseWriter, r *http.Request, cfg config.Config) {
	defer r.Body.Close()
	var req struct {
		File    string `json:"file"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	clean := filepath.Clean(req.File)
	if strings.Contains(clean, "..") || !isAllowedConfigFile(clean) {
		writeError(w, http.StatusForbidden, "file not allowed")
		return
	}
	baseDir := configDir(cfg)
	filePath := filepath.Join(baseDir, clean)
	if !strings.HasPrefix(filePath, filepath.Clean(baseDir)+string(os.PathSeparator)) {
		writeError(w, http.StatusForbidden, "path traversal denied")
		return
	}
	oldContent, err := os.ReadFile(filePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot read original file")
		return
	}
	bakPath := filePath + ".edit.bak"
	_ = os.WriteFile(bakPath, oldContent, 0644)
	if err := os.WriteFile(filePath, []byte(req.Content), 0644); err != nil {
		_ = os.Remove(bakPath)
		writeError(w, http.StatusInternalServerError, "write failed: "+err.Error())
		return
	}
	// For sing-box config, run validation against the base config the user just edited
	if strings.HasPrefix(clean, "sing-box/") && strings.HasSuffix(clean, ".json") && cfg.SingBox.ValidateBeforeRun {
		checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(checkCtx, cfg.SingBox.Binary, "check", "-c", filePath, "-D", cfg.SingBox.ConfigDir)
		out, err := cmd.CombinedOutput()
		output := string(out)
		if err != nil {
			// Validation failed — rollback
			_ = os.WriteFile(filePath, oldContent, 0644)
			_ = os.Remove(bakPath)
			writeJSON(w, map[string]any{
				"ok":          true,
				"validated":   false,
				"error":       err.Error(),
				"output":      output,
				"rolled_back": true,
				"message":     "配置未通过 sing-box 验证，已自动回滚。请修正后重试。",
			})
			return
		}
	}
	_ = os.Remove(bakPath)
	msg := "配置已保存"
	if strings.HasPrefix(clean, "sing-box/") {
		msg += "（sing-box 将在检测到变化后自动重启）"
	}
	writeJSON(w, map[string]any{"ok": true, "validated": true, "message": msg})
}

func handleAPIRestart(ctx context.Context, w http.ResponseWriter, cfg config.Config, logger *log.Logger) {
	if logger != nil {
		logger.Printf("admin api: restarting sing-box")
	}
	_ = stop(ctx, cfg, logger)
	if err := start(ctx, cfg, logger); err != nil {
		writeError(w, http.StatusInternalServerError, "restart failed: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "message": "sing-box 已重启"})
}