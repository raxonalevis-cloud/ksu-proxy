package firewall

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"ksu-proxy/internal/config"
)

func TestBuildNFTablesScriptUsesWhitelistUIDsOnly(t *testing.T) {
	cfg := config.Default()
	cfg.Firewall.Backend = "auto"
	cfg.Firewall.ChainPrefix = "KSP"
	cfg.Firewall.Mark = "0x12000000/0xff000000"
	cfg.SingBox.TProxyPort = 2025
	cfg.Hotspot.Enabled = true
	manager := Manager{Config: cfg}

	script := manager.buildNFTablesScript([]int{99910308, 10330, 99910308}, []string{"wlan1", "ap0"})
	for _, want := range []string{
		"table inet ksp_proxy",
		"meta skuid { 10330, 99910308 } meta l4proto { tcp, udp } meta mark set 0x12000000",
		"iifname \"lo\" meta mark & 0xff000000 == 0x12000000 tcp dport 0-65535 tproxy to :2025",
		"iifname { \"ap0\", \"wlan1\" } udp dport 0-65535 tproxy to :2025 meta mark set 0x12000000 accept",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("nft script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "99910127") {
		t.Fatalf("nft script unexpectedly contains a UID that was not provided:\n%s", script)
	}
}

func TestNFTHelpers(t *testing.T) {
	if got := nftTableName("KSP"); got != "ksp_proxy" {
		t.Fatalf("nftTableName() = %q", got)
	}
	if got := nftTableName("123 bad"); got != "ksu_proxy" {
		t.Fatalf("nftTableName() = %q", got)
	}
	if got := nftMarkValue("0x12000000/0xff000000"); got != "0x12000000" {
		t.Fatalf("nftMarkValue() = %q", got)
	}
	if got := nftMarkMatchExpr("0x12000000/0xff000000"); got != "meta mark & 0xff000000 == 0x12000000" {
		t.Fatalf("nftMarkMatchExpr() = %q", got)
	}
	if got := nftMarkMatchExpr("0x12000000/0xffffffff"); got != "meta mark 0x12000000" {
		t.Fatalf("nftMarkMatchExpr() = %q", got)
	}
}

func TestApplyPolicyRoutesScopesInitialLookupToUIDRangeAndLoopback(t *testing.T) {
	cfg := config.Default()
	cfg.Firewall.DryRun = true
	cfg.Firewall.Mark = "0x12000000/0xff000000"
	var logs bytes.Buffer
	manager := Manager{Config: cfg, Logger: log.New(&logs, "", 0)}

	if err := manager.applyPolicyRoutes(context.Background(), []int{10330, 99910308}, nil); err != nil {
		t.Fatalf("applyPolicyRoutes() returned error in dry-run style test: %v", err)
	}
	if !strings.Contains(logs.String(), "rule add fwmark 0x12000000/0xff000000 uidrange 10330-10330 table") {
		t.Fatalf("applyPolicyRoutes() did not add the owner UID policy route:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "rule add fwmark 0x12000000/0xff000000 uidrange 99910308-99910308 table") {
		t.Fatalf("applyPolicyRoutes() did not add the clone UID policy route:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "rule add iif lo fwmark 0x12000000/0xff000000 table") {
		t.Fatalf("applyPolicyRoutes() did not add the loopback TProxy policy route:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), "rule add fwmark 0x12000000/0xff000000 table") {
		t.Fatalf("applyPolicyRoutes() added an unscoped global fwmark route:\n%s", logs.String())
	}
}

func TestApplyPolicyRoutesAddsHotspotInterfaceRules(t *testing.T) {
	cfg := config.Default()
	cfg.Firewall.DryRun = true
	cfg.Firewall.Mark = "0x12000000/0xff000000"
	var logs bytes.Buffer
	manager := Manager{Config: cfg, Logger: log.New(&logs, "", 0)}

	if err := manager.applyPolicyRoutes(context.Background(), nil, []string{"ap0", "rndis0"}); err != nil {
		t.Fatalf("applyPolicyRoutes() returned error in dry-run style test: %v", err)
	}
	output := logs.String()
	for _, want := range []string{
		"rule add iif ap0 fwmark 0x12000000/0xff000000 table",
		"rule add iif rndis0 fwmark 0x12000000/0xff000000 table",
		"route add local default dev lo table",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("applyPolicyRoutes() missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "uidrange") {
		t.Fatalf("hotspot-only policy routes should not add UID scoped rules:\n%s", output)
	}
	if strings.Contains(output, "rule add fwmark 0x12000000/0xff000000 table") {
		t.Fatalf("applyPolicyRoutes() added an unscoped global fwmark route:\n%s", output)
	}
}

func TestCleanupUIDPolicyRulesUsesCurrentMarkOnly(t *testing.T) {
	cfg := config.Default()
	cfg.Firewall.DryRun = true
	cfg.Firewall.Mark = "0x12000000/0xff000000"
	var logs bytes.Buffer
	manager := Manager{Config: cfg, Logger: log.New(&logs, "", 0)}

	manager.cleanupUIDPolicyRules(context.Background(), []int{10330, 99910308})
	output := logs.String()
	if got := strings.Count(output, " rule del "); got != 4 {
		t.Fatalf("cleanupUIDPolicyRules() command count = %d, logs:\n%s", got, output)
	}
	if strings.Contains(output, "0x10000000") || strings.Contains(output, "0x4b535550") {
		t.Fatalf("cleanupUIDPolicyRules() should not multiply UID cleanup by legacy marks:\n%s", output)
	}
	if !strings.Contains(output, "uidrange 10330-10330") || !strings.Contains(output, "uidrange 99910308-99910308") {
		t.Fatalf("cleanupUIDPolicyRules() missing uidrange cleanup:\n%s", output)
	}
}

func TestPrepareCommandArgsUsesShortIPTablesWait(t *testing.T) {
	args := prepareCommandArgs("iptables", []string{"-t", "mangle", "-L"})
	got := strings.Join(args, " ")
	if !strings.HasPrefix(got, "-w 5 ") {
		t.Fatalf("prepareCommandArgs() = %q", got)
	}
}
