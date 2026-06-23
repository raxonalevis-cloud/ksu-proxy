package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadXTunnelNodesKeepsMultipleFrontIPs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nodes.list")
	data := "" +
		"@default_dns=223.5.5.5/dns-query\n" +
		"@default_ech=cloudflare-ech.com\n" +
		"@default_front_ips=173.245.59.112, 104.17.127.226\n" +
		"@default_token=token\n" +
		"@default_parallel=3\n" +
		"x-tunnel-test|1088|wss://example.com/about\n"
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	nodes, err := LoadXTunnelNodes(path, XTunnelConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected one node, got %d", len(nodes))
	}
	want := "173.245.59.112,104.17.127.226"
	if nodes[0].FrontIPs != want {
		t.Fatalf("FrontIPs = %q, want %q", nodes[0].FrontIPs, want)
	}
	if nodes[0].IP != want {
		t.Fatalf("legacy IP = %q, want %q", nodes[0].IP, want)
	}
	if nodes[0].DNS != "https://223.5.5.5/dns-query" {
		t.Fatalf("DNS = %q", nodes[0].DNS)
	}
}
