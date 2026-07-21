package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/change/ifaces"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// capNetWrite is docs/api.md's documented write-capability flag name
// (internal/auth.CapNetWrite's underlying string), spelled out as a plain
// string for the same reason topology.go's capNetRead is (see that
// constant's doc comment): keeping this package's auth dependency to the
// AuthService method-seam interface.
const capNetWrite = "netWrite"

// maxChangesetBodyBytes bounds a draft create/update request body. A
// changeset with, say, a hundred ops is at most a few tens of KB even with
// verbose params (fw rule lists, SDN objects); this ceiling is generous
// headroom against an abusive/buggy client, not a realistic limit.
const maxChangesetBodyBytes = 4 << 20 // 4 MiB

// ChangesetService is the subset of *change.Service the router needs: T-201's
// draft CRUD, T-202's Validate, and T-205's diff/apply/confirm/rollback.
// Declared as an interface (the same seam pattern as AuthService/
// TopologyService/LayoutStore above) so this package's dependency on the
// concrete change.Service stays small and testable without a real SQLite
// file.
type ChangesetService interface {
	List(ctx context.Context, status string) ([]change.Changeset, error)
	Get(ctx context.Context, id string) (change.Changeset, error)
	Create(ctx context.Context, author, title string, ops []change.Op) (change.Changeset, error)
	UpdateDraft(ctx context.Context, id, author string, title *string, ops []change.Op) (change.Changeset, error)
	Discard(ctx context.Context, id, author string) error
	Validate(ctx context.Context, id, author string) (change.Changeset, error)

	// T-1703 request-changesets: CreateRequest creates a changeset in
	// StatusRequested (a tenant member's request, blocked from apply until an
	// approver converts it); Approve converts a requested changeset to an
	// ordinary draft.
	CreateRequest(ctx context.Context, author, title string, ops []change.Op) (change.Changeset, error)
	Approve(ctx context.Context, id, approver string) (change.Changeset, error)

	// T-205 apply engine.
	Diff(ctx context.Context, id string) (*ifaces.ChangesetDiff, error)
	Apply(ctx context.Context, id, author string, pveGW change.PVEGateway, confirmTimeout time.Duration) (change.Changeset, error)
	Confirm(ctx context.Context, id, author string) (change.Changeset, error)
	// Rollback's pveGW (T-402) carries the requesting user's own PVE
	// ticket, needed only when the changeset being rolled back has an SDN
	// portion — see change.Service.Rollback's doc comment.
	Rollback(ctx context.Context, id, author string, pveGW change.PVEGateway) (change.Changeset, error)

	// T-208 raw editor: the current live file + hash the editor opens
	// against (see the raw-editor routes mounted alongside the changeset
	// CRUD routes below).
	ReadRawInterfaces(ctx context.Context, node string) (content, hash string, err error)
}

// PVEGatewayProvider supplies a change.PVEGateway bound to the requesting
// session's own PVE ticket (docs/architecture.md §6: writes use the user's
// ticket). cmd/vnproxd wires it from auth.Service.PVEClientFor; a nil provider
// (or one returning ok=false) means cluster-scope PVE steps (sdn.apply) can't
// run for this request — apply of a changeset needing them then fails clearly.
type PVEGatewayProvider interface {
	GatewayFor(ctx context.Context) (change.PVEGateway, bool)
}

// CSRFEnforcer is implemented by AuthService backends that can check the
// double-submit CSRF header (internal/auth.Service.CSRFMiddleware, per
// docs/api.md's conventions section: "X-VNPROX-CSRF header on mutating
// requests"). It is checked with a type assertion — the same pattern
// UsernameLookup uses just above in layouts.go — rather than folded into
// the AuthService interface itself, so existing AuthService test doubles
// that don't need CSRF behavior (this package's own fakeAuth) don't have
// to grow a method just because the changesets routes need one. If auth
// doesn't implement this, the mutating changesets routes still mount
// (unlike the UsernameLookup case, where there'd be no safe author to
// record at all) but skip CSRF enforcement — acceptable only for test
// doubles; cmd/vnproxd's real authServiceAdapter always implements it via
// the embedded *auth.Service.
type CSRFEnforcer interface {
	CSRFMiddleware(next http.Handler) http.Handler
}

// changesetResponse is the wire shape of a changeset, per docs/api.md's
// changesets section ("GET /changesets/{id} — full changeset incl.
// findings, plan, apply log"). Findings is never emitted as a JSON null
// (an empty array instead) so frontend code can always range over it
// without a nil check.
type changesetResponse struct {
	Plan            json.RawMessage  `json:"plan,omitempty"`
	ApplyLog        json.RawMessage  `json:"applyLog,omitempty"`
	ConfirmDeadline *int64           `json:"confirmDeadline,omitempty"`
	ID              string           `json:"id"`
	Title           string           `json:"title"`
	Author          string           `json:"author"`
	Status          string           `json:"status"`
	Ops             []change.Op      `json:"ops"`
	Findings        []change.Finding `json:"findings"`
	CreatedAt       int64            `json:"createdAt"`
	UpdatedAt       int64            `json:"updatedAt"`
	// TouchesMgmtPath is T-703's server-computed flag (docs/api.md's
	// changesets section): the ops intersect a node's resolved management
	// path (change.TouchesMgmtPath over the same MgmtStatus computation
	// GET /protected-interfaces/status answers from). Decorated onto the
	// response by the changesets routes' handlers (see mgmtPathsFor);
	// auxiliary draft-creating routes (drift/findings fix, snapshot
	// restore, blueprint instantiate) return it as false — the canonical
	// GET /changesets/{id} read those flows all funnel into computes it.
	TouchesMgmtPath bool `json:"touchesMgmtPath"`
}

func toChangesetResponse(c change.Changeset) changesetResponse {
	ops := redactOpSecrets(c.Ops)
	if ops == nil {
		ops = []change.Op{}
	}
	findings := c.Findings
	if findings == nil {
		findings = []change.Finding{}
	}
	return changesetResponse{
		ID: c.ID, Title: c.Title, Author: c.Author, Status: string(c.Status),
		Ops: ops, Findings: findings, Plan: c.Plan, ApplyLog: c.ApplyLog,
		ConfirmDeadline: c.ConfirmDeadline, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// redactOpSecrets returns ops with every wg.peer.add op's preshared key
// stripped — both the write-only plaintext PresharedKey and the sealed-at-rest
// PresharedKeyEnc — so no changeset read response (GET /changesets, and every
// route that echoes a changeset) ever carries a peer secret in any form
// (Finding 1 / docs/security.md's WireGuard credential-storage note). The
// stored ops (and the sealed bytes persisted in changesets.ops_json, which the
// apply path reads) are untouched: this allocates a shallow copy only when it
// finds something to redact and never mutates the caller's backing array.
func redactOpSecrets(ops []change.Op) []change.Op {
	var out []change.Op
	for i, op := range ops {
		p, ok := op.Params.(*change.WgPeerAddParams)
		if !ok || (p.PresharedKey == "" && len(p.PresharedKeyEnc) == 0) {
			continue
		}
		if out == nil {
			out = make([]change.Op, len(ops))
			copy(out, ops)
		}
		clone := *p
		clone.PresharedKey = ""
		clone.PresharedKeyEnc = nil
		out[i].Params = &clone
	}
	if out != nil {
		return out
	}
	return ops
}

// mountChangesetsRoutes registers docs/api.md's changesets routes: the
// T-201 draft CRUD (list/create/get/update-draft/delete-draft), T-202's
// validate, and T-205's diff/apply/confirm/rollback — all backed by real
// service logic. Read routes require netRead; every mutating route requires
// netWrite plus (when the auth backend supports it — see CSRFEnforcer) a
// valid CSRF header.
//
// svc and auth are nil-safe to call with (routes simply aren't mounted),
// matching mountTopologyRoutes/mountLayoutsRoutes' pattern. If auth
// doesn't also implement UsernameLookup, the routes are likewise not
// mounted — same reasoning as mountLayoutsRoutes: there would be no safe
// way to attribute a created/discarded changeset to a user for the
// audit trail docs/security.md requires.
func mountChangesetsRoutes(r chi.Router, svc ChangesetService, auth AuthService, gateways PVEGatewayProvider, mgmt MgmtStatusService, wgCarriers change.WgCarrierSource, scoper TenantScoper, notifier ApprovalNotifier, adminStore TenantAdminStore) {
	if svc == nil || auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/changesets", handleListChangesets(svc, mgmt, wgCarriers))
		r.Get("/changesets/{id}", handleGetChangeset(svc, mgmt, wgCarriers))
		r.Get("/changesets/{id}/diff", handleDiffChangeset(svc))

		// T-208 raw editor: the "open" call and its live syntax-lint
		// round trip. Neither mutates server state (the lint endpoint
		// only parses a client-supplied string; it does not even name a
		// node), so both live in the netRead group with no CSRF
		// requirement, alongside every other read route above.
		r.Get("/nodes/{node}/interfaces/raw", handleGetRawInterfaces(svc))
		r.Post("/interfaces/lint", handleLintInterfaces())
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/changesets", handleCreateChangeset(svc, lookup, mgmt, wgCarriers, scoper, notifier, adminStore))
		r.Put("/changesets/{id}", handleUpdateChangeset(svc, lookup, mgmt, wgCarriers))
		r.Delete("/changesets/{id}", handleDiscardChangeset(svc, lookup))
		r.Post("/changesets/{id}/validate", handleValidateChangeset(svc, lookup, mgmt, wgCarriers))
		r.Post("/changesets/{id}/apply", handleApplyChangeset(svc, lookup, gateways, mgmt, wgCarriers))
		r.Post("/changesets/{id}/confirm", handleConfirmChangeset(svc, lookup, mgmt, wgCarriers))
		r.Post("/changesets/{id}/rollback", handleRollbackChangeset(svc, lookup, gateways, mgmt, wgCarriers))
	})

	// T-1103: scheduled changesets & maintenance windows. Mounted alongside
	// (not inside) the two route groups above since the ack route
	// deliberately carries no session/CSRF requirement — see
	// mountScheduleRoutes' own doc comment.
	mountScheduleRoutes(r, svc, auth, lookup)
}

// mgmtPathsFor computes the resolved management-path set the
// touchesMgmtPath response flag is evaluated against, once per request
// (handlers reuse the returned map across a whole changeset list). Nil-safe
// and degrade-quietly: a nil seam (tests, degraded wiring) or a failed
// MgmtStatus read yields nil, so every flag computes false rather than the
// route erroring — the same tolerance the topology badge painting applies
// to the identical computation (topology.go's paintMgmtStatus).
func mgmtPathsFor(ctx context.Context, mgmt MgmtStatusService) map[string][]topology.MgmtPath {
	if mgmt == nil {
		return nil
	}
	status, err := mgmt.MgmtStatus(ctx)
	if err != nil {
		return nil
	}
	return status.Nodes
}

// wgCarriersFor resolves the tunnelID->carrier map TouchesMgmtPath needs to
// flag carrier-less wg ops (wg.peer.*, wg.tunnel.delete, carrier-less
// wg.tunnel.update) on an existing management-path tunnel — supplied by the
// WireGuard read service (change.WgCarrierSource). Nil-safe and
// degrade-quietly, exactly like mgmtPathsFor: a nil seam or a failed read
// yields nil, so those ops simply fall back to params-only coverage rather
// than erroring the route.
func wgCarriersFor(ctx context.Context, src change.WgCarrierSource) map[string]change.WgTunnelCarrier {
	if src == nil {
		return nil
	}
	carriers, err := src.TunnelCarriers(ctx)
	if err != nil {
		return nil
	}
	return carriers
}

// mgmtEval resolves both inputs TouchesMgmtPath needs — the management paths
// and the tunnel-carrier map — once per request, for handlers that decorate a
// whole changeset list.
func mgmtEval(ctx context.Context, mgmt MgmtStatusService, wgCarriers change.WgCarrierSource) (map[string][]topology.MgmtPath, map[string]change.WgTunnelCarrier) {
	return mgmtPathsFor(ctx, mgmt), wgCarriersFor(ctx, wgCarriers)
}

// withMgmtFlag decorates a changesetResponse with the touchesMgmtPath flag.
func withMgmtFlag(c change.Changeset, paths map[string][]topology.MgmtPath, carriers map[string]change.WgTunnelCarrier) changesetResponse {
	resp := toChangesetResponse(c)
	resp.TouchesMgmtPath = change.TouchesMgmtPath(paths, carriers, nil, c.Ops)
	return resp
}

// MgmtAckRecorder is the optional ChangesetService extension backing
// T-703's acknowledgement audit trail (change.Service.RecordMgmtAck).
// Checked with a type assertion, exactly like CSRFEnforcer above, so test
// doubles that don't care about the ceremony don't have to grow a method.
type MgmtAckRecorder interface {
	RecordMgmtAck(ctx context.Context, id, author, node string)
}

// applyRequest is docs/api.md's POST /changesets/{id}/apply body:
// `{confirmTimeoutSec: 120, mgmtAck?: {node}}`. MgmtAck (T-703) is the
// review screen's typed management-path acknowledgement, recorded to the
// audit log when the changeset touches a management path.
type applyRequest struct {
	MgmtAck           *mgmtAckRequest `json:"mgmtAck,omitempty"`
	ConfirmTimeoutSec int             `json:"confirmTimeoutSec"`
}

type mgmtAckRequest struct {
	Node string `json:"node"`
}

func handleApplyChangeset(svc ChangesetService, lookup UsernameLookup, gateways PVEGatewayProvider, mgmt MgmtStatusService, wgCarriers change.WgCarrierSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		var req applyRequest
		if r.ContentLength != 0 {
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
				return
			}
		}

		var gw change.PVEGateway
		if gateways != nil {
			gw, _ = gateways.GatewayFor(r.Context())
		}

		// Pre-apply checks that need the changeset's ops. Best-effort: if
		// the changeset can't be loaded, fall through to svc.Apply, which
		// reports the real error (not found / illegal transition) itself.
		paths, carriers := mgmtEval(r.Context(), mgmt, wgCarriers)
		touchesMgmt := false
		if cs, getErr := svc.Get(r.Context(), id); getErr == nil {
			touchesMgmt = change.TouchesMgmtPath(paths, carriers, nil, cs.Ops)

			// T-701 acceptance criterion 5: fail fast, before any snapshot/
			// mutation, when the plan needs a PVEGateway (sdn/fw/ipam
			// steps) and none resolved — previously this discarded
			// GatewayFor's failure and let apply proceed, dying mid-apply
			// at apply_exec.go's "no PVE gateway available ... (no user
			// session)" with a failed changeset whose only user-visible
			// explanation was that low-level string (T-701 root-cause
			// analysis §5).
			if gw == nil {
				if plan, buildErr := change.BuildPlan(cs.Ops); buildErr == nil && planRequiresPVEGateway(plan) {
					writeJSONError(w, http.StatusUnprocessableEntity, "pve_session_required",
						"this changeset has sdn/firewall/ipam steps that require a live PVE session, but none is available — log in again and retry")
					return
				}
			}
		}

		confirmTimeout := time.Duration(req.ConfirmTimeoutSec) * time.Second
		if touchesMgmt {
			// T-703's confirm-window floor: a management-path changeset's
			// commit-confirm window defaults to, and can never be set
			// below, change.MgmtConfirmTimeoutFloor — the rollback timer is
			// the only safety net if this change severs connectivity, so
			// it must not be shortened in the same breath as arming it.
			floor := change.MgmtConfirmTimeoutFloor
			switch {
			case confirmTimeout == 0:
				confirmTimeout = floor
			case confirmTimeout < floor:
				writeJSONError(w, http.StatusBadRequest, "confirm_window_too_short",
					fmt.Sprintf("this changeset touches a management path; its confirm window cannot be below %d seconds", int(floor.Seconds())))
				return
			}
			// The review screen's typed acknowledgement, audited (T-703:
			// "an audit entry recording the acknowledgement").
			if req.MgmtAck != nil && req.MgmtAck.Node != "" {
				if recorder, ok := svc.(MgmtAckRecorder); ok {
					recorder.RecordMgmtAck(r.Context(), id, username, req.MgmtAck.Node)
				}
			}
		}

		c, err := svc.Apply(r.Context(), id, username, gw, confirmTimeout)
		if err != nil {
			writeApplyError(w, err)
			return
		}
		resp := toChangesetResponse(c)
		resp.TouchesMgmtPath = touchesMgmt
		writeJSON(w, http.StatusAccepted, resp)
	}
}

func handleConfirmChangeset(svc ChangesetService, lookup UsernameLookup, mgmt MgmtStatusService, wgCarriers change.WgCarrierSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		c, err := svc.Confirm(r.Context(), chi.URLParam(r, "id"), username)
		if err != nil {
			writeApplyError(w, err)
			return
		}
		paths, carriers := mgmtEval(r.Context(), mgmt, wgCarriers)
		writeJSON(w, http.StatusOK, withMgmtFlag(c, paths, carriers))
	}
}

func handleRollbackChangeset(svc ChangesetService, lookup UsernameLookup, gateways PVEGatewayProvider, mgmt MgmtStatusService, wgCarriers change.WgCarrierSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var gw change.PVEGateway
		if gateways != nil {
			gw, _ = gateways.GatewayFor(r.Context())
		}
		c, err := svc.Rollback(r.Context(), chi.URLParam(r, "id"), username, gw)
		if err != nil {
			writeApplyError(w, err)
			return
		}
		paths, carriers := mgmtEval(r.Context(), mgmt, wgCarriers)
		writeJSON(w, http.StatusOK, withMgmtFlag(c, paths, carriers))
	}
}

func handleDiffChangeset(svc ChangesetService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		diff, err := svc.Diff(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeApplyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, diff)
	}
}

// rawInterfacesResponse is `GET /nodes/{node}/interfaces/raw`'s body: the
// raw Monaco editor's "open" call (T-208). SHA256 is the conflict-guard
// baseline the editor stamps into its eventual iface.raw.replace op's
// baseHash param.
type rawInterfacesResponse struct {
	Node    string `json:"node"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}

func handleGetRawInterfaces(svc ChangesetService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node := chi.URLParam(r, "node")
		content, hash, err := svc.ReadRawInterfaces(r.Context(), node)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "peer_unreachable",
				fmt.Sprintf("could not read /etc/network/interfaces on node %s", node))
			return
		}
		writeJSON(w, http.StatusOK, rawInterfacesResponse{Node: node, Content: content, SHA256: hash})
	}
}

// lintInterfacesRequest is `POST /interfaces/lint`'s body: `{content}` —
// deliberately node-less, since this is a pure interfaces(5) syntax check
// (T-208 AC1's "syntax errors underline with line-precise messages as you
// type"), not a validation of any particular node's state.
type lintInterfacesRequest struct {
	Content string `json:"content"`
}

// lintInterfacesResponse is `POST /interfaces/lint`'s body: `{errors}`, one
// entry per host.ParseError the T-102 parser reports (today: at most one,
// since that parser stops at the first syntax error — see
// change.LintRawInterfaces's doc comment).
type lintInterfacesResponse struct {
	Errors []change.LintMarker `json:"errors"`
}

func handleLintInterfaces() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChangesetBodyBytes))
		dec.DisallowUnknownFields()
		var req lintInterfacesRequest
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
			return
		}
		markers := change.LintRawInterfaces(req.Content)
		if markers == nil {
			markers = []change.LintMarker{}
		}
		writeJSON(w, http.StatusOK, lintInterfacesResponse{Errors: markers})
	}
}

// planRequiresPVEGateway reports whether plan carries any step that needs a
// live change.PVEGateway to execute — the cluster-scope SDN steps
// (sdn.zone/vnet/subnet.* realized via StepSDNStage, the trailing
// sdn.apply via StepSDNApply), the firewall steps (StepFwApply/
// StepFwVerify), and IPAM allocation steps (StepIpamAlloc) — mirroring
// apply_exec.go's own "if e.pveGW == nil" guards on exactly those step
// kinds (T-701's fail-fast pre-check for handleApplyChangeset).
func planRequiresPVEGateway(plan change.Plan) bool {
	for _, st := range plan.Steps {
		switch st.Kind {
		case change.StepSDNStage, change.StepSDNApply, change.StepFwApply, change.StepFwVerify, change.StepIpamAlloc:
			return true
		}
	}
	return false
}

// writeApplyError maps T-205 apply-engine errors to docs/api.md's error
// envelope + stable codes.
func writeApplyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not_found", "no such changeset")
		return
	}

	var locked *change.ErrChangesetLocked
	if errors.As(err, &locked) {
		writeJSONError(w, http.StatusConflict, "changeset_locked", err.Error())
		return
	}
	var blocked *change.ErrValidationBlocked
	if errors.As(err, &blocked) {
		writeJSONErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed",
			"changeset has blocking validation errors", map[string]any{"findings": blocked.Findings})
		return
	}
	var unsupported *change.ErrUnsupportedOp
	if errors.As(err, &unsupported) {
		writeJSONError(w, http.StatusUnprocessableEntity, "unsupported_op", err.Error())
		return
	}
	var sdnUnhealthy *change.ErrSDNZoneUnhealthy
	if errors.As(err, &sdnUnhealthy) {
		writeJSONErrorDetails(w, http.StatusUnprocessableEntity, "sdn_zone_unhealthy", err.Error(), map[string]any{
			"zone": sdnUnhealthy.Zone, "node": sdnUnhealthy.Node, "status": sdnUnhealthy.Status,
			"detail": sdnUnhealthy.Detail, "taskUpid": sdnUnhealthy.UPID, "taskNode": sdnUnhealthy.TaskNode,
		})
		return
	}
	var restoreUnsupported *change.ErrRestoreUnsupported
	if errors.As(err, &restoreUnsupported) {
		writeJSONError(w, http.StatusUnprocessableEntity, "unsupported_op", err.Error())
		return
	}
	var incompatiblePeer *change.ErrIncompatiblePeer
	if errors.As(err, &incompatiblePeer) {
		writeJSONErrorDetails(w, http.StatusConflict, "peer_incompatible", err.Error(), map[string]any{"node": incompatiblePeer.Node})
		return
	}
	var notConfirmable *change.ErrNotConfirmable
	if errors.As(err, &notConfirmable) {
		writeJSONError(w, http.StatusConflict, "invalid_transition", err.Error())
		return
	}
	var windowExpired *change.ErrRollbackWindowExpired
	if errors.As(err, &windowExpired) {
		writeJSONError(w, http.StatusConflict, "rollback_window_expired", err.Error())
		return
	}
	var illegal *change.ErrIllegalTransition
	if errors.As(err, &illegal) {
		writeJSONError(w, http.StatusConflict, "invalid_transition", err.Error())
		return
	}
	var notConfigured *change.ErrApplyNotConfigured
	if errors.As(err, &notConfigured) {
		writeJSONError(w, http.StatusServiceUnavailable, "apply_unavailable", "the apply engine is not available on this node")
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "apply operation failed")
}

func handleListChangesets(svc ChangesetService, mgmt MgmtStatusService, wgCarriers change.WgCarrierSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := r.URL.Query().Get("status")
		changesets, err := svc.List(r.Context(), status)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list changesets")
			return
		}
		paths, carriers := mgmtEval(r.Context(), mgmt, wgCarriers) // once for the whole list
		out := make([]changesetResponse, len(changesets))
		for i, c := range changesets {
			out[i] = withMgmtFlag(c, paths, carriers)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func handleGetChangeset(svc ChangesetService, mgmt MgmtStatusService, wgCarriers change.WgCarrierSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		c, err := svc.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such changeset")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not load changeset")
			return
		}
		paths, carriers := mgmtEval(r.Context(), mgmt, wgCarriers)
		writeJSON(w, http.StatusOK, withMgmtFlag(c, paths, carriers))
	}
}

// opsField decodes a JSON `ops` array one element at a time so any
// *change.OpDecodeError can be prefixed with the failing op's index —
// `ops[7].params.mtu` instead of a bare `params.mtu`, which is ambiguous
// in a multi-op body (audit-phase-2 F-19). Each element is still decoded by
// change.Op's own strict UnmarshalJSON; this wrapper only adds position.
type opsField []change.Op

func (o *opsField) UnmarshalJSON(data []byte) error {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return &change.OpDecodeError{Path: "ops", Message: "ops must be an array of op objects"}
	}
	if raws == nil { // JSON null: preserve []change.Op's nil semantics
		*o = nil
		return nil
	}
	ops := make([]change.Op, len(raws))
	for i, raw := range raws {
		if err := json.Unmarshal(raw, &ops[i]); err != nil {
			path := fmt.Sprintf("ops[%d]", i)
			var opErr *change.OpDecodeError
			if errors.As(err, &opErr) {
				if opErr.Path != "" {
					path += "." + opErr.Path
				}
				return &change.OpDecodeError{Path: path, Message: opErr.Message}
			}
			return &change.OpDecodeError{Path: path, Message: err.Error()}
		}
	}
	*o = ops
	return nil
}

// createChangesetRequest is docs/api.md's POST /changesets body:
// `{title, ops:[Op]}`. TenantId (T-1703) is optional: when present the body
// creates a request-changeset (StatusRequested) owned by that tenant, subject
// to the tenant scope check, rather than an ordinary draft.
type createChangesetRequest struct {
	Title    string   `json:"title"`
	TenantId string   `json:"tenantId,omitempty"`
	Ops      opsField `json:"ops"`
}

func handleCreateChangeset(svc ChangesetService, lookup UsernameLookup, mgmt MgmtStatusService, wgCarriers change.WgCarrierSource, scoper TenantScoper, notifier ApprovalNotifier, adminStore TenantAdminStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		req, decErr := decodeChangesetRequest(w, r)
		if decErr != nil {
			writeOpDecodeError(w, decErr)
			return
		}

		// T-1703: a body carrying tenantId is a request-changeset. It requires
		// the tenant scoping service to be wired; without it, the field is
		// rejected rather than silently creating an ordinary (unscoped) draft.
		if req.TenantId != "" {
			if scoper == nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "multi-tenancy is not enabled on this deployment")
				return
			}
			createRequestChangeset(w, r, svc, scoper, notifier, adminStore, username, req.TenantId, req.Title, req.Ops)
			return
		}

		c, err := svc.Create(r.Context(), username, req.Title, req.Ops)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not create changeset")
			return
		}
		paths, carriers := mgmtEval(r.Context(), mgmt, wgCarriers)
		writeJSON(w, http.StatusCreated, withMgmtFlag(c, paths, carriers))
	}
}

// updateChangesetRequest is docs/api.md's PUT /changesets/{id} body:
// `{ops:[Op]}`. Title is an additional, optional field this package
// accepts so a parked draft can be renamed via the same endpoint
// (docs/features/change-management.md §1: "Multiple named drafts can be
// parked and resumed") without a second route docs/api.md doesn't
// document.
type updateChangesetRequest struct {
	Title *string  `json:"title,omitempty"`
	Ops   opsField `json:"ops"`
}

func handleUpdateChangeset(svc ChangesetService, lookup UsernameLookup, mgmt MgmtStatusService, wgCarriers change.WgCarrierSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChangesetBodyBytes))
		dec.DisallowUnknownFields()
		var req updateChangesetRequest
		if err := dec.Decode(&req); err != nil {
			var opErr *change.OpDecodeError
			if errors.As(err, &opErr) {
				writeOpDecodeError(w, opErr)
				return
			}
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
			return
		}

		c, err := svc.UpdateDraft(r.Context(), id, username, req.Title, req.Ops)
		if err != nil {
			writeChangesetMutationError(w, err)
			return
		}
		paths, carriers := mgmtEval(r.Context(), mgmt, wgCarriers)
		writeJSON(w, http.StatusOK, withMgmtFlag(c, paths, carriers))
	}
}

func handleDiscardChangeset(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		if err := svc.Discard(r.Context(), id, username); err != nil {
			writeChangesetMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleValidateChangeset backs `POST /changesets/{id}/validate`
// (docs/api.md: "re-run validation, returns findings") with T-202's real
// pipeline.
func handleValidateChangeset(svc ChangesetService, lookup UsernameLookup, mgmt MgmtStatusService, wgCarriers change.WgCarrierSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		c, err := svc.Validate(r.Context(), id, username)
		if err != nil {
			writeChangesetMutationError(w, err)
			return
		}
		paths, carriers := mgmtEval(r.Context(), mgmt, wgCarriers)
		writeJSON(w, http.StatusOK, withMgmtFlag(c, paths, carriers))
	}
}

// decodeChangesetRequest strictly decodes a POST /changesets body,
// bounding it to maxChangesetBodyBytes.
func decodeChangesetRequest(w http.ResponseWriter, r *http.Request) (createChangesetRequest, *change.OpDecodeError) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChangesetBodyBytes))
	dec.DisallowUnknownFields()
	var req createChangesetRequest
	if err := dec.Decode(&req); err != nil {
		var opErr *change.OpDecodeError
		if errors.As(err, &opErr) {
			return createChangesetRequest{}, opErr
		}
		return createChangesetRequest{}, &change.OpDecodeError{Path: "", Message: "malformed request body"}
	}
	return req, nil
}

// writeOpDecodeError translates a *change.OpDecodeError into docs/api.md's
// `validation_failed` error envelope, with the offending JSON path in
// `details.path` (T-201 acceptance criterion 1).
func writeOpDecodeError(w http.ResponseWriter, err *change.OpDecodeError) {
	writeJSONErrorDetails(w, http.StatusBadRequest, "validation_failed", err.Message, map[string]any{"path": err.Path})
}

// writeJSONErrorDetails is writeJSONError (router.go) plus docs/api.md's
// optional `details` object — router.go's own helper has no details
// parameter, and this package's other routes have never needed one, so
// this is kept local to the one handler set that does rather than
// widening every existing writeJSONError call site's signature.
func writeJSONErrorDetails(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
	})
}

// writeChangesetMutationError maps UpdateDraft/Discard errors to HTTP
// status per docs/api.md's error envelope: a missing changeset is 404; an
// illegal status transition (e.g. discarding an already-applied
// changeset) is 409 — a stable code this package introduces since
// docs/api.md's error-code list is explicitly non-exhaustive ("...").
func writeChangesetMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "no such changeset")
		return
	}
	var illegal *change.ErrIllegalTransition
	if errors.As(err, &illegal) {
		writeJSONError(w, http.StatusConflict, "invalid_transition", err.Error())
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not update changeset")
}
