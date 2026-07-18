package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/capture"
)

type fakeCaptureService struct {
	startErr error
	group    capture.Group
}

func (f fakeCaptureService) Start(_ context.Context, _ capture.StartRequest) (capture.Group, error) {
	return f.group, f.startErr
}
func (f fakeCaptureService) StopGroup(_ context.Context, _, _ string) (capture.Group, error) {
	return f.group, nil
}
func (f fakeCaptureService) Get(_ context.Context, _ string) (capture.Group, error) {
	return f.group, nil
}
func (f fakeCaptureService) List(_ context.Context) ([]capture.Group, error) {
	return []capture.Group{f.group}, nil
}

func newCaptureTestRouter(svc CaptureService, auth fakeAuthWithCaps) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{}, Captures: svc,
	})
}

func captureAuth(caps map[string]bool) fakeAuthWithCaps {
	return fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "root@pam"},
		caps:             caps,
		csrf:             false,
	}
}

// TestCaptureRoutes_CapabilityGate is T-1301 AC1 at the HTTP boundary: a
// session holding netRead/netWrite but NOT capture is rejected 403; a session
// that also holds capture succeeds. (The pure mapping — that netWrite alone
// never derives capture — is pinned by internal/auth's caps table test.)
func TestCaptureRoutes_CapabilityGate(t *testing.T) {
	svc := fakeCaptureService{group: capture.Group{ID: "g1", Status: capture.StatusRunning}}
	body := func() *bytes.Reader {
		b, _ := json.Marshal(map[string]any{"targetRef": "bridge:pve1:vmbr0", "filter": "tcp"})
		return bytes.NewReader(b)
	}

	t.Run("no capture cap -> 403", func(t *testing.T) {
		auth := captureAuth(map[string]bool{capNetRead: true, capNetWrite: true})
		r := newCaptureTestRouter(svc, auth)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/captures", body())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("POST /captures without capture cap = %d, want 403", w.Code)
		}
	})

	t.Run("with capture cap -> 201", func(t *testing.T) {
		auth := captureAuth(map[string]bool{capNetRead: true, "capture": true})
		r := newCaptureTestRouter(svc, auth)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/captures", body())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("POST /captures with capture cap = %d, want 201", w.Code)
		}
		var got capture.Group
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		if got.ID != "g1" {
			t.Errorf("group id = %q, want g1", got.ID)
		}
	})

	t.Run("GET /captures also gated on capture", func(t *testing.T) {
		auth := captureAuth(map[string]bool{capNetRead: true})
		r := newCaptureTestRouter(svc, auth)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/captures", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("GET /captures without capture cap = %d, want 403", w.Code)
		}
	})
}

// TestCaptureRoutes_ListWithCapability verifies the read path returns the
// list envelope once both caps are held.
func TestCaptureRoutes_ListWithCapability(t *testing.T) {
	svc := fakeCaptureService{group: capture.Group{ID: "g1", Status: capture.StatusCompleted}}
	auth := captureAuth(map[string]bool{capNetRead: true, "capture": true})
	r := newCaptureTestRouter(svc, auth)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/captures", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /captures = %d, want 200", w.Code)
	}
	var got captureListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != "g1" {
		t.Errorf("items = %+v, want one group g1", got.Items)
	}
}
