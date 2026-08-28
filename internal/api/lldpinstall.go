// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// maxLLDPInstallBodyBytes bounds the POST /lldp/install request body — the
// request is a single boolean confirmation flag, so this ceiling is
// generous headroom against an abusive/buggy client, matching
// maxProtectedBodyBytes' reasoning in protected.go.
const maxLLDPInstallBodyBytes = 1 << 16

// LocalLLDPInstaller is the node-local half of T-605's guided LLDP install
// route (docs/features/lldp-discovery.md §1: "one-click 'install lldpd on
// all nodes' runs through a changeset-like confirmation, executed via peer
// API apt install; audited" — this route is the "changeset-like
// confirmation" coordinator that lldp-discovery.md's peer route doc
// comment says must exist somewhere, per internal/peer/server.go's
// handleInstallLLDPD doc comment: "the caller ... is responsible for
// having obtained that confirmation and for audit-logging the action").
// The onboarding walkthrough's LLDP step (docs/user-guide.md §1.3) is this
// route's only caller today. *host.Real satisfies this directly — the same
// concrete value cmd/vnproxd already wires into internal/peer.ServerOptions
// as LLDPInstaller, reused here rather than opening a second path to it.
type LocalLLDPInstaller interface {
	InstallLLDPD(ctx context.Context) error
}

// PeerLLDPInstaller is the cluster fan-out half: peer discovery plus the
// HMAC-authenticated remote install call. *peer.Client satisfies this
// directly, the same value ClusterPeers/PeerAuditSource/PeerSnapshotSource
// already seam over in clusterfanout.go.
type PeerLLDPInstaller interface {
	ClusterPeers
	InstallLLDPD(ctx context.Context, p peer.Peer, confirm bool) error
}

// lldpInstallAuditor is the minimal audit-log seam this route needs —
// *store.AuditRepo satisfies it directly. This route only ever appends, so
// it declares its own one-method seam rather than depending on the fuller
// AuditService interface audit.go declares for the read route.
type lldpInstallAuditor interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

type lldpInstallRequest struct {
	Confirm bool `json:"confirm"`
}

type lldpInstallNodeResult struct {
	Node  string `json:"node"`
	Error string `json:"error,omitempty"`
	OK    bool   `json:"ok"`
}

type lldpInstallResponse struct {
	Results []lldpInstallNodeResult `json:"results"`
}

// mountLLDPInstallRoutes registers `POST /lldp/install` (additive to
// docs/api.md's original contract; documented there in this same change
// per docs/development.md's definition-of-done #4, the same pattern
// T-302's /lldp/vlan-check addition used). Gated netWrite + CSRF like every
// other mutating route in this package: installing a system package and
// enabling a service is a host mutation, even though it bypasses the
// change engine (docs/features/lldp-discovery.md §1 documents it as its
// own "changeset-like confirmation" flow, not a `change.Op` — there is
// nothing to diff/rollback about installing a monitoring daemon).
//
// local is nil-safe (route not mounted, matching every other mountXRoutes
// function in this package); peers/audit/localNode may be nil/absent
// (single-node cluster, or no audit repo wired in a test) — the route still
// installs locally and simply skips the fan-out/audit step.
func mountLLDPInstallRoutes(r chi.Router, local LocalLLDPInstaller, peers PeerLLDPInstaller, audit lldpInstallAuditor, localNode func() string, auth AuthService) {
	if local == nil || auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/lldp/install", handleLLDPInstall(local, peers, audit, localNode, lookup))
	})
}

func handleLLDPInstall(local LocalLLDPInstaller, peers PeerLLDPInstaller, audit lldpInstallAuditor, localNode func() string, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLLDPInstallBodyBytes))
		dec.DisallowUnknownFields()
		var req lldpInstallRequest
		if err := dec.Decode(&req); err != nil || !req.Confirm {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", `request body must be {"confirm": true}`)
			return
		}

		ctx := r.Context()
		var results []lldpInstallNodeResult

		localName := "local"
		if localNode != nil {
			if n := localNode(); n != "" {
				localName = n
			}
		}
		localErr := local.InstallLLDPD(ctx)
		results = append(results, toLLDPInstallResult(localName, localErr))
		auditLLDPInstall(ctx, audit, username, localName, localErr)

		if peers != nil {
			if peerList, err := peers.Peers(ctx); err == nil {
				for _, p := range peerList {
					err := peers.InstallLLDPD(ctx, p, true)
					results = append(results, toLLDPInstallResult(p.Node, err))
					auditLLDPInstall(ctx, audit, username, p.Node, err)
				}
			}
		}

		writeJSON(w, http.StatusOK, lldpInstallResponse{Results: results})
	}
}

func toLLDPInstallResult(node string, err error) lldpInstallNodeResult {
	if err != nil {
		return lldpInstallNodeResult{Node: node, OK: false, Error: err.Error()}
	}
	return lldpInstallNodeResult{Node: node, OK: true}
}

// auditLLDPInstall appends one audit_log row per node this route attempted
// to install lldpd on (docs/features/lldp-discovery.md §1: "audited").
// audit == nil (no audit repo wired, e.g. a bare test router) simply skips
// logging rather than failing the request — the install action itself
// already happened and must not be masked by a logging failure.
func auditLLDPInstall(ctx context.Context, audit lldpInstallAuditor, username, node string, installErr error) {
	if audit == nil {
		return
	}
	result := "ok"
	detail := ""
	if installErr != nil {
		result = "error"
		detail = installErr.Error()
	}
	var detailJSON string
	if b, err := json.Marshal(map[string]string{"node": node, "detail": detail}); err == nil {
		detailJSON = string(b)
	}
	entry := store.AuditEntry{
		At: time.Now().Unix(), Username: username, Action: "lldp.install", Result: result,
	}
	entry.Target.String, entry.Target.Valid = node, true
	if detailJSON != "" {
		entry.DetailJSON.String, entry.DetailJSON.Valid = detailJSON, true
	}
	_, _ = audit.Append(ctx, entry)
}
