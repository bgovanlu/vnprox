// SPDX-License-Identifier: Apache-2.0

// Package ipv6 implements T-1404's IPv6 enablement suite's read side:
// GET /ipv6/segments aggregates every cluster node's bounded, host-local
// IPv6 Router Advertisement / DHCPv6 observation (internal/host.Reader.
// IPv6RA) into a per-VLAN/VNet view, fanned out across the cluster through
// the peer API exactly like internal/evpn does for FRR/BGP state (see
// nodeRAReader/peerRAReader below, the same local-vs-peer adapter shape).
//
// Unlike internal/evpn, this package holds no history/flap state — an RA
// observation is read fresh on every request (mirroring GET /latmesh/
// heatmap's own current-snapshot query style, not a rolling baseline).
package ipv6

import (
	"context"
	"sort"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// nodeRAReader is the read surface Service needs for one node: satisfied
// directly by host.Reader for the local node, and by peerRAReader (below)
// for every other cluster node.
type nodeRAReader interface {
	IPv6RA(ctx context.Context, node string) ([]host.IPv6RAObservation, error)
}

// PeerSource is the fan-out dependency: peer discovery plus the IPv6RA
// peer read — *peer.Client satisfies this directly.
type PeerSource interface {
	Peers(ctx context.Context) ([]peer.Peer, error)
	IPv6RA(ctx context.Context, p peer.Peer, node string) ([]host.IPv6RAObservation, error)
}

type peerRAReader struct {
	source PeerSource
	peer   peer.Peer
}

func (r peerRAReader) IPv6RA(ctx context.Context, node string) ([]host.IPv6RAObservation, error) {
	return r.source.IPv6RA(ctx, r.peer, node)
}

// Graph is the inventory read surface Service needs to correlate a raw
// per-interface RA observation to a known Bridge/SdnVnet (Ref/Vid/Vnet/
// Zone) — the live *inventory.Graph satisfies this directly.
type Graph interface {
	Snapshot() inventory.Snapshot
}

// Config configures a Service.
type Config struct {
	// Host is the local node's IPv6 RA reader (in production,
	// host.NewReal() — the same instance internal/peer.ServerOptions.
	// Reader already uses).
	Host nodeRAReader
	// Peers is the cluster fan-out dependency (in production,
	// *peer.Client). Nil is the documented single-node "zero peers" case.
	Peers PeerSource
	// LocalNode returns this daemon's own PVE node name, or "" before the
	// PVE poller has discovered it yet.
	LocalNode func() string
	Graph     Graph
	Now       func() time.Time
}

// Service aggregates cluster-wide IPv6 RA/DHCPv6 state for
// GET /ipv6/segments.
type Service struct {
	cfg Config
}

// NewService builds a Service. cfg.Host and cfg.Graph must be non-nil;
// cfg.Peers may be nil (single-node degraded mode).
func NewService(cfg Config) *Service {
	if cfg.LocalNode == nil {
		cfg.LocalNode = func() string { return "" }
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{cfg: cfg}
}

// Segments builds docs/api.md's GET /ipv6/segments response: this
// daemon's own node plus every currently-known peer's RA observations,
// each fetched independently so one unreachable node never blanks the
// rest of the view (the same tolerate-individual-failures posture
// GET /sdn/evpn/status's fan-out establishes).
func (s *Service) Segments(ctx context.Context) (SegmentsResponse, error) {
	now := s.cfg.Now()
	local := s.cfg.LocalNode()

	var nodeNames []string
	readers := map[string]nodeRAReader{}
	var discoveryFailed bool

	if local != "" {
		nodeNames = append(nodeNames, local)
		readers[local] = s.cfg.Host
	}
	if s.cfg.Peers != nil {
		peers, err := s.cfg.Peers.Peers(ctx)
		if err != nil {
			discoveryFailed = true
		} else {
			for _, p := range peers {
				if p.Node == local {
					continue
				}
				nodeNames = append(nodeNames, p.Node)
				readers[p.Node] = peerRAReader{source: s.cfg.Peers, peer: p}
			}
		}
	}
	sort.Strings(nodeNames)

	idx := ifaceIndex{
		bridges: map[string]map[string]*inventory.Bridge{},
		vnets:   map[string]*inventory.SdnVnet{},
		zones:   map[string]*inventory.SdnZone{},
	}
	if s.cfg.Graph != nil {
		idx = buildIfaceIndex(s.cfg.Graph.Snapshot())
	}

	resp := SegmentsResponse{GeneratedAt: now.Unix()}
	if discoveryFailed {
		resp.Partial = true
		resp.FailedNodes = append(resp.FailedNodes, "<cluster peer discovery>")
	}

	for _, node := range nodeNames {
		reader := readers[node]
		if reader == nil {
			resp.Partial = true
			resp.FailedNodes = append(resp.FailedNodes, node)
			continue
		}
		obs, err := reader.IPv6RA(ctx, node)
		if err != nil {
			resp.Partial = true
			resp.FailedNodes = append(resp.FailedNodes, node)
			continue
		}
		for _, o := range obs {
			resp.Items = append(resp.Items, buildSegment(node, o, idx))
		}
	}
	sort.Slice(resp.Items, func(i, j int) bool {
		if resp.Items[i].Node != resp.Items[j].Node {
			return resp.Items[i].Node < resp.Items[j].Node
		}
		return resp.Items[i].Iface < resp.Items[j].Iface
	})
	if resp.Items == nil {
		resp.Items = []Segment{}
	}
	return resp, nil
}

// ifaceIndex resolves (node, ifaceName) to the Bridge/SdnVnet it names, so
// buildSegment can attach Ref/Vid/Vnet/Zone context to a raw RA
// observation.
type ifaceIndex struct {
	bridges map[string]map[string]*inventory.Bridge // node -> name -> bridge
	vnets   map[string]*inventory.SdnVnet           // vnet ID -> vnet (PVE realizes a VNet as a same-named bridge on every node it's deployed to)
	zones   map[string]*inventory.SdnZone
}

func buildIfaceIndex(snap inventory.Snapshot) ifaceIndex {
	idx := ifaceIndex{
		bridges: map[string]map[string]*inventory.Bridge{},
		vnets:   map[string]*inventory.SdnVnet{},
		zones:   map[string]*inventory.SdnZone{},
	}
	for _, e := range snap.All() {
		switch v := e.(type) {
		case *inventory.Bridge:
			m := idx.bridges[v.GetRef().Node]
			if m == nil {
				m = map[string]*inventory.Bridge{}
				idx.bridges[v.GetRef().Node] = m
			}
			m[v.Name] = v
		case *inventory.SdnVnet:
			idx.vnets[v.ID] = v
		case *inventory.SdnZone:
			idx.zones[v.ID] = v
		}
	}
	return idx
}

// buildSegment attaches known Bridge/SdnVnet context (if any) to one raw
// per-node RA observation. An interface this daemon has no inventory
// entity for at all (e.g. a bridge not yet polled, or a name that matches
// neither) still renders — Kind is simply "" — never dropped, since the
// RA data itself is still a true observation regardless of whether vnprox
// can currently name what it belongs to.
func buildSegment(node string, o host.IPv6RAObservation, idx ifaceIndex) Segment {
	seg := Segment{
		Node: node, Iface: o.Iface,
		RAPresent: o.RAPresent, ManagedFlag: o.ManagedFlag, OtherFlag: o.OtherFlag,
		Prefixes: append([]string(nil), o.Prefixes...), RouterLifetimeSec: o.RouterLifetimeSec,
		DHCPv6ServerPresent: o.DHCPv6ServerPresent, DHCPv6InferredFromRA: o.DHCPv6InferredFromRA,
	}
	if vnet, ok := idx.vnets[o.Iface]; ok {
		seg.Kind = "vnet"
		seg.Ref = vnet.GetRef().String()
		seg.Vnet = vnet.ID
		seg.Vid = vnet.Tag
		seg.Zone = vnet.Zone
		return seg
	}
	if byName, ok := idx.bridges[node]; ok {
		if br, ok := byName[o.Iface]; ok {
			seg.Kind = "bridge"
			seg.Ref = br.GetRef().String()
			return seg
		}
	}
	return seg
}
