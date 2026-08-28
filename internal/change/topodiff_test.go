// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// --- harness --------------------------------------------------------------

// diffClock is a hand-driven clock. Every timestamp in this feature (snapshot
// taken_at, audit at, the `now` point) comes from Service.now, so a real clock
// would collapse a whole scenario into one second and make "from is before to"
// untestable.
type diffClock struct {
	at time.Time
	mu sync.Mutex
}

func newDiffClock() *diffClock { return &diffClock{at: time.Unix(1_700_000_000, 0)} }

func (c *diffClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *diffClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func (c *diffClock) unix() int64 { return c.now().Unix() }

// diffFakeInventory is an InventorySource whose node set the test controls, so
// a scheduled capture can be made to cover a different node set on either side
// of a range (the "unmatched node" case).
type diffFakeInventory struct {
	nodes []string
	mu    sync.Mutex
}

func (f *diffFakeInventory) setNodes(nodes ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodes = nodes
}

func (f *diffFakeInventory) Snapshot() inventory.Snapshot {
	f.mu.Lock()
	nodes := append([]string(nil), f.nodes...)
	f.mu.Unlock()
	g := inventory.NewGraph()
	ents := make([]inventory.Entity, 0, len(nodes))
	for _, n := range nodes {
		ents = append(ents, &inventory.Node{
			Ref: inventory.Ref{Kind: inventory.KindNode, Node: n, ID: n}, Name: n, Status: "online", Quorate: true,
		})
	}
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, ents)
	return g.Snapshot()
}

type diffHarness struct {
	*applyHarness
	clock *diffClock
	inv   *diffFakeInventory
}

func newTopoDiffHarness(t *testing.T, fixture string, nodes ...string) *diffHarness {
	t.Helper()
	clock := newDiffClock()
	inv := &diffFakeInventory{}
	inv.setNodes(nodes...)
	h := newHarness(t, fixture, func(c *change.Config) {
		c.Inventory = inv
		c.Now = clock.now
	})
	return &diffHarness{applyHarness: h, clock: clock, inv: inv}
}

// capture takes a scheduled snapshot and returns its id, failing if nothing
// was recorded — a test that silently compared against a snapshot that does
// not exist would assert nothing.
func (h *diffHarness) capture(t *testing.T) string {
	t.Helper()
	summary, created, err := h.svc.CaptureScheduledSnapshot(context.Background())
	if err != nil {
		t.Fatalf("CaptureScheduledSnapshot: %v", err)
	}
	if !created {
		t.Fatal("expected a scheduled snapshot to be recorded; the cluster state was unchanged")
	}
	return summary.ID
}

// applyBridge stages, applies and confirms a bridge.create — a change vnprox
// itself made, and therefore one the diff must attribute.
func (h *diffHarness) applyBridge(t *testing.T, node, name string) change.Changeset {
	t.Helper()
	ctx := context.Background()
	cs := h.mustCreate(t, "alice@pve", "add "+name, []change.Op{bridgeCreateOp(node, name, nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "alice@pve", nil, 0); err != nil {
		t.Fatalf("Apply %s: %v", name, err)
	}
	if _, err := h.svc.Confirm(ctx, cs.ID, "alice@pve"); err != nil {
		t.Fatalf("Confirm %s: %v", name, err)
	}
	return cs
}

// editOutsideVnprox rewrites a node's committed interfaces file directly.
// Nothing about it goes through the change engine: no changeset, no stage, no
// reload, no audit row. It is the `ssh node && vi` case.
func (h *diffHarness) editOutsideVnprox(t *testing.T, node, extraStanza string) {
	t.Helper()
	current, err := h.agent.ReadInterfaces(context.Background(), node)
	if err != nil {
		t.Fatalf("reading %s to edit it out of band: %v", node, err)
	}
	h.agent.setCommittedFile(node, current+"\n"+extraStanza)
}

func findEntityDiff(t *testing.T, rows []topology.EntityDiff, ref string) topology.EntityDiff {
	t.Helper()
	for _, d := range rows {
		if d.Ref == ref {
			return d
		}
	}
	got := make([]string, 0, len(rows))
	for _, d := range rows {
		got = append(got, d.Ref)
	}
	t.Fatalf("no diff row for %s; reported: %v", ref, got)
	return topology.EntityDiff{}
}

const outOfBandBridgeStanza = "auto vmbr77\niface vmbr77 inet manual\n\tbridge-ports none\n\tmtu 9000\n"

// --- AC1 + AC2: attribution, and the honest absence of it -----------------

// The card's central assertion, with its own control leg in the same diff: one
// entity changed by a changeset, one changed by a human over SSH. If the
// implementation attributed everything, the vmbr77 assertion fails; if it
// attributed nothing, the vmbr9 assertion fails. Neither can be satisfied by
// hardcoding.
func TestTopologyDiff_AttributesChangesetsAndMarksOutOfBandChangesUnattributed(t *testing.T) {
	h := newTopoDiffHarness(t, fixtureSingleNode, "pve1")
	ctx := context.Background()

	base := h.capture(t)
	h.clock.advance(time.Minute)

	cs := h.applyBridge(t, "pve1", "vmbr9")
	h.clock.advance(time.Minute)

	h.editOutsideVnprox(t, "pve1", outOfBandBridgeStanza)
	h.clock.advance(time.Minute)

	diff, err := h.svc.TopologyDiff(ctx, base, change.TopologyDiffNowToken)
	if err != nil {
		t.Fatalf("TopologyDiff: %v", err)
	}

	// AC1: the changeset's bridge is attributed TO that changeset.
	made := findEntityDiff(t, diff.Added, "bridge:pve1:vmbr9")
	if !made.Attribution.Attributed {
		t.Fatalf("vmbr9 was created by changeset %s but is reported unattributed", cs.ID)
	}
	if made.Attribution.ChangesetID != cs.ID {
		t.Errorf("vmbr9 attributed to changeset %q, want %q", made.Attribution.ChangesetID, cs.ID)
	}
	if made.Attribution.Actor != "alice@pve" {
		t.Errorf("vmbr9 actor = %q, want alice@pve", made.Attribution.Actor)
	}
	if made.Attribution.At == 0 {
		t.Error("vmbr9 attribution carries no timestamp")
	}

	// AC2: the out-of-band bridge is present AND marked unattributed. Both
	// halves matter — a diff that omitted it entirely would also make the
	// "not attributed" half vacuously true.
	oob := findEntityDiff(t, diff.Added, "bridge:pve1:vmbr77")
	if oob.Attribution.Attributed {
		t.Fatalf("an edit made outside vnprox was attributed to changeset %q — an out-of-band change was laundered as a vnprox one",
			oob.Attribution.ChangesetID)
	}
	if oob.Attribution.ChangesetID != "" {
		t.Errorf("unattributed row still names changeset %q", oob.Attribution.ChangesetID)
	}
	if diff.Unattributed < 1 {
		t.Errorf("unattributedCount = %d, want at least 1", diff.Unattributed)
	}
}

// A changeset explains ONLY the entities its ops target. Without this, a
// single legitimate changeset in the window would launder every out-of-band
// edit made alongside it.
func TestTopologyDiff_ChangesetDoesNotAttributeEntitiesItNeverTouched(t *testing.T) {
	h := newTopoDiffHarness(t, fixtureSingleNode, "pve1")
	ctx := context.Background()

	base := h.capture(t)
	h.clock.advance(time.Minute)
	h.applyBridge(t, "pve1", "vmbr9")
	h.clock.advance(time.Minute)

	// Same window, same node, same second-scale as the changeset above — but
	// a different entity, edited by hand.
	h.editOutsideVnprox(t, "pve1", outOfBandBridgeStanza)
	h.clock.advance(time.Minute)

	diff, err := h.svc.TopologyDiff(ctx, base, change.TopologyDiffNowToken)
	if err != nil {
		t.Fatalf("TopologyDiff: %v", err)
	}
	for _, d := range append(append([]topology.EntityDiff{}, diff.Added...), diff.Modified...) {
		if d.Ref == "bridge:pve1:vmbr9" {
			continue
		}
		if d.Attribution.Attributed {
			t.Errorf("%s was attributed to %s, but that changeset's only op targets bridge:pve1:vmbr9",
				d.Ref, d.Attribution.ChangesetID)
		}
	}
}

// --- AC3: both point spellings, and the refusal of a backwards range ------

func TestTopologyDiff_AcceptsSnapshotIdsAndTimestamps(t *testing.T) {
	h := newTopoDiffHarness(t, fixtureSingleNode, "pve1")
	ctx := context.Background()

	first := h.capture(t)
	firstAt := h.clock.unix()
	h.clock.advance(time.Hour)

	h.applyBridge(t, "pve1", "vmbr9")
	h.clock.advance(time.Minute)
	second := h.capture(t)
	secondAt := h.clock.unix()
	h.clock.advance(time.Minute)

	tests := []struct {
		name string
		from string
		to   string
	}{
		{name: "snapshot id to snapshot id", from: first, to: second},
		{name: "unix seconds both ends", from: unixString(firstAt), to: unixString(secondAt)},
		{name: "rfc3339 both ends", from: rfc3339(firstAt), to: rfc3339(secondAt)},
		{name: "snapshot id to now", from: first, to: change.TopologyDiffNowToken},
		{name: "unix seconds to live alias", from: unixString(firstAt), to: "live"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			diff, err := h.svc.TopologyDiff(ctx, tc.from, tc.to)
			if err != nil {
				t.Fatalf("TopologyDiff(%q,%q): %v", tc.from, tc.to, err)
			}
			// Every spelling resolves to the same range, so every spelling
			// must find the same change.
			findEntityDiff(t, diff.Added, "bridge:pve1:vmbr9")
			if diff.From.At > diff.To.At {
				t.Fatalf("resolved from (%d) after to (%d)", diff.From.At, diff.To.At)
			}
		})
	}
}

func TestTopologyDiff_RefusesAnInvertedRange(t *testing.T) {
	h := newTopoDiffHarness(t, fixtureSingleNode, "pve1")
	ctx := context.Background()

	first := h.capture(t)
	h.clock.advance(time.Hour)
	h.applyBridge(t, "pve1", "vmbr9")
	h.clock.advance(time.Minute)
	second := h.capture(t)

	// Control leg: the same two points the right way round succeed, so the
	// refusal below is about the ORDER and not about the points.
	if _, err := h.svc.TopologyDiff(ctx, first, second); err != nil {
		t.Fatalf("forward range must succeed: %v", err)
	}

	_, err := h.svc.TopologyDiff(ctx, second, first)
	var inverted *change.ErrDiffRangeInverted
	if !errors.As(err, &inverted) {
		t.Fatalf("reversed range error = %v, want *change.ErrDiffRangeInverted", err)
	}
}

// --- AC4: an uncovered range is a stated error, never an empty diff -------

func TestTopologyDiff_UncoveredRangeNamesTheNearestSnapshots(t *testing.T) {
	h := newTopoDiffHarness(t, fixtureSingleNode, "pve1")
	ctx := context.Background()

	only := h.capture(t)
	onlyAt := h.clock.unix()
	h.clock.advance(time.Hour)

	// Control leg: a covered point resolves, so the failure below is about
	// coverage and not about the resolver being broken.
	if _, err := h.svc.TopologyDiff(ctx, unixString(onlyAt), change.TopologyDiffNowToken); err != nil {
		t.Fatalf("a covered from-point must resolve: %v", err)
	}

	before := onlyAt - 86400
	diff, err := h.svc.TopologyDiff(ctx, unixString(before), change.TopologyDiffNowToken)
	if diff != nil {
		t.Fatalf("an uncovered range returned a diff (%d added, %d removed, %d modified); an empty diff reads as \"nothing changed\", which is false",
			len(diff.Added), len(diff.Removed), len(diff.Modified))
	}
	var missing *change.ErrNoSnapshotForPoint
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v, want *change.ErrNoSnapshotForPoint", err)
	}
	if missing.Side != "from" {
		t.Errorf("side = %q, want from", missing.Side)
	}
	if len(missing.Nearest) == 0 {
		t.Fatal("the error names no nearest snapshot; an operator cannot tell which range they could ask for instead")
	}
	named := false
	for _, n := range missing.Nearest {
		if n.SnapshotID == only {
			named = true
		}
	}
	if !named {
		t.Errorf("nearest = %+v, want it to name the one snapshot that exists (%s)", missing.Nearest, only)
	}
	if !strings.Contains(missing.Error(), only) {
		t.Errorf("error message %q does not name snapshot %s", missing.Error(), only)
	}
}

func TestTopologyDiff_EmptyHistorySaysSoRatherThanReturningNothing(t *testing.T) {
	h := newTopoDiffHarness(t, fixtureSingleNode, "pve1")

	diff, err := h.svc.TopologyDiff(context.Background(), unixString(h.clock.unix()), change.TopologyDiffNowToken)
	if diff != nil {
		t.Fatal("a cluster with no snapshots at all must not produce a diff")
	}
	var missing *change.ErrNoSnapshotForPoint
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v, want *change.ErrNoSnapshotForPoint", err)
	}
	if len(missing.Nearest) != 0 {
		t.Errorf("nearest = %+v, want empty when there are no snapshots", missing.Nearest)
	}
	if !strings.Contains(missing.Error(), "no snapshots at all") {
		t.Errorf("error %q should say plainly that there are no snapshots", missing.Error())
	}
}

// --- AC5: a point against itself -----------------------------------------

func TestTopologyDiff_PointAgainstItselfIsEmpty(t *testing.T) {
	h := newTopoDiffHarness(t, fixtureSingleNode, "pve1")
	ctx := context.Background()

	// Make the cluster non-trivial first, so "empty" is a real answer rather
	// than the consequence of there being nothing to compare.
	h.applyBridge(t, "pve1", "vmbr9")
	h.clock.advance(time.Minute)
	id := h.capture(t)
	h.clock.advance(time.Minute)

	diff, err := h.svc.TopologyDiff(ctx, id, id)
	if err != nil {
		t.Fatalf("TopologyDiff(self,self): %v", err)
	}
	if len(diff.Added)+len(diff.Removed)+len(diff.Modified) != 0 {
		t.Fatalf("a point against itself reported changes: +%d -%d ~%d", len(diff.Added), len(diff.Removed), len(diff.Modified))
	}
	if len(diff.Coverage.Nodes) == 0 {
		t.Fatal("an empty diff over zero covered nodes proves nothing; coverage must name the nodes actually compared")
	}
}

// --- AC6: field-level before/after through the service --------------------

func TestTopologyDiff_ModifiedEntityCarriesFieldLevelBeforeAndAfter(t *testing.T) {
	h := newTopoDiffHarness(t, fixtureSingleNode, "pve1")
	ctx := context.Background()

	base := h.capture(t)
	h.clock.advance(time.Minute)

	before, err := h.agent.ReadInterfaces(ctx, "pve1")
	if err != nil {
		t.Fatalf("ReadInterfaces: %v", err)
	}
	if !strings.Contains(before, "mtu 1500") {
		t.Fatalf("fixture does not declare an MTU to change:\n%s", before)
	}
	h.agent.setCommittedFile("pve1", strings.ReplaceAll(before, "mtu 1500", "mtu 9000"))
	h.clock.advance(time.Minute)

	diff, err := h.svc.TopologyDiff(ctx, base, change.TopologyDiffNowToken)
	if err != nil {
		t.Fatalf("TopologyDiff: %v", err)
	}
	if len(diff.Modified) == 0 {
		t.Fatal("an MTU edit produced no modified entity")
	}
	for _, d := range diff.Modified {
		if len(d.Fields) == 0 {
			t.Fatalf("%s is reported modified with no field detail — \"modified\" alone is not an answer", d.Ref)
		}
		found := false
		for _, f := range d.Fields {
			if f.Field == "MTUDeclared" {
				found = true
				if f.Before != "1500" || f.After != "9000" {
					t.Errorf("%s MTUDeclared = %q -> %q, want 1500 -> 9000", d.Ref, f.Before, f.After)
				}
			}
		}
		if !found {
			t.Errorf("%s modified but reports no MTUDeclared change: %+v", d.Ref, d.Fields)
		}
	}
}

// --- coverage honesty ------------------------------------------------------

// A node captured at only one end must be NAMED, not rendered as "every
// interface on it was deleted". This is the same class of false statement AC4
// guards against, in the node dimension.
func TestTopologyDiff_NodeCapturedOnOneSideOnlyIsNamedNotReportedAsDeleted(t *testing.T) {
	h := newTopoDiffHarness(t, fixtureThreeNode, "pve1", "pve2", "pve3")
	ctx := context.Background()

	wide := h.capture(t)
	h.clock.advance(time.Minute)

	// The next capture only covers pve1 — modelling a capture taken while
	// two nodes were unreachable, not two nodes being wiped.
	h.inv.setNodes("pve1")
	h.agent.setCommittedFile("pve1", readNodeFile(t, h, "pve1")+"\n"+outOfBandBridgeStanza)
	narrow := h.capture(t)

	diff, err := h.svc.TopologyDiff(ctx, wide, narrow)
	if err != nil {
		t.Fatalf("TopologyDiff: %v", err)
	}

	if len(diff.Coverage.Nodes) != 1 || diff.Coverage.Nodes[0] != "pve1" {
		t.Fatalf("coverage.nodes = %v, want [pve1]", diff.Coverage.Nodes)
	}
	unmatched := map[string]string{}
	for _, u := range diff.Coverage.UnmatchedNodes {
		unmatched[u.Node] = u.PresentIn
	}
	for _, node := range []string{"pve2", "pve3"} {
		if unmatched[node] != "from" {
			t.Errorf("node %s presentIn = %q, want from", node, unmatched[node])
		}
	}
	for _, d := range diff.Removed {
		if d.Node != "pve1" {
			t.Errorf("%s reported removed, but its node was simply not captured at the `to` point", d.Ref)
		}
	}
	// Control leg: the covered node's own out-of-band change IS reported, so
	// the scoping above narrowed the node set and not the diff itself.
	findEntityDiff(t, diff.Added, "bridge:pve1:vmbr77")
}

// --- read-only -------------------------------------------------------------

// The card is explicit: this route computes and renders, and must not stage,
// apply, or write PVE state. Asserted with a control leg — an apply in the
// same test moves every counter the diff must leave alone, so a broken spy
// cannot make this pass.
func TestTopologyDiff_WritesNothing(t *testing.T) {
	h := newTopoDiffHarness(t, fixtureSingleNode, "pve1")
	ctx := context.Background()

	base := h.capture(t)
	h.clock.advance(time.Minute)

	stageBefore, loadBefore := h.agent.stageCalls, h.agent.loadCalls
	snapsBefore := countSnapshots(t, h.applyHarness)
	csBefore := countChangesets(t, h.applyHarness)

	// Control leg: an apply DOES move all four counters.
	h.applyBridge(t, "pve1", "vmbr9")
	h.clock.advance(time.Minute)
	if h.agent.stageCalls == stageBefore || h.agent.loadCalls == loadBefore {
		t.Fatal("the stage/reload spies did not count an apply; every assertion below would be vacuous")
	}
	if countSnapshots(t, h.applyHarness) == snapsBefore || countChangesets(t, h.applyHarness) == csBefore {
		t.Fatal("the snapshot/changeset counters did not move on an apply; every assertion below would be vacuous")
	}

	stageAfterApply, loadAfterApply := h.agent.stageCalls, h.agent.loadCalls
	snapsAfterApply := countSnapshots(t, h.applyHarness)
	csAfterApply := countChangesets(t, h.applyHarness)

	for range 3 {
		if _, err := h.svc.TopologyDiff(ctx, base, change.TopologyDiffNowToken); err != nil {
			t.Fatalf("TopologyDiff: %v", err)
		}
		h.clock.advance(time.Minute)
	}

	if h.agent.stageCalls != stageAfterApply {
		t.Errorf("stage calls = %d, want %d — the diff staged a file", h.agent.stageCalls, stageAfterApply)
	}
	if h.agent.loadCalls != loadAfterApply {
		t.Errorf("reload calls = %d, want %d — the diff reloaded the network", h.agent.loadCalls, loadAfterApply)
	}
	if got := countSnapshots(t, h.applyHarness); got != snapsAfterApply {
		t.Errorf("snapshots = %d, want %d — the diff wrote a snapshot row", got, snapsAfterApply)
	}
	if got := countChangesets(t, h.applyHarness); got != csAfterApply {
		t.Errorf("changesets = %d, want %d — the diff staged a changeset", got, csAfterApply)
	}
}

// --- determinism -----------------------------------------------------------

// internal/pvemock's list endpoints are order-nondeterministic
// (T-2502-followup-01). The diff must not inherit that: repeated identical
// requests must produce byte-identical row order, or an operator watching the
// page sees rows shuffle on every refresh.
func TestTopologyDiff_RepeatedRequestsAreIdentical(t *testing.T) {
	h := newTopoDiffHarness(t, fixtureThreeNode, "pve1", "pve2", "pve3")
	ctx := context.Background()

	base := h.capture(t)
	h.clock.advance(time.Minute)
	h.applyBridge(t, "pve1", "vmbr9")
	h.clock.advance(time.Minute)
	for _, node := range []string{"pve1", "pve2", "pve3"} {
		h.agent.setCommittedFile(node, readNodeFile(t, h, node)+"\n"+outOfBandBridgeStanza)
	}
	h.clock.advance(time.Minute)

	var want string
	for run := range 12 {
		diff, err := h.svc.TopologyDiff(ctx, base, change.TopologyDiffNowToken)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		var b strings.Builder
		for _, group := range [][]topology.EntityDiff{diff.Added, diff.Removed, diff.Modified} {
			for _, d := range group {
				b.WriteString(string(d.Change) + " " + d.Ref + " " + d.Attribution.ChangesetID + ";")
			}
		}
		got := b.String()
		if run == 0 {
			want = got
			if strings.Count(want, ";") < 4 {
				t.Fatalf("fixture produced only %q; too few rows for an ordering guard", want)
			}
			continue
		}
		if got != want {
			t.Fatalf("run %d order\n got %s\nwant %s", run, got, want)
		}
	}
}

// --- helpers ---------------------------------------------------------------

func readNodeFile(t *testing.T, h *diffHarness, node string) string {
	t.Helper()
	content, err := h.agent.ReadInterfaces(context.Background(), node)
	if err != nil {
		t.Fatalf("ReadInterfaces(%s): %v", node, err)
	}
	return content
}

func unixString(at int64) string { return strconv.FormatInt(at, 10) }

func rfc3339(at int64) string { return time.Unix(at, 0).UTC().Format(time.RFC3339) }

func countSnapshots(t *testing.T, h *applyHarness) int {
	t.Helper()
	rows, _, err := h.svc.ListSnapshots(context.Background(), "", 200)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	return len(rows)
}

func countChangesets(t *testing.T, h *applyHarness) int {
	t.Helper()
	rows, err := h.svc.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List changesets: %v", err)
	}
	return len(rows)
}
