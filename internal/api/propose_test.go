package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/gitsync"
)

// stubProposer is the ChangesetProposer seam under test. It records whether
// Propose was reached at all, which is what the capability/CSRF assertions
// below actually rest on: a route that answered 403 but had already pushed to
// a repository would be a much worse failure than a wrong status code.
//
//nolint:govet // fieldalignment: test double; the call counter sits with what it counts.
type stubProposer struct {
	proposal gitsync.Proposal
	err      error
	enabled  bool
	calls    int
}

func (s *stubProposer) Enabled() bool { return s.enabled }

func (s *stubProposer) Propose(_ context.Context, id, actor string) (gitsync.Proposal, error) {
	s.calls++
	if s.err != nil {
		return gitsync.Proposal{}, s.err
	}
	p := s.proposal
	p.ChangesetID = id
	p.ProposedBy = actor
	return p, nil
}

func (s *stubProposer) Get(_ context.Context, id string) (gitsync.Proposal, error) {
	if s.err != nil {
		return gitsync.Proposal{}, s.err
	}
	if s.proposal.PullRequestURL == "" {
		return gitsync.Proposal{}, gitsync.ErrNoProposal
	}
	p := s.proposal
	p.ChangesetID = id
	return p, nil
}

func proposeRouter(svc ChangesetProposer, auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, ChangesetProposer: svc,
	})
}

func okProposer() *stubProposer {
	return &stubProposer{
		enabled: true,
		proposal: gitsync.Proposal{
			Remote: "https://github.com/org/infra (github)", Branch: "vnprox/changeset-01J", Path: "network/cluster.yaml",
			PullRequestID: "42", PullRequestURL: "https://github.test/org/infra/pull/42", Created: true,
		},
	}
}

// TestPropose_OpensAndReportsThePullRequest is the happy path on both routes.
func TestPropose_OpensAndReportsThePullRequest(t *testing.T) {
	svc := okProposer()
	r := proposeRouter(svc, fullCapsAuth("alice"))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/changesets/01J/propose", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST propose = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var got gitsync.Proposal
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not a proposal: %v (%s)", err, rec.Body.String())
	}
	if got.PullRequestURL == "" || got.ChangesetID != "01J" || got.ProposedBy != "alice" {
		t.Errorf("proposal = %+v, want the acting user and the changeset it names", got)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/changesets/01J/proposal", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET proposal = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestPropose_ErrorMapping: every refusal the propose path can produce maps
// to a status a client can act on, and a refusal never reads as a success.
func TestPropose_ErrorMapping(t *testing.T) {
	//nolint:govet // fieldalignment: test table; field order documents each case.
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantErr  string
	}{
		{name: "nothing to propose", err: gitsync.ErrNothingToPropose, wantCode: http.StatusUnprocessableEntity, wantErr: "nothing_to_propose"},
		{name: "not proposable", err: gitsync.ErrNotProposable, wantCode: http.StatusUnprocessableEntity, wantErr: "not_proposable"},
		{name: "not expressible in the spec", err: gitsync.ErrNotExpressible, wantCode: http.StatusUnprocessableEntity, wantErr: "not_expressible_in_spec"},
		{name: "the round trip failed", err: gitsync.ErrRoundTrip, wantCode: http.StatusUnprocessableEntity, wantErr: "spec_round_trip_failed"},
		{name: "no spec document", err: gitsync.ErrNoSpecDocument, wantCode: http.StatusUnprocessableEntity, wantErr: "no_spec_document"},
		{name: "the host is unreachable", err: gitsync.ErrUnreachable, wantCode: http.StatusBadGateway, wantErr: "remote_unreachable"},
		{name: "the host refused", err: gitsync.ErrRemoteStatus, wantCode: http.StatusBadGateway, wantErr: "remote_unreachable"},
		{name: "anything else", err: errors.New("boom"), wantCode: http.StatusBadGateway, wantErr: "propose_failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := okProposer()
			svc.err = tc.err
			r := proposeRouter(svc, fullCapsAuth("alice"))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/changesets/01J/propose", nil))
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("error body does not parse: %v (%s)", err, rec.Body.String())
			}
			if body.Error.Code != tc.wantErr {
				t.Errorf("error code = %q, want %q", body.Error.Code, tc.wantErr)
			}
		})
	}
}

// TestPropose_UnconfiguredAnswersHonestly: the route exists whether or not
// the deployment has a write credential, and says which it is.
func TestPropose_UnconfiguredAnswersHonestly(t *testing.T) {
	//nolint:govet // fieldalignment: test table; field order documents each case.
	for _, tc := range []struct {
		name string
		svc  ChangesetProposer
	}{
		{name: "no proposer wired at all", svc: nil},
		{name: "a proposer with no write credential", svc: &stubProposer{enabled: false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := proposeRouter(tc.svc, fullCapsAuth("alice"))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/changesets/01J/propose", nil))
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501 (body: %s)", rec.Code, rec.Body.String())
			}
			if stub, ok := tc.svc.(*stubProposer); ok && stub.calls != 0 {
				t.Errorf("an unconfigured proposer was still asked to propose %d time(s)", stub.calls)
			}
		})
	}
}

// TestPropose_AChangesetWithNoProposalIs404 — the review surface asks for the
// link on every changeset, and most have none.
func TestPropose_AChangesetWithNoProposalIs404(t *testing.T) {
	svc := &stubProposer{enabled: true}
	r := proposeRouter(svc, fullCapsAuth("alice"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/changesets/01J/proposal", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestPropose_RequiresWriteCapability: proposing pushes to a repository, so
// netRead is not enough — and the refusal happens BEFORE the proposer is
// reached, asserted on the call counter rather than only on the status code.
func TestPropose_RequiresWriteCapability(t *testing.T) {
	svc := okProposer()
	readOnly := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetRead: true},
	}
	r := proposeRouter(svc, readOnly)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/changesets/01J/propose", nil))
	if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
		t.Fatalf("a netRead-only session proposed a changeset (status %d)", rec.Code)
	}
	if svc.calls != 0 {
		t.Errorf("the proposer was reached %d time(s) by a session without netWrite", svc.calls)
	}

	// --- control: the same request with netWrite DOES reach it -------------
	rec = httptest.NewRecorder()
	proposeRouter(svc, fullCapsAuth("alice")).ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, "/api/v1/changesets/01J/propose", nil))
	if svc.calls != 1 {
		t.Fatalf("control failed: a fully-capable session did not reach the proposer either (calls=%d)", svc.calls)
	}

	// The read route, by contrast, is netRead: knowing where a changeset was
	// proposed must not require the capability to propose it.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/changesets/01J/proposal", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET proposal with netRead = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestProposeHasNoMergeRoute is the API half of "vnprox opens a pull request
// and stops": there is no route that merges, approves or polls one.
func TestProposeHasNoMergeRoute(t *testing.T) {
	r := proposeRouter(okProposer(), fullCapsAuth("alice"))
	for _, probe := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/changesets/01J/propose/merge"},
		{http.MethodPost, "/api/v1/changesets/01J/proposal/merge"},
		{http.MethodPost, "/api/v1/changesets/01J/proposal/approve"},
		{http.MethodDelete, "/api/v1/changesets/01J/proposal"},
		{http.MethodPut, "/api/v1/changesets/01J/proposal"},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(probe.method, probe.path, nil))
		if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
			t.Errorf("%s %s is served; vnprox opens a pull request and stops", probe.method, probe.path)
		}
	}
}
