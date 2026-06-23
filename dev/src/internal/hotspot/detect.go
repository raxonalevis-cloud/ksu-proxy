package hotspot

import (
	"net"
	"strings"

	"ksu-proxy/internal/config"
)

func Detect(cfg config.HotspotConfig) []string {
	if !cfg.Enabled {
		return nil
	}
	if !cfg.AutoDetect {
		return cfg.Interfaces
	}
	allow := make(map[string]bool)
	for _, name := range cfg.Interfaces {
		allow[name] = true
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return cfg.Interfaces
	}
	out := make([]string, 0)
	for _, iface := range ifaces {
		name := iface.Name
		if allow[name] || strings.HasPrefix(name, "ap") || strings.HasPrefix(name, "swlan") || strings.HasPrefix(name, "rndis") {
			if iface.Flags&net.FlagUp != 0 {
				out = append(out, name)
			}
		}
	}
	if len(out) == 0 {
		return cfg.Interfaces
	}
	return out
}

