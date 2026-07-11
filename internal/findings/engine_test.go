package findings_test

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

type fakeDrift struct{ findings []drift.Finding }

func (f fakeDrift) Findings() []drift.Finding                 { return f.findings }
func (f fakeDrift) FixOps(string) ([]change.Op, string, bool) { return nil, "", false }

type fakeLLDP struct{ findings []topology.VlanFinding }

func (f fakeLLDP) VlanFindings() []topology.VlanFinding { return f.findings }

type fakeIPAM struct{ findings []findings.Finding }

func (f fakeIPAM) Findings() []findings.Finding { return f.findings }

// TestFindings_ComposesAllFourSources: AC2's premise — every producer's
// findings show up in one stream, each correctly tagged with its Source.
func TestFindings_ComposesAllFourSources(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkBond(g, "pve1", "bond0", []inventory.BondSlaveState{
		{Name: "eno1", MIIStatus: "up", Active: true},
		{Name: "eno2", MIIStatus: "down", Active: false},
	})

	driftFinding := drift.Finding{ID: "bridge_divergence|bridge:pve1:vmbr0", Check: drift.CheckBridgeDivergence, Severity: drift.SeverityWarning, Detail: "bridge diverges", Nodes: []string{"pve1"}}
	lldpFinding := topology.VlanFinding{BridgeRef: "bridge:pve1:vmbr0", NeighborRef: "lldp-neighbor:pve1:eno1", Code: topology.VlanCheckMissingOnSwitch, Severity: "warning", Message: "switch missing vlan 20"}
	ipamFinding := findings.Finding{ID: "ipam:conflict|subnet1", Source: findings.SourceIPAM, Check: "subnet_conflict", Severity: findings.SeverityError, Detail: "two subnets overlap", Nodes: []string{"pve1"}}

	eng := findings.New(findings.Config{
		Graph: g,
		Drift: fakeDrift{findings: []drift.Finding{driftFinding}},
		LLDP:  fakeLLDP{findings: []topology.VlanFinding{lldpFinding}},
		IPAM:  fakeIPAM{findings: []findings.Finding{ipamFinding}},
	})

	// Run twice so the bond-slave-down hysteresis (2 cycles) has fired.
	eng.Findings()
	all := eng.Findings()

	bySource := map[findings.Source]int{}
	for _, f := range all {
		bySource[f.Source]++
	}
	for _, src := range []findings.Source{findings.SourceDrift, findings.SourceLLDP, findings.SourceIPAM, findings.SourceHealth} {
		if bySource[src] == 0 {
			t.Errorf("no findings from source %q in the unified stream: %+v", src, all)
		}
	}
}

// TestFixOps_DispatchesToDrift: Engine.FixOps strips the "drift:" id prefix
// and forwards to the DriftProvider unchanged.
func TestFixOps_DispatchesToDrift(t *testing.T) {
	wantOps := []change.Op{{Type: change.OpBridgeUpdate}}
	driftFindings := []drift.Finding{{ID: "mtu_consistency|bridge:pve1:vmbr0", Check: drift.CheckMTUConsistency, Fixable: true}}

	g := newGraphWithNodes("pve1")
	eng := findings.New(findings.Config{Graph: g, Drift: fixOpsFakeDrift{ops: wantOps, title: "drift: align mtu", findings: driftFindings}})

	found := findByCheck(t, eng.Findings(), drift.CheckMTUConsistency)
	if len(found) != 1 {
		t.Fatalf("expected 1 adapted drift finding, got %d", len(found))
	}
	ops, title, ok := eng.FixOps(found[0].ID)
	if !ok {
		t.Fatal("FixOps returned ok=false, want true")
	}
	if title != "drift: align mtu" {
		t.Errorf("title = %q, want %q", title, "drift: align mtu")
	}
	if len(ops) != 1 || ops[0].Type != change.OpBridgeUpdate {
		t.Errorf("ops = %+v, want a single bridge.update op", ops)
	}
}

type fixOpsFakeDrift struct {
	title    string
	findings []drift.Finding
	ops      []change.Op
}

func (f fixOpsFakeDrift) Findings() []drift.Finding { return f.findings }
func (f fixOpsFakeDrift) FixOps(id string) ([]change.Op, string, bool) {
	for _, d := range f.findings {
		if d.ID == id {
			return f.ops, f.title, true
		}
	}
	return nil, "", false
}

// fakeNotifier records every Notify call for the transition test below.
type fakeNotifier struct {
	calls []findings.TransitionKind
}

func (n *fakeNotifier) Notify(_ context.Context, _ findings.Finding, kind findings.TransitionKind) error {
	n.calls = append(n.calls, kind)
	return nil
}

// TestNotify_FiresOncePerTransition is AC5: "Notification hook fires once
// per finding transition (not per cycle) on a threshold fixture." A bond
// slave stays down across many RunLoop cycles; the notifier must be called
// exactly once (the "new" transition), not once per cycle.
func TestNotify_FiresOncePerTransition(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkBond(g, "pve1", "bond0", []inventory.BondSlaveState{
		{Name: "eno1", MIIStatus: "up", Active: true},
		{Name: "eno2", MIIStatus: "down", Active: false},
	})

	notifier := &fakeNotifier{}
	eng := findings.New(findings.Config{
		Graph: g, Interval: 5 * time.Millisecond,
		Notifier: notifier, NotifyThreshold: findings.SeverityWarning,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if err := eng.RunLoop(ctx); err != nil {
		t.Fatalf("RunLoop: %v", err)
	}

	newCount := 0
	for _, k := range notifier.calls {
		if k == findings.TransitionNew {
			newCount++
		}
	}
	if newCount != 1 {
		t.Fatalf("notifier fired %d TransitionNew calls across ~16 cycles of an unchanging breach, want exactly 1: %v", newCount, notifier.calls)
	}
}

// TestNotify_ResolvesOnceWhenFindingClears: after the breach clears, exactly
// one TransitionResolved notification fires.
func TestNotify_ResolvesOnceWhenFindingClears(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkBond(g, "pve1", "bond0", []inventory.BondSlaveState{
		{Name: "eno1", MIIStatus: "up", Active: true},
		{Name: "eno2", MIIStatus: "down", Active: false},
	})

	notifier := &fakeNotifier{}
	eng := findings.New(findings.Config{
		Graph: g, Interval: 5 * time.Millisecond,
		Notifier: notifier, NotifyThreshold: findings.SeverityWarning,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- eng.RunLoop(ctx) }()

	time.Sleep(30 * time.Millisecond)
	netlinkBond(g, "pve1", "bond0", []inventory.BondSlaveState{
		{Name: "eno1", MIIStatus: "up", Active: true},
		{Name: "eno2", MIIStatus: "up", Active: true},
	})
	time.Sleep(60 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunLoop: %v", err)
	}

	resolvedCount := 0
	for _, k := range notifier.calls {
		if k == findings.TransitionResolved {
			resolvedCount++
		}
	}
	if resolvedCount != 1 {
		t.Fatalf("notifier fired %d TransitionResolved calls, want exactly 1: %v", resolvedCount, notifier.calls)
	}
}
