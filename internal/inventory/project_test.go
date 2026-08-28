// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"testing"
)

// projectBase builds a two-source graph: a bridge with one port, so linkAll has
// derived Ref fields to resolve and the merge has both a runtime and a declared
// contributor (which is what gives every ref a real provenance entry).
func projectBase(t *testing.T) Snapshot {
	t.Helper()
	g := NewGraph()
	ents := []Entity{
		&PhysNic{Ref: Ref{Kind: KindPhysNic, Node: "pve1", ID: "eno1"}, Name: "eno1", LinkUp: true, LinkUpSet: true},
		&Bridge{
			Ref: Ref{Kind: KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0", Virt: BridgeLinux,
			PortNames: []string{"eno1"}, DeclaredPortNames: []string{"eno1"},
			MTU: 1500, MTUDeclared: 1500,
		},
	}
	g.ApplyPoll(SourceHostNetlink, Scope{}, ents)
	g.ApplyPoll(SourceHostInterfaces, Scope{}, ents)
	return g.Snapshot()
}

// The projection must not write through to the base snapshot's entities.
// linkAll resolves Bridge.Ports IN PLACE, so a ProjectSnapshot that skipped the
// clone would rewrite the live graph as a side effect of rendering a
// hypothetical one — the single most dangerous thing this function could do.
func TestProjectSnapshot_ClonesRatherThanWritingThroughToTheBase(t *testing.T) {
	base := projectBase(t)
	bridgeRef := Ref{Kind: KindBridge, Node: "pve1", ID: "vmbr0"}
	live, ok := base.Get(bridgeRef)
	if !ok {
		t.Fatal("base is missing vmbr0")
	}
	liveBridge, ok := live.(*Bridge)
	if !ok {
		t.Fatalf("vmbr0 resolved to %T, want *Bridge", live)
	}
	if len(liveBridge.Ports) != 1 {
		t.Fatalf("base bridge has %d ports, want 1", len(liveBridge.Ports))
	}

	// Project WITHOUT the NIC: relinking now resolves zero ports.
	var kept []Entity
	for _, e := range base.All() {
		if e.GetRef().Kind != KindPhysNic {
			kept = append(kept, e)
		}
	}
	projected := ProjectSnapshot(base, kept, nil)

	pb, ok := projected.Get(bridgeRef)
	if !ok {
		t.Fatal("projection is missing vmbr0")
	}
	if got, isBridge := pb.(*Bridge); !isBridge || len(got.Ports) != 0 {
		t.Errorf("projected bridge ports = %+v, want none (its port was projected away)", pb)
	}
	if len(liveBridge.Ports) != 1 {
		t.Errorf("the LIVE bridge's ports were rewritten to %+v by projecting; it must be untouched", liveBridge.Ports)
	}
	if _, still := base.Get(Ref{Kind: KindPhysNic, Node: "pve1", ID: "eno1"}); !still {
		t.Error("the live snapshot lost an entity the projection dropped")
	}
}

// Provenance is carried for the refs the caller says the projection left alone,
// and withheld for every other — an entity a projection invented or edited has
// been observed by no collector, and saying otherwise would be a fabrication.
func TestProjectSnapshot_CarriesProvenanceOnlyForUntouchedRefs(t *testing.T) {
	base := projectBase(t)
	nicRef := Ref{Kind: KindPhysNic, Node: "pve1", ID: "eno1"}
	bridgeRef := Ref{Kind: KindBridge, Node: "pve1", ID: "vmbr0"}

	projected := ProjectSnapshot(base, base.All(), map[Ref]bool{nicRef: true})

	if _, ok := projected.Provenance(nicRef); !ok {
		t.Error("provenance was dropped for a ref the caller marked untouched")
	}
	if prov, ok := projected.Provenance(bridgeRef); ok && len(prov.Fields) > 0 {
		t.Errorf("provenance was invented for a touched ref: %+v", prov.Fields)
	}
}

// The projection describes the same instant of observation as the snapshot it
// was folded from — it is a hypothetical, not a fresher reading of the cluster.
func TestProjectSnapshot_InheritsTheBaseObservationInstant(t *testing.T) {
	base := projectBase(t)
	projected := ProjectSnapshot(base, base.All(), nil)

	if !projected.GeneratedAt().Equal(base.GeneratedAt()) {
		t.Errorf("generatedAt = %v, want the base's %v", projected.GeneratedAt(), base.GeneratedAt())
	}
	if projected.Seq() != base.Seq() {
		t.Errorf("seq = %d, want the base's %d", projected.Seq(), base.Seq())
	}
	if projected.Len() != base.Len() {
		t.Errorf("entity count = %d, want %d", projected.Len(), base.Len())
	}
}

// A zero Snapshot is a legitimate base (a daemon that has never polled). It
// must not panic.
func TestProjectSnapshot_ZeroBaseIsSafe(t *testing.T) {
	projected := ProjectSnapshot(Snapshot{}, []Entity{
		&Bridge{Ref: Ref{Kind: KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0"},
	}, map[Ref]bool{{Kind: KindBridge, Node: "pve1", ID: "vmbr0"}: true})

	if projected.Len() != 1 {
		t.Errorf("entity count = %d, want 1", projected.Len())
	}
}
