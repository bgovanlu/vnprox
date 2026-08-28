// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/presence"
)

// capAudit is docs/api.md's audit-capability flag name
// (internal/auth.CapAudit's underlying string), spelled out as a plain
// string for the same reason capNetRead/capNetWrite are — see
// changesets.go's capNetWrite.
//
// T-2805 uses it as the IDENTITY-DISCLOSURE gate: which entities are locked
// and how many people are looking at a changeset are ordinary reads
// (netRead), but WHO holds a lock and WHO is looking are attributions of an
// action to a named person, which is what `audit` means everywhere else in
// this API (GET /audit, GET /entities/{ref}/history). AC5 of the card
// requires presence not to leak identities to a caller lacking the
// capability to see them; this is that capability, reused rather than
// invented — docs/security.md's authorization model adds no new privilege
// for a feature that needs none.
const capAuditIdentities = "audit"

// LockService is the subset of *presence.Service the lock/staging routes
// need. Declared as an interface here (the same seam pattern every other
// cross-package dependency in this package uses) so internal/api's
// dependency on internal/presence stays small and testable.
//
// Note what is NOT on it and never will be: no method that could refuse
// anything. Stage reports what it found; it does not gate. See
// internal/presence's doc.go for why that boundary is structural.
type LockService interface {
	Stage(ctx context.Context, changesetID string, refs []string, p presence.Principal, override bool) (presence.StageResult, error)
	Locks(ctx context.Context) ([]presence.Lock, error)
	ReleaseChangeset(ctx context.Context, changesetID string) (int, error)
}

// PresenceService is the subset of *presence.Service the presence read
// route needs.
type PresenceService interface {
	Scope(scope string) presence.ScopeState
	Scopes() []presence.ScopeState
}

// SessionLookup resolves the requesting principal's session id from ctx —
// the identity an advisory lock's lifetime is tied to, so that dropping the
// connection drops the lock. Implemented by cmd/vnproxd's
// authServiceAdapter via auth.IdentityFromContext; checked with a type
// assertion like UsernameLookup/CSRFEnforcer, so an AuthService test double
// that does not care about locks needs no new method.
type SessionLookup interface {
	SessionID(ctx context.Context) (string, bool)
}

// lockResponse is one advisory lock's wire shape.
//
// Holder is `omitempty` and left empty for a caller without the `audit`
// capability: the fact that an entity is spoken for is an ordinary read;
// naming the person who spoke for it is an attribution.
type lockResponse struct {
	Ref         string `json:"ref"`
	ChangesetID string `json:"changesetId"`
	Holder      string `json:"holder,omitempty"`
	AcquiredAt  int64  `json:"acquiredAt"`
	ExpiresAt   int64  `json:"expiresAt"`
	// Mine reports whether the requesting principal holds this lock. It is
	// always answerable without the identity capability — "is this me?" is
	// not an attribution of anything to anyone else.
	Mine bool `json:"mine"`
}

type locksListResponse struct {
	Locks []lockResponse `json:"locks"`
}

// changesetLocks is the additive `locks` object on a staging response.
// It is present ONLY when the staging attempt has something to say — a
// claim it stepped around, or one it took over — so a changeset staged
// against uncontended entities produces a byte-identical response to the
// pre-T-2805 one.
type changesetLocks struct {
	// Held names the unexpired locks another operator holds on entities
	// this changeset touches. This is the warning of T-2805 AC1: it names
	// the holder and their draft, and it blocks nothing — the changeset was
	// staged before this object was built.
	Held []lockResponse `json:"held,omitempty"`
	// Overridden names the locks this request deliberately took over
	// (`lockOverride: true`). Every entry has a `changeset.lock_override`
	// audit row.
	Overridden []lockResponse `json:"overridden,omitempty"`
}

// presenceViewerResponse is one viewer's wire shape.
type presenceViewerResponse struct {
	User     string `json:"user"`
	Since    int64  `json:"since"`
	Sessions int    `json:"sessions"`
}

// presenceScopeResponse is one scope's presence.
//
// Count is always present; Viewers is omitted entirely for a caller without
// the `audit` capability. The `presence.changed` WS event carries only the
// count for every subscriber regardless — see internal/presence's
// presenceEvent for why that is a structural property of the WS surface
// rather than a filter.
type presenceScopeResponse struct {
	Scope   string                   `json:"scope"`
	Viewers []presenceViewerResponse `json:"viewers,omitempty"`
	Count   int                      `json:"count"`
}

type presenceListResponse struct {
	Scopes []presenceScopeResponse `json:"scopes"`
}

// mountLockRoutes registers GET /locks and GET /presence. Both are reads
// (netRead); neither has a mutating counterpart, deliberately — a lock is
// taken by staging a draft and released by discarding it, by disconnecting,
// or by expiry, and adding a "force release" verb would be the first step
// toward treating a lock as something that must be negotiated rather than
// noticed.
func mountLockRoutes(r chi.Router, locks LockService, pres PresenceService, auth AuthService) {
	if auth == nil {
		return
	}
	if locks == nil && pres == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		if locks != nil {
			r.Get("/locks", handleListLocks(locks, auth))
		}
		if pres != nil {
			r.Get("/presence", handleGetPresence(pres, auth))
		}
	})
}

func handleListLocks(svc LockService, auth AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		held, err := svc.Locks(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read advisory locks")
			return
		}
		mine, _ := sessionIDFor(r.Context(), auth)
		out := locksListResponse{Locks: make([]lockResponse, 0, len(held))}
		for _, l := range held {
			out.Locks = append(out.Locks, toLockResponse(l, mine, canSeeIdentities(r.Context(), auth)))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func handleGetPresence(svc PresenceService, auth AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		showIdentities := canSeeIdentities(r.Context(), auth)

		var states []presence.ScopeState
		if scope := r.URL.Query().Get("scope"); scope != "" {
			if !presence.ValidScope(scope) {
				writeJSONError(w, http.StatusBadRequest, "validation_failed",
					`scope must be "changeset:<id>" or "entity:<ref>"`)
				return
			}
			// One named scope always answers, even when nobody is there:
			// "nobody else is looking at this" is the answer the UI needs,
			// and an empty list would be indistinguishable from an error.
			states = []presence.ScopeState{svc.Scope(scope)}
		} else {
			states = svc.Scopes()
		}

		out := presenceListResponse{Scopes: make([]presenceScopeResponse, 0, len(states))}
		for _, st := range states {
			entry := presenceScopeResponse{Scope: st.Scope, Count: st.Count}
			if showIdentities {
				entry.Viewers = make([]presenceViewerResponse, 0, len(st.Viewers))
				for _, v := range st.Viewers {
					entry.Viewers = append(entry.Viewers, presenceViewerResponse{
						User: v.User, Since: v.Since, Sessions: v.Sessions,
					})
				}
			}
			out.Scopes = append(out.Scopes, entry)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func toLockResponse(l presence.Lock, mySession string, showIdentity bool) lockResponse {
	out := lockResponse{
		Ref:         l.Ref,
		ChangesetID: l.ChangesetID,
		AcquiredAt:  l.AcquiredAt,
		ExpiresAt:   l.ExpiresAt,
		Mine:        mySession != "" && l.SessionID == mySession,
	}
	if showIdentity {
		out.Holder = l.Holder
	}
	return out
}

// canSeeIdentities reports whether this request may be told WHO holds a
// lock / WHO is present. It is a check, never an enforcement gate: the
// routes themselves are netRead, and a caller without `audit` gets counts
// and refs rather than a 403 — being unable to see a name is not a reason
// to refuse to say an entity is spoken for.
//
// Fail-closed: an auth backend that cannot answer (no capability seam
// wired) discloses nothing.
func canSeeIdentities(ctx context.Context, auth AuthService) bool {
	checker, ok := auth.(DiagnoseCapabilityChecker)
	if !ok {
		return false
	}
	return checker.HasCap(ctx, capAuditIdentities)
}

func sessionIDFor(ctx context.Context, auth AuthService) (string, bool) {
	lookup, ok := auth.(SessionLookup)
	if !ok {
		return "", false
	}
	return lookup.SessionID(ctx)
}

// stagePrincipal builds the lock principal for a staging request: the
// username the lock is attributed to, and the session its lifetime is tied
// to. A principal with no resolvable session id still takes locks (so the
// warning works for an API-token caller) — they simply cannot be released
// by a disconnect, only by expiry or by discarding the draft.
func stagePrincipal(ctx context.Context, auth AuthService, username string) presence.Principal {
	sessionID, _ := sessionIDFor(ctx, auth)
	return presence.Principal{Username: username, SessionID: sessionID}
}

// stageLocks records the staging principal's claim on the entities a
// changeset's ops target and turns the result into the additive `locks`
// response object — nil when there is nothing to warn about.
//
// A failure here is logged by the presence service and swallowed: the
// changeset is already staged, and failing the request because an ADVISORY
// lock could not be written would turn an advisory mechanism into a
// mandatory one through the back door.
func stageLocks(ctx context.Context, svc LockService, auth AuthService, username, changesetID string, refs []string, override bool) *changesetLocks {
	if svc == nil || changesetID == "" || len(refs) == 0 {
		return nil
	}
	res, err := svc.Stage(ctx, changesetID, refs, stagePrincipal(ctx, auth, username), override)
	if err != nil {
		// Logged, never surfaced as a failed staging: the changeset is
		// already created or updated, and turning an advisory bookkeeping
		// error into a request failure would make the mechanism mandatory
		// through the back door. Whatever Stage managed to determine before
		// failing is still worth telling the operator, so this falls through
		// rather than returning.
		slog.Default().Warn("api: recording advisory locks for a staged changeset",
			"error", err, "changeset_id", changesetID)
	}
	if !res.Warned() {
		return nil
	}
	showIdentity := canSeeIdentities(ctx, auth)
	mine, _ := sessionIDFor(ctx, auth)
	out := &changesetLocks{}
	for _, l := range res.Conflicts {
		out.Held = append(out.Held, toLockResponse(l, mine, showIdentity))
	}
	for _, l := range res.Overridden {
		out.Overridden = append(out.Overridden, toLockResponse(l, mine, showIdentity))
	}
	return out
}
