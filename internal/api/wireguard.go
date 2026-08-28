// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// WireGuardService is the subset of the daemon's WireGuard read surface the
// router needs for docs/api.md's WireGuard section (T-1401). Every route here
// is read-only and netRead-gated — WireGuard is mutated exclusively through
// the wg.* changeset op family (the change engine), never a dedicated write
// route (CLAUDE.md's single-mutation-path invariant). No method returns a
// private key: only the derived public key and exportable peer config leave
// the daemon.
type WireGuardService interface {
	// Tunnels returns every tunnel's app-owned config merged with its live,
	// authoritative on-node status (a wg-show-dump-equivalent poll), mirroring
	// GET /sdn's running-vs-config-truth pattern.
	Tunnels(ctx context.Context) ([]WireGuardTunnelView, error)
	// PublicKey returns the tunnel's derived public key only. ErrWireGuardNotFound
	// for an unknown id.
	PublicKey(ctx context.Context, id string) (string, error)
	// PeerConfig returns the exportable wg-quick config an external peer would
	// install on its own side. ErrWireGuardNotFound for an unknown id.
	PeerConfig(ctx context.Context, id string) (string, error)
}

// ErrWireGuardNotFound is returned by WireGuardService for an unknown tunnel
// id, mapped to 404 by the handlers.
var ErrWireGuardNotFound = errors.New("wireguard tunnel not found")

// WireGuardTunnelView is one GET /wireguard/tunnels item — the app-owned
// config plus live status. It never carries a private key.
type WireGuardTunnelView struct {
	ID         string                `json:"id"`
	Node       string                `json:"node"`
	IfName     string                `json:"ifName"`
	PublicKey  string                `json:"publicKey"`
	Carrier    string                `json:"carrier,omitempty"`
	Addresses  []string              `json:"addresses"`
	Peers      []WireGuardPeerView   `json:"peers"`
	Status     WireGuardTunnelStatus `json:"status"`
	ListenPort int                   `json:"listenPort"`
	MTU        int                   `json:"mtu"`
}

// WireGuardPeerView is one peer's config + live status within a tunnel view.
type WireGuardPeerView struct {
	PublicKey         string   `json:"publicKey"`
	Endpoint          string   `json:"endpoint,omitempty"`
	ObservedEndpoint  string   `json:"observedEndpoint,omitempty"`
	AllowedIPs        []string `json:"allowedIps"`
	KeepaliveSec      int      `json:"keepaliveSec,omitempty"`
	LastHandshakeUnix int64    `json:"lastHandshakeUnix,omitempty"`
	RxBytes           int64    `json:"rxBytes"`
	TxBytes           int64    `json:"txBytes"`
	External          bool     `json:"external"`
	EndpointDrifted   bool     `json:"endpointDrifted"`
}

// WireGuardTunnelStatus is a tunnel's live interface presence.
type WireGuardTunnelStatus struct {
	InterfaceUp bool `json:"interfaceUp"`
	PeerCount   int  `json:"peerCount"`
}

// mountWireGuardRoutes registers docs/api.md's WireGuard section (T-1401):
// netRead-gated read-only routes. Every WireGuard mutation goes through the
// wg.* changeset op family, never a route here. Nil svc/auth skips mounting,
// matching every other optional Options field.
func mountWireGuardRoutes(r chi.Router, svc WireGuardService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/wireguard/tunnels", handleWireGuardTunnels(svc))
		r.Get("/wireguard/tunnels/{id}/pubkey", handleWireGuardPubkey(svc))
		r.Get("/wireguard/tunnels/{id}/peer-config", handleWireGuardPeerConfig(svc))
	})
}

func handleWireGuardTunnels(svc WireGuardService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tunnels, err := svc.Tunnels(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read WireGuard tunnels")
			return
		}
		if tunnels == nil {
			tunnels = []WireGuardTunnelView{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": tunnels})
	}
}

func handleWireGuardPubkey(svc WireGuardService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		pub, err := svc.PublicKey(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrWireGuardNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such WireGuard tunnel")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read WireGuard tunnel public key")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "publicKey": pub})
	}
}

func handleWireGuardPeerConfig(svc WireGuardService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		cfg, err := svc.PeerConfig(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrWireGuardNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such WireGuard tunnel")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not build WireGuard peer config")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "peerConfig": cfg})
	}
}
