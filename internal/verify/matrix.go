// SPDX-License-Identifier: Apache-2.0

package verify

// matrix.go is the join between this package's check registry and
// docs/status-matrix.md §2, in both directions (AC1).
//
// The one-directional version of this — "every check names a real row" — is
// the easy half and catches typos. The half that matters is the other one:
// a row claiming `V` (validated on real PVE) with no check behind it is a
// claim the repository can no longer substantiate, and it is exactly what
// happens over time as a validated row's evidence ages out and nobody
// notices. Reconcile fails the build for both, so the matrix and the suite
// cannot drift apart silently.

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// HW column values, from the matrix's own legend (§1).
const (
	// HWValidated — validated on real PVE.
	HWValidated = "V"
	// HWMock — mock-validated only.
	HWMock = "M"
	// HWBlocked — blocked, needs hardware this project does not have.
	HWBlocked = "B"
	// HWNotApplicable — the row has no hardware dimension.
	HWNotApplicable = "—"
)

// Row is one docs/status-matrix.md §2 feature-area row.
type Row struct {
	// Area is the feature-area title, with markdown emphasis and code ticks
	// stripped.
	Area string
	// HW is the hardware-validation mark.
	HW string
	// Notes is the trailing notes column.
	Notes string
	// Number is the row's "#" column.
	Number int
}

// matrixSectionHeading is where §2's table starts. Anchoring on the heading
// rather than "the first markdown table in the file" means a table added
// above it does not silently become the row set.
const matrixSectionHeading = "## 2. Feature-area matrix"

var matrixRowPattern = regexp.MustCompile(`^\|\s*(\d+)\s*\|`)

// ParseMatrix extracts §2's rows from the raw markdown.
//
// It is deliberately strict about the column count: a row with the wrong
// number of cells means the table's shape changed, and silently reading the
// HW mark out of whatever column happened to be tenth would produce a
// confidently wrong reconciliation.
func ParseMatrix(markdown []byte) ([]Row, error) {
	text := string(markdown)
	idx := strings.Index(text, matrixSectionHeading)
	if idx < 0 {
		return nil, fmt.Errorf("verify: status matrix has no %q section", matrixSectionHeading)
	}

	const wantCells = 11 // #, area, backend, gui, api, help, docs, unit, e2e, hw, notes
	var rows []Row
	for _, line := range strings.Split(text[idx:], "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") && !strings.HasPrefix(trimmed, matrixSectionHeading) {
			break // the section ended
		}
		m := matrixRowPattern.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		cells := splitTableRow(trimmed)
		if len(cells) != wantCells {
			return nil, fmt.Errorf("verify: status matrix row %q has %d cells, want %d — the table's shape changed and the HW column can no longer be located by position", m[1], len(cells), wantCells)
		}
		number, err := strconv.Atoi(cells[0])
		if err != nil {
			return nil, fmt.Errorf("verify: status matrix row %q has a non-numeric number: %w", trimmed, err)
		}
		rows = append(rows, Row{
			Number: number,
			Area:   NormalizeArea(cells[1]),
			HW:     strings.TrimSpace(strings.ReplaceAll(cells[9], "*", "")),
			Notes:  cells[10],
		})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("verify: status matrix §2 has no numbered rows")
	}
	return rows, nil
}

// splitTableRow splits a markdown table row into its cells, dropping the
// leading and trailing pipe.
func splitTableRow(line string) []string {
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// NormalizeArea strips the markdown a feature-area title carries (bold
// emphasis on new rows, backticks around code identifiers) so a registry
// entry can spell the title as prose.
func NormalizeArea(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "`", "")
	return strings.TrimSpace(s)
}

// Reconcile checks the registry against the matrix in both directions (AC1).
//
// It returns one error listing every problem rather than the first, because
// the person reading it is going to fix all of them and a one-at-a-time
// build failure wastes their afternoon.
func Reconcile(checks []Check, rows []Row) error {
	byNumber := make(map[int]Row, len(rows))
	for _, r := range rows {
		byNumber[r.Number] = r
	}
	backed := make(map[int][]string, len(rows))

	var problems []string
	for _, c := range checks {
		row, ok := byNumber[c.MatrixRow]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"check %q names status-matrix.md §2 row %d, which does not exist (the table has rows 1..%d)",
				c.ID, c.MatrixRow, maxRowNumber(rows)))
			continue
		}
		if NormalizeArea(c.Area) != row.Area {
			problems = append(problems, fmt.Sprintf(
				"check %q claims row %d is %q but the matrix calls it %q — the row was renamed or the check points at the wrong one",
				c.ID, c.MatrixRow, c.Area, row.Area))
		}
		backed[row.Number] = append(backed[row.Number], c.ID)
	}

	// The direction that matters: a row asserting hardware validation with
	// nothing that re-establishes it.
	for _, r := range rows {
		if len(backed[r.Number]) > 0 {
			continue
		}
		switch r.HW {
		case HWValidated:
			problems = append(problems, fmt.Sprintf(
				"status-matrix.md §2 row %d (%q) claims HW=%s — validated on real PVE — but no check backs it. A validated row with nothing that re-checks it is a claim the repository can no longer substantiate",
				r.Number, r.Area, HWValidated))
		case HWBlocked:
			// AC7: every blocked row gets a check, so that "blocked" becomes
			// "here is exactly what to run once you have the hardware"
			// instead of a permanent asterisk.
			problems = append(problems, fmt.Sprintf(
				"status-matrix.md §2 row %d (%q) is marked HW=%s (blocked) with no check to run once the hardware exists (AC7)",
				r.Number, r.Area, HWBlocked))
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("verify: the check registry and docs/status-matrix.md §2 disagree:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

func maxRowNumber(rows []Row) int {
	var max int
	for _, r := range rows {
		if r.Number > max {
			max = r.Number
		}
	}
	return max
}

// RowsByHW groups rows by their hardware mark, which is what a generator of
// the HW column reads.
func RowsByHW(rows []Row, hw string) []Row {
	var out []Row
	for _, r := range rows {
		if r.HW == hw {
			out = append(out, r)
		}
	}
	return out
}

// HWFromReport is the mark a row earns from a signed report: `V` once every
// check backing it passed, and the row's existing mark otherwise.
//
// This is the "status-matrix.md's HW column becomes generated from it" half of
// the card. It is deliberately conservative in one direction — a report can
// only ever *raise* a row to V, never lower an existing V — because a suite
// run on a smaller cluster than the one that validated a row would otherwise
// silently retract that row's validation.
func HWFromReport(row Row, checks []Check, report Report) string {
	byID := make(map[string]Result, len(report.Results))
	for _, res := range report.Results {
		byID[res.ID] = res
	}
	var backing, passed int
	for _, c := range checks {
		if c.MatrixRow != row.Number {
			continue
		}
		res, ran := byID[c.ID]
		if !ran {
			continue
		}
		backing++
		if res.Status == StatusPass {
			passed++
		}
	}
	if backing > 0 && backing == passed {
		return HWValidated
	}
	return row.HW
}
