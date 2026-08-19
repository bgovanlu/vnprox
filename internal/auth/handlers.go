package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/store"
)

// MountRoutes registers docs/api.md's Auth routes onto r:
// POST /auth/login (public — there is no session yet to protect),
// POST /auth/logout and GET /auth/me (behind SessionMiddleware +
// CSRFMiddleware). This is the pattern later route registrations (T-106
// topology, and eventually the change-engine) should copy: wrap a
// chi.Router group with s.SessionMiddleware then s.CSRFMiddleware, then
// apply s.RequireCap(...) per-route or per-group as needed.
func (s *Service) MountRoutes(r chi.Router) {
	r.Post("/auth/login", s.handleLogin)
	// T-1207: OIDC SSO login/callback are public (there is no session yet to
	// protect, exactly like /auth/login) and, being non-cookie, CSRF-exempt on
	// the callback POST. Mounted only when [oidc] is configured.
	if s.oidc != nil {
		r.Get("/auth/oidc/login", s.handleOIDCLogin)
		r.Post("/auth/oidc/callback", s.handleOIDCCallback)
	}
	r.Group(func(r chi.Router) {
		r.Use(s.SessionMiddleware)
		r.Use(s.CSRFMiddleware)
		r.Post("/auth/logout", s.handleLogout)
		r.Get("/auth/me", s.handleMe)
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Realm    string `json:"realm"`
	OTP      string `json:"otp"`
}

type authUser struct {
	Username string `json:"username"`
	Realm    string `json:"realm"`
}

type meResponse struct {
	Caps map[string]Capabilities `json:"caps"`
	User authUser                `json:"user"`
}

// clientIP extracts the caller's address for rate limiting/audit, per
// docs/security.md's threat model ("Credential stuffing on 8007": per-IP
// rate limits). RemoteAddr is host:port; a proxy-forwarded header scheme
// is a deployment-specific concern beyond this task's scope (vnprox is
// meant to be reached directly, per docs/deployment.md's port table — no
// reverse proxy in the documented topology).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := clientIP(r)

	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body", nil)
		return
	}
	if req.Username == "" || req.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "username and password are required", nil)
		return
	}
	if req.Realm == "" && !strings.Contains(req.Username, "@") {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "realm is required (or embed it in username as user@realm)", nil)
		return
	}

	if !s.limiter.allow(ip, req.Username) {
		s.appendAudit(ctx, req.Username, "login", "rate_limited", ip, nil)
		writeJSONError(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts, try again later", nil)
		return
	}

	identity, err := s.newIdentity(req.Username, req.Password, req.Realm, req.OTP)
	if err != nil {
		s.log.Error("auth: constructing PVE identity", "username", req.Username, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	ticket, csrf, err := identity.Login(ctx)
	if err != nil {
		s.handleLoginFailure(w, ctx, req.Username, ip, err)
		return
	}

	caps, err := s.deriveCapabilities(ctx, identity)
	if err != nil {
		// Capability derivation is a secondary, vnprox-enforced layer
		// (docs/security.md §"Authorization"); PVE's own ACL check on the
		// user's ticket remains authoritative regardless. Rather than
		// fail the whole login over it, log and default to no
		// capabilities at all (fail closed) — the hourly re-derivation
		// (renewal.go) will retry.
		s.log.Warn("auth: deriving capabilities failed, defaulting to no capabilities", "username", req.Username, "error", err)
		caps = map[string]Capabilities{"": {}}
	}

	if err := s.startSession(ctx, w, identity, req.Username, req.Realm, ticket, csrf, caps); err != nil {
		s.log.Error("auth: establishing session", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	s.appendAudit(ctx, req.Username, "login", "success", ip, nil)
	writeJSON(w, http.StatusOK, meResponse{
		User: authUser{Username: req.Username, Realm: req.Realm},
		Caps: caps,
	})
}

// startSession persists a new session for an authenticated identity, registers
// it in this process's live map for renewal/hourly re-derivation, and sets the
// session + CSRF cookies. Shared by the PVE ticket bridge (handleLogin) and the
// OIDC callback (handleOIDCCallback) so both converge on one session shape.
// ticket is the PVE ticket ("" for an OIDC session, which has none); csrf is the
// double-submit secret; caps the already-derived (and, for OIDC, PVE-capped)
// capability map.
func (s *Service) startSession(ctx context.Context, w http.ResponseWriter, identity PVEIdentity, username, realm, ticket, csrf string, caps map[string]Capabilities) error {
	sessionID, err := newSessionID()
	if err != nil {
		return err
	}
	capsJSONStr, err := capsJSON(caps)
	if err != nil {
		return err
	}
	now := s.now()
	rec := store.Session{
		ID:        sessionID,
		Username:  username,
		Realm:     realm,
		PVETicket: ticket,
		CSRFToken: csrf,
		CapsJSON:  capsJSONStr,
		CreatedAt: now.Unix(),
		ExpiresAt: now.Add(s.idleTimeout).Unix(),
	}
	if err := s.sessions.Insert(ctx, rec); err != nil {
		return fmt.Errorf("auth: inserting session: %w", err)
	}
	s.mu.Lock()
	s.live[sessionID] = &liveSession{identity: identity, lastCapRefresh: now}
	s.mu.Unlock()

	setSessionCookies(w, sessionID, csrf, s.hardTimeout)
	return nil
}

func (s *Service) handleLoginFailure(w http.ResponseWriter, ctx context.Context, username, ip string, err error) {
	var authErr *pve.ErrPVEAuth
	if errors.As(err, &authErr) {
		s.appendAudit(ctx, username, "login", "denied", ip, map[string]any{"reason": authErr.Message})
		writeJSONError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username, password, realm, or OTP", nil)
		return
	}

	s.log.Error("auth: PVE login failed", "username", username, "error", err)
	s.appendAudit(ctx, username, "login", "error", ip, map[string]any{"reason": err.Error()})
	writeJSONError(w, http.StatusBadGateway, "pve_unreachable", "could not reach the PVE API", nil)
}

// deriveCapabilities fetches the just-authenticated user's PVE permission
// set and the cluster's node list, and derives per-node capabilities. See
// caps.go's BuildCapabilities doc for the empty-nodes fallback.
func (s *Service) deriveCapabilities(ctx context.Context, identity PVEIdentity) (map[string]Capabilities, error) {
	perms, err := identity.Permissions(ctx)
	if err != nil {
		return nil, err
	}
	nodes, err := identity.ClusterNodes(ctx)
	if err != nil {
		// A user without Sys.Audit on "/" can't list cluster nodes; that's
		// expected (not an error worth failing derivation over) — fall
		// back to the cluster-wide-only capability entry.
		var denied *pve.ErrPVEDenied
		if errors.As(err, &denied) {
			nodes = nil
		} else {
			return nil, err
		}
	}
	caps := BuildCapabilities(perms, nodes)
	// T-1207: an OIDC identity caps its PVE-derived capabilities at the
	// group→role bundle it authenticated with (capLimiter). Applied here so
	// BOTH login derivation and the hourly re-derivation (renewal.go) enforce
	// the OIDC ceiling — the bundle can only ever narrow the PVE-derived caps,
	// never widen them (docs/security.md's authn/authz split).
	if lim, ok := identity.(capLimiter); ok {
		bundle := lim.capBundle()
		for node, c := range caps {
			caps[node] = IntersectCaps(c, bundle)
		}
	}
	if s.readOnly {
		forceReadOnly(caps)
	}
	return caps, nil
}

// forceReadOnly zeroes the write-shaped flags in place across every node's
// Capabilities, per Config.ReadOnly — the config's "observe-only until you
// trust it" mode (docs/features/blueprints.md §3). Applied on both the
// cookie path (above) and, since T-2903, the bearer path
// (middleware.go:200).
//
// T-3003-followup-01 (2026-08-19, owner decision): the original four
// (NetWrite/SDNWrite/FWWrite/GuestNet) were not the whole write surface.
// Two more capabilities gated genuinely mutating routes and used to survive
// this function unchanged:
//
//   - Capture — POST /captures and POST /captures/{id}/stop
//     (internal/api/captures.go:94-95) start and stop real packet captures on
//     hosts. docs/security.md's own capture paragraph argues capture is
//     "at least as strict as netWrite's" gate and a "materially stronger
//     read", so read_only now clears it entirely — including the list/get/
//     download routes, since internal/api/captures.go gates all four on the
//     single Capture flag with no read/write split of its own.
//   - Automation — POST /webhooks registers an outbound destination the
//     daemon will then POST to; DELETE /webhooks/{id} removes one
//     (internal/api/webhooks.go). Automation used to be ONE flag gating both
//     that write surface AND the read-only WS "events" topic + GET
//     /webhooks, so clearing it outright would have taken the read
//     capability away with it. caps.go's Capabilities.Automation/
//     AutomationWrite split it in two: Automation (the read half) is left
//     untouched here; AutomationWrite (the write half) is cleared below,
//     the same as Capture.
//
// TestForceReadOnly_PinsExactlyWhichFlagsItClears pins the resulting
// behaviour: NetWrite/SDNWrite/FWWrite/GuestNet/Capture/AutomationWrite are
// cleared; NetRead/SDNRead/FWRead/Audit/Automation are preserved.
//
// Found 2026-08-16 by the T-3003 agent, reading this function to render a
// token's effective scope honestly rather than trusting its documentation;
// fixed 2026-08-19 per the owner's decision recorded in
// planning/tasks/debt-sweep-2026-08-19.md.
func forceReadOnly(caps map[string]Capabilities) {
	for node, c := range caps {
		c.NetWrite = false
		c.SDNWrite = false
		c.FWWrite = false
		c.GuestNet = false
		c.Capture = false
		c.AutomationWrite = false
		caps[node] = c
	}
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rec, ok := sessionFromContext(ctx)
	if !ok {
		// SessionMiddleware only calls next when a session was resolved.
		writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in", nil)
		return
	}

	if err := s.sessions.Delete(ctx, rec.Identity.SessionID); err != nil {
		s.log.Error("auth: deleting session", "session_id", logSessionID(rec.Identity.SessionID), "error", err)
	}
	s.mu.Lock()
	delete(s.live, rec.Identity.SessionID)
	s.mu.Unlock()

	s.appendAudit(ctx, rec.Identity.Username, "logout", "success", clientIP(r), nil)
	clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	rec, ok := sessionFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in", nil)
		return
	}
	writeJSON(w, http.StatusOK, meResponse{
		User: authUser{Username: rec.Identity.Username, Realm: rec.Identity.Realm},
		Caps: rec.Identity.Caps,
	})
}

func (s *Service) appendAudit(ctx context.Context, username, action, result, ip string, detail map[string]any) {
	var detailJSON string
	full := map[string]any{"ip": ip}
	for k, v := range detail {
		full[k] = v
	}
	if b, err := json.Marshal(full); err == nil {
		detailJSON = string(b)
	}
	entry := store.AuditEntry{
		At:       s.now().Unix(),
		Username: username,
		Action:   action,
		Result:   result,
		// T-2902: the ip parameter now also lands in the first-class column
		// every other append site uses; the detail_json copy above is kept
		// so pre-0047 consumers of these rows keep working unchanged.
		IP:         ip,
		DetailJSON: nullString(detailJSON),
	}
	if _, err := s.audit.Append(ctx, entry); err != nil {
		s.log.Error("auth: appending audit entry", "action", action, "error", err)
	}
}

func setSessionCookies(w http.ResponseWriter, sessionID, csrfToken string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(maxAge.Seconds()),
	})
	// The CSRF cookie must be JS-readable (double-submit pattern: the
	// frontend reads it and echoes it back as X-VNPROX-CSRF), so it is
	// deliberately NOT HttpOnly. Still Secure + SameSite=Strict.
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    csrfToken,
		Path:     "/",
		HttpOnly: false,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(maxAge.Seconds()),
	})
}

func clearSessionCookies(w http.ResponseWriter) {
	for _, name := range []string{SessionCookieName, CSRFCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: name == SessionCookieName,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
	}
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONError writes docs/api.md's documented error envelope:
// {"error": {"code", "message", "details"}}. This package cannot reuse
// internal/api's unexported writeJSONError (no details support, and it's
// unexported besides), so it has its own copy — the shape is a contract
// (docs/api.md), not shared logic worth coupling packages over.
func writeJSONError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	body := map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
	if details != nil {
		body["error"].(map[string]any)["details"] = details
	}
	_ = json.NewEncoder(w).Encode(body)
}
