// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"reflect"
	"strings"
	"testing"
)

// TestRegistryIsStageOnlyAllowlist pins the EXACT documented tool allowlist and
// nothing else (T-1701 AC1 / AC6). If a future change adds, removes, or renames
// a tool, this fails loudly — the allowlist is the surface's security boundary,
// not an implementation detail. T-2705 extended the pin (deliberately, in the
// same commit that added the tools and documented them in docs/api.md) with the
// four typed staging tools; every other guard in this file is unchanged.
func TestRegistryIsStageOnlyAllowlist(t *testing.T) {
	want := []string{
		"topology.get",
		"findings.list",
		"flows.query",
		"ipam.subnets.list",
		"simulate.path",
		"diagnose.run",
		"changesets.diff",
		"changesets.create",
		"changesets.validate",
		"changesets.stage.bridge",
		"changesets.stage.iface",
		"changesets.stage.fwrule",
		"changesets.stage.ipam",
	}
	got := make([]string, 0, len(toolSpecs))
	for _, spec := range Tools() {
		got = append(got, spec.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tool allowlist changed:\n got  %v\n want %v", got, want)
	}
}

// TestNoMutatingToolByName is the AC1 regression assertion: no tool name may
// contain an apply/confirm/rollback/discard verb, by name or reachable code
// path. validateRegistry() enforces this at init (panicking a build that
// violates it); this test re-asserts it explicitly so the guarantee is visible
// in the suite and survives any refactor of the init hook.
func TestNoMutatingToolByName(t *testing.T) {
	for _, spec := range Tools() {
		lower := strings.ToLower(spec.Name)
		for _, bad := range forbiddenToolSubstrings {
			if strings.Contains(lower, bad) {
				t.Fatalf("tool %q names forbidden mutating verb %q — the MCP surface must be stage-only", spec.Name, bad)
			}
		}
	}
	// Also assert the forbidden set itself still covers the four live-mutating
	// verbs, so a well-meaning edit can't quietly shrink the guard.
	for _, must := range []string{"apply", "confirm", "rollback", "discard"} {
		found := false
		for _, bad := range forbiddenToolSubstrings {
			if bad == must {
				found = true
			}
		}
		if !found {
			t.Fatalf("forbiddenToolSubstrings no longer covers %q", must)
		}
	}
}

// TestChangesetStagerHasNoMutationVerb asserts, by reflection over the
// interface's own method set, that the change-engine seam this package holds
// exposes no apply/confirm/rollback/discard method — the structural half of the
// stage-only invariant (mirrors T-1702's plugin interface-surface test). No MCP
// code path can call a live-mutating verb because the type it is handed does not
// have one.
func TestChangesetStagerHasNoMutationVerb(t *testing.T) {
	typ := reflect.TypeOf((*ChangesetStager)(nil)).Elem()
	forbidden := []string{"Apply", "Confirm", "Rollback", "Discard"}
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		for _, bad := range forbidden {
			if strings.Contains(name, bad) {
				t.Fatalf("ChangesetStager exposes a mutating method %q — the MCP staging seam must be create/validate/diff only", name)
			}
		}
	}
	// And it MUST expose exactly the three staging methods it needs.
	for _, want := range []string{"CreateWithOrigin", "Validate", "Diff"} {
		if _, ok := typ.MethodByName(want); !ok {
			t.Fatalf("ChangesetStager is missing required staging method %q", want)
		}
	}
}

// TestNoApplyConfirmOrDeleteToolName is T-2705 acceptance criterion 6's NEW
// guard, and it is deliberately not a copy of TestNoMutatingToolByName above:
//
//   - it checks the verb list the card names (apply/confirm/delete) plus the
//     rest of the destructive/authority vocabulary, INDEPENDENTLY of
//     forbiddenToolSubstrings — so shrinking that slice cannot weaken this
//     assertion, which is the failure mode a guard that reads its own guard
//     list has;
//   - it checks the WIRED HANDLER MAP as well as the static registry, so a
//     tool smuggled in by wiring a handler under a mutating name (rather than
//     by adding a ToolSpec) is caught too;
//   - it asserts the staging family is non-empty, so the whole thing cannot
//     pass vacuously on a registry that stages nothing.
func TestNoApplyConfirmOrDeleteToolName(t *testing.T) {
	verbs := []string{
		"apply", "confirm", "delete", "approve",
		"rollback", "discard", "destroy", "remove", "revert", "commit", "execute",
	}

	names := make([]string, 0, len(toolSpecs))
	for _, spec := range Tools() {
		names = append(names, spec.Name)
	}
	if len(names) == 0 {
		t.Fatal("the tool registry enumerated empty; this assertion would be vacuous")
	}

	// The wired handlers, from a real Server — the set a tools/call can
	// actually dispatch to.
	auth := newFakeAuth()
	deps := stubReads()
	deps.Auth = auth
	srv, err := NewServer(deps)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	for name := range srv.handlers {
		names = append(names, name)
	}

	for _, name := range names {
		lower := strings.ToLower(name)
		for _, verb := range verbs {
			if strings.Contains(lower, verb) {
				t.Errorf("tool %q names the mutating verb %q — the MCP surface stages, it never applies", name, verb)
			}
		}
	}

	// Not vacuous in the other direction either: there ARE mutating (staging)
	// tools, and every one of them is wired.
	if len(stagingTools) == 0 {
		t.Fatal("no staging tools registered; T-2705 added four")
	}
	for _, name := range stagingTools {
		if _, ok := toolByName(name); !ok {
			t.Errorf("staging tool %q is not in the allowlist", name)
		}
		if _, ok := srv.handlers[name]; !ok {
			t.Errorf("staging tool %q has no wired handler", name)
		}
	}

	// Control: the guard really does fire on a name that names a verb — an
	// assertion that never fails on a bad input proves nothing.
	bad := "changesets.apply"
	fired := false
	for _, verb := range verbs {
		if strings.Contains(bad, verb) {
			fired = true
		}
	}
	if !fired {
		t.Fatalf("the verb list does not match %q; the guard above is not actually guarding", bad)
	}
}

// TestValidateRegistryPanicsOnMutatingTool proves validateRegistry actually
// rejects a smuggled-in apply tool, so the init-time guard isn't vacuous.
func TestValidateRegistryPanicsOnMutatingTool(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("validateRegistry did not panic on a mutating tool name")
		}
	}()
	orig := toolSpecs
	defer func() { toolSpecs = orig }()
	toolSpecs = append(append([]ToolSpec(nil), orig...), ToolSpec{Name: "changesets.apply", RequiredScope: scopeNetWrite})
	validateRegistry()
}
