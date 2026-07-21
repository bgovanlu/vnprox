package mcp

import (
	"reflect"
	"strings"
	"testing"
)

// TestRegistryIsStageOnlyAllowlist pins the EXACT documented tool allowlist and
// nothing else (T-1701 AC1 / AC6). If a future change adds, removes, or renames
// a tool, this fails loudly — the allowlist is the surface's security boundary,
// not an implementation detail.
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
