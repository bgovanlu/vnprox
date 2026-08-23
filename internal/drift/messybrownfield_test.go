package drift_test

// T-305 acceptance criterion 1: "messy-brownfield fixture produces exactly
// its documented expected findings (the fixture exists to be this test)."
// This test runs the real pvemock -> collect -> inventory.Graph -> drift
// pipeline against testdata/clusters/messy-brownfield.yaml and asserts the
// exact set of findings the five check families are expected to surface
// for that fixture's documented `mess:` list (internal/pvemock's
// GET /mock/mess) — not every mess item maps to a T-305 drift check family
// (some are firewall/SDN-apply/referential issues other features own; see
// this test's per-mess-item commentary below), but every item that *does*
// map to one of docs/features/topology.md §6's five families produces
// exactly the finding(s) asserted here. T-3701 adds a sixth finding on top
// of the original five: checkSDNZoneStatus, a second and independent signal
// for the same MESS 3 gap (see that assertion block below for why it's not
// a duplicate of sdn_realization despite firing on the identical fixture
// scenario).
//
// Acceptance criterion 4 ("File-vs-runtime check catches a
// fixture-simulated manual ip link change") is covered by this same test:
// the fixture's MESS 10 (pve1's eno3 live-enslaved to vmbr0 outside the
// interfaces file) is asserted below.

import (
	"context"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

const fixtureMessyBrownfield = "../../testdata/clusters/messy-brownfield.yaml"

// buildMessyBrownfieldGraph runs one full PVE+host poll cycle against the
// messy-brownfield fixture and returns the resulting graph (mirroring
// internal/topology's own buildGraph test helper — see that package's
// testhelpers_test.go doc comment for the rationale).
func buildMessyBrownfieldGraph(t *testing.T) *inventory.Graph {
	t.Helper()
	f, err := pvemock.LoadFixture(fixtureMessyBrownfield)
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

func findingsByCheck(findings []drift.Finding) map[string][]drift.Finding {
	out := map[string][]drift.Finding{}
	for _, f := range findings {
		out[f.Check] = append(out[f.Check], f)
	}
	return out
}

func TestMessyBrownfield_ExactExpectedFindings(t *testing.T) {
	graph := buildMessyBrownfieldGraph(t)
	svc := drift.New(drift.Config{Graph: graph})
	findings := svc.Findings()
	byCheck := findingsByCheck(findings)

	// --- bridge_divergence -------------------------------------------
	// MESS 3 (presence half): vmbr99 exists on pve1+pve2 but not pve3.
	// No VLAN-awareness/VID-set divergence is present anywhere else in the
	// fixture (every same-named bridge that survives to this check agrees
	// on those two fields), so exactly one bridge_divergence finding.
	bd := byCheck[drift.CheckBridgeDivergence]
	if len(bd) != 1 {
		t.Fatalf("bridge_divergence findings = %d, want 1: %+v", len(bd), bd)
	}
	if bd[0].Fixable {
		t.Errorf("the presence-divergence finding should not be fixable, got Fixable=true: %+v", bd[0])
	}
	assertNodes(t, bd[0], "pve1", "pve2", "pve3")

	// --- mtu_consistency -----------------------------------------------
	// MESS 4: vmbr0 MTU drifted (1500/9000/1500) across the cluster —
	// fixable (majority 1500, pve2 is the outlier).
	//
	// The within-node path sub-check (bridge MTU vs. its port's own MTU —
	// see TestMTUConsistency_Path for a targeted unit test of that half of
	// this family) does not additionally fire here: this test's harness is
	// a single collector with no peer client (matching every other
	// package's own fixture-driven test helper, e.g. internal/topology's
	// buildGraph), so host-netlink/host-interfaces data is only ever
	// polled for the local node (pve1, per GET /cluster/status's index-0
	// convention) — pve2 and pve3's PhysNic/Bridge MTU fields resolve
	// entirely from pve-network (PVE's own staged-aware network API),
	// which reports the *same* value for both a bridge and its declared
	// port whenever neither individually diverges from what PVE itself
	// last staged. A real cluster-aware deployment (T-303's peer fan-out)
	// would poll pve2's live host-interfaces directly and could observe
	// this; this harness's job is only to prove the fixture's documented
	// mess items surface correctly, not to re-prove T-303's own fan-out
	// (already covered by internal/collect's cluster harness tests).
	mtu := byCheck[drift.CheckMTUConsistency]
	if len(mtu) != 1 {
		t.Fatalf("mtu_consistency findings = %d, want 1: %+v", len(mtu), mtu)
	}
	crossNode := mtu[0]
	if !crossNode.Fixable {
		t.Fatalf("cross-node MTU divergence should be fixable: %+v", crossNode)
	}
	ops, _, ok := svc.FixOps(crossNode.ID)
	if !ok || len(ops) != 1 || ops[0].Target.Node != "pve2" {
		t.Errorf("cross-node MTU fix = ok=%v ops=%+v, want exactly one op targeting pve2", ok, ops)
	}

	// --- sdn_realization -------------------------------------------------
	// MESS 3 (realization half): zone-legacy lists pve3 as a member but
	// pve3 has no vmbr99.
	sdn := byCheck[drift.CheckSDNRealization]
	if len(sdn) != 1 {
		t.Fatalf("sdn_realization findings = %d, want 1: %+v", len(sdn), sdn)
	}
	assertNodes(t, sdn[0], "pve3")
	if sdn[0].Fixable {
		t.Errorf("sdn realization gap should not be fixable")
	}

	// --- sdn_zone_status (T-3701) ----------------------------------------
	// The same MESS 3 gap, observed a second, independent way: pve3's real
	// per-node GET /nodes/{node}/sdn/zones poll (via internal/collect's
	// pollSDN, exercised for real by this test's full pvemock round trip —
	// not injected) reports zone-legacy "error" on pve3 because its bridge
	// is genuinely missing there, matching internal/pvemock/sdn.go's own
	// bridge-existence check. This is a distinct signal from sdn_realization
	// above (a statically-computed membership/bridge comparison) even though
	// this particular fixture scenario happens to trip both — see
	// internal/drift/sdn.go's checkSDNZoneStatus doc comment.
	zoneStatus := byCheck[drift.CheckSDNZoneStatus]
	if len(zoneStatus) != 1 {
		t.Fatalf("sdn_zone_status findings = %d, want 1: %+v", len(zoneStatus), zoneStatus)
	}
	assertNodes(t, zoneStatus[0], "pve3")
	if zoneStatus[0].Fixable {
		t.Errorf("sdn zone status finding should not be fixable")
	}
	if zoneStatus[0].Severity != drift.SeverityError {
		t.Errorf("sdn_zone_status severity = %s, want error", zoneStatus[0].Severity)
	}

	// --- pending_interfaces ----------------------------------------------
	// MESS 1: pve2's eno1 has a staged, unapplied edit.
	pending := byCheck[drift.CheckPendingInterfaces]
	if len(pending) != 1 {
		t.Fatalf("pending_interfaces findings = %d, want 1: %+v", len(pending), pending)
	}
	assertNodes(t, pending[0], "pve2")
	if pending[0].Refs[0] != "physnic:pve2:eno1" {
		t.Errorf("pending finding ref = %v, want physnic:pve2:eno1", pending[0].Refs)
	}

	// --- file_runtime_divergence ------------------------------------------
	// MESS 10 (T-305 acceptance criterion 4): pve1's vmbr0 live membership
	// (eno1+eno3) diverges from its declared bridge_ports (eno1 only).
	fr := byCheck[drift.CheckFileRuntimeDivergence]
	if len(fr) != 1 {
		t.Fatalf("file_runtime_divergence findings = %d, want 1: %+v", len(fr), fr)
	}
	assertNodes(t, fr[0], "pve1")
	if fr[0].Refs[0] != "bridge:pve1:vmbr0" {
		t.Errorf("file/runtime finding ref = %v, want bridge:pve1:vmbr0", fr[0].Refs)
	}

	// Every finding's check name is one of the documented families — guards
	// against a stray/miscategorized finding slipping through.
	wantChecks := map[string]bool{
		drift.CheckBridgeDivergence: true, drift.CheckMTUConsistency: true,
		drift.CheckSDNRealization: true, drift.CheckSDNZoneStatus: true,
		drift.CheckPendingInterfaces: true, drift.CheckFileRuntimeDivergence: true,
	}
	for check := range byCheck {
		if !wantChecks[check] {
			t.Errorf("unexpected check family in findings: %s", check)
		}
	}

	if total := len(findings); total != 6 {
		names := make([]string, len(findings))
		for i, f := range findings {
			names[i] = f.Check + ":" + f.ID
		}
		sort.Strings(names)
		t.Fatalf("total findings = %d, want 5: %v", total, names)
	}
}

func assertNodes(t *testing.T, f drift.Finding, want ...string) {
	t.Helper()
	got := append([]string(nil), f.Nodes...)
	sort.Strings(got)
	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)
	if len(got) != len(wantSorted) {
		t.Fatalf("finding %s nodes = %v, want %v", f.ID, got, wantSorted)
	}
	for i := range got {
		if got[i] != wantSorted[i] {
			t.Fatalf("finding %s nodes = %v, want %v", f.ID, got, wantSorted)
		}
	}
}
