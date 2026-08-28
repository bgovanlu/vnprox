// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
)

// Safety interlocks (T-203): deleting a protected interface is a hard error,
// downgraded to a warning under allow_dangerous_ops.
func TestSafetyInterlock_ProtectedDeleteAndOverride(t *testing.T) {
	snap := populatedSnapshot()
	protected := change.ProtectedSet{
		"pve1": {bridgeR("vmbr0")},
	}
	del := change.Op{Type: change.OpBridgeDelete, Target: bridgeR("vmbr0"), Params: &change.BridgeDeleteParams{}}

	findings := change.ValidateWithSafety([]change.Op{del}, snap, change.SafetyOptions{Protected: protected})
	if !hasCode(findings, "safety.protected_interface", change.SeverityError) {
		t.Fatalf("expected protected_interface error, got %+v", findings)
	}

	// allow_dangerous_ops downgrades to a warning (same code, lower severity).
	relaxed := change.ValidateWithSafety([]change.Op{del}, snap, change.SafetyOptions{Protected: protected, AllowDangerousOps: true})
	if !hasCode(relaxed, "safety.protected_interface", change.SeverityWarning) {
		t.Fatalf("expected protected_interface warning under allow_dangerous_ops, got %+v", relaxed)
	}
}

// Address overlap and SDN sibling-subnet overlap referential checks.
func TestReferential_OverlapChecks(t *testing.T) {
	snap := populatedSnapshot()

	// A new bridge declaring an address already used on the node overlaps.
	overlap := change.Op{Type: change.OpBridgeCreate, Target: bridgeR("vmbrO"), Params: &change.BridgeCreateParams{
		Addresses: []string{"192.168.1.10/24"},
	}}
	if !hasCode(change.Validate([]change.Op{overlap}, snap), "referential.address_overlap", change.SeverityError) {
		t.Errorf("expected address_overlap error")
	}

	// Two subnets in the same vnet with overlapping CIDRs (intra-changeset).
	sib1 := change.Op{Type: change.OpSdnSubnetCreate, Target: subnetR("10.5.0.0/16"), Params: &change.SdnSubnetCreateParams{Vnet: "zone1/vnet1", CIDR: "10.5.0.0/16"}}
	sib2 := change.Op{Type: change.OpSdnSubnetCreate, Target: subnetR("10.5.1.0/24"), Params: &change.SdnSubnetCreateParams{Vnet: "zone1/vnet1", CIDR: "10.5.1.0/24"}}
	fs := change.Validate([]change.Op{sib1, sib2}, snap)
	if !hasCode(fs, "referential.address_overlap", change.SeverityError) {
		t.Errorf("expected sibling-subnet overlap error, got %+v", fs)
	}

	// IPAM allocation into an existing subnet, plus an intra-changeset dup.
	a1 := change.Op{Type: change.OpIpamAllocCreate, Target: subnetR("10.0.0.0/24"), Params: &change.IpamAllocCreateParams{CIDR: "10.0.0.5/32"}}
	a2 := change.Op{Type: change.OpIpamAllocCreate, Target: subnetR("10.0.0.0/24"), Params: &change.IpamAllocCreateParams{CIDR: "10.0.0.5/32"}}
	_ = change.Validate([]change.Op{a1}, snap) // exercise the single-alloc path
	if !hasCode(change.Validate([]change.Op{a1, a2}, snap), "referential.address_overlap", change.SeverityError) {
		t.Errorf("expected intra-changeset ipam alloc overlap")
	}

	// Firewall named-object intra-changeset name collision (alias/ipset/group).
	al1 := change.Op{Type: change.OpFwAliasCreate, Target: rulesetRef(), Params: &change.FwAliasCreateParams{Name: "office", CIDR: "10.0.0.0/24"}}
	al2 := change.Op{Type: change.OpFwAliasCreate, Target: rulesetRef(), Params: &change.FwAliasCreateParams{Name: "office", CIDR: "10.0.1.0/24"}}
	if len(change.Validate([]change.Op{al1, al2}, snap)) == 0 {
		t.Errorf("expected fw alias name collision finding")
	}
}

// Advisory-class warnings (T-202 class 5) run when there are no blocking
// errors: single-slave 802.3ad bond without a layer3+4 hash, and a bridge
// with no description.
func TestAdvisoryValidators(t *testing.T) {
	snap := populatedSnapshot()

	bond := change.Op{Type: change.OpBondCreate, Target: bondR("bondAdv"), Params: &change.BondCreateParams{
		Mode: "802.3ad", Slaves: []string{"eno2"}, // single slave, no xmit hash policy
	}}
	bf := change.Validate([]change.Op{bond}, snap)
	if !hasCode(bf, "advisory.bond_single_slave", change.SeverityWarning) &&
		!hasCode(bf, "advisory.bond_missing_layer34_hash", change.SeverityWarning) {
		t.Errorf("expected bond advisory warnings, got %+v", bf)
	}

	bridge := change.Op{Type: change.OpBridgeCreate, Target: bridgeR("vmbrAdv"), Params: &change.BridgeCreateParams{
		Ports: []string{"eno2"}, // no Comments
	}}
	if !hasCode(change.Validate([]change.Op{bridge}, snap), "advisory.bridge_missing_comment", change.SeverityWarning) {
		t.Errorf("expected bridge missing-comment advisory")
	}
}

func hasCode(findings []change.Finding, code string, sev change.Severity) bool {
	for _, f := range findings {
		if f.Code == code && f.Severity == sev {
			return true
		}
	}
	return false
}
