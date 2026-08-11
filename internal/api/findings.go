package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/store"
)

// FindingsService is the subset of *findings.Engine the router needs:
// T-602's unified findings stream (docs/features/monitoring.md §5) and the
// fixing-changeset op lookup by finding id — the same two-method shape
// DriftService already established, generalized across every producer.
type FindingsService interface {
	Findings() []findings.Finding
	FixOps(id string) (ops []change.Op, title string, ok bool)
}

// FindingAckService is the acknowledgement half of the findings surface
// (T-2402) — *findings.AckService satisfies it. Optional: when nil, the ack
// routes are not mounted and GET /findings behaves exactly as it did before,
// which is what keeps a degraded startup (no store) serving findings.
type FindingAckService interface {
	Decorate(ctx context.Context, in []findings.Finding) ([]findings.Finding, int, error)
	Ack(ctx context.Context, findingID, reason, actor string, expiresAt int64, present map[string]findings.Finding) (findings.Ack, error)
	Unack(ctx context.Context, findingID string) error
}

// findingsAuditWriter is the minimal audit seam the ack routes need (exactly
// *store.AuditRepo's Append). Optional; nil skips the audit row but never
// skips the ack itself — an unwritten audit row must not be able to refuse an
// operator's triage decision.
type findingsAuditWriter interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

type findingAckRequest struct {
	Reason string `json:"reason"`
	// ExpiresAt is unix seconds, or 0/absent for "until explicitly un-acked".
	ExpiresAt int64 `json:"expiresAt"`
}

type findingsBatchFixRequest struct {
	IDs []string `json:"ids"`
}

// mountFindingsRoutes registers `GET /findings` and `POST /findings/{id}/fix`
// (T-602; not in the original docs/api.md contract — documented there in
// this same change per docs/development.md's definition-of-done #4, the
// same pattern T-305's /drift additions used). This is additive to, not a
// replacement for, the existing `GET /drift`/`POST /drift/{id}/fix` routes
// (mountDriftRoutes) — those keep working entirely unchanged for any
// existing caller that only cares about drift-family findings; /findings is
// the new unified superset spanning drift+lldp+ipam+health.
func mountFindingsRoutes(r chi.Router, svc FindingsService, changesets ChangesetService, acks FindingAckService, audit findingsAuditWriter, auth AuthService, scopeMW func(http.Handler) http.Handler) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		if scopeMW != nil {
			r.Use(scopeMW)
		}
		r.Get("/findings", handleFindings(svc, acks))
	})

	lookup, hasLookup := auth.(UsernameLookup)

	// Acknowledgement (T-2402) is a write to vnprox's own triage state, not to
	// the network, so it takes capNetWrite like every other authenticated
	// mutation on this surface — but it can never reach the change engine.
	if acks != nil && hasLookup {
		r.Group(func(r chi.Router) {
			r.Use(auth.SessionMiddleware)
			if csrf, ok := auth.(CSRFEnforcer); ok {
				r.Use(csrf.CSRFMiddleware)
			}
			r.Use(auth.RequireCap(capNetWrite))
			r.Post("/findings/{id}/ack", handleFindingAck(svc, acks, audit, lookup))
			r.Delete("/findings/{id}/ack", handleFindingUnack(acks, audit, lookup))
		})
	}

	if changesets == nil || !hasLookup {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/findings/{id}/fix", handleFindingsFix(svc, changesets, lookup))
		r.Post("/findings/fix", handleFindingsBatchFix(svc, changesets, acks, lookup))
	})
}

// handleFindings serves `GET /findings?source=&severity=&node=` — AC2's
// "filter by source/severity/node works ... uniformly": every filter is
// applied generically over the already-unified Finding shape (no
// producer-specific filter logic needed, since every producer's findings
// already arrive through the same fields). Every filter param is optional
// and additive (AND, not OR): source=drift&severity=error returns only
// error-severity drift findings. An unrecognized value for any filter
// simply matches nothing for that filter (never a 400) — this is a
// read-only convenience filter, not a validated query language.
func handleFindings(svc FindingsService, acks FindingAckService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		source := strings.TrimSpace(q.Get("source"))
		severity := strings.TrimSpace(q.Get("severity"))
		node := strings.TrimSpace(q.Get("node"))
		acked := strings.TrimSpace(q.Get("acked"))

		all := svc.Findings()
		// T-2402: attach each finding's currently-active acknowledgement
		// before filtering, so ?acked= can see it. A failure here degrades to
		// undecorated findings rather than failing the read — the stream is
		// the important thing and an ack is an annotation on it.
		if acks != nil {
			decorated, _, err := acks.Decorate(r.Context(), all)
			if err == nil {
				all = decorated
			}
		}
		items := make([]findings.Finding, 0, len(all))
		for _, f := range all {
			if source != "" && string(f.Source) != source {
				continue
			}
			if severity != "" && f.Severity != severity {
				continue
			}
			if node != "" && !findingHasNode(f.Nodes, node) {
				continue
			}
			// T-2402: "only"/"exclude" are the two useful views — the default
			// (no param) returns everything with acks attached, because
			// acknowledgement is not suppression and the stream must never
			// silently hide a finding.
			if acked == "only" && f.Ack == nil {
				continue
			}
			if acked == "exclude" && f.Ack != nil {
				continue
			}
			items = append(items, f)
		}
		// T-1703: a tenant sees only findings that reference one of its own
		// visible resources (never cluster-wide/node-wide findings).
		if scope, scoped := scopeFromContext(r.Context()); scoped {
			items = filterFindingsForScope(items, scope)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func findingHasNode(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func handleFindingsFix(svc FindingsService, changesets ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		ops, title, ok := svc.FixOps(id)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "not_found", "no fixable finding with that id")
			return
		}
		c, err := changesets.Create(r.Context(), username, title, ops)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not create fixing changeset")
			return
		}
		writeJSON(w, http.StatusCreated, toChangesetResponse(c))
	}
}

// handleFindingAck serves `POST /findings/{id}/ack` (T-2402).
//
// The finding must currently be reported by the engine: acking an id nothing
// produces would leave a row the UI can never surface (it only renders acks
// alongside their finding) and therefore never clear.
func handleFindingAck(svc FindingsService, acks FindingAckService, audit findingsAuditWriter, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var req findingAckRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "body must be a JSON object")
			return
		}
		id := chi.URLParam(r, "id")

		ack, err := acks.Ack(r.Context(), id, req.Reason, username, req.ExpiresAt, findings.PresentFindings(svc.Findings()))
		var tooEarly *findings.ErrAckTooEarly
		switch {
		case errors.Is(err, findings.ErrNoSuchFinding):
			writeJSONError(w, http.StatusNotFound, "not_found", "no finding with that id is currently reported")
			return
		case errors.As(err, &tooEarly):
			// T-2604: a finding with an acknowledgement floor (the break-glass
			// record) refuses an early ack with its own stable code and the
			// instant it becomes ackable — never a generic validation error,
			// because the caller's request was well-formed and will succeed
			// unchanged later.
			writeJSONErrorDetails(w, http.StatusConflict, "ack_too_early", tooEarly.Error(),
				map[string]any{"ackableAt": tooEarly.AckableAt})
			return
		case errors.Is(err, findings.ErrAckReasonRequired):
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "an acknowledgement requires a reason")
			return
		case errors.Is(err, findings.ErrAckExpiryInPast):
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "expiresAt is already in the past")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not record the acknowledgement")
			return
		}

		appendFindingAckAudit(r.Context(), audit, username, "finding.ack", id, ack.Reason)
		writeJSON(w, http.StatusOK, ack)
	}
}

// handleFindingUnack serves `DELETE /findings/{id}/ack` (T-2402).
//
// Deliberately does NOT require the finding to still be reported: an operator
// must always be able to clear a stale acknowledgement, including one whose
// finding has since gone away.
func handleFindingUnack(acks FindingAckService, audit findingsAuditWriter, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		if err := acks.Unack(r.Context(), id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not clear the acknowledgement")
			return
		}
		appendFindingAckAudit(r.Context(), audit, username, "finding.unack", id, "")
		w.WriteHeader(http.StatusNoContent)
	}
}

// appendFindingAckAudit records one audit row per acknowledgement decision,
// carrying the reason: "why is this muted" must be answerable from the audit
// log alone, after the ack itself has expired and been forgotten.
//
// A failed audit write is deliberately not fatal to the ack — the operator's
// triage decision has already been recorded, and refusing it now would leave
// the store and the response disagreeing.
func appendFindingAckAudit(ctx context.Context, audit findingsAuditWriter, username, action, findingID, reason string) {
	if audit == nil {
		return
	}
	entry := store.AuditEntry{
		Username: username,
		Action:   action,
		Result:   "ok",
		Target:   sql.NullString{String: findingID, Valid: true},
	}
	if reason != "" {
		if b, err := json.Marshal(map[string]string{"reason": reason}); err == nil {
			entry.DetailJSON = sql.NullString{String: string(b), Valid: true}
		}
	}
	_, _ = audit.Append(ctx, entry)
}

// maxBatchFixIDs bounds one batch. Twenty findings is already far more than
// anyone reviews in one changeset; the bound exists so a malformed or hostile
// caller cannot ask the engine to resolve an unbounded list.
const maxBatchFixIDs = 100

// handleFindingsBatchFix serves `POST /findings/fix` (T-2408): stage EVERY
// selected fixable finding into ONE changeset.
//
// The whole batch is refused if any id is unknown, unfixable, acked, or
// conflicts with another id in the batch. Partial success here is a trap: the
// response is a single changeset, so a caller who got one back could not tell
// which half of their selection it represents — and the half that silently
// vanished is the half they would then believe was fixed.
//
// This stages only. The resulting changeset is an ordinary draft and still
// validates, diffs, and passes review/approval like any other; nothing here is
// a second mutation path (CLAUDE.md's change-engine rule).
func handleFindingsBatchFix(svc FindingsService, changesets ChangesetService, acks FindingAckService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var req findingsBatchFixRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "body must be a JSON object")
			return
		}
		ids := dedupeStrings(req.IDs)
		if len(ids) == 0 {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "ids must contain at least one finding id")
			return
		}
		if len(ids) > maxBatchFixIDs {
			writeJSONError(w, http.StatusBadRequest, "validation_failed",
				fmt.Sprintf("at most %d findings may be fixed in one batch", maxBatchFixIDs))
			return
		}

		// Acked findings are refused: an operator deliberately decided this
		// state is intentional, and a batch fix must not quietly undo that.
		if acks != nil {
			decorated, _, err := acks.Decorate(r.Context(), svc.Findings())
			if err == nil {
				ackedIDs := make(map[string]bool)
				for _, f := range decorated {
					if f.Ack != nil {
						ackedIDs[f.ID] = true
					}
				}
				for _, id := range ids {
					if ackedIDs[id] {
						writeJSONError(w, http.StatusConflict, "conflict",
							fmt.Sprintf("finding %s is acknowledged; un-acknowledge it before fixing", id))
						return
					}
				}
			}
		}

		var ops []change.Op
		// ownerOfTarget maps an op's target to the finding that proposed it, so
		// a conflict names BOTH findings rather than reporting a nameless clash.
		ownerOfTarget := map[string]string{}
		titles := make([]string, 0, len(ids))
		for _, id := range ids {
			fixOps, title, ok := svc.FixOps(id)
			if !ok {
				writeJSONError(w, http.StatusNotFound, "not_found",
					fmt.Sprintf("no fixable finding with id %s; nothing was staged", id))
				return
			}
			for _, op := range fixOps {
				key := opConflictKey(op)
				if other, clash := ownerOfTarget[key]; clash && other != id {
					writeJSONError(w, http.StatusConflict, "conflict",
						fmt.Sprintf("findings %s and %s both change %s; fix them separately", other, id, key))
					return
				}
				ownerOfTarget[key] = id
			}
			ops = append(ops, fixOps...)
			titles = append(titles, title)
		}

		c, err := changesets.Create(r.Context(), username, batchFixTitle(titles), ops)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not create fixing changeset")
			return
		}
		writeJSON(w, http.StatusCreated, toChangesetResponse(c))
	}
}

// opConflictKey identifies what an op writes, at the granularity a conflict
// matters: two ops of the same type on the same entity. Two findings both
// proposing an MTU for vmbr0 conflict; one proposing an MTU and another a
// bridge port do not.
func opConflictKey(op change.Op) string {
	return string(op.Type) + " " + op.Target.String()
}

// batchFixTitle names the batch after what it contains. One finding keeps that
// finding's own title, so a batch of one is indistinguishable from the
// single-finding route's output.
func batchFixTitle(titles []string) string {
	if len(titles) == 1 {
		return titles[0]
	}
	return fmt.Sprintf("Fix %d findings", len(titles))
}

// dedupeStrings removes blanks and duplicates while preserving nothing about
// order — the batch is sorted so one selection always produces one changeset
// shape regardless of the order the UI happened to send.
func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
