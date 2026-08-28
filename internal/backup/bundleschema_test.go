// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// bundleschema_test.go is T-1902 AC2: "adding a new field to a collected
// structure without declaring it fails a test — the allowlist is enforced,
// so redaction cannot rot as the product grows."
//
// The pair of tests below is deliberate. The first asserts the real schema
// is clean; on its own that proves nothing, because a checker that always
// returns "no problems" also passes it. The second is the control: it runs
// the SAME checker over deliberately-wrong fixtures and requires it to
// complain about each one. Neither test means anything without the other.

// TestBundleSchema_AC2_EveryEmittedFieldIsDeclared is the enforcement.
//
// checkSchema fails in BOTH directions, and both matter:
//
//   - a field on a collected type with no fieldDecl is a value that would
//     reach a forum post without anyone having decided it was safe to;
//   - a fieldDecl with no field behind it means the schema has stopped
//     describing the code, which is how an inventory becomes decorative.
func TestBundleSchema_AC2_EveryEmittedFieldIsDeclared(t *testing.T) {
	problems := checkSchema(bundleDocTypes(), bundleFieldSchema)
	for _, p := range problems {
		t.Errorf("%s", p)
	}
	if len(problems) > 0 {
		t.Logf("%d problem(s). Every collected field must be declared in bundleFieldSchema with a "+
			"disposition and a reason it is safe to attach to a public thread.", len(problems))
	}

	// Non-vacuity floor. checkSchema reports "declared but not reachable"
	// for every schema key the walk failed to reach, so a walker that found
	// nothing would fail loudly above rather than pass here — but a floor
	// on the inventory's size also catches the opposite mistake, a document
	// type quietly dropped from bundleDocTypes.
	if len(bundleFieldSchema) < 15 {
		t.Fatalf("bundleFieldSchema declares only %d types; the bundle emits more than that, "+
			"so something has been dropped from bundleDocTypes", len(bundleFieldSchema))
	}
	fields := 0
	for _, decls := range bundleFieldSchema {
		fields += len(decls)
	}
	if fields < 80 {
		t.Fatalf("bundleFieldSchema declares only %d fields in total — implausibly few for the documents "+
			"a bundle emits, so the walk or the inventory has degraded", fields)
	}

	// Every document type must be reachable as a root: a type that is only
	// reachable through another is fine, but a ROOT that is not in
	// bundleDocTypes cannot be checked at all.
	if got, want := len(bundleDocTypes()), 10; got != want {
		t.Errorf("bundleDocTypes has %d roots, expected %d — add the new document type to the roots "+
			"as well as to bundleFieldSchema, or it is never walked", got, want)
	}
}

// --- control fixtures -------------------------------------------------
//
// These types exist only to be checked. They are NOT reachable from
// bundleDocTypes and are never serialised into anything.

type ctlGood struct {
	Name  string
	Count int
}

type ctlUndeclaredField struct {
	Name string
	// Sneaky is the field a careless change adds. Nothing declares it.
	Sneaky string
}

type ctlOpaqueDeclaredEmit struct {
	Blob json.RawMessage
}

type ctlEmptyReason struct {
	Name string
}

type ctlNested struct {
	Inner ctlUndeclaredType
}

type ctlUndeclaredType struct {
	Value string
}

type ctlStaleDecl struct {
	Name string
}

// TestBundleSchema_TheEnforcementIsNotVacuous is the control for the test
// above.
//
// Every row deliberately breaks one rule and requires checkSchema to name
// it. Without this, the enforcement test could be passing because
// checkSchema returns an empty slice unconditionally — which is exactly how
// a redaction allowlist quietly stops being one.
func TestBundleSchema_TheEnforcementIsNotVacuous(t *testing.T) {
	// The baseline: a correctly declared fixture must produce NO problems.
	// This is what proves the rows below fail for the reason claimed rather
	// than because checkSchema rejects everything.
	good := map[string][]fieldDecl{
		"ctlGood": {
			{"Name", dispEmit, "a fixture field"},
			{"Count", dispEmit, "a fixture field"},
		},
	}
	if problems := checkSchema([]reflect.Type{reflect.TypeOf(ctlGood{})}, good); len(problems) != 0 {
		t.Fatalf("CONTROL FAILED: a correctly declared type produced %d problem(s): %v. "+
			"checkSchema rejects everything, so every 'it complains' assertion below proves nothing.",
			len(problems), problems)
	}

	cases := []struct {
		name   string
		root   reflect.Type
		schema map[string][]fieldDecl
		// want is a substring the reported problem must contain.
		want string
	}{
		{
			name: "a field added without a declaration",
			root: reflect.TypeOf(ctlUndeclaredField{}),
			schema: map[string][]fieldDecl{"ctlUndeclaredField": {
				{"Name", dispEmit, "a fixture field"},
			}},
			want: "not declared in bundleFieldSchema",
		},
		{
			name: "an opaque field declared as plainly emitted",
			root: reflect.TypeOf(ctlOpaqueDeclaredEmit{}),
			schema: map[string][]fieldDecl{"ctlOpaqueDeclaredEmit": {
				{"Blob", dispEmit, "a fixture field"},
			}},
			want: "opaque to reflection",
		},
		{
			name: "a declaration with no reason",
			root: reflect.TypeOf(ctlEmptyReason{}),
			schema: map[string][]fieldDecl{"ctlEmptyReason": {
				{"Name", dispEmit, "   "},
			}},
			want: "empty reason",
		},
		{
			name: "a nested type nobody declared",
			root: reflect.TypeOf(ctlNested{}),
			schema: map[string][]fieldDecl{"ctlNested": {
				{"Inner", dispEmit, "a fixture field"},
			}},
			want: "no entry in bundleFieldSchema",
		},
		{
			name: "a declaration for a field that no longer exists",
			root: reflect.TypeOf(ctlStaleDecl{}),
			schema: map[string][]fieldDecl{"ctlStaleDecl": {
				{"Name", dispEmit, "a fixture field"},
				{"Removed", dispEmit, "a field that was deleted from the struct"},
			}},
			want: "no such field exists",
		},
		{
			name: "a declared type nothing emits",
			root: reflect.TypeOf(ctlGood{}),
			schema: map[string][]fieldDecl{
				"ctlGood": {
					{"Name", dispEmit, "a fixture field"},
					{"Count", dispEmit, "a fixture field"},
				},
				"ctlOrphan": {{"Gone", dispEmit, "a type nothing emits"}},
			},
			want: "not reachable from any bundle document",
		},
		{
			name: "an unknown disposition",
			root: reflect.TypeOf(ctlGood{}),
			schema: map[string][]fieldDecl{"ctlGood": {
				{"Name", disposition("hope-for-the-best"), "a fixture field"},
				{"Count", dispEmit, "a fixture field"},
			}},
			want: "unknown disposition",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := checkSchema([]reflect.Type{tc.root}, tc.schema)
			if len(problems) == 0 {
				t.Fatalf("checkSchema found no problem, but this fixture deliberately breaks %q. "+
					"The AC2 enforcement is not actually enforcing.", tc.want)
			}
			joined := make([]string, 0, len(problems))
			for _, p := range problems {
				joined = append(joined, p.String())
			}
			if !strings.Contains(strings.Join(joined, "\n"), tc.want) {
				t.Errorf("problems did not mention %q:\n%s", tc.want, strings.Join(joined, "\n"))
			}
		})
	}
}

// TestBundleSchema_OpaqueClassification pins down the one predicate the
// whole "allowlist or redactor" rule rests on. If opaque() ever started
// returning false for json.RawMessage, every AC2 assertion above would
// still pass while the bundle happily emitted unredacted JSON.
func TestBundleSchema_OpaqueClassification(t *testing.T) {
	cases := []struct {
		typ  reflect.Type
		name string
		want bool
	}{
		{reflect.TypeOf(json.RawMessage{}), "json.RawMessage", true},
		{reflect.TypeOf([]byte{}), "[]byte", true},
		{reflect.TypeOf((*any)(nil)).Elem(), "any", true},
		{reflect.TypeOf(map[string]string{}), "map[string]string", true},
		{reflect.TypeOf(map[string]any{}), "map[string]any", true},
		{reflect.TypeOf([]any{}), "[]any", true},
		{reflect.TypeOf(&json.RawMessage{}), "*json.RawMessage", true},
		{reflect.TypeOf(""), "string", false},
		{reflect.TypeOf(int64(0)), "int64", false},
		{reflect.TypeOf(false), "bool", false},
		{reflect.TypeOf([]string{}), "[]string", false},
		{reflect.TypeOf([]PathFact{}), "[]PathFact", false},
		{reflect.TypeOf(PathFact{}), "PathFact", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := opaque(tc.typ); got != tc.want {
				t.Errorf("opaque(%s) = %v, want %v", tc.typ, got, tc.want)
			}
		})
	}
}

// TestBundleEntrySchema_IsInternallyConsistent checks the *entry* half of
// the declaration: every declared entry names a document type the field
// schema knows about (or is explicitly not a structured document), carries
// a legal archive entry name, and uses a role a bundle may use.
//
// The roles are the load-bearing part. RoleStore and RoleKey must never
// appear: the first would mean the store file is in a bundle, the second
// would mean key material is, and manifest.validate would additionally
// reject the second unless the manifest also declared IncludesKeyMaterial —
// which bundle.Bundle cannot set.
func TestBundleEntrySchema_IsInternallyConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range bundleEntrySchema {
		if seen[d.Name] {
			t.Errorf("entry %q declared twice", d.Name)
		}
		seen[d.Name] = true
		if !validEntryName(d.Name) {
			t.Errorf("entry %q is not a legal archive entry name", d.Name)
		}
		switch d.Role {
		case RoleMeta, RoleConfig:
		case RoleStore:
			t.Errorf("entry %q declares role %q — a support bundle must never carry the store", d.Name, d.Role)
		case RoleKey:
			t.Errorf("entry %q declares role %q — a support bundle must never carry key material", d.Name, d.Role)
		default:
			t.Errorf("entry %q declares unknown role %q", d.Name, d.Role)
		}
		if strings.TrimSpace(d.Redaction) == "" {
			t.Errorf("entry %q does not say what redaction its contents passed", d.Name)
		}
		if strings.TrimSpace(d.About) == "" {
			t.Errorf("entry %q has no description; readme.txt is generated from these", d.Name)
		}
		if d.Doc != "" {
			if _, ok := bundleFieldSchema[d.Doc]; !ok {
				t.Errorf("entry %q says it serialises %q, which bundleFieldSchema does not declare", d.Name, d.Doc)
			}
		}
	}
	if len(bundleEntrySchema) < 8 {
		t.Fatalf("only %d entries declared — implausibly few", len(bundleEntrySchema))
	}
}
