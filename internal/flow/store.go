package flow

import (
	"context"

	"github.com/bgovanlu/vnprox/internal/store"
)

// Filter is store.FlowFilter, re-exported so callers of this package's
// Service (internal/api/flows.go) can spell GET /flows' query-filter type
// as flow.Filter without importing internal/store themselves just for this
// one type.
type Filter = store.FlowFilter

// FlowStore is the subset of *store.FlowSampleRepo Service needs — declared
// as an interface so tests can substitute an in-memory fake without a real
// SQLite file, the same seam internal/metrics.MetricStore establishes for
// *store.MetricSampleRepo.
type FlowStore interface {
	InsertBatch(ctx context.Context, samples []store.FlowSample) error
	Query(ctx context.Context, filter store.FlowFilter, cursor string, limit int) ([]store.FlowSample, string, error)
	PruneOlderThan(ctx context.Context, cutoff int64) (int64, error)
	PruneToCap(ctx context.Context, maxRows int64) (int64, error)
}

// toStoreSample/fromStoreSample convert between this package's Record and
// store.FlowSample's row shape — see store.FlowSample's doc comment for why
// internal/store keeps its own copy of the field set rather than importing
// this package's Record type directly (layering: internal/store sits below
// the packages built on it, docs/architecture.md §2).
func toStoreSample(r Record) store.FlowSample {
	return store.FlowSample{
		At: r.At, Node: r.Node, SrcIP: r.SrcIP, DstIP: r.DstIP,
		SrcPort: r.SrcPort, DstPort: r.DstPort, Proto: r.Proto,
		Bytes: r.Bytes, Packets: r.Packets, VLAN: r.VLAN,
		SrcRef: r.SrcRef, DstRef: r.DstRef,
		IngressIf: r.IngressIfIndex, EgressIf: r.EgressIfIndex,
		Source: string(r.Source),
	}
}

func fromStoreSample(s store.FlowSample) Record {
	return Record{
		At: s.At, Node: s.Node, SrcIP: s.SrcIP, DstIP: s.DstIP,
		SrcPort: s.SrcPort, DstPort: s.DstPort, Proto: s.Proto,
		Bytes: s.Bytes, Packets: s.Packets, VLAN: s.VLAN,
		SrcRef: s.SrcRef, DstRef: s.DstRef,
		IngressIfIndex: s.IngressIf, EgressIfIndex: s.EgressIf,
		Source: Source(s.Source),
	}
}
