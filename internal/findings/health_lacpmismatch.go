// health_lacpmismatch.go implements T-804's `lacp_partner_mismatch` check:
// an 802.3ad bond whose slaves disagree on the LACP partner's system
// ID/key (split-brain aggregation — each slave is actually talking to a
// different real partner) or whose actor state isn't fully negotiated
// (synchronized+collecting+distributing, docs/features/change-management.md
// §5) on a bond netlink reports as MII-up. Substrate: internal/host's
// /proc/net/bonding "details actor/partner lacp pdu" parser (bonding.go),
// flowed through inventory.Bond.SlaveDetail exactly like
// health_bondslave.go's CheckBondSlaveDown reads that same field's older
// MIIStatus/Active data. Detection only: which physical action fixes a
// switch-side LACP misconfiguration is outside any changeset op vnprox can
// safely compute (mirrors CheckBondSlaveDown's own "no computable fix"
// stance). Standard hysteresis debounce — this is live negotiation state, a
// noisy per-poll observation, not a structural fact like mgmt_single_path.

package findings

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

const CheckLACPPartnerMismatch = "lacp_partner_mismatch"

const lacpPartnerMismatchDocsLink = "docs/features/monitoring.md#5-health-checks"

// lacpMismatchRiseCycles/lacpMismatchFallCycles: same debounce window as
// CheckBondSlaveDown (health_bondslave.go) — a breach/clear must repeat 2
// consecutive Engine cycles before the finding fires/clears, so one noisy
// poll (a slave mid-renegotiation after a cable bounce) never flaps it.
const (
	lacpMismatchRiseCycles = 2
	lacpMismatchFallCycles = 2
)

// checkLACPPartnerMismatch evaluates every Bond entity whose netlink-
// reported MII status is up against db (Engine's per-check debouncer) and
// returns one finding per bond that is currently split-brained or not
// fully negotiated, among the slaves that reported LACP PDU detail at all
// (T-804's LACPDetailSet gate — a bond not running 802.3ad, or an older
// kernel/driver, legitimately reports none and this check has nothing to
// say about it).
func checkLACPPartnerMismatch(snap inventory.Snapshot, db *debouncer) []Finding {
	var out []Finding
	live := map[string]bool{}

	for _, e := range snap.All() {
		bond, ok := e.(*inventory.Bond)
		if !ok {
			continue
		}
		if !strings.EqualFold(bond.MIIStatus, "up") {
			continue
		}

		var withDetail []inventory.BondSlaveState
		for _, sl := range bond.SlaveDetail {
			if sl.LACPDetailSet && strings.EqualFold(sl.MIIStatus, "up") {
				withDetail = append(withDetail, sl)
			}
		}
		if len(withDetail) == 0 {
			continue
		}

		key := bond.GetRef().String()
		live[key] = true

		reason, breach := lacpMismatchReason(withDetail)
		active := db.Evaluate(key, breach, lacpMismatchRiseCycles, lacpMismatchFallCycles)
		if !active {
			continue
		}

		refs := []string{bond.GetRef().String()}
		for _, sl := range withDetail {
			refs = append(refs, inventory.Ref{Kind: inventory.KindPhysNic, Node: bond.GetRef().Node, ID: sl.Name}.String())
		}
		detail := fmt.Sprintf("bond %s on node %s: %s", bond.Name, bond.GetRef().Node, reason)
		f := newHealthFinding(CheckLACPPartnerMismatch, SeverityWarning, detail, []string{bond.GetRef().Node}, refs)
		f.DocsLink = lacpPartnerMismatchDocsLink
		out = append(out, f)
	}

	db.Prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// lacpMismatchReason evaluates one bond's LACP-detailed, MII-up slaves and
// reports whether it currently breaches (split-brain partner identity takes
// priority over a plain not-negotiated report, since it is the more
// specific and more actionable diagnosis) plus a plain-English detail.
func lacpMismatchReason(slaves []inventory.BondSlaveState) (reason string, breach bool) {
	partners := map[string][]string{}
	var notNegotiated []string
	for _, sl := range slaves {
		partnerKey := fmt.Sprintf("%s/%d", sl.PartnerSystemID, sl.PartnerKey)
		partners[partnerKey] = append(partners[partnerKey], sl.Name)
		if !sl.ActorSynchronized || !sl.ActorCollecting || !sl.ActorDistributing {
			notNegotiated = append(notNegotiated, sl.Name)
		}
	}

	if len(partners) > 1 {
		keys := make([]string, 0, len(partners))
		for k := range partners {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			names := partners[k]
			sort.Strings(names)
			parts = append(parts, fmt.Sprintf("%s -> partner %s", strings.Join(names, ","), k))
		}
		return "slaves disagree on the LACP partner system/key (split-brain aggregation): " + strings.Join(parts, "; "), true
	}

	if len(notNegotiated) > 0 {
		sort.Strings(notNegotiated)
		return fmt.Sprintf("slave(s) %s are not fully negotiated (missing synchronized/collecting/distributing)", strings.Join(notNegotiated, ",")), true
	}

	return "", false
}
