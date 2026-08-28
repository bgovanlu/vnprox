// SPDX-License-Identifier: Apache-2.0

package pvemock

// Regression coverage for T-2502-followup-01: pvemock's list endpoints were
// building their JSON arrays by ranging directly over Go maps, whose
// iteration order is deliberately randomized by the runtime — not just
// once per process, but freshly on every `range` statement execution. That
// made every response from these endpoints a coin flip, which in turn made
// any test (or recorded cassette) that compared such a list byte-for-byte
// against a golden value flaky by construction.
//
// The tests below prove two things per endpoint: (1) it now returns the
// *same* order on every call in one process, over enough calls and
// elements that a merely-lucky pass is not credible, and (2) that order is
// the documented one (ascending by the resource's own id/name, or by
// numeric vmid for guests) rather than some other stable-but-arbitrary
// order.

import (
	"fmt"
	"sort"
	"testing"
)

// buildOrderFixture returns a synthetic fixture with nNodes nodes (each
// carrying guestsPerNode qemu and guestsPerNode lxc guests) and nSDN SDN
// zones/vnets/subnets/dns-zones, built entirely in memory — no
// testdata/clusters file is read or written. The element counts are large
// enough (double digits per collection) that the number of possible
// orderings vastly exceeds what repeated calls could agree on by chance:
// with n items there are n! possible map-iteration orders, so two
// independent unsorted range calls landing on the same order — let alone
// dozens of them all agreeing with each other AND with the one correct
// sorted order — is not a realistic coincidence.
func buildOrderFixture(nNodes, guestsPerNode, nSDN int) *Fixture {
	f := &Fixture{
		Cluster: ClusterSpec{Name: "order-test", Quorate: true},
		Nodes:   map[string]*NodeSpec{},
		Users:   []UserSpec{{UserID: "root@pam", Password: "x", Privileges: []string{"*"}}},
	}
	for i := 0; i < nNodes; i++ {
		name := fmt.Sprintf("node%02d", i)
		f.Cluster.Nodes = append(f.Cluster.Nodes, ClusterNodeSpec{
			Name: name, IP: fmt.Sprintf("10.0.0.%d", i+1), Online: true,
		})
		ns := &NodeSpec{Qemu: map[string]*GuestSpec{}, Lxc: map[string]*GuestSpec{}}
		for g := 0; g < guestsPerNode; g++ {
			qvmid := fmt.Sprintf("%d", 100+i*guestsPerNode+g)
			lvmid := fmt.Sprintf("%d", 500+i*guestsPerNode+g)
			ns.Qemu[qvmid] = &GuestSpec{Name: "qemu-" + qvmid, Status: "running"}
			ns.Lxc[lvmid] = &GuestSpec{Name: "lxc-" + lvmid, Status: "running"}
		}
		f.Nodes[name] = ns
	}
	for i := 0; i < nSDN; i++ {
		f.SDN.Zones = append(f.SDN.Zones, SDNZoneSpec{ID: fmt.Sprintf("zone%02d", i), Type: "simple"})
	}
	for i := 0; i < nSDN; i++ {
		f.SDN.Vnets = append(f.SDN.Vnets, SDNVnetSpec{ID: fmt.Sprintf("vnet%02d", i), Zone: "zone00"})
	}
	for i := 0; i < nSDN; i++ {
		f.SDN.Subnets = append(f.SDN.Subnets, SDNSubnetSpec{
			ID: fmt.Sprintf("subnet%02d", i), Vnet: "vnet00", CIDR: fmt.Sprintf("10.%d.0.0/24", i),
		})
	}
	for i := 0; i < nSDN; i++ {
		f.SDN.DNSZones = append(f.SDN.DNSZones, SDNDnsZoneSpec{ID: fmt.Sprintf("dns%02d", i), Type: "powerdns"})
	}
	return f
}

// identicalOrder reports whether a and b hold the same elements in the same
// order. Unlike cassettes_test.go's equalStrings (which sorts both sides
// first, i.e. checks set equality) this is deliberately order-sensitive:
// order is exactly the property under test here.
func identicalOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// assertStableOrder calls get() repeats times and fails the test unless
// every call returns exactly want, in order. It's the harness shared by
// every sub-test below.
func assertStableOrder(t *testing.T, label string, repeats int, want []string, get func() []string) {
	t.Helper()
	for i := 0; i < repeats; i++ {
		got := get()
		if !identicalOrder(got, want) {
			t.Fatalf("%s: run %d/%d order = %v, want %v", label, i, repeats, got, want)
		}
	}
}

func TestListEndpoints_DeterministicOrder(t *testing.T) {
	const (
		nNodes        = 14
		guestsPerNode = 6
		nSDN          = 18
		repeats       = 40
	)
	f := buildOrderFixture(nNodes, guestsPerNode, nSDN)
	srv := NewServer(f)
	ticket, _ := login(t, srv, "root@pam", "x")

	t.Run("cluster/resources", func(t *testing.T) {
		// Documented order: node resources in cluster.nodes (fixture)
		// order, then per node — nodes visited alphabetically — that
		// node's qemu guests by ascending numeric vmid, then its lxc
		// guests by ascending numeric vmid.
		var want []string
		for _, n := range f.Cluster.Nodes {
			want = append(want, "node/"+n.Name)
		}
		nodeNames := make([]string, 0, len(f.Nodes))
		for name := range f.Nodes {
			nodeNames = append(nodeNames, name)
		}
		sort.Strings(nodeNames)
		for _, name := range nodeNames {
			ns := f.Nodes[name]
			qemuIDs := make([]string, 0, len(ns.Qemu))
			for vmid := range ns.Qemu {
				qemuIDs = append(qemuIDs, vmid)
			}
			sort.Slice(qemuIDs, func(i, j int) bool { return atoiOr(qemuIDs[i], 0) < atoiOr(qemuIDs[j], 0) })
			for _, vmid := range qemuIDs {
				want = append(want, "qemu/"+vmid)
			}
			lxcIDs := make([]string, 0, len(ns.Lxc))
			for vmid := range ns.Lxc {
				lxcIDs = append(lxcIDs, vmid)
			}
			sort.Slice(lxcIDs, func(i, j int) bool { return atoiOr(lxcIDs[i], 0) < atoiOr(lxcIDs[j], 0) })
			for _, vmid := range lxcIDs {
				want = append(want, "lxc/"+vmid)
			}
		}

		wantCount := nNodes + nNodes*guestsPerNode*2
		if len(want) != wantCount {
			t.Fatalf("test setup: computed %d expected resources, want %d", len(want), wantCount)
		}

		assertStableOrder(t, "/cluster/resources", repeats, want, func() []string {
			got := decodeData[[]clusterResource](t, srv, ticket, "/api2/json/cluster/resources")
			ids := make([]string, len(got))
			for i, r := range got {
				ids[i] = r.ID
			}
			return ids
		})
	})

	t.Run("cluster/sdn/zones", func(t *testing.T) {
		var want []string
		for i := 0; i < nSDN; i++ {
			want = append(want, fmt.Sprintf("zone%02d", i))
		}
		sort.Strings(want) // documented key: zone ID, ascending

		assertStableOrder(t, "/cluster/sdn/zones", repeats, want, func() []string {
			got := decodeData[[]SDNZoneSpec](t, srv, ticket, "/api2/json/cluster/sdn/zones")
			ids := make([]string, len(got))
			for i, z := range got {
				ids[i] = z.ID
			}
			return ids
		})
	})

	t.Run("cluster/sdn/vnets", func(t *testing.T) {
		var want []string
		for i := 0; i < nSDN; i++ {
			want = append(want, fmt.Sprintf("vnet%02d", i))
		}
		sort.Strings(want) // documented key: vnet ID, ascending

		assertStableOrder(t, "/cluster/sdn/vnets", repeats, want, func() []string {
			got := decodeData[[]SDNVnetSpec](t, srv, ticket, "/api2/json/cluster/sdn/vnets")
			ids := make([]string, len(got))
			for i, v := range got {
				ids[i] = v.ID
			}
			return ids
		})
	})

	t.Run("cluster/sdn/vnets/{vnet}/subnets", func(t *testing.T) {
		var want []string
		for i := 0; i < nSDN; i++ {
			want = append(want, fmt.Sprintf("subnet%02d", i))
		}
		sort.Strings(want) // documented key: subnet ID, ascending

		assertStableOrder(t, "/cluster/sdn/vnets/vnet00/subnets", repeats, want, func() []string {
			got := decodeData[[]SDNSubnetSpec](t, srv, ticket, "/api2/json/cluster/sdn/vnets/vnet00/subnets")
			ids := make([]string, len(got))
			for i, s := range got {
				ids[i] = s.ID
			}
			return ids
		})
	})

	t.Run("cluster/sdn/dns", func(t *testing.T) {
		var want []string
		for i := 0; i < nSDN; i++ {
			want = append(want, fmt.Sprintf("dns%02d", i))
		}
		sort.Strings(want) // documented key: dns zone ID, ascending

		assertStableOrder(t, "/cluster/sdn/dns", repeats, want, func() []string {
			got := decodeData[[]SDNDnsZoneSpec](t, srv, ticket, "/api2/json/cluster/sdn/dns")
			ids := make([]string, len(got))
			for i, z := range got {
				ids[i] = z.ID
			}
			return ids
		})
	})

	t.Run("cluster/sdn (status)", func(t *testing.T) {
		var zoneIDs, vnetIDs, subnetIDs []string
		for i := 0; i < nSDN; i++ {
			zoneIDs = append(zoneIDs, fmt.Sprintf("zone%02d", i))
			vnetIDs = append(vnetIDs, fmt.Sprintf("vnet%02d", i))
			subnetIDs = append(subnetIDs, fmt.Sprintf("subnet%02d", i))
		}
		sort.Strings(zoneIDs)
		sort.Strings(vnetIDs)
		sort.Strings(subnetIDs)
		var want []string
		want = append(want, zoneIDs...)
		want = append(want, vnetIDs...)
		want = append(want, subnetIDs...)

		type statusEntry struct {
			Kind string `json:"type"`
			ID   string `json:"id"`
		}
		assertStableOrder(t, "/cluster/sdn (status)", repeats, want, func() []string {
			got := decodeData[[]statusEntry](t, srv, ticket, "/api2/json/cluster/sdn")
			ids := make([]string, len(got))
			for i, e := range got {
				ids[i] = e.ID
			}
			return ids
		})
	})
}
