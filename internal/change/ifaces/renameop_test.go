// SPDX-License-Identifier: Apache-2.0

package ifaces

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func TestMutateIfaceRename_BridgeAndReferences(t *testing.T) {
	const src = `auto vmbr0
iface vmbr0 inet static
	address 10.0.0.11/24
	bridge-ports eno1
	bridge-stp off

auto vmbr0.100
iface vmbr0.100 inet manual
	vlan-raw-device vmbr0
`
	f, err := host.ParseInterfaces([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	op := IfaceRename{Target: ref(inventory.KindBridge, "pve1", "vmbr0"), NewName: "vmbrmgmt"}
	if err := Mutate(f, op, "cs1"); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	got := f.Render()

	for _, want := range []string{
		"auto vmbrmgmt\n",             // auto line renamed
		"iface vmbrmgmt inet static",  // header renamed
		"vlan-raw-device vmbrmgmt",    // the child's parent reference follows the rename
		"iface vmbr0.100 inet manual", // ...but the child keeps its own (dotted) name — no cascade
		"bridge-ports eno1",           // an unrelated port reference is untouched
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rename output missing %q:\n%s", want, got)
		}
	}
	// The old name must not survive as a standalone token anywhere.
	if strings.Contains(got, "auto vmbr0\n") || strings.Contains(got, "iface vmbr0 ") {
		t.Errorf("old name still present as a token:\n%s", got)
	}
	// The address line (10.0.0.11/24) and stp option are preserved verbatim.
	if !strings.Contains(got, "address 10.0.0.11/24") || !strings.Contains(got, "bridge-stp off") {
		t.Errorf("unrelated option lines were altered:\n%s", got)
	}
}

func TestMutateIfaceRename_BondReferencedByBridge(t *testing.T) {
	const src = `iface bond0 inet manual
	bond-slaves eno1 eno2
	bond-mode 802.3ad

auto vmbr0
iface vmbr0 inet manual
	bridge-ports bond0
`
	f, err := host.ParseInterfaces([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	op := IfaceRename{Target: ref(inventory.KindBond, "pve1", "bond0"), NewName: "uplink"}
	if err := Mutate(f, op, "cs1"); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	got := f.Render()
	if !strings.Contains(got, "iface uplink inet manual") {
		t.Errorf("bond header not renamed:\n%s", got)
	}
	if !strings.Contains(got, "bridge-ports uplink") {
		t.Errorf("bridge's port reference not updated:\n%s", got)
	}
	// The bond's own slave list references NICs, not the bond — untouched.
	if !strings.Contains(got, "bond-slaves eno1 eno2") {
		t.Errorf("bond-slaves altered:\n%s", got)
	}
}

func TestMutateIfaceRename_NotFound(t *testing.T) {
	f, err := host.ParseInterfaces([]byte("auto lo\niface lo inet loopback\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	op := IfaceRename{Target: ref(inventory.KindBridge, "pve1", "vmbrX"), NewName: "vmbrY"}
	if err := Mutate(f, op, "cs1"); err == nil {
		t.Fatal("expected an error renaming a missing interface, got nil")
	}
}

func TestReplaceWholeToken_Boundaries(t *testing.T) {
	cases := []struct {
		in, old, newTok, want string
	}{
		{"iface vmbr0 inet static\n", "vmbr0", "vmbrX", "iface vmbrX inet static\n"},
		{"\tvlan-raw-device vmbr0\n", "vmbr0", "vmbrX", "\tvlan-raw-device vmbrX\n"},
		{"iface vmbr0.100 inet manual\n", "vmbr0", "vmbrX", "iface vmbr0.100 inet manual\n"}, // substring, not a token
		{"\tbridge-ports eno1 vmbr0\n", "vmbr0", "vmbrX", "\tbridge-ports eno1 vmbrX\n"},
	}
	for _, c := range cases {
		if got := replaceWholeToken(c.in, c.old, c.newTok); got != c.want {
			t.Errorf("replaceWholeToken(%q,%q,%q) = %q, want %q", c.in, c.old, c.newTok, got, c.want)
		}
	}
}
