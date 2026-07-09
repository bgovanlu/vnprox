package inventory

import "testing"

// TestRefRoundTrip covers acceptance criterion #5: encoding then decoding a
// Ref recovers the exact original for every kind, including cluster-scoped
// (empty node) refs and IDs containing '/' and ':'.
func TestRefRoundTrip(t *testing.T) {
	cases := []Ref{
		{Kind: KindNode, Node: "pve1", ID: "pve1"},
		{Kind: KindPhysNic, Node: "pve1", ID: "eno1"},
		{Kind: KindPhysNic, Node: "pve-2.dc", ID: "enp3s0f0"},
		{Kind: KindBond, Node: "pve1", ID: "bond0"},
		{Kind: KindBridge, Node: "pve1", ID: "vmbr0"},
		{Kind: KindVlan, Node: "pve1", ID: "vmbr0.100"},
		{Kind: KindOVSBridge, Node: "pve1", ID: "vmbr1"},
		{Kind: KindOVSBond, Node: "pve1", ID: "bond1"},
		// cluster-scoped: empty node.
		{Kind: KindSDNZone, Node: "", ID: "zone1"},
		// ID containing '/' — the documented sdn-vnet::zone1/vnet1 example.
		{Kind: KindSDNVnet, Node: "", ID: "zone1/vnet1"},
		// ID containing '/' (IPv4 CIDR).
		{Kind: KindSDNSubnet, Node: "", ID: "10.20.0.0/24"},
		// ID containing both ':' and '/' (IPv6 CIDR).
		{Kind: KindSDNSubnet, Node: "", ID: "2001:db8:abcd::/48"},
		{Kind: KindGuest, Node: "pve1", ID: "100"},
		{Kind: KindGuestNic, Node: "pve1", ID: "100/net0"},
		{Kind: KindLldpNeighbor, Node: "pve1", ID: "eno1/00:11:22:33:44:55/Gi0/1"},
		{Kind: KindFwRuleset, Node: "", ID: "cluster"},
		{Kind: KindFwRuleset, Node: "pve1", ID: "node"},
		// pathological but must still round-trip: empty id, id with trailing colon.
		{Kind: KindBridge, Node: "pve1", ID: ""},
		{Kind: KindGuestNic, Node: "", ID: "a:b:c/d"},
	}
	for _, want := range cases {
		enc := want.String()
		got, err := ParseRef(enc)
		if err != nil {
			t.Errorf("ParseRef(%q) error: %v", enc, err)
			continue
		}
		if got != want {
			t.Errorf("round-trip mismatch: %q -> %q -> %#v, want %#v", want, enc, got, want)
		}
	}
}

// TestParseRefErrors checks malformed inputs are rejected.
func TestParseRefErrors(t *testing.T) {
	for _, s := range []string{
		"",               // empty
		"bridge",         // no colons
		"bridge:pve1",    // one colon
		"bogus:pve1:x",   // unknown kind
		":pve1:vmbr0",    // empty kind
		"BRIDGE:p:vmbr0", // case-sensitive kind mismatch
	} {
		if _, err := ParseRef(s); err == nil {
			t.Errorf("ParseRef(%q) = nil error, want error", s)
		}
	}
}

// TestRefEncodingExample pins the exact documented encoding form.
func TestRefEncodingExample(t *testing.T) {
	r := Ref{Kind: KindSDNVnet, Node: "", ID: "zone1/vnet1"}
	if got := r.String(); got != "sdn-vnet::zone1/vnet1" {
		t.Errorf("encoding = %q, want sdn-vnet::zone1/vnet1", got)
	}
	if !r.ClusterScoped() {
		t.Error("expected cluster-scoped ref")
	}
}
