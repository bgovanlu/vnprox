// SPDX-License-Identifier: Apache-2.0

package route

import (
	"reflect"
	"sort"
	"testing"
)

// pvecubeFIB/pvecubeRules reconstruct pvecube's actual parsed routing
// state (from the same evidence transcript fib_test.go/rules_test.go
// parse from raw JSON) directly as typed values, so Lookup's tests read
// against the same real-world table shape without re-parsing JSON in
// every test.
func pvecubeFIB() []FIBRoute {
	return []FIBRoute{
		{AFI: AFIv4, Table: "main", Type: "unicast", Dst: "0.0.0.0/0", Gateway: "192.168.1.1", Dev: "vmbr0"},
		{AFI: AFIv4, Table: "main", Type: "unicast", Dst: "10.99.0.0/24", Dev: "vmbr99", Protocol: "kernel", Scope: "link", PrefSrc: "10.99.0.1"},
		{AFI: AFIv4, Table: "main", Type: "unicast", Dst: "192.168.1.0/24", Dev: "vmbr0", Protocol: "kernel", Scope: "link", PrefSrc: "192.168.1.9"},
		{AFI: AFIv4, Table: "local", Type: "local", Dst: "10.99.0.1/32", Dev: "vmbr99", Protocol: "kernel", Scope: "host", PrefSrc: "10.99.0.1"},
		{AFI: AFIv4, Table: "local", Type: "local", Dst: "192.168.1.9/32", Dev: "vmbr0", Protocol: "kernel", Scope: "host", PrefSrc: "192.168.1.9"},
		{AFI: AFIv4, Table: "local", Type: "local", Dst: "127.0.0.0/8", Dev: "lo", Protocol: "kernel", Scope: "host", PrefSrc: "127.0.0.1"},
		{AFI: AFIv6, Table: "main", Type: "unicast", Dst: "fe80::/64", Dev: "vmbr0", Protocol: "kernel", Metric: 256, Pref: "medium"},
		{AFI: AFIv6, Table: "main", Type: "unicast", Dst: "fe80::/64", Dev: "vmbr2", Protocol: "kernel", Metric: 256, Pref: "medium"},
		{AFI: AFIv6, Table: "main", Type: "unicast", Dst: "fe80::/64", Dev: "vmbr99", Protocol: "kernel", Metric: 256, Pref: "medium"},
		{AFI: AFIv6, Table: "local", Type: "local", Dst: "::1/128", Dev: "lo", Protocol: "kernel", Pref: "medium"},
	}
}

func pvecubeRules() []PolicyRule {
	return []PolicyRule{
		{AFI: AFIv4, Priority: 0, Src: "all", Table: "local"},
		{AFI: AFIv4, Priority: 32766, Src: "all", Table: "main"},
		{AFI: AFIv4, Priority: 32767, Src: "all", Table: "default"},
		{AFI: AFIv6, Priority: 0, Src: "all", Table: "local"},
		{AFI: AFIv6, Priority: 32766, Src: "all", Table: "main"},
	}
}

func TestLookup_defaultRoute(t *testing.T) {
	// Matches this task's evidence transcript: `ip route get 8.8.8.8` ->
	// via 192.168.1.1 dev vmbr0.
	res, err := Lookup(pvecubeFIB(), pvecubeRules(), "8.8.8.8", "")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !res.Reachable || res.MatchedRoute == nil {
		t.Fatalf("Lookup(8.8.8.8) = %+v, want reachable via the default route", res)
	}
	if res.MatchedRoute.Dev != "vmbr0" || res.MatchedRoute.Gateway != "192.168.1.1" {
		t.Errorf("matched route = %+v, want dev vmbr0 gateway 192.168.1.1", res.MatchedRoute)
	}
	if res.MatchedRule == nil || res.MatchedRule.Table != "main" {
		t.Errorf("matched rule = %+v, want table main", res.MatchedRule)
	}
}

func TestLookup_connectedRoute(t *testing.T) {
	// Matches the evidence transcript's `ip route get 10.99.0.5` -> dev
	// vmbr99, no gateway (on-link).
	res, err := Lookup(pvecubeFIB(), pvecubeRules(), "10.99.0.5", "")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !res.Reachable || res.MatchedRoute.Dev != "vmbr99" || res.MatchedRoute.Gateway != "" {
		t.Errorf("Lookup(10.99.0.5) = %+v", res)
	}
	if res.MatchedRoute.Dst != "10.99.0.0/24" {
		t.Errorf("matched Dst = %q, want the /24, not a more specific route that doesn't exist", res.MatchedRoute.Dst)
	}
}

func TestLookup_ownHostAddress_hitsLocalTableFirst(t *testing.T) {
	// The kernel's priority-0 "lookup local" rule means a node's own
	// address resolves via the local table's "local" pseudo-route, not
	// main's connected network route — this is exactly why rule
	// evaluation order matters.
	res, err := Lookup(pvecubeFIB(), pvecubeRules(), "192.168.1.9", "")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !res.Reachable || res.MatchedRoute.Table != "local" || res.MatchedRoute.Type != "local" {
		t.Errorf("Lookup(own address) = %+v, want table local / type local", res)
	}
}

func TestLookup_linkLocal_ambiguousWithoutHint(t *testing.T) {
	// Matches the evidence transcript's finding that `ip route get
	// fe80::1` (no `dev`) is genuinely ambiguous across vmbr0/vmbr2/
	// vmbr99 — the real `ip` tool itself requires a `dev` here.
	res, err := Lookup(pvecubeFIB(), pvecubeRules(), "fe80::1", "")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.Reachable {
		t.Fatalf("Lookup(fe80::1, no hint) = %+v, want ambiguous (not reachable)", res)
	}
	want := []string{"vmbr0", "vmbr2", "vmbr99"}
	got := append([]string(nil), res.Ambiguous...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Ambiguous = %v, want %v", got, want)
	}
}

func TestLookup_linkLocal_resolvedWithHint(t *testing.T) {
	// Matches the evidence transcript's `ip -j -6 route get fe80::1 dev
	// vmbr0`.
	res, err := Lookup(pvecubeFIB(), pvecubeRules(), "fe80::1", "vmbr0")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !res.Reachable || res.MatchedRoute.Dev != "vmbr0" {
		t.Errorf("Lookup(fe80::1, dev vmbr0) = %+v", res)
	}
	if len(res.Ambiguous) != 0 {
		t.Errorf("Ambiguous = %v, want none once a hint disambiguates", res.Ambiguous)
	}
}

func TestLookup_unreachable(t *testing.T) {
	fib := []FIBRoute{
		{AFI: AFIv4, Table: "main", Dst: "10.0.0.0/24", Dev: "vmbr5"},
	}
	rules := []PolicyRule{{AFI: AFIv4, Priority: 32766, Src: "all", Table: "main"}}
	res, err := Lookup(fib, rules, "8.8.8.8", "")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.Reachable {
		t.Errorf("Lookup with no matching route = %+v, want unreachable", res)
	}
}

func TestLookup_sourceScopedRuleSkipped(t *testing.T) {
	fib := []FIBRoute{{AFI: AFIv4, Table: "200", Dst: "0.0.0.0/0", Gateway: "10.0.0.1", Dev: "vmbr5"}}
	rules := []PolicyRule{
		{AFI: AFIv4, Priority: 100, Src: "10.0.0.0/24", Table: "200"}, // source-scoped: not evaluated
		{AFI: AFIv4, Priority: 32766, Src: "all", Table: "main"},
	}
	res, err := Lookup(fib, rules, "8.8.8.8", "")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(res.RulesSkipped) != 1 {
		t.Fatalf("RulesSkipped = %v, want exactly the source-scoped rule", res.RulesSkipped)
	}
	if res.Reachable {
		t.Errorf("Lookup = %+v, want unreachable (table 200 was skipped, main has no matching route)", res)
	}
}

func TestLookup_invalidAddress(t *testing.T) {
	if _, err := Lookup(pvecubeFIB(), pvecubeRules(), "not-an-ip", ""); err == nil {
		t.Error("Lookup(invalid address) succeeded, want error")
	}
}

func TestLookup_ifaceHintFiltersCandidates(t *testing.T) {
	// A hint that names a device with no matching route at all correctly
	// reports unreachable rather than falling back to another device.
	res, err := Lookup(pvecubeFIB(), pvecubeRules(), "fe80::1", "vmbr7")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.Reachable {
		t.Errorf("Lookup(fe80::1, dev vmbr7 — not a candidate) = %+v, want unreachable", res)
	}
}
