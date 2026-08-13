package telemetry

// docs.go is T-2503 AC6: docs/security.md states exactly what is collected,
// and a test asserts that statement lists every field in the payload struct.
// Adding a field without documenting it fails the build.
//
// It is the same shape as internal/perfbudget's TestDocTableMatchesBudgets
// (T-2506 AC2), for the same reason: a document that describes code is only
// worth reading if it cannot be wrong. The comparison runs in BOTH
// directions and names the line — a field with no row is a value nobody
// promised to be safe, and a row with no field means the promise has
// outlived the code and an operator is being told something false about
// their own machine.
//
// The type column is compared too, not just the name. A field that changes
// from a node COUNT to a node LIST keeps its name, and the whole difference
// between those two is the point of this feature.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// DocRelPath is the prose half of the contract.
const DocRelPath = "docs/security.md"

// The table is fenced by these markers so the parser reads exactly one
// table and cannot drift onto a neighbouring one when the document is
// edited around it.
const (
	docTableBegin = "<!-- telemetry-fields:begin -->"
	docTableEnd   = "<!-- telemetry-fields:end -->"
)

// docColumns is the header the table must have, in order.
var docColumns = []string{"Field", "Type", "What it is", "Why it carries no identity"}

// Field is one field of the payload, as reflection sees it.
type Field struct {
	// Name is the JSON path: "kernel", "checks[].status".
	Name string
	// Type is the documented type name (see docType).
	Type string
}

// DocRow is one row of the documented table.
type DocRow struct {
	Field string
	Type  string
	What  string
	Why   string
	// Line is the 1-based line number in DocRelPath, so a mismatch says
	// where to go and fix it.
	Line int
}

// PayloadFields walks the Payload struct and returns every field, nested
// structs included. Reflection rather than a hand-maintained list: a list
// somebody has to remember to update is the thing this gate exists to
// replace.
func PayloadFields() []Field {
	return structFields(reflect.TypeOf(Payload{}), "")
}

func structFields(t reflect.Type, prefix string) []Field {
	var out []Field
	for i := range t.NumField() {
		sf := t.Field(i)
		if sf.PkgPath != "" {
			// Unexported: json never sees it, so it is not part of what is
			// collected.
			continue
		}
		name := jsonName(sf)
		if name == "" {
			continue
		}
		path := prefix + name
		ft := sf.Type
		elem := ft
		if ft.Kind() == reflect.Slice {
			elem = ft.Elem()
		}
		if elem.Kind() == reflect.Struct {
			out = append(out, structFields(elem, path+"[].")...)
			continue
		}
		out = append(out, Field{Name: path, Type: docType(ft)})
	}
	return out
}

// jsonName is the field's wire name, or "" when it is not serialised.
func jsonName(sf reflect.StructField) string {
	tag := sf.Tag.Get("json")
	if tag == "-" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		name = sf.Name
	}
	return name
}

// docType is the vocabulary the table's Type column uses. Deliberately
// coarse — "number" rather than "int64" — because the document is read by an
// operator deciding whether to opt in, not by a Go programmer.
func docType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice:
		return docType(t.Elem()) + " list"
	default:
		return t.Kind().String()
	}
}

// ParseDocTable extracts the fenced field table from a markdown document.
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
			Field: unquote(cells[0]),
			Type:  cells[1],
			What:  cells[2],
			Why:   cells[3],
			Line:  i + 1,
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

// CompareDoc is AC6 in one function: it fails when the documented table and
// the payload struct disagree, in either direction.
func CompareDoc(fields []Field, rows []DocRow) error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	byName := make(map[string]DocRow, len(rows))
	for _, r := range rows {
		if prev, dup := byName[r.Field]; dup {
			add("%s: documented twice (lines %d and %d)", r.Field, prev.Line, r.Line)
			continue
		}
		byName[r.Field] = r
	}

	for _, f := range fields {
		row, ok := byName[f.Name]
		if !ok {
			add("%s: is in the telemetry payload and has no row in %s — a field that is collected and not documented is exactly what this gate exists to stop",
				f.Name, DocRelPath)
			continue
		}
		if row.Type != f.Type {
			add("%s: %s line %d calls it %q, the payload struct says %q", f.Name, DocRelPath, row.Line, row.Type, f.Type)
		}
		if strings.TrimSpace(row.What) == "" {
			add("%s: %s line %d does not say what it is", f.Name, DocRelPath, row.Line)
		}
		if strings.TrimSpace(row.Why) == "" {
			add("%s: %s line %d does not say why it carries no identity; that column is the whole promise", f.Name, DocRelPath, row.Line)
		}
	}

	known := make(map[string]bool, len(fields))
	for _, f := range fields {
		known[f.Name] = true
	}
	for _, r := range rows {
		if !known[r.Field] {
			add("%s: %s line %d documents a field the payload does not have — the document has outlived the code", r.Field, DocRelPath, r.Line)
		}
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// LoadDocRows reads and parses the repository's own docs/security.md table.
func LoadDocRows() ([]DocRow, error) {
	root, err := repoRoot()
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

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locating repo root: %w", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("locating repo root: no go.mod in any parent directory")
		}
		dir = parent
	}
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
