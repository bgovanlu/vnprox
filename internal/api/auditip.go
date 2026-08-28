// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net"
	"net/http"

	"github.com/bgovanlu/vnprox/internal/store"
)

// auditIPMiddleware (T-2902) stamps the requesting client's source IP into
// the request context via store.WithAuditClientIP, so every audit append
// downstream — the change engine's, auth's, any handler's — can record the
// "source IP" docs/security.md's Audit section documents without threading
// an extra parameter through every call path in between.
//
// RemoteAddr only, deliberately: the documented topology has no reverse
// proxy in front of :8007 (internal/auth's clientIP makes the same call for
// the login limiter and its own audit rows), so honoring X-Forwarded-For
// here would let any client spoof its audited IP with one header. If a
// trusted-proxy mode ever lands (see the T-2905 punch list's limiter note),
// both sites change together.
func auditIPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			// No port (unusual, but e.g. a unix-socket test transport):
			// record what we have rather than dropping attribution.
			host = r.RemoteAddr
		}
		next.ServeHTTP(w, r.WithContext(store.WithAuditClientIP(r.Context(), host)))
	})
}
