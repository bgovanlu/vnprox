// SPDX-License-Identifier: Apache-2.0

package fw

import "github.com/bgovanlu/vnprox/internal/inventory"

// MatchingGuests is T-502's rule-effects preview (docs/features/firewall.md
// §2 P1: "for a guest-scope or group rule, vnprox lists which guests/IPs
// the rule will match, computed from inventory"). For a security-group
// reference, "matches" means every guest whose full resolved evaluation
// order (Resolve) actually splices in that group's own rules — which,
// given this package's documented cluster-cascade simplification
// (resolve.go's Resolve doc comment: cluster rules apply directly to every
// guest), includes every guest at all whenever the *cluster* ruleset is the
// one referencing the group, not just guests that reference it themselves.
//
// Deliberately not filtered by ResolvedView.Active or the reference rule's
// own Enabled flag beyond what Resolve itself already encodes (a disabled
// group-reference rule never splices the group's rules into the resolved
// view at all — appendRule's own behavior — so it naturally excludes
// itself here too): this mirrors T-501's "the resolved view stays a
// complete, honest picture" philosophy — a guest whose *own* firewall
// toggle is off but whose ruleset still configures a group reference is
// still reported as a configured match, since that's what "effects
// preview" is answering ("if this were live, who would it reach"), not "is
// it live right now" (the enablement banners already answer that
// separately).
//
// Guests are returned in Snapshot.sortedGuestRefs order (deterministic —
// map iteration order is otherwise unstable).
func MatchingGuests(snap Snapshot, group string) ([]inventory.Ref, error) {
	var out []inventory.Ref
	for _, g := range snap.sortedGuestRefs() {
		view, err := Resolve(snap, g)
		if err != nil {
			return nil, err
		}
		for _, r := range view.Rules {
			if r.Origin == OriginGroup && r.GroupName == group {
				out = append(out, g)
				break
			}
		}
	}
	return out, nil
}
