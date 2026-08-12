// project.go builds a Snapshot over an explicit, caller-supplied entity set
// instead of over collector polls — the seam T-2605's post-apply topology
// preview needs.
//
// WHY THIS EXISTS RATHER THAN A SECOND ApplyPoll. A projection is not an
// observation. ApplyPoll runs the per-source ownership merge (merge.go), which
// resolves each field from whichever source is entitled to it; feeding an
// already-resolved entity back in under one arbitrary source would silently
// drop every field that source does not own — a bridge's runtime MTU, or its
// declared addresses, depending which source you picked. A preview whose
// entities quietly lost half their fields would be worse than no preview, so
// projection bypasses the merge entirely: the caller hands over resolved
// entities, and they pass through unchanged.
//
// Read-only by construction. Entities are cloned on the way in, so the caller
// may hold live-snapshot entities and the projection can never write through
// to them — linkAll, which resolves derived Ref fields IN PLACE, would
// otherwise mutate the live graph.

package inventory

// ProjectSnapshot returns an in-memory Snapshot over ents, an already-resolved
// entity set (typically a live snapshot's entities with some in-memory change
// folded in). Derived Ref fields — bridge Ports, VLAN Parent, guest-NIC
// BridgeOrVnet/EffectiveVid, LLDP LocalNic — and the typed edge list are
// recomputed by the same linkAll the live graph uses, so a projection and the
// live graph can never disagree about how names resolve to entities.
//
// Provenance and raw source text are carried over from base for exactly the
// refs in carry, and are absent for every other ref. carry is meant to name the
// entities the projection left untouched: nothing has observed an entity a
// projection invented or edited, so claiming a collector for it — or handing
// back the interfaces(5) stanza of a bridge whose address the projection just
// changed — would be a fabrication in precisely the place a preview must not
// fabricate. (Provenance is not decoration: topology.Project reads it to decide
// whether a bond's slave list is "known but degraded" or simply "not observed
// yet".) A nil carry carries nothing.
//
// GeneratedAt and Seq are inherited from base: a projection describes the same
// instant of observation the snapshot it was folded from describes, with a
// hypothetical change applied — it is not a newer reading of the cluster.
func ProjectSnapshot(base Snapshot, ents []Entity, carry map[Ref]bool) Snapshot {
	entities := make(map[Ref]Entity, len(ents))
	prov := make(map[Ref]Provenance, len(ents))
	raw := make(map[Ref]map[Source]string, len(ents))
	for _, e := range ents {
		if e == nil {
			continue
		}
		ref := e.GetRef()
		entities[ref] = e.clone()
		if !carry[ref] || base.s == nil {
			continue
		}
		if p, ok := base.Provenance(ref); ok {
			prov[ref] = p
		}
		if m := base.RawSource(ref); len(m) > 0 {
			raw[ref] = m
		}
	}

	edges := linkAll(entities)
	byRef := make(map[Ref][]Edge, len(edges))
	for _, e := range edges {
		byRef[e.From] = append(byRef[e.From], e)
		byRef[e.To] = append(byRef[e.To], e)
	}

	st := &state{
		entities:   entities,
		prov:       prov,
		raw:        raw,
		edges:      edges,
		edgesByRef: byRef,
	}
	if base.s != nil {
		st.generatedAt = base.s.generatedAt
		st.seq = base.s.seq
	}
	return Snapshot{s: st}
}
