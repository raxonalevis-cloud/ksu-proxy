package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"ksu-proxy/internal/config"
)

func RenderSingBox(cfg config.Config, nodes []config.XTunnelNode) error {
	raw, err := os.ReadFile(cfg.SingBox.BaseConfig)
	if err != nil {
		return err
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("parse sing-box base config: %w", err)
	}

	root["inbounds"] = buildInbounds(cfg)
	root["route"] = patchRoute(root["route"], root["outbounds"], cfg)
	root["experimental"] = patchExperimental(root["experimental"], cfg)

	if cfg.XTunnel.Enabled {
		root["outbounds"] = mergeXTunnelOutbounds(root["outbounds"], cfg.XTunnel.SelectorTag, nodes)
	}

	if err := os.MkdirAll(filepath.Dir(cfg.SingBox.RuntimeConfig), 0755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(cfg.SingBox.RuntimeConfig, out, 0644)
}

func patchRoute(raw any, outbounds any, cfg config.Config) map[string]any {
	route, _ := raw.(map[string]any)
	if route == nil {
		route = make(map[string]any)
	}
	switch cfg.Routing.Mode {
	case "global":
		route["rules"] = essentialRouteRules(route["rules"])
		route["final"] = preferredProxyTag(outbounds, cfg.XTunnel.SelectorTag)
	case "direct":
		route["rules"] = essentialRouteRules(route["rules"])
		route["final"] = directTag(outbounds)
	}
	route["auto_detect_interface"] = true
	return route
}

func essentialRouteRules(raw any) []any {
	rules, _ := raw.([]any)
	out := make([]any, 0, 2)
	for _, item := range rules {
		rule, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if action, _ := rule["action"].(string); action == "sniff" {
			out = append(out, item)
			continue
		}
		if protocol, _ := rule["protocol"].(string); protocol == "dns" {
			if action, _ := rule["action"].(string); action == "hijack-dns" {
				out = append(out, item)
			}
		}
	}
	if len(out) == 0 {
		out = append(out, map[string]any{"action": "sniff"})
		out = append(out, map[string]any{"protocol": "dns", "action": "hijack-dns"})
	}
	return out
}

func buildInbounds(cfg config.Config) []map[string]any {
	switch cfg.Capture.Mode {
	case "tun":
		return []map[string]any{
			{
				"type":                     "tun",
				"tag":                      "tun-in",
				"interface_name":           cfg.SingBox.TunInterface,
				"address":                  compactStrings(cfg.SingBox.TunAddress4, cfg.SingBox.TunAddress6),
				"mtu":                      1400,
				"auto_route":               false,
				"strict_route":             true,
				"endpoint_independent_nat": true,
				"stack":                    "system",
			},
		}
	default:
		return []map[string]any{
			{
				"type":        "tproxy",
				"tag":         "tproxy-in",
				"listen":      cfg.SingBox.TProxyListen,
				"listen_port": cfg.SingBox.TProxyPort,
			},
		}
	}
}

func patchExperimental(raw any, cfg config.Config) map[string]any {
	exp, _ := raw.(map[string]any)
	if exp == nil {
		exp = make(map[string]any)
	}
	cache, _ := exp["cache_file"].(map[string]any)
	if cache == nil {
		cache = make(map[string]any)
	}
	cache["enabled"] = true
	exp["cache_file"] = cache

	clash, _ := exp["clash_api"].(map[string]any)
	if clash == nil {
		clash = make(map[string]any)
	}
	clash["external_controller"] = cfg.SingBox.ClashAPIListen
	if cfg.SingBox.ClashAPISecret != "" {
		clash["secret"] = cfg.SingBox.ClashAPISecret
	}
	if cfg.SingBox.ExternalUI != "" {
		clash["external_ui"] = cfg.SingBox.ExternalUI
	}
	if cfg.SingBox.ExternalUIDownload != "" {
		clash["external_ui_download_url"] = cfg.SingBox.ExternalUIDownload
	}
	if cfg.SingBox.ExternalUIDetour != "" {
		clash["external_ui_download_detour"] = cfg.SingBox.ExternalUIDetour
	}
	exp["clash_api"] = clash
	return exp
}

func mergeXTunnelOutbounds(raw any, selectorTag string, nodes []config.XTunnelNode) []any {
	existing, _ := raw.([]any)
	fallbackDirect := directTag(existing)
	remove := make(map[string]bool)
	remove[selectorTag] = true
	for _, node := range nodes {
		remove[node.Tag] = true
	}

	out := make([]any, 0, len(existing)+len(nodes)+1)

	// Collect all selector outbounds for updating
	var selectors []*map[string]any
	for _, item := range existing {
		m, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		tag, _ := m["tag"].(string)

		// Remove old x-tunnel selector and nodes
		if remove[tag] || (selectorTag != "" && tag != selectorTag && hasTagPrefix(tag, selectorTag)) {
			continue
		}

		// Collect all selectors for x-tunnel injection
		if typ, _ := m["type"].(string); typ == "selector" {
			selectors = append(selectors, &m)
			continue
		}

		out = append(out, item)
	}

	// Create 🚀代理 selector if not found
	hasProxy := false
	for _, sel := range selectors {
		tag, _ := (*sel)["tag"].(string)
		if tag == "🚀代理" {
			hasProxy = true
			break
		}
	}
	if !hasProxy {
		proxySelector := map[string]any{
			"type":                        "selector",
			"tag":                         "🚀代理",
			"use_all_providers":           true,
			"outbounds":                   []string{fallbackDirect},
			"default":                     fallbackDirect,
			"interrupt_exist_connections": true,
		}
		selectors = append([]*map[string]any{&proxySelector}, selectors...)
	}

	// Add SOCKS outbound definitions for x-tunnel nodes
	if len(nodes) > 0 {
		for _, node := range nodes {
			out = append(out, map[string]any{
				"type":        "socks",
				"tag":         node.Tag,
				"server":      "127.0.0.1",
				"server_port": node.ListenPort,
			})
		}
	}

	// Update all selectors with x-tunnel nodes
	for _, sel := range selectors {
		currentOutbounds, _ := (*sel)["outbounds"].([]any)
		newOutbounds := make([]string, 0)

		// Keep existing non-x-tunnel outbounds
		for _, ob := range currentOutbounds {
			if s, ok := ob.(string); ok && s != selectorTag && !isXTunnelNode(s, nodes) {
				newOutbounds = append(newOutbounds, s)
			}
		}

		if len(nodes) > 0 {
			for _, node := range nodes {
				newOutbounds = append(newOutbounds, node.Tag)
			}
			(*sel)["outbounds"] = newOutbounds
			(*sel)["default"] = nodes[0].Tag
		} else {
			(*sel)["outbounds"] = []string{fallbackDirect}
			(*sel)["default"] = fallbackDirect
		}

		out = append(out, *sel)
	}

	return out
}

// isXTunnelNode checks if tag is an x-tunnel node
func isXTunnelNode(tag string, nodes []config.XTunnelNode) bool {
	for _, node := range nodes {
		if node.Tag == tag {
			return true
		}
	}
	return false
}

func directTag(raw any) string {
	outbounds, _ := raw.([]any)
	for _, item := range outbounds {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ != "direct" {
			continue
		}
		if tag, _ := m["tag"].(string); tag != "" {
			return tag
		}
	}
	return "direct"
}

func preferredProxyTag(raw any, fallback string) string {
	outbounds, _ := raw.([]any)
	for _, preferred := range []string{"proxy", "\U0001f680\u4ee3\u7406", fallback} {
		for _, item := range outbounds {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if tag, _ := m["tag"].(string); tag == preferred && tag != "" {
				return tag
			}
		}
	}
	if fallback != "" {
		return fallback
	}
	return "proxy"
}

func hasTagPrefix(tag string, prefix string) bool {
	if len(tag) <= len(prefix) {
		return false
	}
	return tag[:len(prefix)] == prefix
}

func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
