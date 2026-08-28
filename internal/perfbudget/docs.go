// SPDX-License-Identifier: Apache-2.0

package perfbudget

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DocRelPath is the prose half of the contract. docs/performance.md is where
// somebody looks up what the budget is; perf/budgets.json is what the gate
// enforces. T-2506 AC2 is that those two can never disagree, which is why
// CompareDoc exists and why TestDocTableMatchesBudgets runs on every
// `make check`.
const DocRelPath = "docs/performance.md"

// The table in DocRelPath is fenced by these markers so the parser reads
// exactly one table and cannot drift onto a neighbouring one when the document
// is edited around it.
const (
	docTableBegin = "<!-- perf-budgets:begin -->"
	docTableEnd   = "<!-- perf-budgets:end -->"
)

// docColumns is the header the table must have, in order. Spelled out so a
// reordered column is a parse error rather than a silent field swap.
var docColumns = []string{"Budget", "Limit", "Direction", "Samples (N)", "Normalisation", "Enforcement", "Measured by"}

// DocRow is one row of the documented budget table.
type DocRow struct {
	ID          string
	Limit       string
	Direction   string
	Samples     string
	Scaling     string
	Enforcement string
	Site        string
	// Line is the 1-based line number in DocRelPath, so a mismatch says where
	// to go and fix it.
	Line int
}

// ParseDocTable extracts the fenced budget table from a markdown document.
func ParseDocTable(md string) ([]DocRow, error) {
	lines := strings.Split(md, "\n")
	begin, end := -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case docTableBegin:
			if begin >= 0 {
				return nil, fmt.Errorf("%s appears twice (lines %d and %d)", docTableBegin, begin+1, i+1)
			}
			begin = i
		case docTableEnd:
			if end >= 0 {
				return nil, fmt.Errorf("%s appears twice (lines %d and %d)", docTableEnd, end+1, i+1)
			}
			end = i
		}
	}
	if begin < 0 || end < 0 {
		return nil, fmt.Errorf("no %s ... %s block", docTableBegin, docTableEnd)
	}
	if end < begin {
		return nil, fmt.Errorf("%s (line %d) comes before %s (line %d)", docTableEnd, end+1, docTableBegin, begin+1)
	}

	var rows []DocRow
	header := false
	for i := begin + 1; i < end; i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := splitRow(line)
		if !header {
			if len(cells) != len(docColumns) {
				return nil, fmt.Errorf("line %d: header has %d columns, want %d (%s)", i+1, len(cells), len(docColumns), strings.Join(docColumns, " | "))
			}
			for c, want := range docColumns {
				if cells[c] != want {
					return nil, fmt.Errorf("line %d: column %d is %q, want %q", i+1, c+1, cells[c], want)
				}
			}
			header = true
			continue
		}
		if isSeparator(cells) {
			continue
		}
		if len(cells) != len(docColumns) {
			return nil, fmt.Errorf("line %d: row has %d columns, want %d", i+1, len(cells), len(docColumns))
		}
		rows = append(rows, DocRow{
			ID:          unquote(cells[0]),
			Limit:       cells[1],
			Direction:   cells[2],
			Samples:     cells[3],
			Scaling:     cells[4],
			Enforcement: cells[5],
			Site:        unquote(cells[6]),
			Line:        i + 1,
		})
	}
	if !header {
		return nil, fmt.Errorf("the %s block has no table header", docTableBegin)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("the %s block has no rows", docTableBegin)
	}
	return rows, nil
}

// CompareDoc is T-2506 AC2 in one function: it fails when the documented table
// and the machine-readable file disagree, in either direction — a budget with
// no row, a row with no budget, or a row whose numbers differ.
func CompareDoc(f File, rows []DocRow) error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	byID := make(map[string]DocRow, len(rows))
	for _, r := range rows {
		if prev, dup := byID[r.ID]; dup {
			add("%s: documented twice (lines %d and %d)", r.ID, prev.Line, r.Line)
			continue
		}
		byID[r.ID] = r
	}

	for _, b := range f.Budgets {
		row, ok := byID[b.ID]
		if !ok {
			add("%s: in %s but has no row in %s's budget table — a budget the gate enforces and the document does not state is a number in a program",
				b.ID, RepoRelPath, DocRelPath)
			continue
		}
		wantLimit := formatDocLimit(b.Limit, b.Unit)
		if row.Limit != wantLimit {
			add("%s: %s line %d says limit %q, %s says %q", b.ID, DocRelPath, row.Line, row.Limit, RepoRelPath, wantLimit)
		}
		if row.Direction != string(b.Direction) {
			add("%s: %s line %d says direction %q, %s says %q", b.ID, DocRelPath, row.Line, row.Direction, RepoRelPath, b.Direction)
		}
		if row.Samples != strconv.Itoa(b.Samples) {
			add("%s: %s line %d says samples %q, %s says %q", b.ID, DocRelPath, row.Line, row.Samples, RepoRelPath, strconv.Itoa(b.Samples))
		}
		if row.Scaling != string(b.Scaling) {
			add("%s: %s line %d says normalisation %q, %s says %q", b.ID, DocRelPath, row.Line, row.Scaling, RepoRelPath, b.Scaling)
		}
		if row.Enforcement != string(b.Enforcement) {
			add("%s: %s line %d says enforcement %q, %s says %q", b.ID, DocRelPath, row.Line, row.Enforcement, RepoRelPath, b.Enforcement)
		}
		if row.Site != b.Site {
			add("%s: %s line %d says it is measured by %q, %s says %q", b.ID, DocRelPath, row.Line, row.Site, RepoRelPath, b.Site)
		}
	}

	known := make(map[string]bool, len(f.Budgets))
	for _, b := range f.Budgets {
		known[b.ID] = true
	}
	for _, r := range rows {
		if !known[r.ID] {
			add("%s: %s line %d documents a budget that is not in %s — nothing measures or enforces it", r.ID, DocRelPath, r.Line, RepoRelPath)
		}
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// LoadDocRows reads and parses the repository's own docs/performance.md table.
func LoadDocRows() ([]DocRow, error) {
	root, err := RepoRoot()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, DocRelPath)
	raw, err := os.ReadFile(path) //nolint:gosec // a repo-relative path this repo owns
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", DocRelPath, err)
	}
	rows, err := ParseDocTable(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", DocRelPath, err)
	}
	return rows, nil
}

// formatDocLimit is the one rendering of a limit both halves agree on. Written
// once here so "150 ms" in the document and 150 in the file are compared by
// construction rather than by a reader's eye.
func formatDocLimit(limit float64, unit string) string {
	return strconv.FormatFloat(limit, 'f', -1, 64) + " " + unit
}

func splitRow(line string) []string {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "|"), "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, 0, len(parts))
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	return cells
}

func isSeparator(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return true
}

func unquote(cell string) string {
	return strings.Trim(cell, "`")
}
