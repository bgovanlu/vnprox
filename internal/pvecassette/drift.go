package pvecassette

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Sides a Divergence can be present on.
const (
	SideFixture  = "fixture"
	SideCassette = "cassette"
)

// WholeResponse is the Field value used when an entire endpoint is present
// on one side and absent on the other — a coverage gap rather than a shape
// difference, and worth telling apart at a glance.
const WholeResponse = "(entire response)"

// Divergence is one field (or one whole endpoint) present on one side of a
// comparison and absent on the other.
type Divergence struct {
	// Key is the request key both sides were compared under.
	Key string
	// Field is the collapsed JSON path, e.g. "data[].bond_mode".
	Field string
	// PresentIn is SideFixture or SideCassette: the side that HAS it.
	PresentIn string
}

func (d Divergence) String() string {
	return fmt.Sprintf("%s: %s present in %s only", d.Key, d.Field, d.PresentIn)
}

// Drift compares the field sets of two cassette sets and reports every
// field present in one and absent in the other.
//
// This is T-2502's drift check. Its value is not that it passes; it is
// that its output is a list of specific field names somebody has to look
// at. A hand-written fixture that omits a field real PVE always sends is
// invisible to every unit test in this repository — the test asserts on
// what the fixture provides — and shows up here as one line.
//
// Comparison is by *field path*, not by value: values legitimately differ
// between two clusters, shapes should not. Array indices are collapsed
// (`data[0].iface` and `data[7].iface` are the same field) so a response
// with more rows on one side does not drown the report.
func Drift(fixture, cassette map[string]Cassette) []Divergence {
	var out []Divergence

	keys := map[string]bool{}
	for k := range fixture {
		keys[k] = true
	}
	for k := range cassette {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	for _, k := range sorted {
		f, inFixture := fixture[k]
		c, inCassette := cassette[k]
		switch {
		case !inCassette:
			out = append(out, Divergence{Key: k, Field: WholeResponse, PresentIn: SideFixture})
			continue
		case !inFixture:
			out = append(out, Divergence{Key: k, Field: WholeResponse, PresentIn: SideCassette})
			continue
		}
		fFields := fieldSet(f.Body)
		cFields := fieldSet(c.Body)
		for _, field := range sortedKeys(fFields) {
			if !cFields[field] {
				out = append(out, Divergence{Key: k, Field: field, PresentIn: SideFixture})
			}
		}
		for _, field := range sortedKeys(cFields) {
			if !fFields[field] {
				out = append(out, Divergence{Key: k, Field: field, PresentIn: SideCassette})
			}
		}
	}
	return out
}

// FieldPaths returns the sorted, de-duplicated set of JSON field paths in
// body, with array indices collapsed to "[]". A body that is not JSON has
// no field paths (and Drift will therefore report every field of the other
// side, which is the correct signal: one of the two is not what it claims
// to be).
func FieldPaths(body string) []string {
	return sortedKeys(fieldSet(body))
}

func fieldSet(body string) map[string]bool {
	out := map[string]bool{}
	var v any
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return out
	}
	walkFields("", v, out)
	return out
}

func walkFields(path string, v any, out map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			p := k
			if path != "" {
				p = path + "." + k
			}
			out[p] = true
			walkFields(p, child, out)
		}
	case []any:
		for _, child := range t {
			walkFields(path+"[]", child, out)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
