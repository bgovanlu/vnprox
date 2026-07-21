package microseg

import (
	"net/netip"

	"github.com/bgovanlu/vnprox/internal/baseline"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/sim"
)

// excluder decides whether a corpus flow was flagged anomalous by T-1601's own
// detector over the learning window and must therefore be excluded from
// observed-good. It is built once per Propose from baseline.Detect's output,
// keyed by the SAME subject strings baseline.Anomaly carries — so the planner's
// "is this flow anomalous?" test and baseline's own detection cannot drift
// apart. This exclusion is load-bearing: it is what stops a single transient
// compromise inside the training window from legitimizing itself into an allow
// rule (proven by TestPropose_ExcludesAnomalousFlows / TestDryRun_Anomaly*).
type excluder struct {
	newPorts   map[string]bool // baseline.PortKey.String() subjects
	newSubnets map[string]bool // peer-CIDR subjects
	spikeHours map[string]bool // baseline.HourSubject subjects
	active     bool
}

// newExcluder runs baseline.Detect against the supplied profile over the corpus
// and indexes the resulting anomalies for O(1) per-flow membership tests. An
// empty profile (cold start — no baseline learned yet) yields an inert
// excluder: with nothing to deviate from, no flow is flagged, so nothing is
// excluded (matching baseline.Detect's own cold-start-is-silent contract).
func newExcluder(profile baseline.Profile, corpus []flow.Record, cfg Config) excluder {
	ex := excluder{
		newPorts:   map[string]bool{},
		newSubnets: map[string]bool{},
		spikeHours: map[string]bool{},
	}
	if profile.Empty() {
		return ex
	}
	ex.active = true
	for _, a := range baseline.Detect(profile, corpus, cfg.detectConfig()) {
		switch a.Class {
		case baseline.ClassNewPort:
			ex.newPorts[a.Subject] = true
		case baseline.ClassNewSubnet:
			ex.newSubnets[a.Subject] = true
		case baseline.ClassVolumeSpike:
			ex.spikeHours[a.Subject] = true
		}
	}
	return ex
}

// flags reports whether m was flagged by any detected anomaly. A flow to a
// flagged new port, a flagged new subnet, OR falling inside a flagged
// volume-spike hour is excluded. The volume-spike hour test excludes the whole
// spiking hour's flows on purpose: baseline's spike anomaly names an hour, not
// individual flows, so the conservative (safe) reading is to treat every flow
// in that hour as untrusted rather than risk legitimizing the burst. Flows to
// services also seen in normal hours keep their rule from those other hours —
// only a service that appears solely during the spike loses its rule.
func (e excluder) flags(m flowMeta) bool {
	if !e.active {
		return false
	}
	if m.hasPort && e.newPorts[baseline.PortKey{Proto: m.proto, Port: m.port}.String()] {
		return true
	}
	if m.hasSubnet && e.newSubnets[m.subnet] {
		return true
	}
	if len(e.spikeHours) > 0 && e.spikeHours[baseline.HourSubject(m.at)] {
		return true
	}
	return false
}

// toFwFlow projects a flowMeta onto the sim.FwFlow tuple the shared firewall
// evaluator consumes. Only the peer side of the flow is address-constrained by
// the proposed rules (Source for an "in" rule, Dest for an "out" rule), so only
// the peer IP's known-ness matters; the guest's own side is left unset/unknown
// (the proposed rules never test it). An unparseable peer address stays
// unknown, so an address-restricting rule against it is honestly undecidable
// (matchAddr's own contract) rather than silently matched.
func toFwFlow(m flowMeta) sim.FwFlow {
	peer, err := netip.ParseAddr(m.peerIP)
	peerKnown := err == nil
	fl := sim.FwFlow{
		Proto:   flow.ProtoName(m.proto),
		Port:    m.port,
		PortSet: m.hasPort,
	}
	if m.direction == "in" {
		fl.SrcIP = peer
		fl.SrcKnown = peerKnown
	} else {
		fl.DstIP = peer
		fl.DstKnown = peerKnown
	}
	return fl
}

// alreadyAccepted reports whether view (the guest's current resolved firewall)
// ALREADY definitively accepts g's representative flow — in which case the
// planner suppresses a duplicate ACCEPT. It suppresses ONLY on a definitive
// allow: an indeterminate result (e.g. the existing rule references an alias
// this call cannot resolve) or a deny leaves the group un-suppressed, so the
// safe bias is to propose one extra rule, never to skip a rule on an unproven
// assumption that PVE already covers it. Reuses sim.EvaluateFirewall — the same
// evaluator the dry-run and path simulator use.
func alreadyAccepted(view *fw.ResolvedView, g *group, _ Config) bool {
	if view == nil {
		return false
	}
	eval := sim.EvaluateFirewall(*view, g.key.direction, toFwFlow(g.rep), nil, nil)
	return eval.Verdict == sim.FwAllowVerdict
}
