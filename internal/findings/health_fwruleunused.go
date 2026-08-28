// SPDX-License-Identifier: Apache-2.0

// health_fwruleunused.go implements docs/features/monitoring.md §5's
// "fw_rule_unused" health check (T-1006): one informational finding per
// enabled firewall rule — reachable in some known guest's current resolved
// evaluation order — that has recorded zero hits in the configurable N-day
// window (default 30, DefaultFwRuleUnusedWindow), closing the loop between
// firewall editing and reality ("this rule hasn't matched anything in N
// days"). Read-only aggregation over internal/fwlog's own bounded ring
// buffer (internal/fwlog.Analyze, reused verbatim — no new correlation
// logic); source is the existing "health" enum value (this is a
// structural/staleness check over firewall state, not a new kind of
// producer), severity info, never fixable (deleting a rule is a judgment
// call, not a computable patch).
//
// Hysteresis-exempt (mgmt_single_path/trunk_unused_vlans-style): "zero
// hits in the last N days" is itself already a slow, N-day-smoothed
// signal — there is nothing left to further debounce, and AC3 requires it
// to clear the very next poll cycle once the rule records a hit (or is
// deleted), not several cycles later.

package findings

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/bgovanlu/vnprox/internal/fwlog"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

const CheckFwRuleUnused = "fw_rule_unused"

// DefaultFwRuleUnusedWindow is the finding's own default "N days" — a
// deliberately different (and much longer) default than GET
// /firewall/analytics's own windowHours query param, which callers choose
// per request; this check always uses its own fixed default, independent
// of whatever a UI client happens to be viewing.
const DefaultFwRuleUnusedWindow = 30 * 24 * time.Hour

// FwAnalyticsProvider is the seam checkFwRuleUnused needs: T-1006's
// read-only firewall log analytics (fwlog.Service.Analytics). No context
// parameter, mirroring MgmtProvider/CorosyncProvider's context-free shape
// (Engine's own healthFindings cycle has no request context to thread
// through). *fwlog.Service satisfies this directly (structural typing,
// the same "no adapter needed" precedent internal/fwlog.Service's own
// `var _ PeerSource = (*peer.Client)(nil)` establishes) — cmd/vnproxd
// wires it in as-is.
type FwAnalyticsProvider interface {
	Analytics(now time.Time, window time.Duration, topN int) fwlog.Analytics
}

// checkFwRuleUnused evaluates svc's current unused-rule report (over
// DefaultFwRuleUnusedWindow) and returns one finding per unused rule. A
// nil provider (not wired) yields no findings — detection-only, same
// "quietly absent" degradation docs/api.md documents for every other
// optional producer input.
func checkFwRuleUnused(svc FwAnalyticsProvider, now time.Time) []Finding {
	if svc == nil {
		return nil
	}
	analytics := svc.Analytics(now, DefaultFwRuleUnusedWindow, fwlog.DefaultTopN)

	var out []Finding
	for _, u := range analytics.UnusedRules {
		out = append(out, fwRuleUnusedFinding(u))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// fwRuleUnusedFinding builds one Finding from a fwlog.UnusedRule. The
// finding's stable ID is derived from the rule's full identity (guestRef +
// origin + groupName + pos, not just guestRef alone) — "one finding per
// enabled rule", not one per guest — so newHealthFinding's own
// refs-or-nodes-only key scheme (which would collide across multiple
// unused rules on the same guest) is deliberately not used here; Refs
// still names just the guest ref (a real, map-linkable inventory Ref) so
// the topology's finding-badge overlay outlines the right guest.
func fwRuleUnusedFinding(u fwlog.UnusedRule) Finding {
	nodes := fwRuleUnusedNodes(u.Rule.GuestRef)
	windowDays := int(DefaultFwRuleUnusedWindow.Hours() / 24)

	var detail string
	if u.DaysSinceLastHit < 0 {
		detail = fmt.Sprintf(
			"rule at position %d (%s origin) on %s has recorded no hits in the retained firewall log — no match in at least the last %d days",
			u.Rule.Pos, u.Rule.Origin, u.Rule.GuestRef, windowDays,
		)
	} else {
		detail = fmt.Sprintf(
			"rule at position %d (%s origin) on %s has recorded no hits in %d days (threshold: %d)",
			u.Rule.Pos, u.Rule.Origin, u.Rule.GuestRef, u.DaysSinceLastHit, windowDays,
		)
	}

	id := fmt.Sprintf("health:%s|%s|%s|%d", CheckFwRuleUnused, u.Rule.GuestRef, u.Rule.Origin, u.Rule.Pos)
	if u.Rule.GroupName != "" {
		id += "|" + u.Rule.GroupName
	}

	return Finding{
		ID:       id,
		Source:   SourceHealth,
		Check:    CheckFwRuleUnused,
		Severity: SeverityInfo,
		Detail:   detail,
		DocsLink: fwRuleUnusedDeepLink(u.Rule),
		Nodes:    nodes,
		Refs:     []string{u.Rule.GuestRef},
		Fixable:  false,
	}
}

// fwRuleUnusedNodes recovers the affected node from the rule's guest ref
// (always a real, known guest ref — see fwlog.LiveRuleRefs, which only
// ever enumerates snap.Guests' own keys). Always a non-nil slice: Finding.
// Nodes has no `omitempty`, so a nil here would serialize as JSON `null`
// (see probeDivergenceToFinding's identical guard in cmd/vnproxd/findings.go).
func fwRuleUnusedNodes(guestRef string) []string {
	ref, err := inventory.ParseRef(guestRef)
	if err != nil || ref.Node == "" {
		return []string{}
	}
	return []string{ref.Node}
}

// fwRuleUnusedDeepLink builds the UI deep link into T-502's rule editor at
// the exact rule position — deliberately not a docs page (this task's
// card: "a UI deep link into the rule editor", the same documented
// deviation from DocsLink's usual "docs page" contract T-806's
// simDivergenceDeepLink already established for source:"probe" findings).
// Mirrors web/src/fwlog/deeplink.ts's ruleDeepLinkPath / web/src/simulator/
// deeplink.ts's blockingRuleDeepLinkPath exactly (same query param names:
// scope, ref, pos, origin, group) — one established `/firewall` deep-link
// contract, not a fourth bespoke one.
func fwRuleUnusedDeepLink(rule fwlog.RuleRef) string {
	q := url.Values{}
	q.Set("scope", "guest")
	q.Set("ref", rule.GuestRef)
	q.Set("pos", strconv.Itoa(rule.Pos))
	q.Set("origin", rule.Origin)
	if rule.GroupName != "" {
		q.Set("group", rule.GroupName)
	}
	return "/firewall?" + q.Encode()
}
