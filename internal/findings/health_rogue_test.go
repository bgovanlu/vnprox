// SPDX-License-Identifier: Apache-2.0

package findings_test

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// fakeRogue is a static RogueProvider returning a fixed scan — the fixture
// seam T-1605's checks are exercised against, standing in for the production
// rogueScanAdapter over internal/neighbor / the DHCP config view / T-1404's
// RA feed.
type fakeRogue struct{ scan findings.RogueScan }

func (f fakeRogue) RogueScan() findings.RogueScan { return f.scan }

// mutableRogue is a RogueProvider whose scan can be swapped between cycles —
// used by the multi-cycle arp_spoof tests, which must keep the same *Engine
// (and thus its churn tracker) across cycles while varying the neighbor scan.
type mutableRogue struct{ scan findings.RogueScan }

func (m *mutableRogue) RogueScan() findings.RogueScan { return m.scan }

// rogueByCheck filters findings to source "rogue" and the given check.
func rogueByCheck(t *testing.T, fs []findings.Finding, check string) []findings.Finding {
	t.Helper()
	var out []findings.Finding
	for _, f := range fs {
		if f.Source == findings.SourceRogue && f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

// --- rogue_dhcp_server (AC1) ---------------------------------------------

func TestRogueDHCPServer_FiresOncePerRogueOffer(t *testing.T) {
	legit := "aa:bb:cc:00:00:01"
	scan := findings.RogueScan{
		LegitDHCPServerMACs: []string{legit},
		DHCPOffers: []findings.DHCPServerObservation{
			// The subnet's own configured owner — must never fire.
			{MAC: legit, IP: "10.0.0.1", Iface: "vmbr0", Node: "pve1", SubnetCIDR: "10.0.0.0/24"},
			// A rogue server — must fire (twice observed, one finding).
			{MAC: "de:ad:be:ef:00:99", IP: "10.0.0.66", Iface: "vmbr0", Node: "pve1", SubnetCIDR: "10.0.0.0/24"},
			{MAC: "de:ad:be:ef:00:99", IP: "10.0.0.66", Iface: "vmbr0", Node: "pve1", SubnetCIDR: "10.0.0.0/24"},
		},
	}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Rogue: fakeRogue{scan}})
	got := rogueByCheck(t, eng.Findings(), findings.CheckRogueDHCPServer)
	if len(got) != 1 {
		t.Fatalf("got %d rogue_dhcp_server findings, want 1: %+v", len(got), got)
	}
	f := got[0]
	if f.Severity != findings.SeverityError {
		t.Errorf("severity = %q, want error", f.Severity)
	}
	if f.Fixable {
		t.Error("rogue_dhcp_server must not be fixable")
	}
	if f.DocsLink == "" {
		t.Error("rogue_dhcp_server must carry a DocsLink")
	}
	// refs names the offending MAC/interface, not the whole subnet (AC1).
	if !containsStr(f.Refs, "de:ad:be:ef:00:99") {
		t.Errorf("refs = %v, want offending MAC", f.Refs)
	}
	if containsStr(f.Refs, "10.0.0.0/24") {
		t.Errorf("refs = %v must not name the whole subnet", f.Refs)
	}
}

func TestRogueDHCPServer_CleanFixtureSilent(t *testing.T) {
	legit := "aa:bb:cc:00:00:01"
	scan := findings.RogueScan{
		LegitDHCPServerMACs: []string{legit},
		DHCPOffers: []findings.DHCPServerObservation{
			{MAC: legit, IP: "10.0.0.1", Iface: "vmbr0", Node: "pve1", SubnetCIDR: "10.0.0.0/24"},
		},
	}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Rogue: fakeRogue{scan}})
	if got := rogueByCheck(t, eng.Findings(), findings.CheckRogueDHCPServer); len(got) != 0 {
		t.Fatalf("clean DHCP fixture fired %d rogue findings, want 0: %+v", len(got), got)
	}
}

// --- unexpected_ra (AC2) --------------------------------------------------

func TestUnexpectedRA_NoOpWithoutFeed(t *testing.T) {
	// No RA feed wired (pre-T-1404): the check must never fire and never error.
	scan := findings.RogueScan{RAs: nil, LegitRASourceMACs: []string{"aa:bb:cc:00:00:01"}}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Rogue: fakeRogue{scan}})
	if got := rogueByCheck(t, eng.Findings(), findings.CheckUnexpectedRA); len(got) != 0 {
		t.Fatalf("unexpected_ra fired %d findings against an empty RA feed, want 0 (documented no-op)", len(got))
	}
}

func TestUnexpectedRA_FiresOnStubbedUnexpectedSource(t *testing.T) {
	scan := findings.RogueScan{
		LegitRASourceMACs: []string{"aa:bb:cc:00:00:01"},
		RAs: []findings.RAObservation{
			// Known configured RA source — must not fire.
			{SourceMAC: "aa:bb:cc:00:00:01", SourceIP: "fe80::1", Iface: "vmbr0", Node: "pve1", SegmentRef: "bridge:pve1:vmbr0"},
			// Unexpected RA source — must fire.
			{SourceMAC: "de:ad:be:ef:00:aa", SourceIP: "fe80::666", Iface: "vmbr0", Node: "pve1", SegmentRef: "bridge:pve1:vmbr0"},
		},
	}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Rogue: fakeRogue{scan}})
	got := rogueByCheck(t, eng.Findings(), findings.CheckUnexpectedRA)
	if len(got) != 1 {
		t.Fatalf("got %d unexpected_ra findings, want 1: %+v", len(got), got)
	}
	if got[0].Severity != findings.SeverityError || got[0].Fixable {
		t.Errorf("unexpected_ra = %+v, want error/non-fixable", got[0])
	}
	if !containsStr(got[0].Refs, "de:ad:be:ef:00:aa") {
		t.Errorf("refs = %v, want offending RA source MAC", got[0].Refs)
	}
}

// --- arp_spoof_suspected (AC3) -------------------------------------------

func TestArpSpoofSuspected_FiresOnChurn(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mr := &mutableRogue{}
	eng := findings.New(findings.Config{
		Graph: newGraphWithNodes("pve1"),
		Rogue: mr,
		Now:   func() time.Time { return now },
	})
	// The same IP oscillating between two MACs each cycle (a spoofer answering
	// ARP alongside the real host) — crosses the churn threshold.
	macs := []string{"aa:aa:aa:aa:aa:aa", "bb:bb:bb:bb:bb:bb", "aa:aa:aa:aa:aa:aa", "bb:bb:bb:bb:bb:bb", "aa:aa:aa:aa:aa:aa"}
	var last []findings.Finding
	for _, mac := range macs {
		mr.scan = findings.RogueScan{Neighbors: []findings.NeighborObservation{
			{IP: "10.0.0.50", MAC: mac, Node: "pve1"},
		}}
		last = eng.Findings()
		now = now.Add(20 * time.Second)
	}
	got := rogueByCheck(t, last, findings.CheckArpSpoofSuspected)
	if len(got) != 1 {
		t.Fatalf("got %d arp_spoof_suspected findings after oscillation, want 1: %+v", len(got), got)
	}
	if got[0].Severity != findings.SeverityError || got[0].Fixable {
		t.Errorf("arp_spoof = %+v, want error/non-fixable", got[0])
	}
	if !containsStr(got[0].Refs, "10.0.0.50") {
		t.Errorf("refs = %v, want the churning IP", got[0].Refs)
	}
}

func TestArpSpoofSuspected_DHCPRenewalDoesNotFire(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mr := &mutableRogue{}
	eng := findings.New(findings.Config{
		Graph: newGraphWithNodes("pve1"),
		Rogue: mr,
		Now:   func() time.Time { return now },
	})
	// A single DHCP-renewal reassignment: IP moves from one MAC to another
	// once, then stays put. One change — never crosses the threshold.
	seq := []string{"aa:aa:aa:aa:aa:aa", "bb:bb:bb:bb:bb:bb", "bb:bb:bb:bb:bb:bb", "bb:bb:bb:bb:bb:bb"}
	var last []findings.Finding
	for _, mac := range seq {
		mr.scan = findings.RogueScan{Neighbors: []findings.NeighborObservation{
			{IP: "10.0.0.50", MAC: mac, Node: "pve1"},
		}}
		last = eng.Findings()
		now = now.Add(20 * time.Second)
	}
	if got := rogueByCheck(t, last, findings.CheckArpSpoofSuspected); len(got) != 0 {
		t.Fatalf("DHCP-renewal reassignment fired %d arp_spoof findings, want 0: %+v", len(got), got)
	}
}

// --- unknown_mac_protected_segment (AC4) ----------------------------------

func TestUnknownMacProtectedSegment_FiresForUnknownNotKnown(t *testing.T) {
	g := newGraphWithNodes("pve1")
	knownMAC := "aa:bb:cc:11:22:33"
	unknownMAC := "de:ad:be:ef:44:55"
	// A guest NIC gives us the known MAC.
	applyGuestNic(g, "pve1", 100, "net0", knownMAC)
	// The protected bridge learned both a known and an unknown MAC.
	applyBridgeWithFDB(g, "pve1", "vmbr0", []inventory.FDBEntry{
		{Mac: knownMAC, Vlan: 0},
		{Mac: unknownMAC, Vlan: 0},
	})

	eng := findings.New(findings.Config{Graph: g, ProtectedSegments: []string{"vmbr0"}})
	got := rogueByCheck(t, eng.Findings(), findings.CheckUnknownMacProtectedSegment)
	if len(got) != 1 {
		t.Fatalf("got %d unknown_mac findings, want exactly 1 (only the unknown MAC): %+v", len(got), got)
	}
	if !containsStr(got[0].Refs, unknownMAC) {
		t.Errorf("refs = %v, want the unknown MAC", got[0].Refs)
	}
	if containsStr(got[0].Refs, knownMAC) {
		t.Errorf("refs = %v must not name the known MAC", got[0].Refs)
	}
	if got[0].Severity != findings.SeverityError || got[0].Fixable {
		t.Errorf("unknown_mac = %+v, want error/non-fixable", got[0])
	}
}

func TestUnknownMacProtectedSegment_UnprotectedSegmentNeverFires(t *testing.T) {
	g := newGraphWithNodes("pve1")
	unknownMAC := "de:ad:be:ef:44:55"
	applyBridgeWithFDB(g, "pve1", "vmbr9", []inventory.FDBEntry{{Mac: unknownMAC}})

	// vmbr9 is not in the protected list — an unknown MAC on it must be ignored.
	eng := findings.New(findings.Config{Graph: g, ProtectedSegments: []string{"vmbr0"}})
	if got := rogueByCheck(t, eng.Findings(), findings.CheckUnknownMacProtectedSegment); len(got) != 0 {
		t.Fatalf("unknown MAC on an unprotected segment fired %d findings, want 0: %+v", len(got), got)
	}

	// An empty protected list disables the check entirely.
	eng2 := findings.New(findings.Config{Graph: g})
	if got := rogueByCheck(t, eng2.Findings(), findings.CheckUnknownMacProtectedSegment); len(got) != 0 {
		t.Fatalf("empty protected_segments still fired %d findings, want 0", len(got))
	}
}

// --- AC5: fixable:false + hysteresis-exempt (no debounce delay) -----------

func TestRogueChecksAreHysteresisExemptAndUnfixable(t *testing.T) {
	scan := findings.RogueScan{
		LegitDHCPServerMACs: []string{"aa:bb:cc:00:00:01"},
		DHCPOffers: []findings.DHCPServerObservation{
			{MAC: "de:ad:be:ef:00:99", IP: "10.0.0.66", Iface: "vmbr0", Node: "pve1", SubnetCIDR: "10.0.0.0/24"},
		},
		LegitRASourceMACs: []string{"aa:bb:cc:00:00:01"},
		RAs: []findings.RAObservation{
			{SourceMAC: "de:ad:be:ef:00:aa", SourceIP: "fe80::666", Iface: "vmbr0", Node: "pve1", SegmentRef: "bridge:pve1:vmbr0"},
		},
	}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Rogue: fakeRogue{scan}})

	// FIRST cycle: both feed-driven, immediately-observable checks must fire
	// with no rise-cycle delay (unlike the debounced health checks that need
	// 2 cycles). This is the hysteresis-exempt guarantee.
	first := eng.Findings()
	for _, check := range []string{findings.CheckRogueDHCPServer, findings.CheckUnexpectedRA} {
		got := rogueByCheck(t, first, check)
		if len(got) != 1 {
			t.Fatalf("%s did not fire on the FIRST cycle (want no debounce delay): got %d", check, len(got))
		}
		if got[0].Fixable {
			t.Errorf("%s must be fixable:false", check)
		}
		if got[0].DocsLink == "" {
			t.Errorf("%s must carry a DocsLink", check)
		}
	}
}

// TestRogue_ZeroWriteSurface (AC5) mirrors T-1206's zero-write-surface
// pattern: every rogue finding is fixable:false and has no computable fix
// (Engine.FixOps returns ok=false for it), and the producer's source file
// references neither internal/change nor any changeset-op construction — the
// card introduces no changeset ops and no write routes.
func TestRogue_ZeroWriteSurface(t *testing.T) {
	g := newGraphWithNodes("pve1")
	applyBridgeWithFDB(g, "pve1", "vmbr0", []inventory.FDBEntry{{Mac: "de:ad:be:ef:44:55"}})
	scan := findings.RogueScan{
		LegitDHCPServerMACs: []string{"aa:bb:cc:00:00:01"},
		DHCPOffers:          []findings.DHCPServerObservation{{MAC: "de:ad:be:ef:00:99", Iface: "vmbr0", Node: "pve1", SubnetCIDR: "10.0.0.0/24"}},
		LegitRASourceMACs:   []string{"aa:bb:cc:00:00:01"},
		RAs:                 []findings.RAObservation{{SourceMAC: "de:ad:be:ef:00:aa", Iface: "vmbr0", Node: "pve1", SegmentRef: "bridge:pve1:vmbr0"}},
	}
	eng := findings.New(findings.Config{Graph: g, Rogue: fakeRogue{scan}, ProtectedSegments: []string{"vmbr0"}})

	var rogueCount int
	for _, f := range eng.Findings() {
		if f.Source != findings.SourceRogue {
			continue
		}
		rogueCount++
		if f.Fixable {
			t.Errorf("rogue finding %s is fixable — this card introduces no fix path", f.ID)
		}
		if ops, _, ok := eng.FixOps(f.ID); ok || len(ops) != 0 {
			t.Errorf("rogue finding %s produced changeset ops (ok=%v, %d ops) — must have zero write surface", f.ID, ok, len(ops))
		}
	}
	if rogueCount < 3 {
		t.Fatalf("expected all four rogue checks exercisable, got only %d rogue findings", rogueCount)
	}

	// Source-level guard: the producer must not reach into the change engine.
	data, err := os.ReadFile("health_rogue.go")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, tok := range []string{"internal/change", "change.Op", "change.Service", "fw.rule.create", "RuleCreate"} {
		if strings.Contains(string(data), tok) {
			t.Errorf("health_rogue.go references %q — a detection-only producer must introduce no write/changeset surface", tok)
		}
	}
}

// --- test helpers ---------------------------------------------------------

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func applyGuestNic(g *inventory.Graph, node string, vmid int, key, mac string) {
	nic := &inventory.GuestNic{
		Ref:   inventory.Ref{Kind: inventory.KindGuestNic, Node: node, ID: key},
		Guest: inventory.Ref{Kind: inventory.KindGuest, Node: node, ID: strconv.Itoa(vmid)},
		Key:   key,
		Mac:   mac,
	}
	g.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindGuestNic}}, []inventory.Entity{nic})
}

func applyBridgeWithFDB(g *inventory.Graph, node, name string, fdb []inventory.FDBEntry) {
	br := &inventory.Bridge{
		Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name},
		Name: name,
		Virt: inventory.BridgeLinux,
		FDB:  fdb,
	}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBridge}}, []inventory.Entity{br})
}
