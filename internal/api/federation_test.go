package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/federation"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeFederationService is an in-memory FederationService test double that
// records the last credential handed to Add/Update, so tests can prove the
// HTTP layer never echoes it back.
type fakeFederationService struct {
	items    map[string]federation.Cluster
	lastCred federation.Credential
	seq      int
}

func newFakeFederationService() *fakeFederationService {
	return &fakeFederationService{items: map[string]federation.Cluster{}}
}

func (f *fakeFederationService) Add(_ context.Context, name, apiURL string, cred federation.Credential, addedBy string) (federation.Cluster, error) {
	f.lastCred = cred
	f.seq++
	c := federation.Cluster{ID: "cl-" + itoa(f.seq), Name: name, APIURL: apiURL, Status: "unknown", AddedBy: addedBy, AddedAt: 1700000000}
	f.items[c.ID] = c
	return c, nil
}

func (f *fakeFederationService) Get(_ context.Context, id string) (federation.Cluster, error) {
	c, ok := f.items[id]
	if !ok {
		return federation.Cluster{}, store.ErrNotFound
	}
	return c, nil
}

func (f *fakeFederationService) List(context.Context) ([]federation.Cluster, error) {
	var out []federation.Cluster
	for _, c := range f.items {
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeFederationService) Update(_ context.Context, id, name, apiURL string, cred *federation.Credential) (federation.Cluster, error) {
	c, ok := f.items[id]
	if !ok {
		return federation.Cluster{}, store.ErrNotFound
	}
	if name != "" {
		c.Name = name
	}
	if apiURL != "" {
		c.APIURL = apiURL
	}
	if cred != nil {
		f.lastCred = *cred
	}
	f.items[id] = c
	return c, nil
}

func (f *fakeFederationService) Delete(_ context.Context, id string) error {
	delete(f.items, id)
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func newFederationTestRouter(caps map[string]bool, svc FederationService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuthWithCaps{
			caps: caps, csrf: true,
			fakeAuthWithUser: fakeAuthWithUser{username: "root@pam", fakeAuth: fakeAuth{authenticated: true}},
		},
		Topology:   fakeTopologyService{},
		Federation: svc,
	})
}

func TestFederationRoutes_NotMountedWithoutService(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:     fakeAuthWithCaps{caps: map[string]bool{"netRead": true}, csrf: true, fakeAuthWithUser: fakeAuthWithUser{username: "root@pam", fakeAuth: fakeAuth{authenticated: true}}},
		Topology: fakeTopologyService{},
		// Federation deliberately omitted.
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/clusters", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route should not be mounted)", rec.Code)
	}
}

func TestFederationRoutes_CreateRequiresNetWrite(t *testing.T) {
	r := newFederationTestRouter(map[string]bool{"netRead": true}, newFakeFederationService())
	body := bytes.NewBufferString(`{"name":"east","apiUrl":"https://east:8006","credential":{"kind":"token","token":"t"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/clusters", body)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (missing netWrite)", rec.Code)
	}
}

// TestFederationRoutes_CredentialNeverEchoed is the HTTP-layer half of the
// credential-safety guarantee: attach a cluster with a distinctive token,
// then confirm no response body (create, get, or list) ever contains it.
func TestFederationRoutes_CredentialNeverEchoed(t *testing.T) {
	const token = "root@pve!fed=DISTINCTIVE-SECRET-TOKEN-abc123"
	svc := newFakeFederationService()
	r := newFederationTestRouter(map[string]bool{"netRead": true, "netWrite": true}, svc)

	// Create.
	body := bytes.NewBufferString(`{"name":"east","apiUrl":"https://east:8006","credential":{"kind":"token","token":"` + token + `"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/clusters", body)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), token) {
		t.Fatalf("create response echoed the credential token: %s", rec.Body.String())
	}
	// The service did receive the token (proving it wasn't just dropped).
	if svc.lastCred.Token != token {
		t.Fatalf("service received token %q, want the posted one", svc.lastCred.Token)
	}

	// List.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/federation/clusters", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), token) {
		t.Fatalf("list response echoed the credential token: %s", rec.Body.String())
	}

	// Get by id.
	var id string
	for k := range svc.items {
		id = k
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/federation/clusters/"+id, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), token) {
		t.Fatalf("get response echoed the credential token: %s", rec.Body.String())
	}

	// Delete → 204.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/federation/clusters/"+id, nil)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("delete status = %d, want 204", rec.Code)
	}
}
