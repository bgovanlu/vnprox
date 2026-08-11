package spec_test

// T-2703's document-side half: RemoveEntities (the delete ApplyOps cannot
// express) and AdoptEntities (the "adopt reality" rendering), asserted through
// the property that matters — Import against the same live snapshot emits no
// op for an adopted ref — rather than through the document's text.

import (
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/spec"
)

// adoptGraph builds a small graph with declared (pve-network) and, optionally,
// runtime (host-netlink) contributions, mirroring internal/drift's own test
// helpers.
func adoptGraph(t *testing.T) *inventory.Graph {
	t.Helper()
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", Status: "online"},
	})
	return g
}

// declaredBridge is one bridge as the interfaces file declares it.
type declaredBridge struct {
	name  string
	ports []string
	mtu   int
}

// declareBridges applies every bridge in ONE poll: an ApplyPoll replaces the
// whole (source, scope) entity set, so applying them one at a time would leave
// only the last one in the graph.
func declareBridges(g *inventory.Graph, node string, bridges ...declaredBridge) {
	ents := make([]inventory.Entity, 0, len(bridges))
	for _, b := range bridges {
		ents = append(ents, &inventory.Bridge{
			Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: b.name},
			Name: b.name, Virt: inventory.BridgeLinux,
			MTUDeclared: b.mtu, DeclaredPortNames: b.ports,
		})
	}
	g.ApplyPoll(inventory.SourcePVENetwork,
		inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBridge}}, ents)
}

// opsFor returns the ops in a plan that target ref.
func opsFor(t *testing.T, s spec.Spec, snap inventory.Snapshot, ref inventory.Ref) int {
	t.Helper()
	ops, _, err := spec.Import(s, snap)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	n := 0
	for _, op := range ops {
		if op.Target == ref {
			n++
		}
	}
	return n
}

// TestAdoptEntities_ConvergesTheAdoptedRef is the property AC1 rests on: after
// adopting, the document plans to nothing for that entity against the very
// snapshot it was adopted from.
func TestAdoptEntities_ConvergesTheAdoptedRef(t *testing.T) {
	g := adoptGraph(t)
	declareBridges(g, "pve1", declaredBridge{name: "vmbr0", mtu: 1500, ports: []string{"eno1"}})
	snap := g.Snapshot()
	ref := bridgeRef("pve1", "vmbr0")

	base := spec.Spec{SpecVersion: 1, Nodes: []spec.NodeSpec{{
		Name:    "pve1",
		Bridges: []spec.BridgeSpec{{Name: "vmbr0", MTU: 9000, Ports: []string{"eno1", "eno2"}}},
	}}}
	if got := opsFor(t, base, snap, ref); got == 0 {
		t.Fatalf("control failed: the base spec was supposed to diverge from live for %s", ref)
	}

	adopted, err := spec.AdoptEntities(base, []inventory.Ref{ref}, snap)
	if err != nil {
		t.Fatalf("AdoptEntities: %v", err)
	}
	if got := opsFor(t, adopted, snap, ref); got != 0 {
		t.Errorf("after adopting %s the plan still has %d op(s) for it", ref, got)
	}
	// The whole plan is empty here, because that one bridge was the only
	// divergence — AC1's "re-imports to a plan that is empty against current
	// live" in its strongest form.
	ops, notInSpec, err := spec.Import(adopted, snap)
	if err != nil {
		t.Fatalf("Import(adopted): %v", err)
	}
	if len(ops) != 0 {
		t.Errorf("plan after adoption = %d op(s), want 0: %+v", len(ops), ops)
	}
	if len(notInSpec) != 0 {
		t.Errorf("notInSpec after adoption = %v, want none", notInSpec)
	}
	// base is untouched.
	if base.Nodes[0].Bridges[0].MTU != 9000 {
		t.Errorf("AdoptEntities wrote through into its caller's spec (base MTU = %d)", base.Nodes[0].Bridges[0].MTU)
	}
}

// TestAdoptEntities_LiveNoLongerHasIt_RemovesTheDeclaration is the case
// ApplyOps explicitly cannot express: the document declares an entity the
// cluster no longer has, so adopting reality means deleting the declaration.
func TestAdoptEntities_LiveNoLongerHasIt_RemovesTheDeclaration(t *testing.T) {
	g := adoptGraph(t)
	declareBridges(g, "pve1", declaredBridge{name: "vmbr0", mtu: 1500})
	snap := g.Snapshot()
	gone := bridgeRef("pve1", "vmbr9")

	base := spec.Spec{SpecVersion: 1, Nodes: []spec.NodeSpec{{
		Name: "pve1",
		Bridges: []spec.BridgeSpec{
			{Name: "vmbr0", MTU: 1500},
			{Name: "vmbr9", MTU: 1500},
		},
	}}}
	if got := opsFor(t, base, snap, gone); got != 1 {
		t.Fatalf("control failed: the base spec should plan one create for the absent %s, got %d", gone, got)
	}

	adopted, err := spec.AdoptEntities(base, []inventory.Ref{gone}, snap)
	if err != nil {
		t.Fatalf("AdoptEntities: %v", err)
	}
	if got := opsFor(t, adopted, snap, gone); got != 0 {
		t.Errorf("after adopting the absence of %s the plan still has %d op(s) for it", gone, got)
	}
	if len(adopted.Nodes) != 1 || len(adopted.Nodes[0].Bridges) != 1 || adopted.Nodes[0].Bridges[0].Name != "vmbr0" {
		t.Errorf("adopted document = %+v, want pve1 declaring vmbr0 only", adopted.Nodes)
	}
}

// TestAdoptEntities_LeavesOtherEntitiesAlone: adoption is per-entity. A
// finding about one bridge must not quietly rewrite the rest of the cluster's
// intent.
func TestAdoptEntities_LeavesOtherEntitiesAlone(t *testing.T) {
	g := adoptGraph(t)
	declareBridges(g, "pve1", declaredBridge{name: "vmbr0", mtu: 1500}, declaredBridge{name: "vmbr1", mtu: 1500})
	snap := g.Snapshot()

	base := spec.Spec{SpecVersion: 1, Nodes: []spec.NodeSpec{{
		Name: "pve1",
		Bridges: []spec.BridgeSpec{
			{Name: "vmbr0", MTU: 9000},
			{Name: "vmbr1", MTU: 9000},
		},
	}}}
	adopted, err := spec.AdoptEntities(base, []inventory.Ref{bridgeRef("pve1", "vmbr0")}, snap)
	if err != nil {
		t.Fatalf("AdoptEntities: %v", err)
	}
	if got := opsFor(t, adopted, snap, bridgeRef("pve1", "vmbr0")); got != 0 {
		t.Errorf("vmbr0 was adopted but still plans %d op(s)", got)
	}
	if got := opsFor(t, adopted, snap, bridgeRef("pve1", "vmbr1")); got == 0 {
		t.Errorf("vmbr1 was NOT adopted but no longer plans anything — adoption widened past the ref it was given")
	}
}

// TestAdoptEntities_UnadoptableKind refuses a ref the document has no
// vocabulary for, naming it.
func TestAdoptEntities_UnadoptableKind(t *testing.T) {
	g := adoptGraph(t)
	snap := g.Snapshot()
	nic := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}

	if _, err := spec.AdoptEntities(spec.Spec{SpecVersion: 1}, []inventory.Ref{nic}, snap); !errors.Is(err, spec.ErrKindNotAdoptable) {
		t.Errorf("AdoptEntities(physnic) error = %v, want ErrKindNotAdoptable", err)
	}
	if _, err := spec.RemoveEntities(spec.Spec{SpecVersion: 1}, []inventory.Ref{nic}); !errors.Is(err, spec.ErrKindNotAdoptable) {
		t.Errorf("RemoveEntities(physnic) error = %v, want ErrKindNotAdoptable", err)
	}
}

// TestRemoveEntities covers the companion on its own: removing what is there,
// removing what is not, pruning a node the removal emptied, and leaving a node
// the base document carried empty on purpose.
func TestRemoveEntities(t *testing.T) {
	base := spec.Spec{SpecVersion: 1, Nodes: []spec.NodeSpec{
		{Name: "pve1", Bridges: []spec.BridgeSpec{{Name: "vmbr0"}}},
		{Name: "pve2", Bonds: []spec.BondSpec{{Name: "bond0"}}, Bridges: []spec.BridgeSpec{{Name: "vmbr0"}}},
		{Name: "pve3"},
	}, SDN: &spec.SDNSpec{Zones: []spec.ZoneSpec{{ID: "zone1", Type: "vlan"}, {ID: "zone2", Type: "vlan"}}}}

	got, err := spec.RemoveEntities(base, []inventory.Ref{
		bridgeRef("pve1", "vmbr0"),
		bridgeRef("pve2", "vmbrX"), // not declared: a no-op, not an error
		{Kind: inventory.KindSDNZone, ID: "zone1"},
	})
	if err != nil {
		t.Fatalf("RemoveEntities: %v", err)
	}

	names := make([]string, 0, len(got.Nodes))
	for _, n := range got.Nodes {
		names = append(names, n.Name)
	}
	// pve1 was emptied by the removal and is pruned; pve3 was empty in the
	// base document and is left exactly as the operator wrote it.
	if len(names) != 2 || names[0] != "pve2" || names[1] != "pve3" {
		t.Errorf("nodes after removal = %v, want [pve2 pve3]", names)
	}
	if got.SDN == nil || len(got.SDN.Zones) != 1 || got.SDN.Zones[0].ID != "zone2" {
		t.Errorf("sdn zones after removal = %+v, want zone2 only", got.SDN)
	}
	if len(base.Nodes) != 3 || len(base.Nodes[0].Bridges) != 1 {
		t.Errorf("RemoveEntities wrote through into its caller's spec: %+v", base.Nodes)
	}
}

// TestRemoveEntities_LastSDNObject drops the sdn section entirely rather than
// leaving an empty stanza behind.
func TestRemoveEntities_LastSDNObject(t *testing.T) {
	base := spec.Spec{SpecVersion: 1, SDN: &spec.SDNSpec{Zones: []spec.ZoneSpec{{ID: "zone1", Type: "vlan"}}}}
	got, err := spec.RemoveEntities(base, []inventory.Ref{{Kind: inventory.KindSDNZone, ID: "zone1"}})
	if err != nil {
		t.Fatalf("RemoveEntities: %v", err)
	}
	if got.SDN != nil {
		t.Errorf("sdn section after removing its last object = %+v, want nil", got.SDN)
	}
}
