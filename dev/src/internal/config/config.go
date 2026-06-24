package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const DefaultDataDir = "/data/adb/ksu-proxy"

type Config struct {
	Version   int             `json:"version"`
	DataDir   string          `json:"data_dir"`
	Paths     PathsConfig     `json:"paths"`
	Capture   CaptureConfig   `json:"capture"`
	Routing   RoutingConfig   `json:"routing"`
	SingBox   SingBoxConfig   `json:"sing_box"`
	XTunnel   XTunnelConfig   `json:"x_tunnel"`
	Whitelist WhitelistConfig `json:"whitelist"`
	Hotspot   HotspotConfig   `json:"hotspot"`
	Firewall  FirewallConfig  `json:"firewall"`
	Runtime   RuntimeConfig   `json:"runtime"`
	Update    UpdateConfig    `json:"update"`
}

type PathsConfig struct {
	WorkDir    string `json:"work_dir"`
	RuntimeDir string `json:"runtime_dir"`
	LogDir     string `json:"log_dir"`
	RunDir     string `json:"run_dir"`
}

type CaptureConfig struct {
	Mode string `json:"mode"`
}

type RoutingConfig struct {
	Mode string `json:"mode"`
}

type SingBoxConfig struct {
	Enabled            bool   `json:"enabled"`
	Binary             string `json:"binary"`
	BaseConfig         string `json:"base_config"`
	RuntimeConfig      string `json:"runtime_config"`
	ConfigDir          string `json:"config_dir"`
	TProxyListen       string `json:"tproxy_listen"`
	TProxyPort         int    `json:"tproxy_port"`
	TunInterface       string `json:"tun_interface"`
	TunAddress4        string `json:"tun_address4"`
	TunAddress6        string `json:"tun_address6"`
	ClashAPIListen     string `json:"clash_api_listen"`
	ClashAPISecret     string `json:"clash_api_secret"`
	ExternalUI         string `json:"external_ui"`
	ExternalUIDownload string `json:"external_ui_download_url"`
	ExternalUIDetour   string `json:"external_ui_download_detour"`
	ValidateBeforeRun  bool   `json:"validate_before_run"`
	RestartOnConfigMod bool   `json:"restart_on_config_mod"`
}

type XTunnelConfig struct {
	Enabled         bool   `json:"enabled"`
	Binary          string `json:"binary"`
	NodesFile       string `json:"nodes_file"`
	SelectorTag     string `json:"selector_tag"`
	DefaultDNS      string `json:"default_dns"`
	DefaultECH      string `json:"default_ech"`
	DefaultN        int    `json:"default_n"`
	DefaultFrontIPs string `json:"default_front_ips"`
	DefaultIP       string `json:"default_ip"`
	DefaultToken    string `json:"default_token"`
}

type WhitelistConfig struct {
	File         string `json:"file"`
	Mode         string `json:"mode"`  // whitelist, blacklist, global
	CloneUserIDs []int  `json:"clone_user_ids"`
}

type HotspotConfig struct {
	Enabled        bool     `json:"enabled"`
	AutoDetect     bool     `json:"auto_detect"`
	Interfaces     []string `json:"interfaces"`
	ClientMode     string   `json:"client_mode"`
	ClientAllowMAC []string `json:"client_allow_mac"`
}

type FirewallConfig struct {
	Backend      string   `json:"backend"`
	ChainPrefix  string   `json:"chain_prefix"`
	Mark         string   `json:"mark"`
	Table        int      `json:"table"`
	RulePriority int      `json:"rule_priority"`
	CoreUIDs     []int    `json:"core_uids"`
	BypassIPv4   []string `json:"bypass_ipv4"`
	BypassIPv6   []string `json:"bypass_ipv6"`
	DisableQUIC  bool     `json:"disable_quic"`
	DryRun       bool     `json:"dry_run"`
	BlockLoopback bool    `json:"block_loopback"`
	IPv6Mode     string   `json:"ipv6_mode"`
	DNSRoute     bool     `json:"dns_route"`
}

type RuntimeConfig struct {
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	StartGraceMillis    int    `json:"start_grace_millis"`
	AdminAPIListen      string `json:"admin_api_listen"`
	ModuleDir           string `json:"module_dir"`
	ScheduledRestart    bool   `json:"scheduled_restart"`
}

type UpdateConfig struct {
	Enabled           bool   `json:"enabled"`
	Repo              string `json:"repo"`
	CheckIntervalHours int   `json:"check_interval_hours"`
}

func Load(path string) (*Config, error) {
	if path == "" {
		path = filepath.Join(DefaultDataDir, "config", "config.json")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Default()
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	cfg.Normalize()
	return &cfg, nil
}

func Default() Config {
	cfg := Config{
		Version: 1,
		DataDir: DefaultDataDir,
		Capture: CaptureConfig{Mode: "tproxy"},
		Routing: CaptureConfigToRouting("rule"),
		SingBox: SingBoxConfig{
			Enabled:            true,
			TProxyListen:       "::",
			TProxyPort:         2025,
			TunInterface:       "ksu0",
			TunAddress4:        "172.18.0.1/30",
			TunAddress6:        "fdfe:dcba:9876::1/126",
			ClashAPIListen:     "127.0.0.1:9090",
			ExternalUI:         filepath.Join(DefaultDataDir, "runtime", "yacd"),
			ExternalUIDownload: "https://srs.acstudycn.eu.org/gh-pages.zip",
			ExternalUIDetour:   "out-direct",
			ValidateBeforeRun:  true,
			RestartOnConfigMod: true,
		},
		XTunnel: XTunnelConfig{
			Enabled:         true,
			SelectorTag:     "x-tunnel",
			DefaultDNS:      "https://223.5.5.5/dns-query",
			DefaultECH:      "cloudflare-ech.com",
			DefaultN:        3,
			DefaultFrontIPs: "173.245.59.112,104.17.127.226",
		},
		Whitelist: WhitelistConfig{
			Mode:         "package_all_instances",
			CloneUserIDs: []int{999},
		},
		Hotspot: HotspotConfig{
			Enabled:    false,
			AutoDetect: true,
			Interfaces: []string{"ap0", "wlan1", "swlan0", "rndis0", "bt-pan"},
			ClientMode: "all",
		},
		Firewall: FirewallConfig{
			Backend:      "auto",
			ChainPrefix:  "KSP",
			Mark:         "0x12000000/0xff000000",
			Table:        2025,
			RulePriority: 1000,
			CoreUIDs:     []int{0, 1000, 2000},
			BypassIPv4: []string{
				"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
				"169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/3",
			},
			BypassIPv6: []string{"::/127", "fc00::/7", "fe80::/10", "ff00::/8"},
		},
		Runtime: RuntimeConfig{
			PollIntervalSeconds: 2,
			StartGraceMillis:    500,
			AdminAPIListen:      "127.0.0.1:9099",
			ModuleDir:           "/data/adb/modules/ksu-proxy",
		},
	}
	cfg.Normalize()
	return cfg
}

func CaptureConfigToRouting(mode string) RoutingConfig {
	return RoutingConfig{Mode: mode}
}

func (c *Config) Normalize() {
	if c.DataDir == "" {
		c.DataDir = DefaultDataDir
	}
	if c.Paths.WorkDir == "" {
		c.Paths.WorkDir = c.DataDir
	}
	if c.Paths.RuntimeDir == "" {
		c.Paths.RuntimeDir = filepath.Join(c.DataDir, "runtime")
	}
	if c.Paths.LogDir == "" {
		c.Paths.LogDir = filepath.Join(c.DataDir, "logs")
	}
	if c.Paths.RunDir == "" {
		c.Paths.RunDir = filepath.Join(c.DataDir, "run")
	}
	if c.SingBox.Binary == "" {
		c.SingBox.Binary = "/data/adb/modules/ksu-proxy/KsuProxy/bin/arm64-v8a/sing-box"
	}
	if c.SingBox.ConfigDir == "" {
		c.SingBox.ConfigDir = filepath.Join(c.DataDir, "config", "sing-box")
	}
	if c.SingBox.BaseConfig == "" {
		c.SingBox.BaseConfig = filepath.Join(c.SingBox.ConfigDir, "config.json")
	}
	if c.SingBox.RuntimeConfig == "" {
		c.SingBox.RuntimeConfig = filepath.Join(c.Paths.RuntimeDir, "sing-box", "config.json")
	}
	if c.SingBox.TProxyPort == 0 {
		c.SingBox.TProxyPort = 2025
	}
	if c.SingBox.TProxyListen == "" {
		c.SingBox.TProxyListen = "::"
	}
	if c.SingBox.TunInterface == "" {
		c.SingBox.TunInterface = "ksu0"
	}
	if c.SingBox.ClashAPIListen == "" {
		c.SingBox.ClashAPIListen = "127.0.0.1:9090"
	}
	if c.SingBox.ExternalUI == "" {
		c.SingBox.ExternalUI = filepath.Join(c.Paths.RuntimeDir, "yacd")
	}
	if c.SingBox.ExternalUIDownload == "" {
		c.SingBox.ExternalUIDownload = "https://srs.acstudycn.eu.org/gh-pages.zip"
	}
	if c.SingBox.ExternalUIDetour == "" {
		c.SingBox.ExternalUIDetour = "out-direct"
	}
	if c.XTunnel.Binary == "" {
		c.XTunnel.Binary = "/data/adb/modules/ksu-proxy/KsuProxy/bin/arm64-v8a/x-tunnel"
	}
	if c.XTunnel.NodesFile == "" {
		c.XTunnel.NodesFile = filepath.Join(c.DataDir, "config", "x-tunnel", "nodes.list")
	}
	if c.XTunnel.SelectorTag == "" {
		c.XTunnel.SelectorTag = "x-tunnel"
	}
	if c.XTunnel.DefaultDNS == "" {
		c.XTunnel.DefaultDNS = "https://223.5.5.5/dns-query"
	}
	c.XTunnel.DefaultDNS = NormalizeXTunnelDNS(c.XTunnel.DefaultDNS)
	if c.XTunnel.DefaultECH == "" {
		c.XTunnel.DefaultECH = "cloudflare-ech.com"
	}
	if c.XTunnel.DefaultN == 0 {
		c.XTunnel.DefaultN = 3
	}
	if c.XTunnel.DefaultFrontIPs == "" {
		c.XTunnel.DefaultFrontIPs = c.XTunnel.DefaultIP
	}
	if c.Whitelist.File == "" {
		c.Whitelist.File = filepath.Join(c.DataDir, "config", "whitelist", "packages.json")
	}
	if c.Whitelist.Mode == "" {
		c.Whitelist.Mode = "package_all_instances"
	}
	if c.Firewall.ChainPrefix == "" {
		c.Firewall.ChainPrefix = "KSP"
	}
	if c.Firewall.Mark == "" {
		c.Firewall.Mark = "0x12000000/0xff000000"
	}
	if c.Firewall.Table == 0 {
		c.Firewall.Table = 2025
	}
	if c.Firewall.RulePriority == 0 {
		c.Firewall.RulePriority = 1000
	}
	if c.Runtime.PollIntervalSeconds <= 0 {
		c.Runtime.PollIntervalSeconds = 2
	}
	if c.Runtime.StartGraceMillis <= 0 {
		c.Runtime.StartGraceMillis = 500
	}
	if c.Runtime.AdminAPIListen == "" || c.Runtime.AdminAPIListen == "127.0.0.1:2080" {
		c.Runtime.AdminAPIListen = "127.0.0.1:9099"
	}
	if c.Runtime.ModuleDir == "" {
		c.Runtime.ModuleDir = "/data/adb/modules/ksu-proxy"
	}
}

func (c Config) EnsureDirs() error {
	dirs := []string{
		c.Paths.WorkDir,
		c.Paths.RuntimeDir,
		c.Paths.LogDir,
		c.Paths.RunDir,
		filepath.Dir(c.SingBox.RuntimeConfig),
		filepath.Dir(c.SingBox.ExternalUI),
	}
	for _, dir := range dirs {
		if dir == "" {
			return errors.New("empty directory in config")
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}
