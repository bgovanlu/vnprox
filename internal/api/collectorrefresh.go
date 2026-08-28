// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// T-3603: POST /collectors/refresh — re-run a collector poll now.
//
// The staleness banner used to say "no successful poll yet — context
// canceled" and offer nothing. This is the smallest honest thing to offer
// back: try again, and say what happened.
//
// Phase 36 classes this as an operational action, but the *read-only* kind
// (findings.RemedyOperationalRead): it is vnprox re-reading its own inputs,
// not vnprox writing to a node. Nothing on any host changes, no PVE
// configuration is touched, and there is consequently nothing to stage
// through internal/change and nothing meaningful to confirm — a dialog
// asking "re-read the cluster?" would only teach operators to click through
// the dialogs that do matter.
//
// It is not free, though. Each call costs a full PVE poll cycle (plus a
// host/LLDP poll when the scope covers the local node), so a button an
// operator can lean on becomes a way to hammer PVE. Hence the capability
// gate, the minimum interval below, and the audit row.

// maxCollectorRefreshBodyBytes bounds the request body — an optional node
// name and nothing else. Same generous-ceiling reasoning as
// maxLLDPInstallBodyBytes.
const maxCollectorRefreshBodyBytes = 1 << 16

// collectorRefreshMinInterval is the shortest gap between two accepted
// refreshes. Enforced server-side: a client-side throttle protects PVE only
// from the clients that implement it, and this route is reachable by any
// authenticated session with netWrite, not only by the banner's button.
//
// Ten seconds is chosen against what the button is for, not against a
// theoretical load model: an operator who just fixed a peer's connectivity
// wants to re-check within a few seconds, and nobody has a reason to want
// two full cluster polls inside ten. The regular poll cadence is far longer
// than this, so an accepted refresh never doubles the steady-state load for
// more than one cycle.
const collectorRefreshMinInterval = 10 * time.Second

// CollectorRefresher is the seam this route needs over *collect.Collector,
// declared here for the same reason CollectorHealth is: it keeps this
// package's dependency on internal/collect to one method.
//
// The signature is RefreshNow's, deliberately: an empty scope refreshes
// everything, a node-scoped one refreshes just that node's PVE-visible
// state (plus host/LLDP if it is this daemon's own node). This route does
// NOT offer per-source refresh, because RefreshNow does not decompose that
// way — a "refresh only the lldp source" parameter would be an API that
// lies about what it does.
type CollectorRefresher interface {
	RefreshNow(ctx context.Context, scope inventory.Scope) (inventory.Delta, error)
}

type collectorRefreshRequest struct {
	// Node scopes the refresh. Empty refreshes everything.
	Node string `json:"node,omitempty"`
}

type collectorRefreshResponse struct {
	// Node echoes the requested scope ("" = whole cluster), so a client
	// rendering the result cannot mismatch it against a different banner.
	Node string `json:"node,omitempty"`
	// Error is the poll's own failure text, empty on success. A failed
	// refresh is still a 200: the request was understood and performed, and
	// the interesting answer — "it failed again, with this error" — is the
	// response body, not an HTTP status. Returning 500 here would make a
	// perfectly working route look broken every time a peer is down.
	Error string `json:"error,omitempty"`
	// Changed is true when the poll produced any inventory delta at all.
	// Useful for the "it worked but nothing moved" case, which otherwise
	// looks identical to "nothing happened".
	Changed bool `json:"changed"`
}

// collectorRefreshLimiter enforces collectorRefreshMinInterval across every
// caller of this route, process-wide. Deliberately global rather than
// per-session: the resource being protected is the PVE API, and it does not
// care how many different operators are pressing the button.
type collectorRefreshLimiter struct {
	last time.Time
	mu   sync.Mutex
}

// allow reports whether a refresh may run now, and if not, how long the
// caller must wait. now is injected so the test drives a clock rather than
// sleeping.
func (l *collectorRefreshLimiter) allow(now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.last.IsZero() {
		if wait := collectorRefreshMinInterval - now.Sub(l.last); wait > 0 {
			return false, wait
		}
	}
	l.last = now
	return true, 0
}

// retryAfterSeconds renders a wait as the integer seconds a Retry-After
// header takes, rounding UP so a client that obeys it exactly is never
// refused a second time for being a few hundred milliseconds early.
func retryAfterSeconds(wait time.Duration) string {
	secs := int(wait / time.Second)
	if wait%time.Second != 0 {
		secs++
	}
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}

// collectorRefreshAuditor is the same one-method append seam
// lldpInstallAuditor declares, for the same reason: this route only ever
// appends, so it does not need audit.go's fuller read-capable interface.
type collectorRefreshAuditor interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

// auditCollectorRefresh records every refresh — accepted, failed, whichever.
// A nil auditor (a bare test router) skips logging rather than failing the
// request: the poll already happened, and masking that behind a logging
// failure would make the audit trail *less* accurate, not more.
func auditCollectorRefresh(ctx context.Context, audit collectorRefreshAuditor, username, target, detail string, refreshErr error) {
	if audit == nil {
		return
	}
	result := "ok"
	if refreshErr != nil {
		result = "error"
	}
	var detailJSON string
	if b, err := json.Marshal(map[string]string{"node": target, "detail": detail}); err == nil {
		detailJSON = string(b)
	}
	entry := store.AuditEntry{
		At: time.Now().Unix(), Username: username, Action: "collector.refresh", Result: result,
	}
	entry.Target.String, entry.Target.Valid = target, true
	if detailJSON != "" {
		entry.DetailJSON.String, entry.DetailJSON.Valid = detailJSON, true
	}
	_, _ = audit.Append(ctx, entry)
}

func mountCollectorRefreshRoutes(r chi.Router, refresher CollectorRefresher, audit collectorRefreshAuditor, auth AuthService, now func() time.Time) {
	if refresher == nil || auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}
	if now == nil {
		now = time.Now
	}
	limiter := &collectorRefreshLimiter{}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		// netWrite, even though this writes nothing to any node: the
		// capability that already gates "may cause vnprox to act on the
		// cluster's behalf" is the closest existing fit, and inventing a
		// capability for a re-read would be a worse answer than reusing the
		// one operators already reason about.
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/collectors/refresh", handleCollectorRefresh(refresher, audit, lookup, limiter, now))
	})
}

func handleCollectorRefresh(
	refresher CollectorRefresher,
	audit collectorRefreshAuditor,
	lookup UsernameLookup,
	limiter *collectorRefreshLimiter,
	now func() time.Time,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		var req collectorRefreshRequest
		// An empty body is valid and means "refresh everything" — the
		// common case from the cluster-wide staleness banner. Only a body
		// that is present and malformed is an error.
		if r.Body != nil && r.ContentLength != 0 {
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCollectorRefreshBodyBytes))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", `request body must be {} or {"node": "<name>"}`)
				return
			}
		}

		if allowed, wait := limiter.allow(now()); !allowed {
			w.Header().Set("Retry-After", retryAfterSeconds(wait))
			writeJSONError(w, http.StatusTooManyRequests, "rate_limited",
				"a collector refresh ran less than "+collectorRefreshMinInterval.String()+" ago; try again shortly")
			return
		}

		delta, err := refresher.RefreshNow(r.Context(), inventory.Scope{Node: req.Node})

		resp := collectorRefreshResponse{Node: req.Node, Changed: !delta.Empty()}
		detail := "refreshed"
		if err != nil {
			// Reported, not swallowed. "It failed again, and here is the
			// same error as before" is a genuinely useful answer — it tells
			// the operator the problem is not transient, which is exactly
			// what the banner could not say before.
			resp.Error = err.Error()
			detail = "refresh failed: " + err.Error()
		}
		target := req.Node
		if target == "" {
			target = "cluster"
		}
		auditCollectorRefresh(r.Context(), audit, username, target, detail, err)
		writeJSON(w, http.StatusOK, resp)
	}
}
