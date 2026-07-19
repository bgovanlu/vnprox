package main

import (
	"context"
	"net/http"

	"github.com/bgovanlu/vnprox/internal/auth"
)

// authServiceAdapter bridges the concrete *auth.Service T-105 built to
// internal/api's AuthService interface. MountRoutes and SessionMiddleware
// already match the interface via promotion; only RequireCap needs an
// adapting method, since internal/api deliberately keeps its side of that
// seam typed as a plain capability-name string (see internal/api/router.go's
// doc comment on AuthService) rather than importing internal/auth's own Cap
// type — this is the one place that knows about both.
type authServiceAdapter struct {
	*auth.Service
}

// RequireCap converts a plain capability name (e.g. "netRead") to
// internal/auth's Cap type and delegates. This method's own name shadows
// the embedded *auth.Service.RequireCap(auth.Cap) for callers using this
// adapter type, which is exactly the point.
func (a authServiceAdapter) RequireCap(cap string) func(http.Handler) http.Handler {
	return a.Service.RequireCap(auth.Cap(cap))
}

// Username resolves the authenticated username from ctx (populated by
// SessionMiddleware), satisfying internal/api's UsernameLookup interface so
// the layouts routes can key a saved layout to a user without internal/api
// importing internal/auth directly.
func (a authServiceAdapter) Username(ctx context.Context) (string, bool) {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return "", false
	}
	return id.Username, true
}

// HasCap satisfies internal/api's DiagnoseCapabilityChecker interface
// (T-1307's POST /diagnose capture-escalation step): reports whether ctx's
// authenticated session holds the named capability, without 403ing when it
// doesn't — unlike RequireCap above, this is a check, not an enforcement
// gate, so the diagnose ladder can mark its capture step "skipped" rather
// than fail the whole request. Mirrors RequireCap's own "any node" scoping
// (id.HasCap("", cap)): POST /diagnose carries no chi "node" URL param to
// scope a check to.
func (a authServiceAdapter) HasCap(ctx context.Context, cap string) bool {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return false
	}
	return id.HasCap("", auth.Cap(cap))
}

// ValidateTokenScopes satisfies internal/api's TokenMinter interface
// (T-1104's POST /tokens): it normalizes ctx's authenticated session's
// requested scopes against both the full capability vocabulary
// (auth.ParseScopes) and that session's own derived capabilities
// (auth.Identity.ValidateScopeGrant) in one call, translating either
// failure into the exact (status, code, message) internal/api's
// writeJSONError should respond with — this is the one place that knows
// about both internal/api's plain-string convention and internal/auth's
// Cap/sentinel-error types, the same role RequireCap's adapter method
// above plays for capability names.
func (a authServiceAdapter) ValidateTokenScopes(ctx context.Context, rawScopes []string) (scopes []string, status int, code, message string, ok bool) {
	id, idOK := auth.IdentityFromContext(ctx)
	if !idOK {
		return nil, http.StatusUnauthorized, "not_authenticated", "not logged in", false
	}
	parsed, err := auth.ParseScopes(rawScopes)
	if err != nil {
		return nil, http.StatusBadRequest, "validation_failed", err.Error(), false
	}
	if err := id.ValidateScopeGrant(parsed); err != nil {
		return nil, http.StatusForbidden, "forbidden", err.Error(), false
	}
	return auth.ScopeStrings(parsed), 0, "", "", true
}

// GenerateToken satisfies internal/api's TokenMinter interface, delegating
// to auth.GenerateAPIToken so internal/api never needs its own random-token
// generation logic.
func (a authServiceAdapter) GenerateToken() (raw, hash string, err error) {
	return auth.GenerateAPIToken()
}
