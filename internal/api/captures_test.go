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
	startErr     error
	downloadErr  error
	downloadData []byte
	group        capture.Group
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
func (f fakeCaptureService) Download(_ context.Context, sessionID string) ([]byte, capture.Session, error) {
	if f.downloadErr != nil {
		return nil, capture.Session{}, f.downloadErr
	}
	for _, s := range f.group.Sessions {
		if s.ID == sessionID {
			return f.downloadData, s, nil
		}
	}
	return f.downloadData, capture.Session{ID: sessionID}, nil
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

// TestCaptureRoutes_Download is T-1302's per-session pcap download route:
// {id} is the group id, ?sessionId= disambiguates a multi-point group
// (defaulting to the first session), and the response is the raw pcap bytes
// with an attachment Content-Disposition — never a JSON envelope.
func TestCaptureRoutes_Download(t *testing.T) {
	group := capture.Group{
		ID: "g1", Status: capture.StatusCompleted,
		Sessions: []capture.Session{
			{ID: "s1", GroupID: "g1", Node: "pve1"},
			{ID: "s2", GroupID: "g1", Node: "pve2"},
		},
	}
	auth := captureAuth(map[string]bool{capNetRead: true, "capture": true})

	t.Run("no sessionId downloads the first/primary session", func(t *testing.T) {
		svc := fakeCaptureService{group: group, downloadData: []byte("pcap-bytes-1")}
		r := newCaptureTestRouter(svc, auth)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/captures/g1/download", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /captures/g1/download = %d, want 200 (body %s)", w.Code, w.Body.String())
		}
		if got := w.Body.String(); got != "pcap-bytes-1" {
			t.Errorf("body = %q, want the raw pcap bytes", got)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/vnd.tcpdump.pcap" {
			t.Errorf("Content-Type = %q", ct)
		}
		if cd := w.Header().Get("Content-Disposition"); cd == "" {
			t.Error("want a Content-Disposition attachment header")
		}
	})

	t.Run("explicit sessionId picks that session", func(t *testing.T) {
		svc := fakeCaptureService{group: group, downloadData: []byte("pcap-bytes-2")}
		r := newCaptureTestRouter(svc, auth)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/captures/g1/download?sessionId=s2", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if got := w.Body.String(); got != "pcap-bytes-2" {
			t.Errorf("body = %q, want pcap-bytes-2", got)
		}
	})

	t.Run("unknown sessionId -> 404", func(t *testing.T) {
		svc := fakeCaptureService{group: group}
		r := newCaptureTestRouter(svc, auth)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/captures/g1/download?sessionId=nope", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})

	t.Run("purged file -> 404", func(t *testing.T) {
		svc := fakeCaptureService{group: group, downloadErr: capture.ErrFileUnavailable}
		r := newCaptureTestRouter(svc, auth)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/captures/g1/download", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})

	t.Run("without capture cap -> 403", func(t *testing.T) {
		svc := fakeCaptureService{group: group, downloadData: []byte("x")}
		noCap := captureAuth(map[string]bool{capNetRead: true})
		r := newCaptureTestRouter(svc, noCap)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/captures/g1/download", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", w.Code)
		}
	})
}
