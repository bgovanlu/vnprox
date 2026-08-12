package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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

	// T-2003 review surface: per-op/changeset comments and the
	// review-approval gate, generalizing T-1703's tenant approval queue.
	// AddComment/ReviewApprove/ReviewReject are AUTHORIZATION-relevant
	// mutations (docs/security.md) — Apply itself (above) is what actually
	// enforces the gate server-side; these three only ever record state it
	// reads.
	ListComments(ctx context.Context, changesetID string) ([]change.Comment, error)
	AddComment(ctx context.Context, changesetID, author, opID, body string) (change.Comment, error)
	DeleteComment(ctx context.Context, changesetID, commentID, author string) error
	GetApproval(ctx context.Context, changesetID string) (change.ApprovalState, error)
	ReviewApprove(ctx context.Context, changesetID, approver string) (change.Changeset, error)
	ReviewReject(ctx context.Context, changesetID, rejecter, reason string) (change.Changeset, error)

	// T-2604 two-person rule: the read model behind `approval.twoPerson`,
	// and the emergency override. InvokeBreakGlass only ever RECORDS an
	// override — Apply itself decides whether one applies, from the row this
	// wrote, server-side.
	TwoPersonState(ctx context.Context, changesetID string) (change.TwoPersonState, error)
	InvokeBreakGlass(ctx context.Context, changesetID, actor, reason string) (change.BreakGlassRecord, error)
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
//
//nolint:govet // fieldalignment: response DTO; field order is the JSON shape, not packing (matches internal/api/spec.go's identical precedent).
type changesetResponse struct {
	Plan            json.RawMessage `json:"plan,omitempty"`
	ApplyLog        json.RawMessage `json:"applyLog,omitempty"`
	ConfirmDeadline *int64          `json:"confirmDeadline,omitempty"`
	// UnattendedRevert (T-1805) reports whether this changeset will revert
	// itself with no live session, and until when — present on the apply
	// response and on a read of an awaiting_confirm changeset, omitted
	// otherwise. It carries a coverage *bound*, never the sealed PVE ticket
	// the coverage rests on: that credential has no representation in
	// change.Changeset at all (see change.Changeset.RevertTicketExpiresAt),
	// so no response built from one can leak it — a structural guarantee
	// rather than redactOpSecrets' field-by-field stripping.
	UnattendedRevert *change.UnattendedRevert `json:"unattendedRevert,omitempty"`
	ID               string                   `json:"id"`
	Title            string                   `json:"title"`
	Author           string                   `json:"author"`
	Status           string                   `json:"status"`
	// Origin (T-1701) is 'ui'|'mcp'|'cli': who staged this changeset, so the
	// review UI can badge an AI-staged draft distinctly from a human one.
	// OriginTokenID names the staging automation token (present only for a
	// token-staged changeset).
	Origin        string `json:"origin"`
	OriginTokenID string `json:"originTokenId,omitempty"`
	// OriginTool (T-2705) names the MCP staging tool that produced this
	// changeset ("changesets.stage.bridge", …), omitted for every changeset
	// not staged by one. Together with origin/originTokenId it is the tag the
	// review UI badges an AI-staged draft with: which kind of actor, which
	// automation credential (session), and which action.
	OriginTool string           `json:"originTool,omitempty"`
	Ops        []change.Op      `json:"ops"`
	Findings   []change.Finding `json:"findings"`
	CreatedAt  int64            `json:"createdAt"`
	UpdatedAt  int64            `json:"updatedAt"`
	// Comments and Approval (T-2003) are the review surface: per-op/
	// changeset comments and the current review-approval decision,
	// respectively. Both are additive fields (docs/architecture.md §10/§13's
	// deprecation policy) — omitted (nil) everywhere except the canonical
	// GET /changesets/{id} read (handleGetChangeset), which decorates them,
	// exactly like TouchesMgmtPath's own "canonical read computes it" note
	// below; every other response's byte shape is completely unaffected.
	Comments []commentResponse `json:"comments,omitempty"`
	Approval *approvalResponse `json:"approval,omitempty"`
	// ApplyStage (T-2602) describes a staged (canary) apply that is currently
	// PAUSED between stages: which strategy was recorded, which nodes have
	// been applied, which have not been contacted at all, and the two
	// deadlines. Present only while the pause exists — every ordinary
	// all-at-once apply (the default) omits it entirely, so no existing
	// response's byte shape changes.
	ApplyStage *change.StagedApplyState `json:"applyStage,omitempty"`
	// Locks (T-2805) is the advisory-lock warning for a staging request:
	// which entities this changeset touches that another operator already
	// has a draft open against, and which of those this request deliberately
	// took over. It is present ONLY on POST /changesets and PUT
	// /changesets/{id}, and only when there is something to warn about, so
	// every other response — and every uncontended staging — has a
	// byte-identical shape to the pre-T-2805 one.
	//
	// It is a WARNING and nothing else. The changeset it appears on was
	// already created or updated by the time this object was built, and no
	// route anywhere refuses anything because of a lock (docs/api.md's
	// advisory-locks paragraph; internal/presence's doc.go for why that is
	// structural).
	Locks *changesetLocks `json:"locks,omitempty"`
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

// commentResponse is one review Comment's wire shape (docs/api.md's
// changesets section): `{id, opId?, author, body, createdAt}`.
type commentResponse struct {
	ID        string `json:"id"`
	OpID      string `json:"opId,omitempty"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"createdAt"`
}

func toCommentResponse(c change.Comment) commentResponse {
	return commentResponse{ID: c.ID, OpID: c.OpID, Author: c.Author, Body: c.Body, CreatedAt: c.CreatedAt}
}

// approvalResponse is a changeset's review-approval state's wire shape:
// `{status, decidedBy?, reason?, decidedAt?, required}`. status is
// "none"|"approved"|"rejected"; required reports whether THIS deployment's
// policy currently gates apply on approval — a client must never infer that
// from the absence of an apply button; Apply's own refusal
// (approval_required, see writeApplyError below) is the actual enforcement.
type approvalResponse struct {
	// TwoPerson (T-2604) reports the two-person rule's state for this
	// changeset: which protected op classes it falls into, how many DISTINCT
	// principals must approve, who has, and whether an emergency break-glass
	// override is on record. Omitted entirely for a deployment that declares
	// no protected classes, so no pre-T-2604 response's byte shape changed.
	//
	// Like `required` above, it is a READ of the gate, never the gate: a
	// client must not infer permission from it, and Apply's own refusal
	// (two_person_required) is the enforcement.
	TwoPerson *change.TwoPersonState `json:"twoPerson,omitempty"`
	Status    string                 `json:"status"`
	DecidedBy string                 `json:"decidedBy,omitempty"`
	Reason    string                 `json:"reason,omitempty"`
	DecidedAt int64                  `json:"decidedAt,omitempty"`
	Required  bool                   `json:"required"`
}

func toApprovalResponse(a change.ApprovalState) approvalResponse {
	return approvalResponse{Status: string(a.Status), DecidedBy: a.DecidedBy, Reason: a.Reason, DecidedAt: a.DecidedAt, Required: a.Required}
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
	origin := c.Origin
	if origin == "" {
		origin = change.OriginUI
	}
	return changesetResponse{
		ID: c.ID, Title: c.Title, Author: c.Author, Status: string(c.Status),
		Origin: origin, OriginTokenID: c.OriginTokenID, OriginTool: c.OriginTool,
		Ops: ops, Findings: findings, Plan: c.Plan, ApplyLog: c.ApplyLog,
		ConfirmDeadline: c.ConfirmDeadline, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		UnattendedRevert: c.UnattendedRevert,
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
func mountChangesetsRoutes(r chi.Router, svc ChangesetService, auth AuthService, gateways PVEGatewayProvider, mgmt MgmtStatusService, wgCarriers change.WgCarrierSource, scoper TenantScoper, notifier ApprovalNotifier, adminStore TenantAdminStore, locks LockService) {
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
		// T-2404: the blast-radius preview. A read, so it sits with the other
		// netRead routes — knowing what a changeset would disrupt must never
		// require the capability to apply it.
		r.Get("/changesets/{id}/impact", handleChangesetImpact(svc, mgmt, wgCarriers))
		// T-2605: the post-apply topology preview. A read, in the netRead
		// group for the same reason as impact above — seeing what the map
		// would look like must never require the capability to make it so.
		r.Get("/changesets/{id}/preview", handleChangesetPreview(svc))

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
		r.Post("/changesets", handleCreateChangeset(svc, lookup, mgmt, wgCarriers, scoper, notifier, adminStore, locks, auth))
		r.Put("/changesets/{id}", handleUpdateChangeset(svc, lookup, mgmt, wgCarriers, locks, auth))
		r.Delete("/changesets/{id}", handleDiscardChangeset(svc, lookup, locks))
		r.Post("/changesets/{id}/validate", handleValidateChangeset(svc, lookup, mgmt, wgCarriers))
		r.Post("/changesets/{id}/apply", handleApplyChangeset(svc, lookup, gateways, mgmt, wgCarriers))
		r.Post("/changesets/{id}/confirm", handleConfirmChangeset(svc, lookup, mgmt, wgCarriers))
		// T-2602: promote a staged apply past its canary hold.
		r.Post("/changesets/{id}/continue", handleContinueChangeset(svc, lookup, gateways, mgmt, wgCarriers))
		r.Post("/changesets/{id}/rollback", handleRollbackChangeset(svc, lookup, gateways, mgmt, wgCarriers))

		// T-2003: the review surface — per-op/changeset comments and the
		// review-approval gate. /review/approve and /review/reject are
		// deliberately NOT /changesets/{id}/approve: that route already
		// exists (T-1703, mountTenantRoutes below) and means something
		// different — converting a tenant's request-changeset to a draft.
		// This is a new, additive route family, not a repurposing of it.
		r.Post("/changesets/{id}/comments", handleAddComment(svc, lookup))
		r.Delete("/changesets/{id}/comments/{commentId}", handleDeleteComment(svc, lookup))
		r.Post("/changesets/{id}/review/approve", handleReviewApprove(svc, lookup))
		r.Post("/changesets/{id}/review/reject", handleReviewReject(svc, lookup))

		// T-2604: emergency break-glass on the two-person rule. Its own
		// route rather than a field on the apply body, deliberately: an
		// override is an event with its own actor, its own written reason,
		// its own audit action (`change.breakglass`) and its own
		// consequence (an error finding nobody may acknowledge for 24
		// hours). Folding it into the apply request would make all four
		// incidental to a request whose subject is something else.
		r.Post("/changesets/{id}/break-glass", handleBreakGlass(svc, lookup))
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

// StagedApplyService is the optional ChangesetService extension backing
// T-2602's canary / staged apply (change.Service.ApplyStaged /
// ContinueStagedApply / StagedApplyState). Checked with a type assertion,
// exactly like MgmtAckRecorder and ChangesetImpactService above, so a test
// double that does not care about staging need not grow three methods — and
// so a deployment whose changeset service predates it degrades to refusing
// an applyStrategy rather than silently ignoring one.
type StagedApplyService interface {
	ApplyStaged(ctx context.Context, id, author string, pveGW change.PVEGateway, confirmTimeout time.Duration, strategy change.ApplyStrategy) (change.Changeset, error)
	ContinueStagedApply(ctx context.Context, id, author string, pveGW change.PVEGateway) (change.Changeset, error)
	StagedApplyState(ctx context.Context, id string) (change.StagedApplyState, bool, error)
}

// AutoRollbackApplyService is the optional ChangesetService extension backing
// T-2603's `autoRollbackOnError` apply flag (change.Service.ApplyWithOptions).
// Checked with a type assertion, exactly like StagedApplyService above, so a
// deployment whose changeset service predates it REFUSES the field rather
// than applying without the guard the caller asked for — silently dropping a
// safety request is worse than declining it.
type AutoRollbackApplyService interface {
	ApplyWithOptions(ctx context.Context, id, author string, pveGW change.PVEGateway, confirmTimeout time.Duration, strategy change.ApplyStrategy, opts change.ApplyOptions) (change.Changeset, error)
}

// applyRequest is docs/api.md's POST /changesets/{id}/apply body:
// `{confirmTimeoutSec: 120, mgmtAck?: {node}, applyStrategy?: {...}}`.
// MgmtAck (T-703) is the review screen's typed management-path
// acknowledgement, recorded to the audit log when the changeset touches a
// management path. ApplyStrategy (T-2602) stages the apply; omitting it is
// `mode: all`, which is what every apply has always done.
type applyRequest struct {
	MgmtAck       *mgmtAckRequest       `json:"mgmtAck,omitempty"`
	ApplyStrategy *change.ApplyStrategy `json:"applyStrategy,omitempty"`
	// AutoRollbackOnError (T-2603) arms the finding-triggered rollback for
	// this changeset's commit-confirm window. Omitted (nil) means "the
	// cluster default", which is itself off unless an admin opted in — so a
	// body that predates this field behaves exactly as it always did.
	AutoRollbackOnError *bool `json:"autoRollbackOnError,omitempty"`
	ConfirmTimeoutSec   int   `json:"confirmTimeoutSec"`
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

		// T-2602: a request that names an applyStrategy goes through
		// ApplyStaged; one that does not goes through Apply, which is
		// ApplyStaged with the zero (mode: all) strategy. A deployment whose
		// changeset service does not implement the staged extension refuses
		// the field outright rather than applying all-at-once behind the
		// caller's back — silently ignoring a safety request is worse than
		// declining it.
		//
		// T-2603: `autoRollbackOnError` is orthogonal to the strategy (it
		// governs the confirm window, not the fan-out), so a body carrying it
		// goes through ApplyWithOptions with whatever strategy it also named —
		// including the zero `mode: all` one.
		var c change.Changeset
		var err error
		staged, stagedOK := svc.(StagedApplyService)
		guarded, guardedOK := svc.(AutoRollbackApplyService)
		switch {
		case req.AutoRollbackOnError != nil && !guardedOK:
			writeJSONError(w, http.StatusNotImplemented, "not_implemented", "finding-triggered auto-rollback is not available on this deployment")
			return
		case req.ApplyStrategy != nil && !stagedOK:
			writeJSONError(w, http.StatusNotImplemented, "not_implemented", "staged (canary) apply is not available on this deployment")
			return
		case req.AutoRollbackOnError != nil:
			strategy := change.ApplyStrategy{}
			if req.ApplyStrategy != nil {
				strategy = *req.ApplyStrategy
			}
			c, err = guarded.ApplyWithOptions(r.Context(), id, username, gw, confirmTimeout, strategy,
				change.ApplyOptions{AutoRollbackOnError: req.AutoRollbackOnError})
		case req.ApplyStrategy != nil:
			c, err = staged.ApplyStaged(r.Context(), id, username, gw, confirmTimeout, *req.ApplyStrategy)
		default:
			c, err = svc.Apply(r.Context(), id, username, gw, confirmTimeout)
		}
		if err != nil {
			writeApplyError(w, err)
			return
		}
		resp := toChangesetResponse(c)
		resp.TouchesMgmtPath = touchesMgmt
		resp = withStagedApply(r.Context(), svc, resp)
		resp = withReview(r.Context(), svc, resp)
		writeJSON(w, http.StatusAccepted, resp)
	}
}

// handleContinueChangeset serves `POST /changesets/{id}/continue` (T-2602):
// the manual gate's promotion of a staged apply from the canary stage to the
// rest of the cluster.
//
// It is a netWrite + CSRF route sitting beside apply/confirm/rollback, not a
// variant of confirm: confirming a changeset paused between stages would
// commit a half-applied change, which is precisely what this route exists to
// avoid. A changeset that is not paused is refused with `invalid_transition`.
func handleContinueChangeset(svc ChangesetService, lookup UsernameLookup, gateways PVEGatewayProvider, mgmt MgmtStatusService, wgCarriers change.WgCarrierSource) http.HandlerFunc {
	staged, ok := svc.(StagedApplyService)
	return func(w http.ResponseWriter, r *http.Request) {
		if !ok {
			writeJSONError(w, http.StatusNotImplemented, "not_implemented", "staged (canary) apply is not available on this deployment")
			return
		}
		username, found := lookup.Username(r.Context())
		if !found {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var gw change.PVEGateway
		if gateways != nil {
			gw, _ = gateways.GatewayFor(r.Context())
		}
		c, err := staged.ContinueStagedApply(r.Context(), chi.URLParam(r, "id"), username, gw)
		if err != nil {
			writeApplyError(w, err)
			return
		}
		paths, carriers := mgmtEval(r.Context(), mgmt, wgCarriers)
		resp := withStagedApply(r.Context(), svc, withMgmtFlag(c, paths, carriers))
		writeJSON(w, http.StatusOK, withReview(r.Context(), svc, resp))
	}
}

// withStagedApply decorates resp with T-2602's `applyStage` — present only
// while a changeset is actually paused between stages, absent for every
// ordinary apply (which is every apply, by default). Best-effort like
// withReview: a read failure omits the field rather than failing the
// response.
func withStagedApply(ctx context.Context, svc ChangesetService, resp changesetResponse) changesetResponse {
	staged, ok := svc.(StagedApplyService)
	if !ok {
		return resp
	}
	state, paused, err := staged.StagedApplyState(ctx, resp.ID)
	if err != nil || !paused {
		return resp
	}
	resp.ApplyStage = &state
	return resp
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
		writeJSON(w, http.StatusOK, withReview(r.Context(), svc, withMgmtFlag(c, paths, carriers)))
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
		writeJSON(w, http.StatusOK, withReview(r.Context(), svc, withStagedApply(r.Context(), svc, withMgmtFlag(c, paths, carriers))))
	}
}

// ChangesetImpactService is the optional ChangesetService extension backing
// T-2404's blast-radius preview (change.Service.Impact). Checked with a type
// assertion, like MgmtAckRecorder above, so a test double that does not care
// about impact need not grow a method.
type ChangesetImpactService interface {
	Impact(ctx context.Context, id string, mgmtPaths map[string][]topology.MgmtPath, tunnelCarriers map[string]change.WgTunnelCarrier, mgmtSwitchPorts map[string]bool) (change.Impact, error)
}

// handleChangesetImpact serves `GET /changesets/{id}/impact` (T-2404): which
// nodes, carriers and guests this changeset would affect, and how badly.
//
// The management-path resolution is the SAME one the apply ceremony uses
// (mgmtEval), rather than a second derivation, so the impact panel and the
// mandatory-acknowledgement block can never disagree about whether a changeset
// touches the management path.
//
// The impact is computed entirely server-side. Nothing in the request can
// supply, weight, or override it — a client that could soften its own blast
// radius would make the preview worthless precisely when it matters.
func handleChangesetImpact(svc ChangesetService, mgmt MgmtStatusService, wgCarriers change.WgCarrierSource) http.HandlerFunc {
	impacter, ok := svc.(ChangesetImpactService)
	return func(w http.ResponseWriter, r *http.Request) {
		if !ok {
			writeJSONError(w, http.StatusNotImplemented, "not_implemented", "impact preview is not available")
			return
		}
		paths, carriers := mgmtEval(r.Context(), mgmt, wgCarriers)
		impact, err := impacter.Impact(r.Context(), chi.URLParam(r, "id"), paths, carriers, nil)
		if err != nil {
			writeApplyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, impact)
	}
}

// ChangesetPreviewService is the optional ChangesetService extension backing
// T-2605's post-apply topology preview (change.Service.Preview). Checked with a
// type assertion, like ChangesetImpactService above, so a test double that does
// not care about the preview need not grow a method.
type ChangesetPreviewService interface {
	Preview(ctx context.Context, id string) (change.Preview, error)
}

// handleChangesetPreview serves `GET /changesets/{id}/preview` (T-2605): the
// cluster map as it would be with this changeset's ops applied.
//
// Read-only and side-effect free by construction. It takes no PVE gateway (so
// there is nothing for it to write PVE through), passes no author (so there is
// no actor for it to attribute a store write to), and the service method it
// calls persists nothing. A changeset with blocking validation findings is
// refused with the ordinary 422 `validation_failed` envelope rather than
// projected: a changeset that cannot apply has no post-apply map.
func handleChangesetPreview(svc ChangesetService) http.HandlerFunc {
	previewer, ok := svc.(ChangesetPreviewService)
	return func(w http.ResponseWriter, r *http.Request) {
		if !ok {
			writeJSONError(w, http.StatusNotImplemented, "not_implemented", "post-apply preview is not available")
			return
		}
		preview, err := previewer.Preview(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeApplyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, preview)
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
	var approvalRequired *change.ErrApprovalRequired
	if errors.As(err, &approvalRequired) {
		writeJSONError(w, http.StatusUnprocessableEntity, "approval_required", err.Error())
		return
	}
	// T-2604: the two-person rule's refusal. A new, additive 422 code — the
	// changeset may be perfectly valid (so not validation_failed) and the
	// status state machine is untouched (so not invalid_transition). Details
	// carry the class, the count required, and who has approved so far, so
	// the UI can say what would satisfy it rather than only that it refused.
	var twoPerson *change.ErrTwoPersonRequired
	if errors.As(err, &twoPerson) {
		writeJSONErrorDetails(w, http.StatusUnprocessableEntity, "two_person_required", err.Error(), map[string]any{
			"class":     twoPerson.Class,
			"required":  twoPerson.Required,
			"have":      twoPerson.Have,
			"approvers": twoPerson.Approvers,
			"classes":   twoPerson.Classes,
		})
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
	// T-2602: a strategy the engine cannot honour is refused before any
	// mutation, with its own stable code — never folded into
	// validation_failed (the changeset's ops are fine; the apply *strategy*
	// is not) nor into invalid_transition (the status machine is untouched).
	var badStrategy *change.ErrInvalidApplyStrategy
	if errors.As(err, &badStrategy) {
		writeJSONErrorDetails(w, http.StatusUnprocessableEntity, "invalid_apply_strategy", err.Error(),
			map[string]any{"nodes": badStrategy.Nodes})
		return
	}
	var notResumable *change.ErrNotResumable
	if errors.As(err, &notResumable) {
		writeJSONError(w, http.StatusConflict, "invalid_transition", err.Error())
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
		resp := withReview(r.Context(), svc, withStagedApply(r.Context(), svc, withMgmtFlag(c, paths, carriers)))
		writeJSON(w, http.StatusOK, resp)
	}
}

// withReview decorates resp with T-2003's review surface (comments,
// approval state). Called from EVERY route that returns a single
// changesetResponse — not only GET — because the frontend's TanStack Query
// cache for a changeset (changesetKey(id)) is repopulated wholesale from
// whichever response arrives last (create/update/validate/apply/confirm/
// rollback/get all call queryClient.setQueryData with their own response
// body): if only GET carried these fields, the very next mutation (e.g. the
// review screen's own re-validate-on-open) would silently overwrite them
// back to absent in the client's cache, even though the server-side data
// never changed. Mirrors withMgmtFlag's identical "every route decorates
// this" precedent for touchesMgmtPath. Best-effort: a read failure degrades
// to omitting the field rather than failing the whole response (the same
// tolerance mgmtPathsFor already applies).
func withReview(ctx context.Context, svc ChangesetService, resp changesetResponse) changesetResponse {
	if comments, err := svc.ListComments(ctx, resp.ID); err == nil {
		out := make([]commentResponse, len(comments))
		for i, cm := range comments {
			out[i] = toCommentResponse(cm)
		}
		resp.Comments = out
	}
	if approval, err := svc.GetApproval(ctx, resp.ID); err == nil {
		ar := toApprovalResponse(approval)
		// T-2604: the two-person rule's read model rides the same field, and
		// with the same best-effort tolerance — a deployment with no
		// protected classes does no work at all here (change.Service.
		// TwoPersonState short-circuits), and a read failure omits the field
		// rather than failing the whole response.
		if tp, terr := svc.TwoPersonState(ctx, resp.ID); terr == nil && len(tp.Classes) > 0 {
			state := tp
			ar.TwoPerson = &state
		}
		resp.Approval = &ar
	}
	return resp
}

// addCommentRequest is `POST /changesets/{id}/comments`'s body:
// `{opId?, body}`. opId, when present, must name an op currently on the
// changeset (*change.ErrCommentOpNotFound otherwise); omitted attaches a
// changeset-level comment.
type addCommentRequest struct {
	OpID string `json:"opId,omitempty"`
	Body string `json:"body"`
}

func handleAddComment(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		dec.DisallowUnknownFields()
		var req addCommentRequest
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
			return
		}
		if strings.TrimSpace(req.Body) == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "comment body is required")
			return
		}

		c, err := svc.AddComment(r.Context(), id, username, req.OpID, req.Body)
		if err != nil {
			writeReviewError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toCommentResponse(c))
	}
}

func handleDeleteComment(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		commentID := chi.URLParam(r, "commentId")

		if err := svc.DeleteComment(r.Context(), id, commentID, username); err != nil {
			writeReviewError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleReviewApprove(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		if _, err := svc.ReviewApprove(r.Context(), id, username); err != nil {
			writeReviewError(w, err)
			return
		}
		approval, err := svc.GetApproval(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "approval recorded but could not be read back")
			return
		}
		writeJSON(w, http.StatusOK, toApprovalResponse(approval))
	}
}

// reviewRejectRequest is `POST /changesets/{id}/review/reject`'s body:
// `{reason?}`.
type reviewRejectRequest struct {
	Reason string `json:"reason,omitempty"`
}

func handleReviewReject(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		var req reviewRejectRequest
		if r.ContentLength != 0 {
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
				return
			}
		}

		if _, err := svc.ReviewReject(r.Context(), id, username, req.Reason); err != nil {
			writeReviewError(w, err)
			return
		}
		approval, err := svc.GetApproval(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "rejection recorded but could not be read back")
			return
		}
		writeJSON(w, http.StatusOK, toApprovalResponse(approval))
	}
}

// breakGlassRequest is `POST /changesets/{id}/break-glass`'s body:
// `{reason}`. The reason is REQUIRED — an override with no written
// justification is exactly the thing this ceremony exists to prevent — and
// is refused server-side (change.ErrBreakGlassReasonRequired), never merely
// by the form insisting on it.
type breakGlassRequest struct {
	Reason string `json:"reason"`
}

// handleBreakGlass serves `POST /changesets/{id}/break-glass` (T-2604): the
// emergency, reasoned override of the two-person rule on protected op
// classes.
//
// It only RECORDS the override. The apply that follows still runs every
// other gate — validation, T-2003's approval requirement, peer compatibility
// — and still decides for itself, server-side, whether the recorded override
// applies to the ops actually being applied.
func handleBreakGlass(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		var req breakGlassRequest
		if r.ContentLength != 0 {
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
				return
			}
		}

		rec, err := svc.InvokeBreakGlass(r.Context(), id, username, req.Reason)
		if err != nil {
			writeBreakGlassError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rec)
	}
}

// writeBreakGlassError maps T-2604's break-glass errors to their documented
// (status, code) pairs.
func writeBreakGlassError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "no such changeset")
		return
	}
	var reasonRequired *change.ErrBreakGlassReasonRequired
	if errors.As(err, &reasonRequired) {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	var notConfigured *change.ErrBreakGlassNotConfigured
	if errors.As(err, &notConfigured) {
		writeJSONError(w, http.StatusServiceUnavailable, "break_glass_unavailable", err.Error())
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "break-glass could not be recorded")
}

// writeReviewError maps T-2003 review-surface errors (comments/approval) to
// docs/api.md's error envelope + stable codes.
func writeReviewError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "no such changeset or comment")
		return
	}
	var opNotFound *change.ErrCommentOpNotFound
	if errors.As(err, &opNotFound) {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	var selfForbidden *change.ErrSelfApprovalForbidden
	if errors.As(err, &selfForbidden) {
		writeJSONError(w, http.StatusForbidden, "self_approval_forbidden", err.Error())
		return
	}
	var notApprover *change.ErrNotAnApprover
	if errors.As(err, &notApprover) {
		writeJSONError(w, http.StatusForbidden, "not_an_approver", err.Error())
		return
	}
	var notConfigured *change.ErrReviewNotConfigured
	if errors.As(err, &notConfigured) {
		writeJSONError(w, http.StatusServiceUnavailable, "review_unavailable", err.Error())
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "review operation failed")
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
// LockOverride (T-2805) is the deliberate "I know someone else has this
// open, take it anyway" flag. Omitting it (the default, and every
// pre-T-2805 caller) leaves another operator's claim alone and reports it
// in the response's `locks.held`; the changeset is staged either way.
// Setting it takes the claim over and audits each takeover as
// `changeset.lock_override`.
type createChangesetRequest struct {
	Title        string   `json:"title"`
	TenantId     string   `json:"tenantId,omitempty"`
	Ops          opsField `json:"ops"`
	LockOverride bool     `json:"lockOverride,omitempty"`
}

func handleCreateChangeset(svc ChangesetService, lookup UsernameLookup, mgmt MgmtStatusService, wgCarriers change.WgCarrierSource, scoper TenantScoper, notifier ApprovalNotifier, adminStore TenantAdminStore, locks LockService, authSvc AuthService) http.HandlerFunc {
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
		resp := withReview(r.Context(), svc, withMgmtFlag(c, paths, carriers))
		// T-2805: the draft exists at this point regardless of what the lock
		// table says. Staging is never refused; this only decides what the
		// operator is TOLD about it.
		resp.Locks = stageLocks(r.Context(), locks, authSvc, username, c.ID, opTargets(c.Ops), req.LockOverride)
		writeJSON(w, http.StatusCreated, resp)
	}
}

// opTargets is the set of entity Ref strings a changeset's ops name — the
// entities an advisory lock is taken on. Ops with no target at all (a
// trailing `sdn.apply`, say) lock nothing: there is no entity to collide
// over.
func opTargets(ops []change.Op) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		if op.Target.IsZero() {
			continue
		}
		out = append(out, op.Target.String())
	}
	return out
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
	// LockOverride (T-2805): see createChangesetRequest's own comment.
	LockOverride bool `json:"lockOverride,omitempty"`
}

func handleUpdateChangeset(svc ChangesetService, lookup UsernameLookup, mgmt MgmtStatusService, wgCarriers change.WgCarrierSource, locks LockService, authSvc AuthService) http.HandlerFunc {
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
		resp := withReview(r.Context(), svc, withMgmtFlag(c, paths, carriers))
		resp.Locks = stageLocks(r.Context(), locks, authSvc, username, c.ID, opTargets(c.Ops), req.LockOverride)
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleDiscardChangeset(svc ChangesetService, lookup UsernameLookup, locks LockService) http.HandlerFunc {
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
		// T-2805: the draft is gone, so its advisory claim on those entities
		// is meaningless. Best-effort — the discard already succeeded, and a
		// stranded lock expires on its own.
		if locks != nil {
			if _, err := locks.ReleaseChangeset(r.Context(), id); err != nil {
				slog.Default().Warn("api: releasing locks for a discarded changeset", "error", err, "changeset_id", id)
			}
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
		writeJSON(w, http.StatusOK, withReview(r.Context(), svc, withMgmtFlag(c, paths, carriers)))
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
