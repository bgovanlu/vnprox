// Package ingress implements T-1406's read-only reverse-proxy discovery:
// HAProxy, nginx, Caddy, and Traefik, polled only via their own status/
// config endpoints, and only for targets the operator explicitly added to
// the ingress_targets app-store table (internal/store/ingress.go). This
// package never scans a network range and never issues a mutating call —
// see IngressDiscoverer's doc comment for the exact invariant every
// implementation in this package (and every future one T-1702's plugin SDK
// registers) must uphold.
//
// # The read-only invariant
//
// Every *Discoverer type in this package issues only HTTP GET requests
// against Target.Address — never POST/PUT/PATCH/DELETE. This is
// grep-verifiable: no file in this package (excluding _test.go fixtures,
// which simulate a *target*'s server, not a discoverer's client behavior)
// references http.MethodPost, http.MethodPut, http.MethodPatch, or
// http.MethodDelete. zerowrite_test.go asserts this both by source
// inspection and by driving every discoverer against an instrumented
// httptest server that records every request method it receives.
//
// # Why an interface this narrow
//
// IngressDiscoverer is deliberately a single method, Discover(ctx, Target)
// (ProxyState, error) — no config surface, no lifecycle hooks, nothing
// vendor-specific leaking into the seam. Phase 17's plugin SDK (T-1702) is
// expected to let a plugin register additional Kind values against the
// same Registry (registry.go) without vnprox's core changing at all: a
// plugin author implements this one method, and internal/api's /ingress
// routes and internal/edge's chain-drawing logic keep working unmodified.
// The four vendor implementations in this package, plus their status-
// endpoint doubles in ./ingressmock, are meant to double as T-1702's own
// conformance set — a future plugin discoverer should behave identically
// against the same fixture shapes these tests already assert against.
//
// # Storage
//
// ingress_targets (internal/store/ingress.go) holds app-owned intent only —
// which targets to poll, and how to authenticate to them — never a
// snapshot of the polled proxy's own state. GET /ingress/status calls
// Discover fresh on every request; nothing this package returns is ever
// persisted as authoritative (docs/architecture.md §7's new-domain
// invariant, carried forward from T-1401/T-1403/T-1404's own packages).
package ingress
