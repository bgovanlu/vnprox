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
				GuestNet: true, Audit: true, Capture: true,
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
			// T-1301 AC1: holding netWrite's own privilege (Sys.Modify) —
			// even alongside every read/write privilege short of Sys.Console —
			// never grants capture. This is the "wrong permission decision is
			// a data-exposure incident" guard the card calls out.
			name:  "captureNeverFromNetWriteAlone: netops-plus (no Sys.Console)",
			privs: []string{"Sys.Audit", "Sys.Modify", "SDN.Audit", "SDN.Allocate", "VM.Config.Network"},
			want: Capabilities{
				NetRead: true, NetWrite: true,
				SDNRead: true, SDNWrite: true,
				FWRead: true, FWWrite: true,
				GuestNet: true, Audit: true, Capture: false,
			},
		},
		{
			// T-1301 AC1: the capture pairing (Sys.Modify AND Sys.Console)
			// grants capture; it also brings netWrite/fwWrite along, since it
			// is a strict superset of netWrite's own Sys.Modify.
			name:  "captureRequiresSysModifyAndSysConsole: capture operator",
			privs: []string{"Sys.Audit", "Sys.Modify", "Sys.Console"},
			want: Capabilities{
				NetRead: true, NetWrite: true,
				FWRead: true, FWWrite: true,
				Audit: true, Capture: true,
			},
		},
		{
			// Sys.Console without Sys.Modify is not enough either — the
			// pairing is an AND, not an OR.
			name:  "captureRequiresBoth: Sys.Console alone",
			privs: []string{"Sys.Audit", "Sys.Console"},
			want: Capabilities{
				NetRead: true, FWRead: true, Audit: true, Capture: false,
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
}
