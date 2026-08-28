// SPDX-License-Identifier: Apache-2.0

package findings_test

import (
	"context"
	"sync"
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

type fakeProbe struct{ findings []findings.Finding }

func (f fakeProbe) Findings() []findings.Finding { return f.findings }

// TestFindings_ComposesAllFiveSources: AC2's premise — every producer's
// findings show up in one stream, each correctly tagged with its Source
// (drift/lldp/ipam/health, plus T-806's probe producer).
func TestFindings_ComposesAllFiveSources(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkBond(g, "pve1", "bond0", []inventory.BondSlaveState{
		{Name: "eno1", MIIStatus: "up", Active: true},
		{Name: "eno2", MIIStatus: "down", Active: false},
	})

	driftFinding := drift.Finding{ID: "bridge_divergence|bridge:pve1:vmbr0", Check: drift.CheckBridgeDivergence, Severity: drift.SeverityWarning, Detail: "bridge diverges", Nodes: []string{"pve1"}}
	lldpFinding := topology.VlanFinding{BridgeRef: "bridge:pve1:vmbr0", NeighborRef: "lldp-neighbor:pve1:eno1", Code: topology.VlanCheckMissingOnSwitch, Severity: "warning", Message: "switch missing vlan 20"}
	ipamFinding := findings.Finding{ID: "ipam:conflict|subnet1", Source: findings.SourceIPAM, Check: "subnet_conflict", Severity: findings.SeverityError, Detail: "two subnets overlap", Nodes: []string{"pve1"}}
	probeFinding := findings.Finding{ID: "probe:sim_divergence|guest-nic:pve1:300/net0", Source: findings.SourceProbe, Check: "sim_divergence", Severity: findings.SeverityWarning, Detail: "simulated allow, observed unreachable", Refs: []string{"guest-nic:pve1:300/net0"}}

	eng := findings.New(findings.Config{
		Graph: g,
		Drift: fakeDrift{findings: []drift.Finding{driftFinding}},
		LLDP:  fakeLLDP{findings: []topology.VlanFinding{lldpFinding}},
		IPAM:  fakeIPAM{findings: []findings.Finding{ipamFinding}},
		Probe: fakeProbe{findings: []findings.Finding{probeFinding}},
	})

	// Run twice so the bond-slave-down hysteresis (2 cycles) has fired.
	eng.Findings()
	all := eng.Findings()

	bySource := map[findings.Source]int{}
	for _, f := range all {
		bySource[f.Source]++
	}
	for _, src := range []findings.Source{findings.SourceDrift, findings.SourceLLDP, findings.SourceIPAM, findings.SourceHealth, findings.SourceProbe} {
		if bySource[src] == 0 {
			t.Errorf("no findings from source %q in the unified stream: %+v", src, all)
		}
	}
}

// TestFindings_ProbeProviderNil_ContributesNothing: a nil Config.Probe (no
// PVE client wired, e.g. every pre-T-806 caller) degrades quietly like
// every other optional producer — no panic, no probe-sourced findings.
func TestFindings_ProbeProviderNil_ContributesNothing(t *testing.T) {
	g := newGraphWithNodes("pve1")
	eng := findings.New(findings.Config{Graph: g})
	for _, f := range eng.Findings() {
		if f.Source == findings.SourceProbe {
			t.Fatalf("got a probe-sourced finding with a nil Config.Probe: %+v", f)
		}
	}
}

type fakeWebhooks struct{ findings []findings.Finding }

func (f fakeWebhooks) Findings() []findings.Finding { return f.findings }

// TestFindings_WebhookProvider_ContributesHealthFindings (T-1104): a
// wired Config.Webhooks producer's findings show up in the unified
// stream, tagged source health (webhook_unhealthy is a health check, not a
// distinct producer family — see adapt_webhook.go's doc comment).
func TestFindings_WebhookProvider_ContributesHealthFindings(t *testing.T) {
	g := newGraphWithNodes("pve1")
	wf := findings.Finding{
		ID: "health:webhook_unhealthy|wh1", Source: findings.SourceHealth,
		Check: "webhook_unhealthy", Severity: findings.SeverityWarning,
		Detail: "5 consecutive delivery failures", Nodes: []string{},
	}
	eng := findings.New(findings.Config{Graph: g, Webhooks: fakeWebhooks{findings: []findings.Finding{wf}}})

	found := findByCheck(t, eng.Findings(), "webhook_unhealthy")
	if len(found) != 1 {
		t.Fatalf("expected 1 webhook_unhealthy finding, got %d: %+v", len(found), eng.Findings())
	}
	if found[0].Source != findings.SourceHealth {
		t.Errorf("webhook_unhealthy finding Source = %q, want health", found[0].Source)
	}
}

// TestFindings_WebhookProviderNil_ContributesNothing: nil Config.Webhooks
// (no webhook registrations wired) degrades quietly, same as every other
// optional producer.
func TestFindings_WebhookProviderNil_ContributesNothing(t *testing.T) {
	g := newGraphWithNodes("pve1")
	eng := findings.New(findings.Config{Graph: g})
	for _, f := range eng.Findings() {
		if f.Check == "webhook_unhealthy" {
			t.Fatalf("got a webhook_unhealthy finding with a nil Config.Webhooks: %+v", f)
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

// TestOnCycle_FiresEveryCycleWithTheWholeStream is T-2603's seam contract, and
// deliberately the OPPOSITE property to the transition hook above: OnCycle must
// fire on every cycle of an UNCHANGING stream, carrying the findings
// themselves. The change engine decides "new relative to this changeset's
// pre-apply baseline"; a hook that fired only on transitions would leave it
// deciding "new since the last time something else moved".
//
// The control is TestNotify_FiresOncePerTransition above, over the same
// unchanging fixture: that one must see exactly one call where this one sees
// many, so a shared "fires once" bug cannot pass both.
func TestOnCycle_FiresEveryCycleWithTheWholeStream(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkBond(g, "pve1", "bond0", []inventory.BondSlaveState{
		{Name: "eno1", MIIStatus: "up", Active: true},
		{Name: "eno2", MIIStatus: "down", Active: false},
	})

	var mu sync.Mutex
	var cycles [][]findings.Finding
	eng := findings.New(findings.Config{
		Graph: g, Interval: 5 * time.Millisecond,
		OnCycle: func(_ context.Context, fs []findings.Finding) {
			mu.Lock()
			defer mu.Unlock()
			cycles = append(cycles, fs)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if err := eng.RunLoop(ctx); err != nil {
		t.Fatalf("RunLoop: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(cycles) < 3 {
		t.Fatalf("OnCycle fired %d time(s) across ~16 cycles of an unchanging stream, want one per cycle", len(cycles))
	}
	var sawFinding bool
	for _, fs := range cycles {
		for _, f := range fs {
			if f.Check == "bond_slave_down" {
				sawFinding = true
			}
		}
	}
	if !sawFinding {
		t.Error("OnCycle never carried the fixture's own finding; the hook must deliver the stream, not merely a count")
	}
}

// TestOnCycle_NilIsANoOp pins the same nil-safe-optional convention every
// other Config seam in this package follows.
func TestOnCycle_NilIsANoOp(t *testing.T) {
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Interval: 5 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := eng.RunLoop(ctx); err != nil {
		t.Fatalf("RunLoop with no OnCycle: %v", err)
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
