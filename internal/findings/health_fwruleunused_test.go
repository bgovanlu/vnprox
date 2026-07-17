package findings_test

import (
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/fwlog"
)

// fakeFwAnalyticsProvider is a minimal findings.FwAnalyticsProvider stand-in
// — internal/fwlog's own analytics_test.go covers Analyze itself; here we
// only need to prove checkFwRuleUnused correctly turns a pre-computed
// fwlog.Analytics into findings.
type fakeFwAnalyticsProvider struct {
	analytics  fwlog.Analytics
	lastWindow time.Duration
}

func (f *fakeFwAnalyticsProvider) Analytics(_ time.Time, window time.Duration, _ int) fwlog.Analytics {
	f.lastWindow = window
	return f.analytics
}

func unusedRule(guestRef, origin string, pos, days int) fwlog.UnusedRule {
	return fwlog.UnusedRule{Rule: fwlog.RuleRef{GuestRef: guestRef, Origin: origin, Pos: pos}, DaysSinceLastHit: days}
}

// TestFwRuleUnused_OneFindingPerUnusedRule: AC3's core shape — source
// health, check fw_rule_unused, correct refs, info severity, never
// fixable.
func TestFwRuleUnused_OneFindingPerUnusedRule(t *testing.T) {
	provider := &fakeFwAnalyticsProvider{analytics: fwlog.Analytics{
		UnusedRules: []fwlog.UnusedRule{
			unusedRule("guest:pve1:200", "guest", 1, 40),
			unusedRule("guest:pve1:200", "cluster", 0, -1),
		},
	}}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), FwAnalytics: provider})

	found := findByCheck(t, eng.Findings(), findings.CheckFwRuleUnused)
	if len(found) != 2 {
		t.Fatalf("got %d fw_rule_unused findings, want 2: %+v", len(found), found)
	}
	for _, f := range found {
		if f.Source != findings.SourceHealth {
			t.Errorf("Source = %q, want %q (reuses the existing enum, no new value)", f.Source, findings.SourceHealth)
		}
		if f.Severity != findings.SeverityInfo {
			t.Errorf("Severity = %q, want info", f.Severity)
		}
		if f.Fixable {
			t.Error("fw_rule_unused must never be fixable")
		}
		if f.DocsLink == "" {
			t.Error("fw_rule_unused must carry a DocsLink (the rule-editor deep link)")
		}
		if !strings.HasPrefix(f.DocsLink, "/firewall?") {
			t.Errorf("DocsLink = %q, want a /firewall deep link, not a docs page", f.DocsLink)
		}
		if len(f.Refs) != 1 || f.Refs[0] != "guest:pve1:200" {
			t.Errorf("Refs = %v, want [guest:pve1:200]", f.Refs)
		}
		if len(f.Nodes) != 1 || f.Nodes[0] != "pve1" {
			t.Errorf("Nodes = %v, want [pve1]", f.Nodes)
		}
	}
	if found[0].ID == found[1].ID {
		t.Fatalf("two distinct rules on the same guest must not share an ID: %+v", found)
	}
}

// TestFwRuleUnused_DeepLinkTargetsExactRule: the DocsLink names the exact
// guest/origin/pos/group so it deep-links into the correct rule row.
func TestFwRuleUnused_DeepLinkTargetsExactRule(t *testing.T) {
	provider := &fakeFwAnalyticsProvider{analytics: fwlog.Analytics{
		UnusedRules: []fwlog.UnusedRule{unusedRule("guest:pve1:200", "guest", 3, 40)},
	}}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), FwAnalytics: provider})
	found := findByCheck(t, eng.Findings(), findings.CheckFwRuleUnused)
	if len(found) != 1 {
		t.Fatalf("got %d findings, want 1", len(found))
	}
	link := found[0].DocsLink
	for _, want := range []string{"scope=guest", "ref=guest%3Apve1%3A200", "pos=3", "origin=guest"} {
		if !strings.Contains(link, want) {
			t.Errorf("DocsLink = %q, want it to contain %q", link, want)
		}
	}
}

// TestFwRuleUnused_UsesItsOwnDefaultWindow_IndependentOfCallers: the check
// always evaluates over DefaultFwRuleUnusedWindow (30 days), not whatever
// windowHours a UI client might separately pass to GET
// /firewall/analytics.
func TestFwRuleUnused_UsesItsOwnDefaultWindow(t *testing.T) {
	provider := &fakeFwAnalyticsProvider{}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), FwAnalytics: provider})
	eng.Findings()
	if provider.lastWindow != findings.DefaultFwRuleUnusedWindow {
		t.Errorf("window passed to Analytics = %v, want %v", provider.lastWindow, findings.DefaultFwRuleUnusedWindow)
	}
}

// TestFwRuleUnused_ClearsWhenNoLongerUnused: AC3's "clears once the rule
// records a hit within the window (next poll cycle) or is deleted" — this
// check recomputes fresh every cycle (hysteresis-exempt), so a rule
// dropping out of the provider's UnusedRules list simply stops appearing.
func TestFwRuleUnused_ClearsWhenNoLongerUnused(t *testing.T) {
	provider := &fakeFwAnalyticsProvider{analytics: fwlog.Analytics{
		UnusedRules: []fwlog.UnusedRule{unusedRule("guest:pve1:200", "guest", 1, 40)},
	}}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), FwAnalytics: provider})
	if found := findByCheck(t, eng.Findings(), findings.CheckFwRuleUnused); len(found) != 1 {
		t.Fatalf("got %d findings, want 1", len(found))
	}

	provider.analytics = fwlog.Analytics{} // the rule recorded a hit (or was deleted)
	if found := findByCheck(t, eng.Findings(), findings.CheckFwRuleUnused); len(found) != 0 {
		t.Fatalf("got %d findings after the rule left UnusedRules, want 0: %+v", len(found), found)
	}
}

// TestFwRuleUnused_StableIDAcrossPolls: unchanged state reproduces the
// same finding ID.
func TestFwRuleUnused_StableIDAcrossPolls(t *testing.T) {
	provider := &fakeFwAnalyticsProvider{analytics: fwlog.Analytics{
		UnusedRules: []fwlog.UnusedRule{unusedRule("guest:pve1:200", "guest", 1, 40)},
	}}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), FwAnalytics: provider})
	first := findByCheck(t, eng.Findings(), findings.CheckFwRuleUnused)
	second := findByCheck(t, eng.Findings(), findings.CheckFwRuleUnused)
	if len(first) != 1 || len(second) != 1 || first[0].ID != second[0].ID {
		t.Fatalf("id not stable across polls: %+v vs %+v", first, second)
	}
}

// TestFwRuleUnused_NilProvider_NoFindings: not wired -> quietly absent.
func TestFwRuleUnused_NilProvider_NoFindings(t *testing.T) {
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1")})
	if found := findByCheck(t, eng.Findings(), findings.CheckFwRuleUnused); len(found) != 0 {
		t.Fatalf("nil FwAnalytics provider produced findings: %+v", found)
	}
}

// TestFwRuleUnused_NeverObservedRule_HonestDetail: DaysSinceLastHit == -1
// (never observed anywhere in the retained buffer) must not be fabricated
// as a specific day count.
func TestFwRuleUnused_NeverObservedRule_HonestDetail(t *testing.T) {
	provider := &fakeFwAnalyticsProvider{analytics: fwlog.Analytics{
		UnusedRules: []fwlog.UnusedRule{unusedRule("guest:pve1:200", "guest", 1, -1)},
	}}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), FwAnalytics: provider})
	found := findByCheck(t, eng.Findings(), findings.CheckFwRuleUnused)
	if len(found) != 1 {
		t.Fatalf("got %d findings, want 1", len(found))
	}
	if strings.Contains(found[0].Detail, "-1") {
		t.Errorf("detail must not leak the -1 sentinel verbatim: %q", found[0].Detail)
	}
}
