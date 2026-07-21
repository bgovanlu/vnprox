package microseg

import (
	"sort"

	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/sim"
)

// FlowRef identifies one classified flow in a DryRun report — enough to trace a
// would-have-blocked (or cannot-determine) flow back to the exact observed
// conversation and the rule tail it fell into. Reason is set only for
// CannotDetermine entries (the undecidable rule's own reason string).
type FlowRef struct {
	Direction  string `json:"direction"`
	PeerIP     string `json:"peerIp"`
	PeerSubnet string `json:"peerSubnet"`
	Reason     string `json:"reason,omitempty"`
	Proto      int    `json:"proto"`
	Port       int    `json:"port"`
	At         int64  `json:"at"`
	Bytes      int64  `json:"bytes"`
}

// Report is DryRun's result: every replayed flow in exactly one bucket.
//
// The honesty contract governs the buckets. WouldAllow/WouldBlock are the
// definitive verdicts from the shared firewall evaluator. CannotDetermine is
// the load-bearing third bucket docs/features/firewall.md §5/§6 demands: a flow
// the evaluator could not decide is reported here LOUDLY, never folded into
// WouldAllow — the planner never assumes a flow stays permitted when it cannot
// prove it. Ungoverned holds flows in a direction the proposal does not govern
// (the proposal changes nothing for them, so classifying them as blocked would
// be a lie). CoveragePct echoes the proposal's coverage for the report
// envelope.
type Report struct {
	WouldAllow      []FlowRef `json:"wouldAllow"`
	WouldBlock      []FlowRef `json:"wouldBlock"`
	CannotDetermine []FlowRef `json:"cannotDetermine"`
	Ungoverned      []FlowRef `json:"ungoverned"`
	CoveragePct     float64   `json:"coveragePct"`
}

// DryRun replays every flow in corpus against prop's proposed ruleset and
// classifies each one, using internal/sim's firewall evaluator (the SAME
// rule-walk path simulation uses — no second, divergent evaluator). A
// would-block among observed-good flows is the exact "would-have-blocked"
// signal T-1603's review UI surfaces before anyone enforces.
//
// Soundness boundary (stated plainly, because a false "safe" here is an
// outage): DryRun proves what the PROPOSED RULESET does to each replayed flow —
// which ACCEPT matches it, or that the trailing deny drops it. It does not
// re-derive reachability (the flow was observed, so it was reachable) and does
// not model conntrack/NAT/guest-internal firewalls (internal/sim discloses
// those same limits on every path result). Where the evaluator cannot decide a
// flow, DryRun reports CannotDetermine — it never counts such a flow as allowed.
func DryRun(prop Proposal, corpus []flow.Record, cfg Config) Report {
	cfg = cfg.withDefaults()
	view := proposalView(prop)
	governed := map[string]bool{}
	for _, d := range prop.Directions {
		governed[d] = true
	}
	target := prop.Subject.GuestRef.String()

	rep := Report{CoveragePct: prop.CoveragePct}
	for _, rec := range corpus {
		m, ok := projectFlow(rec, target, cfg)
		if !ok {
			continue
		}
		ref := flowRef(m)
		if !governed[m.direction] {
			rep.Ungoverned = append(rep.Ungoverned, ref)
			continue
		}
		eval := sim.EvaluateFirewall(view, m.direction, toFwFlow(m), nil, nil)
		switch eval.Verdict {
		case sim.FwAllowVerdict:
			rep.WouldAllow = append(rep.WouldAllow, ref)
		case sim.FwDenyVerdict:
			rep.WouldBlock = append(rep.WouldBlock, ref)
		default: // FwIndeterminateVerdict — loud "cannot determine", never assumed safe
			ref.Reason = eval.Reason
			rep.CannotDetermine = append(rep.CannotDetermine, ref)
		}
	}

	sortFlowRefs(rep.WouldAllow)
	sortFlowRefs(rep.WouldBlock)
	sortFlowRefs(rep.CannotDetermine)
	sortFlowRefs(rep.Ungoverned)
	return rep
}

// proposalView wraps a proposal's ordered rules as a fw.ResolvedView the shared
// evaluator can walk. Both default policies are DROP (default-deny posture),
// though the trailing match-all deny in each governed direction means the
// default is never actually reached for a governed flow — the policy is fully
// self-contained in its own rules, which is exactly what makes the dry-run
// evaluate the literal artifact Stage emits.
func proposalView(prop Proposal) fw.ResolvedView {
	view := fw.ResolvedView{
		Guest:      prop.Subject.GuestRef,
		Active:     true,
		DefaultIn:  fw.DefaultPolicy{Direction: "in", Policy: "DROP", Origin: fw.OriginDefault},
		DefaultOut: fw.DefaultPolicy{Direction: "out", Policy: "DROP", Origin: fw.OriginDefault},
	}
	for i, r := range prop.Rules {
		rr := r
		rr.Pos = i
		view.Rules = append(view.Rules, fw.ResolvedRule{Origin: fw.OriginGuest, Rule: rr, Pos: i})
	}
	return view
}

func flowRef(m flowMeta) FlowRef {
	return FlowRef{
		Direction:  m.direction,
		PeerIP:     m.peerIP,
		PeerSubnet: m.subnet,
		Proto:      m.proto,
		Port:       m.port,
		At:         m.at,
		Bytes:      m.bytes,
	}
}

func sortFlowRefs(fs []FlowRef) {
	sort.Slice(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.At != b.At {
			return a.At < b.At
		}
		if a.Direction != b.Direction {
			return a.Direction < b.Direction
		}
		if a.Proto != b.Proto {
			return a.Proto < b.Proto
		}
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		return a.PeerIP < b.PeerIP
	})
}
