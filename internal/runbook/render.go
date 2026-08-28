// SPDX-License-Identifier: Apache-2.0

package runbook

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Render is the pure, deterministic half of preparing a runbook — mirroring
// internal/blueprint.Instantiate's own shape and doc comment ("never touches
// the store or the change engine itself"). Given a Runbook and the Finding
// it is being run against plus a freshly-gathered ReadContext, it runs that
// Runbook's read-checks and, if every one still holds, returns the ops its
// template proposes and a title for the resulting changeset draft.
//
// Two distinct "nothing happens" outcomes, both errors, deliberately not
// collapsed into one: ErrNothingToDo means a read-check found the condition
// already resolved (the idempotent, "matches -> stage nothing" case);
// every other error means the runbook could not even determine what to
// propose (a malformed finding, a missing entity, an unsupported case).
// Render never returns a nil error with an empty ops slice — there is
// always something to stage or a reason there is not.
func Render(rb Runbook, f findings.Finding, rc ReadContext) ([]change.Op, string, error) {
	if f.Check != rb.CheckName {
		return nil, "", fmt.Errorf("%w: runbook %q attaches to check %q, finding %q is check %q",
			ErrNotAttached, rb.Name, rb.CheckName, f.ID, f.Check)
	}

	switch rb.Template {
	case TemplateDeleteOrphanVnet:
		return renderDeleteOrphanVnet(f, rc)
	case TemplateDeleteUnusedFwRule:
		return renderDeleteUnusedFwRule(f, rc)
	case TemplateTrimUnusedTrunkVids:
		return renderTrimUnusedTrunkVids(f, rc)
	default:
		return nil, "", fmt.Errorf("%w: %q", ErrUnimplementedTemplate, rb.Template)
	}
}

// renderDeleteOrphanVnet implements TemplateDeleteOrphanVnet
// (findings.CheckOrphanVnet / health_orphanvnet.go). It proposes only the
// "delete the orphaned vnet" half of that check's own doc comment's two
// legitimate fixes ("create the missing zone" or "delete the orphaned
// vnet") — recreating a zone needs zone parameters (type, tag range, MTU...)
// nothing about the finding or the deleted zone's own now-gone config can
// recover, so it is not something this template can safely guess at. An
// operator who wants the other fix still has the ordinary changeset editor.
func renderDeleteOrphanVnet(f findings.Finding, rc ReadContext) ([]change.Op, string, error) {
	vnetRef, err := singleRef(f)
	if err != nil {
		return nil, "", err
	}
	ent, ok := rc.Snapshot.Get(vnetRef)
	if !ok {
		return nil, "", fmt.Errorf("%w: %s already gone", ErrNothingToDo, vnetRef)
	}
	vnet, ok := ent.(*inventory.SdnVnet)
	if !ok {
		return nil, "", fmt.Errorf("runbook: ref %s is not an SDN VNet", vnetRef)
	}

	// Read-check: re-verify the vnet's zone is STILL missing. If the zone
	// was recreated since the finding fired, this vnet is no longer
	// orphaned and deleting it would destroy a now-valid object.
	for _, e := range rc.Snapshot.All() {
		if z, ok := e.(*inventory.SdnZone); ok && z.ID == vnet.Zone {
			return nil, "", fmt.Errorf("%w: zone %s now exists; %s is no longer orphaned", ErrNothingToDo, vnet.Zone, vnetRef)
		}
	}

	op := change.Op{Type: change.OpSdnVnetDelete, Target: vnetRef, Params: change.SdnVnetDeleteParams{}}
	title := fmt.Sprintf("Delete orphaned SDN VNet %s (zone %s no longer exists)", vnet.ID, vnet.Zone)
	return []change.Op{op}, title, nil
}

// renderTrimUnusedTrunkVids implements TemplateTrimUnusedTrunkVids
// (findings.CheckTrunkUnusedVlans / health_trunkvlans.go). It deliberately
// recomputes the unused-VID set fresh from rc.Snapshot rather than parsing
// the finding's own Detail text (which is a formatted, capped-at-20,
// human-readable list, not a machine contract) — the same "always live,
// never a stale cached value" principle types.go's Finding doc comment
// states for FixOps, applied here to a runbook's own read-check.
func renderTrimUnusedTrunkVids(f findings.Finding, rc ReadContext) ([]change.Op, string, error) {
	bridgeRef, err := singleRef(f)
	if err != nil {
		return nil, "", err
	}
	ent, ok := rc.Snapshot.Get(bridgeRef)
	if !ok {
		return nil, "", fmt.Errorf("%w: %s no longer exists", ErrNothingToDo, bridgeRef)
	}
	bridge, ok := ent.(*inventory.Bridge)
	if !ok {
		return nil, "", fmt.Errorf("runbook: ref %s is not a bridge", bridgeRef)
	}

	used := map[int]bool{}
	for _, e := range rc.Snapshot.All() {
		nic, ok := e.(*inventory.GuestNic)
		if !ok || nic.BridgeOrVnet != bridgeRef {
			continue
		}
		if nic.EffectiveVid != 0 {
			used[nic.EffectiveVid] = true
		}
	}

	var kept []int
	unusedRemain := false
	for _, vr := range bridge.Vids {
		for vid := vr.Low; vid <= vr.High; vid++ {
			if used[vid] {
				kept = append(kept, vid)
			} else {
				unusedRemain = true
			}
		}
	}
	if !unusedRemain {
		return nil, "", fmt.Errorf("%w: bridge %s's trunk is already fully in guest use", ErrNothingToDo, bridgeRef)
	}
	if len(kept) == 0 {
		// Narrowing to zero VIDs is not "trim the unused ones" any more —
		// it changes what the trunk means rather than pruning it, and no
		// guest currently on this bridge would keep working. Refuse rather
		// than propose it; a bridge with genuinely nothing worth trunking
		// is an editor decision, not a templated one.
		return nil, "", fmt.Errorf(
			"runbook: narrowing bridge %s's trunk would drop every currently-configured VID (no guest NIC uses any of them); refusing rather than propose an empty trunk",
			bridgeRef)
	}
	sort.Ints(kept)
	newVids := coalesceVids(kept)

	op := change.Op{Type: change.OpBridgeUpdate, Target: bridgeRef, Params: change.BridgeUpdateParams{Vids: &newVids}}
	title := fmt.Sprintf("Narrow bridge %s's trunk to VLANs currently in guest use", bridge.Name)
	return []change.Op{op}, title, nil
}

// coalesceVids turns a sorted, deduplicated list of VLAN ids into the
// minimal ordered set of change.VidRange spans that represents exactly the
// same ids (consecutive runs collapse into one range each).
func coalesceVids(sorted []int) []change.VidRange {
	var out []change.VidRange
	for i := 0; i < len(sorted); {
		j := i
		for j+1 < len(sorted) && sorted[j+1] == sorted[j]+1 {
			j++
		}
		out = append(out, change.VidRange{Low: sorted[i], High: sorted[j]})
		i = j + 1
	}
	return out
}

// renderDeleteUnusedFwRule implements TemplateDeleteUnusedFwRule
// (findings.CheckFwRuleUnused / health_fwruleunused.go). Guest-scoped rules
// only: a cluster- or security-group-scoped rule's op target is not a
// direct function of the finding's own guest ref the way a guest rule's is
// (internal/change/params_fw.go's own documented Ref convention — Target
// is "guest/<kind>/<vmid>" for a guest ruleset; group-scoped rules live
// inside the *cluster* ruleset's own Groups[].Rules, addressed by a
// mechanism this package does not model), so this template refuses those
// with ErrUnsupportedRuleOrigin rather than guess at a target.
func renderDeleteUnusedFwRule(f findings.Finding, rc ReadContext) ([]change.Op, string, error) {
	guestRefStr, origin, pos, groupName, err := parseFwRuleUnusedFindingID(f.ID)
	if err != nil {
		return nil, "", err
	}
	if origin != "guest" {
		return nil, "", fmt.Errorf("%w: origin %q (only guest-scoped rules are supported today)", ErrUnsupportedRuleOrigin, origin)
	}

	guestRef, err := inventory.ParseRef(guestRefStr)
	if err != nil {
		return nil, "", fmt.Errorf("runbook: parsing guest ref %q: %w", guestRefStr, err)
	}
	guestEnt, ok := rc.Snapshot.Get(guestRef)
	if !ok {
		return nil, "", fmt.Errorf("%w: guest %s", ErrRefNotFound, guestRefStr)
	}
	guest, ok := guestEnt.(*inventory.Guest)
	if !ok {
		return nil, "", fmt.Errorf("runbook: ref %s is not a guest", guestRefStr)
	}

	// Read-check: re-verify against fresh firewall log analytics that this
	// exact rule (guest + origin + position + group) is STILL unused. A
	// hit recorded between when the finding fired and now must cancel the
	// remediation, not delete a rule that just started matching traffic.
	if rc.FwAnalytics == nil {
		return nil, "", fmt.Errorf("runbook: firewall analytics unavailable; cannot re-verify rule %d on %s is still unused", pos, guestRefStr)
	}
	stillUnused := false
	for _, u := range rc.FwAnalytics.UnusedRules {
		if u.Rule.GuestRef == guestRefStr && u.Rule.Origin == origin && u.Rule.Pos == pos && u.Rule.GroupName == groupName {
			stillUnused = true
			break
		}
	}
	if !stillUnused {
		return nil, "", fmt.Errorf("%w: rule at pos %d on %s has recorded a hit (or no longer matches) since the finding fired", ErrNothingToDo, pos, guestRefStr)
	}

	rulesetRef := inventory.Ref{Kind: inventory.KindFwRuleset, Node: guestRef.Node, ID: "guest/" + guest.Type + "/" + guestRef.ID}
	op := change.Op{Type: change.OpFwRuleDelete, Target: rulesetRef, Params: change.FwRuleDeleteParams{Pos: pos}}
	title := fmt.Sprintf("Delete unused firewall rule at position %d on %s", pos, guestRefStr)
	return []change.Op{op}, title, nil
}

// parseFwRuleUnusedFindingID recovers the (guestRef, origin, pos, groupName)
// identity health_fwruleunused.go's fwRuleUnusedFinding embeds in a
// fw_rule_unused finding's own ID:
//
//	fmt.Sprintf("health:%s|%s|%s|%d", CheckFwRuleUnused, guestRef, origin, pos)
//	  [+ "|" + groupName, iff origin == "group"]
//
// The check's Refs field carries only the guest ref (deliberately — see
// that file's fwRuleUnusedFinding doc comment: two unused rules on the same
// guest would collide on a refs-only key), so this is the only place a
// fw_rule_unused finding's exact rule identity is recoverable from its
// public fields.
//
// KNOWN LIMIT, stated per this repo's own convention for a coupling like
// this (findings/catalog.go's own doc comment states an analogous one):
// this couples the runbook to health_fwruleunused.go's private ID format. A
// change there without a matching change here fails closed — a malformed
// or unexpected ID returns ErrMalformedFindingID, never a wrong rule.
func parseFwRuleUnusedFindingID(id string) (guestRef, origin string, pos int, groupName string, err error) {
	parts := strings.Split(id, "|")
	if len(parts) != 4 && len(parts) != 5 {
		return "", "", 0, "", fmt.Errorf("%w: %q", ErrMalformedFindingID, id)
	}
	guestRef = parts[1]
	origin = parts[2]
	pos, convErr := strconv.Atoi(parts[3])
	if convErr != nil {
		return "", "", 0, "", fmt.Errorf("%w: %q: %v", ErrMalformedFindingID, id, convErr)
	}
	if len(parts) == 5 {
		groupName = parts[4]
	}
	return guestRef, origin, pos, groupName, nil
}

// singleRef requires f to carry exactly one ref (every built-in template
// that isn't the fw-rule one binds its target this way) and parses it,
// wrapping a parse failure as ErrMalformedFindingID since a malformed ref
// here means the producer that built this Finding did not follow its own
// documented Refs contract.
func singleRef(f findings.Finding) (inventory.Ref, error) {
	if len(f.Refs) != 1 {
		return inventory.Ref{}, fmt.Errorf("runbook: %s finding %s has %d refs, want exactly 1", f.Check, f.ID, len(f.Refs))
	}
	ref, err := inventory.ParseRef(f.Refs[0])
	if err != nil {
		return inventory.Ref{}, fmt.Errorf("%w: parsing ref %q: %v", ErrMalformedFindingID, f.Refs[0], err)
	}
	return ref, nil
}
