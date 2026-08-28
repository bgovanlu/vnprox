// SPDX-License-Identifier: Apache-2.0

// analytics.go implements T-1006's firewall log analytics
// (docs/features/firewall.md §4, docs/features/monitoring.md §5): per-rule
// hit counts, top-N blocked sources/destinations, and an unused-rule
// report, all aggregated read-only over data this package already
// collects and retains (RingBuffer) — no new log-collection path, no new
// unbounded storage.
//
// Design note on rule identity across a move: Analyze does not trust each
// StreamEntry's own *cached* Correlation (computed once, at ingestion
// time, against whatever the resolved ruleset looked like at that
// instant — see Service.Tick). Instead it calls Correlate again, fresh,
// against `snap`'s *current* firewall state for every entry. This is the
// mechanism behind "a moved/renumbered rule's history follows it by
// identity, not position" (this task's card): an entry's own
// direction/action/proto/port fields never change after the fact, so
// re-resolving always finds whichever rule *currently* matches them, at
// its *current* RuleRef.Pos — not the position frozen into a stale cached
// Correlation from before a fw.rule.move. No new correlation logic is
// added; Correlate itself is reused verbatim, just invoked again at query
// time instead of (only) at ingestion time.

package fwlog

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Tunable defaults for GET /firewall/analytics and the fw_rule_unused
// health check. docs/development.md does not pin numeric values for this
// feature; DefaultAnalyticsWindow matches this task's card ("per-rule hit
// counts over a configurable window (default 24h)"), DefaultUnusedWindow
// matches the fw_rule_unused finding's own default ("zero hits in N days
// (default 30)").
const (
	DefaultAnalyticsWindow = 24 * time.Hour
	DefaultUnusedWindow    = 30 * 24 * time.Hour
	DefaultTopN            = 10
)

// RuleHitCount is one rule's observed hit count within Analyze's window,
// plus the most recent hit's timestamp.
type RuleHitCount struct {
	LastSeenAt time.Time
	Rule       RuleRef
	Hits       int
}

// EndpointCount is one source/destination address's occurrence count among
// DROP/REJECT-actioned entries within Analyze's window.
type EndpointCount struct {
	Value string
	Count int
}

// TopBlocked ranks the most frequently blocked (DROP/REJECT) sources and
// destinations within Analyze's window, regardless of whether the
// underlying line correlated to a specific rule — an ambiguous/unmatched
// DROP line still names a real blocked address.
type TopBlocked struct {
	Sources      []EndpointCount
	Destinations []EndpointCount
}

// UnusedRule is one enabled rule — reachable in some known guest's current
// resolved evaluation order — with zero hits within Analyze's window.
// DaysSinceLastHit is the whole number of days since its most recent
// observed hit anywhere in the retained buffer (which may itself predate
// the window); -1 means this rule has no observed hit anywhere in the
// currently-retained buffer at all (its true history, if any, may simply
// have rotated out of the bounded ring — an honest "don't know", not a
// claimed zero).
type UnusedRule struct {
	Rule             RuleRef
	DaysSinceLastHit int
}

// Analytics is Analyze's result: docs/api.md's GET /firewall/analytics
// response shape (via internal/api's JSON view).
type Analytics struct {
	TopBlocked  TopBlocked
	HitCounts   []RuleHitCount
	UnusedRules []UnusedRule
}

// EnabledRuleRefs returns the RuleRef identity of every enabled rule in
// resolved's evaluation order (fw.Resolve's own output — no new resolution
// logic). This is the same RuleRef shape Correlate already produces, so a
// caller can join Analyze's output against "every currently-live rule" by
// plain equality.
func EnabledRuleRefs(resolved fw.ResolvedView) []RuleRef {
	var out []RuleRef
	for _, rr := range resolved.Rules {
		if !rr.Rule.Enabled {
			continue
		}
		out = append(out, RuleRef{
			GuestRef:  resolved.Guest.String(),
			Origin:    string(rr.Origin),
			GroupName: rr.GroupName,
			Pos:       rr.Pos,
		})
	}
	return out
}

// LiveRuleRefs returns the RuleRef identity of every enabled rule reachable
// in any currently-known guest's resolved evaluation order — the full set
// UnusedRules is evaluated against. A guest whose resolve errors (should
// only happen for a malformed ref) is skipped rather than failing the
// whole computation, mirroring Service.correlate's own degrade-on-error
// behavior.
func LiveRuleRefs(snap fw.Snapshot) []RuleRef {
	var out []RuleRef
	for guestRef := range snap.Guests {
		resolved, err := fw.Resolve(snap, guestRef)
		if err != nil {
			continue
		}
		out = append(out, EnabledRuleRefs(resolved)...)
	}
	sort.Slice(out, func(i, j int) bool { return ruleRefLess(out[i], out[j]) })
	return out
}

// Analyze aggregates entries (typically a RingBuffer.Snapshot()) into
// Analytics, evaluated at now: hit counts and top-blocked rankings over the
// trailing `window`, and an unused-rule report — every enabled rule
// currently reachable in some known guest's resolved view (LiveRuleRefs)
// with zero hits within that same `window` — per this task's card ("one
// finding per enabled rule with zero hits in N days", where N is whatever
// window the caller passes; the fw_rule_unused health check and GET
// /firewall/analytics's windowHours param each choose their own).
//
// Entries without a parsed timestamp (Entry.HasTimestamp false) are
// excluded from every window-bound computation — there is no honest way to
// know whether an un-timestamped line falls inside or outside the window.
// A zero/negative window or topN falls back to this package's documented
// defaults.
func Analyze(entries []StreamEntry, snap fw.Snapshot, now time.Time, window time.Duration, topN int) Analytics {
	if window <= 0 {
		window = DefaultAnalyticsWindow
	}
	if topN <= 0 {
		topN = DefaultTopN
	}
	windowStart := now.Add(-window)

	resolvedCache := map[inventory.Ref]fw.ResolvedView{}
	hitCounts := map[RuleRef]*RuleHitCount{}
	lastSeenEver := map[RuleRef]time.Time{}
	sources := map[string]int{}
	dests := map[string]int{}

	for _, se := range entries {
		e := se.Entry
		if !e.HasTimestamp || e.Timestamp.After(now) {
			continue
		}
		inWindow := !e.Timestamp.Before(windowStart)

		if inWindow {
			action := strings.ToUpper(e.Action)
			if action == "DROP" || action == "REJECT" {
				if e.Source != "" {
					sources[e.Source]++
				}
				if e.Dest != "" {
					dests[e.Dest]++
				}
			}
		}

		if !e.Guest {
			continue
		}
		guestRef := inventory.Ref{Kind: inventory.KindGuest, Node: e.Node, ID: strconv.Itoa(e.VMID)}
		resolved, ok := resolvedCache[guestRef]
		if !ok {
			rv, err := fw.Resolve(snap, guestRef)
			if err != nil {
				continue
			}
			resolvedCache[guestRef] = rv
			resolved = rv
		}
		corr := Correlate(e, resolved)
		if corr.Status != StatusRule || corr.Rule == nil {
			continue
		}
		ref := *corr.Rule

		if t, seen := lastSeenEver[ref]; !seen || e.Timestamp.After(t) {
			lastSeenEver[ref] = e.Timestamp
		}
		if !inWindow {
			continue
		}
		hc, ok := hitCounts[ref]
		if !ok {
			hc = &RuleHitCount{Rule: ref}
			hitCounts[ref] = hc
		}
		hc.Hits++
		if e.Timestamp.After(hc.LastSeenAt) {
			hc.LastSeenAt = e.Timestamp
		}
	}

	var out Analytics
	for _, hc := range hitCounts {
		out.HitCounts = append(out.HitCounts, *hc)
	}
	sort.Slice(out.HitCounts, func(i, j int) bool {
		if out.HitCounts[i].Hits != out.HitCounts[j].Hits {
			return out.HitCounts[i].Hits > out.HitCounts[j].Hits
		}
		return ruleRefLess(out.HitCounts[i].Rule, out.HitCounts[j].Rule)
	})

	out.TopBlocked = TopBlocked{
		Sources:      topEndpoints(sources, topN),
		Destinations: topEndpoints(dests, topN),
	}

	for _, ref := range LiveRuleRefs(snap) {
		last, seen := lastSeenEver[ref]
		if seen && !last.Before(windowStart) {
			continue // hit within the window: not unused
		}
		days := -1
		if seen {
			days = int(now.Sub(last).Hours() / 24)
		}
		out.UnusedRules = append(out.UnusedRules, UnusedRule{Rule: ref, DaysSinceLastHit: days})
	}
	sort.Slice(out.UnusedRules, func(i, j int) bool { return ruleRefLess(out.UnusedRules[i].Rule, out.UnusedRules[j].Rule) })

	return out
}

// Analytics computes T-1006's rule hit counts / top-blocked / unused-rule
// aggregation over this Service's current buffer and current firewall
// snapshot (s.cfg.Snapshot — the same live-graph seam Service.Tick's own
// ingestion-time correlation already reads), re-correlating every buffered
// entry fresh — see Analyze's doc comment for why. Safe to call
// concurrently with Run/Tick.
func (s *Service) Analytics(now time.Time, window time.Duration, topN int) Analytics {
	entries, _ := s.buf.Snapshot()
	var snap fw.Snapshot
	if s.cfg.Snapshot != nil {
		snap = s.cfg.Snapshot.FirewallSnapshot()
	}
	return Analyze(entries, snap, now, window, topN)
}

// ruleRefLess gives RuleRef a total, deterministic order for sorting
// (guestRef, then origin, then groupName, then pos).
func ruleRefLess(a, b RuleRef) bool {
	if a.GuestRef != b.GuestRef {
		return a.GuestRef < b.GuestRef
	}
	if a.Origin != b.Origin {
		return a.Origin < b.Origin
	}
	if a.GroupName != b.GroupName {
		return a.GroupName < b.GroupName
	}
	return a.Pos < b.Pos
}

// topEndpoints ranks counts descending (ties broken alphabetically for
// determinism), capped at topN entries.
func topEndpoints(counts map[string]int, topN int) []EndpointCount {
	if len(counts) == 0 {
		return nil
	}
	out := make([]EndpointCount, 0, len(counts))
	for v, c := range counts {
		out = append(out, EndpointCount{Value: v, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	if len(out) > topN {
		out = out[:topN]
	}
	return out
}
