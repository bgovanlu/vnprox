// SPDX-License-Identifier: Apache-2.0

// wan.go implements T-1405's WAN & upstream health routes (docs/api.md's
// WAN & upstream health section):
//
//   - GET  /wan/status   — per-uplink current availability/latency/loss plus
//     a dashboard-tile verdict
//   - GET  /wan/targets  — this node's own configured reference targets
//   - PUT  /wan/targets  — replace this node's configured reference targets
//
// GET/PUT /wan/targets operate on the requesting node's own local store,
// with no {node} path param — internal/wan's own package doc comment
// documents why (node-local scope, the same documented gap T-1303/T-1306's
// GET /latmesh/heatmap and GET /mtuprobe/results already carry). Reads are
// netRead-gated; the PUT is netWrite + CSRF, audited wan.targets_update.
// This is the *only* mutating route in this file: GET /wan/status and
// internal/wan's own probe loop never call a mutating route of any kind —
// there is no WAN failover automation here (T-1405 AC5).

package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/wan"
)

// maxWanTargetsBodyBytes bounds PUT /wan/targets' request body — an
// operator-configured target list is at most a handful of hosts per
// uplink, so this ceiling (matching maxProtectedBodyBytes' reasoning) is
// generous headroom against an abusive/buggy client, not a realistic
// limit.
const maxWanTargetsBodyBytes = 1 << 20 // 1 MiB

// maxWanTargetsPerNode caps how many reference targets a single PUT can
// configure — a sane, generous ceiling (an operator names a handful of
// well-known reference hosts, e.g. 1.1.1.1/8.8.8.8/a gateway, never
// hundreds) that also bounds the probe loop's own per-tick work.
const maxWanTargetsPerNode = 100

// WanService is the subset of *wan.Service the router needs.
type WanService interface {
	Status(ctx context.Context, now int64) (wan.Status, error)
	ListTargets(ctx context.Context, node string) ([]wan.Target, error)
	ReplaceTargets(ctx context.Context, node string, targets []wan.Target, now int64) error
}

// wanAuditor is the minimal audit-log seam this route needs — the same
// shape tokenAuditor/lldpInstallAuditor declare for their own routes,
// *store.AuditRepo satisfies it directly.
type wanAuditor interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

// mountWanRoutes registers the routes above. svc/localNode/auth are
// required together (any nil skips mounting every route in this file,
// matching every other optional Options field's degraded-mode
// convention); findingsSvc/audit are independently optional — a nil
// findingsSvc narrows GET /wan/status's verdict (never fails the read, see
// computeWanVerdict's doc comment) and a nil audit (or an auth backend with
// no UsernameLookup) simply skips mounting the PUT route, the same
// reasoning mountProtectedRoutes/mountTokenRoutes already document for
// their own write routes.
func mountWanRoutes(r chi.Router, svc WanService, findingsSvc FindingsService, localNode func() string, audit wanAuditor, auth AuthService) {
	if svc == nil || localNode == nil || auth == nil {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/wan/status", handleWanStatus(svc, findingsSvc, localNode))
		r.Get("/wan/targets", handleGetWanTargets(svc, localNode))
	})

	lookup, ok := auth.(UsernameLookup)
	if audit == nil || !ok {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Put("/wan/targets", handlePutWanTargets(svc, localNode, audit, lookup))
	})
}

// wanTargetResponse is one GET/PUT /wan/targets item.
type wanTargetResponse struct {
	Uplink string `json:"uplink"`
	Host   string `json:"host"`
}

// wanTargetsResponse is GET/PUT /wan/targets' response shape.
type wanTargetsResponse struct {
	Node    string              `json:"node"`
	Targets []wanTargetResponse `json:"targets"`
}

func handleGetWanTargets(svc WanService, localNode func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node := localNode()
		targets, err := svc.ListTargets(r.Context(), node)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read wan targets")
			return
		}
		writeJSON(w, http.StatusOK, wanTargetsResponse{Node: node, Targets: toWanTargetResponses(targets)})
	}
}

// wanTargetsPutRequest is PUT /wan/targets' request body: a full-set
// replace, never a partial patch (mirrors PUT /protected-interfaces'
// same full-replace semantics).
type wanTargetsPutRequest struct {
	Targets []wanTargetResponse `json:"targets"`
}

func handlePutWanTargets(svc WanService, localNode func() string, audit wanAuditor, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxWanTargetsBodyBytes+1))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "could not read request body")
			return
		}
		if len(body) > maxWanTargetsBodyBytes {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "request body too large")
			return
		}
		var req wanTargetsPutRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "invalid JSON body")
			return
		}
		if len(req.Targets) > maxWanTargetsPerNode {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "too many targets")
			return
		}

		targets := make([]wan.Target, 0, len(req.Targets))
		for _, t := range req.Targets {
			uplink := strings.TrimSpace(t.Uplink)
			host := strings.TrimSpace(t.Host)
			if uplink == "" || host == "" {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "uplink and host are both required for every target")
				return
			}
			// T-2905: a WAN target is dialed/pinged by root-owned probers —
			// constrain it to an IP or a plausible DNS name so nothing
			// option-shaped or shell-hostile is ever stored as a target.
			if !validWANHost(host) {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "host must be an IP address or DNS name: "+host)
				return
			}
			targets = append(targets, wan.Target{Uplink: uplink, Host: host})
		}

		node := localNode()
		now := time.Now().Unix()
		if err := svc.ReplaceTargets(r.Context(), node, targets, now); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save wan targets")
			return
		}

		username, _ := lookup.Username(r.Context())
		auditWanTargetsUpdate(r.Context(), audit, username, node, len(targets))

		// Echo back exactly what was just written, without re-reading the
		// store — the same "PUT's response mirrors its own accepted request
		// body" convention handleUpdateAlertRule follows.
		writeJSON(w, http.StatusOK, wanTargetsResponse{Node: node, Targets: toWanTargetResponses(targets)})
	}
}

func toWanTargetResponses(targets []wan.Target) []wanTargetResponse {
	out := make([]wanTargetResponse, len(targets))
	for i, t := range targets {
		out[i] = wanTargetResponse{Uplink: t.Uplink, Host: t.Host}
	}
	return out
}

func auditWanTargetsUpdate(ctx context.Context, audit wanAuditor, username, node string, count int) {
	detail, err := json.Marshal(map[string]any{"node": node, "targetCount": count})
	if err != nil {
		return
	}
	entry := store.AuditEntry{At: time.Now().Unix(), Username: username, Action: "wan.targets_update", Result: "success"}
	entry.Target.String, entry.Target.Valid = node, true
	entry.DetailJSON.String, entry.DetailJSON.Valid = string(detail), true
	_, _ = audit.Append(ctx, entry)
}

// wanTargetStatusResponse is one GET /wan/status target entry.
type wanTargetStatusResponse struct {
	Host           string  `json:"host"`
	At             int64   `json:"at"`
	RttMs          float64 `json:"rttMs"`
	LossPct        float64 `json:"lossPct"`
	RollingRttMs   float64 `json:"rollingRttMs"`
	RollingLossPct float64 `json:"rollingLossPct"`
	Reachable      bool    `json:"reachable"`
}

// wanUplinkStatusResponse is one GET /wan/status uplink entry.
type wanUplinkStatusResponse struct {
	Node            string                    `json:"node"`
	Uplink          string                    `json:"uplink"`
	Status          string                    `json:"status"`
	Targets         []wanTargetStatusResponse `json:"targets"`
	AvailabilityPct float64                   `json:"availabilityPct"`
	RttMs           float64                   `json:"rttMs"`
	LossPct         float64                   `json:"lossPct"`
}

// wanStatusResponse is GET /wan/status's response shape — per-uplink
// status plus the dashboard-tile verdict (docs/api.md's "the dashboard
// tile that says 'it's the ISP, not the cluster'").
type wanStatusResponse struct {
	Verdict     string                    `json:"verdict"`
	Summary     string                    `json:"summary"`
	Uplinks     []wanUplinkStatusResponse `json:"uplinks"`
	GeneratedAt int64                     `json:"generatedAt"`
}

// handleWanStatus takes localNode only to keep its constructor call site
// symmetric with handleGetWanTargets/handlePutWanTargets above (a future
// caller may want it for a "node" field on the response) — GET /wan/status
// is already implicitly scoped to this node, since internal/wan.Service's
// own TargetDiscoverer only ever probes the local node's own configured
// targets (see internal/wan's package doc comment).
func handleWanStatus(svc WanService, findingsSvc FindingsService, _ func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().Unix()
		status, err := svc.Status(r.Context(), now)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read wan status")
			return
		}

		verdict, summary := computeWanVerdict(status, findingsSvc)
		resp := wanStatusResponse{GeneratedAt: status.GeneratedAt, Verdict: verdict, Summary: summary}
		for _, u := range status.Uplinks {
			resp.Uplinks = append(resp.Uplinks, toWanUplinkStatusResponse(u))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func toWanUplinkStatusResponse(u wan.UplinkStatus) wanUplinkStatusResponse {
	out := wanUplinkStatusResponse{
		Node: u.Node, Uplink: u.Uplink, Status: string(u.Status),
		AvailabilityPct: u.AvailabilityPct, RttMs: u.RttMs, LossPct: u.LossPct,
	}
	for _, t := range u.Targets {
		out.Targets = append(out.Targets, wanTargetStatusResponse{
			Host: t.Host, At: t.At, RttMs: t.RttMs, LossPct: t.LossPct,
			RollingRttMs: t.RollingRttMs, RollingLossPct: t.RollingLossPct, Reachable: t.Reachable,
		})
	}
	return out
}

// computeWanVerdict implements docs/api.md's "dashboard tile verdict"
// contract: "likely your ISP" when WAN probes are degraded but internal
// cluster health is otherwise clean. internal/wan itself never imports
// internal/findings (this package's own dependency-direction convention,
// every other findings *producer* stays one-way), so this correlation is
// computed here, the one place both seams are already wired. A nil
// findingsSvc still returns a verdict — just the less specific
// "wan_degraded" rather than "likely_isp", since there is then no signal
// to confirm the rest of the cluster is quiet (never a confident claim
// this package cannot actually back up).
func computeWanVerdict(status wan.Status, findingsSvc FindingsService) (verdict, summary string) {
	if len(status.Uplinks) == 0 {
		return "no_targets", "No WAN reference targets are configured yet."
	}

	degraded := false
	for _, u := range status.Uplinks {
		if u.Status != wan.UplinkHealthy {
			degraded = true
			break
		}
	}
	if !degraded {
		return "healthy", "All configured WAN uplinks are healthy."
	}

	if findingsSvc != nil && otherFindingsQuiet(findingsSvc.Findings()) {
		return "likely_isp", "WAN reference targets are degraded but the rest of the cluster looks healthy — likely your ISP or upstream, not the cluster."
	}
	return "wan_degraded", "One or more WAN uplinks are degraded."
}

// otherFindingsQuiet reports whether every non-wan-sourced finding is
// below warning severity — the "internal cluster health is otherwise
// clean" half of computeWanVerdict's contract.
func otherFindingsQuiet(fs []findings.Finding) bool {
	for _, f := range fs {
		if f.Source == findings.SourceWan {
			continue
		}
		if f.Severity == findings.SeverityWarning || f.Severity == findings.SeverityError {
			return false
		}
	}
	return true
}

// validWANHost accepts an IP literal or an RFC 1123 hostname — and nothing
// else (T-2905): no leading dash, no slashes, no spaces, none of the shapes
// an argv or URL could reinterpret.
func validWANHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for i, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			case r == '-' && i > 0 && i < len(label)-1:
			default:
				return false
			}
		}
	}
	return true
}
