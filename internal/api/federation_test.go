package api

import (
	"bytes"
	"context"
	"encoding/json"
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

func (f *fakeFederationService) Update(_ context.Context, id, name, apiURL string, cred *federation.Credential, wgTunnelID *string) (federation.Cluster, error) {
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
	if wgTunnelID != nil {
		c.WgTunnelID = *wgTunnelID
		c.WgTunnelSource = ""
		if c.WgTunnelID != "" {
			c.WgTunnelSource = federation.TunnelLinkExplicit
		}
	}
	f.items[id] = c
	return c, nil
}

// seedPeerLinked injects a cluster whose tunnel linkage was derived from a
// WireGuard peer's cluster_id annotation rather than set through this route —
// what the real federation.Service returns once resolveLinkage has run.
func (f *fakeFederationService) seedPeerLinked(id, tunnelID string) {
	f.items[id] = federation.Cluster{
		ID: id, Name: "east", APIURL: "https://east:8006", Status: "ok", AddedBy: "admin@pam",
		AddedAt: 1700000000, WgTunnelID: tunnelID, WgTunnelSource: federation.TunnelLinkPeer,
	}
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

// TestFederationRoutes_UpdateWgTunnelID is T-1407: PUT can set and clear the
// wgTunnelId linkage independently of name/apiUrl/credential, following the
// same "absent leaves unchanged, explicit value replaces" convention
// credential already uses.
func TestFederationRoutes_UpdateWgTunnelID(t *testing.T) {
	svc := newFakeFederationService()
	r := newFederationTestRouter(map[string]bool{"netRead": true, "netWrite": true}, svc)

	body := bytes.NewBufferString(`{"name":"east","apiUrl":"https://east:8006","credential":{"kind":"token","token":"t"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/clusters", body)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created federationClusterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}
	if created.WgTunnelID != "" {
		t.Fatalf("newly-created cluster wgTunnelId = %q, want empty", created.WgTunnelID)
	}

	// Set the linkage.
	body = bytes.NewBufferString(`{"wgTunnelId":"tun-1"}`)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/federation/clusters/"+created.ID, body)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var updated federationClusterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decoding update response: %v", err)
	}
	if updated.WgTunnelID != "tun-1" {
		t.Fatalf("wgTunnelId = %q, want tun-1", updated.WgTunnelID)
	}
	if updated.Name != "east" {
		t.Fatalf("name = %q, want unchanged (east) — setting wgTunnelId must not disturb other fields", updated.Name)
	}

	// A PUT that omits wgTunnelId entirely leaves it untouched.
	body = bytes.NewBufferString(`{"name":"east-renamed"}`)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/federation/clusters/"+created.ID, body)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decoding rename response: %v", err)
	}
	if updated.WgTunnelID != "tun-1" {
		t.Fatalf("wgTunnelId after unrelated rename = %q, want unchanged tun-1", updated.WgTunnelID)
	}

	// An explicit empty string clears it.
	body = bytes.NewBufferString(`{"wgTunnelId":""}`)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/federation/clusters/"+created.ID, body)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// omitempty means a cleared "" is absent from the JSON body entirely —
	// asserted directly on the raw body rather than by decoding into
	// `updated` again, which would otherwise keep its previous "tun-1" value
	// for a key the response no longer sends (json.Unmarshal only touches
	// keys present in the payload).
	if strings.Contains(rec.Body.String(), "wgTunnelId") {
		t.Fatalf("clear response still mentions wgTunnelId (want the key omitted entirely): %s", rec.Body.String())
	}
	if svc.items[created.ID].WgTunnelID != "" {
		t.Fatalf("stored wgTunnelId after explicit clear = %q, want empty", svc.items[created.ID].WgTunnelID)
	}
}

// TestFederationRoutes_WgTunnelSource: the read surface distinguishes an
// operator-set linkage from one derived off a WireGuard peer's cluster_id
// annotation, so a UI can show *why* a cluster is tunnel-linked (and that
// clearing wgTunnelId won't unlink a peer-derived one). GET and PUT report the
// same field; PUT always writes the explicit override.
func TestFederationRoutes_WgTunnelSource(t *testing.T) {
	svc := newFakeFederationService()
	svc.seedPeerLinked("cl-peer", "tun-from-peer")
	r := newFederationTestRouter(map[string]bool{"netRead": true, "netWrite": true}, svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/clusters/cl-peer", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got federationClusterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding get response: %v", err)
	}
	if got.WgTunnelID != "tun-from-peer" || got.WgTunnelSource != federation.TunnelLinkPeer {
		t.Fatalf("GET linkage = (%q, %q), want (tun-from-peer, %s)", got.WgTunnelID, got.WgTunnelSource, federation.TunnelLinkPeer)
	}

	// Overriding it through PUT flips the reported source to "explicit".
	body := bytes.NewBufferString(`{"wgTunnelId":"tun-override"}`)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/federation/clusters/cl-peer", body)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding update response: %v", err)
	}
	if got.WgTunnelID != "tun-override" || got.WgTunnelSource != federation.TunnelLinkExplicit {
		t.Fatalf("PUT linkage = (%q, %q), want (tun-override, %s)", got.WgTunnelID, got.WgTunnelSource, federation.TunnelLinkExplicit)
	}

	// An unlinked cluster reports neither field (both are omitempty).
	req = httptest.NewRequest(http.MethodGet, "/api/v1/federation/clusters", nil)
	rec = httptest.NewRecorder()
	svc.seedPeerLinked("cl-peer", "")
	svc.items["cl-peer"] = federation.Cluster{ID: "cl-peer", Name: "east"}
	r.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "wgTunnelSource") {
		t.Errorf("unlinked cluster leaked wgTunnelSource: %s", rec.Body.String())
	}
}
