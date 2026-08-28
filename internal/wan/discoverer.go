// SPDX-License-Identifier: Apache-2.0

package wan

import (
	"context"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/latmesh"
	"github.com/bgovanlu/vnprox/internal/store"
)

// TargetStore is the subset of *store.WanTargetRepo TargetDiscoverer and
// Service need — declared as an interface (the same "small interface, real
// type satisfies it for free" seam every other Discoverer/store dependency
// in this codebase uses) so tests substitute an in-memory fake without a
// real SQLite file.
type TargetStore interface {
	ListByNode(ctx context.Context, node string) ([]store.WanTarget, error)
	ReplaceForNode(ctx context.Context, node string, targets []store.WanTarget, now int64) error
}

// TargetDiscoverer implements internal/latmesh.Discoverer over this node's
// own configured WAN reference targets — the WAN-probing equivalent of
// latmesh.GraphDiscoverer, re-reading the store fresh on every Pairs() call
// (the same "cheap, local, always current" convention GraphDiscoverer's own
// doc comment establishes for corosync.conf) so a target added via
// PUT /wan/targets is picked up on the very next probe tick with no daemon
// restart.
type TargetDiscoverer struct {
	Store     TargetStore
	LocalNode func() string
	Logger    *slog.Logger
}

// Pairs implements latmesh.Discoverer: one Pair per configured
// (uplink, host) target on this node, Fabric=Fabric ("wan"), Label=uplink
// (so LinkID/ComputeLinkID encodes the uplink the exact same way
// internal/latmesh already encodes a corosync ring/shared-bridge label —
// see store.wanUplinkFromLinkID for the inverse), ToAddr=host (RealProber
// pings ToAddr, falling back to ToNode by name when ToAddr is empty — never
// the case here, Host is always non-empty for a stored target).
func (d *TargetDiscoverer) Pairs() []latmesh.Pair {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if d.Store == nil || d.LocalNode == nil {
		return nil
	}
	node := d.LocalNode()
	if node == "" {
		return nil
	}

	targets, err := d.Store.ListByNode(context.Background(), node)
	if err != nil {
		logger.Warn("wan: reading configured targets, skipping this tick's pairs", "node", node, "error", err)
		return nil
	}

	pairs := make([]latmesh.Pair, 0, len(targets))
	for _, t := range targets {
		pairs = append(pairs, latmesh.Pair{
			LinkID:   latmesh.ComputeLinkID(Fabric, t.Uplink, node, t.Host),
			Label:    t.Uplink,
			Fabric:   Fabric,
			FromNode: node,
			ToNode:   t.Host,
			ToAddr:   t.Host,
		})
	}
	return pairs
}
