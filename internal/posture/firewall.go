package posture

import (
	"strings"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/microseg"
)

// anySources are the source specifiers that mean "from anywhere" — an empty
// source (no constraint) or the IPv4/IPv6 default routes. An inbound ACCEPT
// with one of these, unshadowed by a narrower earlier rule, is an exposed port.
var anySources = map[string]bool{
	"":          true,
	"0.0.0.0/0": true,
	"0.0.0.0":   true,
	"::/0":      true,
	"::":        true,
}

// segmentationCounts returns the total guest count and how many of those guests
// carry an APPLIED microsegmentation policy — a rule bearing microseg's marker
// comment present in the guest's resolved firewall view. Reads off the live
// inventory graph (PVE-authoritative, post-apply), so a merely-proposed policy
// (never staged, never applied, never re-polled) is invisible here by
// construction — the "only applied coverage counts" contract.
func segmentationCounts(snap inventory.Snapshot) (total, segmented int) {
	fwSnap := fw.BuildSnapshot(snap.All())
	for _, e := range snap.All() {
		g, ok := e.(*inventory.Guest)
		if !ok {
			continue
		}
		total++
		if guestHasMicrosegPolicy(fwSnap, g.Ref) {
			segmented++
		}
	}
	return total, segmented
}

// guestHasMicrosegPolicy reports whether guest's resolved firewall view
// contains at least one rule this planner emitted (identified by
// microseg.RuleCommentPrefix). Resolve is the shared T-501 evaluator, so
// "segmented" here means exactly what the firewall cockpit shows, not a
// parallel notion.
func guestHasMicrosegPolicy(fwSnap fw.Snapshot, guest inventory.Ref) bool {
	view, err := fw.Resolve(fwSnap, guest)
	if err != nil {
		return false
	}
	for _, rr := range view.Rules {
		if strings.HasPrefix(rr.Rule.Comment, microseg.RuleCommentPrefix) {
			return true
		}
	}
	return false
}

// exposedPortCount counts, across every guest's resolved firewall view, the
// distinct inbound ACCEPT rules that permit traffic from any source with no
// narrower rule ahead of them.
//
// "Narrower rule ahead" is resolved honestly but deliberately simply (an
// approximation flagged in this card's report, mirroring internal/fw's own
// documented simplifications): an any-source inbound ACCEPT is counted UNLESS an
// earlier enabled inbound rule in the same resolved order is a DROP/REJECT that
// would shadow it — an earlier any-source deny, or a deny for the same
// (proto,port). A specific-source earlier rule is NOT treated as shadowing the
// broader any-source ACCEPT (it only covers its own narrower slice). Disabled
// rules and views gated inert by an enablement banner are skipped: a rule that
// is not actually being enforced does not expose a port.
func exposedPortCount(snap inventory.Snapshot) int {
	fwSnap := fw.BuildSnapshot(snap.All())
	count := 0
	for _, e := range snap.All() {
		g, ok := e.(*inventory.Guest)
		if !ok {
			continue
		}
		view, err := fw.Resolve(fwSnap, g.Ref)
		if err != nil || !view.Active {
			continue
		}
		count += exposedInView(view)
	}
	return count
}

// exposedInView counts the exposed inbound ports in one resolved view, walking
// the rules in evaluation order and tracking earlier denies that shadow later
// any-source accepts.
func exposedInView(view fw.ResolvedView) int {
	var denyAnyPort bool             // an earlier any-source DROP/REJECT for all ports
	deniedPorts := map[string]bool{} // (proto|port) denied by an earlier any-source rule
	exposed := map[string]bool{}

	for _, rr := range view.Rules {
		r := rr.Rule
		if !r.Enabled || r.Direction != "in" {
			continue
		}
		if !anySources[strings.TrimSpace(r.Source)] {
			continue // a narrower (specific-source) rule; does not shadow any-source accepts
		}
		switch r.Action {
		case "DROP", "REJECT":
			if r.Dport == "" {
				denyAnyPort = true
			} else {
				deniedPorts[r.Proto+"|"+r.Dport] = true
			}
		case "ACCEPT":
			key := r.Proto + "|" + r.Dport
			if denyAnyPort || deniedPorts[key] {
				continue // shadowed by an earlier any-source deny
			}
			exposed[key] = true
		}
	}
	return len(exposed)
}
