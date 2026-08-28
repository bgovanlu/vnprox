// SPDX-License-Identifier: Apache-2.0

package api

// T-2703's HTTP surface. The assertions that matter here are not the status
// codes: they are the call counters. A route that answered 403 but had already
// staged a changeset or pushed to a repository would be a far worse failure
// than a wrong code, so every capability/CSRF assertion rests on the
// reconciler not having been reached — each with a control leg proving it IS
// reached when the request is legitimate.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/gitsync"
	"github.com/bgovanlu/vnprox/internal/reconcile"
)

// stubReconciler is the DriftReconciler seam under test.
//
//nolint:govet // fieldalignment: test double; counters sit with what they count.
type stubReconciler struct {
	proposal      gitsync.Proposal
	restoreErr    error
	adoptErr      error
	adoptEnabled  bool
	restoreCalls  int
	adoptCalls    int
	adoptionCalls int
}

func (s *stubReconciler) AdoptEnabled() bool { return s.adoptEnabled }

func (s *stubReconciler) RestoreIntent(_ context.Context, findingID, actor string) (change.Changeset, error) {
	s.restoreCalls++
	if s.restoreErr != nil {
		return change.Changeset{}, s.restoreErr
	}
	return change.Changeset{
		ID: "01JDRAFT", Title: "drift: restore " + findingID + " to the spec",
		Author: actor, Status: change.StatusDraft,
	}, nil
}

func (s *stubReconciler) AdoptReality(_ context.Context, findingID, actor string) (gitsync.Proposal, error) {
	s.adoptCalls++
	if s.adoptErr != nil {
		return gitsync.Proposal{}, s.adoptErr
	}
	p := s.proposal
	p.FindingID = findingID
	p.ProposedBy = actor
	return p, nil
}

func (s *stubReconciler) Adoption(_ context.Context, findingID string) (gitsync.Proposal, error) {
	s.adoptionCalls++
	if s.proposal.PullRequestURL == "" {
		return gitsync.Proposal{}, gitsync.ErrNoProposal
	}
	p := s.proposal
	p.FindingID = findingID
	return p, nil
}

func reconcileRouter(svc DriftReconciler, auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, DriftReconciler: svc,
	})
}

func okReconciler() *stubReconciler {
	return &stubReconciler{
		adoptEnabled: true,
		proposal: gitsync.Proposal{
			Remote: "https://github.com/org/infra (github)", Branch: "vnprox/adopt-spec_reconciliation-bridge-pve1-vmbr0",
			Path: "network/cluster.yaml", PullRequestID: "42",
			PullRequestURL: "https://github.test/org/infra/pull/42", Created: true,
		},
	}
}

const findingPath = "/api/v1/drift/spec_reconciliation%7Cbridge:pve1:vmbr0"

// TestReconcile_BothActionsAreServed is the happy path on both writes and the
// read: each produces its own artifact, and neither produces the other's.
func TestReconcile_BothActionsAreServed(t *testing.T) {
	svc := okReconciler()
	r := reconcileRouter(svc, fullCapsAuth("alice"))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, findingPath+"/restore-intent", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST restore-intent = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var cs map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &cs); err != nil {
		t.Fatalf("decoding the changeset: %v", err)
	}
	if cs["status"] != string(change.StatusDraft) {
		t.Errorf("restoring intent returned a %v changeset, want a draft — this route must never apply", cs["status"])
	}
	if svc.adoptCalls != 0 {
		t.Errorf("restoring intent reached the git host %d time(s)", svc.adoptCalls)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, findingPath+"/adopt-reality", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST adopt-reality = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var proposal gitsync.Proposal
	if err := json.Unmarshal(rec.Body.Bytes(), &proposal); err != nil {
		t.Fatalf("decoding the proposal: %v", err)
	}
	if proposal.PullRequestURL == "" || proposal.FindingID == "" {
		t.Errorf("adoption response = %+v, want a pull request URL and the finding it came from", proposal)
	}
	if svc.restoreCalls != 1 {
		t.Errorf("adopting reality also staged a changeset (restore calls = %d)", svc.restoreCalls)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, findingPath+"/adoption", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET adoption = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestReconcile_ErrorMapping: every refusal the reconcile path can produce
// maps onto a code a client can act on.
func TestReconcile_ErrorMapping(t *testing.T) {
	cases := []struct {
		err      error
		name     string
		wantErr  string
		wantCode int
	}{
		{name: "not offered", err: reconcile.ErrNotOffered, wantCode: http.StatusNotFound, wantErr: "not_found"},
		{name: "nothing to propose", err: gitsync.ErrNothingToPropose, wantCode: http.StatusUnprocessableEntity, wantErr: "nothing_to_propose"},
		{name: "not expressible", err: gitsync.ErrNotExpressible, wantCode: http.StatusUnprocessableEntity, wantErr: "not_expressible_in_spec"},
		{name: "round trip", err: gitsync.ErrRoundTrip, wantCode: http.StatusUnprocessableEntity, wantErr: "spec_round_trip_failed"},
		{name: "no spec document", err: gitsync.ErrNoSpecDocument, wantCode: http.StatusUnprocessableEntity, wantErr: "no_spec_document"},
		{name: "host unreachable", err: gitsync.ErrUnreachable, wantCode: http.StatusBadGateway, wantErr: "remote_unreachable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := okReconciler()
			svc.adoptErr = tc.err
			rec := httptest.NewRecorder()
			reconcileRouter(svc, fullCapsAuth("alice")).ServeHTTP(rec,
				httptest.NewRequest(http.MethodPost, findingPath+"/adopt-reality", nil))
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding the error: %v", err)
			}
			if body.Error.Code != tc.wantErr {
				t.Errorf("error code = %q, want %q", body.Error.Code, tc.wantErr)
			}
		})
	}
}

// TestReconcile_AdoptingWithoutARepositoryIs501AndContactsNothing.
func TestReconcile_AdoptingWithoutARepositoryIs501AndContactsNothing(t *testing.T) {
	svc := &stubReconciler{adoptEnabled: false}
	rec := httptest.NewRecorder()
	reconcileRouter(svc, fullCapsAuth("alice")).ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, findingPath+"/adopt-reality", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (body: %s)", rec.Code, rec.Body.String())
	}
	if svc.adoptCalls != 0 {
		t.Errorf("an unconfigured deployment still reached the adoption path %d time(s)", svc.adoptCalls)
	}

	// Control: with a repository configured, the same request DOES reach it.
	ok := okReconciler()
	rec = httptest.NewRecorder()
	reconcileRouter(ok, fullCapsAuth("alice")).ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, findingPath+"/adopt-reality", nil))
	if ok.adoptCalls != 1 {
		t.Fatalf("control failed: a configured deployment did not reach the adoption path either (calls=%d)", ok.adoptCalls)
	}
}

// TestReconcile_RequiresWriteCapability: both actions produce a reviewable
// artifact outside the reader's own session — a draft changeset, or a commit
// pushed to a repository — so netRead is not enough for either, and the
// refusal happens before the reconciler is reached.
func TestReconcile_RequiresWriteCapability(t *testing.T) {
	svc := okReconciler()
	readOnly := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetRead: true},
	}
	r := reconcileRouter(svc, readOnly)

	for _, action := range []string{"/restore-intent", "/adopt-reality"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, findingPath+action, nil))
		if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
			t.Errorf("a netRead-only session reached %s (status %d)", action, rec.Code)
		}
	}
	if svc.restoreCalls+svc.adoptCalls != 0 {
		t.Errorf("a session without netWrite reached the reconciler (restore=%d adopt=%d)", svc.restoreCalls, svc.adoptCalls)
	}

	// --- control: the same requests with netWrite DO reach it --------------
	full := reconcileRouter(svc, fullCapsAuth("alice"))
	for _, action := range []string{"/restore-intent", "/adopt-reality"} {
		rec := httptest.NewRecorder()
		full.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, findingPath+action, nil))
	}
	if svc.restoreCalls != 1 || svc.adoptCalls != 1 {
		t.Fatalf("control failed: a fully-capable session did not reach the reconciler either (restore=%d adopt=%d)",
			svc.restoreCalls, svc.adoptCalls)
	}

	// Reading where a finding was adopted is netRead: seeing the link must
	// not require the capability to create it.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, findingPath+"/adoption", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET adoption with netRead = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestReconcileHasNoApplyRoute is the API half of "neither action applies":
// there is no route here that applies, confirms or merges anything.
func TestReconcileHasNoApplyRoute(t *testing.T) {
	r := reconcileRouter(okReconciler(), fullCapsAuth("alice"))
	for _, probe := range []struct{ method, path string }{
		{http.MethodPost, findingPath + "/restore-intent/apply"},
		{http.MethodPost, findingPath + "/restore-intent/confirm"},
		{http.MethodPost, findingPath + "/adopt-reality/merge"},
		{http.MethodPost, findingPath + "/adoption/merge"},
		{http.MethodPost, findingPath + "/adoption/approve"},
		{http.MethodDelete, findingPath + "/adoption"},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(probe.method, probe.path, nil))
		if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
			t.Errorf("%s %s is served; neither reconciliation action may apply or merge anything", probe.method, probe.path)
		}
	}
}
