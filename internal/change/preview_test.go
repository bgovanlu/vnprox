// SPDX-License-Identifier: Apache-2.0

package change

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// previewSnapshot builds a snapshot the way a real poll does: the same entity
// set contributed by BOTH the runtime source and the declared-config source, so
// runtime fields (link state, programmed VLAN table) and declared fields
// (addresses, declared MTU) both survive the ownership merge. buildSnapshot's
// single host-netlink poll would silently drop every declared field
// (merge.go's ownershipRules), which would make a projection of an ADDRESS
// change unobservable in exactly the test meant to observe it.
func previewSnapshot(entities ...inventory.Entity) inventory.Snapshot {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, entities)
	g.ApplyPoll(inventory.SourceHostInterfaces, inventory.Scope{}, entities)
	return g.Snapshot()
}

// previewFixture: one node, one NIC pair, a bridge carrying an address with a
// guest on it, and a spare NIC to enslave.
func previewFixture() inventory.Snapshot {
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")
	guest := testRef(inventory.KindGuest, "pve1", "100")
	return previewSnapshot(
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", Status: "online"},
		&inventory.PhysNic{
			Ref: testRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1",
			Mac: "aa:bb:cc:dd:ee:01", LinkUp: true, LinkUpSet: true, MTU: 1500, MTUDeclared: 1500,
		},
		&inventory.PhysNic{
			Ref: testRef(inventory.KindPhysNic, "pve1", "eno2"), Name: "eno2",
			Mac: "aa:bb:cc:dd:ee:02", LinkUp: true, LinkUpSet: true, MTU: 1500,
		},
		&inventory.PhysNic{
			Ref: testRef(inventory.KindPhysNic, "pve1", "eno3"), Name: "eno3",
			Mac: "aa:bb:cc:dd:ee:03", LinkUp: true, LinkUpSet: true, MTU: 1500,
		},
		&inventory.PhysNic{
			Ref: testRef(inventory.KindPhysNic, "pve1", "eno4"), Name: "eno4",
			Mac: "aa:bb:cc:dd:ee:04", LinkUp: true, LinkUpSet: true, MTU: 1500,
		},
		// A bond is in the fixture specifically so the total-equality check in
		// AC6 covers PROVENANCE too: topology.Project paints a bond's status
		// from whether the "slaves" field was ever observed (project.go's
		// bondStatus), so a projection that dropped provenance would repaint
		// every bond grey — a difference no entity-field comparison would see.
		&inventory.Bond{
			Ref: testRef(inventory.KindBond, "pve1", "bond0"), Name: "bond0", Mode: "802.3ad",
			Slaves: []string{"eno3", "eno4"}, DeclaredSlaves: []string{"eno3", "eno4"},
			SlaveDetail: []inventory.BondSlaveState{
				{Name: "eno3", MIIStatus: "up", Active: true},
				{Name: "eno4", MIIStatus: "up", Active: true},
			},
			MTU: 1500, MTUDeclared: 1500,
		},
		&inventory.Bridge{
			Ref: vmbr0, Name: "vmbr0", Virt: inventory.BridgeLinux,
			PortNames: []string{"eno1"}, DeclaredPortNames: []string{"eno1"},
			Addresses: []string{"10.0.0.10/24"}, Gateway: "10.0.0.1",
			MTU: 1500, MTUDeclared: 1500,
		},
		// vmbr1 carries the bond as a port. It is here so a test can delete an
		// entity that a bridge the changeset never mentions REFERENCES: linkAll
		// rewrites Bridge.Ports in place, so a projection that failed to clone
		// would truncate this live bridge's port list as a side effect of
		// rendering a preview.
		&inventory.Bridge{
			Ref: testRef(inventory.KindBridge, "pve1", "vmbr1"), Name: "vmbr1", Virt: inventory.BridgeLinux,
			PortNames: []string{"bond0"}, DeclaredPortNames: []string{"bond0"},
			MTU: 1500, MTUDeclared: 1500,
		},
		&inventory.Guest{Ref: guest, Name: "web01", Type: "qemu", Node: "pve1", VMID: 100, Status: "running"},
		&inventory.GuestNic{
			Ref: testRef(inventory.KindGuestNic, "pve1", "100/net0"), Guest: guest,
			Key: "net0", TargetName: "vmbr0", Model: "virtio", Mac: "de:ad:be:ef:00:01",
		},
	)
}

// snapshotFingerprint is a TOTAL rendering of everything a snapshot can be
// asked for: every entity's full JSON, every edge, and the complete rendered
// topology (which is what carries provenance's observable effect — status
// painting reads it). Comparing fingerprints compares snapshots in full rather
// than sampling a field or two.
func snapshotFingerprint(t *testing.T, snap inventory.Snapshot) string {
	t.Helper()
	type entityRow struct {
		Entity inventory.Entity
		Ref    string
	}
	rows := make([]entityRow, 0, snap.Len())
	for _, e := range snap.All() {
		rows = append(rows, entityRow{Ref: e.GetRef().String(), Entity: e})
	}
	payload := struct {
		Entities []entityRow
		Edges    []inventory.Edge
		Topology topology.Topology
	}{Entities: rows, Edges: snap.Edges(), Topology: topology.Project(snap, topology.Filter{})}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshaling snapshot fingerprint: %v", err)
	}
	return string(b)
}

// previewInventory is the InventorySource seam over one fixed snapshot.
type previewInventory struct{ snap inventory.Snapshot }

func (p previewInventory) Snapshot() inventory.Snapshot { return p.snap }

// newPreviewService wires a real Service (real store, real validation) over a
// fixed inventory snapshot, so the service-level tests exercise the actual
// validate-then-project sequence rather than a stub of it.
func newPreviewService(t *testing.T, snap inventory.Snapshot) *Service {
	t.Helper()
	db := openTestDB(t)
	svc, err := NewService(Config{
		Changesets: store.NewChangesetRepo(db),
		Audit:      store.NewAuditRepo(db),
		Inventory:  previewInventory{snap: snap},
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func changeFor(t *testing.T, p Preview, ref string) PreviewChange {
	t.Helper()
	for _, c := range p.Changes {
		if c.Ref == ref {
			return c
		}
	}
	t.Fatalf("no change reported for %s; changes = %+v", ref, p.Changes)
	return PreviewChange{}
}

func topologyHasNode(topo topology.Topology, id string) bool {
	for _, n := range topo.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

// --- AC1 ------------------------------------------------------------------

// AC1: a changeset creating a bridge produces a projected snapshot containing
// it, marked added — and the live snapshot is unchanged, asserted by RE-READING
// it rather than by trusting that the projection returned a copy.
func TestPreview_CreatedBridgeIsProjectedAndMarkedAdded(t *testing.T) {
	snap := previewFixture()
	before := snapshotFingerprint(t, snap)

	vmbr9 := testRef(inventory.KindBridge, "pve1", "vmbr9")
	ops := []Op{mkOp(OpBridgeCreate, vmbr9, &BridgeCreateParams{
		Ports: []string{"eno2"}, Addresses: []string{"10.9.0.1/24"}, MTU: 9000, VlanAware: true,
	})}

	preview, err := ComputePreview(ops, snap)
	if err != nil {
		t.Fatalf("ComputePreview: %v", err)
	}

	if !topologyHasNode(preview.Topology, vmbr9.String()) {
		t.Fatalf("the projected topology does not contain the created bridge %s", vmbr9)
	}
	got := changeFor(t, preview, vmbr9.String())
	if got.Change != topology.DiffAdded {
		t.Errorf("change for the created bridge = %q, want %q", got.Change, topology.DiffAdded)
	}
	if len(got.Fields) == 0 {
		t.Error("the created bridge came back with no field detail; an added entity should carry what it was created with")
	}

	// The port it enslaves must resolve into a real edge, or the preview shows
	// a bridge floating unattached — the thing the operator is looking at the
	// map to check.
	var attached bool
	for _, e := range preview.Topology.Edges {
		if e.From == testRef(inventory.KindPhysNic, "pve1", "eno2").String() && e.To == vmbr9.String() {
			attached = true
		}
	}
	if !attached {
		t.Errorf("no port-of edge from eno2 to the created bridge; edges = %+v", preview.Topology.Edges)
	}

	// Re-read the live snapshot: it must be byte-for-byte what it was.
	if after := snapshotFingerprint(t, snap); after != before {
		t.Error("the live snapshot changed while projecting; a preview must never write through to the graph")
	}
	if _, exists := snap.Get(vmbr9); exists {
		t.Error("the created bridge leaked into the LIVE snapshot")
	}
}

// The live graph is not merely unmodified through the Snapshot handle — the
// entity objects themselves must be untouched, since linkAll resolves derived
// Ref fields IN PLACE. A projection that forgot to clone would rewrite the live
// bridge's Ports as a side effect of rendering the preview.
func TestPreview_ProjectionDoesNotWriteThroughToLiveEntities(t *testing.T) {
	snap := previewFixture()
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")
	// vmbr1 is the interesting one: the changeset never mentions it, but it
	// carries the bond the changeset deletes, so relinking the projection
	// recomputes ITS port list. If the projection did not clone, that
	// recomputation would land on the live entity.
	vmbr1 := testRef(inventory.KindBridge, "pve1", "vmbr1")

	watched := map[inventory.Ref]string{}
	for _, ref := range []inventory.Ref{vmbr0, vmbr1} {
		e, ok := snap.Get(ref)
		if !ok {
			t.Fatalf("fixture is missing %s", ref)
		}
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		watched[ref] = string(b)
	}

	ops := []Op{
		mkOp(OpBridgePortAdd, vmbr0, &BridgePortAddParams{Port: "eno2"}),
		mkOp(OpBridgeUpdate, vmbr0, &BridgeUpdateParams{Addresses: strsPtr("10.0.0.11/24")}),
		mkOp(OpBondDelete, testRef(inventory.KindBond, "pve1", "bond0"), &BondDeleteParams{}),
	}
	if _, err := ComputePreview(ops, snap); err != nil {
		t.Fatalf("ComputePreview: %v", err)
	}

	for ref, beforeJSON := range watched {
		e, ok := snap.Get(ref)
		if !ok {
			t.Fatalf("%s vanished from the live snapshot", ref)
		}
		afterJSON, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(afterJSON) != beforeJSON {
			t.Errorf("the live entity %s was mutated by the projection:\n before %s\n  after %s",
				ref, beforeJSON, afterJSON)
		}
	}
}

// --- AC2 ------------------------------------------------------------------

// AC2: a changeset deleting an entity marks it removed rather than omitting it.
func TestPreview_DeletedEntityIsMarkedRemovedNotOmitted(t *testing.T) {
	snap := previewFixture()
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")

	preview, err := ComputePreview([]Op{mkOp(OpBridgeDelete, vmbr0, &BridgeDeleteParams{})}, snap)
	if err != nil {
		t.Fatalf("ComputePreview: %v", err)
	}

	got := changeFor(t, preview, vmbr0.String())
	if got.Change != topology.DiffRemoved {
		t.Errorf("change for the deleted bridge = %q, want %q", got.Change, topology.DiffRemoved)
	}
	if got.Name != "vmbr0" {
		t.Errorf("removed entity name = %q, want vmbr0 — a removal must still say what it was", got.Name)
	}
	if topologyHasNode(preview.Topology, vmbr0.String()) {
		t.Error("the deleted bridge is still a node in the projected topology")
	}
}

// --- AC3 ------------------------------------------------------------------

// AC3: an unprojectable op is listed BY NAME with a reason, never silently
// dropped. The card's named case: a raw /etc/network/interfaces edit.
func TestPreview_RawInterfacesEditIsDisclosedAsUnprojectable(t *testing.T) {
	snap := previewFixture()
	node := testRef(inventory.KindNode, "pve1", "pve1")
	vmbr9 := testRef(inventory.KindBridge, "pve1", "vmbr9")

	ops := []Op{
		mkOp(OpBridgeCreate, vmbr9, &BridgeCreateParams{}),
		mkOp(OpIfaceRawReplace, node, &IfaceRawReplaceParams{Content: "auto lo\niface lo inet loopback\n"}),
	}
	ops[1].ID = "op-raw-1"

	preview, err := ComputePreview(ops, snap)
	if err != nil {
		t.Fatalf("ComputePreview: %v", err)
	}

	if len(preview.Unprojectable) != 1 {
		t.Fatalf("unprojectable = %+v, want exactly the raw edit", preview.Unprojectable)
	}
	got := preview.Unprojectable[0]
	if got.Op != string(OpIfaceRawReplace) {
		t.Errorf("unprojectable op = %q, want %q", got.Op, OpIfaceRawReplace)
	}
	if got.OpID != "op-raw-1" {
		t.Errorf("unprojectable opId = %q, want op-raw-1 — the disclosure must name the op the operator staged", got.OpID)
	}
	if got.Target != node.String() {
		t.Errorf("unprojectable target = %q, want %q", got.Target, node)
	}
	if strings.TrimSpace(got.Reason) == "" {
		t.Error("the raw edit was disclosed with no reason; an unexplained gap is what this list exists to prevent")
	}
	if !strings.Contains(got.Reason, "interfaces") {
		t.Errorf("reason %q does not say what about the op cannot be projected", got.Reason)
	}
	if !preview.BestEffort {
		t.Error("bestEffort must be true; the response has to say out loud that this is a projection")
	}
	// The op it could project still projected: disclosure is not a bail-out.
	if !topologyHasNode(preview.Topology, vmbr9.String()) {
		t.Error("the projectable op in the same changeset was dropped alongside the unprojectable one")
	}
}

// The disclosure list must stay honest as the op vocabulary grows: every op
// type either has a projection rule in this file or is disclosed with a
// non-empty reason. Nothing may be silently ignored.
func TestPreview_EveryOpTypeIsProjectedOrDisclosed(t *testing.T) {
	for opType, factory := range paramFactories {
		reason, declared := unprojectableReasons[opType]
		if declared {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("op %s is declared unprojectable with an empty reason", opType)
			}
			continue
		}
		p := newEntityProjection(previewFixture())
		op := Op{Type: opType, Target: testRef(inventory.KindBridge, "pve1", "vmbrX"), Params: factory()}
		if got := p.projectOp(op); got != "" {
			t.Errorf("op %s is neither projected nor declared unprojectable: fell through to %q", opType, got)
		}
	}
}

// The reasons are statements about the op, not about vnprox's completeness. A
// reason that says "not implemented" trains operators to ignore the list.
func TestPreview_UnprojectableReasonsExplainTheOpNotTheGap(t *testing.T) {
	for opType, reason := range unprojectableReasons {
		lower := strings.ToLower(reason)
		for _, bad := range []string{"not implemented", "unsupported", "todo", "not supported yet"} {
			if strings.Contains(lower, bad) {
				t.Errorf("op %s's reason %q describes vnprox rather than the op", opType, reason)
			}
		}
	}
}

// --- AC6 ------------------------------------------------------------------

// AC6: projecting the empty changeset yields the live snapshot EXACTLY. The
// equality is total — every entity's full JSON, every edge, and the complete
// rendered topology — not a sampled field.
func TestPreview_EmptyChangesetProjectsTheLiveSnapshotExactly(t *testing.T) {
	snap := previewFixture()

	projected, unprojectable := ProjectOps(nil, snap)
	if len(unprojectable) != 0 {
		t.Errorf("the empty changeset reported unprojectable ops: %+v", unprojectable)
	}
	if got, want := snapshotFingerprint(t, projected), snapshotFingerprint(t, snap); got != want {
		t.Errorf("projection of the empty changeset differs from the live snapshot:\n got %s\nwant %s", got, want)
	}
	if projected.Len() != snap.Len() {
		t.Errorf("projected entity count = %d, want %d", projected.Len(), snap.Len())
	}

	preview, err := ComputePreview(nil, snap)
	if err != nil {
		t.Fatalf("ComputePreview: %v", err)
	}
	if len(preview.Changes) != 0 {
		t.Errorf("the empty changeset reported changes: %+v", preview.Changes)
	}
	if got, want := preview.Topology, topology.Project(snap, topology.Filter{}); !jsonEqual(t, got, want) {
		t.Error("the empty changeset's projected topology differs from the live topology")
	}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	ab, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	bb, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(ab) == string(bb)
}

// --- the rest of the projection vocabulary --------------------------------

func TestPreview_PortAddAndRemoveMoveTheEdge(t *testing.T) {
	snap := previewFixture()
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")
	eno1 := testRef(inventory.KindPhysNic, "pve1", "eno1").String()
	eno2 := testRef(inventory.KindPhysNic, "pve1", "eno2").String()

	preview, err := ComputePreview([]Op{
		mkOp(OpBridgePortAdd, vmbr0, &BridgePortAddParams{Port: "eno2"}),
		mkOp(OpBridgePortRemove, vmbr0, &BridgePortRemoveParams{Port: "eno1"}),
	}, snap)
	if err != nil {
		t.Fatalf("ComputePreview: %v", err)
	}

	var hasEno2, hasEno1 bool
	for _, e := range preview.Topology.Edges {
		if e.To != vmbr0.String() {
			continue
		}
		switch e.From {
		case eno1:
			hasEno1 = true
		case eno2:
			hasEno2 = true
		}
	}
	if !hasEno2 {
		t.Error("the added port produced no edge in the projected topology")
	}
	if hasEno1 {
		t.Error("the removed port still has an edge in the projected topology")
	}
	if got := changeFor(t, preview, vmbr0.String()); got.Change != topology.DiffModified {
		t.Errorf("port membership change = %q, want %q", got.Change, topology.DiffModified)
	}
}

func TestPreview_GuestReattachFollowsTheGuestToItsNewCarrier(t *testing.T) {
	snap := previewFixture()
	vmbr9 := testRef(inventory.KindBridge, "pve1", "vmbr9")
	nic := testRef(inventory.KindGuestNic, "pve1", "100/net0")

	preview, err := ComputePreview([]Op{
		mkOp(OpBridgeCreate, vmbr9, &BridgeCreateParams{Ports: []string{"eno2"}}),
		mkOp(OpGuestNicUpdate, nic, &GuestNicUpdateParams{BridgeOrVnet: strPtr("vmbr9")}),
	}, snap)
	if err != nil {
		t.Fatalf("ComputePreview: %v", err)
	}

	var attachedTo string
	for _, e := range preview.Topology.Edges {
		if e.From == nic.String() && e.Kind == string(inventory.EdgeAttachedTo) {
			attachedTo = e.To
		}
	}
	if attachedTo != vmbr9.String() {
		t.Errorf("guest NIC is attached to %q, want the bridge this changeset creates (%s)", attachedTo, vmbr9)
	}
}

func TestPreview_RenameMovesTheEntityAndItsReferences(t *testing.T) {
	snap := previewFixture()
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")
	renamed := testRef(inventory.KindBridge, "pve1", "vmbrmgmt")

	preview, err := ComputePreview([]Op{
		mkOp(OpIfaceRename, vmbr0, &IfaceRenameParams{NewName: "vmbrmgmt"}),
	}, snap)
	if err != nil {
		t.Fatalf("ComputePreview: %v", err)
	}

	if got := changeFor(t, preview, vmbr0.String()); got.Change != topology.DiffRemoved {
		t.Errorf("old identity = %q, want %q", got.Change, topology.DiffRemoved)
	}
	if got := changeFor(t, preview, renamed.String()); got.Change != topology.DiffAdded {
		t.Errorf("new identity = %q, want %q", got.Change, topology.DiffAdded)
	}
	var portMoved bool
	for _, e := range preview.Topology.Edges {
		if e.To == renamed.String() && e.From == testRef(inventory.KindPhysNic, "pve1", "eno1").String() {
			portMoved = true
		}
	}
	if !portMoved {
		t.Error("the renamed bridge lost its port; the rename did not follow in-file references")
	}
	// The guest's own PVE config is NOT rewritten by an interfaces rename, so
	// the preview must show it dangling rather than quietly re-pointing it.
	for _, e := range preview.Topology.Edges {
		if e.From == testRef(inventory.KindGuestNic, "pve1", "100/net0").String() && e.Kind == string(inventory.EdgeAttachedTo) {
			t.Errorf("the guest NIC was silently re-pointed at %s by a rename; it would really be left dangling", e.To)
		}
	}
}

// A rename must follow every same-node reference to the old name, or the
// preview shows the renamed interface orphaned from things that still carry it.
func TestPreview_RenameRewritesReferencesOnOtherEntities(t *testing.T) {
	snap := previewFixture()
	bond0 := testRef(inventory.KindBond, "pve1", "bond0")
	bond1 := testRef(inventory.KindBond, "pve1", "bond1")
	vmbr1 := testRef(inventory.KindBridge, "pve1", "vmbr1")

	preview, err := ComputePreview([]Op{
		mkOp(OpIfaceRename, bond0, &IfaceRenameParams{NewName: "bond1"}),
	}, snap)
	if err != nil {
		t.Fatalf("ComputePreview: %v", err)
	}

	var portOf bool
	for _, e := range preview.Topology.Edges {
		if e.From == bond1.String() && e.To == vmbr1.String() {
			portOf = true
		}
		if e.From == bond0.String() {
			t.Errorf("an edge still names the old identity %s", bond0)
		}
	}
	if !portOf {
		t.Errorf("vmbr1 did not follow the bond rename; edges = %+v", preview.Topology.Edges)
	}
	if got := changeFor(t, preview, vmbr1.String()); got.Change != topology.DiffModified {
		t.Errorf("the referencing bridge = %q, want %q", got.Change, topology.DiffModified)
	}
	// The bond's slaves must still resolve, or the rename silently unslaved them.
	for _, nic := range []string{"eno3", "eno4"} {
		var enslaved bool
		for _, e := range preview.Topology.Edges {
			if e.From == testRef(inventory.KindPhysNic, "pve1", nic).String() && e.To == bond1.String() {
				enslaved = true
			}
		}
		if !enslaved {
			t.Errorf("%s is no longer enslaved by the renamed bond", nic)
		}
	}
}

// iface.update is the one op that targets several entity kinds, and its
// address/gateway precedence (an explicit list wins; RemoveAddress applies only
// without one) is the same rule the apply path follows.
func TestPreview_IfaceUpdateFoldsDeclaredFields(t *testing.T) {
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")
	eno1 := testRef(inventory.KindPhysNic, "pve1", "eno1")

	cases := []struct {
		wantFields map[string]string
		name       string
		op         Op
		ref        inventory.Ref
	}{
		{
			name: "an explicit address list replaces the declared one",
			op: mkOp(OpIfaceUpdate, vmbr0, &IfaceUpdateParams{
				Addresses: strsPtr("10.0.0.30/24"), Comments: strPtr("mgmt"),
			}),
			ref:        vmbr0,
			wantFields: map[string]string{"Addresses": "10.0.0.30/24", "Comments": "mgmt"},
		},
		{
			name:       "removeAddress clears the address when no list is supplied",
			op:         mkOp(OpIfaceUpdate, vmbr0, &IfaceUpdateParams{RemoveAddress: true, RemoveGateway: true}),
			ref:        vmbr0,
			wantFields: map[string]string{"Addresses": "", "Gateway": ""},
		},
		{
			name:       "a physnic update moves the declared MTU, not the runtime one",
			op:         mkOp(OpIfaceUpdate, eno1, &IfaceUpdateParams{MTU: intPtr(9000)}),
			ref:        eno1,
			wantFields: map[string]string{"MTUDeclared": "9000"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			preview, err := ComputePreview([]Op{tc.op}, previewFixture())
			if err != nil {
				t.Fatalf("ComputePreview: %v", err)
			}
			got := changeFor(t, preview, tc.ref.String())
			after := map[string]string{}
			for _, f := range got.Fields {
				after[f.Field] = f.After
			}
			for field, want := range tc.wantFields {
				if after[field] != want {
					t.Errorf("field %s = %q, want %q (all changed fields: %+v)", field, after[field], want, got.Fields)
				}
			}
			// The runtime MTU is never invented: an apply does not guarantee
			// the kernel took the declared value.
			if _, moved := after["MTU"]; moved {
				t.Error("the projection moved the RUNTIME mtu; only declared config can be projected")
			}
		})
	}
}

// The partial-update ops all follow the same pointer convention: an absent
// field leaves the current value alone, a present one sets it. This walks the
// remaining families in one table so none of them is projected by guesswork.
func TestPreview_PartialUpdatesFoldOnlyTheFieldsTheyCarry(t *testing.T) {
	zone := inventory.Ref{Kind: inventory.KindSDNZone, ID: "zone1"}
	vnet := inventory.Ref{Kind: inventory.KindSDNVnet, ID: "vnet1"}
	subnet := inventory.Ref{Kind: inventory.KindSDNSubnet, ID: "10.7.0.0/24"}
	dnsZone := inventory.Ref{Kind: inventory.KindSDNDnsZone, ID: "lab.example"}
	dnsRec := inventory.Ref{Kind: inventory.KindSDNDnsRecord, ID: "lab.example/web/A"}
	bond0 := testRef(inventory.KindBond, "pve1", "bond0")
	vlan := testRef(inventory.KindVlan, "pve1", "vmbr0.20")

	snap := previewSnapshot(
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", Status: "online"},
		&inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno3"), Name: "eno3", LinkUp: true, LinkUpSet: true},
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0", Virt: inventory.BridgeLinux},
		&inventory.Bond{Ref: bond0, Name: "bond0", Mode: "balance-rr", Slaves: []string{"eno3"}, DeclaredSlaves: []string{"eno3"}},
		&inventory.VlanIface{Ref: vlan, Name: "vmbr0.20", ParentName: "vmbr0", Vid: 20, Addresses: []string{"10.20.0.1/24"}},
		&inventory.SdnZone{Ref: zone, ID: "zone1", Type: "simple", Bridge: "vmbr0"},
		&inventory.SdnVnet{Ref: vnet, ID: "vnet1", Zone: "zone1", Tag: 10},
		&inventory.SdnSubnet{Ref: subnet, ID: "10.7.0.0/24", Vnet: "vnet1", Gateway: "10.7.0.1"},
		&inventory.SdnDnsZone{Ref: dnsZone, ID: "lab.example", DNS: "pdns1", TTL: 60},
		&inventory.SdnDnsRecord{Ref: dnsRec, Zone: "lab.example", Name: "web", Type: "A", Value: "10.7.0.5", TTL: 60},
	)

	cases := []struct {
		name      string
		op        Op
		ref       inventory.Ref
		wantField string
		wantAfter string
	}{
		{"bond mode", mkOp(OpBondUpdate, bond0, &BondUpdateParams{Mode: strPtr("802.3ad")}), bond0, "Mode", "802.3ad"},
		{"bond slaves", mkOp(OpBondUpdate, bond0, &BondUpdateParams{Slaves: strsPtr("eno3", "eno4")}), bond0, "DeclaredSlaves", "eno3 eno4"},
		{"vlan addresses", mkOp(OpVlanUpdate, vlan, &VlanUpdateParams{Addresses: strsPtr("10.20.0.9/24")}), vlan, "Addresses", "10.20.0.9/24"},
		{"vlan mtu", mkOp(OpVlanUpdate, vlan, &VlanUpdateParams{MTU: intPtr(9000)}), vlan, "MTUDeclared", "9000"},
		{"zone bridge", mkOp(OpSdnZoneUpdate, zone, &SdnZoneUpdateParams{Bridge: strPtr("vmbr1")}), zone, "Bridge", "vmbr1"},
		{"zone mtu", mkOp(OpSdnZoneUpdate, zone, &SdnZoneUpdateParams{MTU: intPtr(1450)}), zone, "MTU", "1450"},
		{"zone nodes", mkOp(OpSdnZoneUpdate, zone, &SdnZoneUpdateParams{Nodes: strsPtr("pve1", "pve2")}), zone, "Nodes", "pve1 pve2"},
		{"vnet tag", mkOp(OpSdnVnetUpdate, vnet, &SdnVnetUpdateParams{Tag: intPtr(77)}), vnet, "Tag", "77"},
		{"vnet alias", mkOp(OpSdnVnetUpdate, vnet, &SdnVnetUpdateParams{Alias: strPtr("lab")}), vnet, "Alias", "lab"},
		{"subnet gateway", mkOp(OpSdnSubnetUpdate, subnet, &SdnSubnetUpdateParams{Gateway: strPtr("10.7.0.254")}), subnet, "Gateway", "10.7.0.254"},
		{"subnet dhcp ranges", mkOp(OpSdnSubnetUpdate, subnet, &SdnSubnetUpdateParams{DHCPRanges: strsPtr("10.7.0.100-10.7.0.200")}), subnet, "DHCPRanges", "10.7.0.100-10.7.0.200"},
		{"dns zone ttl", mkOp(OpSdnDnsZoneUpdate, dnsZone, &SdnDnsZoneUpdateParams{TTL: intPtr(120)}), dnsZone, "TTL", "120"},
		{"dns zone plugin", mkOp(OpSdnDnsZoneUpdate, dnsZone, &SdnDnsZoneUpdateParams{DNS: strPtr("pdns2")}), dnsZone, "DNS", "pdns2"},
		{"dns record value", mkOp(OpSdnDnsRecordUpdate, dnsRec, &SdnDnsRecordUpdateParams{Value: strPtr("10.7.0.6")}), dnsRec, "Value", "10.7.0.6"},
		{"dns record ttl", mkOp(OpSdnDnsRecordUpdate, dnsRec, &SdnDnsRecordUpdateParams{TTL: intPtr(30)}), dnsRec, "TTL", "30"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			preview, err := ComputePreview([]Op{tc.op}, snap)
			if err != nil {
				t.Fatalf("ComputePreview: %v", err)
			}
			got := changeFor(t, preview, tc.ref.String())
			if got.Change != topology.DiffModified {
				t.Fatalf("change = %q, want %q", got.Change, topology.DiffModified)
			}
			var found bool
			for _, f := range got.Fields {
				if f.Field != tc.wantField {
					continue
				}
				found = true
				if f.After != tc.wantAfter {
					t.Errorf("%s after = %q, want %q", tc.wantField, f.After, tc.wantAfter)
				}
			}
			if !found {
				t.Errorf("no change reported for %s; fields = %+v", tc.wantField, got.Fields)
			}
		})
	}
}

// Deletes of the remaining entity families are marked removed too, not just
// bridges (AC2's own case).
func TestPreview_EverySupportedDeleteMarksItsEntityRemoved(t *testing.T) {
	zone := inventory.Ref{Kind: inventory.KindSDNZone, ID: "zone1"}
	vnet := inventory.Ref{Kind: inventory.KindSDNVnet, ID: "vnet1"}
	subnet := inventory.Ref{Kind: inventory.KindSDNSubnet, ID: "10.7.0.0/24"}
	dnsZone := inventory.Ref{Kind: inventory.KindSDNDnsZone, ID: "lab.example"}
	dnsRec := inventory.Ref{Kind: inventory.KindSDNDnsRecord, ID: "lab.example/web/A"}
	bond0 := testRef(inventory.KindBond, "pve1", "bond0")
	vlan := testRef(inventory.KindVlan, "pve1", "vmbr0.20")

	snap := previewSnapshot(
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", Status: "online"},
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0", Virt: inventory.BridgeLinux},
		&inventory.Bond{Ref: bond0, Name: "bond0", Mode: "balance-rr"},
		&inventory.VlanIface{Ref: vlan, Name: "vmbr0.20", ParentName: "vmbr0", Vid: 20},
		&inventory.SdnZone{Ref: zone, ID: "zone1", Type: "simple"},
		&inventory.SdnVnet{Ref: vnet, ID: "vnet1", Zone: "zone1"},
		&inventory.SdnSubnet{Ref: subnet, ID: "10.7.0.0/24", Vnet: "vnet1"},
		&inventory.SdnDnsZone{Ref: dnsZone, ID: "lab.example"},
		&inventory.SdnDnsRecord{Ref: dnsRec, Zone: "lab.example", Name: "web", Type: "A"},
	)

	ops := []Op{
		mkOp(OpBondDelete, bond0, &BondDeleteParams{}),
		mkOp(OpVlanDelete, vlan, &VlanDeleteParams{}),
		mkOp(OpSdnSubnetDelete, subnet, &SdnSubnetDeleteParams{}),
		mkOp(OpSdnVnetDelete, vnet, &SdnVnetDeleteParams{}),
		mkOp(OpSdnZoneDelete, zone, &SdnZoneDeleteParams{}),
		mkOp(OpSdnDnsRecordDelete, dnsRec, &SdnDnsRecordDeleteParams{}),
		mkOp(OpSdnDnsZoneDelete, dnsZone, &SdnDnsZoneDeleteParams{}),
	}
	preview, err := ComputePreview(ops, snap)
	if err != nil {
		t.Fatalf("ComputePreview: %v", err)
	}
	for _, op := range ops {
		if got := changeFor(t, preview, op.Target.String()); got.Change != topology.DiffRemoved {
			t.Errorf("%s: change = %q, want %q", op.Type, got.Change, topology.DiffRemoved)
		}
	}
}

func TestPreview_AddressChangeIsReportedFieldByField(t *testing.T) {
	snap := previewFixture()
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")

	preview, err := ComputePreview([]Op{
		mkOp(OpBridgeUpdate, vmbr0, &BridgeUpdateParams{Addresses: strsPtr("10.0.0.20/24")}),
	}, snap)
	if err != nil {
		t.Fatalf("ComputePreview: %v", err)
	}

	got := changeFor(t, preview, vmbr0.String())
	if got.Change != topology.DiffModified {
		t.Fatalf("change = %q, want %q", got.Change, topology.DiffModified)
	}
	var found bool
	for _, f := range got.Fields {
		if f.Field != "Addresses" {
			continue
		}
		found = true
		if f.Before != "10.0.0.10/24" || f.After != "10.0.0.20/24" {
			t.Errorf("address change = %q → %q, want 10.0.0.10/24 → 10.0.0.20/24", f.Before, f.After)
		}
	}
	if !found {
		t.Errorf("no Addresses field change reported; fields = %+v", got.Fields)
	}
}

func TestPreview_SdnZoneAndVnetProjectOntoTheMap(t *testing.T) {
	snap := previewFixture()
	zone := inventory.Ref{Kind: inventory.KindSDNZone, ID: "zone1"}
	vnet := inventory.Ref{Kind: inventory.KindSDNVnet, ID: "vnet1"}

	preview, err := ComputePreview([]Op{
		mkOp(OpSdnZoneCreate, zone, &SdnZoneCreateParams{Type: "simple", Bridge: "vmbr0"}),
		mkOp(OpSdnVnetCreate, vnet, &SdnVnetCreateParams{Zone: "zone1", Tag: 42}),
	}, snap)
	if err != nil {
		t.Fatalf("ComputePreview: %v", err)
	}

	if !topologyHasNode(preview.Topology, zone.String()) || !topologyHasNode(preview.Topology, vnet.String()) {
		t.Fatalf("the projected topology is missing the created SDN entities: %+v", preview.Topology.Nodes)
	}
	var realizes bool
	for _, e := range preview.Topology.Edges {
		if e.From == vnet.String() && e.To == testRef(inventory.KindBridge, "pve1", "vmbr0").String() {
			realizes = true
		}
	}
	if !realizes {
		t.Error("the created vnet is not shown realized on its zone's bridge")
	}
}

// --- AC5 (service level) ---------------------------------------------------

// AC5: projecting a changeset that fails validation is REFUSED, not projected.
func TestServicePreview_RefusesAChangesetWithBlockingFindings(t *testing.T) {
	svc := newPreviewService(t, previewFixture())
	ctx := context.Background()

	// bridge.update against a bridge that is not in the inventory: a
	// referential error, which is a blocking finding.
	cs, err := svc.Create(ctx, "alice", "widen a bridge that isn't there", []Op{
		mkOp(OpBridgeUpdate, testRef(inventory.KindBridge, "pve1", "vmbr404"), &BridgeUpdateParams{MTU: intPtr(9000)}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.Preview(ctx, cs.ID)
	if err == nil {
		t.Fatal("Preview projected a changeset with blocking validation findings")
	}
	var blocked *ErrValidationBlocked
	if !errors.As(err, &blocked) {
		t.Fatalf("Preview error = %v, want *ErrValidationBlocked so the route answers 422 validation_failed", err)
	}
	if len(blocked.Findings) == 0 {
		t.Error("the refusal carried no findings; the operator cannot act on it")
	}
}

// A clean changeset previews through the service, and the response names the
// changeset it is a preview of.
func TestServicePreview_CleanChangesetProjects(t *testing.T) {
	svc := newPreviewService(t, previewFixture())
	ctx := context.Background()
	vmbr9 := testRef(inventory.KindBridge, "pve1", "vmbr9")

	cs, err := svc.Create(ctx, "alice", "add a bridge", []Op{
		mkOp(OpBridgeCreate, vmbr9, &BridgeCreateParams{MTU: 1500}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	preview, err := svc.Preview(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if preview.ChangesetID != cs.ID {
		t.Errorf("changesetId = %q, want %q", preview.ChangesetID, cs.ID)
	}
	if !topologyHasNode(preview.Topology, vmbr9.String()) {
		t.Error("the created bridge is not in the projected topology")
	}
	if !preview.BestEffort {
		t.Error("bestEffort must be true on every preview")
	}
}

// PreviewSummary is what T-2702's pull-request body renders. It must describe
// the same projection the route serves, including the disclosure.
func TestServicePreviewSummary_RendersChangesAndDisclosure(t *testing.T) {
	svc := newPreviewService(t, previewFixture())
	ctx := context.Background()

	// A projectable op and an unprojectable one in the same changeset. The
	// unprojectable one is a QoS shape rather than AC3's raw-file edit:
	// validating a raw edit requires a live node agent to read the file it is
	// based on, which this pure service harness has no business standing up —
	// and the disclosure path being exercised is the same one either way.
	cs, err := svc.Create(ctx, "alice", "widen a bridge and shape it", []Op{
		mkOp(OpBridgeUpdate, testRef(inventory.KindBridge, "pve1", "vmbr0"), &BridgeUpdateParams{MTU: intPtr(9000)}),
		mkOp(OpQosShapeCreate, testRef(inventory.KindQosShape, "pve1", "shape1"),
			&QosShapeCreateParams{Bridge: "vmbr0", RateMbit: 100}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	summary, err := svc.PreviewSummary(ctx, cs.ID)
	if err != nil {
		t.Fatalf("PreviewSummary: %v", err)
	}
	for _, want := range []string{"Best-effort", "modified", "vmbr0", "Not projected", string(OpQosShapeCreate)} {
		if !strings.Contains(summary, want) {
			t.Errorf("preview summary is missing %q:\n%s", want, summary)
		}
	}
}
