package validation

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ExpectedRow is one row of a planning/validation/expected/<section>.md
// table: for a given checklist item id, what a named field of its evidence
// blob entry ("pointer") should contain, and what it means if that
// diverges. See planning/validation/README.md for the full column
// reference and planning/validation/expected/*.md for real examples.
type ExpectedRow struct {
	ID       string // matches an Item.ID in the evidence blob
	Pointer  string // "raw" | "exit_code" | "command" | "checklist_ref" | "verdict_inputs.<key>"
	Op       string // "equals" | "contains" | "not_contains" | "regex"
	Expected string
	Meaning  string // human-readable: what a divergence would mean
}

// tableRowPattern matches a single markdown table row: leading/trailing
// pipes, cells separated by "|". Separator rows ("|---|---|") and the
// header row are filtered out by content, not by this pattern.
var tableRowPattern = regexp.MustCompile(`^\s*\|(.+)\|\s*$`)

// separatorCellPattern matches a markdown table separator cell (e.g. "---",
// ":---", "---:", ":---:").
var separatorCellPattern = regexp.MustCompile(`^:?-{1,}:?$`)

// ParseExpected parses every row of every markdown table in an
// expected-outcome file into ExpectedRows. Column order is fixed: id,
// pointer, op, expected, meaning. Non-table lines (prose, headings) are
// ignored, so an expected/<section>.md can freely mix explanatory text
// with its table(s).
func ParseExpected(md []byte) ([]ExpectedRow, error) {
	var rows []ExpectedRow
	lineNo := 0
	for _, line := range strings.Split(string(md), "\n") {
		lineNo++
		m := tableRowPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		cells := splitCells(m[1])
		if isSeparatorRow(cells) {
			continue
		}
		if len(cells) > 0 && strings.EqualFold(strings.TrimSpace(cells[0]), "id") {
			continue // header row
		}
		if len(cells) < 5 {
			return nil, fmt.Errorf("expected-outcome table row at line %d has %d cells, want 5 (id | pointer | op | expected | meaning): %q", lineNo, len(cells), line)
		}
		row := ExpectedRow{
			ID:       strings.TrimSpace(cells[0]),
			Pointer:  strings.TrimSpace(cells[1]),
			Op:       strings.TrimSpace(cells[2]),
			Expected: strings.TrimSpace(cells[3]),
			Meaning:  strings.TrimSpace(cells[4]),
		}
		if row.ID == "" {
			return nil, fmt.Errorf("expected-outcome table row at line %d has an empty id", lineNo)
		}
		switch row.Op {
		case "equals", "contains", "not_contains", "regex":
		default:
			return nil, fmt.Errorf("expected-outcome table row at line %d (id %q): unknown op %q (want equals|contains|not_contains|regex)", lineNo, row.ID, row.Op)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func splitCells(inner string) []string {
	parts := strings.Split(inner, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

func isSeparatorRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if !separatorCellPattern.MatchString(strings.TrimSpace(c)) {
			return false
		}
	}
	return true
}

// Status is a single expected-outcome row's triage verdict against a
// returned evidence blob.
type Status string

const (
	// StatusMatch: the blob's value at Pointer satisfies Op against
	// Expected.
	StatusMatch Status = "match"
	// StatusDivergence: the blob's value at Pointer does NOT satisfy Op
	// against Expected — the row's Meaning explains what that implies.
	StatusDivergence Status = "divergence"
	// StatusItemMissing: no item with this ID exists in the blob at all
	// (the harness didn't run this check, or the id was renamed/typo'd).
	StatusItemMissing Status = "item-missing"
	// StatusFieldMissing: the item exists but Pointer doesn't resolve
	// (e.g. a verdict_inputs key the item never set).
	StatusFieldMissing Status = "field-missing"
)

// Result is one expected-outcome row's triage outcome.
type Result struct {
	Row    ExpectedRow
	Actual string
	Status Status
	Detail string
}

// Triage compares every expected row against the blob and returns one
// Result per row, in row order. It never mutates the blob and never
// decides anything the blob's raw fields didn't already say — this
// function is the one place T-1802/T-1804/T-1808 are expected to call
// after a human returns an evidence blob, per docs/roadmap-proven.md D7.
func Triage(blob *Blob, expected []ExpectedRow) []Result {
	results := make([]Result, 0, len(expected))
	for _, row := range expected {
		item, ok := blob.ItemByID(row.ID)
		if !ok {
			results = append(results, Result{
				Row:    row,
				Status: StatusItemMissing,
				Detail: fmt.Sprintf("no item with id %q in this blob", row.ID),
			})
			continue
		}
		actual, found := extract(item, row.Pointer)
		if !found {
			results = append(results, Result{
				Row:    row,
				Status: StatusFieldMissing,
				Detail: fmt.Sprintf("pointer %q does not resolve on item %q", row.Pointer, row.ID),
			})
			continue
		}
		if evaluate(row.Op, actual, row.Expected) {
			results = append(results, Result{Row: row, Actual: actual, Status: StatusMatch})
			continue
		}
		results = append(results, Result{
			Row:    row,
			Actual: actual,
			Status: StatusDivergence,
			Detail: row.Meaning,
		})
	}
	return results
}

// Diverged reports whether any result in a Triage run is not a clean
// match — a convenience for callers that just want a pass/fail gate
// (e.g. planning/validation's own tests) without walking every result.
func Diverged(results []Result) bool {
	for _, r := range results {
		if r.Status != StatusMatch {
			return true
		}
	}
	return false
}

func extract(item Item, pointer string) (value string, found bool) {
	switch pointer {
	case "raw":
		return item.Raw, true
	case "exit_code":
		return strconv.Itoa(item.ExitCode), true
	case "command":
		return item.Command, true
	case "checklist_ref":
		return item.ChecklistRef, true
	}
	const prefix = "verdict_inputs."
	if !strings.HasPrefix(pointer, prefix) {
		return "", false
	}
	key := strings.TrimPrefix(pointer, prefix)
	v, ok := item.VerdictInputs[key]
	if !ok {
		return "", false
	}
	return formatValue(v), true
}

// formatValue renders a decoded-JSON value (string, float64, bool, or nil
// — the types encoding/json produces for map[string]any) the way a human
// would type it in an expected-outcome table: whole-number floats without
// a trailing ".0" (every JSON number in a verdict_inputs object here is
// conceptually an integer — an HTTP status, an exit code, a count).
func formatValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

func evaluate(op, actual, expected string) bool {
	switch op {
	case "equals":
		return actual == expected
	case "contains":
		return strings.Contains(actual, expected)
	case "not_contains":
		return !strings.Contains(actual, expected)
	case "regex":
		re, err := regexp.Compile(expected)
		if err != nil {
			return false
		}
		return re.MatchString(actual)
	default:
		return false
	}
}
