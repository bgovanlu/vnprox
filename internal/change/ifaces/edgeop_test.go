// SPDX-License-Identifier: Apache-2.0

package ifaces

import (
	"errors"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

const edgeTestFixture = `auto vmbr0
iface vmbr0 inet static
	address 203.0.113.10/24
	gateway 203.0.113.1
	bridge-ports eno1
	bridge-stp off
`

// TestMutateNatPortForwardCreate_GoldenDiff is T-1403 acceptance criterion
// 2: nat.portforward.create applies as a post-up/post-down stanza pair in
// /etc/network/interfaces, appended inside the named iface's own stanza —
// asserted as an exact rendered-diff (golden) comparison, not just a
// substring probe, so any change to the generated command shape is a
// visible, reviewed diff.
func TestMutateNatPortForwardCreate_GoldenDiff(t *testing.T) {
	f, err := host.ParseInterfaces([]byte(edgeTestFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	op := NatPortForwardCreate{
		Target: ref(inventory.KindNatRule, "pve1", "pf-web"), Iface: "vmbr0",
		Proto: "tcp", ExtPort: 8080, IntIP: "192.168.1.50", IntPort: 80,
	}
	if err := Mutate(f, op, "cs1"); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	got := f.Render()

	const want = `auto vmbr0
iface vmbr0 inet static
	address 203.0.113.10/24
	gateway 203.0.113.1
	bridge-ports eno1
	bridge-stp off
	post-up iptables -t nat -A PREROUTING -i vmbr0 -p tcp --dport 8080 -j DNAT --to-destination 192.168.1.50:80 # vnprox-edge:nat-portforward:extPort=8080&id=pf-web&iface=vmbr0&intIp=192.168.1.50&intPort=80&proto=tcp
	post-down iptables -t nat -D PREROUTING -i vmbr0 -p tcp --dport 8080 -j DNAT --to-destination 192.168.1.50:80 # vnprox-edge:nat-portforward:extPort=8080&id=pf-web&iface=vmbr0&intIp=192.168.1.50&intPort=80&proto=tcp
`
	if got != want {
		t.Errorf("rendered file mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestMutateNatMasqueradeCreate(t *testing.T) {
	f, err := host.ParseInterfaces([]byte(edgeTestFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	op := NatMasqueradeCreate{Target: ref(inventory.KindNatRule, "pve1", "masq1"), Iface: "vmbr0", SourceCIDR: "192.168.1.0/24"}
	if err := Mutate(f, op, "cs1"); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	got := f.Render()
	for _, want := range []string{
		"post-up iptables -t nat -A POSTROUTING -s 192.168.1.0/24 -o vmbr0 -j MASQUERADE",
		"post-down iptables -t nat -D POSTROUTING -s 192.168.1.0/24 -o vmbr0 -j MASQUERADE",
		"# vnprox-edge:nat-masquerade:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestMutateRouteStaticCreate(t *testing.T) {
	f, err := host.ParseInterfaces([]byte(edgeTestFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	op := RouteStaticCreate{
		Target: ref(inventory.KindStaticRoute, "pve1", "lab"), Iface: "vmbr0",
		DestCIDR: "10.10.0.0/24", Gateway: "203.0.113.5", Metric: 50,
	}
	if err := Mutate(f, op, "cs1"); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	got := f.Render()
	for _, want := range []string{
		"post-up ip route add 10.10.0.0/24 via 203.0.113.5 dev vmbr0 metric 50",
		"post-down ip route del 10.10.0.0/24 via 203.0.113.5 dev vmbr0 metric 50",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

// TestMutateNatPortForwardDelete_Reversible is the "reversible on rollback"
// half of acceptance criterion 2: deleting the rule created above removes
// both generated lines and leaves the file byte-identical to before the
// create — the same guarantee a real rollback (restore pre-apply snapshot)
// relies on being achievable by inverse ops, and what MutateAll's
// create-then-delete round trip inside one changeset must also produce.
func TestMutateNatPortForwardDelete_Reversible(t *testing.T) {
	f, err := host.ParseInterfaces([]byte(edgeTestFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	create := NatPortForwardCreate{
		Target: ref(inventory.KindNatRule, "pve1", "pf-web"), Iface: "vmbr0",
		Proto: "tcp", ExtPort: 8080, IntIP: "192.168.1.50", IntPort: 80,
	}
	del := NatPortForwardDelete{Target: ref(inventory.KindNatRule, "pve1", "pf-web")}
	if err := MutateAll(f, []Op{create, del}, "cs1"); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if got := f.Render(); got != edgeTestFixture {
		t.Errorf("create+delete round trip not byte-identical to original:\n--- got ---\n%s\n--- want ---\n%s", got, edgeTestFixture)
	}
}

func TestMutateNatPortForwardUpdate_MergesStoredFields(t *testing.T) {
	f, err := host.ParseInterfaces([]byte(edgeTestFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	create := NatPortForwardCreate{
		Target: ref(inventory.KindNatRule, "pve1", "pf-web"), Iface: "vmbr0",
		Proto: "tcp", ExtPort: 8080, IntIP: "192.168.1.50", IntPort: 80, Comment: "web",
	}
	newPort := 9090
	update := NatPortForwardUpdate{Target: ref(inventory.KindNatRule, "pve1", "pf-web"), ExtPort: &newPort}
	if err := MutateAll(f, []Op{create, update}, "cs1"); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	got := f.Render()
	if !strings.Contains(got, "--dport 9090") {
		t.Errorf("updated extPort not applied:\n%s", got)
	}
	if strings.Contains(got, "--dport 8080") {
		t.Errorf("old extPort still present after update:\n%s", got)
	}
	// Fields the update didn't touch (proto, intIp, comment) survive the
	// merge unchanged.
	if !strings.Contains(got, "192.168.1.50:80") || !strings.Contains(got, "-p tcp") {
		t.Errorf("untouched fields lost across update:\n%s", got)
	}
}

func TestMutateEdgeOps_NotFoundErrors(t *testing.T) {
	cases := []Op{
		NatMasqueradeCreate{Target: ref(inventory.KindNatRule, "pve1", "m1"), Iface: "missing", SourceCIDR: "10.0.0.0/24"},
		NatMasqueradeDelete{Target: ref(inventory.KindNatRule, "pve1", "nonexistent")},
		NatPortForwardCreate{Target: ref(inventory.KindNatRule, "pve1", "p1"), Iface: "missing", Proto: "tcp", ExtPort: 80, IntIP: "10.0.0.1", IntPort: 80},
		NatPortForwardUpdate{Target: ref(inventory.KindNatRule, "pve1", "nonexistent")},
		NatPortForwardDelete{Target: ref(inventory.KindNatRule, "pve1", "nonexistent")},
		RouteStaticCreate{Target: ref(inventory.KindStaticRoute, "pve1", "r1"), Iface: "missing", DestCIDR: "10.0.0.0/24", Gateway: "10.0.0.1"},
		RouteStaticUpdate{Target: ref(inventory.KindStaticRoute, "pve1", "nonexistent")},
		RouteStaticDelete{Target: ref(inventory.KindStaticRoute, "pve1", "nonexistent")},
	}
	for _, op := range cases {
		fresh, err := host.ParseInterfaces([]byte(edgeTestFixture))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if err := Mutate(fresh, op, "cs1"); !errors.Is(err, ErrNotFound) {
			t.Errorf("%T: err = %v, want ErrNotFound", op, err)
		}
	}
}
