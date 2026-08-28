// SPDX-License-Identifier: Apache-2.0

package change

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// edgeTestSnapshot is a one-node snapshot with a single bridge (vmbr0)
// carrying an address on 203.0.113.0/24 — the "known interface" T-1403
// acceptance criterion 3's referential gateway-reachability check needs.
func edgeTestSnapshot() inventory.Snapshot {
	return buildSnapshot(
		&inventory.PhysNic{Ref: inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}, Name: "eno1", LinkUp: true},
		&inventory.Bridge{
			Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0",
			Addresses: []string{"203.0.113.10/24"},
		},
	)
}

// TestValidate_RouteStaticCreate is T-1403 acceptance criterion 3's table
// test: schema class catches a malformed CIDR/gateway, referential class
// catches an unreachable gateway (the rejection case) and a nonexistent
// iface, and a fully valid op passes clean.
func TestValidate_RouteStaticCreate(t *testing.T) {
	snap := edgeTestSnapshot()
	target := testRef(inventory.KindStaticRoute, "pve1", "lab-route")

	cases := []struct {
		name   string
		params *RouteStaticCreateParams
		want   []wantFinding
	}{
		{
			name:   "valid",
			params: &RouteStaticCreateParams{Iface: "vmbr0", DestCIDR: "10.10.0.0/24", Gateway: "203.0.113.1"},
			want:   nil,
		},
		{
			name:   "invalid destCidr",
			params: &RouteStaticCreateParams{Iface: "vmbr0", DestCIDR: "not-a-cidr", Gateway: "203.0.113.1"},
			want:   []wantFinding{{sev: SeverityError, code: codeCIDRInvalid, ref: target.String()}},
		},
		{
			name:   "invalid gateway ip",
			params: &RouteStaticCreateParams{Iface: "vmbr0", DestCIDR: "10.10.0.0/24", Gateway: "not-an-ip"},
			want:   []wantFinding{{sev: SeverityError, code: codeIPInvalid, ref: target.String()}},
		},
		{
			name:   "missing iface",
			params: &RouteStaticCreateParams{DestCIDR: "10.10.0.0/24", Gateway: "203.0.113.1"},
			want:   []wantFinding{{sev: SeverityError, code: codeRequiredFieldMissing, ref: target.String()}},
		},
		{
			name:   "iface not found (referential rejection)",
			params: &RouteStaticCreateParams{Iface: "vmbr9", DestCIDR: "10.10.0.0/24", Gateway: "203.0.113.1"},
			want:   []wantFinding{{sev: SeverityError, code: codeIfaceNotFound, ref: target.String()}},
		},
		{
			name:   "gateway unreachable via any known interface (referential rejection)",
			params: &RouteStaticCreateParams{Iface: "vmbr0", DestCIDR: "10.10.0.0/24", Gateway: "198.51.100.1"},
			want:   []wantFinding{{sev: SeverityError, code: codeRouteGatewayUnreachable, ref: target.String()}},
		},
		{
			name:   "negative metric",
			params: &RouteStaticCreateParams{Iface: "vmbr0", DestCIDR: "10.10.0.0/24", Gateway: "203.0.113.1", Metric: -1},
			want:   []wantFinding{{sev: SeverityError, code: codeRequiredFieldMissing, ref: target.String()}},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Validate([]Op{mkOp(OpRouteStaticCreate, target, c.params)}, snap)
			assertFindings(t, got, c.want)
		})
	}
}

func TestValidate_NatMasqueradeCreate(t *testing.T) {
	snap := edgeTestSnapshot()
	target := testRef(inventory.KindNatRule, "pve1", "masq1")

	cases := []struct {
		name   string
		params *NatMasqueradeCreateParams
		want   []wantFinding
	}{
		{
			name:   "valid",
			params: &NatMasqueradeCreateParams{Iface: "vmbr0", SourceCIDR: "192.168.1.0/24"},
			want:   nil,
		},
		{
			name:   "invalid sourceCidr",
			params: &NatMasqueradeCreateParams{Iface: "vmbr0", SourceCIDR: "bogus"},
			want:   []wantFinding{{sev: SeverityError, code: codeCIDRInvalid, ref: target.String()}},
		},
		{
			name:   "iface not found",
			params: &NatMasqueradeCreateParams{Iface: "missing", SourceCIDR: "192.168.1.0/24"},
			want:   []wantFinding{{sev: SeverityError, code: codeIfaceNotFound, ref: target.String()}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Validate([]Op{mkOp(OpNatMasqueradeCreate, target, c.params)}, snap)
			assertFindings(t, got, c.want)
		})
	}
}

func TestValidate_NatPortForwardCreate(t *testing.T) {
	snap := edgeTestSnapshot()
	target := testRef(inventory.KindNatRule, "pve1", "pf1")

	cases := []struct {
		name   string
		params *NatPortForwardCreateParams
		want   []wantFinding
	}{
		{
			name:   "valid",
			params: &NatPortForwardCreateParams{Iface: "vmbr0", Proto: "tcp", ExtPort: 8080, IntIP: "192.168.1.50", IntPort: 80},
			want:   nil,
		},
		{
			name:   "invalid proto",
			params: &NatPortForwardCreateParams{Iface: "vmbr0", Proto: "icmp", ExtPort: 8080, IntIP: "192.168.1.50", IntPort: 80},
			want:   []wantFinding{{sev: SeverityError, code: codeNatProtoInvalid, ref: target.String()}},
		},
		{
			name:   "port out of range",
			params: &NatPortForwardCreateParams{Iface: "vmbr0", Proto: "tcp", ExtPort: 70000, IntIP: "192.168.1.50", IntPort: 80},
			want:   []wantFinding{{sev: SeverityError, code: codePortNumberInvalid, ref: target.String()}},
		},
		{
			name:   "invalid intIp",
			params: &NatPortForwardCreateParams{Iface: "vmbr0", Proto: "tcp", ExtPort: 8080, IntIP: "not-an-ip", IntPort: 80},
			want:   []wantFinding{{sev: SeverityError, code: codeIPInvalid, ref: target.String()}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Validate([]Op{mkOp(OpNatPortForwardCreate, target, c.params)}, snap)
			assertFindings(t, got, c.want)
		})
	}
}

// TestValidate_EdgeRuleIDInvalid covers the schema.edge_rule_id_invalid
// code shared by every T-1403 create op's target id.
func TestValidate_EdgeRuleIDInvalid(t *testing.T) {
	snap := edgeTestSnapshot()
	target := testRef(inventory.KindNatRule, "pve1", "")
	params := &NatMasqueradeCreateParams{Iface: "vmbr0", SourceCIDR: "192.168.1.0/24"}
	got := Validate([]Op{mkOp(OpNatMasqueradeCreate, target, params)}, snap)
	assertFindings(t, got, []wantFinding{{sev: SeverityError, code: codeEdgeRuleIDInvalid, ref: target.String()}})
}
