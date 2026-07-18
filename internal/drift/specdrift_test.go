package drift_test

// T-1102 acceptance criteria for internal/drift's sixth check family,
// spec_drift (specdrift.go): live state vs. a pinned declarative spec
// (internal/spec, T-1101), reusing spec.Import verbatim as the diff engine.

import (
	"context"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/spec"
)

const fixtureThreeNodeVlan = "../../testdata/clusters/three-node-vlan.yaml"

// buildThreeNodeVlanGraph runs one full PVE+host poll cycle against the
// three-node-vlan fixture named by AC1/AC2 (docs/development.md's second
// baseline cluster), mirroring buildMessyBrownfieldGraph above and
// internal/spec/testhelpers_test.go's identical buildFixtureGraph helper.
func buildThreeNodeVlanGraph(t *testing.T) *inventory.Graph {
	t.Helper()
	f, err := pvemock.LoadFixture(fixtureThreeNodeVlan)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := pvemock.NewServer(f)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	graph := inventory.NewGraph()
	c, err := collect.New(collect.Config{
		PVE:   client,
		Host:  host.NewFixtureReader(pvemock.NewFixtureHostReader(srv)),
		Graph: graph,
	})
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.RefreshNow(ctx, inventory.Scope{}); err != nil {
		t.Fatalf("RefreshNow: %v", err)
	}
	return graph
}

// TestSpecDrift_ThreeNodeVlanFixture_PinMatchingLive_NoFindings is AC1
// against the letter of the task card's named fixture: pin a spec captured
// from three-node-vlan -> GET /drift immediately after has no spec_drift
// findings.
func TestSpecDrift_ThreeNodeVlanFixture_PinMatchingLive_NoFindings(t *testing.T) {
	graph := buildThreeNodeVlanGraph(t)
	exported := spec.Export(graph.Snapshot())
	content, err := spec.Marshal(exported)
	if err != nil {
		t.Fatalf("spec.Marshal: %v", err)
	}

	svc := drift.New(drift.Config{Graph: graph, Pins: staticPin(string(content))})
	if found := findByCheck(t, svc.Findings(), drift.CheckSpecDrift); len(found) != 0 {
		t.Fatalf("spec_drift findings = %+v, want none (pin captured from live three-node-vlan)", found)
	}
}

// TestSpecDrift_ThreeNodeVlanFixture_MutatedBridgeMTU_OneFinding is AC2
// against the letter of the task card's named fixture: a fixture-injected
// bridge MTU change on one node, without touching the pin, raises exactly
// one spec_drift finding naming that entity.
func TestSpecDrift_ThreeNodeVlanFixture_MutatedBridgeMTU_OneFinding(t *testing.T) {
	graph := buildThreeNodeVlanGraph(t)
	exported := spec.Export(graph.Snapshot())
	content, err := spec.Marshal(exported)
	if err != nil {
		t.Fatalf("spec.Marshal: %v", err)
	}

	// Fixture-injected bridge MTU change on pve2's vmbr0, after capturing
	// the pin — the pin still declares three-node-vlan's original MTU (1500).
	pveBridge(graph, "pve2", "vmbr0", 9000, true, nil, []string{"bond0"})

	svc := drift.New(drift.Config{Graph: graph, Pins: staticPin(string(content))})
	found := findByCheck(t, svc.Findings(), drift.CheckSpecDrift)
	if len(found) != 1 {
		t.Fatalf("spec_drift findings = %d, want exactly 1: %+v", len(found), found)
	}
	if len(found[0].Refs) != 1 || found[0].Refs[0] != "bridge:pve2:vmbr0" {
		t.Errorf("spec_drift finding refs = %v, want [bridge:pve2:vmbr0]", found[0].Refs)
	}
}

// fakePinProvider is a minimal drift.PinProvider stand-in for tests: content
// is read fresh on every call (via a func, not a fixed field), so a test can
// simulate a pin changing (or being unpinned) between drift cycles without
// rebuilding the Service.
type fakePinProvider struct {
	pin func() (string, bool)
}

func (f fakePinProvider) Pin() (string, bool) {
	if f.pin == nil {
		return "", false
	}
	return f.pin()
}

func staticPin(content string) fakePinProvider {
	return fakePinProvider{pin: func() (string, bool) { return content, true }}
}

// TestSpecDrift_PinMatchingLive_NoFindings is AC1: pinning a spec captured
// from live produces zero spec_drift findings immediately after (the same
// round-trip identity T-1101 AC2 already establishes for spec.Import itself
// — Export(live) -> Import(spec, live) is zero ops).
func TestSpecDrift_PinMatchingLive_NoFindings(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2", "pve3")
	pveBridge(g, "pve1", "vmbr0", 1500, true, nil, nil)
	pveBridge(g, "pve2", "vmbr0", 1500, true, nil, nil)
	pveBridge(g, "pve3", "vmbr0", 1500, true, nil, nil)

	exported := spec.Export(g.Snapshot())
	content, err := spec.Marshal(exported)
	if err != nil {
		t.Fatalf("spec.Marshal: %v", err)
	}

	svc := drift.New(drift.Config{Graph: g, Pins: staticPin(string(content))})
	found := findByCheck(t, svc.Findings(), drift.CheckSpecDrift)
	if len(found) != 0 {
		t.Fatalf("spec_drift findings = %+v, want none (pin matches live exactly)", found)
	}
}

// TestSpecDrift_MutatedLiveWithoutTouchingPin_OneFindingNamingEntity is AC2:
// mutating live state (a bridge's MTU) without touching the pin raises
// exactly one spec_drift finding naming that entity — with a check value
// distinct from bridge_divergence/mtu_consistency even though the exact same
// entity (bridge:pve2:vmbr0) independently triggers mtu_consistency too
// (pve2's vmbr0 MTU now disagrees with pve1/pve3's).
func TestSpecDrift_MutatedLiveWithoutTouchingPin_OneFindingNamingEntity(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2", "pve3")
	pveBridge(g, "pve1", "vmbr0", 1500, true, nil, nil)
	pveBridge(g, "pve2", "vmbr0", 1500, true, nil, nil)
	pveBridge(g, "pve3", "vmbr0", 1500, true, nil, nil)

	exported := spec.Export(g.Snapshot())
	content, err := spec.Marshal(exported)
	if err != nil {
		t.Fatalf("spec.Marshal: %v", err)
	}

	// Mutate live (pve2's vmbr0 MTU) without touching the pin, which still
	// declares 1500 for every node.
	pveBridge(g, "pve2", "vmbr0", 9000, true, nil, nil)

	svc := drift.New(drift.Config{Graph: g, Pins: staticPin(string(content))})
	findings := svc.Findings()

	specDrift := findByCheck(t, findings, drift.CheckSpecDrift)
	if len(specDrift) != 1 {
		t.Fatalf("spec_drift findings = %d, want exactly 1: %+v", len(specDrift), specDrift)
	}
	f := specDrift[0]
	if len(f.Refs) != 1 || f.Refs[0] != "bridge:pve2:vmbr0" {
		t.Errorf("spec_drift finding refs = %v, want [bridge:pve2:vmbr0]", f.Refs)
	}
	if !strings.Contains(f.Detail, "bridge:pve2:vmbr0") {
		t.Errorf("detail = %q, want it to name the diverging entity", f.Detail)
	}
	if !f.Fixable {
		t.Errorf("spec_drift finding should be fixable (spec.Import's op patch is computable)")
	}

	// The exact same entity also independently triggers mtu_consistency
	// (cross-node MTU divergence) — spec_drift is a distinct check value,
	// not a duplicate/replacement of it.
	mtu := findByCheck(t, findings, drift.CheckMTUConsistency)
	if len(mtu) == 0 {
		t.Fatalf("expected mtu_consistency to also fire for the same mutation (cross-node MTU divergence)")
	}
	if drift.CheckSpecDrift == drift.CheckMTUConsistency || drift.CheckSpecDrift == drift.CheckBridgeDivergence {
		t.Fatalf("CheckSpecDrift must be distinct from CheckMTUConsistency/CheckBridgeDivergence")
	}
}

// TestSpecDrift_FixOps_ReturnsReconcileOps is AC3 at the drift-package
// level: FixOps on a spec_drift finding returns spec.Import's own computed
// op patch (never applied — that's the caller's job through the normal
// changeset lifecycle, exactly like every other drift fix).
func TestSpecDrift_FixOps_ReturnsReconcileOps(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pveBridge(g, "pve1", "vmbr0", 1500, true, nil, nil)

	exported := spec.Export(g.Snapshot())
	content, err := spec.Marshal(exported)
	if err != nil {
		t.Fatalf("spec.Marshal: %v", err)
	}

	// Diverge: bump the live bridge's MTU after capturing the pin.
	pveBridge(g, "pve1", "vmbr0", 9000, true, nil, nil)

	svc := drift.New(drift.Config{Graph: g, Pins: staticPin(string(content))})
	findings := findByCheck(t, svc.Findings(), drift.CheckSpecDrift)
	if len(findings) != 1 {
		t.Fatalf("spec_drift findings = %d, want 1: %+v", len(findings), findings)
	}

	ops, title, ok := svc.FixOps(findings[0].ID)
	if !ok {
		t.Fatalf("FixOps(%s) ok = false, want true", findings[0].ID)
	}
	if !strings.HasPrefix(title, "drift: ") {
		t.Errorf("fix title = %q, want a drift:-prefixed title", title)
	}
	if len(ops) != 1 || ops[0].Type != change.OpBridgeUpdate {
		t.Fatalf("fix ops = %+v, want exactly one bridge.update reconciling MTU back to 1500", ops)
	}
	upd, ok := ops[0].Params.(*change.BridgeUpdateParams)
	if !ok || upd.MTU == nil || *upd.MTU != 1500 {
		t.Errorf("fix op params = %+v, want MTU reconciled to the pinned value 1500", ops[0].Params)
	}
}

// TestSpecDrift_NoPin_NoFindings covers the nil-Pins and not-pinned cases:
// pinning is opt-in, so absent a pin the check family contributes zero
// findings — the pre-T-1102 default behavior for every existing caller of
// drift.New (messybrownfield_test.go's exact-5-findings assertion depends on
// this).
func TestSpecDrift_NoPin_NoFindings(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pveBridge(g, "pve1", "vmbr0", 1500, true, nil, nil)

	// Pins entirely nil (the zero-value Config, matching every pre-T-1102 test).
	svcNilPins := drift.New(drift.Config{Graph: g})
	if found := findByCheck(t, svcNilPins.Findings(), drift.CheckSpecDrift); len(found) != 0 {
		t.Errorf("nil Pins: spec_drift findings = %+v, want none", found)
	}

	// Pins set but reporting "nothing pinned".
	svcUnpinned := drift.New(drift.Config{Graph: g, Pins: fakePinProvider{}})
	if found := findByCheck(t, svcUnpinned.Findings(), drift.CheckSpecDrift); len(found) != 0 {
		t.Errorf("unpinned: spec_drift findings = %+v, want none", found)
	}
}

// TestSpecDrift_UnpinningClearsFindings is AC4's drift-cycle half: once the
// pin provider reports "nothing pinned", the very next Findings() call has
// no spec_drift findings — Findings recomputes fresh from current pin state
// every call, there is no stale cache to invalidate.
func TestSpecDrift_UnpinningClearsFindings(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pveBridge(g, "pve1", "vmbr0", 1500, true, nil, nil)

	exported := spec.Export(g.Snapshot())
	content, err := spec.Marshal(exported)
	if err != nil {
		t.Fatalf("spec.Marshal: %v", err)
	}
	pveBridge(g, "pve1", "vmbr0", 9000, true, nil, nil) // diverge from the pin

	pinned := true
	pins := fakePinProvider{pin: func() (string, bool) {
		if !pinned {
			return "", false
		}
		return string(content), true
	}}
	svc := drift.New(drift.Config{Graph: g, Pins: pins})

	if found := findByCheck(t, svc.Findings(), drift.CheckSpecDrift); len(found) != 1 {
		t.Fatalf("before unpin: spec_drift findings = %d, want 1", len(found))
	}

	pinned = false // simulate DELETE /spec/pin
	if found := findByCheck(t, svc.Findings(), drift.CheckSpecDrift); len(found) != 0 {
		t.Fatalf("after unpin: spec_drift findings = %+v, want none", found)
	}
}

// TestSpecDrift_MessyBrownfieldRegression is AC5: pinning a (deliberately
// diverging) spec against the messy-brownfield fixture raises spec_drift
// findings without disturbing the five existing cross-node families' own
// results — pin state is additive, never a replacement for the other checks.
func TestSpecDrift_MessyBrownfieldRegression(t *testing.T) {
	graph := buildMessyBrownfieldGraph(t)

	baseline := drift.New(drift.Config{Graph: graph})
	baselineFindings := baseline.Findings()
	baselineByCheck := findingsByCheck(baselineFindings)

	// An arbitrary pin that diverges from live (a single bridge on pve1 with
	// a deliberately wrong MTU) — enough to prove spec_drift fires
	// independently, without needing to hand-craft a full-cluster spec.
	pinContent := "specVersion: 1\n" +
		"nodes:\n" +
		"  - name: pve1\n" +
		"    bridges:\n" +
		"      - name: vmbr0\n" +
		"        mtu: 1234\n"

	pinned := drift.New(drift.Config{Graph: graph, Pins: staticPin(pinContent)})
	pinnedFindings := pinned.Findings()
	pinnedByCheck := findingsByCheck(pinnedFindings)

	specDrift := findByCheck(t, pinnedFindings, drift.CheckSpecDrift)
	if len(specDrift) == 0 {
		t.Fatalf("expected at least one spec_drift finding against the deliberately-diverging pin")
	}

	for _, check := range []string{
		drift.CheckBridgeDivergence, drift.CheckMTUConsistency, drift.CheckSDNRealization,
		drift.CheckPendingInterfaces, drift.CheckFileRuntimeDivergence,
	} {
		before := baselineByCheck[check]
		after := pinnedByCheck[check]
		if len(before) != len(after) {
			t.Errorf("%s findings changed by pinning: before=%d after=%d", check, len(before), len(after))
			continue
		}
		beforeIDs := findingIDs(before)
		afterIDs := findingIDs(after)
		sort.Strings(beforeIDs)
		sort.Strings(afterIDs)
		for i := range beforeIDs {
			if beforeIDs[i] != afterIDs[i] {
				t.Errorf("%s finding IDs changed by pinning: before=%v after=%v", check, beforeIDs, afterIDs)
				break
			}
		}
	}
}

func findingIDs(findings []drift.Finding) []string {
	out := make([]string, len(findings))
	for i, f := range findings {
		out[i] = f.ID
	}
	return out
}
