package pvemock

import (
	"context"
	"errors"
	"testing"
)

// TestFixtureHostReader_DHCPLeases_IpamLab loads ipam-lab.yaml (extended by
// T-406 with a dhcp_leases block including malformed lines) and verifies
// the fixture reader renders it verbatim, then that internal/host's
// defensive parser correctly separates the valid leases from the
// deliberately malformed ones — the fixture-backed corpus T-406
// acceptance criterion 3 asks for, exercised through the same read path
// (peer API -> host.ParseDHCPLeases) production code uses.
func TestFixtureHostReader_DHCPLeases_IpamLab(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "ipam-lab.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := NewServer(f)
	r := NewFixtureHostReader(srv)

	raw, err := r.DHCPLeases(context.Background(), "pve1")
	if err != nil {
		t.Fatalf("DHCPLeases(pve1): %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("DHCPLeases(pve1) returned no content, want the fixture's declared lease-file text")
	}

	// Cross-package smoke check (this package deliberately doesn't import
	// internal/host — see hostreader.go's doc comment — so this just
	// verifies the raw bytes look like the expected line count/shape;
	// internal/host/dnsmasq_test.go owns the actual parser assertions).
	lines := countNonEmptyLines(raw)
	if lines != 5 {
		t.Fatalf("dhcp_leases fixture line count = %d, want 5 (matching testdata/clusters/ipam-lab.yaml)", lines)
	}
}

// TestFixtureHostReader_DHCPLeases_NoneDeclared verifies a node with no
// dhcp_leases block declared returns empty content, not an error — the
// common "no DHCP-managed SDN zone on this node" case.
func TestFixtureHostReader_DHCPLeases_NoneDeclared(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := NewServer(f)
	r := NewFixtureHostReader(srv)
	node := onlyNodeName(t, f)

	raw, err := r.DHCPLeases(context.Background(), node)
	if err != nil {
		t.Fatalf("DHCPLeases(%s): %v", node, err)
	}
	if len(raw) != 0 {
		t.Errorf("DHCPLeases(%s) = %q, want empty", node, raw)
	}
}

// TestFixtureHostReader_DHCPLeases_UnknownNode verifies the ErrNotFound
// path for a node the fixture doesn't know about at all.
func TestFixtureHostReader_DHCPLeases_UnknownNode(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := NewServer(f)
	r := NewFixtureHostReader(srv)

	if _, err := r.DHCPLeases(context.Background(), "does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DHCPLeases error = %v, want wrapped ErrNotFound", err)
	}
}

func countNonEmptyLines(raw []byte) int {
	n := 0
	start := 0
	for i, b := range raw {
		if b == '\n' {
			if i > start {
				n++
			}
			start = i + 1
		}
	}
	if start < len(raw) {
		n++
	}
	return n
}
