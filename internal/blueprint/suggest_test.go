package blueprint_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/ipam"
)

// fakePicker is a table-driven blueprint.AddressPicker double: allocations
// keys a canned ipam.AllocationList by the exact pool CIDR string
// SuggestAddress passes in, and errs keys a canned error for a pool CIDR
// that should look like an Allocations failure instead. A pool present in
// neither map returns ipam.ErrSubnetNotFound — the "IPAM has never heard
// of this subnet" case a brand-new blueprint subnet hits in practice.
type fakePicker struct {
	allocations map[string]ipam.AllocationList
	errs        map[string]error
}

func (f fakePicker) Allocations(_ context.Context, cidr string) (ipam.AllocationList, error) {
	if err, ok := f.errs[cidr]; ok {
		return ipam.AllocationList{}, err
	}
	if list, ok := f.allocations[cidr]; ok {
		return list, nil
	}
	return ipam.AllocationList{}, ipam.ErrSubnetNotFound
}

// T-603 AC4 (pre-delegation heuristic, still exercised as the fallback
// path with a nil picker): the inventory-only scan skips used and
// network/broadcast addresses.
func TestSuggestAddress_SkipsUsedNetworkAndBroadcast(t *testing.T) {
	g := newGraphWithNodes("pve1")
	applyBridge(g, "pve1", "vmbr0", bridgeOpts{addresses: []string{"192.168.1.1/24", "192.168.1.2/24"}})

	got, err := blueprint.SuggestAddress(context.Background(), nil, g.Snapshot(), "192.168.1.0/24")
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

	_, err := blueprint.SuggestAddress(context.Background(), nil, g.Snapshot(), "10.0.0.0/30")
	if err == nil {
		t.Fatal("expected an error when the pool has no free host addresses")
	}
}

// A nil picker (IPAM not configured for this deployment) must not block a
// suggestion — a blueprint that can't expand because IPAM is absent would
// be a regression.
func TestSuggestAddress_NilPicker_FallsBackToInventoryHeuristic(t *testing.T) {
	g := newGraphWithNodes("pve1")
	applyBridge(g, "pve1", "vmbr0", bridgeOpts{addresses: []string{"192.168.1.1/24"}})

	got, err := blueprint.SuggestAddress(context.Background(), nil, g.Snapshot(), "192.168.1.0/24")
	if err != nil {
		t.Fatalf("SuggestAddress: %v", err)
	}
	if got != "192.168.1.2/24" {
		t.Fatalf("got %q, want 192.168.1.2/24", got)
	}
}

// A configured picker that has never heard of pool (ipam.ErrSubnetNotFound
// — the case for a subnet the blueprint is about to create, which by
// definition PVE/IPAM has no allocations for yet) falls back to the same
// inventory heuristic, unchanged.
func TestSuggestAddress_PickerSubnetNotFound_FallsBackToInventoryHeuristic(t *testing.T) {
	g := newGraphWithNodes("pve1")
	applyBridge(g, "pve1", "vmbr0", bridgeOpts{addresses: []string{"192.168.1.1/24", "192.168.1.2/24"}})
	picker := fakePicker{} // no entry for 192.168.1.0/24 -> ErrSubnetNotFound

	got, err := blueprint.SuggestAddress(context.Background(), picker, g.Snapshot(), "192.168.1.0/24")
	if err != nil {
		t.Fatalf("SuggestAddress: %v", err)
	}
	if got != "192.168.1.3/24" {
		t.Fatalf("got %q, want 192.168.1.3/24 (inventory heuristic result)", got)
	}
}

// A picker error that isn't ErrSubnetNotFound (e.g. PVE momentarily
// unreachable) degrades to the inventory heuristic too, rather than
// failing the suggestion outright.
func TestSuggestAddress_PickerError_FallsBackToInventoryHeuristic(t *testing.T) {
	g := newGraphWithNodes("pve1")
	applyBridge(g, "pve1", "vmbr0", bridgeOpts{addresses: []string{"192.168.1.1/24"}})
	picker := fakePicker{errs: map[string]error{
		"192.168.1.0/24": errors.New("pve: connection refused"),
	}}

	got, err := blueprint.SuggestAddress(context.Background(), picker, g.Snapshot(), "192.168.1.0/24")
	if err != nil {
		t.Fatalf("SuggestAddress: %v", err)
	}
	if got != "192.168.1.2/24" {
		t.Fatalf("got %q, want 192.168.1.2/24", got)
	}
}

// The test that proves the integration was worth doing: an address the
// inventory-only heuristic would suggest (nothing in inventory.Snapshot
// declares it used) is one IPAM knows is actually taken — a PVE-IPAM
// reservation or a guest-agent observation with no matching bridge/vlan
// address in inventory. Delegating to the picker must skip it; the old
// approach could not have known to.
func TestSuggestAddress_DelegatesToIPAM_AvoidsConflictHeuristicMisses(t *testing.T) {
	g := newGraphWithNodes("pve1")
	// Deliberately no bridge/vlan addresses in 192.168.1.0/24 at all: the
	// inventory-only heuristic sees the whole subnet as free and would
	// suggest 192.168.1.1/24, the first usable host address.
	picker := fakePicker{allocations: map[string]ipam.AllocationList{
		"192.168.1.0/24": {
			CIDR:   "192.168.1.0/24",
			Prefix: 24,
			// IPAM knows .1-.4 are occupied (reservations/observations
			// invisible to inventory.Snapshot); its first free range
			// starts at .5.
			FreeRanges: []ipam.FreeRange{{Start: "192.168.1.5", End: "192.168.1.254", Count: 250}},
		},
	}}

	got, err := blueprint.SuggestAddress(context.Background(), picker, g.Snapshot(), "192.168.1.0/24")
	if err != nil {
		t.Fatalf("SuggestAddress: %v", err)
	}
	if got == "192.168.1.1/24" {
		t.Fatalf("suggested %q, the address IPAM knows is taken — delegation did not take effect", got)
	}
	if got != "192.168.1.5/24" {
		t.Fatalf("got %q, want 192.168.1.5/24 (IPAM's first free range)", got)
	}
}

// A picker that resolves pool but reports it full is IPAM's authoritative
// answer, not a "picker doesn't know" case — it must surface as an error,
// never silently fall back to a heuristic that (wrongly) thinks an
// address is free.
func TestSuggestAddress_PickerReportsPoolFull_DoesNotFallBack(t *testing.T) {
	g := newGraphWithNodes("pve1") // no addresses declared: the heuristic alone would find one free
	picker := fakePicker{allocations: map[string]ipam.AllocationList{
		"192.168.1.0/24": {CIDR: "192.168.1.0/24", Prefix: 24, FreeRanges: nil},
	}}

	_, err := blueprint.SuggestAddress(context.Background(), picker, g.Snapshot(), "192.168.1.0/24")
	if err == nil {
		t.Fatal("expected an error when IPAM authoritatively reports the pool full")
	}
}

func TestSuggestForParam_UsesParamSubnet(t *testing.T) {
	bp, ok := blueprint.StarterByID(blueprint.StarterSingleNICHomelab)
	if !ok {
		t.Fatal("starter not found")
	}
	g := newGraphWithNodes("pve1")
	applyBridge(g, "pve1", "vmbr0", bridgeOpts{addresses: []string{"192.168.1.10/24"}})

	got, err := blueprint.SuggestForParam(context.Background(), nil, bp, "mgmtCidr", g.Snapshot())
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

	got, err := blueprint.SuggestForParam(context.Background(), nil, bp, "overlayGateway", g.Snapshot())
	if err != nil {
		t.Fatalf("SuggestForParam: %v", err)
	}
	if got != "10.100.0.1" {
		t.Fatalf("got %q, want a bare IP with no prefix", got)
	}
}

// SuggestForParam also strips the prefix on a delegated ParamIP suggestion
// — the stripping is a formatting step on SuggestAddress's result, not
// something special-cased to the inventory heuristic.
func TestSuggestForParam_ParamIP_StripsPrefix_WhenDelegated(t *testing.T) {
	bp, ok := blueprint.StarterByID(blueprint.StarterVXLANOverlay)
	if !ok {
		t.Fatal("starter not found")
	}
	g := newGraphWithNodes("pve1")
	picker := fakePicker{allocations: map[string]ipam.AllocationList{
		"10.100.0.0/24": {
			CIDR: "10.100.0.0/24", Prefix: 24,
			FreeRanges: []ipam.FreeRange{{Start: "10.100.0.9", End: "10.100.0.254", Count: 246}},
		},
	}}

	got, err := blueprint.SuggestForParam(context.Background(), picker, bp, "overlayGateway", g.Snapshot())
	if err != nil {
		t.Fatalf("SuggestForParam: %v", err)
	}
	if got != "10.100.0.9" {
		t.Fatalf("got %q, want the delegated bare IP 10.100.0.9", got)
	}
}

func TestSuggestForParam_NotEligible(t *testing.T) {
	bp, _ := blueprint.StarterByID(blueprint.StarterSingleNICHomelab)
	g := newGraphWithNodes("pve1")
	if _, err := blueprint.SuggestForParam(context.Background(), nil, bp, "bridgeName", g.Snapshot()); err == nil {
		t.Fatal("expected an error suggesting an address for a non-address-suggest param")
	}
}
