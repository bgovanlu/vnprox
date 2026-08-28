// SPDX-License-Identifier: Apache-2.0

package change

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

func vfPFTestRef() inventory.Ref {
	return inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
}

// TestSchemaValidate_VFProvision is T-1506's schema-class table test: a
// malformed Count/VFs shape (both set, neither set, negative count, a MAC
// shared across more than one freshly-numbered VF, an out-of-range VLAN, an
// invalid MAC) each produce a codeVFPlanInvalid/codeVIDOutOfRange/
// codeMACInvalid finding; a well-formed op is clean.
func TestSchemaValidate_VFProvision(t *testing.T) {
	tests := []struct {
		params  *VFProvisionParams
		name    string
		wantErr bool
	}{
		{name: "well-formed count mode", params: &VFProvisionParams{Count: 2, VLAN: 100}, wantErr: false},
		{name: "well-formed explicit vfs", params: &VFProvisionParams{VFs: []VFSpec{{ID: 0, VLAN: 100}}}, wantErr: false},
		{name: "both count and vfs set", params: &VFProvisionParams{Count: 1, VFs: []VFSpec{{ID: 0}}}, wantErr: true},
		{name: "neither count nor vfs set", params: &VFProvisionParams{}, wantErr: true},
		{name: "negative count", params: &VFProvisionParams{Count: -1}, wantErr: true},
		{name: "macAddr with count > 1", params: &VFProvisionParams{Count: 2, MacAddr: "aa:bb:cc:dd:ee:ff"}, wantErr: true},
		{name: "vlan out of range", params: &VFProvisionParams{Count: 1, VLAN: 5000}, wantErr: true},
		{name: "invalid top-level macAddr", params: &VFProvisionParams{Count: 1, MacAddr: "not-a-mac"}, wantErr: true},
		{name: "invalid per-vf macAddr", params: &VFProvisionParams{VFs: []VFSpec{{ID: 0, MacAddr: "nope"}}}, wantErr: true},
		{name: "duplicate vf id", params: &VFProvisionParams{VFs: []VFSpec{{ID: 0}, {ID: 0}}}, wantErr: true},
		{name: "negative vf id", params: &VFProvisionParams{VFs: []VFSpec{{ID: -1}}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := buildSnapshot(&inventory.PhysNic{Ref: vfPFTestRef(), Name: "eno1"})
			op := mkOp(OpVFProvision, vfPFTestRef(), tt.params)
			findings := Validate([]Op{op}, snap)
			if got := hasError(findings); got != tt.wantErr {
				t.Fatalf("hasError = %v, want %v (findings: %+v)", got, tt.wantErr, findings)
			}
		})
	}
}

// TestValidate_VFProvision_PFNotFound is the referential-class check: a
// vf.provision op whose Target does not resolve to an existing physnic is
// blocked with codePFNotFound.
func TestValidate_VFProvision_PFNotFound(t *testing.T) {
	snap := buildSnapshot() // no physnic at all
	op := mkOp(OpVFProvision, vfPFTestRef(), &VFProvisionParams{Count: 1})
	findings := Validate([]Op{op}, snap)
	found := false
	for _, f := range findings {
		if f.Code == codePFNotFound {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s finding among: %+v", codePFNotFound, findings)
	}
}

// TestValidate_VFProvision_SpoofcheckMismatch is T-1506 acceptance
// criterion 3's changeset-validate-time half: a staged vf.provision op
// whose resolved VLAN diverges from its PF's bridge's declared VID set is
// blocked with codeVFSpoofcheckMismatch; a consistent one validates clean
// (both directions, against the same fixture bridge).
func TestValidate_VFProvision_SpoofcheckMismatch(t *testing.T) {
	bridgeRef := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr1"}
	snapFor := func() inventory.Snapshot {
		return buildSnapshot(
			&inventory.PhysNic{Ref: vfPFTestRef(), Name: "eno1"},
			&inventory.Bridge{
				Ref: bridgeRef, Name: "vmbr1", Virt: inventory.BridgeLinux,
				VlanAware: true, VlanAwareSet: true,
				Vids:      []inventory.VidRange{{Low: 100, High: 100}},
				PortNames: []string{"eno1"},
			},
		)
	}

	t.Run("vlan outside bridge's declared VID set: blocked", func(t *testing.T) {
		op := mkOp(OpVFProvision, vfPFTestRef(), &VFProvisionParams{Count: 1, VLAN: 999, SpoofCheck: boolPtr(true)})
		findings := Validate([]Op{op}, snapFor())
		found := false
		for _, f := range findings {
			if f.Code == codeVFSpoofcheckMismatch {
				found = true
			}
		}
		if !found {
			t.Fatalf("no %s finding among: %+v", codeVFSpoofcheckMismatch, findings)
		}
	})

	t.Run("spoofcheck disabled on a vlan-aware bridge: blocked", func(t *testing.T) {
		op := mkOp(OpVFProvision, vfPFTestRef(), &VFProvisionParams{Count: 1, VLAN: 100, SpoofCheck: boolPtr(false)})
		findings := Validate([]Op{op}, snapFor())
		found := false
		for _, f := range findings {
			if f.Code == codeVFSpoofcheckMismatch {
				found = true
			}
		}
		if !found {
			t.Fatalf("no %s finding among: %+v", codeVFSpoofcheckMismatch, findings)
		}
	})

	t.Run("consistent with bridge policy: silent", func(t *testing.T) {
		op := mkOp(OpVFProvision, vfPFTestRef(), &VFProvisionParams{Count: 1, VLAN: 100, SpoofCheck: boolPtr(true)})
		findings := Validate([]Op{op}, snapFor())
		for _, f := range findings {
			if f.Code == codeVFSpoofcheckMismatch {
				t.Fatalf("unexpected %s finding for a consistent VF: %+v", codeVFSpoofcheckMismatch, findings)
			}
		}
	})
}
