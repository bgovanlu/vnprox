package gitsync_test

// T-2702's acceptance tests. Every negative assertion here carries a control
// leg proving the thing it asserts the absence of is observable at all: a
// "no orphan branch" test that could not see a branch, or a "no second pull
// request" test against a mock that can only hold one, would pass for the
// wrong reason.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/gitsync"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

const (
	proposePath   = "network/cluster.yaml"
	proposeRef    = "main"
	pushToken     = "glpat-VNPROXPUSHMARKER-do-not-log-me" //nolint:gosec // a test marker, not a real credential
	proposeChgset = "01JCHANGESET0000000000000TEST"
)

// --- doubles ----------------------------------------------------------------

//nolint:govet // fieldalignment: test double; mutex first, then what it guards.
type fakeChangesetReader struct {
	mu  sync.Mutex
	all map[string]change.Changeset
}

var _ gitsync.ChangesetReader = (*fakeChangesetReader)(nil)

func newChangesetReader(cs ...change.Changeset) *fakeChangesetReader {
	r := &fakeChangesetReader{all: map[string]change.Changeset{}}
	for _, c := range cs {
		r.all[c.ID] = c
	}
	return r
}

func (r *fakeChangesetReader) Get(_ context.Context, id string) (change.Changeset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.all[id]
	if !ok {
		return change.Changeset{}, store.ErrNotFound
	}
	return c, nil
}

//nolint:govet // fieldalignment: test double; mutex first, then what it guards.
type fakeProposals struct {
	mu   sync.Mutex
	rows map[string]store.ChangesetProposal
}

var _ gitsync.ProposalStore = (*fakeProposals)(nil)

func newFakeProposals() *fakeProposals {
	return &fakeProposals{rows: map[string]store.ChangesetProposal{}}
}

func (f *fakeProposals) Get(_ context.Context, id string) (store.ChangesetProposal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok {
		return store.ChangesetProposal{}, store.ErrNotFound
	}
	return row, nil
}

func (f *fakeProposals) Upsert(_ context.Context, p store.ChangesetProposal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[p.ChangesetID] = p
	return nil
}

func (f *fakeProposals) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

// fakeHost is a Host that is NOT an HTTPHost. It exists for the second half
// of AC6: the Proposer drives the interface and nothing else, so a completely
// different implementation must work identically with no change above the
// seam.
//
//nolint:govet // fieldalignment: test double.
type fakeHost struct {
	mu       sync.Mutex
	branches map[string]string
	files    map[string]map[string][]byte
	prs      []mockPR
	calls    []string
}

var _ gitsync.Host = (*fakeHost)(nil)

func newFakeHost(base string, files map[string][]byte) *fakeHost {
	return &fakeHost{
		branches: map[string]string{base: "basesha"},
		files:    map[string]map[string][]byte{base: files},
	}
}

func (f *fakeHost) note(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeHost) Describe() string { return "fake://org/infra (fake)" }

func (f *fakeHost) ResolveRef(_ context.Context, ref string) (string, error) {
	f.note("ResolveRef")
	f.mu.Lock()
	defer f.mu.Unlock()
	sha, ok := f.branches[ref]
	if !ok {
		return "", errors.New("no such ref")
	}
	return sha, nil
}

func (f *fakeHost) BranchHead(_ context.Context, branch string) (string, bool, error) {
	f.note("BranchHead")
	f.mu.Lock()
	defer f.mu.Unlock()
	sha, ok := f.branches[branch]
	return sha, ok, nil
}

func (f *fakeHost) CreateBranch(_ context.Context, branch, fromSHA string) error {
	f.note("CreateBranch")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.branches[branch] = fromSHA
	files := map[string][]byte{}
	for p, c := range f.files["main"] {
		files[p] = append([]byte(nil), c...)
	}
	f.files[branch] = files
	return nil
}

func (f *fakeHost) DeleteBranch(_ context.Context, branch string) error {
	f.note("DeleteBranch")
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.branches, branch)
	delete(f.files, branch)
	return nil
}

func (f *fakeHost) ReadFile(_ context.Context, ref, path string) ([]byte, bool, error) {
	f.note("ReadFile")
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.files[ref][path]
	return c, ok, nil
}

func (f *fakeHost) CommitFile(_ context.Context, req gitsync.CommitRequest) (string, error) {
	f.note("CommitFile")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.files[req.Branch] == nil {
		f.files[req.Branch] = map[string][]byte{}
	}
	f.files[req.Branch][req.Path] = req.Content
	return "fakecommit", nil
}

func (f *fakeHost) FindOpenPullRequest(_ context.Context, branch string) (gitsync.PullRequest, bool, error) {
	f.note("FindOpenPullRequest")
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, pr := range f.prs {
		if pr.Branch == branch {
			return gitsync.PullRequest{ID: "1", URL: "fake://pr/1", Title: pr.Title}, true, nil
		}
	}
	return gitsync.PullRequest{}, false, nil
}

func (f *fakeHost) OpenPullRequest(_ context.Context, in gitsync.PullRequestInput) (gitsync.PullRequest, error) {
	f.note("OpenPullRequest")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prs = append(f.prs, mockPR{ID: 1, Branch: in.Branch, Base: in.Base, Title: in.Title, Body: in.Body})
	return gitsync.PullRequest{ID: "1", URL: "fake://pr/1", Title: in.Title}, nil
}

func (f *fakeHost) UpdatePullRequest(_ context.Context, id string, in gitsync.PullRequestInput) (gitsync.PullRequest, error) {
	f.note("UpdatePullRequest")
	return gitsync.PullRequest{ID: id, URL: "fake://pr/" + id, Title: in.Title}, nil
}

// --- harness ----------------------------------------------------------------

// proposeFixture is one wired Proposer plus everything a test asserts against.
//
//nolint:govet // fieldalignment: test harness; fields are grouped by role, not packed.
type proposeFixture struct {
	provider  string
	serverURL string
	client    *http.Client
	graph     *inventory.Graph
	baseDoc   []byte
	ops       []change.Op
	changeset change.Changeset
	proposer  *gitsync.Proposer
	host      *gitHostServer
	proposals *fakeProposals
	audit     *fakeAudit
	logs      func() string
}

// newProposeFixture builds the standard situation every acceptance test
// starts from: a repository whose spec matches live exactly (so the base
// plans to zero ops), and a changeset that would set the first bridge's MTU.
func newProposeFixture(t *testing.T, provider string) *proposeFixture {
	t.Helper()
	g := buildFixtureGraph(t, fixtureThreeNode)
	baseDoc := specMatchingLive(t, g)
	ops := planFor(t, divergentSpec(t, g, 1400), g)
	if len(ops) == 0 {
		t.Fatal("the divergent fixture plans to zero ops; every assertion below would be vacuous")
	}

	host := newGitHostServer(provider, proposeRef, map[string][]byte{proposePath: baseDoc})
	ts := httptest.NewServer(host)
	t.Cleanup(ts.Close)

	src, err := gitsync.NewHTTPSource(gitsync.SourceConfig{
		URL: ts.URL + "/org/infra", Provider: gitsync.Provider(provider),
		Token: "read-only-token", Client: ts.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPSource: %v", err)
	}
	h, err := gitsync.NewHTTPHost(gitsync.HostConfig{
		URL: ts.URL + "/org/infra", Provider: gitsync.Provider(provider),
		Token: pushToken, Client: ts.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPHost: %v", err)
	}

	cs := change.Changeset{
		ID: proposeChgset, Title: "Raise vmbr0 MTU", Author: "brian",
		Status: change.StatusDraft, Origin: change.OriginUI, Ops: ops,
	}
	proposals := newFakeProposals()
	audit := &fakeAudit{}
	logger, logs := captureLogger()

	return &proposeFixture{
		provider: provider, serverURL: ts.URL, client: ts.Client(),
		graph: g, baseDoc: baseDoc, ops: ops, changeset: cs, host: host,
		proposals: proposals, audit: audit, logs: logs,
		proposer: gitsync.NewProposer(gitsync.ProposerConfig{
			Enabled: true, Source: src, Host: h, Ref: proposeRef, Path: proposePath,
			Changesets: newChangesetReader(cs), Inventory: g, Proposals: proposals,
			Audit: audit, Logger: logger,
			Now: func() time.Time { return time.Unix(1_754_000_000, 0) },
		}),
	}
}

func (f *proposeFixture) branch() string { return gitsync.DefaultBranchPrefix + proposeChgset }

// reSource / reHost build a second source and host against this fixture's own
// mock server, for the sub-cases that need a differently-wired Proposer
// against the same repository.
func (f *proposeFixture) reSource(t *testing.T) gitsync.Source {
	t.Helper()
	src, err := gitsync.NewHTTPSource(gitsync.SourceConfig{
		URL: f.serverURL + "/org/infra", Provider: gitsync.Provider(f.provider),
		Token: "read-only-token", Client: f.client,
	})
	if err != nil {
		t.Fatalf("NewHTTPSource: %v", err)
	}
	return src
}

func (f *proposeFixture) reHost(t *testing.T) gitsync.Host {
	t.Helper()
	h, err := gitsync.NewHTTPHost(gitsync.HostConfig{
		URL: f.serverURL + "/org/infra", Provider: gitsync.Provider(f.provider),
		Token: pushToken, Client: f.client,
	})
	if err != nil {
		t.Fatalf("NewHTTPHost: %v", err)
	}
	return h
}

// sameOpSet compares two op lists semantically: type, target and params,
// ignoring the change engine's own op ids and the order they arrive in.
func sameOpSet(a, b []change.Op) bool {
	key := func(ops []change.Op) []string {
		out := make([]string, 0, len(ops))
		for _, op := range ops {
			params, _ := json.Marshal(op.Params)
			out = append(out, string(op.Type)+"|"+op.Target.String()+"|"+string(params))
		}
		sortStrings(out)
		return out
	}
	return reflect.DeepEqual(key(a), key(b))
}

func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j] < ss[j-1]; j-- {
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
}

// --- AC1 ---------------------------------------------------------------------

// TestAC1_ProposedBranchReImportsToTheSameOps is acceptance criterion 1: the
// branch's spec, re-imported against live state, plans to the SAME op set as
// the changeset — asserted semantically (parse the document the host now
// holds, run it back through spec.Import, compare ops) rather than by
// comparing YAML text.
func TestAC1_ProposedBranchReImportsToTheSameOps(t *testing.T) {
	f := newProposeFixture(t, "github")

	res, err := f.proposer.Propose(context.Background(), proposeChgset, "brian")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if !res.Created || res.PullRequestURL == "" {
		t.Fatalf("Propose did not open a request: %+v", res)
	}

	committed, ok := f.host.fileOn(f.branch(), proposePath)
	if !ok {
		t.Fatalf("the branch %s carries no %s", f.branch(), proposePath)
	}

	// The assertion.
	got := planFor(t, committed, f.graph)
	if !sameOpSet(got, f.ops) {
		t.Errorf("re-importing the branch's spec plans to a different op set.\n got: %v\nwant: %v", got, f.ops)
	}

	// --- control 1: the comparison has teeth -------------------------------
	// A document that is NOT the proposal must fail the same assertion,
	// otherwise "they match" would be true of anything.
	other := divergentSpec(t, f.graph, 1401)
	if sameOpSet(planFor(t, other, f.graph), f.ops) {
		t.Error("control failed: a document with a different MTU compared equal, so the AC1 assertion proves nothing")
	}

	// --- control 2: the branch is not simply the base ----------------------
	if string(committed) == string(f.baseDoc) {
		t.Error("control failed: the branch carries the unmodified base document, so the round-trip above was vacuous")
	}
}

// TestAC1_RoundTripIsEnforcedBeforeAnythingIsWritten proves the round-trip is
// a production guard, not just a test: a changeset the spec cannot express
// (a delete) is refused, and the host is left completely untouched.
func TestAC1_RoundTripRefusalWritesNothing(t *testing.T) {
	f := newProposeFixture(t, "github")

	deleting := change.Changeset{
		ID: "cs-delete", Title: "Remove vmbr9", Status: change.StatusDraft, Origin: change.OriginUI,
		Ops: []change.Op{{
			Type:   change.OpBridgeDelete,
			Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"},
			Params: &change.BridgeDeleteParams{},
		}},
	}
	p := gitsync.NewProposer(gitsync.ProposerConfig{
		Enabled: true, Source: mustSource(t, f), Host: mustHost(t, f), Ref: proposeRef, Path: proposePath,
		Changesets: newChangesetReader(deleting), Inventory: f.graph, Proposals: newFakeProposals(),
	})

	_, err := p.Propose(context.Background(), "cs-delete", "brian")
	if !errors.Is(err, gitsync.ErrNotExpressible) {
		t.Fatalf("proposing a delete op = %v, want ErrNotExpressible", err)
	}
	if !strings.Contains(err.Error(), "bridge.delete") {
		t.Errorf("the refusal does not name the offending op: %v", err)
	}
	if branches := f.host.branchNames(); len(branches) != 1 {
		t.Errorf("the refused proposal touched the host: branches = %v, want only the base branch", branches)
	}
	if prs := f.host.openPRs(); len(prs) != 0 {
		t.Errorf("the refused proposal opened %d request(s)", len(prs))
	}
}

// TestAC1_AChangesetThatWouldNotRoundTripIsRefused is the case the round-trip
// guard exists for, and the one the happy path cannot cover: every op here IS
// expressible, so nothing earlier refuses it — but the document the repository
// already holds says something different about the same field, so the branch
// would plan to something other than this changeset. It must be refused, with
// the difference named, before anything is written.
func TestAC1_AChangesetThatWouldNotRoundTripIsRefused(t *testing.T) {
	g := buildFixtureGraph(t, fixtureThreeNode)
	// The repository is AHEAD of live: it already declares MTU 9000 on the
	// bridge, which live does not have.
	baseDoc := divergentSpec(t, g, 9000)
	// ...and the operator staged a change setting that same field to 1400.
	ops := planFor(t, divergentSpec(t, g, 1400), g)
	if len(ops) == 0 {
		t.Fatal("the fixture plans to zero ops; the assertion would be vacuous")
	}

	host := newGitHostServer("github", proposeRef, map[string][]byte{proposePath: baseDoc})
	ts := httptest.NewServer(host)
	defer ts.Close()

	src, err := gitsync.NewHTTPSource(gitsync.SourceConfig{
		URL: ts.URL + "/org/infra", Provider: gitsync.ProviderGitHub, Token: "read", Client: ts.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPSource: %v", err)
	}
	h, err := gitsync.NewHTTPHost(gitsync.HostConfig{
		URL: ts.URL + "/org/infra", Provider: gitsync.ProviderGitHub, Token: pushToken, Client: ts.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPHost: %v", err)
	}
	cs := change.Changeset{ID: "cs-conflict", Title: "MTU 1400", Status: change.StatusDraft, Ops: ops}
	p := gitsync.NewProposer(gitsync.ProposerConfig{
		Enabled: true, Source: src, Host: h, Ref: proposeRef, Path: proposePath,
		Changesets: newChangesetReader(cs), Inventory: g, Proposals: newFakeProposals(),
	})

	_, err = p.Propose(context.Background(), "cs-conflict", "brian")
	if !errors.Is(err, gitsync.ErrRoundTrip) {
		t.Fatalf("Propose = %v, want ErrRoundTrip", err)
	}
	if !strings.Contains(err.Error(), ops[0].Target.String()) {
		t.Errorf("the refusal does not name the entity in conflict: %v", err)
	}
	if branches := host.branchNames(); len(branches) != 1 {
		t.Errorf("the refused proposal touched the host: branches = %v", branches)
	}
	if prs := host.openPRs(); len(prs) != 0 {
		t.Errorf("the refused proposal opened %d request(s)", len(prs))
	}

	// --- control: the SAME wiring proposes fine when the document does not
	// contradict the changeset — so the refusal above is about the conflict,
	// not about this fixture being unproposable.
	host2 := newGitHostServer("github", proposeRef, map[string][]byte{proposePath: specMatchingLive(t, g)})
	ts2 := httptest.NewServer(host2)
	defer ts2.Close()
	src2, err := gitsync.NewHTTPSource(gitsync.SourceConfig{
		URL: ts2.URL + "/org/infra", Provider: gitsync.ProviderGitHub, Token: "read", Client: ts2.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPSource: %v", err)
	}
	h2, err := gitsync.NewHTTPHost(gitsync.HostConfig{
		URL: ts2.URL + "/org/infra", Provider: gitsync.ProviderGitHub, Token: pushToken, Client: ts2.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPHost: %v", err)
	}
	p2 := gitsync.NewProposer(gitsync.ProposerConfig{
		Enabled: true, Source: src2, Host: h2, Ref: proposeRef, Path: proposePath,
		Changesets: newChangesetReader(cs), Inventory: g, Proposals: newFakeProposals(),
	})
	if _, ctrlErr := p2.Propose(context.Background(), "cs-conflict", "brian"); ctrlErr != nil {
		t.Fatalf("control failed: the same changeset against a non-conflicting document was refused too: %v", ctrlErr)
	}
}

// --- AC2 ---------------------------------------------------------------------

// TestAC2_BodyCarriesBlastRadiusAndDiff, and an empty changeset cannot be
// proposed at all.
func TestAC2_BodyCarriesBlastRadiusAndDiff(t *testing.T) {
	f := newProposeFixture(t, "github")
	if _, err := f.proposer.Propose(context.Background(), proposeChgset, "brian"); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	prs := f.host.openPRs()
	if len(prs) != 1 {
		t.Fatalf("open requests = %d, want 1", len(prs))
	}
	body := prs[0].Body

	for _, want := range []string{
		"Blast radius",   // T-2404's preview
		"Disruption:",    // ...with its verdict
		"Spec diff",      // the semantic diff
		"```diff",        // ...rendered as one
		"bridge.update",  // the op
		proposeChgset,    // attribution back to the changeset
		"does not merge", // the standing statement about what vnprox will not do
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the pull request body does not carry %q:\n%s", want, body)
		}
	}
	// The diff must actually contain a change, not an empty hunk.
	if !strings.Contains(body, "+") || !strings.Contains(body, "mtu") {
		t.Errorf("the rendered spec diff carries no change:\n%s", body)
	}
}

func TestAC2_AnEmptyChangesetCannotBeProposed(t *testing.T) {
	f := newProposeFixture(t, "github")

	//nolint:govet // fieldalignment: test table; field order documents each case.
	cases := []struct {
		name string
		cs   change.Changeset
		want error
	}{
		{
			name: "no ops at all",
			cs:   change.Changeset{ID: "cs-empty", Title: "nothing", Status: change.StatusDraft},
			want: gitsync.ErrNothingToPropose,
		},
		{
			// An op the document renders to no change at all: the spec's v1
			// schema cannot express a flag as false (omitempty means
			// "unmanaged"), so turning STP off is a real op that produces an
			// empty spec diff — exactly AC2's case.
			name: "ops that render to no change in the document",
			cs: change.Changeset{
				ID: "cs-noop", Title: "already true", Status: change.StatusDraft,
				Ops: []change.Op{{
					Type: change.OpBridgeUpdate, Target: f.ops[0].Target,
					Params: &change.BridgeUpdateParams{STP: boolPtr(false)},
				}},
			},
			want: gitsync.ErrNothingToPropose,
		},
		{
			name: "a discarded changeset's ops are not intent",
			cs: change.Changeset{
				ID: "cs-discarded", Title: "abandoned", Status: change.StatusDiscarded, Ops: f.ops,
			},
			want: gitsync.ErrNotProposable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := gitsync.NewProposer(gitsync.ProposerConfig{
				Enabled: true, Source: mustSource(t, f), Host: mustHost(t, f), Ref: proposeRef, Path: proposePath,
				Changesets: newChangesetReader(tc.cs), Inventory: f.graph, Proposals: newFakeProposals(),
			})
			_, err := p.Propose(context.Background(), tc.cs.ID, "brian")
			if !errors.Is(err, tc.want) {
				t.Fatalf("Propose = %v, want %v", err, tc.want)
			}
			if prs := f.host.openPRs(); len(prs) != 0 {
				t.Errorf("a refused proposal opened %d request(s)", len(prs))
			}
		})
	}

	// --- control: the same harness DOES propose a real changeset -----------
	if _, err := f.proposer.Propose(context.Background(), proposeChgset, "brian"); err != nil {
		t.Fatalf("control failed: the harness cannot propose anything at all: %v", err)
	}
	if len(f.host.openPRs()) != 1 {
		t.Fatal("control failed: a valid proposal opened no request, so the refusals above prove nothing")
	}
}

// --- AC3 ---------------------------------------------------------------------

// TestAC3_HostFailureLeavesNoOrphanBranch: either the branch and the request
// both exist, or neither does.
func TestAC3_HostFailureLeavesNoOrphanBranch(t *testing.T) {
	failures := []struct {
		name string
		op   hostFailure
	}{
		// Each of these fails AFTER a branch has been created, which is the
		// only situation the compensating delete exists for. (A failure
		// before that — an unreachable host, an unreadable base document —
		// cannot orphan anything, because nothing has been created yet.)
		{name: "the commit is refused", op: failCommit},
		{name: "the pull-request lookup is refused", op: failFindPR},
		{name: "opening the pull request is refused", op: failOpenPR},
	}
	for _, provider := range []string{"github", "gitlab"} {
		for _, tc := range failures {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				f := newProposeFixture(t, provider)
				f.host.failWith(tc.op, 500)

				_, err := f.proposer.Propose(context.Background(), proposeChgset, "brian")
				if err == nil {
					t.Fatal("Propose succeeded although the host refused a call")
				}
				if f.host.hasBranch(f.branch()) {
					t.Errorf("the failed proposal left the orphan branch %s behind", f.branch())
				}
				if prs := f.host.openPRs(); len(prs) != 0 {
					t.Errorf("the failed proposal left %d request(s) behind", len(prs))
				}

				// --- control: the same fixture, unbroken, DOES create the
				// branch — so "no branch" above is evidence, not an artefact
				// of a mock that never records one.
				f.host.clearFailures()
				if _, retryErr := f.proposer.Propose(context.Background(), proposeChgset, "brian"); retryErr != nil {
					t.Fatalf("control failed: the retry after clearing the failure errored: %v", retryErr)
				}
				if !f.host.hasBranch(f.branch()) {
					t.Fatal("control failed: a successful proposal created no branch, so the assertion above proves nothing")
				}
			})
		}
	}
}

// TestAC3_AnExistingBranchSurvivesAFailedRepropose is the other half of AC3's
// compensation rule: the branch is removed only when THIS call created it. A
// branch carrying a pull request someone is already reviewing must never be
// deleted because a later propose failed.
func TestAC3_AnExistingBranchSurvivesAFailedRepropose(t *testing.T) {
	f := newProposeFixture(t, "github")
	if _, err := f.proposer.Propose(context.Background(), proposeChgset, "brian"); err != nil {
		t.Fatalf("first Propose: %v", err)
	}
	f.host.failWith(failUpdatePR, 500)
	if _, err := f.proposer.Propose(context.Background(), proposeChgset, "brian"); err == nil {
		t.Fatal("the second Propose succeeded although the host refused the update")
	}
	if !f.host.hasBranch(f.branch()) {
		t.Errorf("a failed re-propose deleted the branch %s, which carried an open request", f.branch())
	}
	if len(f.host.openPRs()) != 1 {
		t.Errorf("a failed re-propose disturbed the open request: %+v", f.host.openPRs())
	}
}

// --- AC4 ---------------------------------------------------------------------

// TestAC4_ProposingTwiceUpdatesTheExistingRequest.
func TestAC4_ProposingTwiceUpdatesTheExistingRequest(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			f := newProposeFixture(t, provider)

			first, err := f.proposer.Propose(context.Background(), proposeChgset, "brian")
			if err != nil {
				t.Fatalf("first Propose: %v", err)
			}
			second, err := f.proposer.Propose(context.Background(), proposeChgset, "brian")
			if err != nil {
				t.Fatalf("second Propose: %v", err)
			}

			if second.Created {
				t.Error("the second propose reported opening a new request")
			}
			if first.PullRequestID != second.PullRequestID {
				t.Errorf("request id changed between proposes: %q then %q", first.PullRequestID, second.PullRequestID)
			}
			if prs := f.host.openPRs(); len(prs) != 1 {
				t.Errorf("open requests = %d, want exactly 1: %+v", len(prs), prs)
			}
			if f.proposals.count() != 1 {
				t.Errorf("recorded proposals = %d, want exactly 1", f.proposals.count())
			}
			// The branch was created once and re-used, not re-created.
			_, created := f.host.surfaces()
			if len(created) != 1 {
				t.Errorf("branches created = %v, want exactly one", created)
			}

			// --- control: the mock CAN hold two requests ------------------
			// Otherwise "exactly 1" above would be a property of the double.
			other := change.Changeset{ID: "cs-other", Title: "another change", Status: change.StatusDraft, Ops: f.ops}
			p2 := gitsync.NewProposer(gitsync.ProposerConfig{
				Enabled: true, Source: mustSource(t, f), Host: mustHost(t, f), Ref: proposeRef, Path: proposePath,
				Changesets: newChangesetReader(other), Inventory: f.graph, Proposals: newFakeProposals(),
			})
			if _, err := p2.Propose(context.Background(), "cs-other", "brian"); err != nil {
				t.Fatalf("control failed: a second, different changeset could not be proposed: %v", err)
			}
			if prs := f.host.openPRs(); len(prs) != 2 {
				t.Fatalf("control failed: the mock holds %d request(s) after two distinct proposals, so \"exactly 1\" above proves nothing", len(prs))
			}
		})
	}
}

// --- AC5 ---------------------------------------------------------------------

// TestAC5_CredentialIsAbsentFromEverySurface drives the REAL HTTPHost with a
// REAL push token through a successful proposal and a failing one, then scans
// every surface the card names — the pull-request body, the commit message
// and the branch name — plus the log, the audit trail and the returned error,
// which is where a credential leaks in practice.
//
// Two controls run first, exactly as T-2701's AC6 test does: the host must
// have actually been presented with the token, and each surface must contain
// a known non-secret marker proving it was populated and is being read.
func TestAC5_CredentialIsAbsentFromEverySurface(t *testing.T) {
	f := newProposeFixture(t, "gitlab")

	res, err := f.proposer.Propose(context.Background(), proposeChgset, "brian")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	// A failing cycle: the host answers 500 with a body quoting the
	// credential headers back at us. This is the realistic leak.
	f.host.failWith(failUpdatePR, 500)
	_, failErr := f.proposer.Propose(context.Background(), proposeChgset, "brian")
	if failErr == nil {
		t.Fatal("the failing propose did not fail")
	}

	// --- control 1: the credential really was in flight --------------------
	if seen := f.host.credentialSeen(); !strings.Contains(seen, pushToken) {
		t.Fatalf("control failed: the host was presented with %q, which does not carry the push token — "+
			"every absence assertion below would be vacuous", seen)
	}

	commits, branches := f.host.surfaces()
	prs := f.host.openPRs()
	if len(prs) != 1 || len(commits) == 0 || len(branches) == 0 {
		t.Fatalf("the proposal did not populate the surfaces to scan: prs=%d commits=%d branches=%d",
			len(prs), len(commits), len(branches))
	}
	auditText := auditSurface(t, f.audit)

	surfaces := []struct {
		name    string
		text    string
		control string
	}{
		{name: "pull request body", text: prs[0].Body, control: "Blast radius"},
		{name: "pull request title", text: prs[0].Title, control: "vnprox"},
		{name: "commit message", text: strings.Join(commits, "\n"), control: proposeChgset},
		{name: "branch name", text: strings.Join(branches, "\n"), control: gitsync.DefaultBranchPrefix},
		{name: "daemon log", text: f.logs(), control: "gitsync"},
		{name: "audit entries", text: auditText, control: "changeset.propose"},
		{name: "the error returned to the caller", text: failErr.Error(), control: "gitsync"},
		{name: "the recorded proposal", text: recordedProposalText(t, f), control: res.Branch},
	}
	for _, s := range surfaces {
		t.Run(s.name, func(t *testing.T) {
			// --- control 2: this surface is populated and readable ---------
			if !strings.Contains(s.text, s.control) {
				t.Fatalf("control failed: the %s surface does not contain %q, so it was never populated — "+
					"a leak assertion against it proves nothing.\nsurface was:\n%s", s.name, s.control, s.text)
			}
			if strings.Contains(s.text, pushToken) {
				t.Errorf("the push credential appears in the %s surface:\n%s", s.name, s.text)
			}
			if strings.Contains(s.text, "VNPROXPUSHMARKER") {
				t.Errorf("part of the push credential appears in the %s surface:\n%s", s.name, s.text)
			}
		})
	}
}

func recordedProposalText(t *testing.T, f *proposeFixture) string {
	t.Helper()
	row, err := f.proposals.Get(context.Background(), proposeChgset)
	if err != nil {
		t.Fatalf("the proposal was not recorded: %v", err)
	}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshaling the recorded proposal: %v", err)
	}
	return string(b)
}

// --- AC6 ---------------------------------------------------------------------

// TestAC6_BothHostsProduceTheSameProposal runs the identical proposal against
// both hosts' REST shapes and asserts the OUTCOME is the same. Nothing in the
// Proposer is parameterized by provider — see
// TestAC6_NoHostSpecificLogicAboveTheInterface below, which asserts that
// structurally.
func TestAC6_BothHostsProduceTheSameProposal(t *testing.T) {
	results := map[string][]change.Op{}
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			f := newProposeFixture(t, provider)
			res, err := f.proposer.Propose(context.Background(), proposeChgset, "brian")
			if err != nil {
				t.Fatalf("Propose: %v", err)
			}
			if res.PullRequestURL == "" || res.PullRequestID == "" {
				t.Fatalf("no request identity came back: %+v", res)
			}
			if res.Branch != gitsync.DefaultBranchPrefix+proposeChgset {
				t.Errorf("branch = %q", res.Branch)
			}
			committed, ok := f.host.fileOn(res.Branch, proposePath)
			if !ok {
				t.Fatal("nothing was committed on the branch")
			}
			results[provider] = planFor(t, committed, f.graph)
		})
	}
	if !sameOpSet(results["github"], results["gitlab"]) {
		t.Errorf("the two hosts produced documents that plan differently:\ngithub: %v\ngitlab: %v",
			results["github"], results["gitlab"])
	}
	if len(results["github"]) == 0 {
		t.Error("both hosts produced a document that plans to nothing; the comparison above is vacuous")
	}
}

// TestAC6_AnyHostImplementationWorks: the Proposer drives the Host interface
// and nothing else, so a completely different implementation — not an
// HTTPHost, not speaking either provider's REST shape — proposes identically.
func TestAC6_AnyHostImplementationWorks(t *testing.T) {
	g := buildFixtureGraph(t, fixtureThreeNode)
	baseDoc := specMatchingLive(t, g)
	ops := planFor(t, divergentSpec(t, g, 1400), g)

	// The base document still has to be READ from somewhere; that is the
	// (separate) read seam, driven here by T-2701's own fake source.
	src := &fakeSource{}
	src.set("basesha", baseDoc)
	host := newFakeHost(proposeRef, map[string][]byte{proposePath: baseDoc})

	cs := change.Changeset{ID: "cs-fake", Title: "MTU", Status: change.StatusDraft, Ops: ops}
	p := gitsync.NewProposer(gitsync.ProposerConfig{
		Enabled: true, Source: src, Host: host, Ref: proposeRef, Path: proposePath,
		Changesets: newChangesetReader(cs), Inventory: g, Proposals: newFakeProposals(),
	})

	res, err := p.Propose(context.Background(), "cs-fake", "brian")
	if err != nil {
		t.Fatalf("Propose against a non-HTTP host: %v", err)
	}
	if !res.Created || res.PullRequestURL != "fake://pr/1" {
		t.Fatalf("unexpected result: %+v", res)
	}
	committed := host.files[res.Branch][proposePath]
	if !sameOpSet(planFor(t, committed, g), ops) {
		t.Error("the document committed through a plain Host implementation does not round-trip")
	}
}

// TestAC6_NoHostSpecificLogicAboveTheInterface is the structural half of AC6:
// the propose path must not know which host it is talking to. Asserted by
// reading the source — with a control leg proving the scan works, since a
// scan that found nothing anywhere would pass trivially.
func TestAC6_NoHostSpecificLogicAboveTheInterface(t *testing.T) {
	above, err := os.ReadFile("propose.go")
	if err != nil {
		t.Fatalf("reading propose.go: %v", err)
	}
	for _, line := range strings.Split(string(above), "\n") {
		// This module's own import path contains the string "github.com";
		// that is not host-specific logic.
		if strings.Contains(line, "github.com/bgovanlu/vnprox") {
			continue
		}
		for _, forbidden := range []string{"github", "GitHub", "gitlab", "GitLab"} {
			if strings.Contains(line, forbidden) {
				t.Errorf("propose.go mentions %q — host-specific logic belongs below the Host interface, in host.go:\n  %s",
					forbidden, strings.TrimSpace(line))
			}
		}
	}

	// --- control: the scan does find these words where they DO belong ------
	below, err := os.ReadFile("host.go")
	if err != nil {
		t.Fatalf("reading host.go: %v", err)
	}
	if !strings.Contains(string(below), "ProviderGitHub") {
		t.Fatal("control failed: host.go does not mention ProviderGitHub, so the scan above proves nothing")
	}
}

// --- the seams ---------------------------------------------------------------

// TestSeamsStaySeparate is this card's structural invariant: the READ seam and
// the WRITE seam are different types, and neither grew the other's verbs.
//
// It is the same standard T-2701 held itself to with
// TestChangesetStagerHasNoApplyVerb — verified by temporarily adding a Push
// method to Source and watching this test fail.
func TestSeamsStaySeparate(t *testing.T) {
	sourceType := reflect.TypeOf((*gitsync.Source)(nil)).Elem()
	hostType := reflect.TypeOf((*gitsync.Host)(nil)).Elem()
	readerType := reflect.TypeOf((*gitsync.ChangesetReader)(nil)).Elem()

	// 1. The read seam has no write verb. If it did, the sync Service — which
	//    holds one — could push.
	for _, forbidden := range []string{"Push", "Commit", "Create", "Delete", "Update", "Write", "Open", "Merge"} {
		for i := 0; i < sourceType.NumMethod(); i++ {
			if strings.Contains(sourceType.Method(i).Name, forbidden) {
				t.Errorf("gitsync.Source exposes %q — the sync path must stay read-only", sourceType.Method(i).Name)
			}
		}
	}
	// control: the same scan over the WRITE seam finds those verbs, so the
	// loop above is actually looking at method names.
	found := false
	for i := 0; i < hostType.NumMethod(); i++ {
		if strings.Contains(hostType.Method(i).Name, "Create") {
			found = true
		}
	}
	if !found {
		t.Fatal("control failed: the write seam has no Create* method, so the scan above proves nothing")
	}

	// 2. The write seam cannot merge, approve, or poll: vnprox opens a
	//    request and stops.
	for _, forbidden := range []string{"Merge", "Approve", "Review", "Poll", "Wait", "Status", "Check"} {
		for i := 0; i < hostType.NumMethod(); i++ {
			if strings.Contains(hostType.Method(i).Name, forbidden) {
				t.Errorf("gitsync.Host exposes %q — vnprox opens a pull request and stops", hostType.Method(i).Name)
			}
		}
	}

	// 3. The propose path's change-engine seam is one read.
	if readerType.NumMethod() != 1 {
		t.Errorf("gitsync.ChangesetReader has %d methods; proposing is a pure read of a changeset", readerType.NumMethod())
	}
	for _, forbidden := range []string{"Apply", "Confirm", "Rollback", "Discard", "Approve", "Create", "Update"} {
		for i := 0; i < readerType.NumMethod(); i++ {
			if strings.Contains(readerType.Method(i).Name, forbidden) {
				t.Errorf("gitsync.ChangesetReader exposes %q — proposing must not mutate a changeset", readerType.Method(i).Name)
			}
		}
	}

	// 4. The read implementation does not satisfy the write interface: the
	//    two are not one type wearing two hats.
	if reflect.TypeOf((*gitsync.HTTPSource)(nil)).Implements(hostType) {
		t.Error("*gitsync.HTTPSource satisfies gitsync.Host — the read and write seams have collapsed into one type")
	}
	// control: something DOES satisfy it.
	if !reflect.TypeOf((*gitsync.HTTPHost)(nil)).Implements(hostType) {
		t.Fatal("control failed: *gitsync.HTTPHost does not satisfy gitsync.Host, so the assertion above proves nothing")
	}
}

// TestProposerIsInertUntilConfigured: a deployment that has not set up a
// write credential proposes nothing and contacts nothing.
func TestProposerIsInertUntilConfigured(t *testing.T) {
	p := gitsync.NewProposer(gitsync.ProposerConfig{})
	if p.Enabled() {
		t.Error("a zero-valued Proposer reports itself enabled")
	}
	if _, err := p.Propose(context.Background(), "cs-1", "brian"); !errors.Is(err, gitsync.ErrProposeNotConfigured) {
		t.Errorf("Propose on an unconfigured proposer = %v, want ErrProposeNotConfigured", err)
	}
	if _, err := p.Get(context.Background(), "cs-1"); !errors.Is(err, gitsync.ErrNoProposal) {
		t.Errorf("Get on an unconfigured proposer = %v, want ErrNoProposal", err)
	}
}

// TestNewHTTPHost_Validation: the write host refuses what it cannot safely
// do, at construction rather than at the first push.
func TestNewHTTPHost_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     gitsync.HostConfig
		wantErr string
	}{
		{name: "empty url", cfg: gitsync.HostConfig{Token: "t"}, wantErr: "url is required"},
		{
			name:    "plaintext http to a real host is refused",
			cfg:     gitsync.HostConfig{URL: "http://git.example.com/org/infra", Provider: gitsync.ProviderGitHub, Token: "t"},
			wantErr: "must use https",
		},
		{
			name:    "a credential in the url is refused, and the message names the push key",
			cfg:     gitsync.HostConfig{URL: "https://user:s3cret@github.com/org/infra", Token: "t"},
			wantErr: "push_token_file",
		},
		{
			name:    "a raw file host has no pull-request API",
			cfg:     gitsync.HostConfig{URL: "https://files.example/specs", Provider: gitsync.ProviderRaw, Token: "t"},
			wantErr: "no branch or pull-request API",
		},
		{
			name:    "a write host with no credential is refused",
			cfg:     gitsync.HostConfig{URL: "https://github.com/org/infra"},
			wantErr: "write-scoped credential is required",
		},
		{
			name: "an ordinary github repository is fine",
			cfg:  gitsync.HostConfig{URL: "https://github.com/org/infra", Token: "t"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, err := gitsync.NewHTTPHost(tc.cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("NewHTTPHost: %v", err)
				}
				if h == nil {
					t.Fatal("NewHTTPHost returned no host and no error")
				}
				return
			}
			if err == nil {
				t.Fatalf("NewHTTPHost accepted %+v", tc.cfg)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Errorf("the refusal echoes the credential: %q", err)
			}
		})
	}
}

// --- helpers ------------------------------------------------------------------

func mustSource(t *testing.T, f *proposeFixture) gitsync.Source {
	t.Helper()
	return f.reSource(t)
}

func mustHost(t *testing.T, f *proposeFixture) gitsync.Host {
	t.Helper()
	return f.reHost(t)
}

func boolPtr(b bool) *bool { return &b }
