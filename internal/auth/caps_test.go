// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"os"
	"reflect"
	"regexp"
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

// TestRequiredPrivilegesCoversMapping keeps RequiredPrivileges honest against
// DeriveCapabilities.
//
// `vnproxctl doctor` (T-1904) tells an operator which PVE privileges to grant.
// If the mapping starts consulting a privilege that RequiredPrivileges does not
// list, doctor stays silent about a privilege whose absence genuinely breaks
// vnprox — the operator grants everything doctor asked for and it still does
// not work. So this reads the privileges DeriveCapabilities actually consults
// out of caps.go itself, rather than trusting a second hand-written list.
func TestRequiredPrivilegesCoversMapping(t *testing.T) {
	src, err := os.ReadFile("caps.go")
	if err != nil {
		t.Fatalf("reading caps.go: %v", err)
	}
	body := string(src)

	// Constant name -> privilege string, from the const block.
	constRe := regexp.MustCompile(`(priv[A-Za-z]+)\s*=\s*"([^"]+)"`)
	values := make(map[string]string)
	for _, m := range constRe.FindAllStringSubmatch(body, -1) {
		values[m[1]] = m[2]
	}
	if len(values) < 5 {
		t.Fatalf("found only %d priv* constants in caps.go; the scan is broken, not the mapping", len(values))
	}

	// Every privilege DeriveCapabilities consults.
	useRe := regexp.MustCompile(`privs\.has\((priv[A-Za-z]+)\)`)
	consulted := make(map[string]bool)
	for _, m := range useRe.FindAllStringSubmatch(body, -1) {
		name, ok := values[m[1]]
		if !ok {
			t.Errorf("DeriveCapabilities consults %s, which has no constant definition", m[1])
			continue
		}
		consulted[name] = true
	}
	if len(consulted) < 5 {
		t.Fatalf("found only %d consulted privileges; the scan is broken, not the mapping", len(consulted))
	}
	// Control: a privilege known to be in the mapping must have been found.
	if !consulted["Sys.Modify"] {
		t.Fatal("the scan did not find Sys.Modify, which DeriveCapabilities certainly consults")
	}

	listed := make(map[string]bool)
	for _, rp := range RequiredPrivileges() {
		if rp.Name == "" || rp.Unlocks == "" {
			t.Errorf("RequiredPrivileges entry %+v is missing a name or an explanation", rp)
		}
		if listed[rp.Name] {
			t.Errorf("RequiredPrivileges lists %s twice", rp.Name)
		}
		listed[rp.Name] = true
	}

	for priv := range consulted {
		if !listed[priv] {
			t.Errorf("DeriveCapabilities consults %q but RequiredPrivileges does not list it: `vnproxctl doctor` would never tell an operator to grant it", priv)
		}
	}
	for priv := range listed {
		if !consulted[priv] {
			t.Errorf("RequiredPrivileges lists %q but the mapping no longer consults it: doctor would ask for a privilege vnprox does not use", priv)
		}
	}
}
