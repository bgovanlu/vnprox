// SPDX-License-Identifier: Apache-2.0

package route

import "testing"

// pvecubeRIBv4 is `vtysh -c "show ip route json"`'s exact output on
// pvecube (plain, non-`vrf all` shape — the shape internal/host's
// Real.FRRRIBV4 fetches day-to-day), captured in
// planning/reports/evidence/pve-9.2.4-routing-2026-08-28.txt.
const pvecubeRIBv4 = `{"0.0.0.0/0":[{"prefix":"0.0.0.0/0","prefixLen":0,"protocol":"kernel","vrfId":0,"vrfName":"default","selected":true,"destSelected":true,"distance":0,"metric":0,"installed":true,"table":254,"uptime":"6d08h28m","nexthops":[{"flags":3,"fib":true,"ip":"192.168.1.1","afi":"ipv4","interfaceIndex":6,"interfaceName":"vmbr0","active":true,"weight":1}]}]
,"192.168.1.0/24":[{"prefix":"192.168.1.0/24","prefixLen":24,"protocol":"connected","vrfId":0,"vrfName":"default","selected":true,"destSelected":true,"distance":0,"metric":0,"installed":true,"table":254,"uptime":"6d08h28m","nexthops":[{"flags":3,"fib":true,"directlyConnected":true,"interfaceIndex":6,"interfaceName":"vmbr0","active":true,"weight":1}]}]
}`

// pvecubeRIBv4VrfAll is the equivalent `vtysh -c "show ip route vrf all
// json"` shape — one extra nesting level, keyed by VRF name.
const pvecubeRIBv4VrfAll = `{"default":{"0.0.0.0/0":[{"prefix":"0.0.0.0/0","protocol":"kernel","vrfName":"default","selected":true,"installed":true,"nexthops":[{"ip":"192.168.1.1","interfaceName":"vmbr0","active":true,"fib":true,"weight":1}]}]}}`

func TestParseFRRRIB_pvecubeEvidence_plainShape(t *testing.T) {
	routes, err := ParseFRRRIB([]byte(pvecubeRIBv4), AFIv4)
	if err != nil {
		t.Fatalf("ParseFRRRIB: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("got %d routes, want 2: %+v", len(routes), routes)
	}

	byPrefix := map[string]RIBRoute{}
	for _, r := range routes {
		byPrefix[r.Prefix] = r
	}

	def, ok := byPrefix["0.0.0.0/0"]
	if !ok {
		t.Fatal("missing 0.0.0.0/0")
	}
	if def.Protocol != "kernel" || def.VRF != "default" || !def.Selected || !def.Installed {
		t.Errorf("default route = %+v", def)
	}
	if len(def.Nexthops) != 1 || def.Nexthops[0].IP != "192.168.1.1" || def.Nexthops[0].Interface != "vmbr0" || !def.Nexthops[0].FIB {
		t.Errorf("default route nexthops = %+v", def.Nexthops)
	}

	conn, ok := byPrefix["192.168.1.0/24"]
	if !ok {
		t.Fatal("missing 192.168.1.0/24")
	}
	if conn.Protocol != "connected" || len(conn.Nexthops) != 1 || !conn.Nexthops[0].DirectlyConnected {
		t.Errorf("connected route = %+v", conn)
	}
}

func TestParseFRRRIB_vrfAllShape(t *testing.T) {
	routes, err := ParseFRRRIB([]byte(pvecubeRIBv4VrfAll), AFIv4)
	if err != nil {
		t.Fatalf("ParseFRRRIB(vrf-all shape): %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1: %+v", len(routes), routes)
	}
	if routes[0].VRF != "default" || routes[0].Prefix != "0.0.0.0/0" {
		t.Errorf("route = %+v", routes[0])
	}
}

func TestParseFRRRIB_selectedInstalledDefaultFalseWhenAbsent(t *testing.T) {
	// Mirrors the fe80::/64 case in the evidence transcript: two of three
	// connected candidates for the same prefix carry neither `selected`
	// nor `installed` at all (FRR omits rather than emitting `false`).
	raw := `{"fe80::/64":[
		{"prefix":"fe80::/64","protocol":"connected","vrfName":"default","nexthops":[{"interfaceName":"vmbr2","directlyConnected":true}]},
		{"prefix":"fe80::/64","protocol":"connected","vrfName":"default","selected":true,"installed":true,"nexthops":[{"interfaceName":"vmbr0","directlyConnected":true}]}
	]}`
	routes, err := ParseFRRRIB([]byte(raw), AFIv6)
	if err != nil {
		t.Fatalf("ParseFRRRIB: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("got %d routes, want 2", len(routes))
	}
	if routes[0].Selected || routes[0].Installed {
		t.Errorf("route[0] = %+v, want Selected/Installed both false", routes[0])
	}
	if !routes[1].Selected || !routes[1].Installed {
		t.Errorf("route[1] = %+v, want Selected/Installed both true", routes[1])
	}
}

func TestParseFRRRIB_emptyInput(t *testing.T) {
	routes, err := ParseFRRRIB(nil, AFIv4)
	if err != nil || routes != nil {
		t.Fatalf("ParseFRRRIB(nil) = %v, %v; want nil, nil", routes, err)
	}
}

func TestParseFRRRIB_malformedTopLevel(t *testing.T) {
	if _, err := ParseFRRRIB([]byte(`["not", "an", "object"]`), AFIv4); err == nil {
		t.Error("ParseFRRRIB(array) succeeded, want error")
	}
}

func TestParseFRRRIB_oneMalformedPrefixSkipped(t *testing.T) {
	// A prefix whose value is neither an array-of-routes nor a
	// vrf-nested object (here, a bare string) is skipped rather than
	// failing the whole document.
	raw := `{"192.168.1.0/24":[{"prefix":"192.168.1.0/24","protocol":"connected","vrfName":"default","nexthops":[]}],"garbage":"not routes"}`
	routes, err := ParseFRRRIB([]byte(raw), AFIv4)
	if err != nil {
		t.Fatalf("ParseFRRRIB: %v", err)
	}
	if len(routes) != 1 || routes[0].Prefix != "192.168.1.0/24" {
		t.Errorf("got %+v", routes)
	}
}
