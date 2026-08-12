package perfbudget

import (
	"fmt"
	"strings"
	"testing"
)

// docFixture renders a budget table the way docs/performance.md carries it, so
// the tests below can mutate one cell and watch the comparison notice.
func docFixture(rows ...string) string {
	var b strings.Builder
	b.WriteString("# Some document\n\nProse above the table.\n\n")
	b.WriteString(docTableBegin + "\n\n")
	b.WriteString("| " + strings.Join(docColumns, " | ") + " |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		b.WriteString(r + "\n")
	}
	b.WriteString("\n" + docTableEnd + "\n\nProse below the table.\n")
	return b.String()
}

func docRowFor(b Budget) string {
	return fmt.Sprintf("| `%s` | %s | %s | %d | %s | %s | `%s` |",
		b.ID, formatDocLimit(b.Limit, b.Unit), b.Direction, b.Samples, b.Scaling, b.Enforcement, b.Site)
}

// TestCompareDoc_BothDirections is T-2506 AC2.
//
// The requirement is not "the document mentions the budgets"; it is that the
// prose and the machine-readable file cannot disagree. So each case changes
// exactly one of the two and requires the comparison to fail — a check that
// only caught changes to one side would let the other drift, which is the
// state docs/performance.md was already in before this card (its numbers were
// transcriptions nothing verified).
func TestCompareDoc_BothDirections(t *testing.T) {
	base := validBudget()
	f := validFile(base)

	// The strings lead and File trails: govet's fieldalignment again — File's
	// own pointer region ends 24 bytes before its end, so putting it last is
	// what shortens this struct's pointer-bearing prefix.
	cases := []struct {
		name    string
		doc     string
		wantErr string
		file    File
	}{
		{
			name:    "agreeing",
			file:    f,
			doc:     docFixture(docRowFor(base)),
			wantErr: "",
		},
		{
			name:    "the DOCUMENT's limit was changed",
			file:    f,
			doc:     docFixture("| `example.thing_ms` | 200 ms | max | 5 | calibrated | gate | `internal/example/example_test.go` |"),
			wantErr: `says limit "200 ms"`,
		},
		{
			name: "the FILE's limit was changed",
			file: func() File {
				g := validFile(base)
				g.Budgets[0].Limit = 200
				return g
			}(),
			doc:     docFixture(docRowFor(base)),
			wantErr: `says limit "100 ms"`,
		},
		{
			name:    "the document's sample count was changed",
			file:    f,
			doc:     docFixture("| `example.thing_ms` | 100 ms | max | 9 | calibrated | gate | `internal/example/example_test.go` |"),
			wantErr: `says samples "9"`,
		},
		{
			name:    "the document's enforcement was changed",
			file:    f,
			doc:     docFixture("| `example.thing_ms` | 100 ms | max | 5 | calibrated | report | `internal/example/example_test.go` |"),
			wantErr: `says enforcement "report"`,
		},
		{
			name:    "the document's normalisation was changed",
			file:    f,
			doc:     docFixture("| `example.thing_ms` | 100 ms | max | 5 | absolute | gate | `internal/example/example_test.go` |"),
			wantErr: `says normalisation "absolute"`,
		},
		{
			name:    "the document's direction was changed",
			file:    f,
			doc:     docFixture("| `example.thing_ms` | 100 ms | min | 5 | calibrated | gate | `internal/example/example_test.go` |"),
			wantErr: `says direction "min"`,
		},
		{
			name:    "the document points at the wrong measurement site",
			file:    f,
			doc:     docFixture("| `example.thing_ms` | 100 ms | max | 5 | calibrated | gate | `internal/somewhere/else_test.go` |"),
			wantErr: "measured by",
		},
		{
			name:    "a budget the document does not mention",
			file:    validFile(base, func() Budget { b := validBudget(); b.ID = "example.second_ms"; return b }()),
			doc:     docFixture(docRowFor(base)),
			wantErr: "has no row",
		},
		{
			name:    "a documented budget nothing enforces",
			file:    f,
			doc:     docFixture(docRowFor(base), "| `example.ghost_ms` | 1 ms | max | 5 | calibrated | gate | `internal/example/example_test.go` |"),
			wantErr: "documents a budget that is not in",
		},
		{
			name:    "the same budget documented twice",
			file:    f,
			doc:     docFixture(docRowFor(base), docRowFor(base)),
			wantErr: "documented twice",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := ParseDocTable(tc.doc)
			if err != nil {
				t.Fatalf("parsing the fixture: %v", err)
			}
			err = CompareDoc(tc.file, rows)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("want agreement, got %v", err)
			case tc.wantErr == "":
			case err == nil:
				t.Fatalf("want an error containing %q, got none", tc.wantErr)
			case !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("want an error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestParseDocTable_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		md      string
		wantErr string
	}{
		{name: "no markers", md: "# nothing here\n", wantErr: "no <!-- perf-budgets:begin -->"},
		{
			name:    "a reordered column",
			md:      strings.Replace(docFixture(docRowFor(validBudget())), "| Budget | Limit |", "| Limit | Budget |", 1),
			wantErr: "column 1 is",
		},
		{
			name:    "a row with a missing cell",
			md:      docFixture("| `example.thing_ms` | 100 ms | max | 5 | calibrated | gate |"),
			wantErr: "row has 6 columns",
		},
		{
			name:    "an empty table",
			md:      docFixture(),
			wantErr: "has no rows",
		},
		{
			name:    "the markers the wrong way round",
			md:      docTableEnd + "\n\n" + docTableBegin + "\n",
			wantErr: "comes before",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDocTable(tc.md)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want an error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestDocTableMatchesBudgets is the shipped pair, checked on every
// `make check` — the reason nobody has to remember to update the document.
func TestDocTableMatchesBudgets(t *testing.T) {
	f, err := LoadRepo()
	if err != nil {
		t.Fatalf("%v", err)
	}
	rows, err := LoadDocRows()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if err := CompareDoc(f, rows); err != nil {
		t.Fatalf("%s and %s disagree: %v", DocRelPath, RepoRelPath, err)
	}
	t.Logf("%d budgets, stated identically in %s and %s", len(rows), RepoRelPath, DocRelPath)
}
