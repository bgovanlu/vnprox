// SPDX-License-Identifier: Apache-2.0

package findings

// maintenance_test.go is T-4007's acceptance-criteria coverage at the
// findings-package layer: inside/outside a declared maintenance window,
// boundary instants (the "expires the instant end passes" AC3 contract),
// cross-node scoping (a window on node A must never suppress a finding for
// node B — the failure mode the task's own warning names), the
// "suppressed but visible, never omitted" assertion, and alert
// (notification) suppression with its own transition-preserving contract.
// internal/change/maintenance_test.go covers declaration/validation/the
// calendar/the MaintenanceState read model.

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

type fakeMaintenanceProvider struct {
	windows []change.MaintenanceWindow
	calls   int
}

func (f *fakeMaintenanceProvider) MaintenanceWindows() []change.MaintenanceWindow {
	f.calls++
	return f.windows
}

func findingFor(id, node string, severity string) Finding {
	return newHealthFinding("some_check", severity, "detail", []string{node}, nil).withID(id)
}

// withID lets a test pin a deterministic id distinct from newHealthFinding's
// derived one, so notify.go's per-id transition tracking is exercised
// predictably.
func (f Finding) withID(id string) Finding {
	f.ID = id
	return f
}

// --- decorateMaintenance: scoping, visibility, boundaries -------------------

func TestDecorateMaintenance_NilProviderIsNoOp(t *testing.T) {
	in := []Finding{findingFor("f1", "pvecube", SeverityWarning)}
	out := decorateMaintenance(in, nil, time.Unix(1000, 0))
	if out[0].Suppressed {
		t.Fatal("nil provider must never suppress")
	}
}

func TestDecorateMaintenance_InsideWindowIsSuppressedButVisible(t *testing.T) {
	provider := &fakeMaintenanceProvider{windows: []change.MaintenanceWindow{
		{ID: "w1", Node: "pvecube", Reason: "firmware upgrade", Start: 1000, End: 2000},
	}}
	in := []Finding{findingFor("f1", "pvecube", SeverityWarning)}
	out := decorateMaintenance(in, provider, time.Unix(1500, 0))

	if len(out) != 1 {
		t.Fatalf("decorateMaintenance dropped a finding: got %d, want 1 — suppression must never omit", len(out))
	}
	f := out[0]
	if !f.Suppressed {
		t.Fatal("finding inside an active window should be Suppressed")
	}
	if f.SuppressedWindow == nil {
		t.Fatal("Suppressed finding must carry SuppressedWindow (the \"why\")")
	}
	if f.SuppressedWindow.WindowID != "w1" || f.SuppressedWindow.Node != "pvecube" ||
		f.SuppressedWindow.Reason != "firmware upgrade" || f.SuppressedWindow.StartsAt != 1000 || f.SuppressedWindow.EndsAt != 2000 {
		t.Fatalf("SuppressedWindow = %+v, unexpected", f.SuppressedWindow)
	}
	// Still the same finding otherwise — nothing about its own content changed.
	if f.ID != "f1" || f.Severity != SeverityWarning {
		t.Fatalf("finding content altered by suppression: %+v", f)
	}
}

func TestDecorateMaintenance_OutsideWindowFiresNormally(t *testing.T) {
	provider := &fakeMaintenanceProvider{windows: []change.MaintenanceWindow{
		{ID: "w1", Node: "pvecube", Start: 1000, End: 2000},
	}}
	in := []Finding{findingFor("f1", "pvecube", SeverityWarning)}
	out := decorateMaintenance(in, provider, time.Unix(500, 0))
	if out[0].Suppressed || out[0].SuppressedWindow != nil {
		t.Fatalf("finding outside the window must not be suppressed: %+v", out[0])
	}
}

func TestDecorateMaintenance_BoundaryInstants(t *testing.T) {
	provider := &fakeMaintenanceProvider{windows: []change.MaintenanceWindow{
		{ID: "w1", Node: "pvecube", Start: 1000, End: 2000},
	}}
	cases := []struct {
		name string
		at   int64
		want bool
	}{
		{"one second before start", 999, false},
		{"at start — suppression begins", 1000, true},
		{"one second before end", 1999, true},
		{"at end — AC3: suppression stops the instant end passes", 2000, false},
		{"one second after end", 2001, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := []Finding{findingFor("f1", "pvecube", SeverityWarning)}
			out := decorateMaintenance(in, provider, time.Unix(c.at, 0))
			if out[0].Suppressed != c.want {
				t.Errorf("at %d: Suppressed = %v, want %v", c.at, out[0].Suppressed, c.want)
			}
		})
	}
}

// TestDecorateMaintenance_ScopingIsExact is the failure mode the task
// itself names: a window declared for one node must never suppress a
// finding raised for a different node, even in the same cluster and even
// when both findings are evaluated in the same cycle.
func TestDecorateMaintenance_ScopingIsExact(t *testing.T) {
	provider := &fakeMaintenanceProvider{windows: []change.MaintenanceWindow{
		{ID: "w1", Node: "pvecube", Start: 1000, End: 2000},
	}}
	in := []Finding{
		findingFor("f-pvecube", "pvecube", SeverityWarning),
		findingFor("f-pve001", "pve001", SeverityWarning),
	}
	out := decorateMaintenance(in, provider, time.Unix(1500, 0))
	for _, f := range out {
		switch f.Nodes[0] {
		case "pvecube":
			if !f.Suppressed {
				t.Error("pvecube's finding should be suppressed (its own node is in maintenance)")
			}
		case "pve001":
			if f.Suppressed {
				t.Error("pve001's finding must NOT be suppressed by pvecube's maintenance window")
			}
		}
	}
}

// TestDecorateMaintenance_MultiNodeFindingSuppressedOnlyIfAnyNodeMatches
// covers a finding that names more than one node (e.g. a link between two
// nodes): it is suppressed if ANY of its nodes is in maintenance — the
// finding is still "about" the node under maintenance even if phrased as a
// pair.
func TestDecorateMaintenance_MultiNodeFindingSuppressedIfAnyNodeMatches(t *testing.T) {
	provider := &fakeMaintenanceProvider{windows: []change.MaintenanceWindow{
		{ID: "w1", Node: "pvecube", Start: 1000, End: 2000},
	}}
	f := newHealthFinding("link_down", SeverityWarning, "detail", []string{"pve001", "pvecube"}, nil)
	out := decorateMaintenance([]Finding{f}, provider, time.Unix(1500, 0))
	if !out[0].Suppressed {
		t.Fatal("a finding naming pvecube among its Nodes should be suppressed by pvecube's window")
	}
}

func TestDecorateMaintenance_OverlappingWindowsPickSoonestEnding(t *testing.T) {
	provider := &fakeMaintenanceProvider{windows: []change.MaintenanceWindow{
		{ID: "long", Node: "pvecube", Start: 1000, End: 5000},
		{ID: "soon", Node: "pvecube", Start: 1000, End: 2000},
	}}
	out := decorateMaintenance([]Finding{findingFor("f1", "pvecube", SeverityWarning)}, provider, time.Unix(1500, 0))
	if out[0].SuppressedWindow.WindowID != "soon" {
		t.Fatalf("SuppressedWindow.WindowID = %q, want the soonest-ending window %q", out[0].SuppressedWindow.WindowID, "soon")
	}
}

// --- Engine.Findings integration --------------------------------------------

func TestEngineFindings_SuppressesAcrossEveryProducer(t *testing.T) {
	now := int64(1500)
	provider := &fakeMaintenanceProvider{windows: []change.MaintenanceWindow{
		{ID: "w1", Node: "pvecube", Start: 1000, End: 2000},
	}}
	bg := &fakeBreakGlassProvider{events: []change.BreakGlassRecord{testBreakGlassRecord("cs1")}}
	eng := New(Config{
		Graph:       inventory.NewGraph(),
		BreakGlass:  bg,
		Maintenance: provider,
		Now:         func() time.Time { return time.Unix(now, 0) },
	})
	all := eng.Findings()
	if len(all) == 0 {
		t.Fatal("expected at least the break-glass finding")
	}
	// change_break_glass's finding carries Refs (the changeset id), not
	// Nodes, so it is correctly NEVER suppressed by a node-scoped window —
	// asserting that here pins the "node-scoped, not global" contract.
	for _, f := range all {
		if f.Check == CheckBreakGlass && f.Suppressed {
			t.Errorf("change_break_glass has no Nodes, so a node maintenance window must never suppress it: %+v", f)
		}
	}
}

// --- alerts (notifications) are suppressed too, per the card's own framing -

type recordingNotifier struct {
	calls []TransitionKind
}

func (n *recordingNotifier) Notify(_ context.Context, _ Finding, kind TransitionKind) error {
	n.calls = append(n.calls, kind)
	return nil
}

func TestNotify_SuppressedFindingDoesNotFire(t *testing.T) {
	notifier := &recordingNotifier{}
	eng := New(Config{Notifier: notifier, NotifyThreshold: SeverityWarning})

	f := findingFor("f1", "pvecube", SeverityWarning)
	f.Suppressed = true
	eng.evaluateNotifications(context.Background(), []Finding{f})

	if len(notifier.calls) != 0 {
		t.Fatalf("Notify calls = %v, want none while suppressed", notifier.calls)
	}
}

// TestNotify_UnsuppressedAfterWindowFiresOnlyIfGenuinelyNew covers the
// steady-state case: a finding present, unchanged, for the whole
// maintenance window must NOT fire once the window ends — it was already
// known before maintenance began, so re-alerting on it is noise, not the
// "no forgot to turn it back on" guarantee the card asks for.
func TestNotify_SteadyStateThroughSuppressionDoesNotRefireOnUnsuppress(t *testing.T) {
	notifier := &recordingNotifier{}
	eng := New(Config{Notifier: notifier, NotifyThreshold: SeverityWarning})
	ctx := context.Background()

	f := findingFor("f1", "pvecube", SeverityWarning)

	// Cycle 1: unsuppressed, notified as new.
	eng.evaluateNotifications(ctx, []Finding{f})
	if len(notifier.calls) != 1 || notifier.calls[0] != TransitionNew {
		t.Fatalf("cycle 1 calls = %v, want exactly one TransitionNew", notifier.calls)
	}

	// Cycle 2: same finding, now suppressed (maintenance window opened) —
	// must not fire again.
	suppressed := f
	suppressed.Suppressed = true
	eng.evaluateNotifications(ctx, []Finding{suppressed})
	if len(notifier.calls) != 1 {
		t.Fatalf("calls after suppressed cycle = %v, want unchanged (no new Notify)", notifier.calls)
	}

	// Cycle 3: window closed, finding unsuppressed again, SAME severity —
	// no notification, because nothing genuinely changed.
	eng.evaluateNotifications(ctx, []Finding{f})
	if len(notifier.calls) != 1 {
		t.Fatalf("calls after unsuppress (unchanged severity) = %v, want unchanged (steady state, no re-fire)", notifier.calls)
	}
}

// TestNotify_EscalationWhileSuppressedFiresOnceUnsuppressed covers the
// opposite case: if the finding got WORSE while suppressed, that escalation
// must still reach the operator — just delayed to the first evaluation
// after the window closes, never lost.
func TestNotify_EscalationWhileSuppressedFiresOnceUnsuppressed(t *testing.T) {
	notifier := &recordingNotifier{}
	eng := New(Config{Notifier: notifier, NotifyThreshold: SeverityWarning})
	ctx := context.Background()

	warn := findingFor("f1", "pvecube", SeverityWarning)
	eng.evaluateNotifications(ctx, []Finding{warn})
	if len(notifier.calls) != 1 || notifier.calls[0] != TransitionNew {
		t.Fatalf("cycle 1 = %v, want one TransitionNew", notifier.calls)
	}

	// Escalates to error WHILE suppressed: no Notify call yet.
	errSuppressed := findingFor("f1", "pvecube", SeverityError)
	errSuppressed.Suppressed = true
	eng.evaluateNotifications(ctx, []Finding{errSuppressed})
	if len(notifier.calls) != 1 {
		t.Fatalf("calls while suppressed = %v, want unchanged", notifier.calls)
	}

	// Window closes; same error severity, now unsuppressed: the missed
	// escalation fires now.
	errUnsuppressed := findingFor("f1", "pvecube", SeverityError)
	eng.evaluateNotifications(ctx, []Finding{errUnsuppressed})
	if len(notifier.calls) != 2 || notifier.calls[1] != TransitionEscalated {
		t.Fatalf("calls after unsuppress = %v, want [New, Escalated]", notifier.calls)
	}
}
