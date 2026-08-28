// SPDX-License-Identifier: Apache-2.0

package sim

import "github.com/bgovanlu/vnprox/internal/inventory"

// --- category: honesty contract / AC5 -------------------------------------

func honestyCases() []simCase {
	cat := "honesty"
	return []simCase{
		{name: "unknown-endpoint-kind-not-a-nic", category: cat, want: VerdictIndeterminate,
			wantCaveats: []string{CodeNotEvaluated},
			build:       func() Input { return twoGuestBridge(true, 0, 0, false).build() },
			req: func(Input) Request {
				// point src at a bridge ref (wrong kind) rather than a guest NIC.
				return Request{
					Src: Endpoint{Kind: EndpointGuestNic, NicRef: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}},
					Dst: guestEP(nicRef("pve1", "101")), Proto: "tcp", Port: 22,
				}
			}},
		{name: "guest-nic-not-found", category: cat, want: VerdictIndeterminate,
			wantCaveats: []string{CodeNotEvaluated},
			build:       func() Input { return twoGuestBridge(true, 0, 0, false).build() },
			req:         req(guestEP(nicRef("pve1", "999")), guestEP(nicRef("pve1", "101")), "tcp", 22)},
		{name: "invalid-ip-literal", category: cat, want: VerdictIndeterminate,
			wantCaveats: []string{CodeNotEvaluated},
			build:       func() Input { return twoGuestBridge(true, 0, 0, false).build() },
			req:         req(ipEP("not-an-ip"), guestEP(nicRef("pve1", "101")), "tcp", 22)},
		{name: "unattached-nic-unreachable", category: cat, want: VerdictUnreachable,
			missingCode: "nic_unattached",
			build: func() Input {
				w := newWorld()
				w.bond("pve1", "bond0", "eno1")
				w.bridge("pve1", "vmbr0", true, nil, "10.0.0.1", "bond0")
				w.guest("pve1", "100", "a")
				w.nic("pve1", "100", "net0", "ghostbr", 0, false) // no such bridge
				w.guest("pve1", "101", "b")
				w.nic("pve1", "101", "net0", "vmbr0", 0, false)
				return w.build()
			},
			req: req(guestEP(nicRef("pve1", "100")), guestEP(nicRef("pve1", "101")), "tcp", 22)},
		{name: "ovs-bridge-caveat", category: cat, want: VerdictAllow,
			wantCaveats: []string{CodeOVS},
			build: func() Input {
				w := newWorld()
				w.ovsBridge("pve1", "vmbr0", "bond0")
				w.guest("pve1", "100", "a")
				w.nic("pve1", "100", "net0", "vmbr0", 0, false)
				w.guest("pve1", "101", "b")
				w.nic("pve1", "101", "net0", "vmbr0", 0, false)
				return w.build()
			},
			req: req(guestEP(nicRef("pve1", "100")), guestEP(nicRef("pve1", "101")), "tcp", 22)},
		{name: "different-vlan-no-sdn-indeterminate", category: cat, want: VerdictIndeterminate,
			wantCaveats: []string{CodeNotEvaluated},
			build:       func() Input { return twoGuestBridge(true, 100, 200, false).build() },
			req:         req(guestEP(nicRef("pve1", "100")), guestEP(nicRef("pve1", "101")), "tcp", 22)},
		{name: "address-rule-guest-ip-unknown", category: cat, want: VerdictIndeterminate,
			wantCaveats: []string{CodeGuestIPUnknown, CodeNotEvaluated},
			build: func() Input {
				// two guests, firewall on, but NO IPs in the side-table.
				w := newWorld()
				w.bond("pve1", "bond0", "eno1")
				w.bridge("pve1", "vmbr0", true, nil, "10.0.0.1", "bond0")
				w.guest("pve1", "100", "a")
				w.nic("pve1", "100", "net0", "vmbr0", 0, true)
				w.guest("pve1", "101", "b")
				w.nic("pve1", "101", "net0", "vmbr0", 0, true)
				w.clusterFw(true, "DROP", "ACCEPT", nil)
				w.guestFw("pve1", "100", true, "", "", nil)
				w.guestFw("pve1", "101", true, "", "", rules(rule(0, "in", "ACCEPT", source("10.0.0.0/24"))))
				return w.build()
			},
			req: req(guestEP(nicRef("pve1", "100")), guestEP(nicRef("pve1", "101")), "tcp", 22)},
		{name: "node-firewall-not-on-path-caveat", category: cat, want: VerdictAllow,
			wantCaveats: []string{CodeNodeFirewall},
			build: func() Input {
				w := fwWorld()
				w.clusterFw(true, "ACCEPT", "ACCEPT", nil)
				w.guestFw("pve1", "100", true, "", "", nil)
				w.guestFw("pve1", "101", true, "", "", nil)
				w.nodeFw("pve1", true, rules(rule(0, "in", "DROP", proto("tcp"), dport("22"))))
				return w.build()
			},
			req: req(guestEP(g100), guestEP(g101), "tcp", 22)},
		{name: "guest-agent-ip-confidence-caveat", category: cat, want: VerdictAllow,
			wantCaveats: []string{CodeGuestAgentIP},
			build: func() Input {
				w := newWorld()
				w.bond("pve1", "bond0", "eno1")
				w.bridge("pve1", "vmbr0", true, nil, "10.0.0.1", "bond0")
				w.guest("pve1", "100", "a")
				na := w.nic("pve1", "100", "net0", "vmbr0", 0, false)
				w.ip(na, "10.0.0.10", IPSourceAgent) // low-confidence source
				w.guest("pve1", "101", "b")
				w.nic("pve1", "101", "net0", "vmbr0", 0, false)
				return w.build()
			},
			req: req(guestEP(nicRef("pve1", "100")), guestEP(nicRef("pve1", "101")), "tcp", 22)},
	}
}
