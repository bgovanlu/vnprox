// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// T-3604: POST /services/start — start a stopped SDN service on a node.
//
// dnsmasq is SDN's DHCP server and frr is its EVPN/routing daemon. Either
// being down is a real outage with a one-line remedy that an operator
// previously had to leave vnprox to apply.
//
// This is the most powerful thing Phase 36 adds, and the narrowest version
// of it that is still useful:
//
//   - Only `start`. Not restart, not stop, not enable-at-boot. vnprox does
//     not change a node's boot configuration behind a button that says
//     "start", and it does not stop things at all.
//   - Only internal/host.WatchedServices — dnsmasq and frr. The name is
//     checked here, again on the receiving node, and a third time in the
//     function that builds the argv. The coordinator's check is a courtesy
//     that produces a good error message; the receiving node's is the one
//     that holds if this daemon is compromised or simply a different
//     version.
//   - netWrite. The card asked whether this deserves a capability of its
//     own, on the grounds that "may edit bridges" and "may start daemons"
//     are not obviously the same permission — the T-3403 Automation /
//     AutomationWrite split being the precedent for taking that seriously.
//     The answer here is no, and the reason is the allow-list: the two
//     units are network daemons that are *supposed* to be running, and
//     starting one restores the intended configuration rather than changing
//     it. Starting frr is strictly less invasive than editing a bridge,
//     which netWrite already permits. If the allow-list ever widens beyond
//     "network daemons this cluster is already configured to run", that
//     reasoning expires and the capability question has to be reopened.
const maxServiceStartBodyBytes = 1 << 16

// ServiceStarter is the node-local half — *host.Real satisfies it, the same
// concrete value already wired in as LLDPInstaller.
type ServiceStarter interface {
	StartService(ctx context.Context, unit string) error
}

// PeerServiceStarter is the remote half. *peer.Client satisfies it.
type PeerServiceStarter interface {
	Peers(ctx context.Context) ([]peer.Peer, error)
	StartService(ctx context.Context, p peer.Peer, unit string, confirm bool) error
}

type serviceStartRequest struct {
	Node    string `json:"node"`
	Unit    string `json:"unit"`
	Confirm bool   `json:"confirm"`
}

type serviceStartResponse struct {
	Node string `json:"node"`
	Unit string `json:"unit"`
	OK   bool   `json:"ok"`
}

func mountServiceStartRoutes(r chi.Router, local ServiceStarter, peers PeerServiceStarter, audit collectorRefreshAuditor, localNode func() string, auth AuthService) {
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
		r.Post("/services/start", handleServiceStart(local, peers, audit, localNode, lookup))
	})
}

func handleServiceStart(local ServiceStarter, peers PeerServiceStarter, audit collectorRefreshAuditor, localNode func() string, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxServiceStartBodyBytes))
		dec.DisallowUnknownFields()
		var req serviceStartRequest
		if err := dec.Decode(&req); err != nil || !req.Confirm {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", `request body must be {"node": "...", "unit": "...", "confirm": true}`)
			return
		}
		if req.Node == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "node is required")
			return
		}
		// Refused *and audited*: an attempt to start something outside the
		// allow-list is precisely the event an audit log exists for, and
		// dropping it silently would make the interesting case the
		// invisible one.
		if !host.IsWatchedService(req.Unit) {
			auditServiceStart(r.Context(), audit, username, req.Node, req.Unit, "refused", errors.New("unit not allow-listed"))
			writeJSONError(w, http.StatusBadRequest, "validation_failed",
				"unit is not one of vnprox's watched services (dnsmasq, frr)")
			return
		}

		err := startServiceOnNode(r.Context(), local, peers, localNode, req.Node, req.Unit)
		result := "ok"
		if err != nil {
			result = "error"
		}
		auditServiceStart(r.Context(), audit, username, req.Node, req.Unit, result, err)
		if err != nil {
			// The systemd message reaches the operator verbatim: "Unit
			// frr.service is masked" is actionable in a way that "could not
			// start service" is not.
			writeJSONError(w, http.StatusBadGateway, "service_start_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, serviceStartResponse{Node: req.Node, Unit: req.Unit, OK: true})
	}
}

// startServiceOnNode routes to the local host writer or to the named peer.
// An unknown node is an error rather than a silent no-op — reporting
// success for a node this daemon has never heard of would be the worst
// possible outcome of pressing "start".
func startServiceOnNode(ctx context.Context, local ServiceStarter, peers PeerServiceStarter, localNode func() string, node, unit string) error {
	if localNode != nil && localNode() == node {
		return local.StartService(ctx, unit)
	}
	if peers == nil {
		return errors.New("node " + node + " is not this daemon's node and no peer client is configured")
	}
	list, err := peers.Peers(ctx)
	if err != nil {
		return err
	}
	for _, p := range list {
		if p.Node == node {
			return peers.StartService(ctx, p, unit, true)
		}
	}
	return errors.New("no reachable peer for node " + node)
}

func auditServiceStart(ctx context.Context, audit collectorRefreshAuditor, username, node, unit, result string, startErr error) {
	if audit == nil {
		return
	}
	detail := ""
	if startErr != nil {
		detail = startErr.Error()
	}
	var detailJSON string
	if b, err := json.Marshal(map[string]string{"node": node, "unit": unit, "detail": detail}); err == nil {
		detailJSON = string(b)
	}
	entry := store.AuditEntry{
		At: time.Now().Unix(), Username: username, Action: "service.start", Result: result,
	}
	entry.Target.String, entry.Target.Valid = node+"/"+unit, true
	if detailJSON != "" {
		entry.DetailJSON.String, entry.DetailJSON.Valid = detailJSON, true
	}
	_, _ = audit.Append(ctx, entry)
}
