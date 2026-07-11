package pvemock

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// TestFixtureHostReader_FRR_EvpnLab loads evpn-lab.yaml (T-404's extension
// of T-401's fixture) and verifies the FRR reader renders each node's
// declared BGP/EVPN state into parseable, realistic-shaped JSON.
func TestFixtureHostReader_FRR_EvpnLab(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "evpn-lab.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := NewServer(f)
	r := NewFixtureHostReader(srv)
	ctx := context.Background()

	bgp, err := r.FRRBGPSummary(ctx, "pve1")
	if err != nil {
		t.Fatalf("FRRBGPSummary(pve1): %v", err)
	}
	var decoded map[string]any
	if uErr := json.Unmarshal(bgp, &decoded); uErr != nil {
		t.Fatalf("decoding bgp summary: %v (raw: %s)", uErr, bgp)
	}
	block, ok := decoded["l2VpnEvpn"].(map[string]any)
	if !ok {
		t.Fatalf("expected l2VpnEvpn block, got %v", decoded)
	}
	peers, ok := block["peers"].(map[string]any)
	if !ok || len(peers) != 2 {
		t.Fatalf("expected 2 peers under l2VpnEvpn, got %v", block["peers"])
	}

	vni, err := r.FRREVPNVNI(ctx, "pve1")
	if err != nil {
		t.Fatalf("FRREVPNVNI(pve1): %v", err)
	}
	var vniDecoded map[string]any
	if uErr := json.Unmarshal(vni, &vniDecoded); uErr != nil {
		t.Fatalf("decoding evpn vni: %v (raw: %s)", uErr, vni)
	}
	if _, ok := vniDecoded["10001"]; !ok {
		t.Errorf("expected VNI 10001 entry, got %v", vniDecoded)
	}
}

// TestFixtureHostReader_FRR_Unavailable verifies a node with no `frr:`
// block declared (single-node.yaml's only node has none) returns
// ErrFRRUnavailable cleanly — the mock's modeled equivalent of T-404 AC2.
func TestFixtureHostReader_FRR_Unavailable(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := NewServer(f)
	r := NewFixtureHostReader(srv)
	ctx := context.Background()

	node := onlyNodeName(t, f)

	if _, err := r.FRRBGPSummary(ctx, node); !errors.Is(err, ErrFRRUnavailable) {
		t.Errorf("FRRBGPSummary error = %v, want wrapped ErrFRRUnavailable", err)
	}
	if _, err := r.FRREVPNVNI(ctx, node); !errors.Is(err, ErrFRRUnavailable) {
		t.Errorf("FRREVPNVNI error = %v, want wrapped ErrFRRUnavailable", err)
	}
}

// TestFixtureHostReader_FRR_UnknownNode verifies the ErrNotFound path
// (distinct from ErrFRRUnavailable) for a node the fixture doesn't know
// about at all.
func TestFixtureHostReader_FRR_UnknownNode(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := NewServer(f)
	r := NewFixtureHostReader(srv)
	ctx := context.Background()

	if _, err := r.FRRBGPSummary(ctx, "does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Errorf("FRRBGPSummary error = %v, want wrapped ErrNotFound", err)
	}
}

func onlyNodeName(t *testing.T, f *Fixture) string {
	t.Helper()
	for name := range f.Nodes {
		return name
	}
	t.Fatal("fixture has no nodes")
	return ""
}
