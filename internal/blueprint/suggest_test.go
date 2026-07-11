package blueprint_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
)

// T-603 AC4: address params get next-free suggestions.
func TestSuggestAddress_SkipsUsedNetworkAndBroadcast(t *testing.T) {
	g := newGraphWithNodes("pve1")
	applyBridge(g, "pve1", "vmbr0", bridgeOpts{addresses: []string{"192.168.1.1/24", "192.168.1.2/24"}})

	got, err := blueprint.SuggestAddress(g.Snapshot(), "192.168.1.0/24")
	if err != nil {
		t.Fatalf("SuggestAddress: %v", err)
	}
	if got != "192.168.1.3/24" {
		t.Fatalf("got %q, want 192.168.1.3/24 (first free host after the two used addresses)", got)
	}
}

func TestSuggestAddress_PoolExhausted(t *testing.T) {
	g := newGraphWithNodes("pve1")
	// A /30 has exactly two usable host addresses; use both.
	applyBridge(g, "pve1", "vmbr0", bridgeOpts{addresses: []string{"10.0.0.1/30", "10.0.0.2/30"}})

	_, err := blueprint.SuggestAddress(g.Snapshot(), "10.0.0.0/30")
	if err == nil {
		t.Fatal("expected an error when the pool has no free host addresses")
	}
}

func TestSuggestForParam_UsesParamSubnet(t *testing.T) {
	bp, ok := blueprint.StarterByID(blueprint.StarterSingleNICHomelab)
	if !ok {
		t.Fatal("starter not found")
	}
	g := newGraphWithNodes("pve1")
	applyBridge(g, "pve1", "vmbr0", bridgeOpts{addresses: []string{"192.168.1.10/24"}})

	got, err := blueprint.SuggestForParam(bp, "mgmtCidr", g.Snapshot())
	if err != nil {
		t.Fatalf("SuggestForParam: %v", err)
	}
	if got == "192.168.1.10/24" {
		t.Fatalf("suggested the already-used address %q", got)
	}
}

// SuggestForParam on a ParamIP-typed param (the SDN starters' bare-IP
// gateway) returns a bare IP, no "/<prefix>" suffix.
func TestSuggestForParam_ParamIP_StripsPrefix(t *testing.T) {
	bp, ok := blueprint.StarterByID(blueprint.StarterVXLANOverlay)
	if !ok {
		t.Fatal("starter not found")
	}
	g := newGraphWithNodes("pve1")

	got, err := blueprint.SuggestForParam(bp, "overlayGateway", g.Snapshot())
	if err != nil {
		t.Fatalf("SuggestForParam: %v", err)
	}
	if got != "10.100.0.1" {
		t.Fatalf("got %q, want a bare IP with no prefix", got)
	}
}

func TestSuggestForParam_NotEligible(t *testing.T) {
	bp, _ := blueprint.StarterByID(blueprint.StarterSingleNICHomelab)
	g := newGraphWithNodes("pve1")
	if _, err := blueprint.SuggestForParam(bp, "bridgeName", g.Snapshot()); err == nil {
		t.Fatal("expected an error suggesting an address for a non-address-suggest param")
	}
}
