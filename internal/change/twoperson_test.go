package change

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// twoperson_test.go covers T-2604's gate at the level where its decisions
// are made. The HTTP-layer proof that the SAME decision is reached for a
// request crafted directly against the API (AC2) lives in
// internal/api/changesets_twoperson_test.go; the storage-level proof that
// two tokens belonging to one person collapse to one approver (AC3) is
// asserted here AND at that layer.

// newTwoPersonService builds a Service with the two-person rule's stores
// wired and classes declared. It returns the service and the db, so a test
// can read the audit trail the gate wrote.
func newTwoPersonService(t *testing.T, classes []ProtectedClass) (*Service, *store.DB) {
	t.Helper()
	db := openTestDB(t)
	svc, err := NewService(Config{
		Changesets:       store.NewChangesetRepo(db),
		Audit:            store.NewAuditRepo(db),
		Approvals:        store.NewChangesetApprovalRepo(db),
		Signoffs:         store.NewChangesetSignoffRepo(db),
		BreakGlass:       store.NewChangesetBreakGlassRepo(db),
		ProtectedClasses: classes,
		Approval:         ApprovalConfig{AllowSelfApproval: true},
		Now:              func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, db
}

func fwOps() []Op {
	return []Op{{
		Type:   OpFwRuleCreate,
		Target: inventory.Ref{Kind: inventory.KindFwRuleset, Node: "", ID: "cluster"},
		Params: &FwRuleCreateParams{Action: "ACCEPT", Direction: "in"},
	}}
}

func TestNormalizeProtectedClasses(t *testing.T) {
	tests := []struct {
		name    string
		wantErr string
		in      []ProtectedClass
		want    []ProtectedClass
	}{
		{
			name: "op-type globs and the reserved mgmtPath class are accepted, sorted",
			in:   []ProtectedClass{{Class: "sdn.*", Approvals: 3}, {Class: ProtectedClassMgmtPath, Approvals: 2}, {Class: "fw.*", Approvals: 2}},
			want: []ProtectedClass{{Class: "fw.*", Approvals: 2}, {Class: ProtectedClassMgmtPath, Approvals: 2}, {Class: "sdn.*", Approvals: 3}},
		},
		{
			name: "a tag class is accepted without knowing which policy declares it",
			in:   []ProtectedClass{{Class: "tag:pci-scope", Approvals: 2}},
			want: []ProtectedClass{{Class: "tag:pci-scope", Approvals: 2}},
		},
		{
			name: "an omitted or below-two approval count normalizes up to two",
			in:   []ProtectedClass{{Class: "fw.*"}, {Class: "sdn.*", Approvals: 1}},
			want: []ProtectedClass{{Class: "fw.*", Approvals: 2}, {Class: "sdn.*", Approvals: 2}},
		},
		{
			name:    "a glob no op type can satisfy is a typo, not a rule",
			in:      []ProtectedClass{{Class: "firewall.*", Approvals: 2}},
			wantErr: "matches no op type",
		},
		{
			name:    "an empty class name is refused",
			in:      []ProtectedClass{{Class: "   ", Approvals: 2}},
			wantErr: "class is required",
		},
		{
			name:    "a tag prefix naming no tag is refused",
			in:      []ProtectedClass{{Class: "tag:", Approvals: 2}},
			wantErr: "names no policy tag",
		},
		{
			name:    "a duplicate class is refused rather than silently merged",
			in:      []ProtectedClass{{Class: "fw.*", Approvals: 2}, {Class: "fw.*", Approvals: 3}},
			wantErr: "declared more than once",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeProtectedClasses(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeProtectedClasses: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// A malformed class list must fail the daemon's construction, not be dropped:
// a deployment must never come up believing it has a gate it does not have.
func TestNewService_RefusesAMalformedProtectedClass(t *testing.T) {
	db := openTestDB(t)
	_, err := NewService(Config{
		Changesets:       store.NewChangesetRepo(db),
		Audit:            store.NewAuditRepo(db),
		ProtectedClasses: []ProtectedClass{{Class: "not.an.op.family", Approvals: 2}},
	})
	if err == nil {
		t.Fatal("NewService accepted a protected class no op type can match")
	}
}

func TestMatchedProtectedClasses(t *testing.T) {
	fwChangeset := Changeset{ID: "cs1", Ops: fwOps()}
	bridgeChangeset := Changeset{ID: "cs2", Ops: sampleOps()}
	taggedReport := PolicyResult{Rules: []PolicyRuleResult{{
		RuleID: "r1", Severity: PolicyWarn, Tags: []string{"pci-scope"}, MatchedOps: []int{0},
	}}}

	tests := []struct {
		name    string
		report  PolicyResult
		classes []ProtectedClass
		want    []MatchedClass
		cs      Changeset
	}{
		{
			name:    "an fw op falls in the fw.* class",
			classes: []ProtectedClass{{Class: "fw.*", Approvals: 2}},
			cs:      fwChangeset,
			want:    []MatchedClass{{Class: "fw.*", Approvals: 2, Ops: 1}},
		},
		{
			name:    "a bridge op does not",
			classes: []ProtectedClass{{Class: "fw.*", Approvals: 2}},
			cs:      bridgeChangeset,
			want:    nil,
		},
		{
			name:    "a policy tag declares a class independently of the op type",
			classes: []ProtectedClass{{Class: "tag:pci-scope", Approvals: 3}},
			cs:      bridgeChangeset,
			report:  taggedReport,
			want:    []MatchedClass{{Class: "tag:pci-scope", Approvals: 3, Ops: 1}},
		},
		{
			name:    "a tag no rule matched declares nothing",
			classes: []ProtectedClass{{Class: "tag:pci-scope", Approvals: 3}},
			cs:      bridgeChangeset,
			report:  PolicyResult{},
			want:    nil,
		},
		{
			name:    "several classes can match at once",
			classes: []ProtectedClass{{Class: "fw.*", Approvals: 2}, {Class: "tag:pci-scope", Approvals: 4}},
			cs:      fwChangeset,
			report:  taggedReport,
			want:    []MatchedClass{{Class: "fw.*", Approvals: 2, Ops: 1}, {Class: "tag:pci-scope", Approvals: 4, Ops: 1}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTwoPersonService(t, tt.classes)
			got, err := svc.matchedProtectedClasses(context.Background(), tt.cs, tt.report)
			if err != nil {
				t.Fatalf("matchedProtectedClasses: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// The binding class is the strictest matched one, and it is what the refusal
// names — so an operator told "2 approvers" for a changeset that actually
// needs 4 never happens.
func TestBindingClassPicksTheStrictestMatch(t *testing.T) {
	class, required := bindingClass([]MatchedClass{
		{Class: "fw.*", Approvals: 2}, {Class: "tag:pci-scope", Approvals: 4}, {Class: "sdn.*", Approvals: 3},
	})
	if class != "tag:pci-scope" || required != 4 {
		t.Fatalf("bindingClass = (%q, %d), want (\"tag:pci-scope\", 4)", class, required)
	}
	if class, required := bindingClass(nil); class != "" || required != 0 {
		t.Fatalf("bindingClass(nil) = (%q, %d), want (\"\", 0)", class, required)
	}
}

// AC1: N-1 approvals is refused, and the refusal names the class AND the
// count required. The control leg — the Nth approval opening the gate — is
// asserted in the same test, so "refused" is never just "this code path
// always refuses".
func TestEnforceTwoPerson_RefusesUntilNDistinctApproversHaveSigned(t *testing.T) {
	svc, _ := newTwoPersonService(t, []ProtectedClass{{Class: "fw.*", Approvals: 2}})
	ctx := context.Background()
	cs, err := svc.Create(ctx, "alice", "open a port", fwOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Zero approvals.
	err = svc.enforceTwoPerson(ctx, cs, "alice", PolicyResult{})
	var refusal *ErrTwoPersonRequired
	if !asTwoPersonRequired(err, &refusal) {
		t.Fatalf("with no approvals, err = %v, want *ErrTwoPersonRequired", err)
	}
	if refusal.Required != 2 || refusal.Have != 0 || refusal.Class != "fw.*" {
		t.Fatalf("refusal = %+v, want class fw.*, required 2, have 0", refusal)
	}
	if !strings.Contains(refusal.Error(), `"fw.*"`) || !strings.Contains(refusal.Error(), "2 distinct approvers") {
		t.Errorf("refusal message %q must name the class and the count required", refusal.Error())
	}

	// N-1 approvals: still refused, and the message now says who has signed.
	if _, err = svc.ReviewApprove(ctx, cs.ID, "bob"); err != nil {
		t.Fatalf("ReviewApprove(bob): %v", err)
	}
	err = svc.enforceTwoPerson(ctx, cs, "alice", PolicyResult{})
	if !asTwoPersonRequired(err, &refusal) {
		t.Fatalf("with 1 of 2 approvals, err = %v, want *ErrTwoPersonRequired", err)
	}
	if refusal.Have != 1 || len(refusal.Approvers) != 1 || refusal.Approvers[0] != "bob" {
		t.Fatalf("refusal = %+v, want have 1 (bob)", refusal)
	}

	// CONTROL LEG: the Nth distinct approver opens the gate. Without this,
	// every assertion above would also pass against a gate that is simply
	// always closed.
	if _, err = svc.ReviewApprove(ctx, cs.ID, "carol"); err != nil {
		t.Fatalf("ReviewApprove(carol): %v", err)
	}
	if err = svc.enforceTwoPerson(ctx, cs, "alice", PolicyResult{}); err != nil {
		t.Fatalf("with 2 of 2 approvals, err = %v, want nil", err)
	}
}

// AC3: two approvals by the SAME principal count as one. This is asserted at
// the storage level (one row, not two) and at the gate's level (still
// refused), with a control leg proving two DIFFERENT principals do open it —
// otherwise "still refused" would prove only that approvals never count.
func TestEnforceTwoPerson_SamePrincipalTwiceIsOneApprover(t *testing.T) {
	svc, _ := newTwoPersonService(t, []ProtectedClass{{Class: "fw.*", Approvals: 2}})
	ctx := context.Background()
	cs, err := svc.Create(ctx, "alice", "open a port", fwOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Two separate approval calls by bob — two sessions, or two API tokens
	// bob minted, which carry bob's username either way.
	for i := range 2 {
		if _, err = svc.ReviewApprove(ctx, cs.ID, "bob"); err != nil {
			t.Fatalf("ReviewApprove(bob) #%d: %v", i+1, err)
		}
	}
	approvers, err := svc.signoffPrincipals(ctx, cs.ID)
	if err != nil {
		t.Fatalf("signoffPrincipals: %v", err)
	}
	if len(approvers) != 1 || approvers[0] != "bob" {
		t.Fatalf("approvers = %v, want exactly [bob] — one person is one approver however many times they click", approvers)
	}
	var refusal *ErrTwoPersonRequired
	if err = svc.enforceTwoPerson(ctx, cs, "alice", PolicyResult{}); !asTwoPersonRequired(err, &refusal) {
		t.Fatalf("err = %v, want *ErrTwoPersonRequired — bob approving twice is not two people", err)
	} else if refusal.Have != 1 {
		t.Fatalf("have = %d, want 1", refusal.Have)
	}

	// CONTROL LEG: a genuinely different principal makes it two.
	if _, err = svc.ReviewApprove(ctx, cs.ID, "carol"); err != nil {
		t.Fatalf("ReviewApprove(carol): %v", err)
	}
	if err = svc.enforceTwoPerson(ctx, cs, "alice", PolicyResult{}); err != nil {
		t.Fatalf("two distinct principals: err = %v, want nil", err)
	}
}

// A rejection withdraws that principal's endorsement: a changeset must not
// apply on the strength of an approval its approver has since retracted.
func TestReviewReject_WithdrawsThatPrincipalsSignoff(t *testing.T) {
	svc, _ := newTwoPersonService(t, []ProtectedClass{{Class: "fw.*", Approvals: 2}})
	ctx := context.Background()
	cs, err := svc.Create(ctx, "alice", "open a port", fwOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, who := range []string{"bob", "carol"} {
		if _, err = svc.ReviewApprove(ctx, cs.ID, who); err != nil {
			t.Fatalf("ReviewApprove(%s): %v", who, err)
		}
	}
	if err = svc.enforceTwoPerson(ctx, cs, "alice", PolicyResult{}); err != nil {
		t.Fatalf("precondition: two approvals should satisfy the gate, got %v", err)
	}

	if _, err = svc.ReviewReject(ctx, cs.ID, "carol", "on reflection, no"); err != nil {
		t.Fatalf("ReviewReject: %v", err)
	}
	var refusal *ErrTwoPersonRequired
	if err = svc.enforceTwoPerson(ctx, cs, "alice", PolicyResult{}); !asTwoPersonRequired(err, &refusal) {
		t.Fatalf("err = %v, want *ErrTwoPersonRequired after carol withdrew", err)
	} else if refusal.Have != 1 || refusal.Approvers[0] != "bob" {
		t.Fatalf("refusal = %+v, want only bob still counted", refusal)
	}
}

// Editing the ops clears every sign-off: people endorsed a specific set of
// ops, exactly the rule T-2003 already applies to the single review decision.
func TestUpdateDraft_ClearsEverySignoff(t *testing.T) {
	svc, _ := newTwoPersonService(t, []ProtectedClass{{Class: "fw.*", Approvals: 2}})
	ctx := context.Background()
	cs, err := svc.Create(ctx, "alice", "open a port", fwOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, who := range []string{"bob", "carol"} {
		if _, err = svc.ReviewApprove(ctx, cs.ID, who); err != nil {
			t.Fatalf("ReviewApprove(%s): %v", who, err)
		}
	}
	edited, err := svc.UpdateDraft(ctx, cs.ID, "alice", nil, append(fwOps(), fwOps()...))
	if err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	approvers, err := svc.signoffPrincipals(ctx, cs.ID)
	if err != nil {
		t.Fatalf("signoffPrincipals: %v", err)
	}
	if len(approvers) != 0 {
		t.Fatalf("approvers after an edit = %v, want none", approvers)
	}
	var refusal *ErrTwoPersonRequired
	if err = svc.enforceTwoPerson(ctx, edited, "alice", PolicyResult{}); !asTwoPersonRequired(err, &refusal) {
		t.Fatalf("err = %v, want *ErrTwoPersonRequired after the ops changed", err)
	}
}

// AC6, at the unit level: with no protected classes declared, the gate does
// nothing at all — no store read, no management-path resolution, no policy
// evaluation. (The proof that the whole existing apply suite is unaffected is
// that suite itself, which runs with no classes configured.)
func TestEnforceTwoPerson_NoDeclaredClassesIsACompleteNoOp(t *testing.T) {
	svc, _ := newTwoPersonService(t, nil)
	ctx := context.Background()
	cs, err := svc.Create(ctx, "alice", "open a port", fwOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err = svc.enforceTwoPerson(ctx, cs, "alice", PolicyResult{}); err != nil {
		t.Fatalf("err = %v, want nil for a deployment that declares no protected classes", err)
	}
	state, err := svc.TwoPersonState(ctx, cs.ID)
	if err != nil {
		t.Fatalf("TwoPersonState: %v", err)
	}
	if len(state.Classes) != 0 || !state.Satisfied || state.Required != 0 {
		t.Fatalf("TwoPersonState = %+v, want the empty, satisfied state", state)
	}
}

// A changeset in NO protected class is unaffected even when classes ARE
// declared — the gate is about the class, not about the feature being on.
func TestEnforceTwoPerson_UnprotectedChangesetIsUnaffected(t *testing.T) {
	svc, _ := newTwoPersonService(t, []ProtectedClass{{Class: "fw.*", Approvals: 2}})
	ctx := context.Background()
	cs, err := svc.Create(ctx, "alice", "add a bridge", sampleOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err = svc.enforceTwoPerson(ctx, cs, "alice", PolicyResult{}); err != nil {
		t.Fatalf("err = %v, want nil for a changeset in no protected class", err)
	}
}

// AC4 (engine half): break-glass with no reason writes nothing; with a
// reason it is recorded, audited under its own action, and opens the gate.
func TestBreakGlass_RequiresAReasonAndThenOpensTheGate(t *testing.T) {
	svc, db := newTwoPersonService(t, []ProtectedClass{{Class: "fw.*", Approvals: 2}})
	ctx := context.Background()
	cs, err := svc.Create(ctx, "alice", "open a port", fwOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, reason := range []string{"", "   ", "\t\n"} {
		var wantErr *ErrBreakGlassReasonRequired
		if _, err = svc.InvokeBreakGlass(ctx, cs.ID, "alice", reason); !asBreakGlassReasonRequired(err, &wantErr) {
			t.Fatalf("reason %q: err = %v, want *ErrBreakGlassReasonRequired", reason, err)
		}
	}
	if _, ok := svc.breakGlassFor(ctx, cs.ID); ok {
		t.Fatal("a refused break-glass still recorded an override")
	}
	var refusal *ErrTwoPersonRequired
	if err = svc.enforceTwoPerson(ctx, cs, "alice", PolicyResult{}); !asTwoPersonRequired(err, &refusal) {
		t.Fatalf("err = %v, want the gate still shut after a refused break-glass", err)
	}

	rec, err := svc.InvokeBreakGlass(ctx, cs.ID, "alice", "corosync down, nobody else on call")
	if err != nil {
		t.Fatalf("InvokeBreakGlass: %v", err)
	}
	if rec.InvokedBy != "alice" || rec.Reason != "corosync down, nobody else on call" {
		t.Fatalf("record = %+v", rec)
	}
	if want := rec.InvokedAt + int64(BreakGlassAckFloor.Seconds()); rec.AckableAt != want {
		t.Fatalf("AckableAt = %d, want invokedAt + 24h (%d)", rec.AckableAt, want)
	}
	if err = svc.enforceTwoPerson(ctx, cs, "alice", PolicyResult{}); err != nil {
		t.Fatalf("err = %v, want nil under a valid break-glass", err)
	}

	// Audited under its OWN action, not as a result value on changeset.apply.
	entries, err := store.NewAuditRepo(db).List(ctx, cs.ID, 100)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Action == "change.breakglass" {
			found = true
			if e.Username != "alice" || e.ChangesetID.String != cs.ID {
				t.Errorf("audit entry = %+v, want alice on %s", e, cs.ID)
			}
			if !strings.Contains(e.DetailJSON.String, "nobody else on call") {
				t.Errorf("audit detail %q must carry the written reason", e.DetailJSON.String)
			}
		}
	}
	if !found {
		t.Error("no change.breakglass audit entry was written")
	}
}

// A break-glass taken for one set of ops must not authorize whatever the
// draft is edited into afterwards.
func TestBreakGlass_DoesNotSurviveAnEditOfTheOps(t *testing.T) {
	svc, _ := newTwoPersonService(t, []ProtectedClass{{Class: "fw.*", Approvals: 2}})
	ctx := context.Background()
	cs, err := svc.Create(ctx, "alice", "open a port", fwOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err = svc.InvokeBreakGlass(ctx, cs.ID, "alice", "incident 42"); err != nil {
		t.Fatalf("InvokeBreakGlass: %v", err)
	}
	if err = svc.enforceTwoPerson(ctx, cs, "alice", PolicyResult{}); err != nil {
		t.Fatalf("precondition: the override should open the gate for the ops it was taken for, got %v", err)
	}

	edited, err := svc.UpdateDraft(ctx, cs.ID, "alice", nil, append(fwOps(), fwOps()...))
	if err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	var refusal *ErrTwoPersonRequired
	if err = svc.enforceTwoPerson(ctx, edited, "alice", PolicyResult{}); !asTwoPersonRequired(err, &refusal) {
		t.Fatalf("err = %v, want *ErrTwoPersonRequired — a stale override must not authorize edited ops", err)
	}
	// The record itself survives: it is evidence, and the finding it raises
	// must not be deletable by editing the draft.
	if _, ok := svc.breakGlassFor(ctx, edited.ID); !ok {
		t.Fatal("the break-glass record was deleted by an edit; its finding would vanish with it")
	}
}

func TestOpsFingerprint_ChangesWithTheOpsAndIsStableWithout(t *testing.T) {
	a := fwOps()
	b := fwOps()
	if opsFingerprint(a) != opsFingerprint(b) {
		t.Fatal("the same ops must fingerprint identically")
	}
	if opsFingerprint(a) == opsFingerprint(append(b, sampleOps()...)) {
		t.Fatal("different ops must fingerprint differently")
	}
}

// BreakGlassEvents is the findings engine's input; a service with no
// break-glass store reports none rather than failing a cycle.
func TestBreakGlassEvents(t *testing.T) {
	svc, _ := newTwoPersonService(t, []ProtectedClass{{Class: "fw.*", Approvals: 2}})
	ctx := context.Background()
	if got := svc.BreakGlassEvents(ctx); len(got) != 0 {
		t.Fatalf("BreakGlassEvents on a clean store = %v, want none", got)
	}
	cs, err := svc.Create(ctx, "alice", "open a port", fwOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err = svc.InvokeBreakGlass(ctx, cs.ID, "alice", "incident 42"); err != nil {
		t.Fatalf("InvokeBreakGlass: %v", err)
	}
	got := svc.BreakGlassEvents(ctx)
	if len(got) != 1 || got[0].ChangesetID != cs.ID || got[0].Reason != "incident 42" {
		t.Fatalf("BreakGlassEvents = %+v", got)
	}

	unwired := newTestService(t, &fakeBroadcaster{})
	if got := unwired.BreakGlassEvents(ctx); got != nil {
		t.Fatalf("a Service with no break-glass store reported %+v, want nil", got)
	}
}

func TestTwoPersonState_ReportsWhatTheGateWouldDecide(t *testing.T) {
	svc, _ := newTwoPersonService(t, []ProtectedClass{{Class: "fw.*", Approvals: 2}})
	ctx := context.Background()
	cs, err := svc.Create(ctx, "alice", "open a port", fwOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	state, err := svc.TwoPersonState(ctx, cs.ID)
	if err != nil {
		t.Fatalf("TwoPersonState: %v", err)
	}
	if state.Required != 2 || state.Satisfied || len(state.Classes) != 1 || state.Classes[0].Class != "fw.*" {
		t.Fatalf("state = %+v, want required 2, unsatisfied, class fw.*", state)
	}
	if state.BreakGlass != nil {
		t.Fatalf("state.BreakGlass = %+v, want nil before any override", state.BreakGlass)
	}

	for _, who := range []string{"bob", "carol"} {
		if _, err = svc.ReviewApprove(ctx, cs.ID, who); err != nil {
			t.Fatalf("ReviewApprove(%s): %v", who, err)
		}
	}
	if _, err = svc.InvokeBreakGlass(ctx, cs.ID, "alice", "belt and braces"); err != nil {
		t.Fatalf("InvokeBreakGlass: %v", err)
	}
	state, err = svc.TwoPersonState(ctx, cs.ID)
	if err != nil {
		t.Fatalf("TwoPersonState: %v", err)
	}
	if !state.Satisfied || len(state.Approvers) != 2 || state.BreakGlass == nil {
		t.Fatalf("state = %+v, want satisfied, two approvers, and the override reported", state)
	}
}

// errors.As, spelled out as helpers so each call site reads as one
// assertion rather than three lines of plumbing.
func asTwoPersonRequired(err error, target **ErrTwoPersonRequired) bool {
	e, ok := err.(*ErrTwoPersonRequired)
	if ok {
		*target = e
	}
	return ok
}

func asBreakGlassReasonRequired(err error, target **ErrBreakGlassReasonRequired) bool {
	e, ok := err.(*ErrBreakGlassReasonRequired)
	if ok {
		*target = e
	}
	return ok
}

// The mgmtPath class is not a property of an op's TYPE but of the cluster's
// current topology, so it is computed from the same MgmtStatus the apply
// ceremony and the scheduler's own gate use. Here: an op on the resolved
// management bridge is in the class; the same op on an unrelated bridge is
// not (the control leg — otherwise "in the class" would only prove the class
// matches everything).
func TestMatchedProtectedClasses_MgmtPath(t *testing.T) {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", IP: "10.10.0.1"},
		&inventory.Bridge{
			Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0",
			Addresses: []string{"10.10.0.1/24"}, PortNames: []string{"eno1"},
		},
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr9"), Name: "vmbr9"},
		&inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1", LinkUp: true, LinkUpSet: true},
	})
	db := openTestDB(t)
	svc, err := NewService(Config{
		Changesets:       store.NewChangesetRepo(db),
		Audit:            store.NewAuditRepo(db),
		Signoffs:         store.NewChangesetSignoffRepo(db),
		BreakGlass:       store.NewChangesetBreakGlassRepo(db),
		Inventory:        g,
		ProtectedPath:    filepath.Join(t.TempDir(), "protected.json"),
		ProtectedClasses: []ProtectedClass{{Class: ProtectedClassMgmtPath, Approvals: 2}},
		Now:              func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	opOn := func(bridge string) Changeset {
		return Changeset{ID: "cs-" + bridge, Ops: []Op{{
			Type:   OpBridgeUpdate,
			Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: bridge},
			Params: &BridgeUpdateParams{},
		}}}
	}

	got, err := svc.matchedProtectedClasses(context.Background(), opOn("vmbr0"), PolicyResult{})
	if err != nil {
		t.Fatalf("matchedProtectedClasses(vmbr0): %v", err)
	}
	if len(got) != 1 || got[0].Class != ProtectedClassMgmtPath {
		t.Fatalf("editing the management bridge matched %+v, want the mgmtPath class", got)
	}

	// CONTROL LEG: an op on a bridge that carries no management path is in
	// no class at all.
	got, err = svc.matchedProtectedClasses(context.Background(), opOn("vmbr9"), PolicyResult{})
	if err != nil {
		t.Fatalf("matchedProtectedClasses(vmbr9): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("editing an unrelated bridge matched %+v, want no class", got)
	}
}

// The policy-tagged class is the card's extension point and the T-2601
// dependency: this exercises it through the REAL apply prologue
// (beginApply), against a policy set actually installed in the store, so
// what is proved is that the gate reads the tags the validate stage
// produced — not that a hand-built PolicyResult can be passed to a helper.
//
// It also pins the gate's POSITION: a refusal happens inside beginApply,
// before the changeset transitions to applying and before any plan exists.
func TestBeginApply_RefusesAChangesetATaggedPolicyRuleClassifies(t *testing.T) {
	db := openTestDB(t)
	svc, err := NewService(Config{
		Changesets:       store.NewChangesetRepo(db),
		Audit:            store.NewAuditRepo(db),
		Approvals:        store.NewChangesetApprovalRepo(db),
		Signoffs:         store.NewChangesetSignoffRepo(db),
		BreakGlass:       store.NewChangesetBreakGlassRepo(db),
		Policies:         store.NewPolicySetRepo(db),
		ProtectedClasses: []ProtectedClass{{Class: "tag:pci-scope", Approvals: 2}},
		Approval:         ApprovalConfig{AllowSelfApproval: true},
		Now:              func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()

	if _, err = svc.SetPolicySet(ctx, "admin", PolicySet{Rules: []PolicyRule{{
		ID:          "pci-bridges",
		Description: "bridges in the cardholder-data segment are in PCI scope",
		Severity:    PolicyWarn,
		Tags:        []string{"pci-scope"},
		Match:       []PolicyCondition{{Field: "op", Op: PolicyOpEq, Value: "bridge.create"}},
	}}}); err != nil {
		t.Fatalf("SetPolicySet: %v", err)
	}

	cs, err := svc.Create(ctx, "alice", "add vmbr5", sampleOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, _, _, err = svc.beginApply(ctx, cs.ID, "alice", ApplyStrategy{}, DefaultConfirmTimeout)
	var refusal *ErrTwoPersonRequired
	if !asTwoPersonRequired(err, &refusal) {
		t.Fatalf("beginApply err = %v, want *ErrTwoPersonRequired", err)
	}
	if refusal.Class != "tag:pci-scope" || refusal.Required != 2 {
		t.Fatalf("refusal = %+v, want the tag class requiring 2", refusal)
	}
	after, err := svc.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Status != StatusDraft {
		t.Errorf("status after a refused apply = %s, want draft (the gate precedes the transition)", after.Status)
	}
	if len(after.Plan) != 0 {
		t.Errorf("a plan was persisted for a refused apply: %s", after.Plan)
	}

	// CONTROL LEG: two distinct approvals and the same call gets through the
	// gate — the changeset transitions to applying, which is the next thing
	// beginApply does after this check.
	for _, who := range []string{"bob", "carol"} {
		if _, err = svc.ReviewApprove(ctx, cs.ID, who); err != nil {
			t.Fatalf("ReviewApprove(%s): %v", who, err)
		}
	}
	got, _, _, err := svc.beginApply(ctx, cs.ID, "alice", ApplyStrategy{}, DefaultConfirmTimeout)
	if err != nil {
		t.Fatalf("beginApply after two approvals: %v", err)
	}
	if got.Status != StatusApplying {
		t.Fatalf("status = %s, want applying", got.Status)
	}
}
