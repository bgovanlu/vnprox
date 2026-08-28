// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// fwGuestRef is single-node.yaml's qemu guest 100 ("web01")'s firewall
// ruleset target, per internal/change/params_fw.go's documented Ref
// convention (Ref{Kind: KindFwRuleset, Node: n, ID: "guest/<kind>/<vmid>"}).
// The fixture seeds it with one pre-existing rule ("web traffic", tcp
// 80/443) — every test below accounts for that rather than assuming an
// empty ruleset, since it's the same shared fixture other suites' exact
// rule-count assertions depend on (docs/development.md: "every feature
// must work against single-node.yaml").
func fwGuestRef() inventory.Ref {
	return inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "guest/qemu/100"}
}

func fwGuestScope() pve.FirewallScope {
	return pve.GuestFirewallScope("pve1", pve.GuestQemu, 100)
}

// liveFwInventorySource is a change.InventorySource that reflects target's
// live pvemock ruleset content on every call. Production wires a real
// *inventory.Graph kept current by internal/collect's poller; these apply-
// engine tests have no poller running, so this is the minimal stand-in
// fw.rule.move/update/delete's requireRuleset referential check needs to
// see what this test has actually done to the ruleset via the PVE API
// (including out-of-band pokes that simulate a concurrent edit — the same
// thing a real poll would eventually observe too).
type liveFwInventorySource struct {
	client *pve.Client
	target inventory.Ref
	scope  pve.FirewallScope
}

func (s liveFwInventorySource) Snapshot() inventory.Snapshot {
	g := inventory.NewGraph()
	rules, err := s.client.ListFirewallRules(context.Background(), s.scope)
	if err != nil {
		return g.Snapshot()
	}
	invRules := make([]inventory.FwRule, len(rules))
	for i, r := range rules {
		invRules[i] = inventory.FwRule{
			Pos: r.Pos, Enabled: r.Enabled, Direction: r.Type, Action: r.Action,
			Proto: r.Proto, Source: r.Source, Dest: r.Dest, Sport: r.Sport, Dport: r.Dport,
			Iface: r.Iface, Macro: r.Macro, Log: r.Log, Comment: r.Comment,
		}
	}
	g.ApplyPoll(inventory.SourcePVEFirewall, inventory.Scope{Kinds: []inventory.Kind{inventory.KindFwRuleset}}, []inventory.Entity{
		&inventory.FwRuleset{Ref: s.target, Scope: inventory.FwScopeGuest, Enabled: true, Rules: invRules},
	})
	return g.Snapshot()
}

// newFwHarness is newHarness plus a liveFwInventorySource wired for
// fwGuestRef, so revalidation (beginApply's ValidateWithSafety call
// immediately before every apply) sees the ruleset's real, current PVE
// state rather than an empty snapshot.
func newFwHarness(t *testing.T, fixturePath string) *applyHarness {
	t.Helper()
	h := newHarness(t, fixturePath)
	inv := liveFwInventorySource{client: h.client, target: fwGuestRef(), scope: fwGuestScope()}
	svc, err := change.NewService(change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws, Inventory: inv,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: h.timers.New,
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	h.svc = svc
	return h
}

// TestApply_Firewall_Lifecycle_BuildReorderApplyVerify is T-502 acceptance
// criterion 1: build a guest ruleset (3 rules via builder incl. a macro),
// reorder by drag (fw.rule.move), apply -> pvemock's firewall state matches
// the expected golden order/content; post-apply verification (StepFwVerify)
// passes.
func TestApply_Firewall_Lifecycle_BuildReorderApplyVerify(t *testing.T) {
	h := newFwHarness(t, fixtureSingleNode)
	ctx := context.Background()
	gw := &fakePVEGateway{client: h.client, pollNode: "pve1"}
	target := fwGuestRef()

	// The fixture's guest already has one rule (pos 0, "web traffic"); the
	// three new rules are appended after it (pos 1..3, matching what
	// pvemock's create-always-appends semantics will actually assign, so
	// no create needs an extra move of its own — only the final drag
	// reorders one).
	ops := []change.Op{
		{Type: change.OpFwRuleCreate, Target: target, Params: &change.FwRuleCreateParams{
			Direction: "in", Action: "ACCEPT", Macro: "HTTP", Comment: "web", Pos: 1, Enabled: true,
		}},
		{Type: change.OpFwRuleCreate, Target: target, Params: &change.FwRuleCreateParams{
			Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "22", Comment: "ssh", Pos: 2, Enabled: true,
		}},
		{Type: change.OpFwRuleCreate, Target: target, Params: &change.FwRuleCreateParams{
			Direction: "out", Action: "DROP", Comment: "deny egress test", Pos: 3, Enabled: true,
		}},
		// Drag-to-reorder: move the HTTP rule (drafted at pos 1) to the end
		// (pos 3). Expect captures exactly what the create op above
		// establishes, since by the time this step runs (same changeset,
		// same StepFwApply step, ops execute in order) that's what's live
		// at pos 1.
		{Type: change.OpFwRuleMove, Target: target, Params: &change.FwRuleMoveParams{
			FromPos: 1, ToPos: 3,
			Expect: &change.FwRuleFields{Direction: "in", Action: "ACCEPT", Macro: "HTTP", Comment: "web", Enabled: true},
		}},
	}

	cs := h.mustCreate(t, "root@pam", "guest firewall ruleset", ops)
	applied, err := h.svc.Apply(ctx, cs.ID, "root@pam", gw, 0)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after apply = %s, want awaiting_confirm (post-apply verification should have passed)", applied.Status)
	}

	log := h.applyLog(t, cs.ID)
	for _, s := range log.Steps {
		if s.Status != change.StepOK {
			t.Fatalf("step %d (%s) status = %s, want ok: %s", s.Index, s.Kind, s.Status, s.Error)
		}
	}
	sawApply, sawVerify := false, false
	for _, s := range log.Steps {
		if s.Kind == change.StepFwApply {
			sawApply = true
		}
		if s.Kind == change.StepFwVerify {
			sawVerify = true
		}
	}
	if !sawApply || !sawVerify {
		t.Fatalf("apply log missing fw_apply/fw_verify steps: %+v", log.Steps)
	}

	// Golden: pvemock's live ruleset now reads [web-traffic, ssh,
	// deny-egress, web(HTTP)] — the reorder moved the HTTP rule to the
	// end, exactly as drafted.
	rules, err := h.client.ListFirewallRules(ctx, fwGuestScope())
	if err != nil {
		t.Fatalf("ListFirewallRules: %v", err)
	}
	if len(rules) != 4 {
		t.Fatalf("len(rules) = %d, want 4: %+v", len(rules), rules)
	}
	wantOrder := []string{"web traffic", "ssh", "deny egress test", "web"}
	for i, r := range rules {
		if r.Pos != i {
			t.Errorf("rules[%d].Pos = %d, want %d", i, r.Pos, i)
		}
		if r.Comment != wantOrder[i] {
			t.Errorf("rules[%d].Comment = %q, want %q (order: %+v)", i, r.Comment, wantOrder[i], rules)
		}
	}
	if rules[3].Macro != "HTTP" {
		t.Errorf("rules[3] (moved HTTP rule) = %+v, macro not preserved across move", rules[3])
	}
}

// TestApply_Firewall_MoveRace_RevalidationCatchesShiftedPosition is T-502
// acceptance criterion 3: if the ruleset's live position state shifts
// between drafting a fw.rule.move and applying it, apply-time revalidation
// catches the mismatch and fails cleanly rather than silently moving
// whatever now happens to occupy that position.
func TestApply_Firewall_MoveRace_RevalidationCatchesShiftedPosition(t *testing.T) {
	h := newFwHarness(t, fixtureSingleNode)
	ctx := context.Background()
	gw := &fakePVEGateway{client: h.client, pollNode: "pve1"}
	target := fwGuestRef()
	scope := fwGuestScope()

	// Baseline: two more rules on top of the fixture's pre-existing one,
	// applied and committed via a first changeset (so the "current
	// fixture state" the second changeset's move is drafted against is
	// real, applied state, not just an in-memory draft).
	baseline := h.mustCreate(t, "root@pam", "baseline rules", []change.Op{
		{Type: change.OpFwRuleCreate, Target: target, Params: &change.FwRuleCreateParams{
			Direction: "in", Action: "ACCEPT", Comment: "rule-a", Pos: 1, Enabled: true,
		}},
		{Type: change.OpFwRuleCreate, Target: target, Params: &change.FwRuleCreateParams{
			Direction: "in", Action: "ACCEPT", Comment: "rule-b", Pos: 2, Enabled: true,
		}},
	})
	if _, err := h.svc.Apply(ctx, baseline.ID, "root@pam", gw, 0); err != nil {
		t.Fatalf("Apply(baseline): %v", err)
	}
	if _, err := h.svc.Confirm(ctx, baseline.ID, "root@pam"); err != nil {
		t.Fatalf("Confirm(baseline): %v", err)
	}

	// Draft a move of "rule-a" (observed at pos 1) to pos 2 — Expect
	// captures rule-a's content as seen at draft time.
	moveCS := h.mustCreate(t, "root@pam", "reorder rule-a", []change.Op{
		{Type: change.OpFwRuleMove, Target: target, Params: &change.FwRuleMoveParams{
			FromPos: 1, ToPos: 2,
			Expect: &change.FwRuleFields{Direction: "in", Action: "ACCEPT", Comment: "rule-a", Enabled: true},
		}},
	})

	// Simulate a concurrent edit landing between draft and apply: delete
	// what's at pos 1 and insert a different rule there, so pos 1 no
	// longer holds what the draft expects.
	if err := h.client.DeleteFirewallRule(ctx, scope, 1); err != nil {
		t.Fatalf("simulating concurrent edit (delete): %v", err)
	}
	if err := h.client.CreateFirewallRule(ctx, scope, pve.FirewallRule{Type: "in", Action: "DROP", Comment: "unrelated-concurrent-rule"}); err != nil {
		t.Fatalf("simulating concurrent edit (create): %v", err)
	}
	before, err := h.client.ListFirewallRules(ctx, scope)
	if err != nil {
		t.Fatalf("ListFirewallRules (pre-apply snapshot of tampered state): %v", err)
	}

	_, err = h.svc.Apply(ctx, moveCS.ID, "root@pam", gw, 0)
	if err == nil {
		t.Fatal("Apply succeeded, want a revalidation failure (position changed since draft)")
	}
	var posErr *change.ErrFwPositionChanged
	if !errors.As(err, &posErr) {
		t.Fatalf("Apply error = %v (%T), want *change.ErrFwPositionChanged somewhere in the chain", err, err)
	}

	final := h.get(t, moveCS.ID)
	if final.Status != change.StatusFailed {
		t.Fatalf("status after failed apply = %s, want failed", final.Status)
	}

	// No silent misplacement: the live ruleset is exactly what it was
	// right before this Apply call — the move never happened, and nothing
	// else was disturbed.
	after, err := h.client.ListFirewallRules(ctx, scope)
	if err != nil {
		t.Fatalf("ListFirewallRules (post-apply): %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("rule count changed: before=%d after=%d", len(before), len(after))
	}
	for i := range before {
		if before[i].Comment != after[i].Comment || before[i].Pos != after[i].Pos {
			t.Errorf("rules[%d] changed: before=%+v after=%+v", i, before[i], after[i])
		}
	}
}
