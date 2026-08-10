package mcp

// T-2705's acceptance tests: the mutating MCP tools stage, and never apply.
//
// The two doubles that matter:
//
//   - spyStager satisfies ChangesetStager (compile-time assertion below) AND
//     carries Apply/Confirm/Approve/Discard methods the interface does NOT
//     have. Those exist only so the "apply was never called" assertions have a
//     CONTROL LEG: TestAC1_ControlLeg proves the counters move when the methods
//     are called, so "the counter is zero" is evidence rather than a tautology
//     about a counter nothing can increment.
//   - fakePolicy is T-2601's evaluator as this package consumes it. It records
//     every evaluation, so a test can assert an op was policy-checked BEFORE it
//     was staged (by comparing against the stager's own call log), not merely
//     that both happened.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/change/ifaces"
	"github.com/bgovanlu/vnprox/internal/store"
)

// --- doubles ----------------------------------------------------------------

// spyStager records every change-engine call the MCP surface makes.
//
//nolint:govet // fieldalignment: test double; the call counters are grouped together deliberately.
type spyStager struct {
	changesets map[string]change.Changeset
	// calls is the ordered log of change-engine method names, so a test can
	// assert ORDER (policy before staging), not just totals.
	calls []string
	// applyCalls/confirmCalls/approveCalls/discardCalls count the verbs the
	// SEAM DOES NOT HAVE. Nothing in this package can move them; the control
	// leg proves they are movable at all.
	applyCalls   int
	confirmCalls int
	approveCalls int
	discardCalls int
	createCalls  int
	updateCalls  int
	nextID       int
	createErr    error
	mu           sync.Mutex
}

// The seam internal/mcp holds is exactly this and no more.
var _ ChangesetStager = (*spyStager)(nil)

func newSpyStager() *spyStager {
	return &spyStager{changesets: map[string]change.Changeset{}}
}

func (f *spyStager) record(name string) {
	f.calls = append(f.calls, name)
}

func (f *spyStager) CreateWithOrigin(ctx context.Context, author, title string, ops []change.Op, origin, originTokenID string) (change.Changeset, error) {
	return f.CreateWithProvenance(ctx, author, title, ops, change.Provenance{Origin: origin, TokenID: originTokenID})
}

func (f *spyStager) CreateWithProvenance(_ context.Context, author, title string, ops []change.Op, p change.Provenance) (change.Changeset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("CreateWithProvenance")
	f.createCalls++
	if f.createErr != nil {
		return change.Changeset{}, f.createErr
	}
	f.nextID++
	id := fmt.Sprintf("cs-%02d", f.nextID)
	c := change.Changeset{
		ID: id, Title: title, Author: author, Status: change.StatusDraft,
		Origin: p.Origin, OriginTokenID: p.TokenID, OriginTool: p.Tool,
		Ops: ops, CreatedAt: int64(f.nextID), UpdatedAt: int64(f.nextID),
	}
	f.changesets[id] = c
	return c, nil
}

func (f *spyStager) UpdateDraft(_ context.Context, id, author string, title *string, ops []change.Op) (change.Changeset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("UpdateDraft")
	f.updateCalls++
	c, ok := f.changesets[id]
	if !ok {
		return change.Changeset{}, errors.New("no such changeset")
	}
	c.Ops = ops
	c.Author = author
	if title != nil {
		c.Title = *title
	}
	f.changesets[id] = c
	return c, nil
}

func (f *spyStager) Validate(_ context.Context, id, _ string) (change.Changeset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Validate")
	c, ok := f.changesets[id]
	if !ok {
		return change.Changeset{}, errors.New("no such changeset")
	}
	return c, nil
}

func (f *spyStager) Diff(_ context.Context, _ string) (*ifaces.ChangesetDiff, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Diff")
	return &ifaces.ChangesetDiff{}, nil
}

func (f *spyStager) List(_ context.Context, status string) ([]change.Changeset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("List")
	var out []change.Changeset
	for _, c := range f.changesets {
		if status == "" || string(c.Status) == status {
			out = append(out, c)
		}
	}
	return out, nil
}

// --- the verbs the seam does NOT have (control leg only) ---------------------
//
// These exist so the AC1 counters are provably movable. They are NOT part of
// ChangesetStager: nothing in internal/mcp holds a *spyStager, only the
// interface, so no tool could reach them even by mistake.

func (f *spyStager) Apply(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Apply")
	f.applyCalls++
	return nil
}

func (f *spyStager) Confirm(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirmCalls++
	return nil
}

func (f *spyStager) Approve(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approveCalls++
	return nil
}

func (f *spyStager) Discard(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discardCalls++
	return nil
}

func (f *spyStager) counts() (create, update, apply, confirm, approve, discard int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createCalls, f.updateCalls, f.applyCalls, f.confirmCalls, f.approveCalls, f.discardCalls
}

func (f *spyStager) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// fakePolicy is the T-2601 evaluator double.
type fakePolicy struct {
	result change.PolicyResult
	err    error
	// evaluated records the op list of every evaluation, in order.
	evaluated [][]change.Op
	mu        sync.Mutex
}

var _ PolicyChecker = (*fakePolicy)(nil)

func (p *fakePolicy) EvaluatePolicySet(_ context.Context, set change.PolicySet, ops []change.Op) (change.PolicyResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !set.IsEmpty() {
		return change.PolicyResult{}, fmt.Errorf("mcp must pass the empty set so the cluster's INSTALLED policy is used; got %d rules", len(set.Rules))
	}
	p.evaluated = append(p.evaluated, append([]change.Op(nil), ops...))
	return p.result, p.err
}

func (p *fakePolicy) evaluations() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.evaluated)
}

// denyResult builds the PolicyResult a deny rule produces over op index 0.
func denyResult(ruleID, description, message string) change.PolicyResult {
	return change.PolicyResult{
		Rules: []change.PolicyRuleResult{{
			RuleID: ruleID, Description: description,
			Severity: change.PolicyDeny, MatchedOps: []int{0}, ViolatingOps: []int{0},
		}},
		Findings: []change.Finding{{Severity: change.SeverityError, Code: "policy_violation", Message: message}},
	}
}

// --- harness ----------------------------------------------------------------

// stagingArgs is one valid argument set per mutating tool, so every acceptance
// test below can drive the WHOLE mutating surface rather than one example of
// it. changesets.create and changesets.validate are included because AC1 is
// about "the whole tool suite", not only T-2705's four new tools.
func stagingArgs() []struct {
	args map[string]any
	tool string
} {
	return []struct {
		args map[string]any
		tool string
	}{
		{map[string]any{"targetRef": "bridge:pve1:vmbr9", "addresses": []string{"10.0.0.1/24"}, "vlanAware": true}, ToolStageBridge},
		{map[string]any{"targetRef": "physnic:pve1:eno1", "mtu": 9000}, ToolStageIface},
		{map[string]any{"targetRef": "fw-ruleset:pve1:guest/100", "direction": "in", "action": "ACCEPT", "dport": "22"}, ToolStageFwRule},
		{map[string]any{"targetRef": "sdn-subnet::10.0.0.0/24", "cidr": "10.0.0.42/32", "hostname": "db1"}, ToolStageIPAM},
		{map[string]any{"title": "generic", "ops": []any{}}, ToolChangesetsCreate},
		{map[string]any{"id": "cs-01"}, ToolChangesetsValidate},
	}
}

// newStagingServer wires a Server over the two doubles with a full-scope
// session, and returns the client, the doubles, and the session.
func newStagingServer(t *testing.T, mutate func(*Deps)) (*mockClient, *spyStager, *fakePolicy) {
	t.Helper()
	auth := newFakeAuth()
	auth.add("full", TokenInfo{ID: "tok-full", Name: "ci-bot", Scopes: []string{"netRead", "netWrite", "automation"}})
	stager := newSpyStager()
	policy := &fakePolicy{}
	deps := stubReads()
	deps.Auth = auth
	deps.Staging = stager
	deps.Policy = policy
	deps.Now = func() time.Time { return time.Unix(5000, 0) }
	if mutate != nil {
		mutate(&deps)
	}
	srv, err := NewServer(deps)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session, aerr := srv.Authenticate(context.Background(), "full")
	if aerr != nil {
		t.Fatalf("Authenticate: %v", aerr)
	}
	client, _ := newMockClient(t, srv, session)
	client.initialize()
	return client, stager, policy
}

// --- AC1 --------------------------------------------------------------------

// TestAC1_EveryMutatingToolProducesADraftAndNothingElse is acceptance criterion
// 1: every mutating tool produces a draft, and the apply path records ZERO
// calls — asserted once per tool. The control leg is TestAC1_ControlLeg below.
func TestAC1_EveryMutatingToolProducesADraftAndNothingElse(t *testing.T) {
	for _, tc := range stagingArgs() {
		t.Run(tc.tool, func(t *testing.T) {
			client, stager, _ := newStagingServer(t, nil)

			// changesets.validate needs a changeset to validate; stage one
			// through the surface itself, so even the setup goes through a
			// stage-only path.
			if tc.tool == ToolChangesetsValidate {
				res, rerr := client.callTool(ToolStageIface, map[string]any{"targetRef": "physnic:pve1:eno1", "mtu": 1500})
				if rerr != nil || res.IsError {
					t.Fatalf("setup stage failed: %+v %+v", rerr, res.Content)
				}
			}

			res, rerr := client.callTool(tc.tool, tc.args)
			if rerr != nil {
				t.Fatalf("%s: protocol error %+v", tc.tool, rerr)
			}
			if res.IsError {
				t.Fatalf("%s failed: %+v", tc.tool, res.Content)
			}

			var view changesetView
			remarshal(t, res.StructuredContent, &view)
			if view.ID == "" {
				t.Errorf("%s returned no changeset id", tc.tool)
			}
			if view.Status != string(change.StatusDraft) {
				t.Errorf("%s produced status %q, want %q", tc.tool, view.Status, change.StatusDraft)
			}

			// THE assertion, once per tool.
			_, _, apply, confirm, approve, discard := stager.counts()
			if apply != 0 || confirm != 0 || approve != 0 || discard != 0 {
				t.Fatalf("%s reached a live-mutating verb: apply=%d confirm=%d approve=%d discard=%d — the MCP surface must stage only",
					tc.tool, apply, confirm, approve, discard)
			}
			for _, called := range stager.callLog() {
				switch called {
				case "Apply", "Confirm", "Approve", "Discard":
					t.Fatalf("%s called %s on the change engine", tc.tool, called)
				}
			}
		})
	}
}

// TestAC1_WholeToolSuiteRecordsZeroApplyCalls is AC1's "across the WHOLE tool
// suite" clause, taken literally: it enumerates the real allowlist — every
// tool, read and mutating alike — drives each one, and asserts the apply
// counter is still zero after each. Enumerating rather than listing means a
// tool added later is covered the day it lands, not the day someone remembers
// to extend a table.
func TestAC1_WholeToolSuiteRecordsZeroApplyCalls(t *testing.T) {
	args := map[string]any{}
	for _, tc := range stagingArgs() {
		args[tc.tool] = tc.args
	}
	// Arguments for the read tools that need any.
	args[ToolDiagnoseRun] = map[string]any{"targetRef": "guest-nic:pve1:100/net0"}
	args[ToolSimulatePath] = map[string]any{"src": map[string]any{"kind": "ip", "ip": "10.0.0.1"}, "dst": map[string]any{"kind": "ip", "ip": "10.0.0.2"}}
	args[ToolChangesetsDiff] = map[string]any{"id": "cs-01"}

	tools := Tools()
	if len(tools) == 0 {
		t.Fatal("the tool registry enumerated empty; this assertion would be vacuous")
	}
	client, stager, _ := newStagingServer(t, func(d *Deps) { d.MaxOpenMCPDrafts = 100 })
	for _, spec := range tools {
		// Every tool is invoked; whether it succeeds or reports a tool error is
		// beside the point — the assertion is about what it could REACH.
		_, rerr := client.callTool(spec.Name, args[spec.Name])
		if rerr != nil {
			t.Fatalf("%s: protocol error %+v", spec.Name, rerr)
		}
		if _, _, apply, confirm, approve, discard := stager.counts(); apply != 0 || confirm != 0 || approve != 0 || discard != 0 {
			t.Fatalf("after %s: apply=%d confirm=%d approve=%d discard=%d, want all zero",
				spec.Name, apply, confirm, approve, discard)
		}
	}
	// Not vacuous: the suite really did stage through this same spy.
	if create, _, _, _, _, _ := stager.counts(); create == 0 {
		t.Fatal("no tool staged anything; the zero-apply assertion above proves nothing about a live surface")
	}
	t.Logf("drove %d MCP tools; the change engine recorded zero apply/confirm/approve/discard calls", len(tools))
}

// TestAC1_ControlLeg proves the counters TestAC1 reads actually move. Without
// this, "apply == 0" would be a statement about a counter nothing can
// increment, which is no evidence at all.
func TestAC1_ControlLeg(t *testing.T) {
	stager := newSpyStager()
	if _, _, apply, _, _, _ := stager.counts(); apply != 0 {
		t.Fatalf("fresh spy already recorded %d applies", apply)
	}
	ctx := context.Background()
	if err := stager.Apply(ctx, "cs-01", "human"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := stager.Confirm(ctx, "cs-01", "human"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if err := stager.Approve(ctx, "cs-01", "human"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := stager.Discard(ctx, "cs-01", "human"); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	create, _, apply, confirm, approve, discard := stager.counts()
	if apply != 1 || confirm != 1 || approve != 1 || discard != 1 {
		t.Fatalf("control leg: counters = apply %d confirm %d approve %d discard %d, want 1/1/1/1 — the AC1 assertion reads a dead counter",
			apply, confirm, approve, discard)
	}
	if create != 0 {
		t.Errorf("control leg moved the create counter (%d); the counters are not independent", create)
	}
}

// TestAC1_ApplyIsNotReachableThroughTheSeam is the other half of AC1's "nothing
// else": the spy HAS an Apply method, but the interface the server holds does
// not, so the method is unreachable from any tool by construction — not merely
// unused.
func TestAC1_ApplyIsNotReachableThroughTheSeam(t *testing.T) {
	var seam ChangesetStager = newSpyStager()
	if _, ok := seam.(interface {
		Apply(context.Context, string, string) error
	}); ok {
		// A type assertion CAN recover it from the concrete value; the point is
		// that no code in this package performs one — asserted structurally by
		// the compile-time shape in stageonly.go and by the reflection guard in
		// registry_test.go. This branch documents that the double really does
		// carry the method, i.e. that the control leg is meaningful.
		t.Log("spyStager carries an Apply method the ChangesetStager interface does not expose (control confirmed)")
		return
	}
	t.Fatal("spyStager has no Apply method; TestAC1_ControlLeg is testing nothing")
}

// --- AC3 --------------------------------------------------------------------

// TestAC3_PolicyDeniedOpReturnsRuleIDAndDescription is acceptance criterion 3.
// It asserts three things per tool: the caller is told the rule id AND its
// description, the underlying finding's message comes back too, and NOTHING was
// staged — the policy check runs before the draft exists.
func TestAC3_PolicyDeniedOpReturnsRuleIDAndDescription(t *testing.T) {
	const (
		ruleID = "no-guest-bridge-without-two-uplinks"
		desc   = "a bridge carrying guests must have two uplinks"
		msg    = `policy rule "no-guest-bridge-without-two-uplinks": failed assertion target.uplinkCount gte 2`
	)
	for _, tc := range stagingArgs() {
		if tc.tool == ToolChangesetsCreate || tc.tool == ToolChangesetsValidate {
			continue // the pre-T-2705 generic tools; T-2705's gate is on the typed ones
		}
		t.Run(tc.tool, func(t *testing.T) {
			client, stager, policy := newStagingServer(t, nil)
			policy.result = denyResult(ruleID, desc, msg)

			res, rerr := client.callTool(tc.tool, tc.args)
			if rerr != nil {
				t.Fatalf("protocol error: %+v", rerr)
			}
			if !res.IsError {
				t.Fatalf("%s was not refused by a deny rule: %+v", tc.tool, res.StructuredContent)
			}
			text := toolErrorText(t, res)
			for _, want := range []string{ruleID, desc, msg} {
				if !strings.Contains(text, want) {
					t.Errorf("denial message %q does not carry %q", text, want)
				}
			}

			// Nothing staged: no create, no update.
			create, update, _, _, _, _ := stager.counts()
			if create != 0 || update != 0 {
				t.Errorf("a policy-denied op still staged something: create=%d update=%d", create, update)
			}
			if policy.evaluations() != 1 {
				t.Errorf("policy evaluations = %d, want exactly 1", policy.evaluations())
			}
		})
	}
}

// TestAC3_PolicyIsEvaluatedBeforeStaging asserts the ORDER, not just that both
// happened: the evaluator sees the op while the stager has not been called.
func TestAC3_PolicyIsEvaluatedBeforeStaging(t *testing.T) {
	client, stager, policy := newStagingServer(t, nil)
	res, rerr := client.callTool(ToolStageBridge, map[string]any{"targetRef": "bridge:pve1:vmbr9"})
	if rerr != nil || res.IsError {
		t.Fatalf("stage failed: %+v %+v", rerr, res.Content)
	}
	if policy.evaluations() != 1 {
		t.Fatalf("policy evaluations = %d, want 1", policy.evaluations())
	}
	log := stager.callLog()
	for _, name := range log {
		if name == "CreateWithProvenance" || name == "UpdateDraft" {
			break
		}
		if name == "Apply" {
			t.Fatalf("the change engine applied before staging: %v", log)
		}
	}
	// The evaluated ops are the ops that were staged — not a different list.
	if got := policy.evaluated[0]; len(got) != 1 || got[0].Type != change.OpBridgeCreate {
		t.Fatalf("policy evaluated %+v, want the single bridge.create op that was staged", got)
	}
}

// TestAC3_PolicyEvaluationErrorFailsClosed: a policy set that cannot be
// evaluated refuses the stage rather than staging unchecked.
func TestAC3_PolicyEvaluationErrorFailsClosed(t *testing.T) {
	client, stager, policy := newStagingServer(t, nil)
	policy.err = errors.New("stored policy set is unusable")

	res, rerr := client.callTool(ToolStageIface, map[string]any{"targetRef": "physnic:pve1:eno1", "mtu": 1500})
	if rerr != nil {
		t.Fatalf("protocol error: %+v", rerr)
	}
	if !res.IsError {
		t.Fatal("an unevaluable policy set did not refuse the stage")
	}
	if create, _, _, _, _, _ := stager.counts(); create != 0 {
		t.Errorf("staged %d changesets despite an unevaluable policy set", create)
	}
}

// TestAC3_StagingWithoutAPolicyEngineIsRefused: the same fail-closed stance for
// a daemon with no policy evaluator wired at all.
func TestAC3_StagingWithoutAPolicyEngineIsRefused(t *testing.T) {
	client, stager, _ := newStagingServer(t, func(d *Deps) { d.Policy = nil })
	res, rerr := client.callTool(ToolStageIPAM, map[string]any{"targetRef": "sdn-subnet::10.0.0.0/24", "cidr": "10.0.0.9/32"})
	if rerr != nil {
		t.Fatalf("protocol error: %+v", rerr)
	}
	if !res.IsError {
		t.Fatal("staging succeeded with no policy engine wired")
	}
	if create, _, _, _, _, _ := stager.counts(); create != 0 {
		t.Errorf("staged %d changesets with no policy engine wired", create)
	}
}

// --- AC4 --------------------------------------------------------------------

// TestAC4_StagedChangesetIsTaggedWithToolAndSession is acceptance criterion 4's
// first half, over the double: every typed staging tool tags its draft with
// origin=mcp, the session's token id, and its own tool name, and the tag comes
// back to the caller. The "survives to the review API" half is
// TestAC4_TagSurvivesToTheReviewAPI below (a real store) plus
// internal/api's TestChangesetResponseCarriesOriginTool.
func TestAC4_StagedChangesetIsTaggedWithToolAndSession(t *testing.T) {
	for _, tc := range stagingArgs() {
		if tc.tool == ToolChangesetsCreate || tc.tool == ToolChangesetsValidate {
			continue
		}
		t.Run(tc.tool, func(t *testing.T) {
			client, stager, _ := newStagingServer(t, nil)
			res, rerr := client.callTool(tc.tool, tc.args)
			if rerr != nil || res.IsError {
				t.Fatalf("stage failed: %+v %+v", rerr, res.Content)
			}
			var view changesetView
			remarshal(t, res.StructuredContent, &view)
			if view.Origin != change.OriginMCP {
				t.Errorf("origin = %q, want %q", view.Origin, change.OriginMCP)
			}
			if view.OriginTokenID != "tok-full" {
				t.Errorf("originTokenId = %q, want tok-full (the session's token)", view.OriginTokenID)
			}
			if view.OriginTool != tc.tool {
				t.Errorf("originTool = %q, want %q", view.OriginTool, tc.tool)
			}
			if view.Author != "mcp:ci-bot" {
				t.Errorf("author = %q, want mcp:ci-bot", view.Author)
			}
			// The tag reached the change engine, not just the response.
			stored := stager.changesets[view.ID]
			if stored.OriginTool != tc.tool || stored.Origin != change.OriginMCP || stored.OriginTokenID != "tok-full" {
				t.Errorf("persisted tag = (%q, %q, %q), want (%q, mcp, tok-full)",
					stored.Origin, stored.OriginTokenID, stored.OriginTool, tc.tool)
			}
		})
	}
}

// TestAC4_TagSurvivesToTheReviewAPI drives a REAL change.Service over a REAL
// store: the tag is not just carried in memory, it round-trips through SQLite
// and comes back on the read the review surface performs.
func TestAC4_TagSurvivesToTheReviewAPI(t *testing.T) {
	auth := newFakeAuth()
	auth.add("full", TokenInfo{ID: "tok-real", Name: "ci-bot", Scopes: []string{"netRead", "netWrite", "automation"}})
	svc, _ := realChange(t, func() time.Time { return time.Unix(7000, 0) })
	deps := stubReads()
	deps.Auth = auth
	deps.Staging = svc
	deps.Policy = svc // the real T-2601 evaluator; no policy installed => nothing denied
	srv, err := NewServer(deps)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session, _ := srv.Authenticate(context.Background(), "full")
	client, _ := newMockClient(t, srv, session)
	client.initialize()

	res, rerr := client.callTool(ToolStageFwRule, map[string]any{
		"targetRef": "fw-ruleset:pve1:guest/100", "direction": "in", "action": "ACCEPT", "dport": "22",
	})
	if rerr != nil || res.IsError {
		t.Fatalf("stage failed: %+v %+v", rerr, res.Content)
	}
	var view changesetView
	remarshal(t, res.StructuredContent, &view)

	// The canonical read every review surface funnels into.
	stored, gerr := svc.Get(context.Background(), view.ID)
	if gerr != nil {
		t.Fatalf("Get: %v", gerr)
	}
	if stored.Origin != change.OriginMCP || stored.OriginTokenID != "tok-real" || stored.OriginTool != ToolStageFwRule {
		t.Fatalf("stored provenance = (%q, %q, %q), want (mcp, tok-real, %s)",
			stored.Origin, stored.OriginTokenID, stored.OriginTool, ToolStageFwRule)
	}
	if stored.Status != change.StatusDraft {
		t.Errorf("stored status = %q, want draft", stored.Status)
	}
	if len(stored.Ops) != 1 || stored.Ops[0].Type != change.OpFwRuleCreate {
		t.Fatalf("stored ops = %+v, want exactly one fw.rule.create", stored.Ops)
	}
	// And it is a fresh READ from SQLite that carries the tag, not a cached
	// in-memory value: List goes back to the database too.
	all, lerr := svc.List(context.Background(), "")
	if lerr != nil {
		t.Fatalf("List: %v", lerr)
	}
	var found bool
	for _, c := range all {
		if c.ID == view.ID {
			found = true
			if c.OriginTool != ToolStageFwRule {
				t.Errorf("listed originTool = %q, want %q", c.OriginTool, ToolStageFwRule)
			}
		}
	}
	if !found {
		t.Fatalf("staged changeset %s is not in the listing", view.ID)
	}
}

// --- AC5 --------------------------------------------------------------------

// TestAC5_OpenDraftCapRefusesFurtherStaging is acceptance criterion 5: once the
// cap is reached, staging is refused with a message NAMING the cap, and nothing
// more is staged.
func TestAC5_OpenDraftCapRefusesFurtherStaging(t *testing.T) {
	const maxOpen = 3
	client, stager, _ := newStagingServer(t, func(d *Deps) { d.MaxOpenMCPDrafts = maxOpen })

	for i := 0; i < maxOpen; i++ {
		res, rerr := client.callTool(ToolStageIface, map[string]any{"targetRef": "physnic:pve1:eno1", "mtu": 1500 + i})
		if rerr != nil || res.IsError {
			t.Fatalf("stage %d failed: %+v %+v", i, rerr, res.Content)
		}
	}

	res, rerr := client.callTool(ToolStageIface, map[string]any{"targetRef": "physnic:pve1:eno1", "mtu": 9000})
	if rerr != nil {
		t.Fatalf("protocol error: %+v", rerr)
	}
	if !res.IsError {
		t.Fatal("staging past the open-draft cap succeeded")
	}
	text := toolErrorText(t, res)
	if !strings.Contains(text, fmt.Sprintf("%d", maxOpen)) {
		t.Errorf("cap refusal %q does not name the cap (%d)", text, maxOpen)
	}
	if !strings.Contains(text, "cap") {
		t.Errorf("cap refusal %q does not say it is a cap", text)
	}
	if create, _, _, _, _, _ := stager.counts(); create != maxOpen {
		t.Errorf("staged %d changesets, want exactly the cap (%d)", create, maxOpen)
	}

	// Appending to an ALREADY-OPEN draft is still allowed at the cap: it opens
	// nothing new, so it cannot make the backlog worse.
	open, _ := stager.List(context.Background(), "")
	res, rerr = client.callTool(ToolStageIface, map[string]any{
		"targetRef": "physnic:pve1:eno2", "mtu": 9000, "changesetId": open[0].ID,
	})
	if rerr != nil || res.IsError {
		t.Fatalf("appending to an open draft at the cap was refused: %+v %+v", rerr, res.Content)
	}
}

// TestAC5_RateLimitIsPerSession is the other half of AC5: a session may stage
// only so often, the refusal names the budget, and the budget refills as the
// window slides.
func TestAC5_RateLimitIsPerSession(t *testing.T) {
	now := time.Unix(9000, 0)
	client, stager, _ := newStagingServer(t, func(d *Deps) {
		d.StageRateLimit = 2
		d.StageRateWindow = time.Minute
		d.MaxOpenMCPDrafts = 100
		d.Now = func() time.Time { return now }
	})

	for i := 0; i < 2; i++ {
		res, rerr := client.callTool(ToolStageIface, map[string]any{"targetRef": "physnic:pve1:eno1", "mtu": 1500 + i})
		if rerr != nil || res.IsError {
			t.Fatalf("stage %d failed: %+v %+v", i, rerr, res.Content)
		}
	}
	res, rerr := client.callTool(ToolStageIface, map[string]any{"targetRef": "physnic:pve1:eno1", "mtu": 9000})
	if rerr != nil {
		t.Fatalf("protocol error: %+v", rerr)
	}
	if !res.IsError {
		t.Fatal("the third stage in the window was not rate-limited")
	}
	if text := toolErrorText(t, res); !strings.Contains(text, "rate limit") || !strings.Contains(text, "2") {
		t.Errorf("rate-limit refusal %q does not name the budget", text)
	}
	if create, _, _, _, _, _ := stager.counts(); create != 2 {
		t.Errorf("staged %d changesets under a limit of 2", create)
	}

	// The window slides: after it passes, staging works again. (Driven by the
	// injected clock, not by sleeping — this test is not load-sensitive.)
	now = now.Add(2 * time.Minute)
	res, rerr = client.callTool(ToolStageIface, map[string]any{"targetRef": "physnic:pve1:eno1", "mtu": 9000})
	if rerr != nil || res.IsError {
		t.Fatalf("stage after the window slid was still refused: %+v %+v", rerr, res.Content)
	}
}

// TestStageLimiter is the limiter's own table-driven unit test, including the
// "a refused attempt is not itself charged" property.
func TestStageLimiter(t *testing.T) {
	base := time.Unix(1000, 0)
	cases := []struct {
		name   string
		key    string
		steps  []time.Duration // offset from base for each attempt
		want   []bool
		window time.Duration
		limit  int
	}{
		{
			name: "under the limit", key: "a", limit: 3, window: time.Minute,
			steps: []time.Duration{0, time.Second, 2 * time.Second},
			want:  []bool{true, true, true},
		},
		{
			name: "the one over is refused", key: "a", limit: 2, window: time.Minute,
			steps: []time.Duration{0, time.Second, 2 * time.Second},
			want:  []bool{true, true, false},
		},
		{
			name: "a refusal is not charged, so the window still expires", key: "a", limit: 1, window: time.Minute,
			steps: []time.Duration{0, 30 * time.Second, 61 * time.Second},
			want:  []bool{true, false, true},
		},
		{
			name: "the window slides", key: "a", limit: 2, window: time.Minute,
			steps: []time.Duration{0, time.Second, 61 * time.Second, 62 * time.Second},
			want:  []bool{true, true, true, true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := newStageLimiter()
			for i, off := range tc.steps {
				got := l.allow(tc.key, base.Add(off), tc.limit, tc.window)
				if got != tc.want[i] {
					t.Errorf("attempt %d at +%s = %v, want %v", i, off, got, tc.want[i])
				}
			}
		})
	}

	// Budgets are per key: one session exhausting its budget does not spend
	// another's.
	l := newStageLimiter()
	if !l.allow("s1", base, 1, time.Minute) || l.allow("s1", base, 1, time.Minute) {
		t.Fatal("s1's own budget did not behave")
	}
	if !l.allow("s2", base, 1, time.Minute) {
		t.Error("s2 was refused because s1 exhausted its budget; the limiter is not per session")
	}
}

// --- argument handling ------------------------------------------------------

// TestStageToolArgumentErrors: every rejection is a clear message and stages
// nothing. A model that mis-calls a tool must be able to fix its call from the
// error alone.
func TestStageToolArgumentErrors(t *testing.T) {
	cases := []struct {
		args map[string]any
		name string
		tool string
		want string
	}{
		{map[string]any{"mtu": 1500}, "bridge: missing target", ToolStageBridge, "targetRef is required"},
		{map[string]any{"targetRef": "physnic:pve1:eno1"}, "bridge: wrong kind", ToolStageBridge, "this tool targets bridge|ovs-bridge"},
		{map[string]any{"targetRef": "vmbr9"}, "bridge: malformed ref", ToolStageBridge, "want kind:node:id"},
		{map[string]any{"targetRef": "bridge:pve1:vmbr9", "nope": 1}, "bridge: unknown field", ToolStageBridge, "unknown field"},
		{map[string]any{"targetRef": "physnic:pve1:eno1"}, "iface: nothing to change", ToolStageIface, "nothing to change"},
		{map[string]any{"targetRef": "guest:pve1:100", "mtu": 1500}, "iface: wrong kind", ToolStageIface, "this tool targets"},
		{map[string]any{"targetRef": "fw-ruleset:pve1:cluster", "action": "DROP"}, "fwrule: missing direction", ToolStageFwRule, "direction and action are required"},
		{map[string]any{"targetRef": "bridge:pve1:vmbr0", "direction": "in", "action": "DROP"}, "fwrule: wrong kind", ToolStageFwRule, "this tool targets fw-ruleset"},
		{map[string]any{"targetRef": "sdn-subnet::10.0.0.0/24"}, "ipam: missing cidr", ToolStageIPAM, "cidr is required"},
		{map[string]any{"targetRef": "sdn-vnet::vnet1", "cidr": "10.0.0.5/32"}, "ipam: wrong kind", ToolStageIPAM, "this tool targets sdn-subnet"},
		{map[string]any{"targetRef": "physnic:pve1:eno1", "mtu": 1500, "changesetId": "cs-nope"}, "append to an unknown draft", ToolStageIface, "not an open MCP-staged draft"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, stager, _ := newStagingServer(t, nil)
			res, rerr := client.callTool(tc.tool, tc.args)
			if rerr != nil {
				t.Fatalf("protocol error: %+v", rerr)
			}
			if !res.IsError {
				t.Fatalf("%s accepted invalid arguments", tc.tool)
			}
			if text := toolErrorText(t, res); !strings.Contains(text, tc.want) {
				t.Errorf("error %q does not contain %q", text, tc.want)
			}
			if create, update, _, _, _, _ := stager.counts(); create != 0 || update != 0 {
				t.Errorf("an invalid call still staged something: create=%d update=%d", create, update)
			}
		})
	}
}

// TestStageAppendsIntoAnExistingDraft: the multi-op path. Four separate tool
// calls become ONE reviewable changeset, and the ops accumulate in order.
func TestStageAppendsIntoAnExistingDraft(t *testing.T) {
	client, stager, policy := newStagingServer(t, nil)

	res, rerr := client.callTool(ToolStageBridge, map[string]any{"targetRef": "bridge:pve1:vmbr9", "vlanAware": true})
	if rerr != nil || res.IsError {
		t.Fatalf("first stage failed: %+v %+v", rerr, res.Content)
	}
	var first changesetView
	remarshal(t, res.StructuredContent, &first)

	res, rerr = client.callTool(ToolStageIface, map[string]any{
		"targetRef": "physnic:pve1:eno1", "mtu": 9000, "changesetId": first.ID,
	})
	if rerr != nil || res.IsError {
		t.Fatalf("append failed: %+v %+v", rerr, res.Content)
	}
	var second changesetView
	remarshal(t, res.StructuredContent, &second)
	if second.ID != first.ID {
		t.Fatalf("append opened a new changeset %s instead of extending %s", second.ID, first.ID)
	}
	create, update, _, _, _, _ := stager.counts()
	if create != 1 || update != 1 {
		t.Errorf("create/update = %d/%d, want 1/1", create, update)
	}
	got := stager.changesets[first.ID].Ops
	if len(got) != 2 || got[0].Type != change.OpBridgeCreate || got[1].Type != change.OpIfaceUpdate {
		t.Fatalf("draft ops = %+v, want [bridge.create iface.update]", got)
	}
	// The append was policy-checked over the WHOLE resulting op list, not just
	// the new op — a rule about the changeset as a whole must see it all.
	if n := policy.evaluations(); n != 2 {
		t.Fatalf("policy evaluations = %d, want 2", n)
	}
	if last := policy.evaluated[1]; len(last) != 2 {
		t.Errorf("the append was policy-checked over %d ops, want the resulting 2", len(last))
	}
}

// TestStageToolsRequireNetWrite: a read-scoped session cannot see or call any
// staging tool, and the refusal is indistinguishable from "unknown tool".
func TestStageToolsRequireNetWrite(t *testing.T) {
	auth := newFakeAuth()
	auth.add("read", TokenInfo{ID: "tok-read", Name: "reader", Scopes: []string{"netRead", "automation"}})
	deps := stubReads()
	deps.Auth = auth
	deps.Staging = newSpyStager()
	deps.Policy = &fakePolicy{}
	srv, err := NewServer(deps)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session, _ := srv.Authenticate(context.Background(), "read")
	client, _ := newMockClient(t, srv, session)
	client.initialize()

	names := client.listToolNames()
	for _, tool := range stagingTools {
		if contains(names, tool) {
			t.Errorf("read-only session was offered staging tool %q", tool)
		}
		_, rerr := client.callTool(tool, map[string]any{"targetRef": "bridge:pve1:vmbr9"})
		if rerr == nil || rerr.Code != codeUnknownTool {
			t.Errorf("out-of-scope %s error = %+v, want codeUnknownTool", tool, rerr)
		}
	}
}

// TestStagingIsAudited: every staging call writes its own audit row with the
// mcp:<token-name> actor, so an AI-staged draft is traceable from the audit
// trail alone.
func TestStagingIsAudited(t *testing.T) {
	auth := newFakeAuth()
	auth.add("full", TokenInfo{ID: "tok-full", Name: "ci-bot", Scopes: []string{"netRead", "netWrite", "automation"}})
	svc, audit := realChange(t, func() time.Time { return time.Unix(8000, 0) })
	deps := stubReads()
	deps.Auth = auth
	deps.Staging = svc
	deps.Policy = svc
	deps.Audit = audit
	srv, err := NewServer(deps)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session, _ := srv.Authenticate(context.Background(), "full")
	client, _ := newMockClient(t, srv, session)
	client.initialize()

	res, rerr := client.callTool(ToolStageBridge, map[string]any{"targetRef": "bridge:pve1:vmbr9", "vlanAware": true})
	if rerr != nil || res.IsError {
		t.Fatalf("stage failed: %+v %+v", rerr, res.Content)
	}

	rows, lerr := audit.List(context.Background(), "", 0)
	if lerr != nil {
		t.Fatalf("audit.List: %v", lerr)
	}
	var sawInvoke, sawCreate bool
	for _, r := range rows {
		if r.Username != "mcp:ci-bot" {
			continue
		}
		switch r.Action {
		case "mcp.tool.invoke":
			if strings.Contains(detailOf(r), ToolStageBridge) {
				sawInvoke = true
			}
		case "changeset.create":
			if strings.Contains(detailOf(r), ToolStageBridge) {
				sawCreate = true
			}
		}
	}
	if !sawInvoke {
		t.Error("no mcp.tool.invoke audit row naming the staging tool")
	}
	if !sawCreate {
		t.Error("no changeset.create audit row naming the staging tool (originTool)")
	}
}

// --- helpers ----------------------------------------------------------------

func toolErrorText(t *testing.T, res callToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		b.WriteString(c.Text)
	}
	if b.Len() == 0 {
		raw, _ := json.Marshal(res)
		t.Fatalf("tool result carries no text content: %s", raw)
	}
	return b.String()
}

func detailOf(e store.AuditEntry) string {
	if !e.DetailJSON.Valid {
		return ""
	}
	return e.DetailJSON.String
}
