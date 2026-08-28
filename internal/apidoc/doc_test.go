// SPDX-License-Identifier: Apache-2.0

package apidoc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMissing_FailsForARouteWithNoDescription is T-2405 AC1 at the unit
// level: the gate must *fail* for an undescribed route. Asserting that the
// current route set passes proves only that the current route set passes,
// which is what a gate that silently stopped working would also do.
func TestMissing_FailsForARouteWithNoDescription(t *testing.T) {
	routes := []Route{
		{Method: "GET", Pattern: "/api/v1/health"},
		{Method: "GET", Pattern: "/api/v1/brand-new-route-nobody-described"},
	}
	missing := Missing(routes)
	if len(missing) != 1 || missing[0] != "GET /api/v1/brand-new-route-nobody-described" {
		t.Fatalf("Missing() = %v, want exactly the undescribed route", missing)
	}

	// Control: the same call with only described routes reports nothing, so
	// the failure above is attributable to the new route and not to the
	// checker reporting everything.
	if got := Missing(routes[:1]); len(got) != 0 {
		t.Fatalf("Missing() on a described route = %v, want none", got)
	}
}

// TestUnserved_FailsForADescribedRouteNobodyServes is AC2 — the other
// direction. A documented route that 404s is worse than an undocumented one:
// the integrator writes code against it.
func TestUnserved_FailsForADescribedRouteNobodyServes(t *testing.T) {
	// Every Operations entry except one is served.
	var routes []Route
	const dropped = "GET /api/v1/health"
	for key := range Operations {
		if key == dropped {
			continue
		}
		method, pattern, ok := strings.Cut(key, " ")
		if !ok {
			t.Fatalf("Operations key %q is not \"METHOD /path\"", key)
		}
		routes = append(routes, Route{Method: method, Pattern: pattern})
	}

	unserved := Unserved(routes)
	if len(unserved) != 1 || unserved[0] != dropped {
		t.Fatalf("Unserved() = %v, want exactly %q", unserved, dropped)
	}

	// Control: put it back and nothing is unserved.
	routes = append(routes, Route{Method: "GET", Pattern: "/api/v1/health"})
	if got := Unserved(routes); len(got) != 0 {
		t.Fatalf("Unserved() with every route served = %v, want none", got)
	}
}

// TestBuild_PathTemplatingIsChis is AC5: OpenAPI's `{id}` and chi's `{id}`
// must be the same string, asserted against a pattern the production router
// really registers rather than an invented one.
func TestBuild_PathTemplatingIsChis(t *testing.T) {
	const real = "/api/v1/changesets/{id}/impact"
	if _, ok := Operations[Key("GET", real)]; !ok {
		t.Fatalf("%q is no longer a described route; pick another real pattern for this assertion", real)
	}

	doc := Build([]Route{{Method: "GET", Pattern: real}}, "test")
	item, ok := doc.Paths[real]
	if !ok {
		t.Fatalf("document has no path %q; it has %v", real, keysOf(doc.Paths))
	}
	if item.Get == nil {
		t.Fatal("path has no GET operation")
	}
	if len(item.Get.Parameters) != 1 || item.Get.Parameters[0].Name != "id" {
		t.Fatalf("parameters = %+v, want exactly one named \"id\" extracted from the {id} segment", item.Get.Parameters)
	}
	if strings.Contains(real, ":id") {
		t.Fatal("the pattern uses Gin/echo-style :id; chi and OpenAPI both use {id}")
	}
}

// TestBuild_UnauthenticatedRoutesCarryAnEmptySecurityRequirement guards the
// one security mistake in this file that would be invisible: an operation
// with no `security` key inherits the document default. Emitting `[{}]` says
// "no credentials" explicitly.
func TestBuild_UnauthenticatedRoutesCarryAnEmptySecurityRequirement(t *testing.T) {
	doc := Build([]Route{{Method: "GET", Pattern: "/api/v1/openapi.json"}}, "test")
	op := doc.Paths["/api/v1/openapi.json"].Get
	if op == nil {
		t.Fatal("no GET operation for /api/v1/openapi.json")
	}
	if len(op.Security) != 1 || len(op.Security[0]) != 0 {
		t.Fatalf("security = %v, want exactly one empty requirement object", op.Security)
	}
	if _, has := op.Responses["401"]; has {
		t.Error("an unauthenticated route documents a 401 response; nothing can produce it")
	}
}

// TestBuild_SkipsWildcardAndNonAPIRoutes documents the deliberate exclusions,
// so that removing skip() fails a test rather than quietly adding the SPA's
// static file server to a generated client.
func TestBuild_SkipsWildcardAndNonAPIRoutes(t *testing.T) {
	doc := Build([]Route{
		{Method: "GET", Pattern: "/*"},
		{Method: "GET", Pattern: "/embed/*"},
		{Method: "GET", Pattern: "/api/v1/health"},
	}, "test")
	if len(doc.Paths) != 1 {
		t.Fatalf("paths = %v, want only the API route", keysOf(doc.Paths))
	}
	if _, ok := doc.Paths["/api/v1/health"]; !ok {
		t.Fatalf("the API route was skipped; paths = %v", keysOf(doc.Paths))
	}
}

// TestCommittedDocument_IsStructurallyValidOpenAPI31 is AC3.
//
// It validates structure, not merely that the bytes parse as JSON: version,
// required info fields, path templating, parameter/path agreement,
// operationId uniqueness, response shape, and that every $ref and every named
// security scheme resolves. It is not a complete OpenAPI validator — vnprox
// does not carry one as a dependency — and it does not claim to be; what it
// checks is stated here so a reader knows what a pass means.
func TestCommittedDocument_IsStructurallyValidOpenAPI31(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "openapi.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s (run `make openapi`): %v", path, err)
	}
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	if !strings.HasPrefix(doc.OpenAPI, "3.1.") {
		t.Errorf("openapi = %q, want a 3.1.x version", doc.OpenAPI)
	}
	if doc.Info.Title == "" || doc.Info.Version == "" {
		t.Errorf("info.title/info.version = %q/%q, both are required", doc.Info.Title, doc.Info.Version)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("the document describes no paths")
	}
	if _, ok := doc.Components.Schemas["Error"]; !ok {
		t.Error("components.schemas.Error is missing, but operations reference it")
	}

	declaredTags := map[string]bool{}
	for _, tag := range doc.Tags {
		declaredTags[tag.Name] = true
	}

	seenIDs := map[string]string{}
	for p, item := range doc.Paths {
		if !strings.HasPrefix(p, "/") {
			t.Errorf("path %q does not start with /", p)
		}
		if strings.Contains(p, ":") {
			t.Errorf("path %q uses :param templating; OpenAPI (and chi) use {param}", p)
		}
		if strings.Count(p, "{") != strings.Count(p, "}") {
			t.Errorf("path %q has unbalanced templating braces", p)
		}
		inPath := map[string]bool{}
		for _, seg := range strings.Split(p, "/") {
			if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
				inPath[seg[1:len(seg)-1]] = true
			}
		}

		for method, op := range opsOf(item) {
			where := method + " " + p
			if op.OperationID == "" {
				t.Errorf("%s: operationId is empty", where)
			}
			if prev, clash := seenIDs[op.OperationID]; clash {
				t.Errorf("%s: operationId %q is also used by %s; a generated client would have two identically named methods", where, op.OperationID, prev)
			}
			seenIDs[op.OperationID] = where
			if op.Summary == "" {
				t.Errorf("%s: summary is empty", where)
			}
			if len(op.Tags) == 0 {
				t.Errorf("%s: no tags", where)
			}
			for _, tag := range op.Tags {
				if !declaredTags[tag] {
					t.Errorf("%s: tag %q is used but not declared at the top level", where, tag)
				}
			}
			if op.Security == nil {
				t.Errorf("%s: no security field; the operation would inherit a document-level default", where)
			}
			for _, req := range op.Security {
				for scheme := range req {
					if _, ok := doc.Components.SecuritySchemes[scheme]; !ok {
						t.Errorf("%s: security scheme %q is not defined in components", where, scheme)
					}
				}
			}

			declared := map[string]bool{}
			for _, param := range op.Parameters {
				if param.In != "path" {
					t.Errorf("%s: parameter %q has in=%q; only path parameters are emitted", where, param.Name, param.In)
				}
				if !param.Required {
					t.Errorf("%s: path parameter %q is not required; OpenAPI requires path parameters to be", where, param.Name)
				}
				declared[param.Name] = true
			}
			for name := range inPath {
				if !declared[name] {
					t.Errorf("%s: path template has {%s} but the operation does not declare it", where, name)
				}
			}
			for name := range declared {
				if !inPath[name] {
					t.Errorf("%s: declares path parameter %q that the path template does not contain", where, name)
				}
			}

			if len(op.Responses) == 0 {
				t.Errorf("%s: no responses", where)
			}
			for code, resp := range op.Responses {
				if resp.Description == "" {
					t.Errorf("%s: response %s has no description, which OpenAPI requires", where, code)
				}
				for _, media := range resp.Content {
					if media.Schema.Ref == "" {
						continue
					}
					const prefix = "#/components/schemas/"
					if !strings.HasPrefix(media.Schema.Ref, prefix) {
						t.Errorf("%s: response %s has unresolvable $ref %q", where, code, media.Schema.Ref)
						continue
					}
					if _, ok := doc.Components.Schemas[strings.TrimPrefix(media.Schema.Ref, prefix)]; !ok {
						t.Errorf("%s: response %s references undefined schema %q", where, code, media.Schema.Ref)
					}
				}
			}
		}
	}
}

func opsOf(item PathItem) map[string]*Op {
	out := map[string]*Op{}
	for method, op := range map[string]*Op{
		"GET": item.Get, "PUT": item.Put, "POST": item.Post,
		"DELETE": item.Delete, "PATCH": item.Patch, "HEAD": item.Head,
	} {
		if op != nil {
			out[method] = op
		}
	}
	return out
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
