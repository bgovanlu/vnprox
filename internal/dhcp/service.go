// SPDX-License-Identifier: Apache-2.0

// Package dhcp implements T-406's dnsmasq lease-file reader fan-out: a
// cluster-wide ipam.LeaseSource that reads every node's raw DHCP
// lease-file content (locally via internal/host.Reader, and for every
// other cluster node via the peer API — docs/features/sdn.md §5: "a live
// leases view (parsed per-node via peer API)"), defensively parses it
// (internal/host.ParseDHCPLeases), and exposes it as confidence-labeled
// ipam.Observation values, wired into T-405's IPAM enrichment merge at
// exactly the interface point that task left open for this one
// (ipam.Config.Leases).
//
// This mirrors internal/evpn's local-vs-peer fan-out shape
// (nodeFRRReader/peerFRRReader) exactly — see that package's doc comment
// — applied here to a much simpler, stateless read (no flap-tracking
// history to accumulate across calls).
package dhcp

import (
	"context"
	"log/slog"
	"sort"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// nodeLeaseReader is the read surface Service needs for one node:
// satisfied directly by host.Reader for the local node, and by
// peerLeaseReader (below) for every other cluster node.
type nodeLeaseReader interface {
	DHCPLeases(ctx context.Context, node string) ([]byte, error)
}

// PeerSource is the fan-out dependency: peer discovery plus the DHCP
// leases read, routed through the peer API — *peer.Client satisfies this
// directly (the same seam shape internal/evpn.PeerSource uses).
type PeerSource interface {
	Peers(ctx context.Context) ([]peer.Peer, error)
	DHCPLeases(ctx context.Context, p peer.Peer, node string) ([]byte, error)
}

// peerLeaseReader adapts a PeerSource + a specific peer.Peer into a
// nodeLeaseReader.
type peerLeaseReader struct {
	source PeerSource
	peer   peer.Peer
}

func (r peerLeaseReader) DHCPLeases(ctx context.Context, node string) ([]byte, error) {
	return r.source.DHCPLeases(ctx, r.peer, node)
}

// Config configures a Service.
type Config struct {
	// Host is the local node's DHCP-lease reader (in production,
	// host.NewReal() — the same instance internal/peer.ServerOptions.Reader
	// already uses).
	Host nodeLeaseReader
	// Peers is the cluster fan-out dependency (in production,
	// *peer.Client). Nil is the documented single-node "zero peers" case.
	Peers PeerSource
	// LocalNode returns this daemon's own PVE node name, or "" before the
	// PVE poller has discovered it yet (mirrors internal/evpn.Config's
	// same field).
	LocalNode func() string
	Logger    *slog.Logger
}

// Service aggregates cluster-wide DHCP lease data into ipam.Observation
// values, implementing ipam.LeaseSource.
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

var _ ipam.LeaseSource = (*Service)(nil)

// Leases implements ipam.LeaseSource: this daemon's own node plus every
// currently-known peer's raw DHCP lease-file content, each fetched and
// parsed independently so one unreachable node never blanks every other
// node's leases (the same tolerate-individual-failures posture
// internal/evpn.Service.Status and GET /audit/GET /snapshots' cluster
// fan-out already establish). A lease with no IP (defensively parsed but
// meaningless without one — ParseDHCPLeases never actually produces this,
// but the invariant is enforced here rather than assumed) is skipped.
func (s *Service) Leases(ctx context.Context) ([]ipam.Observation, error) {
	local := s.cfg.LocalNode()

	var nodeNames []string
	readers := map[string]nodeLeaseReader{}

	if local != "" && s.cfg.Host != nil {
		nodeNames = append(nodeNames, local)
		readers[local] = s.cfg.Host
	}
	if s.cfg.Peers != nil {
		peers, err := s.cfg.Peers.Peers(ctx)
		if err != nil {
			s.log.Debug("dhcp: peer discovery failed, reporting only the local node's leases", "error", err)
		} else {
			for _, p := range peers {
				if p.Node == local {
					continue
				}
				nodeNames = append(nodeNames, p.Node)
				readers[p.Node] = peerLeaseReader{source: s.cfg.Peers, peer: p}
			}
		}
	}
	sort.Strings(nodeNames)

	var out []ipam.Observation
	for _, node := range nodeNames {
		raw, err := readers[node].DHCPLeases(ctx, node)
		if err != nil {
			s.log.Debug("dhcp: reading leases failed, skipping this node", "node", node, "error", err)
			continue
		}
		leases, skipped := host.ParseDHCPLeases(raw)
		if skipped > 0 {
			s.log.Debug("dhcp: some lease lines were malformed and skipped", "node", node, "skipped", skipped)
		}
		for _, l := range leases {
			if l.IP == "" {
				continue
			}
			out = append(out, ipam.Observation{
				IP: l.IP, MAC: l.MAC, Hostname: l.Hostname, Source: "dhcp-lease",
			})
		}
	}
	return out, nil
}
