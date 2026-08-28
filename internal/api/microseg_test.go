// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/microseg"
)

// microseg_test.go is T-1602's HTTP-level coverage: it proves the router mounts
// the two routes netRead-gated and serializes the proposal (including the
// honesty fields — coverage %, uncovered count) and the dry-run report
// (including the loud cannotDetermine bucket) correctly. The planner's own
// logic is covered by internal/microseg's tests.

type fakeMicrosegService struct {
	err    error
	ops    []change.Op
	report microseg.Report
	prop   microseg.Proposal
}

func (f *fakeMicrosegService) Propose(context.Context, string) (microseg.Proposal, []change.Op, error) {
	return f.prop, f.ops, f.err
}

func (f *fakeMicrosegService) DryRun(context.Context, string, bool) (microseg.Proposal, microseg.Report, error) {
	return f.prop, f.report, f.err
}

func microsegTestRouter(svc MicrosegService, authed bool) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: authed}, Microseg: svc,
	})
}

func sampleProposal() (microseg.Proposal, []change.Op) {
	subj := microseg.Subject{
		GuestRef:   inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "100"},
		RulesetRef: microseg.GuestRulesetRef("pve1", "qemu", "100"),
	}
	prop := microseg.Proposal{
		Subject:            subj,
		Directions:         []string{"out"},
		Rules:              []inventory.FwRule{{Direction: "out", Action: "ACCEPT", Proto: "tcp", Dport: "443", Dest: "10.0.0.0/24", Pos: 0, Enabled: true}},
		CoveragePct:        99.7,
		UncoveredFlowCount: 3,
	}
	return prop, microseg.Stage(prop)
}

func TestMicrosegPropose_Serializes(t *testing.T) {
	prop, ops := sampleProposal()
	svc := &fakeMicrosegService{prop: prop, ops: ops}

	body := strings.NewReader(`{"guestRef":"guest:pve1:100"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/microseg/propose", body)
	rec := httptest.NewRecorder()
	microsegTestRouter(svc, true).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got proposalView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.CoveragePct != 99.7 || got.UncoveredFlowCount != 3 {
		t.Errorf("honesty fields not serialized: coverage=%.2f uncovered=%d", got.CoveragePct, got.UncoveredFlowCount)
	}
	if len(got.Rules) != 1 || got.Rules[0].Dport != "443" {
		t.Errorf("rules not serialized: %+v", got.Rules)
	}
	if len(got.StagedOps) != 1 || got.StagedOps[0].Type != change.OpFwRuleCreate {
		t.Errorf("staged ops must be present and fw.rule.create: %+v", got.StagedOps)
	}
}

func TestMicrosegDryRun_SerializesCannotDetermine(t *testing.T) {
	prop, _ := sampleProposal()
	svc := &fakeMicrosegService{
		prop: prop,
		report: microseg.Report{
			WouldAllow:      []microseg.FlowRef{{Direction: "out", Port: 443, PeerSubnet: "10.0.0.0/24"}},
			WouldBlock:      []microseg.FlowRef{{Direction: "out", Port: 3306, PeerSubnet: "10.9.0.0/24"}},
			CannotDetermine: []microseg.FlowRef{{Direction: "out", Port: 443, Reason: "endpoint IP is not known"}},
			CoveragePct:     99.7,
		},
	}
	body := strings.NewReader(`{"guestRef":"guest:pve1:100","heldOut":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/microseg/dry-run", body)
	rec := httptest.NewRecorder()
	microsegTestRouter(svc, true).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got dryRunView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.WouldBlock) != 1 || len(got.CannotDetermine) != 1 {
		t.Errorf("wouldBlock/cannotDetermine = %d/%d, want 1/1", len(got.WouldBlock), len(got.CannotDetermine))
	}
	if got.CannotDetermine[0].Reason == "" {
		t.Error("cannotDetermine reason must survive serialization")
	}
	// Empty buckets must be [] not null.
	if got.Ungoverned == nil {
		t.Error("ungoverned must serialize as [] not null")
	}
}

func TestMicrosegPropose_RequiresGuestRef(t *testing.T) {
	prop, ops := sampleProposal()
	svc := &fakeMicrosegService{prop: prop, ops: ops}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/microseg/propose", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	microsegTestRouter(svc, true).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestMicrosegPropose_GatedNetRead(t *testing.T) {
	prop, ops := sampleProposal()
	svc := &fakeMicrosegService{prop: prop, ops: ops}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/microseg/propose", strings.NewReader(`{"guestRef":"guest:pve1:100"}`))
	rec := httptest.NewRecorder()
	microsegTestRouter(svc, false).ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("unauthenticated request must not reach the handler; got 200")
	}
}
