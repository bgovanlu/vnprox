package ingress

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Kind names one supported reverse-proxy vendor. A future T-1702 plugin
// registers a new Kind value against Registry (registry.go) rather than
// this package growing a fifth vendor implementation.
type Kind string

const (
	KindHAProxy Kind = "haproxy"
	KindNginx   Kind = "nginx"
	KindCaddy   Kind = "caddy"
	KindTraefik Kind = "traefik"
)

// ValidKinds lists every Kind this package's default Registry ships a
// discoverer for — internal/api's target-creation validation reuses this
// exact set (docs/api.md's Ingress visibility section, `kind` field).
var ValidKinds = []Kind{KindHAProxy, KindNginx, KindCaddy, KindTraefik}

// Target is one operator-configured reverse-proxy endpoint to poll — the
// in-memory shape an ingress_targets row is decoded into before a Discover
// call. Never constructed from anything but a row in that table (or a
// test fixture): no code path in this package or its callers ever
// synthesizes a Target from network discovery/scanning (T-1406 AC5).
type Target struct {
	ID string
	// Kind selects which registered IngressDiscoverer handles this target.
	Kind Kind
	// Address is the target's own status/admin endpoint base URL, e.g.
	// "http://10.0.0.5:8404" for an HAProxy stats page, or
	// "http://10.0.0.7:2019" for a Caddy admin API. Each discoverer
	// documents exactly which path it appends.
	Address string
	// Credential is an optional decrypted bearer-token/basic-auth secret,
	// held in memory only for the duration of one Discover call — never
	// logged, never returned by any API response. The sealed form
	// (ingress_targets.credential_enc) is internal/api/ingress.go's
	// concern, not this package's.
	Credential string
}

// Backend is one discovered backend/upstream server behind a proxy target.
// Address is whatever the vendor's status endpoint reports as the dial
// target for that backend — a bare "host:port" for vendors whose status
// endpoint reports one (nginx Plus, Caddy, Traefik), or just a server name
// for vendors whose classic stats output doesn't (HAProxy's CSV stats page
// when its optional `addr` column is absent) — GuestLookup-based
// correlation (chains.go) simply finds nothing for the latter case rather
// than guessing.
type Backend struct {
	// Route names the frontend/service/upstream-pool that owns this
	// backend, when the vendor's status endpoint reports one.
	Route   string
	Address string
	// Detail carries vendor-specific extra status text (e.g. HAProxy's own
	// server name when Address had to fall back to it), optional.
	Detail  string
	Healthy bool
}

// ProxyState is one Discover call's result — a snapshot of a single
// target's currently configured routes/backends. Never persisted as
// authoritative state (see doc.go's Storage section): GET /ingress/status
// calls Discover fresh on every request.
type ProxyState struct {
	TargetID string
	Kind     Kind
	// Error is set when Reachable is false — a human-readable reason
	// (connection refused, non-2xx status, malformed body, ...).
	Error     string
	Backends  []Backend
	Reachable bool
}

// IngressDiscoverer is the read-only reverse-proxy discovery seam T-1702's
// future plugin SDK will make pluggable: exactly one method, taking a
// Target and returning its currently discovered ProxyState. Every
// implementation in this package issues only read-only HTTP GET requests
// against Target.Address — never a mutating call (see doc.go's "read-only
// invariant" section), and never any target this interface wasn't
// explicitly handed by its caller (internal/api/ingress.go iterates
// exactly the ingress_targets table, nothing else).
type IngressDiscoverer interface {
	Discover(ctx context.Context, target Target) (ProxyState, error)
}

// Registry dispatches Discover to the IngressDiscoverer registered for a
// Target's Kind. This is the concrete seam T-1702 extends: a plugin
// registers Registry[newKind] = pluginDiscoverer, and every caller holding
// a Registry (which itself satisfies IngressDiscoverer, so callers only
// ever depend on the one-method interface, never this concrete type)
// immediately gains the new vendor with no change to internal/api's routes
// or internal/ingress's chain-correlation logic.
type Registry map[Kind]IngressDiscoverer

// Discover implements IngressDiscoverer by dispatching to the registered
// discoverer for target.Kind. An unregistered Kind is reported as an
// unreachable target with a descriptive error, not a panic or a silently
// skipped target — a target row naming a Kind no build of vnprox has a
// discoverer for (e.g. one only a since-removed plugin understood) still
// shows up in GET /ingress/status, just as permanently unreachable.
func (reg Registry) Discover(ctx context.Context, target Target) (ProxyState, error) {
	d, ok := reg[target.Kind]
	if !ok {
		return ProxyState{
			TargetID: target.ID, Kind: target.Kind, Reachable: false,
			Error: fmt.Sprintf("no discoverer registered for kind %q", target.Kind),
		}, nil
	}
	return d.Discover(ctx, target)
}

// defaultHTTPTimeout bounds every discoverer's outbound call — GET
// /ingress/status blocks on these, so a single unreachable target must
// never hang the whole response.
const defaultHTTPTimeout = 5 * time.Second

// maxDiscoverBodyBytes bounds how much of a status-endpoint response any
// discoverer in this package reads — a generous cap for a proxy's own
// stats/config output, guarding against a misbehaving or malicious target
// streaming an unbounded body back.
const maxDiscoverBodyBytes = 4 << 20 // 4 MiB

// NewDefaultRegistry builds the Registry backing every shipped vendor
// (HAProxy/nginx/Caddy/Traefik), all sharing client (a nil client gets a
// package-default one with defaultHTTPTimeout). This is what
// cmd/vnproxd wires into router.Options.IngressDiscoverers; tests
// typically build a narrower Registry by hand instead.
func NewDefaultRegistry(client *http.Client) Registry {
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return Registry{
		KindHAProxy: &HAProxyDiscoverer{Client: client},
		KindNginx:   &NginxDiscoverer{Client: client},
		KindCaddy:   &CaddyDiscoverer{Client: client},
		KindTraefik: &TraefikDiscoverer{Client: client},
	}
}

// newRequest builds a GET request against target's address + suffix,
// attaching Credential as a bearer token when set — the one shared piece
// of request-building every vendor discoverer in this package uses, so the
// "GET-only, never a mutating verb" invariant (doc.go) has exactly one
// place where a method string is written.
func newRequest(ctx context.Context, target Target, suffix string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.Address+suffix, nil)
	if err != nil {
		return nil, fmt.Errorf("ingress: building request for target %s: %w", target.ID, err)
	}
	if target.Credential != "" {
		req.Header.Set("Authorization", "Bearer "+target.Credential)
	}
	return req, nil
}
