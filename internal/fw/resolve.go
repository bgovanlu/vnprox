package fw

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Origin labels where one entry in a resolved evaluation order came from,
// per docs/features/firewall.md §1's documented order: "cluster rules →
// security groups → guest rules → default policies".
type Origin string

const (
	// OriginCluster is a rule defined directly in the datacenter-wide
	// ruleset.
	OriginCluster Origin = "cluster"
	// OriginGroup is a rule that came from a security group's own rule
	// list, spliced in at the position a "GROUP" reference rule named it
	// (ResolvedRule.GroupName names the group).
	OriginGroup Origin = "group"
	// OriginGuest is a rule defined directly in the guest's own ruleset.
	OriginGuest Origin = "guest"
	// OriginDefault marks a DefaultPolicy's fallthrough action, not an
	// individual rule.
	OriginDefault Origin = "default"
)

// pveHardDefaultIn/Out are pve-firewall's own documented policy defaults
// applied when no scope in a guest's evaluation chain ever set
// policy_in/policy_out explicitly.
const (
	pveHardDefaultIn  = "DROP"
	pveHardDefaultOut = "ACCEPT"
)

// ResolvedRule is one entry in a guest's effective, ordered evaluation.
// Pos is the entry's 0-based position in the final effective order —
// stable and contiguous across cluster rules, spliced-in security-group
// rules, and the guest's own rules, regardless of which ruleset each rule
// came from. Disabled rules (Rule.Enabled == false) are included, not
// filtered, so the resolved view stays a complete, honest picture of
// what's configured — a caller computing an actual verdict (T-503) skips
// !Enabled entries itself.
type ResolvedRule struct {
	Origin    Origin
	GroupName string // set iff this rule came from (or is) a security-group reference
	Rule      inventory.FwRule
	Pos       int
}

// DefaultPolicy is the fallthrough action for one traffic direction when
// no rule in the effective order matches. Origin names which scope
// actually supplied the value: the guest's own ruleset if it set one
// explicitly, else the cluster's, else pve-firewall's own hardcoded
// default (OriginDefault; DROP in, ACCEPT out).
type DefaultPolicy struct {
	Direction string // "in" | "out"
	Policy    string // ACCEPT | DROP | REJECT
	Origin    Origin
}

// EnablementGate explains one reason a ruleset's rules are not actually
// being enforced — the "Datacenter firewall is OFF: none of these rules
// are active" footgun docs/features/firewall.md §2 documents by name,
// generalized to every scope it cascades to (see ScopeBanners).
type EnablementGate struct {
	Scope   inventory.FwScope
	Message string
}

// ResolvedView is a guest's full effective evaluation: the ordered rule
// list (with origin labels) and the two directions' fallthrough default
// policies, plus every enablement gate that makes some or all of Rules
// inert despite being configured.
type ResolvedView struct {
	Guest      inventory.Ref
	DefaultIn  DefaultPolicy
	DefaultOut DefaultPolicy
	Gates      []EnablementGate
	Rules      []ResolvedRule
	Active     bool
}

// Resolve computes guest's full effective evaluation order over snap.
//
// Design decision (T-607, confirmed against real hardware by T-3103 —
// planning/reports/evidence/pve-9.2.4-firewall-resolution-order.txt):
// docs/features/firewall.md §1 documents the guest resolved view's order
// as "cluster rules → security groups → guest rules → default policies" —
// this implementation follows that literally, applying the cluster
// ruleset's own rules directly to every guest's resolved view. Real
// pve-firewall's actual iptables realization only applies cluster.fw's
// top-level [RULES] to each node's *host* chain (PVEFW-HOST-IN/-OUT);
// cluster rules only reach a guest's own chain (tapNNNiN-IN/-OUT) when
// referenced via a security group, or through the FORWARD chain's generic
// conntrack defaults — never directly. This was confirmed, not assumed:
// the evidence file above captures a real `pve-firewall compile` showing a
// bare cluster rule landing in PVEFW-HOST-IN and absent from a guest's own
// tap chain. vnprox's product spec deliberately keeps the simpler, more
// visible model above anyway (this is the documented, reviewed behavior
// this task is scored against, not a guess) — the divergence from real
// pve-firewall's chain separation is now a known, confirmed trade-off, not
// an open question.
func Resolve(snap Snapshot, guest inventory.Ref) (ResolvedView, error) {
	if guest.Kind != inventory.KindGuest {
		return ResolvedView{}, fmt.Errorf("fw: resolve guest ref %s: not a guest ref", guest)
	}

	view := ResolvedView{Guest: guest}
	guestRS := snap.Guests[guest]

	view.Gates = guestGates(snap, guestRS)
	view.Active = len(view.Gates) == 0

	pos := 0
	if snap.Cluster != nil {
		pos = appendRuleset(&view.Rules, pos, OriginCluster, snap.Cluster.Rules, snap)
	}
	if guestRS != nil {
		appendRuleset(&view.Rules, pos, OriginGuest, guestRS.Rules, snap)
	}

	view.DefaultIn = resolveDefaultPolicy("in", snap.Cluster, guestRS)
	view.DefaultOut = resolveDefaultPolicy("out", snap.Cluster, guestRS)
	return view, nil
}

// guestGates computes the enablement gates for one guest's resolved view:
// the datacenter-off footgun (cascades from cluster scope) plus the
// guest's own firewall toggle. A nil Cluster (data not yet observed) is
// deliberately not itself a gate — see Snapshot.Cluster's doc comment —
// though it also means no cluster rules will be present in the resolved
// view.
func guestGates(snap Snapshot, guestRS *inventory.FwRuleset) []EnablementGate {
	var gates []EnablementGate
	if snap.Cluster != nil && !snap.Cluster.Enabled {
		gates = append(gates, EnablementGate{
			Scope:   inventory.FwScopeCluster,
			Message: "Datacenter firewall is OFF: none of these rules are active.",
		})
	}
	if guestRS == nil || !guestRS.Enabled {
		gates = append(gates, EnablementGate{
			Scope:   inventory.FwScopeGuest,
			Message: "Firewall is OFF for this guest: none of its rules (or any referenced security groups) are active.",
		})
	}
	return gates
}

// appendRuleset appends ruleRules (a cluster or guest ruleset's own rule
// list, in Pos order) to out, expanding any security-group reference rule
// in place, and returns the next free position.
func appendRuleset(out *[]ResolvedRule, pos int, origin Origin, rules []inventory.FwRule, snap Snapshot) int {
	for _, r := range rules {
		pos = appendRule(out, pos, origin, r, snap)
	}
	return pos
}

// appendRule appends one rule at pos, expanding it in place if it is a
// security-group reference (Direction == "group", per the real PVE API
// shape where a group-reference rule row has type "group" and its group
// name in the action field). The reference line itself always occupies
// one position — so the UI can show "security group <name> included
// here", including when disabled — and, only when enabled and the named
// group resolves, each of the group's own rules is spliced in immediately
// after, each tagged OriginGroup. It returns the next free position.
func appendRule(out *[]ResolvedRule, pos int, origin Origin, r inventory.FwRule, snap Snapshot) int {
	if r.Direction != "group" {
		*out = append(*out, ResolvedRule{Origin: origin, Rule: r, Pos: pos})
		return pos + 1
	}

	*out = append(*out, ResolvedRule{Origin: origin, GroupName: r.Action, Rule: r, Pos: pos})
	pos++
	if !r.Enabled {
		return pos
	}
	group, ok := snap.Group(r.Action)
	if !ok {
		// Dangling group reference (the named group does not exist) —
		// nothing to expand. Flagging this as a distinct finding is
		// drift-detection's job (docs/features/topology.md §6's family
		// list), not this resolver's; it simply contributes zero rules.
		return pos
	}
	for _, gr := range group.Rules {
		*out = append(*out, ResolvedRule{Origin: OriginGroup, GroupName: group.Name, Rule: gr, Pos: pos})
		pos++
	}
	return pos
}

// resolveDefaultPolicy picks the fallthrough policy for one direction:
// the guest's own ruleset's if it set one explicitly, else the cluster
// ruleset's, else pve-firewall's own hardcoded default.
func resolveDefaultPolicy(direction string, cluster, guest *inventory.FwRuleset) DefaultPolicy {
	if guest != nil {
		if p := policyFor(guest, direction); p != "" {
			return DefaultPolicy{Direction: direction, Policy: p, Origin: OriginGuest}
		}
	}
	if cluster != nil {
		if p := policyFor(cluster, direction); p != "" {
			return DefaultPolicy{Direction: direction, Policy: p, Origin: OriginCluster}
		}
	}
	hard := pveHardDefaultIn
	if direction == "out" {
		hard = pveHardDefaultOut
	}
	return DefaultPolicy{Direction: direction, Policy: hard, Origin: OriginDefault}
}

func policyFor(rs *inventory.FwRuleset, direction string) string {
	if direction == "in" {
		return rs.DefaultIn
	}
	return rs.DefaultOut
}

// ScopeBanners returns every enablement banner that applies to the
// ruleset at scope (docs/features/firewall.md §2's documented example,
// generalized: acceptance criterion 3 requires the datacenter-off warning
// to appear "at every affected scope", not only the Datacenter tab
// itself). ruleset may be nil (scope not yet observed, or — for node/
// guest — genuinely absent); node is required for scope == FwScopeNode
// (ignored otherwise) to name the affected node in the message.
func ScopeBanners(snap Snapshot, scope inventory.FwScope, node string, ruleset *inventory.FwRuleset) []EnablementGate {
	clusterOff := snap.Cluster != nil && !snap.Cluster.Enabled

	switch scope {
	case inventory.FwScopeCluster:
		if ruleset == nil || !ruleset.Enabled {
			return []EnablementGate{{
				Scope:   inventory.FwScopeCluster,
				Message: "Datacenter firewall is OFF: none of these rules are active.",
			}}
		}
		return nil
	case inventory.FwScopeNode:
		var gates []EnablementGate
		if clusterOff {
			gates = append(gates, EnablementGate{
				Scope:   inventory.FwScopeCluster,
				Message: fmt.Sprintf("Datacenter firewall is OFF: none of node %s's host-level rules are active.", node),
			})
		}
		if ruleset == nil || !ruleset.Enabled {
			gates = append(gates, EnablementGate{
				Scope:   inventory.FwScopeNode,
				Message: fmt.Sprintf("Firewall is OFF for node %s: none of its own rules are active.", node),
			})
		}
		return gates
	case inventory.FwScopeGuest:
		return guestGates(snap, ruleset)
	case inventory.FwScopeVNet:
		// Mirrors FwScopeNode's cascade shape (datacenter-off, then the
		// vnet's own toggle) — the closest existing precedent, since a vnet
		// ruleset is (like a node's) not a cascade *target* the way a guest
		// is, just a ruleset with its own enable flag. Not itself hardware-
		// captured: T-3103 scoped hardware validation to item 3's guest
		// resolution-order question, not to whether a vnet's forward chain
		// actually goes inert when the datacenter firewall is off. Flagged
		// in that task's report as needs-hardware-validation.
		var gates []EnablementGate
		if clusterOff {
			gates = append(gates, EnablementGate{
				Scope:   inventory.FwScopeCluster,
				Message: fmt.Sprintf("Datacenter firewall is OFF: none of vnet %s's forward-chain rules are active.", node),
			})
		}
		if ruleset == nil || !ruleset.Enabled {
			gates = append(gates, EnablementGate{
				Scope:   inventory.FwScopeVNet,
				Message: fmt.Sprintf("Firewall is OFF for vnet %s: none of its forward-chain rules are active.", node),
			})
		}
		return gates
	default:
		return nil
	}
}
