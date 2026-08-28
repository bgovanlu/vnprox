// SPDX-License-Identifier: Apache-2.0

// jsondoc.go is T-4011's anti-drift gate for docs/cli-json.md: the same
// fenced-markdown-table + bidirectional-compare idiom
// internal/telemetry's ParseDocTable/CompareDoc (T-2503 AC6) and
// internal/perfbudget's TestDocTableMatchesBudgets (T-2506 AC2) already use
// elsewhere in this repo — each of those is its own local copy of the same
// pattern, scoped to its own column set and document, which is the
// established precedent this file follows rather than trying to share code
// with either (their DocRow shapes are genuinely different: telemetry's
// table has a "why it carries no identity" column that means nothing here).
//
// The one deliberate departure from that precedent: instead of reflecting
// over a single Go payload struct, this file decodes the actual JSON bytes
// a command wrote to stdout and inspects the top-level keys. `vnproxctl`'s
// `-o json` commands don't share one payload type the way
// internal/telemetry's Payload does — some are named wire structs, several
// are ad-hoc map[string]any literals (hubcmd.go, rollback.go) — so
// reflection would need a type per command anyway, and inspecting the
// decoded bytes directly is strictly stronger: it catches a map literal
// drifting from its documented shape exactly as readily as a struct would,
// with no per-command Go type registration to keep in sync.
//
// Coverage note: the table for each command documents its TOP-LEVEL keys
// only. A field whose value is itself an object or an array of objects is
// typed "object"/"array of object" here rather than expanded field-by-field
// — several of these top-level payloads embed types from internal/verify,
// internal/doctor and internal/backup that are already documented in their
// own package docs (internal/verify/verify.go's Report, etc.); re-deriving
// every nested field's doc row here would duplicate that contract rather
// than protect it. What this file guarantees is the one thing a script
// parsing `vnproxctl ... -o json` actually depends on first: which
// top-level keys exist and what kind of value each one is.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// cliJSONDocRelPath is the prose half of the contract.
const cliJSONDocRelPath = "docs/cli-json.md"

// cliJSONField is one top-level key of a command's decoded JSON output.
type cliJSONField struct {
	Name string
	Type string
}

// cliJSONDocRow is one row of a command's documented table in
// docs/cli-json.md. A Field written with a trailing `?` (matching
// docs/api.md's own "content?, pinnedBy?, pinnedAt?" convention for
// `omitempty` JSON fields) marks the field Optional: a Go `omitempty` tag
// means two valid invocations of the same command can legitimately produce
// different top-level key sets (`vnproxctl verify`'s `suite` vs.
// `selection` is exactly this — a `--suite` run carries one, a `--only` run
// carries the other, never both), so an optional field's absence from one
// particular sample is not drift the way an undocumented field's presence,
// or a documented required field's absence, is.
type cliJSONDocRow struct {
	Field       string
	Type        string
	Description string
	Line        int
	Optional    bool
}

// cliJSONDocColumns is the header every command's table must have, in
// order.
var cliJSONDocColumns = []string{"Field", "Type", "Description"}

// jsonShape decodes raw and returns its fields, sorted by name so
// comparisons are order-independent. raw must decode to either a JSON
// object (fields are that object's top-level keys — the common case) or a
// JSON array of objects (`snapshots list`, `remote drift`, `remote
// changesets list`, `verify --list`: fields are the union of keys any
// element in the sample carries, since an `omitempty` field may be present
// on some rows and absent on others). Anything else — a bare scalar, or an
// array whose elements are not objects — is an error: every documented
// `-o json` command in this binary emits one of the two shapes above, so a
// command that stopped doing that is exactly the kind of drift this gate
// exists to catch.
func jsonShape(raw []byte) ([]cliJSONField, error) {
	var top any
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("decoding JSON output: %w", err)
	}
	switch v := top.(type) {
	case map[string]any:
		return objectFields(v), nil
	case []any:
		union := map[string]string{}
		for _, elem := range v {
			obj, ok := elem.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("array element is %T, want a JSON object (a documented `-o json` array command must emit an array of objects)", elem)
			}
			for name, val := range obj {
				union[name] = coarseJSONType(val)
			}
		}
		fields := make([]cliJSONField, 0, len(union))
		for name, t := range union {
			fields = append(fields, cliJSONField{Name: name, Type: t})
		}
		sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
		return fields, nil
	default:
		return nil, fmt.Errorf("top-level JSON is %T, want an object or an array of objects", top)
	}
}

func objectFields(decoded map[string]any) []cliJSONField {
	fields := make([]cliJSONField, 0, len(decoded))
	for name, v := range decoded {
		fields = append(fields, cliJSONField{Name: name, Type: coarseJSONType(v)})
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	return fields
}

// coarseJSONType names a decoded JSON value's kind in the vocabulary
// docs/cli-json.md's tables use — coarse (e.g. "array of object" rather
// than a fully expanded element schema) because the document is read by an
// operator or a script author deciding how to parse a field, not by
// something that needs the nested shape spelled out here too (see this
// file's package doc comment).
func coarseJSONType(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case map[string]any:
		return "object"
	case []any:
		if len(val) == 0 {
			return "array"
		}
		elem := coarseJSONType(val[0])
		for _, e := range val[1:] {
			if coarseJSONType(e) != elem {
				return "array" // a mixed-type array; not worth pretending it's homogeneous
			}
		}
		return "array of " + elem
	default:
		return fmt.Sprintf("%T", v)
	}
}

// cliJSONTypesCompatible reports whether a documented type (docType, from
// docs/cli-json.md) and an observed type (gotType, from a real command's
// decoded JSON output) agree. Two deliberate widenings on top of an exact
// match:
//
//   - "null" always satisfies whatever is documented: a Go nil
//     slice/map/pointer with no `omitempty` tag encodes as JSON null rather
//     than an empty array/object, and which of the two a given invocation
//     happens to produce is an implementation detail this gate should not
//     pin.
//   - A documented "array" (the coarse, element-type-agnostic form this
//     document uses for fields whose contents are commonly empty — see
//     coarseJSONType's own doc comment) is satisfied by any "array of X"
//     observed type too: "array" is a deliberately weaker claim than
//     "array of object", not a different one, so a sample that happens to
//     carry elements is not a documentation mismatch.
func cliJSONTypesCompatible(docType, gotType string) bool {
	if docType == gotType || gotType == "null" {
		return true
	}
	return docType == "array" && strings.HasPrefix(gotType, "array of ")
}

// cliJSONDocTableBegin/End fence one command's table in docs/cli-json.md,
// the same "cannot drift onto a neighbouring table" guard
// internal/telemetry/docs.go's identical markers use.
func cliJSONDocTableBegin(cmd string) string { return "<!-- cli-json:" + cmd + ":begin -->" }
func cliJSONDocTableEnd(cmd string) string   { return "<!-- cli-json:" + cmd + ":end -->" }

// parseCLIJSONDocTable extracts one command's fenced field table from
// docs/cli-json.md's full markdown text.
func parseCLIJSONDocTable(md, cmd string) ([]cliJSONDocRow, error) {
	begin, end := cliJSONDocTableBegin(cmd), cliJSONDocTableEnd(cmd)
	lines := strings.Split(md, "\n")
	beginLine, endLine := -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case begin:
			if beginLine >= 0 {
				return nil, fmt.Errorf("%s appears twice (lines %d and %d)", begin, beginLine+1, i+1)
			}
			beginLine = i
		case end:
			if endLine >= 0 {
				return nil, fmt.Errorf("%s appears twice (lines %d and %d)", end, endLine+1, i+1)
			}
			endLine = i
		}
	}
	if beginLine < 0 || endLine < 0 {
		return nil, fmt.Errorf("no %s ... %s block in %s — every -o json command needs a documented table", begin, end, cliJSONDocRelPath)
	}
	if endLine < beginLine {
		return nil, fmt.Errorf("%s (line %d) comes before %s (line %d)", end, endLine+1, begin, beginLine+1)
	}

	var rows []cliJSONDocRow
	header := false
	for i := beginLine + 1; i < endLine; i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := splitCLIJSONRow(line)
		if !header {
			if len(cells) != len(cliJSONDocColumns) {
				return nil, fmt.Errorf("%s line %d: header has %d columns, want %d (%s)", cliJSONDocRelPath, i+1, len(cells), len(cliJSONDocColumns), strings.Join(cliJSONDocColumns, " | "))
			}
			for c, want := range cliJSONDocColumns {
				if cells[c] != want {
					return nil, fmt.Errorf("%s line %d: column %d is %q, want %q", cliJSONDocRelPath, i+1, c+1, cells[c], want)
				}
			}
			header = true
			continue
		}
		if isCLIJSONSeparatorRow(cells) {
			continue
		}
		if len(cells) != len(cliJSONDocColumns) {
			return nil, fmt.Errorf("%s line %d: row has %d columns, want %d", cliJSONDocRelPath, i+1, len(cells), len(cliJSONDocColumns))
		}
		name := unquoteCLIJSON(cells[0])
		optional := strings.HasSuffix(name, "?")
		name = strings.TrimSuffix(name, "?")
		rows = append(rows, cliJSONDocRow{Field: name, Type: cells[1], Description: cells[2], Line: i + 1, Optional: optional})
	}
	if !header {
		return nil, fmt.Errorf("%s: the %s block has no table header", cliJSONDocRelPath, begin)
	}
	return rows, nil
}

// compareCLIJSONDoc fails when fields (the command's actual decoded JSON
// output) and rows (docs/cli-json.md's table for that command) disagree in
// either direction — a field with no row, a row with no field, or a type
// mismatch.
func compareCLIJSONDoc(cmd string, fields []cliJSONField, rows []cliJSONDocRow) error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	byName := make(map[string]cliJSONDocRow, len(rows))
	for _, r := range rows {
		if prev, dup := byName[r.Field]; dup {
			add("%s.%s: documented twice (lines %d and %d)", cmd, r.Field, prev.Line, r.Line)
			continue
		}
		byName[r.Field] = r
	}

	for _, f := range fields {
		row, ok := byName[f.Name]
		if !ok {
			add("%s.%s: is in the command's actual JSON output and has no row in %s — undocumented field", cmd, f.Name, cliJSONDocRelPath)
			continue
		}
		if !cliJSONTypesCompatible(row.Type, f.Type) {
			add("%s.%s: %s line %d calls it %q, the command's actual output is %q", cmd, f.Name, cliJSONDocRelPath, row.Line, row.Type, f.Type)
		}
		if strings.TrimSpace(row.Description) == "" {
			add("%s.%s: %s line %d does not describe the field", cmd, f.Name, cliJSONDocRelPath, row.Line)
		}
	}

	known := make(map[string]bool, len(fields))
	for _, f := range fields {
		known[f.Name] = true
	}
	for _, r := range rows {
		if !known[r.Field] && !r.Optional {
			add("%s.%s: %s line %d documents a field the command's actual JSON output does not have — the document has outlived the code (mark it `%s?` if it's a genuine omitempty field this sample happens not to carry)", cmd, r.Field, cliJSONDocRelPath, r.Line, r.Field)
		}
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// loadCLIJSONDocMarkdown reads docs/cli-json.md, walking up from the
// working directory to find the repo root the same way
// internal/telemetry/docs.go's repoRoot does.
func loadCLIJSONDocMarkdown() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locating repo root: %w", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			path := filepath.Join(dir, cliJSONDocRelPath)
			raw, readErr := os.ReadFile(path) //nolint:gosec // a repo-relative path this repo owns
			if readErr != nil {
				return "", fmt.Errorf("reading %s: %w", cliJSONDocRelPath, readErr)
			}
			return string(raw), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("locating repo root: no go.mod in any parent directory")
		}
		dir = parent
	}
}

// splitCLIJSONRow splits one markdown table row on unescaped `|`, then
// unescapes `\|` back to a literal pipe in each cell — several of this
// document's Description cells enumerate a status enum as `a`\|`b`\|`c`,
// standard markdown practice for a literal pipe inside a table cell, so the
// parser has to honour the same escape a renderer would.
func splitCLIJSONRow(line string) []string {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "|"), "|")
	var parts []string
	var cur strings.Builder
	escaped := false
	for _, r := range trimmed {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '|':
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	parts = append(parts, cur.String())
	cells := make([]string, 0, len(parts))
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	return cells
}

func isCLIJSONSeparatorRow(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return true
}

func unquoteCLIJSON(cell string) string {
	return strings.Trim(cell, "`")
}
