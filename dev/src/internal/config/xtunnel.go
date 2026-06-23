package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

type XTunnelNode struct {
	Tag        string `json:"tag"`
	ListenPort int    `json:"listen_port"`
	Forward    string `json:"forward"`
	FrontIPs   string `json:"front_ips"`
	IP         string `json:"ip,omitempty"`
	Token      string `json:"token"`
	DNS        string `json:"dns"`
	ECH        string `json:"ech"`
	N          int    `json:"n"`
}

func LoadXTunnelNodes(path string, cfg XTunnelConfig) ([]XTunnelNode, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	defaultDNS := NormalizeXTunnelDNS(cfg.DefaultDNS)
	defaultECH := cfg.DefaultECH
	defaultN := cfg.DefaultN
	defaultFrontIPs := firstNonEmpty(cfg.DefaultFrontIPs, cfg.DefaultIP)
	defaultToken := cfg.DefaultToken
	varStyle := false
	vars := map[string]string{
		"default_dns":       defaultDNS,
		"default_ech":       defaultECH,
		"default_front_ip":  defaultFrontIPs,
		"default_front_ips": defaultFrontIPs,
		"default_token":     defaultToken,
		"default_parallel":  strconv.Itoa(defaultN),
	}
	nodes := make([]XTunnelNode, 0)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "@") {
			varStyle = true
			key, val, ok := strings.Cut(strings.TrimPrefix(line, "@"), "=")
			if !ok || !isIdent(key) {
				continue
			}
			switch key {
			case "default_dns":
				defaultDNS = NormalizeXTunnelDNS(val)
				vars[key] = defaultDNS
			case "default_ech":
				defaultECH = strings.TrimSpace(val)
				vars[key] = defaultECH
			case "default_parallel":
				if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil && n > 0 {
					defaultN = n
				}
				vars[key] = strconv.Itoa(defaultN)
			case "default_front_ip", "default_front_ips":
				defaultFrontIPs = normalizeCSV(val)
				vars["default_front_ip"] = defaultFrontIPs
				vars["default_front_ips"] = defaultFrontIPs
			case "default_token":
				defaultToken = strings.TrimSpace(val)
				vars[key] = defaultToken
			default:
				vars[key] = strings.TrimSpace(val)
			}
			continue
		}
		if key, val, ok := strings.Cut(line, "="); ok && isIdent(key) {
			switch key {
			case "dns":
				defaultDNS = NormalizeXTunnelDNS(val)
			case "ech":
				defaultECH = strings.TrimSpace(val)
			case "n":
				if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil && n > 0 {
					defaultN = n
				}
			case "ip", "front_ip", "front_ips":
				defaultFrontIPs = normalizeCSV(val)
			case "token":
				defaultToken = strings.TrimSpace(val)
			}
			vars["default_dns"] = defaultDNS
			vars["default_ech"] = defaultECH
			vars["default_parallel"] = strconv.Itoa(defaultN)
			vars["default_front_ip"] = defaultFrontIPs
			vars["default_front_ips"] = defaultFrontIPs
			vars["default_token"] = defaultToken
			continue
		}

		line = strings.ReplaceAll(line, ";", "|")
		parts := strings.Split(line, "|")
		for len(parts) < 8 {
			parts = append(parts, "")
		}
		port, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || port <= 0 {
			continue
		}
		forward := strings.TrimSpace(parts[2])
		if forward == "" {
			continue
		}
		if !strings.Contains(forward, "://") {
			forward = "wss://" + forward + ":443"
		}
		node := XTunnelNode{Tag: strings.TrimSpace(parts[0]), ListenPort: port, Forward: forward}
		if varStyle {
			node.Token = firstNonEmpty(resolveVar(parts[3], vars), defaultToken)
			node.DNS = firstNonEmpty(resolveVar(parts[4], vars), defaultDNS)
			node.ECH = firstNonEmpty(resolveVar(parts[5], vars), defaultECH)
			node.FrontIPs = firstNonEmpty(resolveVar(parts[6], vars), defaultFrontIPs)
			node.N = parsePositiveInt(firstNonEmpty(resolveVar(parts[7], vars), strconv.Itoa(defaultN)), defaultN)
		} else {
			node.FrontIPs = firstNonEmpty(parts[3], defaultFrontIPs)
			node.Token = firstNonEmpty(parts[4], defaultToken)
			node.DNS = firstNonEmpty(parts[5], defaultDNS)
			node.ECH = firstNonEmpty(parts[6], defaultECH)
			node.N = parsePositiveInt(parts[7], defaultN)
		}
		node.FrontIPs = normalizeCSV(node.FrontIPs)
		node.IP = node.FrontIPs
		node.DNS = NormalizeXTunnelDNS(node.DNS)
		if node.Tag == "" {
			node.Tag = "x-tunnel-" + strconv.Itoa(port)
		}
		nodes = append(nodes, node)
	}
	return nodes, scanner.Err()
}

func NormalizeXTunnelDNS(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "://") {
		return value
	}
	if strings.HasSuffix(strings.ToLower(value), "/dns-query") {
		return "https://" + value
	}
	return value
}

func resolveVar(value string, vars map[string]string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "@") {
		if resolved, ok := vars[strings.TrimPrefix(value, "@")]; ok {
			return resolved
		}
	}
	return value
}

func parsePositiveInt(value string, fallback int) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func firstNonEmpty(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}

func normalizeCSV(value string) string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, ",")
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
