package topology

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Detail builds the GET /inventory/{ref} response for ref against snap: the
// resolved entity's fields, provenance, per-source raw source text, and
// related (edge-linked) entities. ok is false if ref does not resolve in
// this snapshot.
//
// "Fields" and "Provenance" deliberately use two different key
// vocabularies. Fields is the entity's own exported Go struct fields
// (flattened JSON of the concrete inventory type, e.g. *inventory.Bond) —
// complete and mechanically kept in sync with entity.go by construction,
// since it's driven by encoding/json reflection rather than a hand-maintained
// field list. Provenance is keyed by inventory's internal canonical field
// names (the same names Delta.ChangedFields and merge.go's ownership table
// use, e.g. "mtuDeclared", "slaveDetail") — those names aren't required to
// match Go field names 1:1 (fieldMap() is unexported precisely so
// internal/inventory can keep that mapping private), so a UI cross-
// referencing "which field does this provenance entry describe" should
// match case-insensitively / substring against Fields' keys rather than
// expect an exact join. Flagged here for T-107.
func Detail(snap inventory.Snapshot, ref inventory.Ref) (EntityDetail, bool) {
	e, ok := snap.Get(ref)
	if !ok {
		return EntityDetail{}, false
	}
	prov, _ := snap.Provenance(ref)

	provOut := map[string]FieldSource{}
	for field, fp := range prov.Fields {
		conflicts := make([]SourceValue, len(fp.Conflicts))
		for i, c := range fp.Conflicts {
			conflicts[i] = SourceValue{Source: string(c.Source), Value: c.Value}
		}
		provOut[field] = FieldSource{Owner: string(fp.Owner), Conflicts: conflicts}
	}

	fields, err := entityFields(e)
	if err != nil {
		fields = map[string]any{}
	}
	// T-306: a bridge's FDB table is otherwise just its raw Mac/Port/Vlan
	// tuples (entityFields' generic JSON-reflection pass has no way to know
	// about cross-entity ownership) — override the "FDB" key with the same
	// guest/vnprox-known/unknown-labeled rows FDB()/FDBSearch() return, so
	// the inspector's bridge detail (docs/features/lldp-discovery.md §4:
	// "inspector integration: bridge detail shows its FDB with owner
	// labels") needs no separate fetch.
	if br, ok := e.(*inventory.Bridge); ok && len(br.FDB) > 0 {
		fields["FDB"] = fdbRowsForBridge(br, buildMacIndex(snap))
	}

	// Raw source text per contributing source (docs/api.md's "including raw
	// source (interfaces stanza / PVE API object)"): the graph retains, per
	// (Ref, Source), the exact text each contribution was derived from — the
	// verbatim interfaces(5) stanza, the PVE object's pretty-printed JSON, or
	// compact JSON of observed netlink/LLDP state. nil (field omitted) when
	// no source attached any.
	var rawOut map[string]string
	if raw := snap.RawSource(ref); len(raw) > 0 {
		rawOut = make(map[string]string, len(raw))
		for src, txt := range raw {
			rawOut[string(src)] = txt
		}
	}

	related := []RelatedRef{}
	for _, edge := range snap.EdgesOf(ref) {
		switch {
		case edge.From == ref:
			related = append(related, RelatedRef{Ref: edge.To.String(), EdgeKind: string(edge.Kind), Direction: "to"})
		case edge.To == ref:
			related = append(related, RelatedRef{Ref: edge.From.String(), EdgeKind: string(edge.Kind), Direction: "from"})
		}
	}
	sort.Slice(related, func(i, j int) bool {
		if related[i].Ref != related[j].Ref {
			return related[i].Ref < related[j].Ref
		}
		return related[i].EdgeKind < related[j].EdgeKind
	})

	return EntityDetail{
		Ref:         ref.String(),
		Kind:        string(ref.Kind),
		Node:        ref.Node,
		Label:       labelOf(snap, e),
		Fields:      fields,
		Provenance:  provOut,
		RawSource:   rawOut,
		Related:     related,
		GeneratedAt: snap.GeneratedAt().Unix(),
	}, true
}

// entityFields flattens e's exported fields to a JSON-ish map via a
// marshal/unmarshal round trip, rather than hand-listing each of the ~14
// concrete entity types' fields here (which would silently drift out of
// sync with entity.go as new fields are added).
func entityFields(e inventory.Entity) (map[string]any, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("topology: marshaling entity fields: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("topology: unmarshaling entity fields: %w", err)
	}
	return out, nil
}
