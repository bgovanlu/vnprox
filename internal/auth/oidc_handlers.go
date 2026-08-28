// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"encoding/json"
	"net/http"
	"time"
)

// oidcLoginResponse is GET /auth/oidc/login's body: the IdP authorization URL
// the SPA redirects the browser to, plus the opaque state it will echo back on
// the callback. The PKCE verifier and nonce stay server-side (OIDCService.begin)
// and never reach the browser.
type oidcLoginResponse struct {
	AuthorizationURL string `json:"authorizationUrl"`
	State            string `json:"state"`
}

// oidcCallbackRequest is POST /auth/oidc/callback's body: the authorization code
// and state the SPA received on the IdP's redirect back to vnprox's redirect_uri.
type oidcCallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

// handleOIDCLogin (GET /auth/oidc/login) starts an authorization-code + PKCE
// flow: it mints server-side state/nonce/verifier and returns the IdP
// authorization URL for the SPA to navigate to. Public — there is no session
// yet.
func (s *Service) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeJSONError(w, http.StatusNotFound, "not_found", "OIDC SSO is not configured", nil)
		return
	}
	state, nonce, challenge, err := s.oidc.begin()
	if err != nil {
		s.log.Error("auth: starting oidc flow", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}
	authURL, err := s.oidc.provider.AuthCodeURL(r.Context(), state, nonce, challenge)
	if err != nil {
		s.log.Error("auth: building oidc authorization url", "error", err)
		writeJSONError(w, http.StatusBadGateway, "oidc_unreachable", "could not reach the OIDC provider", nil)
		return
	}
	writeJSON(w, http.StatusOK, oidcLoginResponse{AuthorizationURL: authURL, State: state})
}

// handleOIDCCallback (POST /auth/oidc/callback) completes the flow: it redeems
// the pending state, exchanges the code for tokens, verifies the ID token,
// derives the OIDC group→role bundle, caps it at the linked PVE identity's PVE
// ACLs (the authn/authz split), and establishes a session with the identical
// security properties as a PVE-ticket-bridge session. Public + CSRF-exempt (no
// session/cookie exists yet), exactly like POST /auth/login.
func (s *Service) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeJSONError(w, http.StatusNotFound, "not_found", "OIDC SSO is not configured", nil)
		return
	}
	ctx := r.Context()
	ip := clientIP(r)

	var req oidcCallbackRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body", nil)
		return
	}
	if req.Code == "" || req.State == "" {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "code and state are required", nil)
		return
	}

	verifier, nonce, ok := s.oidc.redeem(req.State)
	if !ok {
		s.appendAudit(ctx, "", "login", "denied", ip, map[string]any{"method": "oidc", "reason": "unknown or expired state"})
		writeJSONError(w, http.StatusBadRequest, "invalid_state", "unknown or expired authorization state", nil)
		return
	}

	tr, err := s.oidc.provider.Exchange(ctx, req.Code, verifier)
	if err != nil {
		s.log.Warn("auth: oidc code exchange failed", "error", err)
		s.appendAudit(ctx, "", "login", "denied", ip, map[string]any{"method": "oidc", "reason": "code exchange failed"})
		writeJSONError(w, http.StatusBadGateway, "oidc_unreachable", "could not exchange the authorization code", nil)
		return
	}

	claims, err := s.oidc.provider.VerifyIDToken(ctx, tr.IDToken, nonce)
	if err != nil {
		s.log.Warn("auth: oidc id-token verification failed", "error", err)
		s.appendAudit(ctx, "", "login", "denied", ip, map[string]any{"method": "oidc", "reason": "id-token verification failed"})
		writeJSONError(w, http.StatusUnauthorized, "invalid_oidc_token", "the OIDC identity token could not be verified", nil)
		return
	}

	username := claims.Username()
	bundle := MapGroupsToBundle(claims.Groups, s.oidc.mappings)

	// Resolve the per-cluster PVE authorization linkage (the authn/authz split).
	// A resolver error is treated as "no linkage" (fail closed) rather than
	// failing the whole login — the user is still authenticated, just without
	// cluster-scoped capability until a linkage exists.
	var linked PVEIdentity
	var pveUser string
	if s.oidc.resolver != nil {
		identity, resolvedUser, linkOK, rErr := s.oidc.resolver.ResolvePVE(ctx, s.oidc.clusterID, claims.Groups)
		if rErr != nil {
			s.log.Warn("auth: resolving oidc pve linkage", "user", username, "error", rErr)
		} else if linkOK {
			linked = identity
			pveUser = resolvedUser
		}
	}

	csrf, err := randomToken()
	if err != nil {
		s.log.Error("auth: generating oidc csrf token", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	identity := newOIDCIdentity(s.oidc.provider, linked, bundle, csrf, tr.RefreshToken, time.Duration(tr.ExpiresIn)*time.Second, s.now)

	caps, err := s.deriveCapabilities(ctx, identity)
	if err != nil {
		// deriveCapabilities failing (the linked PVE identity's permission read
		// failed) is not fatal — fall back to no capabilities (fail closed);
		// the hourly re-derivation retries. Same posture as handleLogin.
		s.log.Warn("auth: deriving oidc capabilities failed, defaulting to none", "user", username, "error", err)
		caps = map[string]Capabilities{"": {}}
	}

	const oidcRealm = "oidc"
	if err := s.startSession(ctx, w, identity, username, oidcRealm, "", csrf, caps); err != nil {
		s.log.Error("auth: establishing oidc session", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	s.appendAudit(ctx, username, "login", "success", ip, map[string]any{"method": "oidc", "pveLink": pveUser})
	writeJSON(w, http.StatusOK, meResponse{
		User: authUser{Username: username, Realm: oidcRealm},
		Caps: caps,
	})
}
