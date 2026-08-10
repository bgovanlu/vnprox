package change

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// --- harness ---------------------------------------------------------------

// spyNodeAgent is a NodeAgent that counts every node-file read. It is what
// makes acceptance criterion 3 provable in the strong form the card asks
// for: the assertion is that the diff was NEVER COMPUTED, and a diff cannot
// be computed without reading at least one node's interfaces file.
type spyNodeAgent struct {
	content map[string]string
	reads   []string
	mu      sync.Mutex
}

func newSpyNodeAgent() *spyNodeAgent {
	return &spyNodeAgent{content: map[string]string{}}
}

func (a *spyNodeAgent) ReadInterfaces(_ context.Context, node string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reads = append(a.reads, node)
	if c, ok := a.content[node]; ok {
		return c, nil
	}
	return "auto lo\niface lo inet loopback\n", nil
}

func (a *spyNodeAgent) StageInterfaces(_ context.Context, _, _ string) error { return nil }
func (a *spyNodeAgent) ReloadInterfaces(_ context.Context, _ string) error   { return nil }
func (a *spyNodeAgent) DiscardStaged(_ context.Context, _ string) error      { return nil }

func (a *spyNodeAgent) readCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.reads)
}

type policyHarness struct {
	svc      *Service
	agent    *spyNodeAgent
	policies *store.PolicySetRepo
	audit    *store.AuditRepo
	now      *int64
}

func newPolicyHarness(t *testing.T) *policyHarness {
	t.Helper()
	db := openTestDB(t)
	agent := newSpyNodeAgent()
	policies := store.NewPolicySetRepo(db)
	audit := store.NewAuditRepo(db)
	now := int64(1_700_000_000)

	svc, err := NewService(Config{
		Changesets:    store.NewChangesetRepo(db),
		Audit:         audit,
		Policies:      policies,
		Nodes:         agent,
		Snapshots:     store.NewSnapshotRepo(db),
		Blobs:         store.NewBlobRepo(db),
		ProtectedPath: filepath.Join(t.TempDir(), "protected.json"),
		Now:           func() time.Time { return time.Unix(now, 0) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return &policyHarness{svc: svc, agent: agent, policies: policies, audit: audit, now: &now}
}

// denyVmbr9 is a rule set that refuses any op targeting vmbr9.
func denyVmbr9(t *testing.T) PolicySet {
	t.Helper()
	return mustPolicySet(t, PolicyRule{
		ID:          "no-vmbr9",
		Description: "vmbr9 is managed out of band",
		Severity:    PolicyDeny,
		Match:       []PolicyCondition{cond(policyFieldTargetID, PolicyOpEq, "vmbr9")},
	})
}

func vmbr9CreateOps() []Op {
	return []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr9"), &BridgeCreateParams{MTU: 1500})}
}

// --- acceptance criterion 3: policy runs before diff ----------------------

// TestDiff_DeniedChangesetNeverComputesTheDiff asserts the diff was never
// computed — the node agent recorded ZERO reads — rather than that a
// computed diff was discarded.
func TestDiff_DeniedChangesetNeverComputesTheDiff(t *testing.T) {
	h := newPolicyHarness(t)
	ctx := context.Background()

	cs, err := h.svc.Create(ctx, "alice@pam", "touch vmbr9", vmbr9CreateOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Control: with no policy installed, Diff really does read node files.
	if _, derr := h.svc.Diff(ctx, cs.ID); derr != nil {
		t.Fatalf("Diff with no policy installed: %v", derr)
	}
	if h.agent.readCount() == 0 {
		t.Fatalf("the control case read no node files, so a zero-read assertion below would prove nothing")
	}

	if _, err = h.svc.SetPolicySet(ctx, "admin@pam", denyVmbr9(t)); err != nil {
		t.Fatalf("SetPolicySet: %v", err)
	}
	before := h.agent.readCount()

	_, err = h.svc.Diff(ctx, cs.ID)
	var blocked *ErrValidationBlocked
	if !errors.As(err, &blocked) {
		t.Fatalf("Diff err = %v, want *ErrValidationBlocked", err)
	}
	if got := h.agent.readCount() - before; got != 0 {
		t.Errorf("the node agent was called %d time(s) computing a denied changeset's diff; want 0 (the diff must never be computed)", got)
	}
	if len(blocked.Findings) == 0 || blocked.Findings[0].Code != codePolicyViolation {
		t.Errorf("blocked findings = %+v, want a policy.violation", blocked.Findings)
	}
}

// TestApply_DeniedChangesetProducesNoPlan is the other half of AC3: a
// denied changeset never reaches BuildPlan, so no plan is ever persisted.
func TestApply_DeniedChangesetProducesNoPlan(t *testing.T) {
	h := newPolicyHarness(t)
	ctx := context.Background()

	if _, err := h.svc.SetPolicySet(ctx, "admin@pam", denyVmbr9(t)); err != nil {
		t.Fatalf("SetPolicySet: %v", err)
	}
	cs, err := h.svc.Create(ctx, "alice@pam", "touch vmbr9", vmbr9CreateOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before := h.agent.readCount()

	_, err = h.svc.Apply(ctx, cs.ID, "alice@pam", nil, 0)
	var blocked *ErrValidationBlocked
	if !errors.As(err, &blocked) {
		t.Fatalf("Apply err = %v, want *ErrValidationBlocked", err)
	}
	if got := h.agent.readCount() - before; got != 0 {
		t.Errorf("the node agent was called %d time(s) applying a denied changeset; want 0", got)
	}

	reloaded, err := h.svc.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(reloaded.Plan) != 0 {
		t.Errorf("a denied changeset persisted a plan: %s", reloaded.Plan)
	}
	if reloaded.Status != StatusDraft {
		t.Errorf("Status = %s, want draft (the apply never started)", reloaded.Status)
	}
}

// TestValidate_PolicyDenyBlocksAtValidate covers the route-level half of
// AC1: the changeset stays a draft and carries the blocking finding.
func TestValidate_PolicyDenyBlocksAtValidate(t *testing.T) {
	h := newPolicyHarness(t)
	ctx := context.Background()
	if _, err := h.svc.SetPolicySet(ctx, "admin@pam", denyVmbr9(t)); err != nil {
		t.Fatalf("SetPolicySet: %v", err)
	}

	cs, err := h.svc.Create(ctx, "alice@pam", "touch vmbr9", vmbr9CreateOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	validated, err := h.svc.Validate(ctx, cs.ID, "alice@pam")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validated.Status != StatusDraft {
		t.Errorf("Status = %s, want draft (a denied changeset is never promoted)", validated.Status)
	}
	var found bool
	for _, f := range validated.Findings {
		if f.Code == codePolicyViolation && f.Severity == SeverityError {
			found = true
		}
	}
	if !found {
		t.Errorf("findings = %+v, want a blocking policy.violation", validated.Findings)
	}

	// A conforming changeset is untouched by the same installed policy.
	ok, err := h.svc.Create(ctx, "alice@pam", "add vmbr5", sampleOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	okValidated, err := h.svc.Validate(ctx, ok.ID, "alice@pam")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if okValidated.Status != StatusValidated {
		t.Errorf("Status = %s, want validated for a conforming changeset", okValidated.Status)
	}
}

// --- acceptance criterion 7: the audit entry reconstructs the change ------

func TestSetPolicySet_AuditsTheRuleSetDiff(t *testing.T) {
	h := newPolicyHarness(t)
	ctx := context.Background()

	v1 := mustPolicySet(t,
		policyRule("keep", PolicyDeny, []PolicyCondition{cond(policyFieldTargetID, PolicyOpEq, "vmbr1")}, nil),
		policyRule("gone", PolicyWarn, []PolicyCondition{cond(policyFieldTargetID, PolicyOpEq, "vmbr2")}, nil),
	)
	if _, err := h.svc.SetPolicySet(ctx, "admin@pam", v1); err != nil {
		t.Fatalf("SetPolicySet v1: %v", err)
	}

	changed := policyRule("keep", PolicyWarn, []PolicyCondition{cond(policyFieldTargetID, PolicyOpEq, "vmbr1")}, nil)
	changed.Description = "now only a warning"
	v2 := mustPolicySet(t, changed,
		policyRule("fresh", PolicyDeny, []PolicyCondition{cond(policyFieldTargetID, PolicyOpEq, "vmbr3")}, nil),
	)
	status, err := h.svc.SetPolicySet(ctx, "admin@pam", v2)
	if err != nil {
		t.Fatalf("SetPolicySet v2: %v", err)
	}
	if status.Revision != 2 {
		t.Errorf("Revision = %d, want 2 (the store revision increments per change)", status.Revision)
	}

	entries, _, err := h.audit.ListPage(ctx, store.AuditFilter{Action: "policy.update"}, "", 10)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("policy.update entries = %d, want 2", len(entries))
	}

	// Newest first: the v1 -> v2 change.
	var detail struct {
		ClusterID    string     `json:"clusterId"`
		Diff         PolicyDiff `json:"diff"`
		FromRevision int64      `json:"fromRevision"`
		ToRevision   int64      `json:"toRevision"`
		RuleCount    int        `json:"ruleCount"`
	}
	if !entries[0].DetailJSON.Valid {
		t.Fatalf("policy.update entry carries no detail")
	}
	if err := json.Unmarshal([]byte(entries[0].DetailJSON.String), &detail); err != nil {
		t.Fatalf("decoding audit detail: %v", err)
	}
	if detail.FromRevision != 1 || detail.ToRevision != 2 {
		t.Errorf("revisions = %d -> %d, want 1 -> 2", detail.FromRevision, detail.ToRevision)
	}
	if detail.RuleCount != 2 {
		t.Errorf("ruleCount = %d, want 2", detail.RuleCount)
	}

	// AC7: the entry ALONE reconstructs what changed — full bodies, both
	// sides. Applying the diff to v1 must reproduce v2 exactly.
	if got := applyPolicyDiff(v1, detail.Diff); !DiffPolicySets(got, v2).IsEmpty() {
		t.Errorf("the audited diff does not reconstruct the new rule set:\n got %+v\nwant %+v", got, v2)
	}
	if len(detail.Diff.Removed) != 1 || detail.Diff.Removed[0].ID != "gone" {
		t.Errorf("Removed = %+v, want the full body of the removed rule", detail.Diff.Removed)
	}
	if len(detail.Diff.Changed) != 1 || detail.Diff.Changed[0].Before.Severity != PolicyDeny {
		t.Errorf("Changed = %+v, want both sides of the changed rule", detail.Diff.Changed)
	}
}

// applyPolicyDiff replays an audited PolicyDiff onto a base rule set — the
// literal "reconstruct what changed from the audit entry alone" operation
// AC7 asks for.
func applyPolicyDiff(base PolicySet, d PolicyDiff) PolicySet {
	byID := map[string]PolicyRule{}
	for _, r := range base.Rules {
		byID[r.ID] = r
	}
	for _, r := range d.Removed {
		delete(byID, r.ID)
	}
	for _, c := range d.Changed {
		byID[c.After.ID] = c.After
	}
	for _, r := range d.Added {
		byID[r.ID] = r
	}
	out := PolicySet{Version: base.Version}
	for _, r := range byID {
		out.Rules = append(out.Rules, r)
	}
	return out
}

func TestSetPolicySet_IdempotentReinstallWritesNoRevisionOrAudit(t *testing.T) {
	h := newPolicyHarness(t)
	ctx := context.Background()
	set := denyVmbr9(t)

	first, err := h.svc.SetPolicySet(ctx, "admin@pam", set)
	if err != nil {
		t.Fatalf("SetPolicySet: %v", err)
	}
	second, err := h.svc.SetPolicySet(ctx, "admin@pam", set)
	if err != nil {
		t.Fatalf("SetPolicySet (re-install): %v", err)
	}
	if first.Revision != second.Revision {
		t.Errorf("re-installing an identical policy bumped the revision %d -> %d", first.Revision, second.Revision)
	}
	entries, _, err := h.audit.ListPage(ctx, store.AuditFilter{Action: "policy.update"}, "", 10)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("policy.update entries = %d, want 1 (an unchanged re-install audits nothing)", len(entries))
	}
}

func TestSetPolicySet_RejectsMalformedSetWithoutStoringIt(t *testing.T) {
	h := newPolicyHarness(t)
	ctx := context.Background()

	bad := PolicySet{Version: 1, Rules: []PolicyRule{{ID: "no-desc", Severity: PolicyDeny,
		Match: []PolicyCondition{cond(policyFieldTargetID, PolicyOpEq, "vmbr9")}}}}
	_, err := h.svc.SetPolicySet(ctx, "admin@pam", bad)
	var loadErr *PolicyLoadError
	if !errors.As(err, &loadErr) {
		t.Fatalf("err = %v, want *PolicyLoadError", err)
	}
	if _, gerr := h.policies.Get(ctx, ""); !errors.Is(gerr, store.ErrNotFound) {
		t.Errorf("a rejected policy set was stored anyway (Get err = %v)", gerr)
	}
}

// --- the runtime "matches nothing" report ---------------------------------

// TestPolicyStatus_UnmatchedRuleIsReportedAsProbablyMisconfigured is the
// runtime half of "a policy that matches nothing is an error, not a silent
// pass": after enough evaluations over a long enough window with no match,
// the rule is reported.
func TestPolicyStatus_UnmatchedRuleIsReportedAsProbablyMisconfigured(t *testing.T) {
	h := newPolicyHarness(t)
	ctx := context.Background()

	set := mustPolicySet(t,
		policyRule("never-matches", PolicyDeny, []PolicyCondition{cond(policyFieldTargetID, PolicyOpEq, "vmbr-nope")}, nil),
		policyRule("matches-often", PolicyWarn, []PolicyCondition{cond(policyFieldOpType, PolicyOpEq, "bridge.create")}, nil),
	)
	if _, err := h.svc.SetPolicySet(ctx, "admin@pam", set); err != nil {
		t.Fatalf("SetPolicySet: %v", err)
	}

	for range minPolicyEvalsBeforeReport {
		if _, err := h.svc.Create(ctx, "alice@pam", "add vmbr5", sampleOps()); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	// Before the window elapses, nothing is reported: an idle fortnight is
	// not evidence, and a freshly installed rule deserves the benefit of
	// the doubt.
	status, err := h.svc.PolicyStatus(ctx)
	if err != nil {
		t.Fatalf("PolicyStatus: %v", err)
	}
	for _, r := range status.Rules {
		if r.ProbablyMisconfigured {
			t.Errorf("rule %q was reported as misconfigured before the window elapsed", r.RuleID)
		}
	}

	// Advance the clock past the window.
	*h.now += int64(DefaultPolicyUnmatchedAfter.Seconds()) + 1
	status, err = h.svc.PolicyStatus(ctx)
	if err != nil {
		t.Fatalf("PolicyStatus: %v", err)
	}
	byID := map[string]PolicyRuleStatus{}
	for _, r := range status.Rules {
		byID[r.RuleID] = r
	}
	if !byID["never-matches"].ProbablyMisconfigured {
		t.Errorf("a rule that has never matched anything was not reported: %+v", byID["never-matches"])
	}
	if byID["matches-often"].ProbablyMisconfigured {
		t.Errorf("a rule that matches regularly was reported as misconfigured: %+v", byID["matches-often"])
	}
	if byID["matches-often"].MatchCount == 0 {
		t.Errorf("MatchCount = 0 for a rule that matched every changeset")
	}
	if byID["matches-often"].LastMatchedAt == 0 {
		t.Errorf("LastMatchedAt = 0 for a rule that matched")
	}
}

// TestStoredPolicySet_UnparsableSetFailsClosed pins the fail-closed
// contract: a cluster that has declared a policy must never silently
// validate as though it had none.
func TestStoredPolicySet_UnparsableSetFailsClosed(t *testing.T) {
	h := newPolicyHarness(t)
	ctx := context.Background()

	if err := h.policies.Put(ctx, store.PolicySet{
		ClusterID: "", Revision: 7, RulesJSON: `{"version":1,"rules":[{"id":"x"}]}`,
		UpdatedBy: "someone", UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	cs, err := h.svc.Create(ctx, "alice@pam", "add vmbr5", sampleOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var found bool
	for _, f := range cs.Findings {
		if f.Code == codePolicyInvalid && f.Severity == SeverityError {
			found = true
		}
	}
	if !found {
		t.Errorf("findings = %+v, want a blocking policy.invalid", cs.Findings)
	}
	if _, derr := h.svc.Diff(ctx, cs.ID); derr == nil {
		t.Errorf("Diff succeeded against an unparsable policy set; want it refused")
	}
}

// --- the evaluate-without-staging surface ---------------------------------

// TestEvaluatePolicyForChangeset_StagesNothing is the CLI's contract: the
// changeset is read, evaluated, and left exactly as it was.
func TestEvaluatePolicyForChangeset_StagesNothing(t *testing.T) {
	h := newPolicyHarness(t)
	ctx := context.Background()

	cs, err := h.svc.Create(ctx, "alice@pam", "touch vmbr9", vmbr9CreateOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	res, err := h.svc.EvaluatePolicyForChangeset(ctx, denyVmbr9(t), cs.ID)
	if err != nil {
		t.Fatalf("EvaluatePolicyForChangeset: %v", err)
	}
	if !res.Denied() {
		t.Errorf("the candidate rule set did not deny the changeset: %+v", res)
	}

	after, err := h.svc.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Status != cs.Status || after.UpdatedAt != cs.UpdatedAt || len(after.Findings) != len(cs.Findings) {
		t.Errorf("evaluating a candidate policy mutated the changeset:\nbefore %+v\n after %+v", cs, after)
	}
	// The candidate set is not installed by testing it.
	if _, gerr := h.policies.Get(ctx, ""); !errors.Is(gerr, store.ErrNotFound) {
		t.Errorf("testing a candidate policy installed it (Get err = %v)", gerr)
	}
}

func TestPolicyStatus_NotConfigured(t *testing.T) {
	svc := newTestService(t, nil)
	_, err := svc.PolicyStatus(context.Background())
	var notConfigured *ErrPolicyNotConfigured
	if !errors.As(err, &notConfigured) {
		t.Fatalf("err = %v, want *ErrPolicyNotConfigured", err)
	}
	// ...but validation still works, with an empty rule set.
	if _, cerr := svc.Create(context.Background(), "alice@pam", "add vmbr5", sampleOps()); cerr != nil {
		t.Fatalf("Create with no policy store wired: %v", cerr)
	}
}
