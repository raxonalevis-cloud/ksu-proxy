package firewall

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"ksu-proxy/internal/config"
)

type Manager struct {
	Config config.Config
	Logger *log.Logger
}

var legacyFirewallMarks = []string{
	"0x10000000/0xffffffff",
	"0x10000000",
	"0x4b535550/0xffffffff",
	"0x4b535550",
	"0x12000000/0xffffffff",
	"0x12000000",
	"174063616/534773760",
	"128/128",
	"2097152/2097152",
}

func (m Manager) ApplyTProxy(ctx context.Context, appUIDs []int, hotspotIfaces []string) (err error) {
	appUIDs = uniqueSorted(appUIDs)
	hotspotIfaces = uniqueStrings(hotspotIfaces)
	backend := strings.ToLower(strings.TrimSpace(m.Config.Firewall.Backend))
	if backend == "" {
		backend = "auto"
	}

	switch backend {
	case "auto":
		if err := m.applyTProxyWith(ctx, "nft", appUIDs, hotspotIfaces); err == nil {
			return nil
		} else if m.Logger != nil {
			m.Logger.Printf("nft firewall backend failed, falling back to iptables-nft/iptables: %v", err)
		}
		return m.applyTProxyWith(ctx, "iptables", appUIDs, hotspotIfaces)
	case "nft", "nftables":
		return m.applyTProxyWith(ctx, "nft", appUIDs, hotspotIfaces)
	case "iptables", "xtables":
		return m.applyTProxyWith(ctx, "iptables", appUIDs, hotspotIfaces)
	default:
		return fmt.Errorf("unknown firewall backend: %s", m.Config.Firewall.Backend)
	}
}

func (m Manager) applyTProxyWith(ctx context.Context, backend string, appUIDs []int, hotspotIfaces []string) (err error) {
	_ = m.Cleanup(ctx)
	defer func() {
		if err != nil {
			m.cleanupUIDPolicyRules(ctx, appUIDs)
			_ = m.Cleanup(ctx)
		}
	}()

	if err := m.applyPolicyRoutes(ctx, appUIDs, hotspotIfaces); err != nil {
		return err
	}

	switch backend {
	case "nft":
		if err := m.applyNFTables(ctx, appUIDs, hotspotIfaces); err != nil {
			return err
		}
		return m.writeFirewallUIDs(appUIDs)
	case "iptables":
		if err := m.applyIPTables(ctx, "iptables", false, appUIDs, hotspotIfaces, m.Config.Firewall.BypassIPv4); err != nil {
			return err
		}
		_ = m.applyIPTables(ctx, "ip6tables", true, appUIDs, hotspotIfaces, m.Config.Firewall.BypassIPv6)
		return m.writeFirewallUIDs(appUIDs)
	default:
		return fmt.Errorf("unknown firewall backend: %s", backend)
	}
}

func (m Manager) Cleanup(ctx context.Context) error {
	prefix := m.Config.Firewall.ChainPrefix
	localChain := prefix + "_LOCAL"
	tproxyChain := prefix + "_TPROXY"
	hotspotChain := prefix + "_HOTSPOT"
	quicChain := prefix + "_QUIC"
	loopbackChain := prefix + "_LOOPBACK"
	legacyBypassChain := prefix + "_BYPASS"
	oldUIDs := m.readFirewallUIDs()

	for _, ipt := range []string{"iptables", "ip6tables"} {
		for _, attach := range [][]string{
			{"-t", "mangle", "-D", "OUTPUT", "-j", localChain},
			{"-t", "mangle", "-D", "PREROUTING", "-j", hotspotChain},
			{"-t", "filter", "-D", "OUTPUT", "-j", quicChain},
		} {
			_ = m.run(ctx, true, ipt, attach...)
		}
		for _, mark := range m.cleanupMarks() {
			_ = m.run(ctx, true, ipt, "-t", "mangle", "-D", "PREROUTING", "-i", "lo", "-m", "mark", "--mark", mark, "-j", tproxyChain)
		}
		for _, iface := range m.cleanupHotspotInterfaces() {
			_ = m.run(ctx, true, ipt, "-t", "mangle", "-D", "PREROUTING", "-i", iface, "-j", hotspotChain)
		}
		for _, chain := range []string{localChain, tproxyChain, hotspotChain, loopbackChain, legacyBypassChain} {
			_ = m.run(ctx, true, ipt, "-t", "mangle", "-F", chain)
			_ = m.run(ctx, true, ipt, "-t", "mangle", "-X", chain)
		}
		_ = m.run(ctx, true, ipt, "-t", "filter", "-F", quicChain)
		_ = m.run(ctx, true, ipt, "-t", "filter", "-X", quicChain)
	}
	for _, mark := range m.cleanupMarks() {
		_ = m.run(ctx, true, "ip", "-4", "rule", "del", "fwmark", mark, "table", strconv.Itoa(m.Config.Firewall.Table), "priority", strconv.Itoa(m.Config.Firewall.RulePriority))
		_ = m.run(ctx, true, "ip", "-6", "rule", "del", "fwmark", mark, "table", strconv.Itoa(m.Config.Firewall.Table), "priority", strconv.Itoa(m.Config.Firewall.RulePriority))
		_ = m.run(ctx, true, "ip", "-4", "rule", "del", "iif", "lo", "fwmark", mark, "table", strconv.Itoa(m.Config.Firewall.Table), "priority", strconv.Itoa(m.loopbackRulePriority()))
		_ = m.run(ctx, true, "ip", "-6", "rule", "del", "iif", "lo", "fwmark", mark, "table", strconv.Itoa(m.Config.Firewall.Table), "priority", strconv.Itoa(m.loopbackRulePriority()))
		for _, iface := range m.cleanupHotspotInterfaces() {
			_ = m.run(ctx, true, "ip", "-4", "rule", "del", "iif", iface, "fwmark", mark, "table", strconv.Itoa(m.Config.Firewall.Table), "priority", strconv.Itoa(m.Config.Firewall.RulePriority))
			_ = m.run(ctx, true, "ip", "-6", "rule", "del", "iif", iface, "fwmark", mark, "table", strconv.Itoa(m.Config.Firewall.Table), "priority", strconv.Itoa(m.Config.Firewall.RulePriority))
		}
	}
	m.cleanupUIDPolicyRules(ctx, oldUIDs)
	_ = m.run(ctx, true, "ip", "-4", "route", "flush", "table", strconv.Itoa(m.Config.Firewall.Table))
	_ = m.run(ctx, true, "ip", "-6", "route", "flush", "table", strconv.Itoa(m.Config.Firewall.Table))
	_ = m.run(ctx, true, "nft", "delete", "table", "inet", nftTableName(m.Config.Firewall.ChainPrefix))
	_ = os.Remove(m.firewallUIDFile())
	return nil
}

func (m Manager) applyNFTables(ctx context.Context, appUIDs []int, hotspotIfaces []string) error {
	if err := m.run(ctx, true, "nft", "delete", "table", "inet", nftTableName(m.Config.Firewall.ChainPrefix)); err != nil {
		return err
	}
	script := m.buildNFTablesScript(appUIDs, hotspotIfaces)
	if err := os.MkdirAll(m.Config.Paths.RunDir, 0755); err != nil {
		return err
	}
	path := filepath.Join(m.Config.Paths.RunDir, "firewall.nft")
	if err := os.WriteFile(path, []byte(script), 0644); err != nil {
		return err
	}
	return m.run(ctx, false, "nft", "-f", path)
}

func (m Manager) buildNFTablesScript(appUIDs []int, hotspotIfaces []string) string {
	table := nftTableName(m.Config.Firewall.ChainPrefix)
	mark := nftMarkValue(m.Config.Firewall.Mark)
	markMatch := nftMarkMatchExpr(m.Config.Firewall.Mark)
	port := strconv.Itoa(m.Config.SingBox.TProxyPort)

	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s {\n", table)

	// Output chain: mark app traffic, exclude core UIDs and bypass subnets
	b.WriteString("  chain output {\n")
	b.WriteString("    type route hook output priority mangle; policy accept;\n")
	if len(m.Config.Firewall.CoreUIDs) > 0 {
		fmt.Fprintf(&b, "    meta skuid { %s } return\n", joinInts(m.Config.Firewall.CoreUIDs))
	}
	if len(m.Config.Firewall.BypassIPv4) > 0 {
		fmt.Fprintf(&b, "    ip daddr { %s } return\n", strings.Join(m.Config.Firewall.BypassIPv4, ", "))
	}
	if len(m.Config.Firewall.BypassIPv6) > 0 {
		fmt.Fprintf(&b, "    ip6 daddr { %s } return\n", strings.Join(m.Config.Firewall.BypassIPv6, ", "))
	}
	if len(appUIDs) > 0 {
		fmt.Fprintf(&b, "    meta skuid { %s } meta l4proto { tcp, udp } meta mark set %s\n", joinInts(appUIDs), mark)
	}
	b.WriteString("  }\n\n")

	// Prerouting chain: TPROXY for loopback and hotspot
	b.WriteString("  chain prerouting {\n")
	b.WriteString("    type filter hook prerouting priority mangle; policy accept;\n")
	fmt.Fprintf(&b, "    iifname \"lo\" %s tcp dport 0-65535 tproxy to :%s meta mark set %s accept\n", markMatch, port, mark)
	fmt.Fprintf(&b, "    iifname \"lo\" %s udp dport 0-65535 tproxy to :%s meta mark set %s accept\n", markMatch, port, mark)
	if m.Config.Hotspot.Enabled && len(hotspotIfaces) > 0 {
		if len(m.Config.Firewall.BypassIPv4) > 0 {
			fmt.Fprintf(&b, "    iifname { %s } ip daddr { %s } return\n", joinQuoted(hotspotIfaces), strings.Join(m.Config.Firewall.BypassIPv4, ", "))
		}
		if len(m.Config.Firewall.BypassIPv6) > 0 {
			fmt.Fprintf(&b, "    iifname { %s } ip6 daddr { %s } return\n", joinQuoted(hotspotIfaces), strings.Join(m.Config.Firewall.BypassIPv6, ", "))
		}
		fmt.Fprintf(&b, "    iifname { %s } tcp dport 0-65535 tproxy to :%s meta mark set %s accept\n", joinQuoted(hotspotIfaces), port, mark)
		fmt.Fprintf(&b, "    iifname { %s } udp dport 0-65535 tproxy to :%s meta mark set %s accept\n", joinQuoted(hotspotIfaces), port, mark)
	}
	b.WriteString("  }\n")

	// Anti-loop chain: prevent proxy traffic from looping
	if m.Config.Firewall.BlockLoopback {
		b.WriteString("\n  chain loopback {\n")
		b.WriteString("    type filter hook input priority mangle; policy accept;\n")
		if len(m.Config.Firewall.CoreUIDs) > 0 {
			fmt.Fprintf(&b, "    meta skuid { %s } return\n", joinInts(m.Config.Firewall.CoreUIDs))
		}
		fmt.Fprintf(&b, "    %s tcp dport 0-65535 return\n", markMatch)
		fmt.Fprintf(&b, "    %s udp dport 0-65535 return\n", markMatch)
		b.WriteString("  }\n")
	}

	// QUIC disable chain
	if m.Config.Firewall.DisableQUIC && len(appUIDs) > 0 {
		b.WriteString("\n  chain quic {\n")
		b.WriteString("    type filter hook output priority filter; policy accept;\n")
		fmt.Fprintf(&b, "    meta skuid { %s } udp dport 443 reject\n", joinInts(appUIDs))
		b.WriteString("  }\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func (m Manager) applyIPTables(ctx context.Context, ipt string, ipv6 bool, appUIDs []int, hotspotIfaces []string, bypass []string) error {
	prefix := m.Config.Firewall.ChainPrefix
	localChain := prefix + "_LOCAL"
	tproxyChain := prefix + "_TPROXY"
	hotspotChain := prefix + "_HOTSPOT"
	quicChain := prefix + "_QUIC"
	loopbackChain := prefix + "_LOOPBACK"
	port := strconv.Itoa(m.Config.SingBox.TProxyPort)

	for _, chain := range []string{localChain, tproxyChain, hotspotChain, loopbackChain} {
		if err := m.run(ctx, false, ipt, "-t", "mangle", "-N", chain); err != nil {
			return err
		}
	}

	// Anti-loop: exclude core process traffic
	for _, uid := range m.Config.Firewall.CoreUIDs {
		uidText := strconv.Itoa(uid)
		if err := m.run(ctx, false, ipt, "-t", "mangle", "-A", localChain, "-m", "owner", "--uid-owner", uidText, "-j", "RETURN"); err != nil {
			return err
		}
	}

	// Anti-loop: exclude bypass subnets
	for _, subnet := range bypass {
		if err := m.run(ctx, false, ipt, "-t", "mangle", "-A", localChain, "-d", subnet, "-j", "RETURN"); err != nil {
			return err
		}
	}

	// Anti-loop: mark app traffic
	for _, uid := range appUIDs {
		uidText := strconv.Itoa(uid)
		for _, proto := range []string{"tcp", "udp"} {
			if err := m.run(ctx, false, ipt, "-t", "mangle", "-A", localChain, "-m", "owner", "--uid-owner", uidText, "-p", proto, "-j", "MARK", "--set-xmark", m.Config.Firewall.Mark); err != nil {
				return err
			}
		}
	}

	// TPROXY chain: bypass subnets
	for _, subnet := range bypass {
		if err := m.run(ctx, false, ipt, "-t", "mangle", "-A", tproxyChain, "-d", subnet, "-j", "RETURN"); err != nil {
			return err
		}
	}

	// TPROXY chain: redirect to TPROXY port
	for _, proto := range []string{"tcp", "udp"} {
		if err := m.run(ctx, false, ipt, "-t", "mangle", "-A", tproxyChain, "-p", proto, "-j", "TPROXY", "--on-port", port, "--tproxy-mark", m.Config.Firewall.Mark); err != nil {
			return err
		}
	}

	// HOTSPOT chain: bypass subnets
	for _, subnet := range bypass {
		if err := m.run(ctx, false, ipt, "-t", "mangle", "-A", hotspotChain, "-d", subnet, "-j", "RETURN"); err != nil {
			return err
		}
	}

	// HOTSPOT chain: redirect to TPROXY port
	for _, proto := range []string{"tcp", "udp"} {
		if err := m.run(ctx, false, ipt, "-t", "mangle", "-A", hotspotChain, "-p", proto, "-j", "TPROXY", "--on-port", port, "--tproxy-mark", m.Config.Firewall.Mark); err != nil {
			return err
		}
	}

	// LOOPBACK chain: prevent proxy loop
	if m.Config.Firewall.BlockLoopback {
		// Block traffic from proxy processes going back through TPROXY
		for _, uid := range m.Config.Firewall.CoreUIDs {
			uidText := strconv.Itoa(uid)
			if err := m.run(ctx, false, ipt, "-t", "mangle", "-A", loopbackChain, "-m", "owner", "--uid-owner", uidText, "-j", "RETURN"); err != nil {
				return err
			}
		}
		// Mark loopback traffic from core UIDs to skip TPROXY
		for _, proto := range []string{"tcp", "udp"} {
			if err := m.run(ctx, false, ipt, "-t", "mangle", "-A", loopbackChain, "-p", proto, "-m", "mark", "--mark", m.Config.Firewall.Mark, "-j", "RETURN"); err != nil {
				return err
			}
		}
	}

	// Attach chains
	if err := m.run(ctx, false, ipt, "-t", "mangle", "-A", "OUTPUT", "-j", localChain); err != nil {
		return err
	}
	if err := m.run(ctx, false, ipt, "-t", "mangle", "-A", "PREROUTING", "-i", "lo", "-m", "mark", "--mark", m.Config.Firewall.Mark, "-j", tproxyChain); err != nil {
		return err
	}
	if m.Config.Hotspot.Enabled {
		for _, iface := range hotspotIfaces {
			if err := m.run(ctx, false, ipt, "-t", "mangle", "-A", "PREROUTING", "-i", iface, "-j", hotspotChain); err != nil {
				return err
			}
		}
	}

	// QUIC disable
	if m.Config.Firewall.DisableQUIC {
		if err := m.run(ctx, false, ipt, "-t", "filter", "-N", quicChain); err != nil {
			return err
		}
		reject := "REJECT"
		if ipv6 {
			reject = "DROP"
		}
		for _, uid := range appUIDs {
			uidText := strconv.Itoa(uid)
			if err := m.run(ctx, false, ipt, "-t", "filter", "-A", quicChain, "-m", "owner", "--uid-owner", uidText, "-p", "udp", "--dport", "443", "-j", reject); err != nil {
				return err
			}
		}
		if err := m.run(ctx, false, ipt, "-t", "filter", "-A", "OUTPUT", "-j", quicChain); err != nil {
			return err
		}
	}
	return nil
}

func (m Manager) cleanupMarks() []string {
	values := []string{strings.TrimSpace(m.Config.Firewall.Mark)}
	values = append(values, legacyFirewallMarks...)
	seen := make(map[string]bool)
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func (m Manager) applyPolicyRoutes(ctx context.Context, appUIDs []int, hotspotIfaces []string) error {
	appUIDs = uniqueSorted(appUIDs)
	hotspotIfaces = uniqueStrings(hotspotIfaces)
	if len(appUIDs) == 0 && len(hotspotIfaces) == 0 {
		return nil
	}
	table := strconv.Itoa(m.Config.Firewall.Table)
	priority := strconv.Itoa(m.Config.Firewall.RulePriority)
	for _, uid := range appUIDs {
		uidText := uidRange(uid)
		if err := m.run(ctx, false, "ip", "-4", "rule", "add", "fwmark", m.Config.Firewall.Mark, "uidrange", uidText, "table", table, "priority", priority); err != nil {
			return err
		}
		_ = m.run(ctx, true, "ip", "-6", "rule", "add", "fwmark", m.Config.Firewall.Mark, "uidrange", uidText, "table", table, "priority", priority)
	}
	for _, iface := range hotspotIfaces {
		if err := m.run(ctx, false, "ip", "-4", "rule", "add", "iif", iface, "fwmark", m.Config.Firewall.Mark, "table", table, "priority", priority); err != nil {
			return err
		}
		_ = m.run(ctx, true, "ip", "-6", "rule", "add", "iif", iface, "fwmark", m.Config.Firewall.Mark, "table", table, "priority", priority)
	}
	loopbackPriority := strconv.Itoa(m.loopbackRulePriority())
	if err := m.run(ctx, false, "ip", "-4", "rule", "add", "iif", "lo", "fwmark", m.Config.Firewall.Mark, "table", table, "priority", loopbackPriority); err != nil {
		return err
	}
	_ = m.run(ctx, true, "ip", "-6", "rule", "add", "iif", "lo", "fwmark", m.Config.Firewall.Mark, "table", table, "priority", loopbackPriority)
	if err := m.run(ctx, false, "ip", "-4", "route", "add", "local", "default", "dev", "lo", "table", table); err != nil {
		return err
	}
	_ = m.run(ctx, true, "ip", "-6", "route", "add", "local", "default", "dev", "lo", "table", table)
	return nil
}

func (m Manager) loopbackRulePriority() int {
	return m.Config.Firewall.RulePriority + 1
}

func (m Manager) cleanupUIDPolicyRules(ctx context.Context, appUIDs []int) {
	table := strconv.Itoa(m.Config.Firewall.Table)
	priority := strconv.Itoa(m.Config.Firewall.RulePriority)
	mark := strings.TrimSpace(m.Config.Firewall.Mark)
	if mark == "" {
		return
	}
	for _, uid := range uniqueSorted(appUIDs) {
		uidRange := uidRange(uid)
		_ = m.run(ctx, true, "ip", "-4", "rule", "del", "fwmark", mark, "uidrange", uidRange, "table", table, "priority", priority)
		_ = m.run(ctx, true, "ip", "-6", "rule", "del", "fwmark", mark, "uidrange", uidRange, "table", table, "priority", priority)
	}
}

func uidRange(uid int) string {
	text := strconv.Itoa(uid)
	return text + "-" + text
}

func (m Manager) firewallUIDFile() string {
	return filepath.Join(m.Config.Paths.RunDir, "firewall.uids")
}

func (m Manager) readFirewallUIDs() []int {
	raw, err := os.ReadFile(m.firewallUIDFile())
	if err != nil {
		return nil
	}
	out := make([]int, 0)
	for _, field := range strings.Fields(string(raw)) {
		uid, err := strconv.Atoi(field)
		if err == nil && uid > 0 {
			out = append(out, uid)
		}
	}
	return uniqueSorted(out)
}

func (m Manager) writeFirewallUIDs(appUIDs []int) error {
	if err := os.MkdirAll(m.Config.Paths.RunDir, 0755); err != nil {
		return err
	}
	var b strings.Builder
	for _, uid := range uniqueSorted(appUIDs) {
		fmt.Fprintf(&b, "%d\n", uid)
	}
	return os.WriteFile(m.firewallUIDFile(), []byte(b.String()), 0644)
}

func (m Manager) cleanupHotspotInterfaces() []string {
	return uniqueStrings(append([]string{}, m.Config.Hotspot.Interfaces...))
}

func (m Manager) run(ctx context.Context, ignoreErr bool, name string, args ...string) error {
	execName := resolveSystemCommand(name)
	args = prepareCommandArgs(name, args)
	line := execName + " " + strings.Join(args, " ")
	if m.Config.Firewall.DryRun {
		if m.Logger != nil {
			m.Logger.Printf("[dry-run] %s", line)
		}
		return nil
	}
	if m.Logger != nil {
		m.Logger.Printf("[exec] %s", line)
	}
	cmd := exec.CommandContext(ctx, execName, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ignoreErr {
			return nil
		}
		return fmt.Errorf("%s: %w: %s", line, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func resolveSystemCommand(name string) string {
	base := filepath.Base(name)
	candidates := []string{base}
	switch base {
	case "iptables":
		candidates = []string{"iptables-nft", "iptables"}
	case "ip6tables":
		candidates = []string{"ip6tables-nft", "ip6tables"}
	case "ip", "nft":
	default:
		return name
	}
	for _, candidate := range candidates {
		for _, dir := range []string{"/system/bin", "/system/xbin", "/vendor/bin", "/data/adb/ksu/bin", "/data/adb/ap/bin", "/data/adb/magisk"} {
			path := filepath.Join(dir, candidate)
			if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
				return path
			}
		}
	}
	return name
}

func prepareCommandArgs(name string, args []string) []string {
	out := append([]string{}, args...)
	if !isIPTablesCommand(name) || hasIPTablesWait(out) {
		return out
	}
	// Android 上 iptables 锁冲突较常见，使用较长的等待时间
	return append([]string{"-w", "100"}, out...)
}

func isIPTablesCommand(name string) bool {
	switch filepath.Base(name) {
	case "iptables", "ip6tables":
		return true
	default:
		return false
	}
}

func hasIPTablesWait(args []string) bool {
	for _, arg := range args {
		if arg == "-w" || arg == "--wait" || strings.HasPrefix(arg, "-w") || strings.HasPrefix(arg, "--wait=") {
			return true
		}
	}
	return false
}

func nftTableName(prefix string) string {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	var b strings.Builder
	for _, r := range prefix {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		name = "ksu"
	}
	return name + "_proxy"
}

func nftMarkValue(mark string) string {
	mark, _, _ = strings.Cut(strings.TrimSpace(mark), "/")
	if mark == "" {
		return "0x12000000"
	}
	return mark
}

func nftMarkMatchExpr(mark string) string {
	value, mask, _ := strings.Cut(strings.TrimSpace(mark), "/")
	value = strings.TrimSpace(value)
	mask = strings.ToLower(strings.TrimSpace(mask))
	if value == "" {
		value = "0x12000000"
	}
	if mask == "" || mask == "0xffffffff" {
		return "meta mark " + value
	}
	return fmt.Sprintf("meta mark & %s == %s", mask, value)
}

func joinInts(values []int) string {
	values = append([]int{}, values...)
	sort.Ints(values)
	out := make([]string, 0, len(values))
	last := -1
	for _, value := range values {
		if value < 0 || value == last {
			continue
		}
		out = append(out, strconv.Itoa(value))
		last = value
	}
	return strings.Join(out, ", ")
}

func joinQuoted(values []string) string {
	values = uniqueStrings(values)
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strconv.Quote(value))
	}
	return strings.Join(out, ", ")
}

func uniqueSorted(in []int) []int {
	sort.Ints(in)
	out := make([]int, 0, len(in))
	last := -1
	for _, item := range in {
		if item <= 0 || item == last {
			continue
		}
		out = append(out, item)
		last = item
	}
	return out
}

func uniqueStrings(in []string) []string {
	sort.Strings(in)
	out := make([]string, 0, len(in))
	last := ""
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" || item == last {
			continue
		}
		out = append(out, item)
		last = item
	}
	return out
}
