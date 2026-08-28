package telemetry

import (
	"fmt"
	"strings"
	"testing"
)

// docFixture renders a field table the way docs/security.md carries it, so
// the tests below can change one cell and watch the comparison notice.
func docFixture(rows ...string) string {
	var b strings.Builder
	b.WriteString("# Security design\n\nProse above the table.\n\n")
	b.WriteString(docTableBegin + "\n\n")
	b.WriteString("| " + strings.Join(docColumns, " | ") + " |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, r := range rows {
		b.WriteString(r + "\n")
	}
	b.WriteString("\n" + docTableEnd + "\n\nProse below the table.\n")
	return b.String()
}

func docRowFor(f Field) string {
	return fmt.Sprintf("| `%s` | %s | what it is | why it carries no identity |", f.Name, f.Type)
}

// TestDocSectionMatchesPayload is T-2503 AC6: the shipped pair, checked on
// every `make check`. Adding a field to Payload without documenting it in
// docs/security.md fails here, and so does documenting a field that no
// longer exists.
func TestDocSectionMatchesPayload(t *testing.T) {
	rows, err := LoadDocRows()
	if err != nil {
		t.Fatalf("%v", err)
	}
	fields := PayloadFields()
	if len(fields) == 0 {
		t.Fatal("reflection found no payload fields, so this comparison would pass against an empty document")
	}
	if err := CompareDoc(fields, rows, DocRelPath); err != nil {
		t.Fatalf("%s and the telemetry payload struct disagree: %v", DocRelPath, err)
	}
	t.Logf("%d fields, stated identically in %s and internal/telemetry.Payload", len(rows), DocRelPath)
}

// TestCompareDocFailsInBothDirections is the mutation proof for the gate
// above: each case changes exactly one side and requires the comparison to
// fail. A gate that only noticed changes to one of the two would let the
// other drift, which is the state a hand-written "what we collect" list is
// always in.
func TestCompareDocFailsInBothDirections(t *testing.T) {
	fields := PayloadFields()
	allRows := make([]string, 0, len(fields))
	for _, f := range fields {
		allRows = append(allRows, docRowFor(f))
	}

	// The strings lead and the slice trails: govet's fieldalignment wants
	// the pointer-bearing prefix short, and a slice's len/cap tail is the
	// cheapest thing to put last.
	cases := []struct {
		name    string
		doc     string
		wantErr string
		fields  []Field
	}{
		{
			name:    "agreeing",
			fields:  fields,
			doc:     docFixture(allRows...),
			wantErr: "",
		},
		{
			name:    "a field was added to the CODE and not documented",
			fields:  append(append([]Field{}, fields...), Field{Name: "clusterName", Type: "string"}),
			doc:     docFixture(allRows...),
			wantErr: "has no row",
		},
		{
			name:    "a field was removed from the code and the document still promises it",
			fields:  fields[:len(fields)-1],
			doc:     docFixture(allRows...),
			wantErr: "documents a field the payload does not have",
		},
		{
			name:    "the document calls a count a list",
			fields:  fields,
			doc:     docFixture(append(rowsExcept(allRows, "nodeCount"), "| `nodeCount` | string list | what it is | why |")...),
			wantErr: `calls it "string list"`,
		},
		{
			name:    "a row that does not say why the field carries no identity",
			fields:  fields,
			doc:     docFixture(append(rowsExcept(allRows, "kernel"), "| `kernel` | string | what it is |  |")...),
			wantErr: "does not say why",
		},
		{
			name:    "a row that does not say what the field is",
			fields:  fields,
			doc:     docFixture(append(rowsExcept(allRows, "kernel"), "| `kernel` | string |  | why |")...),
			wantErr: "does not say what it is",
		},
		{
			name:    "the same field documented twice",
			fields:  fields,
			doc:     docFixture(append(allRows, docRowFor(fields[0]))...),
			wantErr: "documented twice",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := ParseDocTable(tc.doc)
			if err != nil {
				t.Fatalf("parsing the fixture: %v", err)
			}
			err = CompareDoc(tc.fields, rows, DocRelPath)
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

// rowsExcept drops the row for one field so a case can substitute its own.
func rowsExcept(rows []string, field string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if strings.HasPrefix(r, "| `"+field+"` ") {
			continue
		}
		out = append(out, r)
	}
	return out
}

func TestParseDocTableRejects(t *testing.T) {
	fields := PayloadFields()
	cases := []struct {
		name    string
		md      string
		wantErr string
	}{
		{name: "no markers", md: "# nothing here\n", wantErr: "no <!-- telemetry-fields:begin -->"},
		{
			name:    "a reordered column",
			md:      strings.Replace(docFixture(docRowFor(fields[0])), "| Field | Type |", "| Type | Field |", 1),
			wantErr: "column 1 is",
		},
		{
			name:    "a row with a missing cell",
			md:      docFixture("| `kernel` | string | what it is |"),
			wantErr: "row has 3 columns",
		},
		{name: "an empty table", md: docFixture(), wantErr: "has no rows"},
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

// TestPayloadFieldsWalksNestedStructs pins the reflection half: `checks` is
// a slice of structs, and a gate that stopped at the top level would let
// three fields inside it go undocumented.
func TestPayloadFieldsWalksNestedStructs(t *testing.T) {
	want := []string{
		"payloadVersion", "installId", "vnproxVersion", "pveVersion", "kernel",
		"nicPciIds", "nodeCount", "suite",
		"checks[].id", "checks[].status", "checks[].durationMs",
	}
	got := PayloadFields()
	if len(got) != len(want) {
		t.Fatalf("PayloadFields() = %v, want %v", got, want)
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("field %d is %q, want %q", i, got[i].Name, name)
		}
	}
}

// TestEveryDocumentedFieldIsAlsoAllowedByTheGuard closes the loop between
// the two gates: the document, the struct and the guard's closed schema all
// name the same set. A field documented and shipped but rejected by the
// guard would make every send fail; one allowed by the guard but not
// documented would be a promise broken quietly.
func TestEveryDocumentedFieldIsAlsoAllowedByTheGuard(t *testing.T) {
	snap := mustBuild(t)
	raw := string(snap.Bytes())
	for _, f := range PayloadFields() {
		leaf := f.Name
		if i := strings.LastIndex(leaf, "."); i >= 0 {
			leaf = leaf[i+1:]
		}
		if !strings.Contains(raw, `"`+leaf+`"`) {
			t.Errorf("field %q is documented and reflected but does not appear in the bytes a real Build produces", f.Name)
		}
	}
	if err := Guard(snap.Bytes(), nil); err != nil {
		t.Fatalf("the guard rejects the payload this build produces: %v", err)
	}
}
