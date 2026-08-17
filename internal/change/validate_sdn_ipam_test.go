package change

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestValidate_SdnIpamDelete_BlockedWhenReferenced is T-3104's acceptance
// criterion 2 analogue of T-3102 acceptance criterion 5: deleting an ipam
// plugin instance a zone's own ipam field still names is blocked. This also
// proves the wiring this task discovered and fixed alongside the ipam CRUD
// family itself: inventory.SdnZone.IPAM existed in the type system before
// T-3104 (change.SdnZoneCreateParams/UpdateParams already carried it) but
// nothing populated it from a live poll (internal/pve.SDNZone had no IPAM
// field at all) — a snapshot's zone.IPAM would always have been "" without
// that fix, making this exact check permanently unable to fire.
func TestValidate_SdnIpamDelete_BlockedWhenReferenced(t *testing.T) {
	snap := buildSnapshot(
		&inventory.SdnZone{Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: "z1"}, ID: "z1", Type: "simple", IPAM: "nb1"},
		&inventory.SdnIpam{Ref: inventory.Ref{Kind: inventory.KindSDNIpam, ID: "nb1"}, ID: "nb1", Type: "netbox"},
	)
	op := mkOp(OpSdnIpamDelete, inventory.Ref{Kind: inventory.KindSDNIpam, ID: "nb1"}, &SdnIpamDeleteParams{})
	findings := Validate([]Op{op}, snap)
	if !hasError(findings) {
		t.Fatalf("expected a blocking finding, got: %+v", findings)
	}
	found := false
	for _, f := range findings {
		if f.Code == codeSdnIpamInUse {
			found = true
			if !containsAll(f.Message, "1", "z1", "nb1") {
				t.Errorf("finding message = %q, want it to mention the ipam id, the referencing zone, and the count 1", f.Message)
			}
		}
	}
	if !found {
		t.Fatalf("no %s finding among: %+v", codeSdnIpamInUse, findings)
	}
}

// TestValidate_SdnIpamDelete_AllowedWhenUnreferenced proves the guard is
// specific to actual usage: an ipam instance no zone references deletes
// clean.
func TestValidate_SdnIpamDelete_AllowedWhenUnreferenced(t *testing.T) {
	snap := buildSnapshot(
		&inventory.SdnZone{Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: "z1"}, ID: "z1", Type: "simple", IPAM: "pve"},
		&inventory.SdnIpam{Ref: inventory.Ref{Kind: inventory.KindSDNIpam, ID: "nb1"}, ID: "nb1", Type: "netbox"},
	)
	op := mkOp(OpSdnIpamDelete, inventory.Ref{Kind: inventory.KindSDNIpam, ID: "nb1"}, &SdnIpamDeleteParams{})
	findings := Validate([]Op{op}, snap)
	if hasError(findings) {
		t.Fatalf("unexpected error findings for deleting an unreferenced ipam instance: %+v", findings)
	}
}

// TestValidate_SdnIpamDelete_NotFound proves the standard "target must
// exist" check applies here too.
func TestValidate_SdnIpamDelete_NotFound(t *testing.T) {
	snap := buildSnapshot()
	op := mkOp(OpSdnIpamDelete, inventory.Ref{Kind: inventory.KindSDNIpam, ID: "ghost"}, &SdnIpamDeleteParams{})
	findings := Validate([]Op{op}, snap)
	found := false
	for _, f := range findings {
		if f.Code == codeTargetNotFound {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s finding among: %+v", codeTargetNotFound, findings)
	}
}

// TestValidate_SdnIpamCreate_TypeConditionalFields pins the required/allowed
// field logic (this task's own documented inference, not a captured fact —
// see params_sdn_ipam.go's doc comment).
func TestValidate_SdnIpamCreate_TypeConditionalFields(t *testing.T) {
	tests := []struct {
		name    string
		params  SdnIpamCreateParams
		wantErr bool
	}{
		{"pve needs nothing", SdnIpamCreateParams{Type: "pve"}, false},
		{"pve rejects url", SdnIpamCreateParams{Type: "pve", URL: "https://example.com"}, true},
		{"netbox requires token", SdnIpamCreateParams{Type: "netbox", URL: "https://example.com"}, true},
		{"netbox with url+token is valid", SdnIpamCreateParams{Type: "netbox", URL: "https://example.com", Token: "t"}, false},
		{"phpipam with url+token is valid", SdnIpamCreateParams{Type: "phpipam", URL: "https://example.com", Token: "t"}, false},
		{"unknown type rejected", SdnIpamCreateParams{Type: "solarwinds"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap := buildSnapshot()
			op := mkOp(OpSdnIpamCreate, inventory.Ref{Kind: inventory.KindSDNIpam, ID: "test1"}, &tc.params)
			findings := Validate([]Op{op}, snap)
			if got := hasError(findings); got != tc.wantErr {
				t.Fatalf("hasError = %v, want %v; findings: %+v", got, tc.wantErr, findings)
			}
		})
	}
}
