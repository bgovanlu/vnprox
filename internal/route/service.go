// SPDX-License-Identifier: Apache-2.0

package route

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// Snapshot is one node's full route-explorer view: kernel FIB (every
// table, both address families), policy rules (both families), and — when
// the node runs FRR — its RIB. Read-only, assembled fresh on every
// request (like GET /conntrack's live table read — there is nothing here
// worth caching across requests, and nothing here is ever persisted to
// vnprox's own store per CLAUDE.md's "app-owned data only" rule).
type Snapshot struct {
	Node  string
	FIB   []FIBRoute
	Rules []PolicyRule
	// RIB is nil when FRRUnavailable is true (node runs no FRR) — the
	// FRRUnavailable/nil-not-empty-slice distinction is deliberate,
	// mirroring internal/evpn.Status' own "no EVPN" vs. "EVPN configured
	// with zero of something" convention.
	RIB []RIBRoute
	// FRRUnavailable mirrors host.ErrFRRUnavailable at the Snapshot
	// level: true means this node runs no FRR at all (a documented,
	// clean condition — most nodes with no SDN EVPN zone), not a fetch
	// failure. A genuine fetch failure for either FIB source instead
	// makes Fetch itself return an error (there is no comparably clean
	// "the kernel has no routing table" condition to degrade into).
	FRRUnavailable bool
}

// Fetcher is the local-node read seam Service needs: exactly
// *host.Real's six route-related methods (internal/host/route.go, this
// task's addition there), declared here as vnprox's usual small-interface
// seam (docs/architecture.md §2) rather than importing host.Reader's much
// larger interface for six methods of it. *pvemock.FixtureHostReader
// satisfies this too (internal/pvemock/route.go), for tests.
type Fetcher interface {
	RouteTableV4(ctx context.Context, node string) ([]byte, error)
	RouteTableV6(ctx context.Context, node string) ([]byte, error)
	RouteRulesV4(ctx context.Context, node string) ([]byte, error)
	RouteRulesV6(ctx context.Context, node string) ([]byte, error)
	// FRRRIBV4/V6 return an error wrapping host.ErrFRRUnavailable when
	// the node runs no FRR at all, exactly like host.Reader's own
	// FRRBGPSummary/FRREVPNVNI.
	FRRRIBV4(ctx context.Context, node string) ([]byte, error)
	FRRRIBV6(ctx context.Context, node string) ([]byte, error)
}

// *host.Real satisfies Fetcher directly, asserted at compile time for the
// same reason PeerSource's assertion below exists.
var _ Fetcher = (*host.Real)(nil)

// PeerSource is the cluster fan-out dependency: peer discovery plus the
// same six reads, routed through the peer API — *peer.Client satisfies
// this directly once its route-explorer methods are added
// (internal/peer/client.go), the same "PeerSource == *peer.Client" shape
// internal/neighbor.PeerSource and internal/evpn.PeerSource already use.
type PeerSource interface {
	Peers(ctx context.Context) ([]peer.Peer, error)
	RouteTableV4(ctx context.Context, p peer.Peer, node string) ([]byte, error)
	RouteTableV6(ctx context.Context, p peer.Peer, node string) ([]byte, error)
	RouteRulesV4(ctx context.Context, p peer.Peer, node string) ([]byte, error)
	RouteRulesV6(ctx context.Context, p peer.Peer, node string) ([]byte, error)
	FRRRIBV4(ctx context.Context, p peer.Peer, node string) (available bool, raw []byte, err error)
	FRRRIBV6(ctx context.Context, p peer.Peer, node string) (available bool, raw []byte, err error)
}

// *peer.Client satisfies PeerSource directly (its RouteTableV4/V6,
// RouteRulesV4/V6, and FRRRIBV4/V6 methods, internal/peer/client.go) —
// asserted here at compile time so a signature drift on either side
// (this interface or peer.Client's methods) fails the build immediately
// rather than surfacing as a runtime wiring error in cmd/vnproxd.
var _ PeerSource = (*peer.Client)(nil)

// peerFetcher adapts a PeerSource + a specific peer.Peer into a Fetcher,
// translating the peer wire's {available,content} FRR envelope back into
// host.ErrFRRUnavailable so Service.Snapshot can treat local and peer
// nodes identically — the same shape internal/evpn's peerFRRReader uses.
type peerFetcher struct {
	source PeerSource
	peer   peer.Peer
}

func (f peerFetcher) RouteTableV4(ctx context.Context, node string) ([]byte, error) {
	return f.source.RouteTableV4(ctx, f.peer, node)
}

func (f peerFetcher) RouteTableV6(ctx context.Context, node string) ([]byte, error) {
	return f.source.RouteTableV6(ctx, f.peer, node)
}

func (f peerFetcher) RouteRulesV4(ctx context.Context, node string) ([]byte, error) {
	return f.source.RouteRulesV4(ctx, f.peer, node)
}

func (f peerFetcher) RouteRulesV6(ctx context.Context, node string) ([]byte, error) {
	return f.source.RouteRulesV6(ctx, f.peer, node)
}

func (f peerFetcher) FRRRIBV4(ctx context.Context, node string) ([]byte, error) {
	available, raw, err := f.source.FRRRIBV4(ctx, f.peer, node)
	if err != nil {
		return nil, err
	}
	if !available {
		return nil, fmt.Errorf("route: peer %s: %w", f.peer.Node, host.ErrFRRUnavailable)
	}
	return raw, nil
}

func (f peerFetcher) FRRRIBV6(ctx context.Context, node string) ([]byte, error) {
	available, raw, err := f.source.FRRRIBV6(ctx, f.peer, node)
	if err != nil {
		return nil, err
	}
	if !available {
		return nil, fmt.Errorf("route: peer %s: %w", f.peer.Node, host.ErrFRRUnavailable)
	}
	return raw, nil
}

// Config configures a Service.
type Config struct {
	// Host is the local node's route reader (in production, the same
	// host.NewReal() instance every other local-node observability
	// service already shares — cmd/vnproxd/server.go's realHost).
	Host Fetcher
	// Peers is the cluster fan-out dependency (in production,
	// *peer.Client). Nil is the documented single-node "zero peers" case.
	Peers PeerSource
	// LocalNode returns this daemon's own PVE node name, or "" before
	// the PVE poller has discovered it yet (mirrors internal/neighbor.
	// Config's identical field).
	LocalNode func() string
	Logger    *slog.Logger
}

// Service is T-3903's cluster-aware route-explorer backend: per-node FIB/
// policy-rule/FRR-RIB snapshots and the Lookup engine over them. Read-only
// throughout.
type Service struct {
	cfg Config
	log *slog.Logger
}

// NewService builds a Service. cfg.Host must be non-nil for the local
// node to be servable; cfg.Peers may be nil (single-node/degraded mode).
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

// Nodes returns every node this Service can currently produce a Snapshot
// for: the local node (if known) plus every currently-reachable peer,
// sorted — the route-explorer UI's node picker.
func (s *Service) Nodes(ctx context.Context) []string {
	var nodes []string
	if local := s.cfg.LocalNode(); local != "" {
		nodes = append(nodes, local)
	}
	if s.cfg.Peers != nil {
		if peers, err := s.cfg.Peers.Peers(ctx); err == nil {
			for _, p := range peers {
				nodes = append(nodes, p.Node)
			}
		}
	}
	sort.Strings(nodes)
	return nodes
}

// fetcherFor resolves node to a Fetcher: the local Host reader when node
// is this daemon's own node, otherwise a peerFetcher routed through the
// peer API — mirroring internal/neighbor.Service.Neighbors' identical
// local-vs-peer resolution.
func (s *Service) fetcherFor(ctx context.Context, node string) (Fetcher, error) {
	if node == "" || node == s.cfg.LocalNode() {
		if s.cfg.Host == nil {
			return nil, fmt.Errorf("route: no local host reader configured")
		}
		return s.cfg.Host, nil
	}
	if s.cfg.Peers == nil {
		return nil, fmt.Errorf("route: node %q is not the local node and no peer source is configured", node)
	}
	peers, err := s.cfg.Peers.Peers(ctx)
	if err != nil {
		return nil, fmt.Errorf("route: discovering cluster peers: %w", err)
	}
	for _, p := range peers {
		if p.Node == node {
			return peerFetcher{source: s.cfg.Peers, peer: p}, nil
		}
	}
	return nil, fmt.Errorf("route: node %q: %w", node, ErrNodeNotFound)
}

// ErrNodeNotFound is returned by Snapshot/Lookup when node names neither
// the local node nor a currently-known peer.
var ErrNodeNotFound = errors.New("route: node not found")

// Snapshot reads node's full route-explorer view. A fetch failure on the
// kernel FIB or policy-rule reads is a hard error (there is no documented
// "the kernel has no routing table" degraded case, unlike FRR); an FRR
// read failing specifically with host.ErrFRRUnavailable instead sets
// Snapshot.FRRUnavailable and leaves RIB nil, never failing the whole
// call — a node with no SDN EVPN zone configured (most nodes) is the
// common case, not a fault.
func (s *Service) Snapshot(ctx context.Context, node string) (Snapshot, error) {
	fetcher, err := s.fetcherFor(ctx, node)
	if err != nil {
		return Snapshot{}, err
	}
	resolvedNode := node
	if resolvedNode == "" {
		resolvedNode = s.cfg.LocalNode()
	}

	v4Raw, err := fetcher.RouteTableV4(ctx, resolvedNode)
	if err != nil {
		return Snapshot{}, fmt.Errorf("route: node %s: reading ipv4 FIB: %w", resolvedNode, err)
	}
	v6Raw, err := fetcher.RouteTableV6(ctx, resolvedNode)
	if err != nil {
		return Snapshot{}, fmt.Errorf("route: node %s: reading ipv6 FIB: %w", resolvedNode, err)
	}
	v4Fib, err := ParseFIBRoutes(v4Raw, AFIv4)
	if err != nil {
		return Snapshot{}, fmt.Errorf("route: node %s: %w", resolvedNode, err)
	}
	v6Fib, err := ParseFIBRoutes(v6Raw, AFIv6)
	if err != nil {
		return Snapshot{}, fmt.Errorf("route: node %s: %w", resolvedNode, err)
	}

	rulesV4Raw, err := fetcher.RouteRulesV4(ctx, resolvedNode)
	if err != nil {
		return Snapshot{}, fmt.Errorf("route: node %s: reading ipv4 policy rules: %w", resolvedNode, err)
	}
	rulesV6Raw, err := fetcher.RouteRulesV6(ctx, resolvedNode)
	if err != nil {
		return Snapshot{}, fmt.Errorf("route: node %s: reading ipv6 policy rules: %w", resolvedNode, err)
	}
	rulesV4, err := ParsePolicyRules(rulesV4Raw, AFIv4)
	if err != nil {
		return Snapshot{}, fmt.Errorf("route: node %s: %w", resolvedNode, err)
	}
	rulesV6, err := ParsePolicyRules(rulesV6Raw, AFIv6)
	if err != nil {
		return Snapshot{}, fmt.Errorf("route: node %s: %w", resolvedNode, err)
	}

	snap := Snapshot{
		Node:  resolvedNode,
		FIB:   append(v4Fib, v6Fib...),
		Rules: append(rulesV4, rulesV6...),
	}

	ribV4Raw, err := fetcher.FRRRIBV4(ctx, resolvedNode)
	switch {
	case err == nil:
		ribV4, perr := ParseFRRRIB(ribV4Raw, AFIv4)
		if perr != nil {
			return Snapshot{}, fmt.Errorf("route: node %s: %w", resolvedNode, perr)
		}
		snap.RIB = append(snap.RIB, ribV4...)
	case errors.Is(err, host.ErrFRRUnavailable):
		snap.FRRUnavailable = true
	default:
		s.log.Debug("route: reading FRR ipv4 RIB failed, continuing without it", "node", resolvedNode, "error", err)
		snap.FRRUnavailable = true
	}

	if !snap.FRRUnavailable {
		ribV6Raw, err := fetcher.FRRRIBV6(ctx, resolvedNode)
		switch {
		case err == nil:
			ribV6, perr := ParseFRRRIB(ribV6Raw, AFIv6)
			if perr != nil {
				return Snapshot{}, fmt.Errorf("route: node %s: %w", resolvedNode, perr)
			}
			snap.RIB = append(snap.RIB, ribV6...)
		case errors.Is(err, host.ErrFRRUnavailable):
			// FRR was reachable a moment ago for v4 but not v6 —
			// vanishingly unlikely (same daemon, same vtysh), but
			// handled rather than assumed: report what v4 found
			// rather than discarding it.
			s.log.Debug("route: FRR ipv6 RIB unavailable after ipv4 RIB succeeded", "node", resolvedNode)
		default:
			s.log.Debug("route: reading FRR ipv6 RIB failed, continuing without it", "node", resolvedNode, "error", err)
		}
	}

	return snap, nil
}

// Lookup answers "which path would traffic to dst take from node" (T-3903's
// core operator question): node's FIB + policy rules, run through the pure
// Lookup function in lookup.go.
func (s *Service) Lookup(ctx context.Context, node, dst, ifaceHint string) (LookupResult, error) {
	snap, err := s.Snapshot(ctx, node)
	if err != nil {
		return LookupResult{}, err
	}
	return Lookup(snap.FIB, snap.Rules, dst, ifaceHint)
}
