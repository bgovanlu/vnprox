// Package apidoc turns vnprox's registered HTTP routes into an OpenAPI 3.1
// document (T-2405).
//
// The document is built from the routes the chi router *actually has*, walked
// at construction time, rather than from a hand-maintained list of paths. That
// is the whole design: a hand-maintained path list drifts silently, and the
// 1,316 lines of Markdown in docs/api.md are the existing proof — a route can
// be added today with no mechanical signal that the contract grew.
//
// What is hand-maintained is the *metadata* — summary, tag, and which
// authentication a route expects — held in Operations (routes.go). A route
// with no entry there is a build failure, not a silently under-described
// operation; see Missing.
//
// SCOPE, stated plainly. This document describes every route's path, method,
// path parameters, security scheme and error responses. It does NOT describe
// request or response *bodies* beyond the shared error envelope: 215 operations'
// worth of body schemas is a separate piece of work, and claiming schema
// coverage we do not have would make generated clients worse than no clients.
// The successful-response schema is deliberately an open object. Callers that
// need body shapes still read docs/api.md.
package apidoc

import (
	"fmt"
	"sort"
	"strings"
)

// Route is one (method, pattern) pair as chi.Walk reports it. Pattern is
// chi's own templating — `{id}`, not `:id` — which is also OpenAPI's, so the
// two agree without translation. That agreement is asserted by a test rather
// than assumed, because it is the kind of thing that is true until a router
// swap makes it quietly false.
type Route struct {
	Method  string
	Pattern string
}

// Key is the table key for a route: "GET /api/v1/health".
func Key(method, pattern string) string {
	return strings.ToUpper(method) + " " + pattern
}

// Key is the receiver form, for convenience at call sites that already hold a
// Route.
func (r Route) Key() string { return Key(r.Method, r.Pattern) }

// AuthKind is how a route authenticates. It is metadata rather than something
// derived from the router, because chi middleware is opaque to a walk: the
// walk can see that a route has three middlewares, not that one of them is a
// session check. Recording it by hand and asserting it against real requests
// (see the reachability tests) is the honest arrangement.
type AuthKind string

const (
	// AuthNone is a route deliberately served without credentials.
	AuthNone AuthKind = "none"
	// AuthSession is the SPA's session cookie plus CSRF header — the great
	// majority of /api/v1.
	AuthSession AuthKind = "session"
	// AuthBearer is an API token in an Authorization header (MCP, embed).
	AuthBearer AuthKind = "bearer"
	// AuthPeer is the cluster peer HMAC scheme on /api/peer/*.
	AuthPeer AuthKind = "peer"
)

// Operation is the hand-maintained description of one route.
type Operation struct {
	// Summary is one line, imperative, describing what the route does.
	Summary string
	// Tag groups the route in the document (and in any generated client).
	Tag string
	// Auth is the credential the route requires.
	Auth AuthKind
}

// Document is an OpenAPI 3.1 document. It is a purpose-built subset: only the
// fields vnprox actually emits are modelled, so an unset field is a field we
// chose not to emit rather than one the type could not express.
//
//nolint:govet // fieldalignment: wire shape; field order is the emitted JSON's key order
type Document struct {
	OpenAPI    string              `json:"openapi"`
	Info       Info                `json:"info"`
	Tags       []Tag               `json:"tags,omitempty"`
	Paths      map[string]PathItem `json:"paths"`
	Components Components          `json:"components"`
}

// Info is the document's `info` object.
type Info struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// Tag is one entry in the document's top-level `tags` array.
type Tag struct {
	Name string `json:"name"`
}

// PathItem holds the operations registered on one path.
type PathItem struct {
	Get    *Op `json:"get,omitempty"`
	Put    *Op `json:"put,omitempty"`
	Post   *Op `json:"post,omitempty"`
	Delete *Op `json:"delete,omitempty"`
	Patch  *Op `json:"patch,omitempty"`
	Head   *Op `json:"head,omitempty"`
}

// Op is a single OpenAPI operation.
//
//nolint:govet // fieldalignment: wire shape; field order is the emitted JSON's key order
type Op struct {
	OperationID string                `json:"operationId"`
	Summary     string                `json:"summary"`
	Tags        []string              `json:"tags"`
	Parameters  []Parameter           `json:"parameters,omitempty"`
	Security    []map[string][]string `json:"security"`
	Responses   map[string]Response   `json:"responses"`
}

// Parameter is an OpenAPI parameter object. Only path parameters are emitted:
// query parameters are per-route and live in the metadata we do not yet carry,
// and inventing them would be worse than omitting them.
//
//nolint:govet // fieldalignment: wire shape; field order is the emitted JSON's key order
type Parameter struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
	Schema      Schema `json:"schema"`
}

// Response is an OpenAPI response object.
//
//nolint:govet // fieldalignment: wire shape; field order is the emitted JSON's key order
type Response struct {
	Description string               `json:"description"`
	Content     map[string]MediaType `json:"content,omitempty"`
}

// MediaType is an OpenAPI media-type object.
type MediaType struct {
	Schema Schema `json:"schema"`
}

// Schema is the subset of JSON Schema this document uses. Ref and the inline
// fields are mutually exclusive; Build never sets both.
//
//nolint:govet // fieldalignment: wire shape; field order is the emitted JSON's key order
type Schema struct {
	Ref                  string            `json:"$ref,omitempty"`
	Type                 string            `json:"type,omitempty"`
	Description          string            `json:"description,omitempty"`
	Properties           map[string]Schema `json:"properties,omitempty"`
	Required             []string          `json:"required,omitempty"`
	AdditionalProperties *bool             `json:"additionalProperties,omitempty"`
}

// Components is the document's reusable-object section.
type Components struct {
	Schemas         map[string]Schema         `json:"schemas"`
	SecuritySchemes map[string]SecurityScheme `json:"securitySchemes"`
}

// SecurityScheme is an OpenAPI security scheme object.
type SecurityScheme struct {
	Type         string `json:"type"`
	Description  string `json:"description,omitempty"`
	Scheme       string `json:"scheme,omitempty"`
	In           string `json:"in,omitempty"`
	Name         string `json:"name,omitempty"`
	BearerFormat string `json:"bearerFormat,omitempty"`
}

// Security scheme names, referenced from each operation's `security`.
const (
	schemeSession = "sessionCookie"
	schemeBearer  = "bearerAuth"
	schemePeer    = "peerSignature"
)

// Missing reports the routes that have no entry in Operations, as sorted
// table keys. A non-empty result is the gate failing: a new endpoint shipped
// without a description.
//
// Routes the document deliberately does not describe (see skip) are not
// reported — they are not undescribed, they are out of scope.
func Missing(routes []Route) []string {
	var out []string
	for _, rt := range routes {
		if skip(rt) {
			continue
		}
		if _, ok := Operations[rt.Key()]; !ok {
			out = append(out, rt.Key())
		}
	}
	sort.Strings(out)
	return out
}

// Unserved reports Operations entries that no route in routes serves — the
// other direction of the same gate. It catches a route deleted or renamed
// while its description stayed behind, which is how a documented-but-absent
// endpoint gets promised to an integrator.
func Unserved(routes []Route) []string {
	have := make(map[string]bool, len(routes))
	for _, rt := range routes {
		have[rt.Key()] = true
	}
	var out []string
	for key := range Operations {
		if !have[key] {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// skip reports whether a walked route is deliberately outside the document.
//
// Two categories, and neither is "we could not be bothered":
//
//   - Non-API routes: the SPA's static assets and the /embed/* view shells
//     serve HTML to a browser, not JSON to a client. An OpenAPI document that
//     described them would tell a generated client it can call them.
//   - chi's wildcard mounts (a trailing "/*"), which have no OpenAPI
//     expression: the path template language has no "and everything below
//     this". The MCP transport at /api/v1/mcp is JSON-RPC over a single POST,
//     which OpenAPI models as one opaque operation at best.
func skip(rt Route) bool {
	p := rt.Pattern
	if strings.HasSuffix(p, "/*") || p == "/*" {
		return true
	}
	if !strings.HasPrefix(p, "/api/") {
		return true
	}
	return false
}

// Build assembles the document from the walked routes. Routes with no
// metadata entry are still emitted — with a summary saying so — because a
// document that silently omits a live route is more dangerous than one that
// admits an undescribed route exists. Missing is what fails the build.
func Build(routes []Route, version string) *Document {
	doc := &Document{
		OpenAPI: "3.1.0",
		Info: Info{
			Title:   "vnprox API",
			Version: version,
			Description: "The vnproxd HTTP API. Paths, methods, path parameters, " +
				"authentication and error responses are complete and generated from the " +
				"router's registered routes. Request and response bodies are described in " +
				"docs/api.md; this document does not yet carry their schemas.",
		},
		Paths: map[string]PathItem{},
		Components: Components{
			Schemas:         map[string]Schema{"Error": errorSchema()},
			SecuritySchemes: securitySchemes(),
		},
	}

	tags := map[string]bool{}
	seenIDs := map[string]string{}

	sorted := append([]Route(nil), routes...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Pattern != sorted[j].Pattern {
			return sorted[i].Pattern < sorted[j].Pattern
		}
		return sorted[i].Method < sorted[j].Method
	})

	for _, rt := range sorted {
		if skip(rt) {
			continue
		}
		meta, ok := Operations[rt.Key()]
		if !ok {
			meta = Operation{
				Summary: "Undescribed route — see docs/api.md.",
				Tag:     deriveTag(rt.Pattern),
				Auth:    AuthSession,
			}
		}
		op := buildOp(rt, meta, seenIDs)
		tags[op.Tags[0]] = true

		item := doc.Paths[rt.Pattern]
		switch strings.ToUpper(rt.Method) {
		case "GET":
			item.Get = op
		case "PUT":
			item.Put = op
		case "POST":
			item.Post = op
		case "DELETE":
			item.Delete = op
		case "PATCH":
			item.Patch = op
		case "HEAD":
			item.Head = op
		default:
			// An unmodelled method would vanish silently into the default
			// branch, so refuse to build rather than emit a document that
			// omits a live route.
			panic(fmt.Sprintf("apidoc: unmodelled HTTP method %q on %s", rt.Method, rt.Pattern))
		}
		doc.Paths[rt.Pattern] = item
	}

	names := make([]string, 0, len(tags))
	for name := range tags {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		doc.Tags = append(doc.Tags, Tag{Name: name})
	}

	return doc
}

func buildOp(rt Route, meta Operation, seenIDs map[string]string) *Op {
	tag := meta.Tag
	if tag == "" {
		tag = deriveTag(rt.Pattern)
	}
	id := operationID(rt)
	if prev, clash := seenIDs[id]; clash {
		// Two routes deriving the same operationId would produce a document
		// that generates a client with two identically named methods. Fail
		// loudly at build time rather than shipping it.
		panic(fmt.Sprintf("apidoc: operationId %q derived from both %q and %q", id, prev, rt.Key()))
	}
	seenIDs[id] = rt.Key()

	op := &Op{
		OperationID: id,
		Summary:     meta.Summary,
		Tags:        []string{tag},
		Parameters:  pathParams(rt.Pattern),
		Security:    security(meta.Auth),
		Responses:   responses(rt, meta),
	}
	return op
}

// pathParams extracts chi's `{name}` path parameters in order of appearance.
func pathParams(pattern string) []Parameter {
	var out []Parameter
	for _, seg := range strings.Split(pattern, "/") {
		if !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
			continue
		}
		name := seg[1 : len(seg)-1]
		// chi supports `{name:regex}`; OpenAPI's parameter name is the part
		// before the colon.
		if i := strings.IndexByte(name, ':'); i >= 0 {
			name = name[:i]
		}
		out = append(out, Parameter{
			Name:     name,
			In:       "path",
			Required: true,
			Schema:   Schema{Type: "string"},
		})
	}
	return out
}

func security(kind AuthKind) []map[string][]string {
	switch kind {
	case AuthNone:
		// An empty requirement object is OpenAPI's "no credentials needed".
		// The array must still be present, or the document inherits any
		// top-level default — which is precisely the mistake that would make
		// /openapi.json look authenticated.
		return []map[string][]string{{}}
	case AuthBearer:
		return []map[string][]string{{schemeBearer: {}}}
	case AuthPeer:
		return []map[string][]string{{schemePeer: {}}}
	case AuthSession:
		return []map[string][]string{{schemeSession: {}}}
	default:
		return []map[string][]string{{schemeSession: {}}}
	}
}

func responses(rt Route, meta Operation) map[string]Response {
	okDesc := "Success."
	out := map[string]Response{
		"200": {
			Description: okDesc,
			Content: map[string]MediaType{
				"application/json": {Schema: Schema{
					Type:        "object",
					Description: "Response body; see docs/api.md for this route's shape.",
				}},
			},
		},
	}

	method := strings.ToUpper(rt.Method)
	if method == "POST" || method == "PUT" || method == "PATCH" || method == "DELETE" {
		out["400"] = errorResponse("The request was malformed or failed validation.")
	}
	if meta.Auth != AuthNone {
		out["401"] = errorResponse("No credential, or the credential is not valid.")
		out["403"] = errorResponse("The credential is valid but lacks the required capability.")
	}
	if len(pathParams(rt.Pattern)) > 0 {
		out["404"] = errorResponse("No such resource.")
	}
	return out
}

func errorResponse(desc string) Response {
	return Response{
		Description: desc,
		Content: map[string]MediaType{
			"application/json": {Schema: Schema{Ref: "#/components/schemas/Error"}},
		},
	}
}

// errorSchema is docs/api.md's error envelope: {"error":{"code","message"}}.
func errorSchema() Schema {
	return Schema{
		Type:        "object",
		Description: "The error envelope every failing vnprox route returns.",
		Required:    []string{"error"},
		Properties: map[string]Schema{
			"error": {
				Type:     "object",
				Required: []string{"code", "message"},
				Properties: map[string]Schema{
					"code":    {Type: "string", Description: "Stable machine-readable error code."},
					"message": {Type: "string", Description: "Human-readable explanation."},
				},
			},
		},
	}
}

func securitySchemes() map[string]SecurityScheme {
	return map[string]SecurityScheme{
		schemeSession: {
			Type: "apiKey", In: "cookie", Name: "vnprox_session",
			Description: "SPA session cookie. Mutating requests additionally require the " +
				"X-VNPROX-CSRF header; OpenAPI cannot express a second required credential " +
				"on one scheme, so see docs/security.md.",
		},
		schemeBearer: {
			Type: "http", Scheme: "bearer", BearerFormat: "opaque",
			Description: "API token issued by POST /tokens.",
		},
		schemePeer: {
			Type: "apiKey", In: "header", Name: "X-VNPROX-Signature",
			Description: "Cluster peer HMAC over the request, keyed by the shared cluster secret.",
		},
	}
}

// deriveTag is the fallback grouping: the first path segment below /api/v1.
func deriveTag(pattern string) string {
	p := strings.TrimPrefix(pattern, "/api/v1/")
	p = strings.TrimPrefix(p, "/api/")
	p = strings.TrimPrefix(p, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	if p == "" || strings.HasPrefix(p, "{") {
		return "misc"
	}
	return p
}

// operationID derives a stable, unique camelCase identifier from the method
// and pattern: GET /api/v1/changesets/{id}/impact -> getChangesetsByIdImpact.
//
// Path parameters become "By<Name>" rather than just "<Name>" so that
// /things/{id} and /things/id — a real possibility, since vnprox has both
// static and parameterised segments in the same position elsewhere — cannot
// collide. A collision panics in buildOp regardless; this makes it rare
// enough that the panic is a genuine surprise.
func operationID(rt Route) string {
	p := strings.TrimPrefix(rt.Pattern, "/api/v1")
	p = strings.TrimPrefix(p, "/api")
	var b strings.Builder
	b.WriteString(strings.ToLower(rt.Method))
	for _, seg := range strings.Split(p, "/") {
		if seg == "" {
			continue
		}
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			name := seg[1 : len(seg)-1]
			if i := strings.IndexByte(name, ':'); i >= 0 {
				name = name[:i]
			}
			b.WriteString("By")
			b.WriteString(camel(name))
			continue
		}
		b.WriteString(camel(seg))
	}
	return b.String()
}

// camel upper-cases the first rune and strips separators, folding
// "finding-events" and "finding_events" to "FindingEvents".
func camel(s string) string {
	var b strings.Builder
	upperNext := true
	for _, r := range s {
		if r == '-' || r == '_' || r == '.' {
			upperNext = true
			continue
		}
		if upperNext {
			b.WriteString(strings.ToUpper(string(r)))
			upperNext = false
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
