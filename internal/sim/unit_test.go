package sim

import (
	"net/netip"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

func mustAddr(s string) netip.Addr { a, _ := netip.ParseAddr(s); return a }

func TestParsePortToken(t *testing.T) {
	cases := []struct {
		in     string
		lo, hi int
		ok     bool
	}{
		{"80", 80, 80, true},
		{"80:90", 80, 90, true},
		{"90:80", 80, 90, true}, // swapped
		{"80-90", 80, 90, true}, // dash form
		{"xx", 0, 0, false},
		{"a:b", 0, 0, false},
		{"1:", 0, 0, false},
	}
	for _, c := range cases {
		lo, hi, ok := parsePortToken(c.in)
		if ok != c.ok || (ok && (lo != c.lo || hi != c.hi)) {
			t.Errorf("parsePortToken(%q) = (%d,%d,%v), want (%d,%d,%v)", c.in, lo, hi, ok, c.lo, c.hi, c.ok)
		}
	}
}

func TestMatchPort(t *testing.T) {
	set := flow{port: 443, portSet: true}
	unset := flow{portSet: false}
	cases := []struct {
		spec string
		fl   flow
		want matchState
	}{
		{"", set, matchYes},
		{"443", set, matchYes},
		{"80,443", set, matchYes},
		{"400:500", set, matchYes},
		{"80", set, matchNo},
		{"80", unset, matchUnknown},
		{"junk", set, matchUnknown},
	}
	for _, c := range cases {
		if got := matchPort(c.spec, c.fl).state; got != c.want {
			t.Errorf("matchPort(%q) = %v, want %v", c.spec, got, c.want)
		}
	}
}

func TestMatchProto(t *testing.T) {
	if matchProto("", "tcp") != matchYes {
		t.Error("empty rule proto should match")
	}
	if matchProto("tcp", "") != matchYes {
		t.Error("empty flow proto should wildcard")
	}
	if matchProto("TCP", "tcp") != matchYes {
		t.Error("proto match should be case-insensitive")
	}
	if matchProto("udp", "tcp") != matchNo {
		t.Error("tcp vs udp should not match")
	}
}

func TestParseCIDROrIP(t *testing.T) {
	if p, err := parseCIDROrIP("10.0.0.0/24"); err != nil || p.Bits() != 24 {
		t.Errorf("cidr parse: %v %v", p, err)
	}
	if p, err := parseCIDROrIP("10.0.0.5"); err != nil || p.Bits() != 32 {
		t.Errorf("ip parse: %v %v", p, err)
	}
	if _, err := parseCIDROrIP("nonsense"); err == nil {
		t.Error("expected error for nonsense")
	}
}

func TestMatchAddr(t *testing.T) {
	lk := fwLookup{
		aliases: map[string]inventory.FwAlias{
			"net":    {Name: "net", CIDR: "10.0.0.0/24"},
			"hostip": {Name: "hostip", CIDR: "10.0.0.7"}, // bare IP value
			"broken": {Name: "broken", CIDR: "not-an-ip"},
		},
		ipsets: map[string]inventory.FwIPSet{
			"set":    {Name: "set", Entries: []inventory.FwIPSetEntry{{CIDR: "10.0.0.0/24"}, {CIDR: "10.0.0.9/32", NoMatch: true}}},
			"broken": {Name: "broken", Entries: []inventory.FwIPSetEntry{{CIDR: "xx"}}},
		},
	}
	ip := mustAddr("10.0.0.7")
	cases := []struct {
		ip    netip.Addr
		field string
		want  matchState
		known bool
	}{
		{field: "", ip: ip, known: true, want: matchYes},
		{field: "10.0.0.0/24", ip: ip, known: true, want: matchYes},
		{field: "10.9.9.0/24", ip: ip, known: true, want: matchNo},
		{field: "10.0.0.7", ip: ip, known: true, want: matchYes},
		{field: "10.0.0.8", ip: ip, known: true, want: matchNo},
		{field: "10.0.0.0/24", ip: netip.Addr{}, known: false, want: matchUnknown}, // IP unknown
		{field: "net", ip: ip, known: true, want: matchYes},                        // alias CIDR
		{field: "hostip", ip: ip, known: true, want: matchYes},                     // alias single IP
		{field: "broken", ip: ip, known: true, want: matchUnknown},                 // alias bad value
		{field: "ghost", ip: ip, known: true, want: matchUnknown},                  // unknown token
		{field: "+set", ip: ip, known: true, want: matchYes},                       // ipset member
		{field: "+set", ip: mustAddr("10.0.0.9"), known: true, want: matchNo},      // nomatch exclusion
		{field: "+set", ip: mustAddr("10.1.0.1"), known: true, want: matchNo},      // not in set
		{field: "+ghost", ip: ip, known: true, want: matchUnknown},                 // ipset undefined
		{field: "+broken", ip: ip, known: true, want: matchUnknown},                // ipset bad entry
		{field: "bad/cidr/x", ip: ip, known: true, want: matchUnknown},             // unparseable CIDR
	}
	for _, c := range cases {
		if got := matchAddr(c.field, c.ip, c.known, lk, "source").state; got != c.want {
			t.Errorf("matchAddr(%q) = %v, want %v", c.field, got, c.want)
		}
	}
}

func TestMatchMacroICMP(t *testing.T) {
	pingRule := inventory.FwRule{Enabled: true, Direction: "in", Action: "ACCEPT", Macro: "Ping"}
	// Ping expands to an ICMP entry with no port.
	if r := matchRule(pingRule, flow{proto: "icmp"}, fwLookup{}); r.state != matchYes {
		t.Errorf("Ping macro vs icmp = %v, want yes", r.state)
	}
	if r := matchRule(pingRule, flow{proto: "tcp", port: 80, portSet: true}, fwLookup{}); r.state != matchNo {
		t.Errorf("Ping macro vs tcp/80 = %v, want no", r.state)
	}
}

func TestUplinkName(t *testing.T) {
	e := &Engine{}
	bondPort := &inventory.Bridge{Ports: []inventory.Ref{{Kind: inventory.KindBond, Node: "n", ID: "bond0"}}}
	if got := e.uplinkName(bondPort); got != "bond0" {
		t.Errorf("uplinkName bond = %q", got)
	}
	physPort := &inventory.Bridge{Ports: []inventory.Ref{{Kind: inventory.KindPhysNic, Node: "n", ID: "eno1"}}}
	if got := e.uplinkName(physPort); got != "eno1" {
		t.Errorf("uplinkName phys = %q", got)
	}
	noPort := &inventory.Bridge{}
	if got := e.uplinkName(noPort); got != "the uplink" {
		t.Errorf("uplinkName none = %q", got)
	}
}

func TestActionKind(t *testing.T) {
	if actionKind("ACCEPT") != decisionAllow {
		t.Error("ACCEPT")
	}
	if actionKind("DROP") != decisionDeny || actionKind("REJECT") != decisionDeny {
		t.Error("drop/reject")
	}
	if actionKind("JUMP") != decisionUnknown {
		t.Error("unknown action should be undecidable")
	}
}

func TestFirstExitNodeAndGates(t *testing.T) {
	if firstExitNode(nil) != "" {
		t.Error("nil zone")
	}
	if firstExitNode(&inventory.SdnZone{}) != "" {
		t.Error("no exit nodes")
	}
	if firstExitNode(&inventory.SdnZone{ExitNodes: []string{"pve3"}}) != "pve3" {
		t.Error("exit node")
	}
	if gatesDetail(nil) != "firewall disabled" {
		t.Error("empty gates")
	}
}

func TestHopHelpers(t *testing.T) {
	if h := hopForEndpoint(resolvedEP{kind: EndpointExternal}); h.Kind != "external" {
		t.Errorf("external hop = %+v", h)
	}
	if h := hopForEndpoint(resolvedEP{kind: EndpointIP, public: ResolvedEndpoint{IP: "1.2.3.4"}}); h.Label != "1.2.3.4" {
		t.Errorf("ip hop = %+v", h)
	}
	if h := hopForEndpoint(resolvedEP{kind: "weird"}); h.Label != "" {
		t.Errorf("unknown hop should be empty: %+v", h)
	}
	if h := hopForAttachment(resolvedEP{attach: attachNone}); h.Label != "" {
		t.Errorf("no-attach hop should be empty: %+v", h)
	}
	if _, ok := l2DomainKey(resolvedEP{attach: attachNone}); ok {
		t.Error("attachNone should have no L2 domain key")
	}
}

func TestFinalizeNilSlices(t *testing.T) {
	r := &Result{}
	finalize(r)
	if r.Hops == nil || r.Caveats == nil {
		t.Fatal("finalize must non-nil the slices")
	}
	if !hasCaveat(*r, CodeSimulated) {
		t.Error("finalize must add standing caveats")
	}
}

func TestBestGuestIPPrefersStatic(t *testing.T) {
	ref := inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "100/net0"}
	e := &Engine{guestIPs: map[inventory.Ref][]GuestIP{ref: {
		{IP: "bad-ip", Source: IPSourceStatic},   // unparseable, skipped
		{IP: "10.0.0.9", Source: IPSourceAgent},  // parseable, low rank
		{IP: "10.0.0.5", Source: IPSourceStatic}, // wins on rank
	}}}
	ip, src, ok := e.bestGuestIP(ref)
	if !ok || src != IPSourceStatic || ip.String() != "10.0.0.5" {
		t.Errorf("bestGuestIP = %v %v %v", ip, src, ok)
	}
	if _, _, ok := e.bestGuestIP(inventory.Ref{Kind: inventory.KindGuestNic}); ok {
		t.Error("no IPs should return not-ok")
	}
}

// TestVnetMissingZoneIndeterminate covers crossNodeVnet's nil-zone path
// (indeterminateVnet): a VNet whose zone is absent from inventory.
func TestVnetMissingZoneIndeterminate(t *testing.T) {
	w := newWorld()
	underlayNodes(w, "pve1", "pve2")
	// vnet references zone "ghostz" which is never defined.
	w.vnet("orphan", "ghostz", 0)
	w.subnet("10.5.0.0/24", "orphan", "10.5.0.1", false)
	w.guest("pve1", "100", "a")
	w.nic("pve1", "100", "net0", "orphan", 0, false)
	w.guest("pve2", "101", "b")
	w.nic("pve2", "101", "net0", "orphan", 0, false)
	res := Simulate(w.build(), Request{
		Src: guestEP(nicRef("pve1", "100")), Dst: guestEP(nicRef("pve2", "101")), Proto: "tcp", Port: 22,
	})
	if res.Verdict != VerdictIndeterminate {
		t.Fatalf("verdict = %q, want indeterminate; missing=%+v", res.Verdict, res.Missing)
	}
	if !hasCaveat(res, CodeNotEvaluated) {
		t.Errorf("expected not-evaluated caveat, have %s", caveatCodes(res))
	}
}

func TestContainsHelpers(t *testing.T) {
	if !contains([]string{"a", "b"}, "b") || contains([]string{"a"}, "z") {
		t.Error("contains")
	}
	if !containsInt([]int{1, 2}, 2) || containsInt([]int{1}, 9) {
		t.Error("containsInt")
	}
}
