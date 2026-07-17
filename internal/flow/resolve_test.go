package flow

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

func TestGraphResolver_Resolve(t *testing.T) {
	entities := []inventory.Entity{
		&inventory.Bridge{
			Ref:       inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"},
			Name:      "vmbr0",
			Addresses: []string{"10.10.0.0/24"},
		},
		&inventory.SdnVnet{
			Ref:  inventory.Ref{Kind: inventory.KindSDNVnet, ID: "zone1/vnet1"},
			ID:   "vnet1",
			Zone: "zone1",
		},
		&inventory.SdnSubnet{
			Ref:  inventory.Ref{Kind: inventory.KindSDNSubnet, ID: "zone1/vnet1/10.20.0.0-24"},
			ID:   "10.20.0.0/24",
			Vnet: "vnet1",
		},
	}

	r := NewGraphResolver()
	r.Refresh(entities)

	tests := []struct {
		name    string
		ip      string
		wantRef string
		wantOK  bool
	}{
		{"bridge subnet match", "10.10.0.55", "bridge:pve1:vmbr0", true},
		{"sdn subnet match resolves to vnet", "10.20.0.42", "sdn-vnet::zone1/vnet1", true},
		{"no match", "192.168.5.5", "", false},
		{"malformed ip never guessed", "not-an-ip", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, ok := r.Resolve(tt.ip)
			if ok != tt.wantOK {
				t.Fatalf("Resolve(%q) ok = %v, want %v", tt.ip, ok, tt.wantOK)
			}
			if ok && ref != tt.wantRef {
				t.Fatalf("Resolve(%q) = %q, want %q", tt.ip, ref, tt.wantRef)
			}
		})
	}
}

func TestGraphResolver_UnknownVnet_NeverGuesses(t *testing.T) {
	// A subnet naming a vnet this snapshot doesn't carry (e.g. a
	// mid-refresh partial view) must never resolve — no Ref exists to
	// point at, and this package's contract is "never guessed".
	entities := []inventory.Entity{
		&inventory.SdnSubnet{
			Ref:  inventory.Ref{Kind: inventory.KindSDNSubnet, ID: "zone1/vnet1/10.30.0.0-24"},
			ID:   "10.30.0.0/24",
			Vnet: "vnet1",
		},
	}
	r := NewGraphResolver()
	r.Refresh(entities)

	if _, ok := r.Resolve("10.30.0.5"); ok {
		t.Fatal("expected no resolution when the subnet's owning vnet is not in the snapshot")
	}
}

func TestResolveRecord_NilResolverIsNoop(t *testing.T) {
	rec := Record{SrcIP: "10.0.0.1", DstIP: "10.0.0.2"}
	ResolveRecord(nil, &rec)
	if rec.SrcRef != "" || rec.DstRef != "" {
		t.Fatalf("expected no refs set with a nil resolver, got src=%q dst=%q", rec.SrcRef, rec.DstRef)
	}
}

func TestResolveRecord_FillsBothRefs(t *testing.T) {
	entities := []inventory.Entity{
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Addresses: []string{"10.0.0.0/24"}},
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr1"}, Addresses: []string{"10.0.1.0/24"}},
	}
	r := NewGraphResolver()
	r.Refresh(entities)

	rec := Record{SrcIP: "10.0.0.5", DstIP: "10.0.1.5"}
	ResolveRecord(r, &rec)
	if rec.SrcRef != "bridge:pve1:vmbr0" {
		t.Errorf("SrcRef = %q", rec.SrcRef)
	}
	if rec.DstRef != "bridge:pve1:vmbr1" {
		t.Errorf("DstRef = %q", rec.DstRef)
	}
}
