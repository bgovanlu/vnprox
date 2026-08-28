// SPDX-License-Identifier: Apache-2.0

package pvemock

import "fmt"

// scaleprofile.go (T-4107) is this package's *size* axis, distinct from
// compat_versions.go's *version* axis. compat_versions.go's
// PVEVersionProfile answers "which API shape does this PVE release line
// have"; nothing before this file answered "what does a cluster with N
// nodes and M guests look like" — grep -rn "scale|Scale" over this package
// before T-4107 turned up only a ticket-lifetime comment, not a fixture
// generator (see the T-4107 task card's "Repo fact").
//
// This is deliberately the same generation strategy testdata/genscale/
// main.go (T-607) already established for the 8-node/300-guest topology.md
// §4 target: build a *pvemock.Fixture directly in Go (guaranteeing its
// shape matches what LoadFixture/Validate expect — no hand-authored YAML
// drift possible) rather than marshal one to disk and read it back. This
// file is a parameterized generalization of that generator, not a rewrite
// of it: testdata/genscale/main.go is untouched (it is the checked-in,
// version-pinned scale-lab.yaml the T-607 benchmarks already depend on
// byte-for-byte), and this file exists so a *larger* profile — the T-4107
// scale envelope, 50 nodes / 5,000 guests — can be built in-process,
// without checking in a multi-megabyte YAML fixture nobody would ever hand-
// review. "Cheap enough to use in tests" means exactly this: no disk I/O,
// no YAML parse, just Go struct literals in a loop.
//
// The node/bridge topology shape (bond0+vmbr0 mgmt, bond1+vmbr1 SDN
// overlay, vmbr2/vmbr3 plain guest bridges, 6 physical NICs/node) is fixed
// to genscale's, not made independently configurable — every scale target
// this project has documented (topology.md §4's 8/300, and the envelope
// below) uses this exact shape, and inventing knobs for a shape nothing
// asks to vary would be speculative surface, not generality.
type ScaleProfileConfig struct {
	// Nodes is the cluster node count.
	Nodes int
	// GuestsPerNode is guests created on each node (so total guests =
	// Nodes * GuestsPerNode). Split evenly per node rather than genscale's
	// remainder-distribution, because every profile this file defines
	// divides evenly — that logic exists in genscale only because 300/8
	// does not.
	GuestsPerNode int
	// VNets is the number of SDN VNets in the one cluster-wide VLAN zone
	// every profile generates, mirroring genscale's SDN shape.
	VNets int
}

// EnvelopeProfile is T-4107's documented scale envelope: 50 nodes, 5,000
// guests (100/node), 100 VNets. See docs/development.md's "Scale envelope"
// section for what was measured against it and the perf/budgets.json
// entries it backs.
var EnvelopeProfile = ScaleProfileConfig{Nodes: 50, GuestsPerNode: 100, VNets: 100}

// scaleProfileNicsPerNode and scaleProfileBridgesPerNode are the fixed shape
// documented above — see the type doc comment for why these are constants
// rather than config fields.
const (
	scaleProfileNicsPerNode    = 6
	scaleProfileBridgesPerNode = 4
)

// NewScaleProfile builds a validated *Fixture at cfg's size, following
// testdata/genscale/main.go's exact node/bridge/guest shape (bond0/vmbr0
// management, bond1/vmbr1 SDN overlay carrying every VNet, vmbr2/vmbr3
// plain guest bridges, guests round-robined across vmbr2/vmbr3/the SDN
// zone). It returns an error rather than panicking on an invalid cfg (zero
// Nodes, etc.) or on a fixture that fails its own Validate — a caller
// building a benchmark fixture wants a table-driven t.Fatalf/b.Fatalf, not
// a process crash.
func NewScaleProfile(cfg ScaleProfileConfig) (*Fixture, error) {
	if cfg.Nodes < 1 {
		return nil, fmt.Errorf("pvemock: scale profile needs at least 1 node, got %d", cfg.Nodes)
	}
	if cfg.GuestsPerNode < 0 {
		return nil, fmt.Errorf("pvemock: scale profile GuestsPerNode must be >= 0, got %d", cfg.GuestsPerNode)
	}
	if cfg.VNets < 1 {
		return nil, fmt.Errorf("pvemock: scale profile needs at least 1 VNet, got %d", cfg.VNets)
	}

	f := &Fixture{
		Cluster: ClusterSpec{Name: "pve-cluster-scale", Quorate: true},
		Users: []UserSpec{
			{
				UserID:     "root@pam",
				Password:   "vnprox-mock",
				Privileges: []string{"*"},
				Tokens: []TokenSpec{
					{TokenID: "daemon", Secret: "5c8e2a1f-9b40-4d7e-8a11-cafe0000scal"},
				},
			},
			{
				UserID:     "auditor@pve",
				Password:   "readonly",
				Privileges: []string{"Sys.Audit", "VM.Audit", "SDN.Audit"},
			},
		},
		Nodes: map[string]*NodeSpec{},
	}

	nodeName := func(i int) string { return fmt.Sprintf("pve%d", i+1) }

	for i := 0; i < cfg.Nodes; i++ {
		f.Cluster.Nodes = append(f.Cluster.Nodes, ClusterNodeSpec{
			Name: nodeName(i), IP: fmt.Sprintf("10.20.%d.%d", i/250, 11+i%250), Online: true,
		})
	}

	vnetTags := make([]int, cfg.VNets)
	for v := range vnetTags {
		vnetTags[v] = 100 + v
	}

	guestCounter := 0
	for i := 0; i < cfg.Nodes; i++ {
		node := nodeName(i)
		ns := &NodeSpec{
			Qemu: map[string]*GuestSpec{}, Lxc: map[string]*GuestSpec{},
			Links: map[string]LinkInfo{}, Stats: map[string]IfaceStats{},
		}

		ns.Network = append(ns.Network, NetIface{Iface: "lo", Type: "loopback", Method: "loopback", Autostart: true})

		for j := 0; j < scaleProfileNicsPerNode; j++ {
			nic := fmt.Sprintf("eno%d", j+1)
			ns.Network = append(ns.Network, NetIface{Iface: nic, Type: "eth", Method: "manual", Autostart: true, MTU: 1500})
			mac := fmt.Sprintf("bc:24:%02x:%02x:00:%02x", (i+1)%256, j+1, j+1)
			ns.Links[nic] = LinkInfo{
				Mac: mac, Driver: "ixgbe", SpeedMbps: 10000, Duplex: "full",
				LinkUp: true, PCIAddr: fmt.Sprintf("0000:%02x:00.%d", (i+1)%256, j%2),
			}
			ns.Stats[nic] = IfaceStats{
				RxBytes:   uint64(3_000_000_000 + i*10_000_000 + j*1_000_000),
				TxBytes:   uint64(1_200_000_000 + i*5_000_000 + j*500_000),
				RxPackets: uint64(2_500_000 + j*10_000), TxPackets: uint64(1_400_000 + j*8_000),
			}
		}

		ns.Network = append(ns.Network, NetIface{
			Iface: "bond0", Type: "bond", Method: "manual", Autostart: true, MTU: 1500,
			Slaves: "eno1 eno2", BondMode: "802.3ad", Comments: "LACP mgmt/corosync uplink",
		})
		bond0Mac := fmt.Sprintf("bc:24:%02x:01:00:01", (i+1)%256)
		ns.Links["bond0"] = LinkInfo{Mac: bond0Mac, LinkUp: true}

		ns.Network = append(ns.Network, NetIface{
			Iface: "vmbr0", Type: "bridge", Method: "static", Autostart: true, MTU: 1500,
			Address: fmt.Sprintf("10.20.%d.%d/24", i/250, 11+i%250), Gateway: "10.20.0.1",
			BridgePorts: "bond0", BridgeVlanAware: false, Comments: "management + corosync",
		})
		ns.Links["vmbr0"] = LinkInfo{Mac: bond0Mac, LinkUp: true}
		ns.Stats["vmbr0"] = IfaceStats{RxBytes: 6_000_000_000, TxBytes: 2_400_000_000, RxPackets: 5_000_000, TxPackets: 2_800_000}

		ns.Network = append(ns.Network, NetIface{
			Iface: "bond1", Type: "bond", Method: "manual", Autostart: true, MTU: 1500,
			Slaves: "eno3 eno4", BondMode: "802.3ad", Comments: "LACP SDN overlay uplink",
		})
		bond1Mac := fmt.Sprintf("bc:24:%02x:01:00:03", (i+1)%256)
		ns.Links["bond1"] = LinkInfo{Mac: bond1Mac, LinkUp: true}

		ns.Network = append(ns.Network, NetIface{
			Iface: "vmbr1", Type: "bridge", Method: "manual", Autostart: true, MTU: 1500,
			BridgePorts: "bond1", BridgeVlanAware: true, Comments: "SDN VLAN zone bridge (scalez)",
		})
		ns.Links["vmbr1"] = LinkInfo{Mac: bond1Mac, LinkUp: true}
		ns.Stats["vmbr1"] = IfaceStats{RxBytes: 9_000_000_000, TxBytes: 3_600_000_000, RxPackets: 7_500_000, TxPackets: 4_100_000}

		for bi, b := range []string{"vmbr2", "vmbr3"} {
			ns.Network = append(ns.Network, NetIface{
				Iface: b, Type: "bridge", Method: "manual", Autostart: true, MTU: 1500,
				BridgeVlanAware: true, Comments: "guest access bridge (no uplink yet)",
			})
			ns.Links[b] = LinkInfo{Mac: fmt.Sprintf("bc:24:%02x:02:00:%02x", (i+1)%256, 10+bi), LinkUp: true}
			ns.Stats[b] = IfaceStats{RxBytes: 2_000_000_000, TxBytes: 800_000_000, RxPackets: 1_800_000, TxPackets: 900_000}
		}

		for j := 0; j < cfg.GuestsPerNode; j++ {
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
				vi := guestCounter % cfg.VNets
				bridge, tag = "vmbr1", vnetTags[vi]
			}

			isQemu := guestCounter%2 == 0
			name := fmt.Sprintf("scale-%d", vmid)
			if isQemu {
				ns.Qemu[fmt.Sprintf("%d", vmid)] = &GuestSpec{
					Name: name, Status: "running",
					Config: map[string]string{
						"name": name, "cores": "2", "memory": "2048",
						"net0":   fmt.Sprintf("virtio=%s,bridge=%s,tag=%d,firewall=1", mac, bridge, tag),
						"ostype": "l26",
					},
				}
			} else {
				ns.Lxc[fmt.Sprintf("%d", vmid)] = &GuestSpec{
					Name: name, Status: "running",
					Config: map[string]string{
						"hostname": name, "cores": "1", "memory": "512",
						"net0": fmt.Sprintf("name=eth0,bridge=%s,tag=%d,firewall=1,hwaddr=%s", bridge, tag, mac),
					},
				}
			}
			guestCounter++
		}

		ns.Firewall = &FirewallScope{Enabled: true, PolicyIn: "DROP", PolicyOut: "ACCEPT"}
		f.Nodes[node] = ns
	}

	wantGuests := cfg.Nodes * cfg.GuestsPerNode
	if guestCounter != wantGuests {
		return nil, fmt.Errorf("pvemock: scale profile generated %d guests, want %d", guestCounter, wantGuests)
	}

	allNodes := make([]string, cfg.Nodes)
	for i := range allNodes {
		allNodes[i] = nodeName(i)
	}
	f.SDN.Zones = []SDNZoneSpec{
		{ID: "scalez", Type: "vlan", Bridge: "vmbr1", Nodes: allNodes, MTU: 1500},
	}
	ipam := SDNIpamSpec{ID: "pve", Type: "pve"}
	for v := 0; v < cfg.VNets; v++ {
		vname := fmt.Sprintf("vnet%d", v)
		cidr := fmt.Sprintf("10.%d.%d.0/24", 150+v/250, v%250)
		gw := fmt.Sprintf("10.%d.%d.1", 150+v/250, v%250)
		f.SDN.Vnets = append(f.SDN.Vnets, SDNVnetSpec{
			ID: vname, Zone: "scalez", Tag: vnetTags[v], Alias: fmt.Sprintf("tier-%02d", v),
		})
		f.SDN.Subnets = append(f.SDN.Subnets, SDNSubnetSpec{
			ID: cidr, Vnet: vname, CIDR: cidr, Gateway: gw,
		})
		ipam.Entries = append(ipam.Entries, IPAMEntrySpec{
			Zone: "scalez", Vnet: vname, Subnet: cidr, IP: gw, Gateway: true,
		})
	}
	f.SDN.Ipams = []SDNIpamSpec{ipam}

	f.Firewall = FirewallSpec{
		Cluster: FirewallScope{
			Enabled: true, PolicyIn: "DROP", PolicyOut: "ACCEPT",
			Rules: []FwRuleSpec{
				{Pos: 0, Enabled: true, Type: "in", Action: "ACCEPT", Proto: "tcp", Dport: "22", Comment: "cluster-wide SSH"},
			},
		},
	}

	if err := f.Validate(); err != nil {
		return nil, fmt.Errorf("pvemock: generated scale profile (nodes=%d guests/node=%d vnets=%d) failed Validate: %w",
			cfg.Nodes, cfg.GuestsPerNode, cfg.VNets, err)
	}
	return f, nil
}
