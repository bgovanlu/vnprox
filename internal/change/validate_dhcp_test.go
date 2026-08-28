// SPDX-License-Identifier: Apache-2.0

package change

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestAdvisoryValidate_DHCPRangeOverlap is T-406 acceptance criterion 4:
// "A DHCP range overlapping existing allocations -> validation warning
// listing the specific overlapping allocations."
func TestAdvisoryValidate_DHCPRangeOverlap(t *testing.T) {
	subnet := testRef(inventory.KindSDNSubnet, "", "10.70.0.0/24")
	vnet := testRef(inventory.KindSDNVnet, "", "zone1/vnet1")

	// createSnap has the owning vnet (referentialValidate's vnetNotFound
	// check needs it) but not the subnet itself, for sdn.subnet.create.
	createSnap := buildSnapshot(
		&inventory.SdnZone{Ref: testRef(inventory.KindSDNZone, "", "zone1"), ID: "zone1", Type: "simple"},
		&inventory.SdnVnet{Ref: vnet, ID: "zone1/vnet1", Zone: "zone1"},
	)
	// updateSnap additionally has the subnet itself (referentialValidate's
	// target-exists check needs it), for sdn.subnet.update.
	updateSnap := buildSnapshot(
		&inventory.SdnZone{Ref: testRef(inventory.KindSDNZone, "", "zone1"), ID: "zone1", Type: "simple"},
		&inventory.SdnVnet{Ref: vnet, ID: "zone1/vnet1", Zone: "zone1"},
		&inventory.SdnSubnet{Ref: subnet, ID: "10.70.0.0/24", Vnet: "zone1/vnet1"},
	)

	allocations := []DHCPRangeAllocation{
		{Subnet: "10.70.0.0/24", IP: "10.70.0.120", Hostname: "web01"},
		{Subnet: "10.70.0.0/24", IP: "10.70.0.130", MAC: "aa:bb:cc:dd:ee:ff"},
		{Subnet: "10.70.0.0/24", IP: "10.70.0.5"},   // outside the range below
		{Subnet: "10.99.0.0/24", IP: "10.70.0.125"}, // different subnet, same address space by coincidence
	}

	t.Run("range overlapping two allocations warns and lists both", func(t *testing.T) {
		ops := []Op{mkOp(OpSdnSubnetCreate, subnet, &SdnSubnetCreateParams{
			Vnet: "zone1/vnet1", CIDR: "10.70.0.0/24", DHCPRanges: []string{"10.70.0.100-10.70.0.150"},
		})}
		findings := ValidateWithSafety(ops, createSnap, SafetyOptions{Allocations: allocations})
		assertFindings(t, findings, []wantFinding{{SeverityWarning, codeAdvisoryDHCPRangeOverlap, subnet.String()}})
		msg := findings[0].Message
		if !containsAll(msg, "10.70.0.120", "web01", "10.70.0.130", "aa:bb:cc:dd:ee:ff") {
			t.Errorf("message %q does not name both overlapping allocations", msg)
		}
	})

	t.Run("range with no overlap is clean", func(t *testing.T) {
		ops := []Op{mkOp(OpSdnSubnetCreate, subnet, &SdnSubnetCreateParams{
			Vnet: "zone1/vnet1", CIDR: "10.70.0.0/24", DHCPRanges: []string{"10.70.0.200-10.70.0.250"},
		})}
		findings := ValidateWithSafety(ops, createSnap, SafetyOptions{Allocations: allocations})
		assertFindings(t, findings, nil)
	})

	t.Run("allocation in a different subnet is not counted", func(t *testing.T) {
		ops := []Op{mkOp(OpSdnSubnetCreate, subnet, &SdnSubnetCreateParams{
			Vnet: "zone1/vnet1", CIDR: "10.70.0.0/24", DHCPRanges: []string{"10.70.0.121-10.70.0.129"},
		})}
		findings := ValidateWithSafety(ops, createSnap, SafetyOptions{Allocations: allocations})
		assertFindings(t, findings, nil)
	})

	t.Run("sdn.subnet.update range overlap warns the same way", func(t *testing.T) {
		ops := []Op{mkOp(OpSdnSubnetUpdate, subnet, &SdnSubnetUpdateParams{
			DHCPRanges: &[]string{"10.70.0.100-10.70.0.150"},
		})}
		findings := ValidateWithSafety(ops, updateSnap, SafetyOptions{Allocations: allocations})
		assertFindings(t, findings, []wantFinding{{SeverityWarning, codeAdvisoryDHCPRangeOverlap, subnet.String()}})
	})

	t.Run("no allocations wired is clean (nil Allocations never warns)", func(t *testing.T) {
		ops := []Op{mkOp(OpSdnSubnetCreate, subnet, &SdnSubnetCreateParams{
			Vnet: "zone1/vnet1", CIDR: "10.70.0.0/24", DHCPRanges: []string{"10.70.0.100-10.70.0.150"},
		})}
		findings := ValidateWithSafety(ops, createSnap, SafetyOptions{})
		assertFindings(t, findings, nil)
	})
}
