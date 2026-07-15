// Package evpn implements T-404's EVPN/BGP observability: docs/api.md's
// GET /sdn/evpn/status aggregates every cluster node's FRR state (peering
// matrix, session detail, VNI list, exit-node health) via internal/host's
// vtysh readers, fanned out across the cluster through the peer API
// exactly like internal/collect's host poller does for netlink/LLDP data
// (see nodeFRRReader/peerFRRReader below, the same local-vs-peer adapter
// shape as internal/collect/host.go's nodeHostReader/peerHostReader).
//
// Unlike internal/sdn (which reads PVE fresh on every request and holds no
// state of its own), this package's Service is long-lived: flap detection
// (docs/features/sdn.md §3: "Flapping sessions raise a health finding")
// is fundamentally about change across repeated observations, so the
// Service accumulates a short rolling history of observed session states
// across calls to Status — the same "collector with its own history"
// shape docs/architecture.md §2 assigns internal/metrics for counter
// rings, applied here to BGP session state instead.
package evpn

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/sdn"
)

// nodeFRRReader is the read surface Service needs for one node: satisfied
// directly by host.Reader for the local node, and by peerFRRReader (below)
// for every other cluster node.
type nodeFRRReader interface {
	FRRBGPSummary(ctx context.Context, node string) ([]byte, error)
	FRREVPNVNI(ctx context.Context, node string) ([]byte, error)
}

// PeerSource is the fan-out dependency: peer discovery plus the two FRR
// reads, routed through the peer API's {available,content} envelope
// (docs/api.md's Peer API section) rather than an error for "no FRR here"
// — *peer.Client satisfies this directly.
type PeerSource interface {
	Peers(ctx context.Context) ([]peer.Peer, error)
	FRRBGPSummary(ctx context.Context, p peer.Peer, node string) (available bool, raw []byte, err error)
	FRREVPNVNI(ctx context.Context, p peer.Peer, node string) (available bool, raw []byte, err error)
}

// peerFRRReader adapts a PeerSource + a specific peer.Peer into a
// nodeFRRReader, translating the peer API's available/error convention
// back into host.ErrFRRUnavailable so Service.fetchNode can treat local
// and peer nodes identically.
type peerFRRReader struct {
	source PeerSource
	peer   peer.Peer
}

func (r peerFRRReader) FRRBGPSummary(ctx context.Context, node string) ([]byte, error) {
	available, raw, err := r.source.FRRBGPSummary(ctx, r.peer, node)
	if err != nil {
		return nil, err
	}
	if !available {
		return nil, fmt.Errorf("evpn: peer %s: %w", r.peer.Node, host.ErrFRRUnavailable)
	}
	return raw, nil
}

func (r peerFRRReader) FRREVPNVNI(ctx context.Context, node string) ([]byte, error) {
	available, raw, err := r.source.FRREVPNVNI(ctx, r.peer, node)
	if err != nil {
		return nil, err
	}
	if !available {
		return nil, fmt.Errorf("evpn: peer %s: %w", r.peer.Node, host.ErrFRRUnavailable)
	}
	return raw, nil
}

// SDNZoneSource is the small, optional seam Service uses to compute
// exit-node health (docs/features/sdn.md §3: "exit-node health"):
// *sdn.Service's own Tree method satisfies this directly. Nil-safe —
// Status simply reports no exit nodes when SDN is not wired, the same
// nil-safety convention every other optional dependency in this codebase
// follows (e.g. peer.ServerOptions.LLDPInstaller).
type SDNZoneSource interface {
	Tree(ctx context.Context) (sdn.Tree, error)
}

// Config configures a Service.
type Config struct {
	// Host is the local node's FRR reader (in production, host.NewReal()
	// — the same instance internal/peer.ServerOptions.Reader already
	// uses).
	Host nodeFRRReader
	// Peers is the cluster fan-out dependency (in production,
	// *peer.Client). Nil is the documented single-node "zero peers" case.
	Peers PeerSource
	// LocalNode returns this daemon's own PVE node name, or "" before the
	// PVE poller has discovered it yet (mirrors
	// cmd/vnproxd/server.go's `localNode` closure over
	// collect.Collector.Status().LocalNode).
	LocalNode func() string
	// SDN is the optional exit-node-health seam (see SDNZoneSource).
	SDN        SDNZoneSource
	Now        func() time.Time
	FlapWindow time.Duration
	FlapThresh int
}

// Service aggregates cluster-wide FRR/EVPN state for GET /sdn/evpn/status.
type Service struct {
	flap *flapTracker
	cfg  Config
}

// NewService builds a Service. cfg.Host must be non-nil; cfg.Peers/cfg.SDN
// may be nil (degraded/single-node modes, see their doc comments).
func NewService(cfg Config) *Service {
	if cfg.LocalNode == nil {
		cfg.LocalNode = func() string { return "" }
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{
		cfg:  cfg,
		flap: newFlapTracker(cfg.FlapWindow, cfg.FlapThresh),
	}
}

// Status builds docs/api.md's GET /sdn/evpn/status response: this
// daemon's own node plus every currently-known peer's FRR state, each
// fetched independently so one unreachable/FRR-less node never blanks the
// rest of the cockpit (the same tolerate-individual-failures posture
// GET /audit/GET /snapshots' cluster fan-out and internal/collect's peer
// host poll both already establish).
func (s *Service) Status(ctx context.Context) (Status, error) {
	local := s.cfg.LocalNode()
	now := s.cfg.Now()

	var nodeNames []string
	readers := map[string]nodeFRRReader{}
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
				readers[p.Node] = peerFRRReader{source: s.cfg.Peers, peer: p}
			}
		}
	}
	sort.Strings(nodeNames)

	status := Status{GeneratedAt: now.Unix()}
	if discoveryFailed {
		status.Partial = true
		status.FailedNodes = append(status.FailedNodes, "<cluster peer discovery>")
	}

	for _, node := range nodeNames {
		ns, failed := s.fetchNode(ctx, node, readers[node], now)
		status.Nodes = append(status.Nodes, ns)
		if failed {
			status.Partial = true
			status.FailedNodes = append(status.FailedNodes, node)
		}
	}
	if status.Nodes == nil {
		status.Nodes = []NodeStatus{}
	}

	status.Findings = s.collectFindings(status.Nodes)
	if status.Findings == nil {
		status.Findings = []Finding{}
	}

	status.ExitNodes = s.exitNodeHealth(ctx, status.Nodes)
	if status.ExitNodes == nil {
		status.ExitNodes = []ExitNodeHealth{}
	}

	return status, nil
}

// fetchNode fetches and parses one node's BGP summary and EVPN VNI table,
// feeds every observed peer session's state into the flap tracker, and
// reports whether the fetch itself failed (as opposed to "FRR not
// installed", which is a clean, non-failure NodeStatus per AC2).
func (s *Service) fetchNode(ctx context.Context, node string, reader nodeFRRReader, now time.Time) (NodeStatus, bool) {
	// Peers/VNIs are initialized to empty (never nil) so every early return
	// below — no reader, FRR unavailable, a read/parse error — still carries
	// arrays, not nil slices. A nil slice marshals to JSON `null`, and the
	// EVPN view iterates node.peers / node.vnis directly (buildEvpnMatrix's
	// `for..of ns.peers`, VniList's `n.vnis.map`); a `null` there is not
	// iterable and blanked the whole EVPN/BGP page on any node without FRR.
	ns := NodeStatus{Node: node, Peers: []Peer{}, VNIs: []VNI{}}
	if reader == nil {
		ns.Error = "no reader configured for this node"
		return ns, true
	}

	bgpRaw, bgpErr := reader.FRRBGPSummary(ctx, node)
	switch {
	case bgpErr == nil:
		ns.FRRInstalled = true
	case errors.Is(bgpErr, host.ErrFRRUnavailable):
		ns.FRRInstalled = false
		return ns, false // AC2: clean "no EVPN", not a failure
	default:
		ns.Error = bgpErr.Error()
		return ns, true
	}

	summary, err := host.ParseBGPSummary(bgpRaw)
	if err != nil {
		ns.Error = fmt.Sprintf("parsing bgp summary: %v", err)
		return ns, true
	}
	ns.RouterID = summary.RouterID
	ns.ASN = summary.ASN
	ns.Peers = mergePeers(summary.Peers)
	for i := range ns.Peers {
		p := &ns.Peers[i]
		transitions := s.flap.observe(node, p.PeerAddr, now, p.State)
		p.FlapTransitions = transitions
	}
	if ns.Peers == nil {
		ns.Peers = []Peer{}
	}

	vniRaw, vniErr := reader.FRREVPNVNI(ctx, node)
	switch {
	case vniErr == nil:
		vnis, parseErr := host.ParseEVPNVNI(vniRaw)
		if parseErr != nil {
			ns.Error = fmt.Sprintf("parsing evpn vni table: %v", parseErr)
			return ns, true
		}
		ns.VNIs = toVNIs(vnis)
	case errors.Is(vniErr, host.ErrFRRUnavailable):
		// FRR is installed (the BGP summary read above succeeded) but
		// this particular read failed as unavailable — leave VNIs empty
		// rather than failing the whole node.
	default:
		ns.Error = vniErr.Error()
		return ns, true
	}
	if ns.VNIs == nil {
		ns.VNIs = []VNI{}
	}

	return ns, false
}

// mergePeers converts internal/host's per-(address,AFI) BGPPeer
// observations into one Peer per distinct address, preferring the
// "l2VpnEvpn" address-family observation (the one docs/features/sdn.md §3
// is about) when a peer appears under more than one AFI, and falling back
// to any other AFI's observation otherwise (same underlying TCP session,
// so state/uptime are equivalent; prefix counts differ per-AFI and are
// reported from whichever observation was kept).
func mergePeers(raw []host.BGPPeer) []Peer {
	byAddr := map[string]host.BGPPeer{}
	var order []string
	for _, p := range raw {
		existing, ok := byAddr[p.Addr]
		if !ok {
			byAddr[p.Addr] = p
			order = append(order, p.Addr)
			continue
		}
		if existing.AddressFamily != "l2VpnEvpn" && p.AddressFamily == "l2VpnEvpn" {
			byAddr[p.Addr] = p
		}
	}
	sort.Strings(order)
	out := make([]Peer, 0, len(order))
	for _, addr := range order {
		p := byAddr[addr]
		out = append(out, Peer{
			PeerAddr:      p.Addr,
			PeerNode:      p.Hostname,
			AddressFamily: p.AddressFamily,
			State:         p.State,
			StateReason:   p.StateReason,
			RemoteAS:      p.RemoteAS,
			PfxRcd:        p.PfxRcd,
			PfxSnt:        p.PfxSnt,
			UptimeSecs:    p.UptimeSecs,
		})
	}
	return out
}

func toVNIs(raw []host.EVPNVni) []VNI {
	out := make([]VNI, 0, len(raw))
	for _, v := range raw {
		out = append(out, VNI{
			VNI: v.VNI, Type: v.Type, VxlanIf: v.VxlanIf,
			TenantVRF: v.TenantVRF, NumMacs: v.NumMacs, NumArpND: v.NumArpND,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VNI < out[j].VNI })
	return out
}

// collectFindings emits one Finding per session whose flap-transition
// count meets the tracker's threshold (docs/features/sdn.md §3).
func (s *Service) collectFindings(nodes []NodeStatus) []Finding {
	var findings []Finding
	for _, ns := range nodes {
		for _, p := range ns.Peers {
			if !s.flap.flapping(p.FlapTransitions) {
				continue
			}
			findings = append(findings, Finding{
				ID:       fmt.Sprintf("evpn_bgp_flapping:%s:%s", ns.Node, p.PeerAddr),
				Code:     "evpn_bgp_flapping",
				Severity: "warning",
				Node:     ns.Node,
				PeerAddr: p.PeerAddr,
				Detail:   fmt.Sprintf("session %s<->%s changed state %d times in the last %s", ns.Node, p.PeerAddr, p.FlapTransitions, s.flap.window),
			})
		}
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].ID < findings[j].ID })
	return findings
}

// exitNodeHealth computes per-exit-node health for every EVPN zone
// SDNZoneSource reports (docs/features/sdn.md §3: "exit-node health"). A
// node is healthy iff it has FRR installed and every one of its observed
// peer sessions is Established; nil SDN dependency or a Tree() failure
// yields no exit-node entries at all (degrades cleanly, same as every
// other optional dependency).
func (s *Service) exitNodeHealth(ctx context.Context, nodes []NodeStatus) []ExitNodeHealth {
	if s.cfg.SDN == nil {
		return nil
	}
	tree, err := s.cfg.SDN.Tree(ctx)
	if err != nil {
		return nil
	}
	byNode := map[string]NodeStatus{}
	for _, ns := range nodes {
		byNode[ns.Node] = ns
	}

	var out []ExitNodeHealth
	for _, zone := range tree.Zones {
		if zone.Type != "evpn" || len(zone.ExitNodes) == 0 {
			continue
		}
		for _, en := range zone.ExitNodes {
			eh := ExitNodeHealth{Zone: zone.ID, Node: en}
			ns, ok := byNode[en]
			switch {
			case !ok:
				eh.Detail = "node not observed in this cluster fan-out"
			case ns.Error != "":
				eh.Detail = "FRR read failed: " + ns.Error
			case !ns.FRRInstalled:
				eh.Detail = "FRR not installed on exit node"
			default:
				eh.Healthy = true
				for _, p := range ns.Peers {
					if p.State != "Established" {
						eh.Healthy = false
						eh.Detail = fmt.Sprintf("session to %s is %s, not Established", p.PeerAddr, p.State)
						break
					}
				}
			}
			out = append(out, eh)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Zone != out[j].Zone {
			return out[i].Zone < out[j].Zone
		}
		return out[i].Node < out[j].Node
	})
	return out
}
