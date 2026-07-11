package sim

import "github.com/bgovanlu/vnprox/internal/inventory"

// underlayNodes gives each node an underlay bridge vmbr0 (with a gateway),
// used to anchor SDN worlds and external-boundary tests.
func underlayNodes(w *world, nodes ...string) {
	for _, n := range nodes {
		w.physnic(n, "eno1")
		w.bridge(n, "vmbr0", false, nil, "10.20.0.1", "eno1")
	}
}

// --- category: zone routing (L3 / SDN) ------------------------------------

func zoneRoutingCases() []simCase {
	cat := "zone-routing"

	// evpnTwoVnet: two VNets in one EVPN zone, one guest on each, IPs set.
	evpnTwoVnet := func() *world {
		w := newWorld()
		underlayNodes(w, "pve1", "pve2", "pve3")
		w.zone("evpnz", "evpn", "", []string{"pve1", "pve2", "pve3"}, []string{"pve3"})
		w.vnet("vnetA", "evpnz", 0)
		w.subnet("192.168.10.0/24", "vnetA", "192.168.10.1", false)
		w.vnet("vnetB", "evpnz", 0)
		w.subnet("192.168.20.0/24", "vnetB", "192.168.20.1", false)
		w.guest("pve1", "100", "a")
		na := w.nic("pve1", "100", "net0", "vnetA", 0, false)
		w.ip(na, "192.168.10.5", IPSourceIPAM)
		w.guest("pve2", "101", "b")
		nb := w.nic("pve2", "101", "net0", "vnetB", 0, false)
		w.ip(nb, "192.168.20.5", IPSourceIPAM)
		return w
	}

	// zonePair: two vnets in DIFFERENT zones (evpn + vlan), guests on each.
	zonePair := func() *world {
		w := newWorld()
		underlayNodes(w, "pve1", "pve2")
		w.zone("evpnz", "evpn", "", []string{"pve1", "pve2"}, []string{"pve2"})
		w.vnet("vnetA", "evpnz", 0)
		w.subnet("192.168.10.0/24", "vnetA", "192.168.10.1", false)
		w.zone("vlanz", "vlan", "vmbr1", []string{"pve1", "pve2"}, nil)
		w.bridge("pve1", "vmbr1", true, nil, "", "bond0")
		w.vnet("vnetV", "vlanz", 50)
		w.subnet("10.50.0.0/24", "vnetV", "10.50.0.1", false)
		w.guest("pve1", "100", "a")
		na := w.nic("pve1", "100", "net0", "vnetA", 0, false)
		w.ip(na, "192.168.10.5", IPSourceIPAM)
		w.guest("pve1", "101", "v")
		nv := w.nic("pve1", "101", "net0", "vnetV", 0, false)
		w.ip(nv, "10.50.0.5", IPSourceIPAM)
		return w
	}

	// vlanTwoVnet: two vnets in one VLAN zone (no inter-VNet routing).
	vlanTwoVnet := func() *world {
		w := newWorld()
		w.bridge("pve1", "vmbr0", true, nil, "", "bond0")
		w.zone("vlanz", "vlan", "vmbr0", []string{"pve1"}, nil)
		w.vnet("vnetA", "vlanz", 10)
		w.subnet("10.10.0.0/24", "vnetA", "10.10.0.1", false)
		w.vnet("vnetB", "vlanz", 20)
		w.subnet("10.20.0.0/24", "vnetB", "10.20.0.1", false)
		w.guest("pve1", "100", "a")
		na := w.nic("pve1", "100", "net0", "vnetA", 0, false)
		w.ip(na, "10.10.0.5", IPSourceIPAM)
		w.guest("pve1", "101", "b")
		nb := w.nic("pve1", "101", "net0", "vnetB", 0, false)
		w.ip(nb, "10.20.0.5", IPSourceIPAM)
		return w
	}

	// overlayVnet: one vnet in a given zone type, one guest per node.
	overlayVnet := func(ztype string, nodes []string) *world {
		w := newWorld()
		underlayNodes(w, "pve1", "pve2")
		w.zone("z", ztype, "vmbr0", nodes, nil)
		w.vnet("vnetX", "z", 0)
		w.subnet("172.16.0.0/24", "vnetX", "172.16.0.1", false)
		w.guest("pve1", "100", "a")
		w.nic("pve1", "100", "net0", "vnetX", 0, false)
		w.guest("pve2", "101", "b")
		w.nic("pve2", "101", "net0", "vnetX", 0, false)
		return w
	}

	crossReq := req(guestEP(nicRef("pve1", "100")), guestEP(nicRef("pve2", "101")), "tcp", 22)

	return []simCase{
		{name: "evpn-inter-vnet-routes", category: cat, want: VerdictAllow,
			build: func() Input { return evpnTwoVnet().build() }, req: crossReq},
		{name: "evpn-inter-vnet-ip-src", category: cat, want: VerdictAllow,
			build: func() Input { return evpnTwoVnet().build() },
			req:   req(ipEP("192.168.10.9"), guestEP(nicRef("pve2", "101")), "tcp", 22)},
		{name: "vlan-zone-no-inter-vnet-route", category: cat, want: VerdictUnreachable,
			missingCode: "no_intrazone_route", missingContains: "only EVPN zones",
			build: func() Input { return vlanTwoVnet().build() },
			req:   req(guestEP(nicRef("pve1", "100")), guestEP(nicRef("pve1", "101")), "tcp", 22)},
		{name: "different-zones-no-route", category: cat, want: VerdictUnreachable,
			missingCode: "no_route_between_zones", missingContains: "different zones without exit node",
			build: func() Input { return zonePair().build() },
			req:   req(guestEP(nicRef("pve1", "100")), guestEP(nicRef("pve1", "101")), "tcp", 22)},
		{name: "simple-zone-node-local", category: cat, want: VerdictUnreachable,
			missingCode: "simple_zone_node_local", missingContains: "node-local",
			build: func() Input { return overlayVnet("simple", []string{"pve1", "pve2"}).build() }, req: crossReq},
		{name: "vxlan-overlay-cross-node", category: cat, want: VerdictAllow,
			build: func() Input { return overlayVnet("vxlan", []string{"pve1", "pve2"}).build() }, req: crossReq},
		{name: "evpn-overlay-same-vnet-cross-node", category: cat, want: VerdictAllow,
			build: func() Input { return overlayVnet("evpn", []string{"pve1", "pve2"}).build() }, req: crossReq},
		{name: "vnet-not-realized-on-dst", category: cat, want: VerdictUnreachable,
			missingCode: "vnet_not_realized", missingContains: "not realized on node pve2",
			build: func() Input { return overlayVnet("vxlan", []string{"pve1"}).build() }, req: crossReq},
		{name: "unknown-zone-type-indeterminate", category: cat, want: VerdictIndeterminate,
			wantCaveats: []string{CodeNotEvaluated},
			build:       func() Input { return overlayVnet("mystery", []string{"pve1", "pve2"}).build() }, req: crossReq},
		{name: "qinq-service-vlan-trunked", category: cat, want: VerdictAllow,
			wantCaveats: []string{CodeNotEvaluated},
			build: func() Input {
				w := newWorld()
				w.bond("pve1", "bond0", "eno1")
				w.bridge("pve1", "vmbr0", true, nil, "", "bond0")
				w.bond("pve2", "bond0", "eno1")
				w.bridge("pve2", "vmbr0", true, nil, "", "bond0")
				w.zone("qinqz", "qinq", "vmbr0", []string{"pve1", "pve2"}, nil)
				w.vnet("vnetQ", "qinqz", 200)
				w.subnet("10.70.0.0/24", "vnetQ", "10.70.0.1", false)
				w.guest("pve1", "100", "a")
				w.nic("pve1", "100", "net0", "vnetQ", 0, false)
				w.guest("pve2", "101", "b")
				w.nic("pve2", "101", "net0", "vnetQ", 0, false)
				return w.build()
			}, req: crossReq},
	}
}

// --- category: disabled-scope passthrough ---------------------------------

func disabledScopeCases() []simCase {
	cat := "disabled-scope"
	r := req(guestEP(g100), guestEP(g101), "tcp", 22)

	dcOff := func() Input {
		w := fwWorld()
		w.clusterFw(false, "DROP", "ACCEPT", rules(rule(0, "in", "DROP")))
		w.guestFw("pve1", "100", true, "", "", nil)
		w.guestFw("pve1", "101", true, "", "", nil)
		return w.build()
	}
	guestOff := func() Input {
		w := fwWorld()
		w.clusterFw(true, "DROP", "ACCEPT", nil)
		w.guestFw("pve1", "100", true, "", "", nil)
		w.guestFw("pve1", "101", false, "", "", rules(rule(0, "in", "DROP")))
		return w.build()
	}
	nicOff := func(fw100, fw101 bool) func() Input {
		return func() Input {
			w := newWorld()
			w.bond("pve1", "bond0", "eno1")
			w.bridge("pve1", "vmbr0", true, nil, "10.0.0.1", "bond0")
			w.guest("pve1", "100", "a")
			na := w.nic("pve1", "100", "net0", "vmbr0", 0, fw100)
			w.ip(na, "10.0.0.10", IPSourceIPAM)
			w.guest("pve1", "101", "b")
			nb := w.nic("pve1", "101", "net0", "vmbr0", 0, fw101)
			w.ip(nb, "10.0.0.11", IPSourceIPAM)
			w.clusterFw(true, "DROP", "ACCEPT", nil)
			w.guestFw("pve1", "100", true, "", "", rules(rule(0, "out", "DROP")))
			w.guestFw("pve1", "101", true, "", "", rules(rule(0, "in", "DROP")))
			return w.build()
		}
	}
	return []simCase{
		{name: "datacenter-off-passthrough", category: cat, want: VerdictAllow, build: dcOff, req: r},
		{name: "guest-firewall-off-passthrough", category: cat, want: VerdictAllow, build: guestOff, req: r},
		{name: "both-nics-firewall-off", category: cat, want: VerdictAllow, build: nicOff(false, false), req: r},
		{name: "source-nic-off-dest-on-denies-at-dest", category: cat, want: VerdictDeny,
			blockingPoint: "dest-guest-in", build: nicOff(false, true), req: r},
		{name: "dest-nic-off-source-on-denies-at-source", category: cat, want: VerdictDeny,
			blockingPoint: "source-guest-out", build: nicOff(true, false), req: r},
	}
}

// --- category: external endpoints -----------------------------------------

func externalCases() []simCase {
	cat := "external"

	// plain bridge with a gateway; firewall optional.
	extBridge := func(nicFw bool, cIn, cOut string, g100r []inventory.FwRule, gateway string) func() Input {
		return func() Input {
			w := newWorld()
			w.bond("pve1", "bond0", "eno1")
			w.bridge("pve1", "vmbr0", true, nil, gateway, "bond0")
			w.guest("pve1", "100", "a")
			na := w.nic("pve1", "100", "net0", "vmbr0", 0, nicFw)
			w.ip(na, "10.0.0.10", IPSourceIPAM)
			if nicFw {
				w.clusterFw(true, cIn, cOut, nil)
				w.guestFw("pve1", "100", true, "", "", g100r)
			}
			return w.build()
		}
	}
	// SDN vnet guest with configurable exit-node / SNAT.
	sdnGuest := func(exitNodes []string, snat bool) func() Input {
		return func() Input {
			w := newWorld()
			underlayNodes(w, "pve1", "pve2")
			w.zone("evpnz", "evpn", "", []string{"pve1", "pve2"}, exitNodes)
			w.vnet("vnetA", "evpnz", 0)
			w.subnet("192.168.10.0/24", "vnetA", "192.168.10.1", snat)
			w.guest("pve1", "100", "a")
			na := w.nic("pve1", "100", "net0", "vnetA", 0, false)
			w.ip(na, "192.168.10.5", IPSourceIPAM)
			return w.build()
		}
	}

	extRef := extEP()
	toExt := req(guestEP(g100), extRef, "tcp", 443)
	fromExt := req(extRef, guestEP(g100), "tcp", 22)

	return []simCase{
		{name: "guest-to-external-via-gateway-allow", category: cat, want: VerdictAllow,
			build: extBridge(true, "DROP", "ACCEPT", nil, "10.0.0.1"), req: toExt},
		{name: "external-to-guest-default-in-drop", category: cat, want: VerdictDeny, blockingPoint: "dest-guest-in",
			build: extBridge(true, "DROP", "ACCEPT", nil, "10.0.0.1"), req: fromExt},
		{name: "external-to-guest-accept-rule", category: cat, want: VerdictAllow,
			build: extBridge(true, "DROP", "ACCEPT", rules(rule(0, "in", "ACCEPT", proto("tcp"), dport("22"))), "10.0.0.1"),
			req:   fromExt},
		{name: "guest-to-external-source-out-drop", category: cat, want: VerdictDeny, blockingPoint: "source-guest-out",
			build: extBridge(true, "ACCEPT", "DROP", nil, "10.0.0.1"), req: toExt},
		{name: "bridge-no-gateway-unreachable", category: cat, want: VerdictUnreachable,
			missingCode: "no_gateway", build: extBridge(false, "", "", nil, ""), req: toExt},
		{name: "evpn-exit-node-reachable", category: cat, want: VerdictAllow,
			build: sdnGuest([]string{"pve2"}, false),
			req:   req(guestEP(nicRef("pve1", "100")), extRef, "tcp", 443)},
		{name: "evpn-no-exit-no-snat-unreachable", category: cat, want: VerdictUnreachable,
			missingCode: "no_external_boundary",
			build:       sdnGuest(nil, false), req: req(guestEP(nicRef("pve1", "100")), extRef, "tcp", 443)},
		{name: "snat-egress-with-asymmetry-caveat", category: cat, want: VerdictAllow,
			wantCaveats: []string{CodeSNATAsymmetry},
			build:       sdnGuest(nil, true), req: req(guestEP(nicRef("pve1", "100")), extRef, "tcp", 443)},
		{name: "external-to-external-nonsense", category: cat, want: VerdictUnreachable,
			missingCode: "both_external",
			build:       func() Input { return newWorld().build() }, req: req(extEP(), extEP(), "tcp", 80)},
		{name: "external-inbound-conntrack-caveat", category: cat, want: VerdictAllow,
			wantCaveats: []string{CodeConntrack},
			build:       extBridge(true, "ACCEPT", "ACCEPT", nil, "10.0.0.1"), req: fromExt},
	}
}
