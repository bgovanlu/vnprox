package inventory

import (
	"reflect"
	"testing"
)

func refStrings(rs []Ref) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.String()
	}
	return out
}

// TestDeltaSequence is acceptance criterion #3: a realistic poll sequence
// with exact expected deltas at each step, including a no-op poll yielding an
// empty delta and a single-field change yielding exactly one Updated entity
// with the correct changed-field set.
func TestDeltaSequence(t *testing.T) {
	g := NewGraph()
	node := "pve1"
	eno1 := Ref{Kind: KindPhysNic, Node: node, ID: "eno1"}
	vmbr0 := Ref{Kind: KindBridge, Node: node, ID: "vmbr0"}

	pollA := []Entity{
		&PhysNic{Ref: eno1, Name: "eno1", MTU: 1500, LinkUp: true},
		&Bridge{Ref: vmbr0, Name: "vmbr0", MTU: 1500},
	}

	// Poll A: first poll populates everything -> two Added, nothing else.
	d := g.ApplyPoll(SourceHostNetlink, Scope{Node: node}, pollA)
	if want := []string{"bridge:pve1:vmbr0", "physnic:pve1:eno1"}; !reflect.DeepEqual(refStrings(d.Added), want) {
		t.Errorf("poll A Added = %v, want %v", refStrings(d.Added), want)
	}
	if len(d.Updated) != 0 || len(d.Removed) != 0 {
		t.Errorf("poll A had unexpected updates/removes: %+v", d)
	}

	// Poll B: identical -> empty delta.
	d = g.ApplyPoll(SourceHostNetlink, Scope{Node: node}, clonePoll(pollA))
	if !d.Empty() {
		t.Errorf("poll B (no change) delta not empty: %+v", d)
	}

	// Poll C: change only vmbr0's MTU -> exactly one Updated with field "mtu".
	pollC := []Entity{
		&PhysNic{Ref: eno1, Name: "eno1", MTU: 1500, LinkUp: true},
		&Bridge{Ref: vmbr0, Name: "vmbr0", MTU: 9000},
	}
	d = g.ApplyPoll(SourceHostNetlink, Scope{Node: node}, pollC)
	if want := []string{"bridge:pve1:vmbr0"}; !reflect.DeepEqual(refStrings(d.Updated), want) {
		t.Errorf("poll C Updated = %v, want %v", refStrings(d.Updated), want)
	}
	if len(d.Added) != 0 || len(d.Removed) != 0 {
		t.Errorf("poll C had unexpected adds/removes: %+v", d)
	}
	if got := d.ChangedFields["bridge:pve1:vmbr0"]; !reflect.DeepEqual(got, []string{"mtu"}) {
		t.Errorf("poll C changed fields = %v, want [mtu]", got)
	}

	// Poll D: omit eno1 -> exactly one Removed, bridge unchanged.
	pollD := []Entity{
		&Bridge{Ref: vmbr0, Name: "vmbr0", MTU: 9000},
	}
	d = g.ApplyPoll(SourceHostNetlink, Scope{Node: node}, pollD)
	if want := []string{"physnic:pve1:eno1"}; !reflect.DeepEqual(refStrings(d.Removed), want) {
		t.Errorf("poll D Removed = %v, want %v", refStrings(d.Removed), want)
	}
	if len(d.Added) != 0 || len(d.Updated) != 0 {
		t.Errorf("poll D had unexpected adds/updates: %+v", d)
	}
}

// TestDeltaCrossSourceUpdate checks that a second source enriching an
// existing entity yields an Updated delta naming only the newly-owned fields.
func TestDeltaCrossSourceUpdate(t *testing.T) {
	g := NewGraph()
	node := "pve1"
	vmbr0 := Ref{Kind: KindBridge, Node: node, ID: "vmbr0"}

	g.ApplyPoll(SourceHostNetlink, Scope{Node: node}, []Entity{
		&Bridge{Ref: vmbr0, Name: "vmbr0", MTU: 1500},
	})
	d := g.ApplyPoll(SourcePVENetwork, Scope{Node: node}, []Entity{
		&Bridge{Ref: vmbr0, Name: "vmbr0", MTUDeclared: 9000, Comments: "uplink"},
	})
	if want := []string{"bridge:pve1:vmbr0"}; !reflect.DeepEqual(refStrings(d.Updated), want) {
		t.Fatalf("Updated = %v, want %v", refStrings(d.Updated), want)
	}
	got := d.ChangedFields["bridge:pve1:vmbr0"]
	if !reflect.DeepEqual(got, []string{"comments", "mtuDeclared"}) {
		t.Errorf("changed fields = %v, want [comments mtuDeclared]", got)
	}
}

// TestDeltaSourceScopedRemoval checks a host poll omitting an entity does not
// remove entities owned by a different source/scope (a PVE-cluster node, an
// SDN vnet).
func TestDeltaSourceScopedRemoval(t *testing.T) {
	g := NewGraph()
	g.ApplyPoll(SourcePVESDN, Scope{}, []Entity{
		&SdnZone{Ref: Ref{Kind: KindSDNZone, ID: "z1"}, ID: "z1"},
	})
	g.ApplyPoll(SourceHostNetlink, Scope{Node: "pve1"}, []Entity{
		&Bridge{Ref: Ref{Kind: KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0"},
	})
	// A host poll of pve1 that finds no bridges must not touch the SDN zone.
	d := g.ApplyPoll(SourceHostNetlink, Scope{Node: "pve1"}, nil)
	if want := []string{"bridge:pve1:vmbr0"}; !reflect.DeepEqual(refStrings(d.Removed), want) {
		t.Errorf("Removed = %v, want %v", refStrings(d.Removed), want)
	}
	if _, ok := g.Snapshot().Get(Ref{Kind: KindSDNZone, ID: "z1"}); !ok {
		t.Errorf("SDN zone wrongly removed by host poll")
	}
}

func clonePoll(es []Entity) []Entity {
	out := make([]Entity, len(es))
	for i, e := range es {
		out[i] = e.clone()
	}
	return out
}
