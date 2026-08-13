package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// T-2801 demo mode's API half.
//
// The card's requirement is blunt: "every write path is a no-op that
// reports what it would have done", and "a store checksum before and after
// a full staged-and-applied changeset is unchanged". This file is where
// that is true, and it is a middleware in front of the router rather than a
// check inside each handler for one reason: a per-handler check is a
// promise about every handler that exists today, and the mutating surface
// of this package is ~200 routes and still growing. A middleware in front
// of routing is a statement about the surface itself — a route added
// tomorrow is covered the day it is added, by someone who never read this
// file.
//
// It runs BEFORE authentication and capability derivation, which is
// deliberate: whether a mutation would have been permitted is a question
// about a real cluster, and in demo mode there isn't one. Answering "you
// lack netWrite" to a demo visitor would be inventing an authorization
// decision about a cluster that does not exist.

// demoModeHeader is set on every response a demo daemon produces, mutating
// or not.
//
// The banner does not depend on it (the SPA reads GET /health), but an
// operator running `curl -i` against a URL they were given, or a script
// pointed at the wrong host, gets an unmissable answer without parsing a
// body. A demo instance that is silently indistinguishable from a real one
// on the wire is the failure this header exists to prevent.
const demoModeHeader = "X-Vnprox-Demo"

// DemoWouldHave is the body every intercepted mutating request answers
// with. HTTP 200: the request was understood, well-formed and accepted as
// far as demo mode goes — it simply had no effect, which the body says.
//
// A 4xx would be wrong twice: it would make a demo look broken, and it
// would claim the request was invalid when the only thing wrong with it is
// that there is no cluster to apply it to.
type DemoWouldHave struct {
	Demo DemoAction `json:"demo"`
}

// DemoAction is DemoWouldHave's payload. Mode is always "demo" so a client
// can branch on the presence of this envelope without matching on prose.
type DemoAction struct {
	Mode      string `json:"mode"`
	WouldHave string `json:"wouldHave"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Detail    string `json:"detail"`
}

// demoAllowedWrites are the only mutating routes a demo daemon actually
// performs.
//
// Exactly the session lifecycle, and nothing else. A demo whose login POST
// were intercepted would answer "I would have logged you in" and then show
// no screens at all, so this is not a loophole in the write refusal — it is
// what makes there be anything to refuse writes *on*. Every one of these
// writes lands in the sessions table and touches neither PVE nor any
// network-config-bearing table.
//
// Deliberately a closed set of exact paths, not a prefix: "/auth/" as a
// prefix would silently admit any future mutating route under it.
//
// A consequence worth stating plainly, because it is not obvious: several
// READ surfaces in this API are POST-shaped (POST /simulate/path, POST
// /diagnose, the MCP transport). They are intercepted too. That is a real
// cost — those screens answer "would have" in a demo — and it is taken
// deliberately: AC2 says "every mutating API", and an allowlist of "posts
// that are really reads" is a list someone has to keep correct forever,
// with a store checksum as the only thing standing behind it. See
// docs/features/demo-mode.md for the list and the tracked follow-up.
var demoAllowedWrites = map[string]bool{
	"/api/v1/auth/login":         true,
	"/api/v1/auth/logout":        true,
	"/api/v1/auth/oidc/callback": true,
}

// demoRefusedEndpointPrefixes are the routes that configure a way to reach
// another cluster — T-2801 AC3's second direction: "a real endpoint cannot
// be configured while in demo mode".
//
// These get a hard, named refusal rather than the "would have" answer every
// other mutating route gets, because "I would have attached your production
// cluster" is not a sentence a demo should be able to say. The distinction
// is the whole point of AC3: refusing to *pretend* is different from
// refusing to *do*.
//
// Note this is belt to internal/demo's braces. A demo daemon's PVE clients
// are built on a transport with no dialer at all (internal/demo/
// transport.go), so even a stored endpoint could not be reached. This
// middleware is what makes the refusal legible to the operator instead of a
// row that silently never connects.
// Deliberately short, and deliberately only routes that EXIST. A prefix for
// a route this router does not serve would make the assertion below pass
// against nothing, which is worse than not asserting it: the middleware runs
// in front of routing, so it would answer 403 for a path that would
// otherwise have been a 404.
//
// Routes that configure an outbound DELIVERY target rather than a cluster —
// /webhooks, /alert-rules — are not here on purpose. They get the ordinary
// would-have answer, which is truthful (nothing is stored, nothing is sent)
// and keeps this error code meaning one specific thing.
var demoRefusedEndpointPrefixes = []string{
	// Attaches another PVE cluster by apiUrl (docs/api.md, Federation).
	"/api/v1/federation/clusters",
	// Registers a Kubernetes API server by apiUrl (docs/api.md, Kubernetes).
	"/api/v1/k8s/clusters",
}

// demoWriteMiddleware answers every mutating request with either a
// "would have" result or, for the endpoint-configuring routes above, a
// named refusal. GET/HEAD/OPTIONS pass straight through.
func demoWriteMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(demoModeHeader, "1")

		if !isMutatingMethod(r.Method) || demoAllowedWrites[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		if demoRefusesEndpointConfig(r.URL.Path) {
			writeJSONError(w, http.StatusForbidden, "demo_real_endpoint_refused",
				"this daemon is running in demo mode against an embedded synthetic cluster; a real endpoint cannot be configured while in demo mode. Restart without --demo to manage a real cluster.")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(DemoWouldHave{Demo: DemoAction{
			Mode:      "demo",
			WouldHave: r.Method + " " + r.URL.Path,
			Method:    r.Method,
			Path:      r.URL.Path,
			Detail: "Demo mode: this request was accepted and had no effect. Nothing was written to this daemon's store " +
				"and nothing was sent to any cluster — there is no cluster, only the embedded synthetic one.",
		}})
	})
}

// isMutatingMethod is the method half of "mutating". Everything that is not
// a documented safe method counts, including anything unrecognized: a
// method this router does not know is not a method demo mode may assume is
// harmless.
func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func demoRefusesEndpointConfig(path string) bool {
	for _, p := range demoRefusedEndpointPrefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
