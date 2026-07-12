// Command genscale generates testdata/clusters/scale-lab.yaml: the T-607
// scale fixture at exactly docs/features/topology.md §4's target — 8 nodes
// x 6 NICs, 4 bridges/node, 300 guests, 40 VNets. It is not hand-written
// (300 guest stanzas is not something to type out by hand); this program
// builds a pvemock.Fixture value directly (so its shape is guaranteed to
// match what pvemock.LoadFixture/Validate expects — no hand-authored YAML
// drift possible) and marshals it with the same yaml.v3 encoder LoadFixture
// decodes with.
//
// This file lives under testdata/ specifically so `go build ./...`/
// `go vet ./...`/golangci-lint's package discovery ignore it (the go
// toolchain skips any directory named "testdata") — it is a one-off
// generator, not a shipped binary, and needs no Makefile wiring.
//
// Regenerate with:
//
//	go run ./testdata/genscale > testdata/clusters/scale-lab.yaml
//
// (the shebang-style header below is written by hand after generation,
// since yaml.Marshal cannot emit leading comments itself).
package main

import (
	"fmt"
	"os"

	"github.com/bgovanlu/vnprox/internal/pvemock"
	"gopkg.in/yaml.v3"
)

const (
	numNodes       = 8
	nicsPerNode    = 6
	bridgesPerNode = 4
	numGuests      = 300
	numVNets       = 40
)

func main() {
	f := buildFixture()
	if err := f.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "genscale: generated fixture failed Validate:", err)
		os.Exit(1)
	}

	out, err := yaml.Marshal(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "genscale: marshal:", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintln(os.Stderr, "genscale: write:", err)
		os.Exit(1)
	}
}

func nodeName(i int) string { return fmt.Sprintf("pve%d", i+1) }

func buildFixture() *pvemock.Fixture {
	f := &pvemock.Fixture{
		Cluster: pvemock.ClusterSpec{
			Name:    "pve-cluster-scale",
			Quorate: true,
		},
		Users: []pvemock.UserSpec{
			{
				UserID:     "root@pam",
				Password:   "vnprox-mock",
				Privileges: []string{"*"},
				Tokens: []pvemock.TokenSpec{
					{TokenID: "daemon", Secret: "5c8e2a1f-9b40-4d7e-8a11-cafe0000scal"},
				},
			},
			{
				UserID:     "auditor@pve",
				Password:   "readonly",
				Privileges: []string{"Sys.Audit", "VM.Audit", "SDN.Audit"},
			},
		},
		Nodes: map[string]*pvemock.NodeSpec{},
	}

	for i := 0; i < numNodes; i++ {
		node := nodeName(i)
		f.Cluster.Nodes = append(f.Cluster.Nodes, pvemock.ClusterNodeSpec{
			Name: node, IP: fmt.Sprintf("10.20.0.%d", 11+i), Online: true,
		})
	}

	var vnetTags [numVNets]int
	for v := 0; v < numVNets; v++ {
		vnetTags[v] = 100 + v
	}

	guestCounter := 0
	for i := 0; i < numNodes; i++ {
		node := nodeName(i)
		ns := &pvemock.NodeSpec{
			Qemu:  map[string]*pvemock.GuestSpec{},
			Lxc:   map[string]*pvemock.GuestSpec{},
			Links: map[string]pvemock.LinkInfo{},
			Stats: map[string]pvemock.IfaceStats{},
		}

		// lo
		ns.Network = append(ns.Network, pvemock.NetIface{
			Iface: "lo", Type: "loopback", Method: "loopback", Autostart: true,
		})

		// 6 physical NICs.
		for j := 0; j < nicsPerNode; j++ {
			nic := fmt.Sprintf("eno%d", j+1)
			ns.Network = append(ns.Network, pvemock.NetIface{
				Iface: nic, Type: "eth", Method: "manual", Autostart: true, MTU: 1500,
			})
			mac := fmt.Sprintf("bc:24:%02x:%02x:00:%02x", i+1, j+1, j+1)
			ns.Links[nic] = pvemock.LinkInfo{
				Mac: mac, Driver: "ixgbe", SpeedMbps: 10000, Duplex: "full",
				LinkUp: true, PCIAddr: fmt.Sprintf("0000:%02x:00.%d", i+1, j%2),
			}
			ns.Stats[nic] = pvemock.IfaceStats{
				RxBytes:   uint64(3_000_000_000 + i*10_000_000 + j*1_000_000),
				TxBytes:   uint64(1_200_000_000 + i*5_000_000 + j*500_000),
				RxPackets: uint64(2_500_000 + j*10_000), TxPackets: uint64(1_400_000 + j*8_000),
			}
		}

		// bond0 (eno1+eno2) -> vmbr0: management/corosync bridge, protected,
		// not part of the SDN overlay.
		ns.Network = append(ns.Network, pvemock.NetIface{
			Iface: "bond0", Type: "bond", Method: "manual", Autostart: true, MTU: 1500,
			Slaves: "eno1 eno2", BondMode: "802.3ad", Comments: "LACP mgmt/corosync uplink",
		})
		bond0Mac := fmt.Sprintf("bc:24:%02x:01:00:01", i+1)
		ns.Links["bond0"] = pvemock.LinkInfo{Mac: bond0Mac, LinkUp: true}

		ns.Network = append(ns.Network, pvemock.NetIface{
			Iface: "vmbr0", Type: "bridge", Method: "static", Autostart: true, MTU: 1500,
			Address: fmt.Sprintf("10.20.0.%d/24", 11+i), Gateway: "10.20.0.1",
			BridgePorts: "bond0", BridgeVlanAware: false, Comments: "management + corosync",
		})
		ns.Links["vmbr0"] = pvemock.LinkInfo{Mac: bond0Mac, LinkUp: true}
		ns.Stats["vmbr0"] = pvemock.IfaceStats{RxBytes: 6_000_000_000, TxBytes: 2_400_000_000, RxPackets: 5_000_000, TxPackets: 2_800_000}

		// bond1 (eno3+eno4) -> vmbr1: the cluster-wide SDN zone bridge
		// hosting all 40 VNets.
		ns.Network = append(ns.Network, pvemock.NetIface{
			Iface: "bond1", Type: "bond", Method: "manual", Autostart: true, MTU: 1500,
			Slaves: "eno3 eno4", BondMode: "802.3ad", Comments: "LACP SDN overlay uplink",
		})
		bond1Mac := fmt.Sprintf("bc:24:%02x:01:00:03", i+1)
		ns.Links["bond1"] = pvemock.LinkInfo{Mac: bond1Mac, LinkUp: true}

		ns.Network = append(ns.Network, pvemock.NetIface{
			Iface: "vmbr1", Type: "bridge", Method: "manual", Autostart: true, MTU: 1500,
			BridgePorts: "bond1", BridgeVlanAware: true, Comments: "SDN VLAN zone bridge (scalez)",
		})
		ns.Links["vmbr1"] = pvemock.LinkInfo{Mac: bond1Mac, LinkUp: true}
		ns.Stats["vmbr1"] = pvemock.IfaceStats{RxBytes: 9_000_000_000, TxBytes: 3_600_000_000, RxPackets: 7_500_000, TxPackets: 4_100_000}

		// vmbr2, vmbr3: two plain, port-less guest bridges (prepared but not
		// yet wired to an uplink — a realistic "isolated/internal network"
		// shape, and it's what real PVE allows: a bridge needs no ports at
		// all). Deliberately NOT attached to eno5/eno6 (unlike an earlier
		// version of this generator, which enslaved them and left the
		// fixture with zero free NICs anywhere — a real gap the "create a
		// LACP bond from two NICs" E2E task surfaced, since it needs
		// genuinely free NICs to bond). eno5/eno6 stay standalone physical
		// NICs with no bridge/bond attachment at all.
		for bi, b := range []string{"vmbr2", "vmbr3"} {
			ns.Network = append(ns.Network, pvemock.NetIface{
				Iface: b, Type: "bridge", Method: "manual", Autostart: true, MTU: 1500,
				BridgeVlanAware: true, Comments: "guest access bridge (no uplink yet)",
			})
			ns.Links[b] = pvemock.LinkInfo{Mac: fmt.Sprintf("bc:24:%02x:02:00:%02x", i+1, 10+bi), LinkUp: true}
			ns.Stats[b] = pvemock.IfaceStats{RxBytes: 2_000_000_000, TxBytes: 800_000_000, RxPackets: 1_800_000, TxPackets: 900_000}
		}

		// Guests: numGuests split evenly across nodes (remainder to the
		// first nodes) so cluster-wide VMIDs stay globally unique, matching
		// real PVE's cluster-unique VMID constraint.
		guestsThisNode := numGuests / numNodes
		if i < numGuests%numNodes {
			guestsThisNode++
		}
		for j := 0; j < guestsThisNode; j++ {
			vmid := 100 + guestCounter
			mac := fmt.Sprintf("BC:24:11:%02X:%02X:%02X", (vmid>>16)&0xff, (vmid>>8)&0xff, vmid&0xff)
			var bridge string
			var tag int
			switch guestCounter % 3 {
			case 0:
				bridge, tag = "vmbr2", 10+(guestCounter%20)
			case 1:
				bridge, tag = "vmbr3", 10+(guestCounter%20)
			default:
				vi := guestCounter % numVNets
				bridge, tag = "vmbr1", vnetTags[vi]
			}

			isQemu := guestCounter%2 == 0
			name := fmt.Sprintf("scale-%d", vmid)
			if isQemu {
				ns.Qemu[fmt.Sprintf("%d", vmid)] = &pvemock.GuestSpec{
					Name: name, Status: "running",
					Config: map[string]string{
						"name":   name,
						"cores":  "2",
						"memory": "2048",
						"net0":   fmt.Sprintf("virtio=%s,bridge=%s,tag=%d,firewall=1", mac, bridge, tag),
						"ostype": "l26",
					},
				}
			} else {
				ns.Lxc[fmt.Sprintf("%d", vmid)] = &pvemock.GuestSpec{
					Name: name, Status: "running",
					Config: map[string]string{
						"hostname": name,
						"cores":    "1",
						"memory":   "512",
						"net0":     fmt.Sprintf("name=eth0,bridge=%s,tag=%d,firewall=1,hwaddr=%s", bridge, tag, mac),
					},
				}
			}
			guestCounter++
		}

		ns.Firewall = &pvemock.FirewallScope{
			Enabled: true, PolicyIn: "DROP", PolicyOut: "ACCEPT",
		}
		f.Nodes[node] = ns
	}
	if guestCounter != numGuests {
		panic(fmt.Sprintf("genscale: generated %d guests, want %d", guestCounter, numGuests))
	}

	// SDN: one VLAN zone spanning all 8 nodes on vmbr1, hosting all 40
	// VNets/subnets.
	allNodes := make([]string, numNodes)
	for i := range allNodes {
		allNodes[i] = nodeName(i)
	}
	f.SDN.Zones = []pvemock.SDNZoneSpec{
		{ID: "scalez", Type: "vlan", Bridge: "vmbr1", Nodes: allNodes, MTU: 1500},
	}
	ipam := pvemock.SDNIpamSpec{ID: "pve", Type: "pve"}
	for v := 0; v < numVNets; v++ {
		vname := fmt.Sprintf("vnet%d", v)
		cidr := fmt.Sprintf("10.150.%d.0/24", v)
		gw := fmt.Sprintf("10.150.%d.1", v)
		f.SDN.Vnets = append(f.SDN.Vnets, pvemock.SDNVnetSpec{
			ID: vname, Zone: "scalez", Tag: vnetTags[v], Alias: fmt.Sprintf("tier-%02d", v),
		})
		f.SDN.Subnets = append(f.SDN.Subnets, pvemock.SDNSubnetSpec{
			ID: cidr, Vnet: vname, CIDR: cidr, Gateway: gw,
		})
		ipam.Entries = append(ipam.Entries, pvemock.IPAMEntrySpec{
			Zone: "scalez", Vnet: vname, Subnet: cidr, IP: gw, Gateway: true,
		})
	}
	f.SDN.Ipams = []pvemock.SDNIpamSpec{ipam}

	f.Firewall = pvemock.FirewallSpec{
		Cluster: pvemock.FirewallScope{
			Enabled: true, PolicyIn: "DROP", PolicyOut: "ACCEPT",
			Rules: []pvemock.FwRuleSpec{
				{Pos: 0, Enabled: true, Type: "in", Action: "ACCEPT", Proto: "tcp", Dport: "22", Comment: "cluster-wide SSH"},
			},
		},
	}

	return f
}
