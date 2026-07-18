package latmesh

import (
	"context"

	"github.com/bgovanlu/vnprox/internal/store"
)

// Ring is the subset of *store.LatencySampleRepo Service needs — declared
// as an interface (the same "small interface, real type satisfies it for
// free" seam internal/flow.FlowStore establishes over *store.
// FlowSampleRepo) so tests can substitute an in-memory fake without a real
// SQLite file.
type Ring interface {
	InsertBatch(ctx context.Context, samples []store.LatencySample) error
	QueryRange(ctx context.Context, linkID string, fromTs, toTs int64) ([]store.LatencySample, error)
	LatestPerLink(ctx context.Context) ([]store.LatencySample, error)
	PruneOlderThan(ctx context.Context, cutoff int64) (int64, error)
	PruneToCap(ctx context.Context, maxRows int64) (int64, error)
}

func toStoreSample(s Sample) store.LatencySample {
	return store.LatencySample{
		LinkID: s.LinkID, Fabric: string(s.Fabric), FromNode: s.FromNode, ToNode: s.ToNode,
		At: s.At, RttMs: s.RttMs, LossPct: s.LossPct,
	}
}

func toStoreSamples(ss []Sample) []store.LatencySample {
	out := make([]store.LatencySample, len(ss))
	for i, s := range ss {
		out[i] = toStoreSample(s)
	}
	return out
}

func fromStoreSample(s store.LatencySample) Sample {
	return Sample{
		LinkID: s.LinkID, Fabric: Fabric(s.Fabric), FromNode: s.FromNode, ToNode: s.ToNode,
		At: s.At, RttMs: s.RttMs, LossPct: s.LossPct,
	}
}
