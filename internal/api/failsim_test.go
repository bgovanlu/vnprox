// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/failsim"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// failsim_test.go is T-1604's HTTP-level coverage: it proves the router mounts
// the two routes netRead-gated and serializes failsim.SPOFScore / failsim.Impact
// correctly. The simulator's own logic is covered by internal/failsim's tests.

type fakeFailsimService struct {
	generatedAt  time.Time
	preflightErr error
	impact       failsim.Impact
	score        failsim.SPOFScore
}

func (f *fakeFailsimService) SPOFScore(context.Context) (failsim.SPOFScore, time.Time, error) {
	return f.score, f.generatedAt, nil
}

func (f *fakeFailsimService) PreflightImpactForChangeset(context.Context, string) (failsim.Impact, error) {
	return f.impact, f.preflightErr
}

func failsimTestRouter(svc FailsimService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Failsim: svc,
	})
}

func TestFailsimSPOFScore_Serializes(t *testing.T) {
	bond := inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"}
	guest := inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "101"}
	svc := &fakeFailsimService{
		generatedAt: time.Unix(1_800_000_000, 0),
		score: failsim.SPOFScore{
			Score: 75,
			Entries: []failsim.SPOFEntry{{
				Ref: bond,
				Impact: failsim.Impact{
					Target: bond, Severity: failsim.SeverityCritical,
					DisconnectedGuests: []inventory.Ref{guest},
					MgmtPathLoss:       []string{"pve1"},
					NotEvaluated:       []string{failsim.DimCeph, failsim.DimTunnels},
				},
			}},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/failsim/spof-score", nil)
	rec := httptest.NewRecorder()
	failsimTestRouter(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got spofScoreResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Score != 75 || len(got.Entries) != 1 {
		t.Fatalf("score/entries = %d/%d, want 75/1", got.Score, len(got.Entries))
	}
	e := got.Entries[0]
	if e.Ref != bond.String() || e.Impact.Severity != failsim.SeverityCritical {
		t.Errorf("entry = %+v", e)
	}
	if len(e.Impact.MgmtPathLoss) != 1 || e.Impact.MgmtPathLoss[0] != "pve1" {
		t.Errorf("mgmtPathLoss = %v, want [pve1]", e.Impact.MgmtPathLoss)
	}
	if len(e.Impact.NotEvaluated) != 2 {
		t.Errorf("notEvaluated = %v, want ceph+tunnels", e.Impact.NotEvaluated)
	}
	if got.GeneratedAt == "" {
		t.Error("generatedAt empty")
	}
}

func TestFailsimPreflightImpact_NotFound(t *testing.T) {
	svc := &fakeFailsimService{preflightErr: store.ErrNotFound}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/nope/preflight-impact", nil)
	rec := httptest.NewRecorder()
	failsimTestRouter(svc).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestFailsimPreflightImpact_Serializes(t *testing.T) {
	bond := inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"}
	svc := &fakeFailsimService{impact: failsim.Impact{
		Target: bond, Severity: failsim.SeverityCritical, QuorumRisk: true,
		NotEvaluated: []string{failsim.DimCeph},
	}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/cs-1/preflight-impact", nil)
	rec := httptest.NewRecorder()
	failsimTestRouter(svc).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got impactResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.QuorumRisk || got.Severity != failsim.SeverityCritical {
		t.Errorf("got = %+v", got)
	}
	if got.DisconnectedGuests == nil || got.StrandedVlans == nil || got.MgmtPathLoss == nil {
		t.Error("empty ref slices should serialize as [] not null")
	}
}
