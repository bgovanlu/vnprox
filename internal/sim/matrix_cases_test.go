// SPDX-License-Identifier: Apache-2.0

package sim

import "github.com/bgovanlu/vnprox/internal/inventory"

// Fixed endpoint refs used across the firewall categories (guests 100/101 on
// pve1, both on vmbr0).
var (
	g100 = nicRef("pve1", "100")
	g101 = nicRef("pve1", "101")
)

func req(src, dst Endpoint, proto string, port int) func(Input) Request {
	return func(Input) Request { return Request{Src: src, Dst: dst, Proto: proto, Port: port} }
}

// fwWorld: two guests on one node/bridge, firewall ON per-NIC, IPs known.
// Callers add the cluster/guest rulesets the case exercises.
func fwWorld() *world {
	w := newWorld()
	w.bond("pve1", "bond0", "eno1", "eno2")
	w.bridge("pve1", "vmbr0", true, nil, "10.0.0.1", "bond0")
	w.guest("pve1", "100", "app01")
	na := w.nic("pve1", "100", "net0", "vmbr0", 0, true)
	w.ip(na, "10.0.0.10", IPSourceIPAM)
	w.guest("pve1", "101", "app02")
	nb := w.nic("pve1", "101", "net0", "vmbr0", 0, true)
	w.ip(nb, "10.0.0.11", IPSourceIPAM)
	return w
}

// activeGuests adds enabled (but ruleless) guest rulesets for 100 and 101 so
// their firewall is Active (a guest with no ruleset at all is treated as
// firewall-off by fw.Resolve). Individual cases override with rules.
func activeGuests(w *world) *world {
	w.guestFw("pve1", "100", true, "", "", nil)
	w.guestFw("pve1", "101", true, "", "", nil)
	return w
}

// --- category: same-L2 allow ----------------------------------------------

func sameL2Cases() []simCase {
	cat := "same-l2-allow"
	mk := func(name string, sameNode bool, va, vb int) simCase {
		return simCase{
			name: name, category: cat, want: VerdictAllow,
			build: func() Input { return twoGuestBridge(sameNode, va, vb, false).build() },
			req: func(in Input) Request {
				dstNode := "pve1"
				if !sameNode {
					dstNode = "pve2"
				}
				return Request{Src: guestEP(nicRef("pve1", "100")), Dst: guestEP(nicRef(dstNode, "101")), Proto: "tcp", Port: 22}
			},
		}
	}
	cases := []simCase{
		mk("same-node-untagged", true, 0, 0),
		mk("same-node-vlan100", true, 100, 100),
		mk("cross-node-untagged", false, 0, 0),
		mk("cross-node-vlan100-all-permitted", false, 100, 100),
	}

	// Same VNet (vlan zone) same node, firewall off.
	vnetWorld := func(sameNode bool) *world {
		w := newWorld()
		w.bond("pve1", "bond0", "eno1", "eno2")
		w.bridge("pve1", "vmbr0", true, nil, "10.0.0.1", "bond0")
		w.zone("vlanz", "vlan", "vmbr0", []string{"pve1", "pve2"}, nil)
		w.vnet("vnet100", "vlanz", 100)
		w.subnet("10.100.0.0/24", "vnet100", "10.100.0.1", false)
		w.guest("pve1", "100", "app01")
		w.nic("pve1", "100", "net0", "vnet100", 0, false)
		nb := "pve1"
		if sameNode {
			w.guest("pve1", "101", "app02")
			w.nic("pve1", "101", "net0", "vnet100", 0, false)
		} else {
			nb = "pve2"
			w.bond("pve2", "bond0", "eno1", "eno2")
			w.bridge("pve2", "vmbr0", true, nil, "10.0.0.2", "bond0")
			w.guest("pve2", "101", "app02")
			w.nic("pve2", "101", "net0", "vnet100", 0, false)
		}
		_ = nb
		return w
	}
	cases = append(cases,
		simCase{name: "same-vnet-vlanzone-same-node", category: cat, want: VerdictAllow,
			build: func() Input { return vnetWorld(true).build() },
			req:   req(guestEP(nicRef("pve1", "100")), guestEP(nicRef("pve1", "101")), "tcp", 22)},
		simCase{name: "same-vnet-vlanzone-cross-node-trunked", category: cat, want: VerdictAllow,
			build: func() Input { return vnetWorld(false).build() },
			req:   req(guestEP(nicRef("pve1", "100")), guestEP(nicRef("pve2", "101")), "tcp", 22)},
	)

	// Firewall ON but an explicit ACCEPT rule + permissive default → allow.
	cases = append(cases,
		simCase{name: "same-node-fw-on-accept-rule", category: cat, want: VerdictAllow,
			build: func() Input {
				w := fwWorld()
				activeGuests(w)
				w.clusterFw(true, "ACCEPT", "ACCEPT", []inventory.FwRule{rule(0, "in", "ACCEPT", proto("tcp"), dport("22"))})
				return w.build()
			},
			req: req(guestEP(g100), guestEP(g101), "tcp", 22)},
		simCase{name: "same-node-fw-on-default-accept", category: cat, want: VerdictAllow,
			build: func() Input {
				w := fwWorld()
				activeGuests(w)
				w.clusterFw(true, "ACCEPT", "ACCEPT", nil)
				return w.build()
			},
			req: req(guestEP(g100), guestEP(g101), "tcp", 80)},
	)
	return cases
}

// --- category: VLAN trunk / mismatch --------------------------------------

func vlanTrunkCases() []simCase {
	cat := "vlan-trunk"
	// crossBridge builds a cross-node vmbr0 with a given VID set on each node.
	crossBridge := func(srcVids, dstVids []inventory.VidRange, srcVlanAware, dstVlanAware bool, vid int) *world {
		w := newWorld()
		w.bond("pve1", "bond0", "eno1", "eno2")
		w.bridge("pve1", "vmbr0", srcVlanAware, srcVids, "10.0.0.1", "bond0")
		w.bond("pve2", "bond0", "eno1", "eno2")
		w.bridge("pve2", "vmbr0", dstVlanAware, dstVids, "10.0.0.2", "bond0")
		w.guest("pve1", "100", "app01")
		w.nic("pve1", "100", "net0", "vmbr0", vid, false)
		w.guest("pve2", "101", "app02")
		w.nic("pve2", "101", "net0", "vmbr0", vid, false)
		return w
	}
	r := req(guestEP(nicRef("pve1", "100")), guestEP(nicRef("pve2", "101")), "tcp", 22)

	return []simCase{
		lldpMismatchCase(),
		{name: "src-bridge-prunes-vlan", category: cat, want: VerdictUnreachable,
			missingCode: "vlan_not_trunked", missingContains: "VLAN 100 is not trunked on bond0 of node pve1",
			build: func() Input { return crossBridge(vids([2]int{10, 30}), nil, true, true, 100).build() }, req: r},
		{name: "dst-bridge-prunes-vlan", category: cat, want: VerdictUnreachable,
			missingCode: "vlan_not_trunked", missingContains: "VLAN 100 is not trunked on bond0 of node pve2",
			build: func() Input { return crossBridge(nil, vids([2]int{10, 30}), true, true, 100).build() }, req: r},
		{name: "src-bridge-not-vlan-aware", category: cat, want: VerdictUnreachable,
			missingCode: "bridge_not_vlan_aware", missingContains: "is not VLAN-aware",
			build: func() Input { return crossBridge(nil, nil, false, true, 100).build() }, req: r},
		{name: "vlan-in-range-permitted", category: cat, want: VerdictAllow,
			build: func() Input {
				return crossBridge(vids([2]int{10, 200}), vids([2]int{10, 200}), true, true, 100).build()
			}, req: r},
		{name: "vlan-exact-boundary-permitted", category: cat, want: VerdictAllow,
			build: func() Input {
				return crossBridge(vids([2]int{100, 100}), vids([2]int{100, 100}), true, true, 100).build()
			}, req: r},
		{name: "vlan-just-outside-range", category: cat, want: VerdictUnreachable,
			missingCode: "vlan_not_trunked", missingContains: "VLAN 100 is not trunked on bond0 of node pve1",
			build: func() Input { return crossBridge(vids([2]int{10, 99}), nil, true, true, 100).build() }, req: r},
		{name: "multi-range-vids-hit", category: cat, want: VerdictAllow,
			build: func() Input {
				return crossBridge(vids([2]int{10, 20}, [2]int{100, 100}), vids([2]int{100, 100}), true, true, 100).build()
			}, req: r},
		{name: "multi-range-vids-miss", category: cat, want: VerdictUnreachable,
			missingCode: "vlan_not_trunked",
			build: func() Input {
				return crossBridge(vids([2]int{10, 20}, [2]int{30, 40}), nil, true, true, 100).build()
			}, req: r},
	}
}

// lldpTrunkCase: bridge permits the VLAN but the switch's LLDP does not
// advertise it — advisory caveat, verdict still allow.
func lldpMismatchCase() simCase {
	return simCase{name: "lldp-trunk-cross-check", category: "vlan-trunk", want: VerdictAllow,
		wantCaveats: []string{CodeLLDPTrunkMismatch},
		build: func() Input {
			w := newWorld()
			for _, n := range []string{"pve1", "pve2"} {
				w.physnic(n, "eno1")
				w.bond(n, "bond0", "eno1")
				w.bridge(n, "vmbr0", true, nil, "10.0.0.1", "bond0")
				w.lldp(n, "eno1", "aa:bb:cc:dd:ee:0"+n[len(n)-1:], "Gi1/0/1", 10) // advertises only PVID 10
			}
			w.guest("pve1", "100", "app01")
			w.nic("pve1", "100", "net0", "vmbr0", 100, false)
			w.guest("pve2", "101", "app02")
			w.nic("pve2", "101", "net0", "vmbr0", 100, false)
			return w.build()
		},
		req: req(guestEP(nicRef("pve1", "100")), guestEP(nicRef("pve2", "101")), "tcp", 22)}
}
