// Package neighbor implements T-805's ARP/neighbor-table fan-out: a
// cluster-wide ipam.NeighborSource that reads every node's resolved ARP
// (IPv4) / IPv6-neighbor table (locally via internal/host.Reader, and for
// every other cluster node via the peer API — docs/architecture.md §5's
// peer fan-out convention), and exposes it as confidence-labeled
// ipam.Observation values, wired into internal/ipam's enrichment merge at
// exactly the interface point T-405 left open for it
// (ipam.Config.Neighbors).
//
// This mirrors internal/dhcp's local-vs-peer fan-out shape
// (nodeLeaseReader/peerLeaseReader) exactly — see that package's doc
// comment — applied here to the same kind of stateless per-node read (no
// flap-tracking history to accumulate across calls).
package neighbor

import (
	"context"
	"log/slog"
	"sort"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// nodeNeighborReader is the read surface Service needs for one node:
// satisfied directly by host.Reader for the local node, and by
// peerNeighborReader (below) for every other cluster node.
type nodeNeighborReader interface {
	Neighbors(ctx context.Context, node string) ([]host.Neighbor, error)
}

// PeerSource is the fan-out dependency: peer discovery plus the neighbor
// read, routed through the peer API — *peer.Client satisfies this directly
// (the same seam shape internal/dhcp.PeerSource uses).
type PeerSource interface {
	Peers(ctx context.Context) ([]peer.Peer, error)
	Neighbors(ctx context.Context, p peer.Peer, node string) ([]host.Neighbor, error)
}

// peerNeighborReader adapts a PeerSource + a specific peer.Peer into a
// nodeNeighborReader.
type peerNeighborReader struct {
	source PeerSource
	peer   peer.Peer
}

func (r peerNeighborReader) Neighbors(ctx context.Context, node string) ([]host.Neighbor, error) {
	return r.source.Neighbors(ctx, r.peer, node)
}

// Config configures a Service.
type Config struct {
	// Host is the local node's neighbor-table reader (in production,
	// host.NewReal() — the same instance internal/peer.ServerOptions.Reader
	// already uses).
	Host nodeNeighborReader
	// Peers is the cluster fan-out dependency (in production,
	// *peer.Client). Nil is the documented single-node "zero peers" case.
	Peers PeerSource
	// LocalNode returns this daemon's own PVE node name, or "" before the
	// PVE poller has discovered it yet (mirrors internal/dhcp.Config's same
	// field).
	LocalNode func() string
	Logger    *slog.Logger
}

// Service aggregates cluster-wide ARP/neighbor-table data into
// ipam.Observation values, implementing ipam.NeighborSource.
type Service struct {
	cfg Config
	log *slog.Logger
}

// NewService builds a Service. cfg.Host must be non-nil; cfg.Peers may be
// nil (single-node/degraded mode).
func NewService(cfg Config) *Service {
	if cfg.LocalNode == nil {
		cfg.LocalNode = func() string { return "" }
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Service{cfg: cfg, log: log}
}

var _ ipam.NeighborSource = (*Service)(nil)

// Neighbors implements ipam.NeighborSource: this daemon's own node plus
// every currently-known peer's resolved ARP/IPv6-neighbor table, each
// fetched independently so one unreachable node never blanks every other
// node's neighbors — the same tolerate-individual-failures posture
// internal/dhcp.Service.Leases already establishes (docs/features/ipam.md
// §1: "never authoritative", matching guest-agent's own
// confidence-labeling contract exactly).
func (s *Service) Neighbors(ctx context.Context) ([]ipam.Observation, error) {
	local := s.cfg.LocalNode()

	var nodeNames []string
	readers := map[string]nodeNeighborReader{}

	if local != "" && s.cfg.Host != nil {
		nodeNames = append(nodeNames, local)
		readers[local] = s.cfg.Host
	}
	if s.cfg.Peers != nil {
		peers, err := s.cfg.Peers.Peers(ctx)
		if err != nil {
			s.log.Debug("neighbor: peer discovery failed, reporting only the local node's neighbors", "error", err)
		} else {
			for _, p := range peers {
				if p.Node == local {
					continue
				}
				nodeNames = append(nodeNames, p.Node)
				readers[p.Node] = peerNeighborReader{source: s.cfg.Peers, peer: p}
			}
		}
	}
	sort.Strings(nodeNames)

	var out []ipam.Observation
	for _, node := range nodeNames {
		neighbors, err := readers[node].Neighbors(ctx, node)
		if err != nil {
			s.log.Debug("neighbor: reading neighbor table failed, skipping this node", "node", node, "error", err)
			continue
		}
		for _, n := range neighbors {
			if n.IP == "" {
				continue
			}
			out = append(out, ipam.Observation{IP: n.IP, MAC: n.MAC, Source: "neighbor"})
		}
	}
	return out, nil
}
