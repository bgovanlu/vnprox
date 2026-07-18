package auth

import (
	"reflect"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// TestDeriveCapabilities_MappingTable is the documented source-of-truth
// test for caps.go's mapping table (docs/security.md: "internal/auth/caps.go
// is the single source of truth"). Each case's privilege list is copied
// verbatim from testdata/clusters/*.yaml's fixture users, covering all
// four T-105 acceptance-criterion-3 personas (root, auditor, sdn-only,
// vm-user) plus netops. The same four personas are also driven through a
// real pvemock login end-to-end by
// TestIntegration_CapabilityMatrixAgainstMock (integration_test.go); this
// unit test pins the pure mapping in isolation.
func TestDeriveCapabilities_MappingTable(t *testing.T) {
	cases := []struct {
		name  string
		privs []string
		want  Capabilities
	}{
		{
			name:  "root@pam wildcard (single-node.yaml, three-node-vlan.yaml)",
			privs: []string{"*"},
			want: Capabilities{
				NetRead: true, NetWrite: true,
				SDNRead: true, SDNWrite: true,
				FWRead: true, FWWrite: true,
				GuestNet: true, Audit: true,
			},
		},
		{
			name:  "auditor@pve read-only (single-node.yaml, three-node-vlan.yaml)",
			privs: []string{"Sys.Audit", "VM.Audit", "SDN.Audit"},
			want: Capabilities{
				NetRead: true, NetWrite: false,
				SDNRead: true, SDNWrite: false,
				FWRead: true, FWWrite: false,
				GuestNet: false, Audit: true,
			},
		},
		{
			name:  "netops@pve sdn+net operator (three-node-vlan.yaml, evpn-lab.yaml, messy-brownfield.yaml)",
			privs: []string{"Sys.Audit", "Sys.Modify", "SDN.Audit", "SDN.Allocate", "VM.Audit"},
			want: Capabilities{
				NetRead: true, NetWrite: true,
				SDNRead: true, SDNWrite: true,
				FWRead: true, FWWrite: true,
				GuestNet: false, Audit: true,
			},
		},
		{
			name:  "sdn-only@pve (three-node-vlan.yaml)",
			privs: []string{"SDN.Audit", "SDN.Allocate"},
			want: Capabilities{
				SDNRead: true, SDNWrite: true,
			},
		},
		{
			name:  "vm-user@pve (three-node-vlan.yaml)",
			privs: []string{"VM.Audit", "VM.Config.Network"},
			want: Capabilities{
				GuestNet: true,
			},
		},
		{
			name:  "no privileges at all",
			privs: []string{},
			want:  Capabilities{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveCapabilities(newPrivilegeSet(tc.privs))
			if got != tc.want {
				t.Errorf("DeriveCapabilities(%v) = %+v, want %+v", tc.privs, got, tc.want)
			}
		})
	}
}

func TestBuildCapabilities_PerNodeFromRootAndNodeScopedGrants(t *testing.T) {
	perms := pve.Permissions{
		"/":           {"Sys.Audit": true},
		"/nodes/pve1": {"Sys.Modify": true},
		"/nodes/pve2": {},
	}
	got := BuildCapabilities(perms, []string{"pve1", "pve2"})

	want := map[string]Capabilities{
		"pve1": {NetRead: true, NetWrite: true, FWRead: true, FWWrite: true, Audit: true},
		"pve2": {NetRead: true, FWRead: true, Audit: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildCapabilities = %+v, want %+v", got, want)
	}
}

func TestBuildCapabilities_EmptyNodesFallsBackToClusterWideEntry(t *testing.T) {
	perms := pve.Permissions{"/": {"Sys.Audit": true, "SDN.Audit": true}}
	got := BuildCapabilities(perms, nil)

	want := map[string]Capabilities{"": {NetRead: true, SDNRead: true, FWRead: true, Audit: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildCapabilities(nodes=nil) = %+v, want %+v", got, want)
	}
}

func TestCapabilities_Has(t *testing.T) {
	c := Capabilities{NetRead: true, SDNWrite: true}
	if !c.Has(CapNetRead) {
		t.Error("Has(CapNetRead) = false, want true")
	}
	if c.Has(CapNetWrite) {
		t.Error("Has(CapNetWrite) = true, want false")
	}
	if !c.Has(CapSDNWrite) {
		t.Error("Has(CapSDNWrite) = false, want true")
	}
	if c.Has(Cap("bogus")) {
		t.Error("Has(unknown cap) = true, want false")
	}

	c2 := Capabilities{Automation: true}
	if !c2.Has(CapAutomation) {
		t.Error("Has(CapAutomation) = false, want true")
	}
}

func TestDeriveCapabilities_NeverGrantsAutomation(t *testing.T) {
	// Automation is not derived from any PVE privilege (Capabilities.
	// Automation's doc comment) — even a full-wildcard "*" privilege set
	// must not set it, since the only way a request context ever carries
	// Automation: true is a bearer token's own minted scopes.
	caps := DeriveCapabilities(newPrivilegeSet([]string{"*"}))
	if caps.Automation {
		t.Error("DeriveCapabilities with wildcard privilege set Automation = true, want false (never PVE-derived)")
	}
}
