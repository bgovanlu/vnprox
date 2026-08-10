// pitdiff.go implements T-2704's point-in-time topology diff — the pure half.
//
// THE GAP THIS CLOSES. A changeset records what *vnprox* did, and the history
// timeline plays those records back. Neither answers "what is different about
// this cluster compared to Tuesday". That gap is exactly the class of change
// the drift checker exists to catch: the one a human made over SSH. This file
// computes the answer; internal/change/topodiff.go resolves the two points and
// attributes each difference (or honestly marks it unattributed).
//
// TWO PROPERTIES WORTH STATING, because each is the difference between a diff
// an operator can act on and one that quietly lies:
//
//   - DETERMINISTIC ORDER. Every list this file produces is ordered by a stable
//     key (the entity's Ref string, then the field name). Nothing here iterates
//     a Go map into output. internal/pvemock's list endpoints are known to be
//     order-nondeterministic (T-2502-followup-01), and a diff whose output
//     depended on any such ordering would flake — worse, it would report
//     spurious "modified" rows on a cluster nobody touched.
//
//   - NO SYNTHESISED ABSENCE. An entity is reported removed only when its node
//     was actually captured on both sides. Deciding otherwise would let a
//     snapshot that simply did not cover a node read as "every bridge on it was
//     deleted", which is the same false statement in the other direction as an
//     empty diff reading as "nothing changed". Node scoping is the caller's
//     job (see change.Service.TopologyDiff); this file only ever compares what
//     it is handed.
//
// Read-only by construction: nothing in this file touches a gateway, a store,
// or PVE. It takes text and returns values.

package topology

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// DiffChange names what happened to one entity between two points.
type DiffChange string

const (
	// DiffAdded: the entity exists at `to` and did not exist at `from`.
	DiffAdded DiffChange = "added"
	// DiffRemoved: the entity existed at `from` and does not exist at `to`.
	DiffRemoved DiffChange = "removed"
	// DiffModified: the entity exists at both points with at least one
	// differing field.
	DiffModified DiffChange = "modified"
)

// PointEntity is one entity's comparable state at a single point in time: its
// identity plus a canonical, string-valued field map.
//
// Fields is deliberately string-valued rather than typed. A diff renders
// before/after to a human, and the only operation this package performs on a
// value is equality — so the canonical string form (canonicalValue) IS the
// value as far as this file is concerned. A field whose canonical form is
// empty is absent from the map, so "unset" and "set to the zero value" both
// render as an empty before/after rather than as `0`/`false` noise on every
// entity.
type PointEntity struct {
	Fields map[string]string
	Name   string
	Ref    inventory.Ref
}

// FieldChange is one field's before/after across the range — AC6's
// "field-level before/after ... not merely modified".
type FieldChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// DiffAttribution answers "did vnprox do this?" for one entity change.
//
// The zero value means **unattributed**, and that is the whole point of the
// type: an unattributed change is an out-of-band change — someone edited
// /etc/network/interfaces over SSH — and it is the single most operationally
// interesting row in any diff. `attributed` is therefore always serialised
// (no omitempty): a reader must never have to infer absence from a missing
// key.
//
// This package never fills it in; it has no knowledge of changesets. The
// change engine does that (see change.Service.TopologyDiff).
type DiffAttribution struct {
	ChangesetID    string `json:"changesetId,omitempty"`
	ChangesetTitle string `json:"changesetTitle,omitempty"`
	Actor          string `json:"actor,omitempty"`
	At             int64  `json:"at,omitempty"`
	Attributed     bool   `json:"attributed"`
}

// EntityDiff is one entity's difference between the two points.
type EntityDiff struct {
	Ref         string          `json:"ref"`
	Kind        string          `json:"kind"`
	Node        string          `json:"node,omitempty"`
	Name        string          `json:"name,omitempty"`
	Change      DiffChange      `json:"change"`
	Fields      []FieldChange   `json:"fields"`
	Attribution DiffAttribution `json:"attribution"`
}

// refIdentityFields are the entity fields that encode identity rather than
// configuration. They are already carried by EntityDiff.Ref/Node, and an
// entity whose Ref differs is a different entity, so they can never appear as
// a field-level change — including them would put three constant rows on every
// "added"/"removed" entry.
//
//nolint:gochecknoglobals // a lookup set, read-only after init
var refIdentityFields = map[string]bool{"Kind": true, "Node": true, "ID": true}

// EntitiesFromInterfaces parses one node's captured /etc/network/interfaces
// content into its comparable entities.
//
// It reuses inventory.FromInterfaces — the exact classifier the live graph is
// built with — rather than re-deriving "is this stanza a bridge or a bond"
// here. Two classifiers would eventually disagree, and the day they did, the
// diff would report a change that the map did not show.
func EntitiesFromInterfaces(node, content string) ([]PointEntity, error) {
	f, err := host.ParseInterfaces([]byte(content))
	if err != nil {
		return nil, fmt.Errorf("topology: parsing captured interfaces for node %s: %w", node, err)
	}
	ents := inventory.FromInterfaces(node, f)
	out := make([]PointEntity, 0, len(ents))
	for _, e := range ents {
		fields, ferr := entityFieldStrings(e)
		if ferr != nil {
			return nil, fmt.Errorf("topology: reading fields for %s: %w", e.GetRef(), ferr)
		}
		out = append(out, PointEntity{Ref: e.GetRef(), Name: fields["Name"], Fields: fields})
	}
	sortPointEntities(out)
	return out, nil
}

func sortPointEntities(ents []PointEntity) {
	sort.Slice(ents, func(i, j int) bool { return ents[i].Ref.String() < ents[j].Ref.String() })
}

// entityFieldStrings flattens an entity to its canonical, comparable field
// map.
//
// It goes through encoding/json rather than a hand-written per-kind field
// list, for the same reason topology.Detail's entityFields does: a hand-
// written list silently omits any field added to internal/inventory later,
// and a field the diff cannot see is a change the diff reports as not having
// happened. Completeness here is maintained by the compiler, not by memory.
func entityFieldStrings(e inventory.Entity) (map[string]string, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("topology: marshaling entity fields: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("topology: unmarshaling entity fields: %w", err)
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if refIdentityFields[k] {
			continue
		}
		if s := canonicalValue(v); s != "" {
			out[k] = s
		}
	}
	return out, nil
}

// canonicalValue renders one decoded JSON value as the single string the diff
// compares and displays. Deterministic for every input: object keys are
// sorted, arrays keep their (file-derived, hence stable) order.
func canonicalValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "" // false is indistinguishable from unset in a declared config
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, canonicalValue(e))
		}
		return strings.Join(parts, " ")
	case map[string]any:
		if s, ok := vidRangeString(t); ok {
			return s
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+canonicalValue(t[k]))
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprintf("%v", v)
	}
}

// vidRangeString renders an inventory.VidRange's decoded JSON form
// ({"Low":n,"High":m}) the way an operator writes it in bridge-vids ("2-100",
// or just "7" for a single VID) instead of as the generic "High=100,Low=2"
// object rendering. VID sets are the single most-read field of a diffed
// bridge; every other struct falls through to the generic path.
func vidRangeString(m map[string]any) (string, bool) {
	if len(m) != 2 {
		return "", false
	}
	lo, okLo := m["Low"].(float64)
	hi, okHi := m["High"].(float64)
	if !okLo || !okHi {
		return "", false
	}
	if lo == hi {
		return strconv.FormatInt(int64(lo), 10), true
	}
	return strconv.FormatInt(int64(lo), 10) + "-" + strconv.FormatInt(int64(hi), 10), true
}

// DiffPoints compares two points' entity sets and returns every difference,
// ordered by Ref string.
//
// Both inputs are taken as complete for whatever scope the caller established:
// an entity present in `from` and absent from `to` is reported removed, with
// no attempt to guess whether its node was captured. Scoping is the caller's
// responsibility precisely so that this function cannot silently invent an
// absence (see this file's doc comment).
//
// Diffing a point against itself returns an empty slice, never nil — the
// caller marshals it, and `[]` is the honest JSON for "compared, found
// nothing", where `null` reads as "did not compare".
func DiffPoints(from, to []PointEntity) []EntityDiff {
	fromByRef := make(map[inventory.Ref]PointEntity, len(from))
	for _, e := range from {
		fromByRef[e.Ref] = e
	}
	toByRef := make(map[inventory.Ref]PointEntity, len(to))
	for _, e := range to {
		toByRef[e.Ref] = e
	}

	out := []EntityDiff{}
	for _, e := range to {
		prev, existed := fromByRef[e.Ref]
		if !existed {
			out = append(out, newEntityDiff(e.Ref, e.Name, DiffAdded, fieldChanges(nil, e.Fields)))
			continue
		}
		if changes := fieldChanges(prev.Fields, e.Fields); len(changes) > 0 {
			out = append(out, newEntityDiff(e.Ref, e.Name, DiffModified, changes))
		}
	}
	for _, e := range from {
		if _, still := toByRef[e.Ref]; still {
			continue
		}
		out = append(out, newEntityDiff(e.Ref, e.Name, DiffRemoved, fieldChanges(e.Fields, nil)))
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func newEntityDiff(ref inventory.Ref, name string, change DiffChange, fields []FieldChange) EntityDiff {
	return EntityDiff{
		Ref:    ref.String(),
		Kind:   string(ref.Kind),
		Node:   ref.Node,
		Name:   name,
		Change: change,
		Fields: fields,
	}
}

// fieldChanges returns every field whose canonical value differs, sorted by
// field name. Added and removed entities go through the same function (with a
// nil on one side), so an added bridge carries the same per-field detail a
// modified one does rather than the bare word "added".
func fieldChanges(before, after map[string]string) []FieldChange {
	names := make([]string, 0, len(before)+len(after))
	seen := make(map[string]bool, len(before)+len(after))
	for _, m := range []map[string]string{before, after} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				names = append(names, k)
			}
		}
	}
	sort.Strings(names)

	out := make([]FieldChange, 0, len(names))
	for _, n := range names {
		b, a := before[n], after[n]
		if b == a {
			continue
		}
		out = append(out, FieldChange{Field: n, Before: b, After: a})
	}
	return out
}
