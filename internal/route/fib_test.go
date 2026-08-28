// SPDX-License-Identifier: Apache-2.0

package route

import "testing"

// pvecubeRouteTableV4 is `ip -j route show table all`'s exact output on
// pvecube, captured in
// planning/reports/evidence/pve-9.2.4-routing-2026-08-28.txt.
const pvecubeRouteTableV4 = `[{"dst":"default","gateway":"192.168.1.1","dev":"vmbr0","flags":[]},
{"dst":"10.99.0.0/24","dev":"vmbr99","protocol":"kernel","scope":"link","prefsrc":"10.99.0.1","flags":[]},
{"dst":"192.168.1.0/24","dev":"vmbr0","protocol":"kernel","scope":"link","prefsrc":"192.168.1.9","flags":[]},
{"type":"local","dst":"10.99.0.1","dev":"vmbr99","table":"local","protocol":"kernel","scope":"host","prefsrc":"10.99.0.1","flags":[]},
{"type":"broadcast","dst":"10.99.0.255","dev":"vmbr99","table":"local","protocol":"kernel","scope":"link","prefsrc":"10.99.0.1","flags":[]},
{"type":"local","dst":"127.0.0.0/8","dev":"lo","table":"local","protocol":"kernel","scope":"host","prefsrc":"127.0.0.1","flags":[]}]`

const pvecubeRouteTableV6 = `[{"dst":"fe80::/64","dev":"vmbr0","protocol":"kernel","metric":256,"flags":[],"pref":"medium"},
{"dst":"fe80::/64","dev":"vmbr2","protocol":"kernel","metric":256,"flags":[],"pref":"medium"},
{"dst":"fe80::/64","dev":"vmbr99","protocol":"kernel","metric":256,"flags":[],"pref":"medium"},
{"type":"local","dst":"::1","dev":"lo","table":"local","protocol":"kernel","metric":0,"flags":[],"pref":"medium"},
{"type":"multicast","dst":"ff00::/8","dev":"vmbr0","table":"local","protocol":"kernel","metric":256,"flags":[],"pref":"medium"}]`

func TestParseFIBRoutes_pvecubeEvidence(t *testing.T) {
	v4, err := ParseFIBRoutes([]byte(pvecubeRouteTableV4), AFIv4)
	if err != nil {
		t.Fatalf("ParseFIBRoutes(v4): %v", err)
	}
	if len(v4) != 6 {
		t.Fatalf("got %d v4 routes, want 6", len(v4))
	}

	def := v4[0]
	if def.Dst != "0.0.0.0/0" {
		t.Errorf("default route Dst = %q, want 0.0.0.0/0 (normalized from iproute2's \"default\" keyword)", def.Dst)
	}
	if def.Gateway != "192.168.1.1" || def.Dev != "vmbr0" {
		t.Errorf("default route = %+v, want gateway 192.168.1.1 dev vmbr0", def)
	}
	if def.Table != "main" {
		t.Errorf("default route Table = %q, want \"main\" (absent `table` key defaults to main)", def.Table)
	}
	if def.Type != "unicast" {
		t.Errorf("default route Type = %q, want \"unicast\" (absent `type` key defaults to unicast)", def.Type)
	}

	connected := v4[1]
	if connected.Dst != "10.99.0.0/24" || connected.PrefSrc != "10.99.0.1" || connected.Gateway != "" {
		t.Errorf("connected route = %+v, want dst 10.99.0.0/24 prefsrc 10.99.0.1 no gateway", connected)
	}

	local := v4[3]
	if local.Table != "local" || local.Type != "local" {
		t.Errorf("local route = %+v, want table/type local", local)
	}
	if local.Dst != "10.99.0.1/32" {
		t.Errorf("local route Dst = %q, want 10.99.0.1/32 (bare host address given the full v4 prefix length)", local.Dst)
	}

	v6, err := ParseFIBRoutes([]byte(pvecubeRouteTableV6), AFIv6)
	if err != nil {
		t.Fatalf("ParseFIBRoutes(v6): %v", err)
	}
	if len(v6) != 5 {
		t.Fatalf("got %d v6 routes, want 5", len(v6))
	}
	if v6[0].Pref != "medium" {
		t.Errorf("v6 connected route Pref = %q, want \"medium\" (v6-only RFC4191 preference field)", v6[0].Pref)
	}
	if v6[3].Dst != "::1/128" {
		t.Errorf("v6 local route Dst = %q, want ::1/128 (bare host address given the full v6 prefix length)", v6[3].Dst)
	}
}

func TestParseFIBRoutes_emptyInput(t *testing.T) {
	routes, err := ParseFIBRoutes(nil, AFIv4)
	if err != nil || routes != nil {
		t.Fatalf("ParseFIBRoutes(nil) = %v, %v; want nil, nil", routes, err)
	}
	routes, err = ParseFIBRoutes([]byte("   "), AFIv4)
	if err != nil || routes != nil {
		t.Fatalf("ParseFIBRoutes(whitespace) = %v, %v; want nil, nil", routes, err)
	}
}

func TestParseFIBRoutes_malformed(t *testing.T) {
	for _, raw := range []string{`{"not":"an array"}`, `[{"dst": 5}]`, `not json at all`} {
		if _, err := ParseFIBRoutes([]byte(raw), AFIv4); err == nil {
			t.Errorf("ParseFIBRoutes(%q) succeeded, want error", raw)
		}
	}
}

func TestNormalizeDst(t *testing.T) {
	cases := []struct {
		dst, afi, want string
	}{
		{"default", "ipv4", "0.0.0.0/0"},
		{"default", "ipv6", "::/0"},
		{"192.168.1.0/24", "ipv4", "192.168.1.0/24"},
		{"10.99.0.1", "ipv4", "10.99.0.1/32"},
		{"fe80::1", "ipv6", "fe80::1/128"},
		{"fe80::/64", "ipv6", "fe80::/64"},
		{"", "ipv4", ""},
	}
	for _, c := range cases {
		got := normalizeDst(c.dst, AFI(c.afi))
		if got != c.want {
			t.Errorf("normalizeDst(%q, %s) = %q, want %q", c.dst, c.afi, got, c.want)
		}
	}
}
