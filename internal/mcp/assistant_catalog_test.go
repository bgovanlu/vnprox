package mcp

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// assistant_catalog_test.go is T-2808's structural half, on the side of the
// boundary that can actually enforce it.
//
// T-2808 adds an in-app assistant panel (web/src/assistant/) that runs "the
// existing MCP tools against the local daemon" — no new backend capability,
// no new data path. That claim is only true while the panel's client-side
// tool catalogue is a SUBSET of this package's frozen allowlist. A panel
// that quietly grew a seventh "tool" of its own would be a second surface
// wearing the first one's name, and nothing in TypeScript would notice.
//
// So this test reads the real catalogue out of the real file and checks it
// against the real allowlist. It is the same shape T-2805's
// internal/presence/deps_test.go uses (scan the actual source, then prove
// the scan is not vacuous), applied across the language boundary.
//
// WHAT IT DOES NOT DO: re-implement T-2705's stage-only guarantee. That
// guarantee is inherited unchanged — stageonly.go asserts at COMPILE TIME
// that the change-engine seam this package holds has no Apply/Confirm/
// Approve/Rollback/Discard method, so a build in which MCP (and therefore
// anything mirroring MCP) can apply cannot be produced. The check below is
// the narrower, additional question T-2808 raises: does the panel's list of
// tool names still name only tools that exist here, and does it still name
// no mutating verb?
const assistantCatalogFile = "web/src/assistant/tools.ts"

var (
	assistantUnionBlock = regexp.MustCompile(`(?s)export type AssistantToolName =(.*?);`)
	assistantQuoted     = regexp.MustCompile(`"([a-z][a-zA-Z0-9.]*)"`)
	assistantSpecName   = regexp.MustCompile(`name:\s*"([^"]+)"`)
)

func readAssistantCatalog(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	path := filepath.Join(root, filepath.FromSlash(assistantCatalogFile))
	raw, err := os.ReadFile(path) //nolint:gosec // a fixed, repo-relative path in a test
	if err != nil {
		t.Fatalf("reading %s: %v — T-2808's assistant catalogue is what this test is about; "+
			"if the panel moved, move this scan with it rather than deleting it", assistantCatalogFile, err)
	}
	return string(raw)
}

// assistantToolNames extracts the catalogue twice — from the TypeScript
// union type and from the ASSISTANT_TOOLS table — and requires the two to
// agree. Reading one alone would pass while the other drifted.
func assistantToolNames(t *testing.T, source string) []string {
	t.Helper()

	block := assistantUnionBlock.FindStringSubmatch(source)
	if block == nil {
		t.Fatalf("%s: could not find `export type AssistantToolName = ...` — this scan is not reading what it thinks it is",
			assistantCatalogFile)
	}
	var union []string
	for _, m := range assistantQuoted.FindAllStringSubmatch(block[1], -1) {
		union = append(union, m[1])
	}

	var table []string
	for _, m := range assistantSpecName.FindAllStringSubmatch(source, -1) {
		table = append(table, m[1])
	}

	if len(union) == 0 || len(table) == 0 {
		t.Fatalf("%s: parsed %d union members and %d table entries — a catalogue scan that finds nothing "+
			"certifies nothing", assistantCatalogFile, len(union), len(table))
	}
	if strings.Join(union, ",") != strings.Join(table, ",") {
		t.Fatalf("%s: the AssistantToolName union %v and the ASSISTANT_TOOLS table %v disagree; "+
			"one of them is not the catalogue the panel actually runs", assistantCatalogFile, union, table)
	}
	return table
}

// TestAssistantCatalogIsASubsetOfTheFrozenAllowlist: every tool the in-app
// assistant claims to run must be a tool this package really exposes.
func TestAssistantCatalogIsASubsetOfTheFrozenAllowlist(t *testing.T) {
	names := assistantToolNames(t, readAssistantCatalog(t))

	// Non-vacuity, both directions: the allowlist is populated, and the
	// catalogue is a plausible size for a panel that mirrors it.
	if len(toolSpecs) == 0 {
		t.Fatal("the MCP allowlist is empty — this comparison would pass trivially")
	}
	if len(names) < 4 {
		t.Fatalf("parsed only %d assistant tools (%v); the panel mirrors the read surfaces, so this is "+
			"almost certainly a broken scan rather than a real catalogue", len(names), names)
	}

	for _, name := range names {
		if _, ok := toolByName(name); !ok {
			t.Errorf("the in-app assistant claims tool %q, which is not in the MCP allowlist.\n\n"+
				"T-2808's whole premise is that the panel runs the EXISTING MCP tools: no new backend "+
				"capability, no new data path. A name here that this package does not have means the "+
				"panel has grown a surface of its own, which is a task-card decision, not an edit.", name)
		}
	}
}

// TestAssistantCatalogNamesNoMutatingVerb re-asserts T-2705's stage-only
// boundary at the panel's edge, using this package's own forbidden-verb
// vocabulary rather than a second copy of it.
func TestAssistantCatalogNamesNoMutatingVerb(t *testing.T) {
	names := assistantToolNames(t, readAssistantCatalog(t))

	if len(forbiddenToolSubstrings) == 0 {
		t.Fatal("forbiddenToolSubstrings is empty — this check would pass for any name at all")
	}

	for _, name := range names {
		lower := strings.ToLower(name)
		for _, bad := range forbiddenToolSubstrings {
			if strings.Contains(lower, bad) {
				t.Errorf("the in-app assistant names tool %q, which contains the mutating verb %q. "+
					"The MCP surface is stage-only (docs/security.md's \"MCP stage-only boundary\"), and a "+
					"panel over it inherits that, not an exception to it.", name, bad)
			}
		}
	}

	// CONTROL: the vocabulary really does reject a mutating name, so the
	// loop above passing means "no such name", not "no such check".
	var caught bool
	for _, bad := range forbiddenToolSubstrings {
		if strings.Contains("changesets.apply", bad) {
			caught = true
		}
	}
	if !caught {
		t.Fatal("forbiddenToolSubstrings does not reject \"changesets.apply\" — the scan above is not " +
			"testing what it claims to test")
	}
}

// TestAssistantCatalogCoversOnlyReadAndStageTools states the same thing from
// the other side: the panel's catalogue contains no tool whose RequiredScope
// is netWrite. The assistant reads with the caller's session; the one write
// it can perform (staging a draft) goes through the ordinary POST /changesets
// path with the caller's own capability, not through a tool.
func TestAssistantCatalogCoversOnlyReadAndStageTools(t *testing.T) {
	names := assistantToolNames(t, readAssistantCatalog(t))

	var sawRead bool
	for _, name := range names {
		spec, ok := toolByName(name)
		if !ok {
			continue // reported by the subset test above
		}
		if spec.RequiredScope == scopeNetRead {
			sawRead = true
		}
		if spec.RequiredScope == scopeNetWrite {
			t.Errorf("the in-app assistant catalogue includes %q, a netWrite tool. The panel's read side "+
				"must stay read-only; staging is a separate, human-visible action.", name)
		}
	}
	if !sawRead {
		t.Fatal("no catalogued tool is netRead-scoped — the scope comparison above is reading nothing")
	}
}
