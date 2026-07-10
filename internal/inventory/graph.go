package inventory

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// state is one immutable published snapshot of the whole graph. Once stored
// in Graph.cur it is never mutated; readers share it lock-free. All slices
// and maps it references are treated as read-only after publication.
type state struct {
	generatedAt time.Time
	entities    map[Ref]Entity
	prov        map[Ref]Provenance
	raw         map[Ref]map[Source]string
	edgesByRef  map[Ref][]Edge
	edges       []Edge
	seq         uint64
}

func emptyState() *state {
	return &state{
		entities:    map[Ref]Entity{},
		prov:        map[Ref]Provenance{},
		raw:         map[Ref]map[Source]string{},
		edgesByRef:  map[Ref][]Edge{},
		generatedAt: time.Now(),
	}
}

// Graph is the normalized in-memory network model: a multi-source,
// reconciled, immutable-snapshot graph of entities and typed edges
// (docs/data-model.md §1).
//
// Concurrency model (see the T-103 report for the rationale):
//   - Writers (ApplyPoll) serialize on mu and publish a fresh immutable
//     state via an atomic pointer (copy-on-write).
//   - Readers (Snapshot) do a single atomic load and then read shared
//     immutable data with no locks and no coordination.
//
// This makes Snapshot O(1) and race-free regardless of writer activity.
type Graph struct {
	cur     atomic.Pointer[state]
	contrib map[Ref]map[Source]Entity
	seq     uint64
	mu      sync.Mutex
}

// NewGraph returns an empty graph with an initialized empty snapshot.
func NewGraph() *Graph {
	g := &Graph{contrib: map[Ref]map[Source]Entity{}}
	g.cur.Store(emptyState())
	return g
}

// Scope bounds what a poll authoritatively enumerated, so that entities a
// source previously reported but omitted this time are correctly removed —
// without a node-scoped host poll wiping cluster-scoped SDN entities, or a
// poll of node A removing node B's entities.
//
// Node bounds removal to one node's entities ("" means the poll covers
// cluster-scoped entities, i.e. Refs with empty Node). Kinds, when
// non-empty, further restricts removal reconciliation to those kinds — use
// it when one source emits several kinds in separate polls (e.g. a firewall
// poll that only enumerates fw-rulesets should not retire guests).
type Scope struct {
	Node  string
	Kinds []Kind
}

// matches reports whether ref falls within this scope, i.e. whether the
// poll's removal reconciliation is entitled to retire it.
func (s Scope) matches(ref Ref) bool {
	if ref.Node != s.Node {
		return false
	}
	if len(s.Kinds) == 0 {
		return true
	}
	for _, k := range s.Kinds {
		if ref.Kind == k {
			return true
		}
	}
	return false
}

// ApplyPoll ingests one collector poll: entities observed by source within
// scope. It reconciles them against prior contributions from the same
// source (upserting present entities, removing scope-matching entities the
// poll omitted), re-resolves the affected model, publishes a new immutable
// snapshot, and returns the delta versus the previous snapshot.
//
// Entities are deep-copied on ingestion, so the caller may reuse or mutate
// the passed slice afterward without affecting the graph.
func (g *Graph) ApplyPoll(source Source, scope Scope, entities []Entity) Delta {
	g.mu.Lock()
	defer g.mu.Unlock()

	incoming := make(map[Ref]bool, len(entities))
	for _, e := range entities {
		ref := e.GetRef()
		incoming[ref] = true
		parts := g.contrib[ref]
		if parts == nil {
			parts = map[Source]Entity{}
			g.contrib[ref] = parts
		}
		parts[source] = e.clone()
	}

	// Removal: drop this source's contribution for scope-matching refs it
	// previously reported but did not report this poll.
	for ref, parts := range g.contrib {
		if !incoming[ref] && scope.matches(ref) {
			if _, had := parts[source]; had {
				delete(parts, source)
				if len(parts) == 0 {
					delete(g.contrib, ref)
				}
			}
		}
	}

	g.seq++
	next := buildState(g.contrib, g.seq)
	prev := g.cur.Load()
	delta := diffStates(prev, next)
	g.cur.Store(next)
	return delta
}

// buildState resolves every contributed Ref, links derived Ref fields, and
// computes edges into a fresh immutable state.
func buildState(contrib map[Ref]map[Source]Entity, seq uint64) *state {
	ents := make(map[Ref]Entity, len(contrib))
	prov := make(map[Ref]Provenance, len(contrib))
	raw := make(map[Ref]map[Source]string, len(contrib))
	for ref, parts := range contrib {
		r := resolveEntity(ref, parts)
		if r.entity == nil {
			continue
		}
		ents[ref] = r.entity
		prov[ref] = r.prov
		// Retain each source's raw source text (interfaces stanza / PVE
		// JSON / netlink rendering). Strings are immutable and shared with
		// the contribution, so this costs one small map per multi-observed
		// Ref, not a copy of the text.
		for src, part := range parts {
			if rs := part.rawSource(); rs != "" {
				m := raw[ref]
				if m == nil {
					m = make(map[Source]string, len(parts))
					raw[ref] = m
				}
				m[src] = rs
			}
		}
	}
	edges := linkAll(ents)
	byRef := make(map[Ref][]Edge, len(edges))
	for _, e := range edges {
		byRef[e.From] = append(byRef[e.From], e)
		byRef[e.To] = append(byRef[e.To], e)
	}
	return &state{
		entities:    ents,
		prov:        prov,
		raw:         raw,
		edges:       edges,
		edgesByRef:  byRef,
		generatedAt: time.Now(),
		seq:         seq,
	}
}

// --- deltas --------------------------------------------------------------

// Delta reports the difference between two consecutive snapshots. Added,
// Updated, and Removed are the documented Ref lists (docs/data-model.md §1);
// ChangedFields additionally gives, per updated Ref (keyed by Ref.String),
// the exact set of resolved field names that changed.
type Delta struct {
	ChangedFields map[string][]string
	Added         []Ref
	Updated       []Ref
	Removed       []Ref
}

// Empty reports whether the delta carries no changes.
func (d Delta) Empty() bool {
	return len(d.Added) == 0 && len(d.Updated) == 0 && len(d.Removed) == 0
}

func diffStates(prev, next *state) Delta {
	d := Delta{ChangedFields: map[string][]string{}}
	for ref, ne := range next.entities {
		oe, ok := prev.entities[ref]
		if !ok {
			d.Added = append(d.Added, ref)
			continue
		}
		if changed := changedFields(oe, ne); len(changed) > 0 {
			d.Updated = append(d.Updated, ref)
			d.ChangedFields[ref.String()] = changed
		}
	}
	for ref := range prev.entities {
		if _, ok := next.entities[ref]; !ok {
			d.Removed = append(d.Removed, ref)
		}
	}
	sortRefs(d.Added)
	sortRefs(d.Updated)
	sortRefs(d.Removed)
	return d
}

// changedFields returns the sorted set of field names whose canonical value
// differs between two resolved entities of the same Ref.
func changedFields(oldE, newE Entity) []string {
	om, nm := oldE.fieldMap(), newE.fieldMap()
	var changed []string
	seen := map[string]bool{}
	for k, ov := range om {
		seen[k] = true
		if nm[k] != ov {
			changed = append(changed, k)
		}
	}
	for k, nv := range nm {
		if !seen[k] && nv != "" {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	return changed
}

func sortRefs(rs []Ref) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].String() < rs[j].String() })
}

// --- snapshots -----------------------------------------------------------

// Snapshot is an immutable, cheap read view of the graph at one instant.
// Obtaining one is a single atomic load; the underlying data is shared and
// never mutated, so a Snapshot is safe to hold and read from any goroutine
// concurrently with ongoing ApplyPoll writes.
type Snapshot struct{ s *state }

// Snapshot returns the current immutable graph view. O(1); safe under
// concurrent writers.
func (g *Graph) Snapshot() Snapshot { return Snapshot{s: g.cur.Load()} }

// Len is the number of resolved entities.
func (s Snapshot) Len() int { return len(s.s.entities) }

// Get returns the resolved entity for ref, if present.
func (s Snapshot) Get(ref Ref) (Entity, bool) {
	e, ok := s.s.entities[ref]
	return e, ok
}

// Provenance returns the provenance of the entity at ref.
func (s Snapshot) Provenance(ref Ref) (Provenance, bool) {
	p, ok := s.s.prov[ref]
	return p, ok
}

// RawSource returns, per contributing Source, the raw source text the
// entity at ref was derived from (docs/api.md: GET /inventory/{ref}
// includes "raw source (interfaces stanza / PVE API object)"):
//
//   - SourceHostInterfaces: the entity's interfaces(5) stanza text,
//     byte-identical to the file (concatenation of every "iface <name> ..."
//     stanza for that interface, via the lossless AST).
//   - SourcePVE* sources: pretty-printed JSON of the PVE API object the
//     entity came from.
//   - SourceHostNetlink / SourceHostLLDP: a compact JSON rendering of the
//     observed link state / neighbor row.
//
// Sources that attached no raw text are absent from the map; nil is
// returned when no source attached any. The returned map is a fresh copy
// the caller may mutate; the strings themselves are shared with the
// immutable snapshot.
func (s Snapshot) RawSource(ref Ref) map[Source]string {
	m := s.s.raw[ref]
	if len(m) == 0 {
		return nil
	}
	out := make(map[Source]string, len(m))
	for src, txt := range m {
		out[src] = txt
	}
	return out
}

// All returns every resolved entity, sorted by Ref for determinism.
func (s Snapshot) All() []Entity {
	out := make([]Entity, 0, len(s.s.entities))
	for _, e := range s.s.entities {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetRef().String() < out[j].GetRef().String() })
	return out
}

// Edges returns every typed edge, in a deterministic order.
func (s Snapshot) Edges() []Edge { return s.s.edges }

// EdgesOf returns all edges incident to ref (as either endpoint).
func (s Snapshot) EdgesOf(ref Ref) []Edge { return s.s.edgesByRef[ref] }

// GeneratedAt is when this snapshot's state was built.
func (s Snapshot) GeneratedAt() time.Time { return s.s.generatedAt }

// Seq is the monotonic publish counter of this snapshot.
func (s Snapshot) Seq() uint64 { return s.s.seq }
