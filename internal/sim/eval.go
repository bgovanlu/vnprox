package sim

import (
	"net/netip"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// eval.go exposes the engine's firewall rule-order decision as a standalone,
// reachability-free evaluator. Its sole external consumer today is
// internal/microseg's microsegmentation dry-run (T-1602), which must classify
// every observed flow against a *proposed* ruleset using the SAME rule-walk
// (decideDirection) and per-rule matcher (matchRule) Simulate itself uses —
// docs/features/firewall.md §6's "no silent approximation" honesty contract
// forbids a second, divergent firewall evaluator. Everything here is a thin,
// type-converting wrapper over the exact internal code path; it adds no new
// matching logic of its own.

// FwVerdict is the tri-state outcome of evaluating one direction of a resolved
// firewall view against a single flow. It is deliberately tri-state: an
// undecidable rule (an unresolvable alias/ipset/macro, or an address test
// against an unknown endpoint IP) yields FwIndeterminate, never a guessed
// allow/deny — the honesty-contract lynchpin the dry-run relies on to say
// "cannot determine" loudly rather than assume a flow stays permitted.
type FwVerdict int

const (
	// FwAllowVerdict: the rule order (or the direction's default policy)
	// definitively permits the flow.
	FwAllowVerdict FwVerdict = iota
	// FwDenyVerdict: a rule (or default policy) definitively drops/rejects it.
	FwDenyVerdict
	// FwIndeterminateVerdict: the engine could not decide — Reason names what
	// it could not resolve. A caller must NOT treat this as allow.
	FwIndeterminateVerdict
)

// FwFlow is the concrete traffic tuple EvaluateFirewall tests a resolved view
// against — the exported projection of the engine's internal flow type. SrcIP/
// DstIP are the flow's addresses; SrcKnown/DstKnown record whether each is
// actually known (an address-restricting rule against a not-known endpoint is
// undecidable, matchAddr's own contract). Proto is a lowercase name
// ("tcp"/"udp"/"icmp"/"") — empty is proto-agnostic. Port is the destination
// port; PortSet distinguishes "port 0 was asked" (unset) from a real port.
type FwFlow struct {
	SrcIP    netip.Addr
	DstIP    netip.Addr
	Proto    string
	Port     int
	PortSet  bool
	SrcKnown bool
	DstKnown bool
}

// FwEval is EvaluateFirewall's result: the verdict plus, for
// FwIndeterminateVerdict, the reason string the undecidable rule produced (so
// the dry-run's "cannot determine" report can name what blocked evaluation).
type FwEval struct {
	Reason  string
	Verdict FwVerdict
}

// EvaluateFirewall resolves the effective verdict for one direction
// ("in"|"out") of view against fl, reusing decideDirection/matchRule verbatim.
// aliases/ipsets supply the name resolution a rule's source/dest may reference
// (both may be nil for a ruleset built from literal CIDRs, as the
// microsegmentation planner's proposed rules always are).
//
// It evaluates ONLY the rule-order decision — not reachability, conntrack,
// enablement gates, or node/host chains. That is exactly, and only, what a
// firewall-policy dry-run needs: "given this ordered ruleset, would this flow
// be accepted or dropped?" A caller wanting a full path verdict uses Simulate;
// a caller dry-running a proposed ruleset against already-observed flows (whose
// reachability is a fact, not a question) uses this.
func EvaluateFirewall(view fw.ResolvedView, dir string, fl FwFlow, aliases []inventory.FwAlias, ipsets []inventory.FwIPSet) FwEval {
	lk := fwLookup{
		aliases: make(map[string]inventory.FwAlias, len(aliases)),
		ipsets:  make(map[string]inventory.FwIPSet, len(ipsets)),
	}
	for _, a := range aliases {
		lk.aliases[a.Name] = a
	}
	for _, s := range ipsets {
		lk.ipsets[s.Name] = s
	}

	dec := decideDirection(view, dir, flow{
		srcIP:    fl.SrcIP,
		dstIP:    fl.DstIP,
		proto:    fl.Proto,
		port:     fl.Port,
		portSet:  fl.PortSet,
		srcKnown: fl.SrcKnown,
		dstKnown: fl.DstKnown,
	}, lk)

	switch dec.kind {
	case decisionUnknown:
		return FwEval{Verdict: FwIndeterminateVerdict, Reason: dec.reason}
	case decisionDeny:
		return FwEval{Verdict: FwDenyVerdict}
	default:
		return FwEval{Verdict: FwAllowVerdict}
	}
}
