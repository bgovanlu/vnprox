// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"context"
	"testing"
)

// TestFixtureHostReader_Conntrack is T-1305's fixture-backing smoke test:
// three-node-vlan.yaml's pve1 conntrack block round-trips through
// FixtureHostReader.Conntrack verbatim, including a SNAT and a DNAT entry.
func TestFixtureHostReader_Conntrack(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "three-node-vlan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(f)
	reader := NewFixtureHostReader(srv)
	ctx := context.Background()

	entries, err := reader.Conntrack(ctx, "pve1")
	if err != nil {
		t.Fatalf("Conntrack: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("got %d entries, want 4: %+v", len(entries), entries)
	}

	var sawSNAT, sawDNAT bool
	for _, e := range entries {
		if e.NatSrc != nil {
			sawSNAT = true
			if e.NatSrc.IP != "203.0.113.10" || e.NatSrc.Port != 44444 {
				t.Errorf("SNAT entry NatSrc = %+v, unexpected", e.NatSrc)
			}
		}
		if e.NatDst != nil {
			sawDNAT = true
			if e.NatDst.IP != "10.10.0.11" || e.NatDst.Port != 80 {
				t.Errorf("DNAT entry NatDst = %+v, unexpected", e.NatDst)
			}
		}
	}
	if !sawSNAT {
		t.Error("expected at least one SNAT entry (NatSrc set)")
	}
	if !sawDNAT {
		t.Error("expected at least one DNAT entry (NatDst set)")
	}

	pve2Entries, err := reader.Conntrack(ctx, "pve2")
	if err != nil {
		t.Fatalf("Conntrack(pve2): %v", err)
	}
	if len(pve2Entries) != 1 {
		t.Fatalf("pve2: got %d entries, want 1: %+v", len(pve2Entries), pve2Entries)
	}
}

func TestFixtureHostReader_Conntrack_UnknownNode(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(f)
	reader := NewFixtureHostReader(srv)
	if _, err := reader.Conntrack(context.Background(), "nope"); err == nil {
		t.Fatal("expected error for unknown node")
	}
}
