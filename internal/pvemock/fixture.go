// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadFixture reads and validates a YAML cluster fixture from path. It
// returns ErrFixtureInvalid (wrapped, with detail) if the fixture fails
// referential-integrity validation — callers must never receive a Fixture
// that hasn't passed Validate.
func LoadFixture(path string) (*Fixture, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pvemock: reading fixture %s: %w", path, err)
	}
	var f Fixture
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("pvemock: parsing fixture %s: %w", path, err)
	}
	if err := f.Validate(); err != nil {
		return nil, fmt.Errorf("pvemock: fixture %s: %w", path, err)
	}
	return &f, nil
}

// Validate performs referential-integrity checks across the fixture: every
// pointer between entities (bridge port -> nic, bond slave -> nic, vlan
// parent -> iface, vnet -> zone, subnet -> vnet, cluster node <-> node spec)
// must resolve, or loading fails with a clear, specific error rather than
// silently producing a broken in-memory model.
//
// Cross-entity *drift* (an SDN zone listing a node whose bridge is missing,
// a firewall rule citing a deleted ipset, mismatched MTUs across nodes) is
// intentionally NOT rejected here: those are realistic brownfield states a
// running PVE cluster can be in, and are exactly what testdata/clusters/
// messy-brownfield.yaml exists to model for later drift-detection testing.
// Only structurally dangling references (pointers to names that were never
// defined anywhere) are treated as fixture bugs.
func (f *Fixture) Validate() error {
	var errs []string
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf(format, args...))
	}

	if f.Cluster.Name == "" {
		fail("cluster.name is required")
	}
	if len(f.Cluster.Nodes) == 0 {
		fail("cluster.nodes must list at least one node")
	}

	clusterNodes := map[string]bool{}
	for _, n := range f.Cluster.Nodes {
		if n.Name == "" {
			fail("cluster.nodes: entry with empty name")
			continue
		}
		if clusterNodes[n.Name] {
			fail("cluster.nodes: duplicate node %q", n.Name)
		}
		clusterNodes[n.Name] = true
	}
	for name := range clusterNodes {
		if _, ok := f.Nodes[name]; !ok {
			fail("cluster node %q has no corresponding entry under nodes:", name)
		}
	}
	for name := range f.Nodes {
		if !clusterNodes[name] {
			fail("nodes[%q] is not listed under cluster.nodes", name)
		}
	}

	if len(f.Users) == 0 {
		fail("users must list at least one user")
	}
	seenUsers := map[string]bool{}
	for _, u := range f.Users {
		if u.UserID == "" {
			fail("users: entry with empty userid")
			continue
		}
		if !strings.Contains(u.UserID, "@") {
			fail("users[%q]: userid must include a realm (e.g. user@pam)", u.UserID)
		}
		if seenUsers[u.UserID] {
			fail("users: duplicate userid %q", u.UserID)
		}
		seenUsers[u.UserID] = true
		if len(u.Privileges) == 0 {
			fail("users[%q]: privileges must not be empty (use [\"Sys.Audit\"] at minimum)", u.UserID)
		}
		seenTokens := map[string]bool{}
		for _, tok := range u.Tokens {
			if tok.TokenID == "" {
				fail("users[%q].tokens: entry with empty tokenid", u.UserID)
				continue
			}
			if seenTokens[tok.TokenID] {
				fail("users[%q].tokens: duplicate tokenid %q", u.UserID, tok.TokenID)
			}
			seenTokens[tok.TokenID] = true
			if tok.Secret == "" {
				fail("users[%q].tokens[%q]: secret must not be empty", u.UserID, tok.TokenID)
			}
		}
	}

	for nodeName, ns := range f.Nodes {
		validateNodeNetwork(nodeName, ns, ns.Network, "network", fail)
		validateNodeNetwork(nodeName, ns, ns.NetworkPending, "network_pending", fail)

		for iface := range ns.Links {
			if !ifaceExists(ns.Network, iface) && !ifaceExists(ns.NetworkPending, iface) {
				fail("nodes[%q].links[%q]: no matching network iface", nodeName, iface)
			}
		}
		for iface := range ns.LLDP {
			if !ifaceExists(ns.Network, iface) {
				fail("nodes[%q].lldp[%q]: no matching network iface", nodeName, iface)
			}
		}
		for iface := range ns.Stats {
			if !ifaceExists(ns.Network, iface) {
				fail("nodes[%q].stats[%q]: no matching network iface", nodeName, iface)
			}
		}
		for vmid, g := range ns.Qemu {
			if g == nil {
				fail("nodes[%q].qemu[%q]: empty guest spec", nodeName, vmid)
			}
		}
		for vmid, g := range ns.Lxc {
			if g == nil {
				fail("nodes[%q].lxc[%q]: empty guest spec", nodeName, vmid)
			}
		}
	}

	zoneIDs := map[string]bool{}
	for _, z := range f.SDN.Zones {
		if z.ID == "" {
			fail("sdn.zones: entry with empty id")
			continue
		}
		if zoneIDs[z.ID] {
			fail("sdn.zones: duplicate zone id %q", z.ID)
		}
		zoneIDs[z.ID] = true
		for _, n := range z.Nodes {
			if !clusterNodes[n] {
				fail("sdn.zones[%q]: references unknown node %q", z.ID, n)
			}
		}
	}
	vnetIDs := map[string]bool{}
	for _, v := range f.SDN.Vnets {
		if v.ID == "" {
			fail("sdn.vnets: entry with empty id")
			continue
		}
		if vnetIDs[v.ID] {
			fail("sdn.vnets: duplicate vnet id %q", v.ID)
		}
		vnetIDs[v.ID] = true
		if v.Zone == "" || !zoneIDs[v.Zone] {
			fail("sdn.vnets[%q]: references unknown zone %q", v.ID, v.Zone)
		}
	}
	subnetCIDRs := map[string]bool{}
	for _, s := range f.SDN.Subnets {
		if s.ID == "" {
			fail("sdn.subnets: entry with empty id")
			continue
		}
		if s.Vnet == "" || !vnetIDs[s.Vnet] {
			fail("sdn.subnets[%q]: references unknown vnet %q", s.ID, s.Vnet)
		}
		subnetCIDRs[s.CIDR] = true
	}

	ipamIDs := map[string]bool{}
	for _, ip := range f.SDN.Ipams {
		if ip.ID == "" {
			fail("sdn.ipams: entry with empty id")
			continue
		}
		if ipamIDs[ip.ID] {
			fail("sdn.ipams: duplicate ipam id %q", ip.ID)
		}
		ipamIDs[ip.ID] = true
		if ip.Type == "" {
			fail("sdn.ipams[%q]: type is required (pve|netbox|phpipam)", ip.ID)
		}
		for i, e := range ip.Entries {
			if e.IP == "" {
				fail("sdn.ipams[%q].entries[%d]: ip is required", ip.ID, i)
			}
			// Entries pointing at zone/vnet/subnet names that were never
			// defined are structural fixture bugs (dangling references),
			// per this function's doc comment.
			if e.Zone != "" && !zoneIDs[e.Zone] {
				fail("sdn.ipams[%q].entries[%d]: references unknown zone %q", ip.ID, i, e.Zone)
			}
			if e.Vnet != "" && !vnetIDs[e.Vnet] {
				fail("sdn.ipams[%q].entries[%d]: references unknown vnet %q", ip.ID, i, e.Vnet)
			}
			if e.Subnet != "" && !subnetCIDRs[e.Subnet] {
				fail("sdn.ipams[%q].entries[%d]: references unknown subnet CIDR %q", ip.ID, i, e.Subnet)
			}
		}
	}

	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("%w: %s", ErrFixtureInvalid, strings.Join(errs, "; "))
	}
	return nil
}

func ifaceExists(ifaces []NetIface, name string) bool {
	for _, i := range ifaces {
		if i.Iface == name {
			return true
		}
	}
	return false
}

// validateNodeNetwork checks that every bridge port, bond slave, and vlan
// parent within a single node's interface list (field, either "network" or
// "network_pending") resolves to a defined iface on that same node.
func validateNodeNetwork(nodeName string, _ *NodeSpec, ifaces []NetIface, field string, fail func(string, ...any)) {
	names := map[string]bool{}
	for _, i := range ifaces {
		if i.Iface == "" {
			fail("nodes[%q].%s: entry with empty iface name", nodeName, field)
			continue
		}
		if names[i.Iface] {
			fail("nodes[%q].%s: duplicate iface %q", nodeName, field, i.Iface)
		}
		names[i.Iface] = true
	}
	for _, i := range ifaces {
		switch i.Type {
		case "bridge", "OVSBridge":
			for _, port := range strings.Fields(i.BridgePorts) {
				if !names[port] {
					fail("nodes[%q].%s: bridge %q references non-existent port %q", nodeName, field, i.Iface, port)
				}
			}
		case "bond", "OVSBond":
			for _, slave := range strings.Fields(i.Slaves) {
				if !names[slave] {
					fail("nodes[%q].%s: bond %q references non-existent slave %q", nodeName, field, i.Iface, slave)
				}
			}
			// OVSBond's VlanRawDevice doubles as its ovs_bridge attachment
			// (see render.go's doc comment) — validated the same as
			// OVSIntPort's below.
			if i.Type == "OVSBond" && i.VlanRawDevice != "" && !names[i.VlanRawDevice] {
				fail("nodes[%q].%s: ovs bond %q references non-existent bridge %q", nodeName, field, i.Iface, i.VlanRawDevice)
			}
		case "vlan", "OVSIntPort":
			if i.VlanRawDevice != "" && !names[i.VlanRawDevice] {
				fail("nodes[%q].%s: vlan %q references non-existent parent %q", nodeName, field, i.Iface, i.VlanRawDevice)
			}
			if i.VlanID < 0 || i.VlanID > 4094 {
				fail("nodes[%q].%s: vlan %q has out-of-range vlan_id %d", nodeName, field, i.Iface, i.VlanID)
			}
		}
	}
}
