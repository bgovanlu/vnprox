package verify

// matrix_test.go is T-2501 AC1, in both directions, against the real
// docs/status-matrix.md rather than against a fixture copy of it.
//
// Reading the shipped file is the whole point. A test that parsed an inlined
// copy of the table would keep passing forever after someone renumbered a row
// — which is exactly the drift this test exists to catch, and exactly the
// mistake web/src/help/coverage.test.ts avoided by parsing the real router.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// statusMatrixPath is the shipped matrix, relative to this package.
const statusMatrixPath = "../../docs/status-matrix.md"

func loadMatrix(t *testing.T) []Row {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(statusMatrixPath))
	if err != nil {
		t.Fatalf("reading %s: %v", statusMatrixPath, err)
	}
	rows, err := ParseMatrix(raw)
	if err != nil {
		t.Fatalf("parsing %s: %v", statusMatrixPath, err)
	}
	return rows
}

// TestRegistryReconcilesWithTheShippedMatrix is AC1 itself: every check id
// resolves to a real row, every row claiming `V` is backed, and every row
// marked `B` has something to run once the hardware exists.
func TestRegistryReconcilesWithTheShippedMatrix(t *testing.T) {
	if err := Reconcile(Checks(), loadMatrix(t)); err != nil {
		t.Fatal(err)
	}
}

// TestReconcileCatchesACheckNamingARowThatDoesNotExist is AC1's first
// direction, proven by mutation: the guard is only worth having if it fires.
func TestReconcileCatchesACheckNamingARowThatDoesNotExist(t *testing.T) {
	rows := loadMatrix(t)
	checks := append([]Check(nil), Checks()...)
	checks = append(checks, Check{
		ID:           "ghost.row",
		MatrixRow:    9999,
		Area:         "A feature area that was never in the matrix",
		Suite:        SuiteHardware,
		MinNodes:     1,
		Precondition: "nothing, because this row does not exist",
		Run:          func(_ context.Context, _ Deps) Outcome { return Pass("x", NewEvidence("state", "x", "x")) },
	})

	err := Reconcile(checks, rows)
	if err == nil {
		t.Fatal("Reconcile accepted a check naming status-matrix.md row 9999")
	}
	if !strings.Contains(err.Error(), "ghost.row") || !strings.Contains(err.Error(), "9999") {
		t.Errorf("the error does not name the offending check and row: %v", err)
	}
}

// TestReconcileCatchesARenamedRow: pointing at a row by number alone would let
// a rename silently re-attribute a check to a different feature. Carrying the
// title too turns that into a build failure.
func TestReconcileCatchesARenamedRow(t *testing.T) {
	rows := loadMatrix(t)
	// Rename the row the LLDP check is registered against.
	for i := range rows {
		if rows[i].Number == 32 {
			rows[i].Area = "Something else entirely"
		}
	}
	err := Reconcile(Checks(), rows)
	if err == nil {
		t.Fatal("Reconcile accepted a check whose declared area no longer matches the matrix row it names")
	}
	if !strings.Contains(err.Error(), "Something else entirely") {
		t.Errorf("the error does not name what the matrix now calls the row: %v", err)
	}
}

// TestReconcileCatchesAValidatedRowWithNoCheck is AC1's *second* direction,
// which is the one that matters.
//
// A row marked `V` says "validated on real PVE". Once the person who ran that
// validation is gone, the only thing standing behind the claim is a check
// that re-establishes it. This test proves the build fails when a row asserts
// hardware validation with nothing behind it — including for a row that has
// never had a check, which is how the claim would rot in practice.
func TestReconcileCatchesAValidatedRowWithNoCheck(t *testing.T) {
	rows := loadMatrix(t)

	// Find a row nothing currently backs and promote it to `V`, which is what
	// a well-meaning future edit to the matrix looks like.
	backed := map[int]bool{}
	for _, c := range Checks() {
		backed[c.MatrixRow] = true
	}
	var promoted *Row
	for i := range rows {
		if !backed[rows[i].Number] && rows[i].HW == HWMock {
			rows[i].HW = HWValidated
			promoted = &rows[i]
			break
		}
	}
	if promoted == nil {
		t.Fatal("every mock-validated row already has a check, so this mutation cannot be constructed")
	}

	err := Reconcile(Checks(), rows)
	if err == nil {
		t.Fatalf("Reconcile accepted row %d (%q) claiming HW=V with no check backing it", promoted.Number, promoted.Area)
	}
	if !strings.Contains(err.Error(), promoted.Area) {
		t.Errorf("the error does not name the unbacked row: %v", err)
	}
}

// TestReconcileCatchesAnUnbackedBlockedRow is AC7's structural half: a row
// marked `B` with no check is a permanent asterisk rather than an instruction.
func TestReconcileCatchesAnUnbackedBlockedRow(t *testing.T) {
	rows := loadMatrix(t)
	backed := map[int]bool{}
	for _, c := range Checks() {
		backed[c.MatrixRow] = true
	}
	var promoted *Row
	for i := range rows {
		if !backed[rows[i].Number] && rows[i].HW == HWMock {
			rows[i].HW = HWBlocked
			promoted = &rows[i]
			break
		}
	}
	if promoted == nil {
		t.Fatal("no unbacked row available to mark blocked")
	}
	err := Reconcile(Checks(), rows)
	if err == nil {
		t.Fatalf("Reconcile accepted row %d (%q) marked HW=B with nothing to run once the hardware exists", promoted.Number, promoted.Area)
	}
}

// TestEveryBlockedRowIsCovered is AC7's positive half, stated against the
// shipped matrix so the count in the message is the real one.
func TestEveryBlockedRowIsCovered(t *testing.T) {
	rows := loadMatrix(t)
	backed := map[int][]string{}
	for _, c := range Checks() {
		backed[c.MatrixRow] = append(backed[c.MatrixRow], c.ID)
	}

	var blocked, uncovered []string
	for _, r := range RowsByHW(rows, HWBlocked) {
		blocked = append(blocked, r.Area)
		if len(backed[r.Number]) == 0 {
			uncovered = append(uncovered, r.Area)
		}
	}
	if len(blocked) == 0 {
		t.Fatal("the matrix has no rows marked B, so this test is not testing anything — did the HW column's vocabulary change?")
	}
	if len(uncovered) > 0 {
		t.Errorf("%d of %d blocked feature area(s) have no check (AC7): %s", len(uncovered), len(blocked), strings.Join(uncovered, "; "))
	}
	t.Logf("%d blocked feature areas, all covered", len(blocked))
}

// TestAtLeastTwentyChecksShip is AC7's other half. The number is in the card,
// so it is asserted rather than assumed.
func TestAtLeastTwentyChecksShip(t *testing.T) {
	const want = 20
	if got := len(Checks()); got < want {
		t.Errorf("%d checks registered, want at least %d (AC7)", got, want)
	}
}

// TestEveryCheckStatesAPrecondition is AC7's per-check half.
func TestEveryCheckStatesAPrecondition(t *testing.T) {
	for _, c := range Checks() {
		if strings.TrimSpace(c.Precondition) == "" {
			t.Errorf("check %q states no hardware precondition", c.ID)
			continue
		}
		// A precondition that just restates the check's own name tells an
		// operator nothing about what to go and get.
		if strings.EqualFold(c.Precondition, c.ID) || len(c.Precondition) < 25 {
			t.Errorf("check %q's precondition is too thin to act on: %q", c.ID, c.Precondition)
		}
	}
}

// TestRegistryIsWellFormed runs the same validation Run does, so a malformed
// registry fails the build rather than the first person to run the command.
func TestRegistryIsWellFormed(t *testing.T) {
	if err := ValidateRegistry(Checks()); err != nil {
		t.Fatal(err)
	}
}

// TestValidateRegistryCatchesAMissingPrecondition proves that guard fires.
func TestValidateRegistryCatchesAMissingPrecondition(t *testing.T) {
	checks := []Check{{
		ID:        "x.y",
		MatrixRow: 1,
		Area:      "a",
		Suite:     SuiteHardware,
		MinNodes:  1,
		Run:       func(_ context.Context, _ Deps) Outcome { return Pass("x") },
	}}
	err := ValidateRegistry(checks)
	if err == nil || !strings.Contains(err.Error(), "precondition") {
		t.Fatalf("ValidateRegistry accepted a check with no precondition: %v", err)
	}
}

// TestParseMatrixRejectsAReshapedTable: reading the HW mark by column position
// is only safe if a column-count change is fatal.
func TestParseMatrixRejectsAReshapedTable(t *testing.T) {
	const reshaped = matrixSectionHeading + "\n\n" +
		"| # | Feature area | Backend | HW | Notes |\n" +
		"|---|---|---|---|---|\n" +
		"| 1 | Topology map | ● | M | |\n"
	if _, err := ParseMatrix([]byte(reshaped)); err == nil {
		t.Fatal("ParseMatrix accepted a table with a different column count, so the HW mark would be read out of the wrong column")
	}
}

// TestParseMatrixReadsTheRealFile pins the parse against the shipped file's
// actual content, so a silent parse of zero rows cannot look like success.
func TestParseMatrixReadsTheRealFile(t *testing.T) {
	rows := loadMatrix(t)
	if len(rows) < 50 {
		t.Fatalf("parsed only %d rows from the shipped matrix", len(rows))
	}
	byNumber := map[int]Row{}
	for _, r := range rows {
		byNumber[r.Number] = r
	}
	// Two rows spot-checked against the file, one bold and one backticked, so
	// NormalizeArea's markdown stripping is covered by the real markup rather
	// than by a synthetic string.
	if got := byNumber[66].Area; got != "Certificate management (new)" {
		t.Errorf("row 66 parsed as %q; the ** emphasis was not stripped", got)
	}
	if got := byNumber[74].Area; got != "vnproxctl operator CLI" {
		t.Errorf("row 74 parsed as %q; the backticks were not stripped", got)
	}
	if got := byNumber[61].HW; got != HWValidated {
		t.Errorf("row 61's HW parsed as %q, want %q", got, HWValidated)
	}
}

// TestHWFromReportOnlyEverRaisesARow covers the generated-HW-column rule.
//
// A suite run on a smaller cluster than the one that validated a row must not
// silently retract that row's validation, so a report can promote a row to V
// and can never demote one.
func TestHWFromReportOnlyEverRaisesARow(t *testing.T) {
	checks := Checks()
	var lldp Check
	for _, c := range checks {
		if c.ID == "lldp.neighbors_match_pve_interfaces" {
			lldp = c
		}
	}
	row := Row{Number: lldp.MatrixRow, Area: lldp.Area, HW: HWMock}

	passing := Report{Results: []Result{{ID: lldp.ID, Status: StatusPass}}}
	if got := HWFromReport(row, checks, passing); got != HWValidated {
		t.Errorf("a passing check did not raise its row to %s: got %s", HWValidated, got)
	}

	skipped := Report{Results: []Result{{ID: lldp.ID, Status: StatusSkip}}}
	if got := HWFromReport(row, checks, skipped); got != HWMock {
		t.Errorf("a skipped check changed its row's mark to %s: a skip must never raise a row", got)
	}

	validatedRow := Row{Number: lldp.MatrixRow, Area: lldp.Area, HW: HWValidated}
	if got := HWFromReport(validatedRow, checks, skipped); got != HWValidated {
		t.Errorf("a run that skipped the check demoted an already-validated row to %s", got)
	}
}
