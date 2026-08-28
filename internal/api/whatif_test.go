// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/failsim"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/whatif"
)

// whatif_test.go is T-4103's HTTP-level coverage: it proves the router
// mounts POST /capacity/what-if netRead-gated and serializes whatif.Verdict
// correctly. The evaluation logic itself is covered by internal/whatif's
// own tests.

type fakeWhatIfService struct {
	err     error
	gotTgt  inventory.Ref
	gotProf whatif.GuestProfile
	verdict whatif.Verdict
	gotN    int
}

func (f *fakeWhatIfService) Evaluate(_ context.Context, profile whatif.GuestProfile, n int, target inventory.Ref) (whatif.Verdict, error) {
	f.gotProf, f.gotN, f.gotTgt = profile, n, target
	return f.verdict, f.err
}

func whatIfTestRouter(svc WhatIfService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, WhatIf: svc,
	})
}

func TestWhatIf_Serializes(t *testing.T) {
	breaksAt := 5
	svc := &fakeWhatIfService{
		verdict: whatif.Verdict{
			N: 10,
			Capacity: whatif.CapacityAxis{
				Status: whatif.AxisBreaks, BreaksAtN: &breaksAt, ConsumedPct: 100,
				Basis: "estimate: observed peak + linear projection", Estimated: true,
			},
			IPAM: whatif.IPAMAxis{Status: whatif.AxisOK, FreeAddresses: 200, AddrsPerGuest: 1},
			Failsim: whatif.FailsimAxis{
				Status: whatif.AxisOK,
				Before: failsim.Impact{Severity: failsim.SeverityNone},
				After:  failsim.Impact{Severity: failsim.SeverityNone},
			},
			Binding:     "capacity",
			BindingAtN:  &breaksAt,
			Unavailable: []string{},
			Summary:     "adding 10 guest(s): capacity is the binding constraint, breaking at N=5.",
		},
	}

	body, _ := json.Marshal(whatIfRequest{
		Profile: guestProfileRequest{
			Name: "standard-vm", NICCount: 1, ExpectedMbps: 10,
			Attachment: attachmentRequest{Kind: whatif.AttachBridge, Node: "pve1", Name: "vmbr9"},
		},
		N:          10,
		FailTarget: "physnic:pve1:eno1",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/capacity/what-if", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	whatIfTestRouter(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got verdictResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Binding != "capacity" || got.BindingAtN == nil || *got.BindingAtN != 5 {
		t.Errorf("binding = %+v, want capacity/5", got)
	}
	if !got.Capacity.Estimated || got.Capacity.Basis == "" {
		t.Errorf("capacity axis must round-trip Estimated/Basis, got %+v", got.Capacity)
	}
	if got.IPAM.Estimated {
		t.Errorf("ipam axis must never be Estimated, got %+v", got.IPAM)
	}

	if svc.gotN != 10 {
		t.Errorf("service got n=%d, want 10", svc.gotN)
	}
	if svc.gotProf.Attachment.Name != "vmbr9" {
		t.Errorf("service got attachment=%+v, want vmbr9", svc.gotProf.Attachment)
	}
	wantTarget := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
	if svc.gotTgt != wantTarget {
		t.Errorf("service got target=%v, want %v", svc.gotTgt, wantTarget)
	}
}

func TestWhatIf_RejectsBadAttachmentKind(t *testing.T) {
	svc := &fakeWhatIfService{}
	body, _ := json.Marshal(whatIfRequest{
		Profile: guestProfileRequest{Attachment: attachmentRequest{Kind: "not-a-kind"}},
		N:       1,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/capacity/what-if", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	whatIfTestRouter(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestWhatIf_RejectsNonPositiveN(t *testing.T) {
	svc := &fakeWhatIfService{}
	body, _ := json.Marshal(whatIfRequest{
		Profile: guestProfileRequest{Attachment: attachmentRequest{Kind: whatif.AttachBridge, Node: "pve1", Name: "vmbr9"}},
		N:       0,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/capacity/what-if", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	whatIfTestRouter(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestWhatIf_NilServiceLeavesRouteUnmounted(t *testing.T) {
	body, _ := json.Marshal(whatIfRequest{
		Profile: guestProfileRequest{Attachment: attachmentRequest{Kind: whatif.AttachBridge, Node: "pve1", Name: "vmbr9"}},
		N:       1,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/capacity/what-if", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	whatIfTestRouter(nil).ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("expected the route to be unmounted with a nil service, got 200")
	}
}
