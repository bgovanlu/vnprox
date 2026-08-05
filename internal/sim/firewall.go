package sim

import (
	"fmt"
	"strings"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// fwState is the firewall phase's outcome.
type fwState int

const (
	fwAllow fwState = iota
	fwDeny
	fwIndeterminate
)

type fwOutcome struct {
	blocking *RuleRef
	state    fwState
}

// evaluateFirewall runs pve-firewall evaluation at every enforcement point
// the path crosses, in PVE's real order: the source guest's OUT chain first
// (the packet leaving), then the destination guest's IN chain. It uses
// internal/fw.Resolve (T-501) as the substrate, so its decisions agree with
// the resolved view the rest of the product renders (AC2).
func (e *Engine) evaluateFirewall(src, dst resolvedEP, req Request, res *Result) fwOutcome {
	fl := flow{
		proto:    req.Proto,
		port:     req.Port,
		portSet:  req.Port != 0,
		srcIP:    src.ip,
		srcKnown: src.ipKnown,
		dstIP:    dst.ip,
		dstKnown: dst.ipKnown,
	}

	if src.kind == EndpointGuestNic {
		if o := e.enforceGuest(src, fl, "out", "source-guest-out", res); o.state != fwAllow {
			return o
		}
	}
	if dst.kind == EndpointGuestNic {
		if o := e.enforceGuest(dst, fl, "in", "dest-guest-in", res); o.state != fwAllow {
			return o
		}
	}
	e.noteNodeFirewall(src, res)
	e.noteNodeFirewall(dst, res)
	return fwOutcome{state: fwAllow}
}

// enforceGuest evaluates one guest enforcement point (its resolved view,
// filtered to dir) against the flow.
func (e *Engine) enforceGuest(ep resolvedEP, fl flow, dir, point string, res *Result) fwOutcome {
	guest := ep.nic.Guest

	// Per-NIC pve-firewall toggle: if the NIC has firewall=0, no rules apply
	// to it (disabled-scope passthrough).
	if !ep.fwEnabled {
		e.addHop(res, Hop{Kind: "firewall", Node: ep.node,
			Label:  fmt.Sprintf("firewall (%s): not enforced", point),
			Detail: fmt.Sprintf("firewall is disabled on NIC %s (firewall=0)", ep.nic.Key)})
		return fwOutcome{state: fwAllow}
	}

	view, err := fw.Resolve(e.fw, guest)
	if err != nil {
		res.addCaveat(blockerCaveat(CodeNotEvaluated,
			fmt.Sprintf("could not resolve firewall for %s: %v", guest, err)))
		return fwOutcome{state: fwIndeterminate}
	}

	// Enablement gates (datacenter-off, guest-fw-off) make every rule inert:
	// PVE forwards the traffic. Honest passthrough with a disclosing caveat.
	if !view.Active {
		e.addHop(res, Hop{Kind: "firewall", Node: ep.node,
			Label:  fmt.Sprintf("firewall (%s): not enforced", point),
			Detail: gatesDetail(view.Gates)})
		res.addCaveat(infoCaveat(CodeSimulated, fmt.Sprintf(
			"Firewall not enforced at %s: %s. Traffic passes this point unfiltered.", point, gatesDetail(view.Gates))))
		return fwOutcome{state: fwAllow}
	}

	lk := e.lookupFor(guest)
	dec := decideDirection(view, dir, fl, lk)

	// Surface T-501's cluster→guest simplification when it was decisive.
	if dec.origin == fw.OriginCluster {
		res.addCaveat(warnCaveat(CodeFwClusterHostGuest, fmt.Sprintf(
			"The %s decision came from a cluster-scope rule applied directly to the guest's chain — internal/fw's documented simplification of pve-firewall's host/guest chain separation (needs hardware validation).", point)))
	}
	if dec.ifaceSeen {
		res.addCaveat(warnCaveat(CodeSimulated,
			"A rule along this path constrains a network interface (iface=); interface matching is not evaluated, so such rules are treated as interface-agnostic."))
	}

	switch dec.kind {
	case decisionUnknown:
		res.addCaveat(blockerCaveat(CodeNotEvaluated, fmt.Sprintf(
			"firewall %s could not be decided: %s", point, dec.reason)))
		if dec.reason != "" && dec.unknownIsIP {
			res.addCaveat(blockerCaveat(CodeGuestIPUnknown, fmt.Sprintf(
				"A firewall rule at %s restricts by address but an endpoint IP is unknown (the inventory does not carry guest IPs); %s", point, FeatureGuestIP)))
		}
		return fwOutcome{state: fwIndeterminate}
	case decisionDeny:
		rr := e.ruleRef(point, dir, guest, dec)
		e.addHop(res, Hop{Kind: "firewall", Node: ep.node,
			Label:  fmt.Sprintf("firewall (%s): %s", point, dec.action),
			Detail: describeDecision(dec)})
		return fwOutcome{state: fwDeny, blocking: rr}
	default: // allow
		e.addHop(res, Hop{Kind: "firewall", Node: ep.node,
			Label:  fmt.Sprintf("firewall (%s): ACCEPT", point),
			Detail: describeDecision(dec)})
		return fwOutcome{state: fwAllow}
	}
}

type decisionKind int

const (
	decisionAllow decisionKind = iota
	decisionDeny
	decisionUnknown
)

// decision is the resolved outcome of walking one direction of a guest's
// evaluation order.
type decision struct {
	action      string
	origin      fw.Origin
	groupName   string
	reason      string
	rule        inventory.FwRule
	kind        decisionKind
	pos         int
	fromRule    bool
	unknownIsIP bool
	ifaceSeen   bool
}

// decideDirection walks view's rules for direction dir, returning the first
// definitive match, or the direction's default policy on fallthrough. An
// undecidable rule short-circuits to decisionUnknown (never guessed past).
//
// It is a free function (it reads only its arguments, never Engine state) so
// the exported EvaluateFirewall wrapper (eval.go) — the microsegmentation
// dry-run's evaluator (T-1602) — reuses this exact rule-walk rather than
// re-deriving a second, divergent firewall evaluator.
func decideDirection(view fw.ResolvedView, dir string, fl flow, lk fwLookup) decision {
	ifaceSeen := false
	for _, rr := range view.Rules {
		r := rr.Rule
		if r.Direction == "group" { // a group-reference marker, not a leaf rule
			continue
		}
		if !r.Enabled || r.Direction != dir {
			continue
		}
		if r.Iface != "" {
			ifaceSeen = true
		}
		m := matchRule(r, fl, lk)
		switch m.state {
		case matchUnknown:
			return decision{kind: decisionUnknown, reason: m.reason,
				unknownIsIP: isIPReason(m.reason), ifaceSeen: ifaceSeen}
		case matchYes:
			return decision{
				kind: actionKind(r.Action), action: r.Action, fromRule: true,
				rule: r, origin: rr.Origin, groupName: rr.GroupName, pos: rr.Pos,
				ifaceSeen: ifaceSeen,
			}
		}
	}
	// Fallthrough to the direction's default policy.
	def := view.DefaultIn
	if dir == "out" {
		def = view.DefaultOut
	}
	return decision{kind: actionKind(def.Policy), action: def.Policy, origin: def.Origin, ifaceSeen: ifaceSeen}
}

func actionKind(action string) decisionKind {
	switch action {
	case "ACCEPT":
		return decisionAllow
	case "DROP", "REJECT":
		return decisionDeny
	default:
		// An unrecognized action (e.g. a jump to another group we did not
		// expand) is not something to guess a verdict from.
		return decisionUnknown
	}
}

// ruleRef builds the RuleRef the frozen `simulate.path` MCP tool
// (cmd/vnproxd/mcpwire.go's mcpSimulatePath returns sim.Result verbatim,
// docs/architecture.md §13.1's decision D10) and POST /simulate/path's own
// blockingRule both surface. RulesetRef names the ruleset the matched rule
// is literally defined in — the cluster ruleset for origin cluster/group,
// or guest's own ruleset for origin guest — populated for all three cases
// (T-2002 fixed the guest-origin gap this had before). This is deliberately
// NOT what web/src/simulator/deeplink.ts's deep link uses (it derives the
// guest to open from ResolvedEndpoint.guest instead, since the link's
// target — "which guest's resolved view to open" — differs from "which
// ruleset owns this rule" whenever origin is cluster/group); RulesetRef
// exists for a different, still-genuine consumer: an MCP automation client
// that wants to target a fw.rule.* changeset op at the exact ruleset the
// blocking rule lives in.
func (e *Engine) ruleRef(point, dir string, guest inventory.Ref, dec decision) *RuleRef {
	rulesetRef := ""
	switch dec.origin {
	case fw.OriginCluster, fw.OriginGroup:
		if e.fw.Cluster != nil {
			rulesetRef = e.fw.Cluster.GetRef().String()
		}
	case fw.OriginGuest:
		if rs, ok := e.fw.Guests[guest]; ok && rs != nil {
			rulesetRef = rs.GetRef().String()
		}
	}
	return &RuleRef{
		EnforcementPoint: point,
		RulesetRef:       rulesetRef,
		Origin:           string(dec.origin),
		GroupName:        dec.groupName,
		Pos:              dec.pos,
		Direction:        dir,
		Action:           dec.action,
		Rule:             dec.rule,
	}
}

// noteNodeFirewall discloses that an enabled node-scope (host chain) ruleset
// exists but is not evaluated for guest forwarded traffic.
func (e *Engine) noteNodeFirewall(ep resolvedEP, res *Result) {
	if ep.kind != EndpointGuestNic {
		return
	}
	if rs, ok := e.fw.Nodes[ep.node]; ok && rs != nil && rs.Enabled && len(rs.Rules) > 0 {
		res.addCaveat(warnCaveat(CodeNodeFirewall, fmt.Sprintf(
			"Node %s has host-scope firewall rules; guest-to-guest forwarded traffic does not traverse the host INPUT/OUTPUT chains, so those rules are not evaluated for this path.", ep.node)))
	}
}

func gatesDetail(gates []fw.EnablementGate) string {
	if len(gates) == 0 {
		return "firewall disabled"
	}
	return gates[0].Message
}

func describeDecision(dec decision) string {
	if !dec.fromRule {
		return fmt.Sprintf("default policy %s (origin %s)", dec.action, dec.origin)
	}
	label := fmt.Sprintf("rule #%d %s (origin %s", dec.pos, dec.action, dec.origin)
	if dec.groupName != "" {
		label += " " + dec.groupName
	}
	return label + ")"
}

func isIPReason(reason string) bool {
	return strings.Contains(reason, "endpoint IP is not known")
}
