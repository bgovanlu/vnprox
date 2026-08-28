// SPDX-License-Identifier: Apache-2.0

// overlaywire.go wires T-4106's overlay-readiness preflight
// (change.OverlayReadinessPreflighter, internal/change/validate_overlay.go)
// onto internal/evpn (BGP session state) and internal/mtuprobe (measured
// underlay MTU headroom). Like failsimAdapter (failsimwire.go), the
// dependency built after change.NewService — here, evpnSvc, which needs
// peerClient/sdnSvc built further down server.go — is left unset at
// construction and filled in via set() once available; mtuProbeSvc is
// available earlier (setupMTUProbe, before changeSvc) so it is passed in
// directly.
//
// VTEP reachability's honest limits: internal/mtuprobe never records a
// negative result (Service.Tick keeps the last known-good reading on a
// failed probe attempt rather than overwriting it with an absence — see
// that method's own doc comment), so this adapter can only ever confirm
// "reachable" (a link this node has successfully probed) or "unknown" (no
// successful probe yet) for the VTEP signal, never a confirmed
// "unreachable" — there is no dedicated reachability prober in this
// codebase to source that negative fact from. This is a deliberate,
// flagged scope boundary (see this task's completion report), not an
// oversight: the pure composer in internal/change fully supports and tests
// an OverlayBad VTEP verdict for whenever a future signal source can
// honestly produce one.

package main

import (
	"context"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/evpn"
	"github.com/bgovanlu/vnprox/internal/mtuprobe"
)

// overlayAdapter implements change.OverlayReadinessPreflighter.
type overlayAdapter struct {
	mtu     *mtuprobe.Service
	evpnSvc *evpn.Service
}

func newOverlayAdapter(mtu *mtuprobe.Service) *overlayAdapter {
	return &overlayAdapter{mtu: mtu}
}

func (a *overlayAdapter) set(evpnSvc *evpn.Service) { a.evpnSvc = evpnSvc }

// OverlayReadiness implements change.OverlayReadinessPreflighter: fetches
// cluster BGP/EVPN state once (not once per zone) and mtuprobe's current
// readings, then composes each queried zone's BGP/VTEP/MTU signals from
// that one fetch.
func (a *overlayAdapter) OverlayReadiness(ctx context.Context, zones []change.OverlayZoneQuery) (map[string]change.ZoneOverlaySignals, error) {
	out := make(map[string]change.ZoneOverlaySignals, len(zones))
	if len(zones) == 0 {
		return out, nil
	}

	var status evpn.Status
	var statusErr error
	if a.evpnSvc != nil {
		status, statusErr = a.evpnSvc.Status(ctx)
	}
	byNode := make(map[string]evpn.NodeStatus, len(status.Nodes))
	for _, ns := range status.Nodes {
		byNode[ns.Node] = ns
	}

	var mtuResults []mtuprobe.Result
	if a.mtu != nil {
		mtuResults = a.mtu.Results()
	}

	for _, z := range zones {
		out[z.ZoneID] = change.ZoneOverlaySignals{
			BGP:  a.bgpSignal(z.Nodes, byNode, statusErr),
			VTEP: vtepSignal(z.Nodes, mtuResults),
			MTU:  a.mtuSignal(z.Nodes),
		}
	}
	return out, nil
}

// bgpSignal reports zone's BGP/EVPN control-plane readiness across nodes:
// OverlayBad the moment any node's any observed peer session is not
// Established (a confirmed, named-reason failure — mirrors
// internal/evpn.exitNodeHealth's identical "every observed session must be
// Established" rule); OverlayUnknown when this daemon has no EVPN seam
// wired, the cluster-wide fetch itself failed, a node was not observed in
// the fan-out, its FRR read failed, or FRR is not installed/running there
// (T-404's own documented "FRR not installed" clean-but-unknown case);
// OverlayGood only once every node was observed with FRR installed and at
// least one Established session was actually seen.
func (a *overlayAdapter) bgpSignal(nodes []string, byNode map[string]evpn.NodeStatus, statusErr error) change.OverlaySignal {
	if a.evpnSvc == nil {
		return change.OverlaySignal{State: change.OverlayUnknown, Detail: "evpn service not configured on this daemon"}
	}
	if statusErr != nil {
		return change.OverlaySignal{State: change.OverlayUnknown, Detail: fmt.Sprintf("fetching cluster BGP/EVPN status failed: %v", statusErr)}
	}

	var unknownDetail string
	var establishedSeen bool
	for _, node := range nodes {
		ns, ok := byNode[node]
		switch {
		case !ok:
			unknownDetail = fmt.Sprintf("node %s not observed in this cluster's BGP/EVPN fan-out", node)
		case ns.Error != "":
			unknownDetail = fmt.Sprintf("FRR read on node %s failed: %s", node, ns.Error)
		case !ns.FRRInstalled:
			unknownDetail = fmt.Sprintf("FRR not installed/running on node %s", node)
		default:
			for _, p := range ns.Peers {
				if p.State != "Established" {
					return change.OverlaySignal{
						State:  change.OverlayBad,
						Detail: fmt.Sprintf("session %s<->%s is %s, not Established", node, p.PeerAddr, p.State),
					}
				}
				establishedSeen = true
			}
		}
	}
	if unknownDetail != "" {
		return change.OverlaySignal{State: change.OverlayUnknown, Detail: unknownDetail}
	}
	if !establishedSeen {
		return change.OverlaySignal{State: change.OverlayUnknown, Detail: "no BGP peer sessions observed on this zone's nodes"}
	}
	return change.OverlaySignal{State: change.OverlayGood}
}

// vtepSignal reports OverlayGood when mtuprobe has successfully probed at
// least one link touching one of nodes (a successful DF-probe is itself
// proof the underlying path was reachable at the time it ran), else
// OverlayUnknown — see this file's own doc comment for why a confirmed
// OverlayBad is not something this data source can honestly produce.
func vtepSignal(nodes []string, results []mtuprobe.Result) change.OverlaySignal {
	if results == nil {
		return change.OverlaySignal{State: change.OverlayUnknown, Detail: "mtuprobe service not configured on this daemon"}
	}
	set := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		set[n] = true
	}
	for _, r := range results {
		if set[r.FromNode] || set[r.ToNode] {
			return change.OverlaySignal{
				State:  change.OverlayGood,
				Detail: fmt.Sprintf("confirmed via a successful mtuprobe probe on link %s", r.LinkID),
			}
		}
	}
	return change.OverlaySignal{State: change.OverlayUnknown, Detail: "no mtuprobe reachability data yet for this zone's nodes"}
}

// mtuSignal resolves the tightest (minimum) measured underlay MTU across
// nodes via mtuprobe.Service.MeasuredUnderlayMTU — the constraining node
// for the whole VTEP mesh's headroom. HasValue false (the zero value) when
// no node has a measurement yet, which overlayMTUReason
// (internal/change/validate_overlay.go) treats as "fall back to the
// assumed default", never blocking.
func (a *overlayAdapter) mtuSignal(nodes []string) change.OverlayMTUSignal {
	if a.mtu == nil {
		return change.OverlayMTUSignal{}
	}
	var sig change.OverlayMTUSignal
	for _, n := range nodes {
		m, ok := a.mtu.MeasuredUnderlayMTU(n)
		if !ok {
			continue
		}
		if !sig.HasValue || m < sig.Measured {
			sig = change.OverlayMTUSignal{Node: n, Measured: m, HasValue: true}
		}
	}
	return sig
}
