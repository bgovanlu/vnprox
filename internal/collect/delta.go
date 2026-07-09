package collect

import (
	"reflect"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// diffSnapshots computes the exact Added/Updated/Removed Ref sets between
// two Graph snapshots by comparing full entity sets directly — not by
// composing the Delta values individual Graph.ApplyPoll calls returned in
// between. This is what lets a whole poll cycle (which internally issues
// several ApplyPoll calls, one per PVE/host source) be reported to callers
// as exactly one merged Delta batch (deliverable 3's acceptance criterion:
// RefreshNow "triggers exactly one delta batch").
//
// It only uses inventory's exported Snapshot API (All, GetRef), so it has
// no dependency on inventory's internal merge/diff machinery. ChangedFields
// is left empty: computing exact per-field diffs would require inventory's
// unexported fieldMap; Added/Updated/Removed membership (which this
// package's consumers and tests actually need) does not.
func diffSnapshots(prev, next inventory.Snapshot) inventory.Delta {
	prevByRef := make(map[inventory.Ref]inventory.Entity, prev.Len())
	for _, e := range prev.All() {
		prevByRef[e.GetRef()] = e
	}
	nextByRef := make(map[inventory.Ref]inventory.Entity, next.Len())
	for _, e := range next.All() {
		nextByRef[e.GetRef()] = e
	}

	d := inventory.Delta{ChangedFields: map[string][]string{}}
	for ref, ne := range nextByRef {
		oe, existed := prevByRef[ref]
		switch {
		case !existed:
			d.Added = append(d.Added, ref)
		case !reflect.DeepEqual(oe, ne):
			d.Updated = append(d.Updated, ref)
		}
	}
	for ref := range prevByRef {
		if _, stillPresent := nextByRef[ref]; !stillPresent {
			d.Removed = append(d.Removed, ref)
		}
	}

	sortRefs(d.Added)
	sortRefs(d.Updated)
	sortRefs(d.Removed)
	return d
}

func sortRefs(rs []inventory.Ref) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].String() < rs[j].String() })
}

// emitDelta logs a non-empty delta and forwards it to onDelta, if set.
func (c *Collector) emitDelta(source string, delta inventory.Delta) {
	if delta.Empty() {
		return
	}
	c.log.Debug("collect: applied delta",
		"source", source,
		"added", len(delta.Added),
		"updated", len(delta.Updated),
		"removed", len(delta.Removed),
	)
	if c.onDelta != nil {
		c.onDelta(delta)
	}
}
