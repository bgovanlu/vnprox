package main

import (
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
