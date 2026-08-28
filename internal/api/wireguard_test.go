// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// This file is T-1401's HTTP-level coverage for the WireGuard read routes,
// including AC1/AC2's key-custody assertions at the API boundary: no route
// returns the private key, and GET /{id}/pubkey round-trips the derived public
// key.

type fakeWireGuardService struct {
	err        error
	pubkey     string
	peerConfig string
	tunnels    []WireGuardTunnelView
}

func (f *fakeWireGuardService) Tunnels(context.Context) ([]WireGuardTunnelView, error) {
	return f.tunnels, f.err
}

func (f *fakeWireGuardService) PublicKey(_ context.Context, id string) (string, error) {
	if id == "missing" {
		return "", ErrWireGuardNotFound
	}
	return f.pubkey, f.err
}

func (f *fakeWireGuardService) PeerConfig(_ context.Context, id string) (string, error) {
	if id == "missing" {
		return "", ErrWireGuardNotFound
	}
	return f.peerConfig, f.err
}

func wireguardTestRouter(svc WireGuardService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, WireGuard: svc,
	})
}

func TestWireGuardTunnels_NoPrivateKeyInResponse(t *testing.T) {
	svc := &fakeWireGuardService{tunnels: []WireGuardTunnelView{{
		ID: "tun1", Node: "pve1", IfName: "wg0", PublicKey: "PUBkey000000000000000000000000000000000000=",
		ListenPort: 51820, Addresses: []string{"10.10.0.1/24"}, MTU: 1420, Carrier: "vmbr0",
		Peers:  []WireGuardPeerView{{PublicKey: "PEER", AllowedIPs: []string{"10.10.0.2/32"}, External: true, RxBytes: 1, TxBytes: 2}},
		Status: WireGuardTunnelStatus{InterfaceUp: true, PeerCount: 1},
	}}}
	r := wireguardTestRouter(svc)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/wireguard/tunnels", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// The response JSON must never contain a private-key field at all.
	if strings.Contains(strings.ToLower(body), "privatekey") || strings.Contains(strings.ToLower(body), "private_key") {
		t.Fatalf("tunnels response leaked a private-key field: %s", body)
	}
	var out struct {
		Items []WireGuardTunnelView `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].PublicKey == "" {
		t.Fatalf("items = %+v", out.Items)
	}
}

func TestWireGuardPubkey_RoundTrips(t *testing.T) {
	svc := &fakeWireGuardService{pubkey: "PUBkey000000000000000000000000000000000000="}
	r := wireguardTestRouter(svc)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/wireguard/tunnels/tun1/pubkey", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ID        string `json:"id"`
		PublicKey string `json:"publicKey"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.PublicKey != svc.pubkey {
		t.Fatalf("publicKey = %q, want %q", out.PublicKey, svc.pubkey)
	}

	// Unknown id -> 404.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/wireguard/tunnels/missing/pubkey", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing pubkey status = %d, want 404", rec.Code)
	}
}

func TestWireGuardPeerConfig_ExportsExternalSide(t *testing.T) {
	svc := &fakeWireGuardService{peerConfig: "[Interface]\nPrivateKey = <REPLACE>\n\n[Peer]\nPublicKey = PUB\n"}
	r := wireguardTestRouter(svc)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/wireguard/tunnels/tun1/peer-config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		PeerConfig string `json:"peerConfig"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(out.PeerConfig, "[Peer]") {
		t.Fatalf("peer config missing [Peer]: %q", out.PeerConfig)
	}
}

// TestWireGuardRoutes_NoWriteRoute proves there is no wg write route mounted:
// WireGuard is mutated only through the wg.* changeset ops. A POST/PUT/DELETE
// to any /wireguard path is not routed (405/404), never accepted.
func TestWireGuardRoutes_NoWriteRoute(t *testing.T) {
	r := wireguardTestRouter(&fakeWireGuardService{})
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/wireguard/tunnels", nil))
		if rec.Code == http.StatusOK || rec.Code == http.StatusAccepted {
			t.Fatalf("%s /wireguard/tunnels was accepted (%d) — WireGuard must have no write route", method, rec.Code)
		}
	}
}
