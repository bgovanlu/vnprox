package ifaces

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// corpusFiles lists every T-102 testdata/interfaces fixture, by base name,
// for property-testing against the full corpus.
var corpusFiles = []string{
	"01-simple-single-bridge.interfaces",
	"02-vlan-aware-bridge.interfaces",
	"03-bond-with-vlans.interfaces",
	"04-ovs-bridge.interfaces",
	"05-ovs-bond.interfaces",
	"06-source-includes.interfaces",
	"07-source-directory-relative.interfaces",
	"08-exotic-comments.interfaces",
	"09-dual-stack-multi-stanza.interfaces",
	"10-mapping-stanza.interfaces",
	"11-allow-hotplug-rename-noautodown.interfaces",
	"12-line-continuation.interfaces",
	"13-inherits-template.interfaces",
	"14-crlf-no-trailing-newline.interfaces",
	"15-messy-brownfield-style.interfaces",
}

// TestProperty_CreateOpsPreserveOriginalContent is task card T-204's
// "untouched-stanza byte-identity asserted across the corpus" acceptance
// criterion: for every one of T-102's 15 round-trip fixtures, a Create op
// targeting a brand-new name must leave every byte of the original file's
// AST untouched — appended only, never edited in place (see
// appendStanza/prepareAppend in entryutil.go). This is checked directly
// against the parsed Entry/BodyItem structure (not string-diffed) so it
// catches any accidental in-place mutation regardless of whether it
// happens to render identically.
func TestProperty_CreateOpsPreserveOriginalContent(t *testing.T) {
	for _, name := range corpusFiles {
		t.Run(name, func(t *testing.T) {
			orig, _ := parseCorpus(t, name)
			f, _ := parseCorpus(t, name)

			ops := []Op{
				BondCreate{Target: ref(inventory.KindBond, "pve1", "gentestbond0"), Slaves: []string{"gena", "genb"}, Mode: "802.3ad", Autostart: true},
				BridgeCreate{Target: ref(inventory.KindBridge, "pve1", "gentestbr0"), Ports: []string{"gentestbond0"}, Autostart: true},
				VlanCreate{Target: ref(inventory.KindVlan, "pve1", "gentestbr0.42"), Parent: "gentestbr0", VID: 42, Autostart: true},
			}
			if err := MutateAll(f, ops, "PROPCS"); err != nil {
				t.Fatalf("MutateAll: %v", err)
			}
			requireOriginalEntriesPreserved(t, orig, f)

			// The rendered result must itself still parse cleanly (no op
			// may produce a syntactically broken file).
			if _, err := host.ParseInterfaces([]byte(f.Render())); err != nil {
				t.Fatalf("mutated output does not reparse: %v", err)
			}
		})
	}
}

// randSlaveNames, randPortNames, randMode, etc. generate small valid
// randomized inputs for the property test below.
func randNames(r *rand.Rand, prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return out
}

var bondModes = []string{"802.3ad", "active-backup", "balance-rr"}

// TestProperty_RandomizedCreateOpsMatchIntent is task card T-204's property
// test: "apply op -> parse result -> inventory-level effect matches the
// op's intent, for randomized valid ops." For each of many randomly
// generated bond/bridge/vlan Create ops (random slave/port counts, VIDs,
// MTUs, modes, autostart), it applies the op, re-parses the *rendered
// output* from scratch (round-tripping through bytes, not just inspecting
// the in-memory AST Mutate already built), and checks that the entity-level
// facts a collector's inventory.FromNetlinkLinks-style ingestion would read
// back out of that stanza (name, family/method, slave/port membership, VID
// linkage, MTU, autostart wiring) match what the op asked for.
func TestProperty_RandomizedCreateOpsMatchIntent(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	const iterations = 300

	for i := 0; i < iterations; i++ {
		base := corpusFiles[r.Intn(len(corpusFiles))]
		f, _ := parseCorpus(t, base)

		switch r.Intn(3) {
		case 0:
			name := fmt.Sprintf("pgenbond%d", i)
			slaves := randNames(r, fmt.Sprintf("pgs%d_", i), 2+r.Intn(2))
			mode := bondModes[r.Intn(len(bondModes))]
			mtu := []int{0, 1500, 9000}[r.Intn(3)]
			autostart := r.Intn(2) == 0

			op := BondCreate{
				Target: ref(inventory.KindBond, "pve1", name), Mode: mode,
				Slaves: slaves, MTU: mtu, Autostart: autostart,
			}
			if err := Mutate(f, op, "PROPCS"); err != nil {
				t.Fatalf("[%d] Mutate bond.create: %v", i, err)
			}
			reparsed, err := host.ParseInterfaces([]byte(f.Render()))
			if err != nil {
				t.Fatalf("[%d] reparse: %v", i, err)
			}
			checkBondIntent(t, i, reparsed, name, mode, slaves, mtu, autostart)

		case 1:
			name := fmt.Sprintf("pgenbr%d", i)
			ports := randNames(r, fmt.Sprintf("pps%d_", i), 1+r.Intn(3))
			vlanAware := r.Intn(2) == 0
			mtu := []int{0, 1500, 9000}[r.Intn(3)]
			autostart := r.Intn(2) == 0

			op := BridgeCreate{
				Target: ref(inventory.KindBridge, "pve1", name), Ports: ports,
				VlanAware: vlanAware, MTU: mtu, Autostart: autostart,
			}
			if err := Mutate(f, op, "PROPCS"); err != nil {
				t.Fatalf("[%d] Mutate bridge.create: %v", i, err)
			}
			reparsed, err := host.ParseInterfaces([]byte(f.Render()))
			if err != nil {
				t.Fatalf("[%d] reparse: %v", i, err)
			}
			checkBridgeIntent(t, i, reparsed, name, ports, vlanAware, mtu, autostart)

		case 2:
			parent := fmt.Sprintf("pgenparent%d", i)
			vid := 2 + r.Intn(4093)
			mtu := []int{0, 1500, 9000}[r.Intn(3)]
			autostart := r.Intn(2) == 0
			name := VlanName(parent, vid)

			op := VlanCreate{
				Target: ref(inventory.KindVlan, "pve1", name), Parent: parent, VID: vid,
				MTU: mtu, Autostart: autostart,
			}
			if err := Mutate(f, op, "PROPCS"); err != nil {
				t.Fatalf("[%d] Mutate vlan.create: %v", i, err)
			}
			reparsed, err := host.ParseInterfaces([]byte(f.Render()))
			if err != nil {
				t.Fatalf("[%d] reparse: %v", i, err)
			}
			checkVlanIntent(t, i, reparsed, name, parent, mtu, autostart)
		}
	}
}

func checkBondIntent(t *testing.T, i int, f *host.File, name, mode string, slaves []string, mtu int, autostart bool) {
	t.Helper()
	e, ok := f.Iface(name)
	if !ok {
		t.Fatalf("[%d] bond %s not found after reparse", i, name)
	}
	if e.Family != "inet" || e.Method != "manual" {
		t.Errorf("[%d] bond %s family/method = %s/%s, want inet/manual", i, name, e.Family, e.Method)
	}
	if v, _ := e.Get("bond-mode"); v != mode {
		t.Errorf("[%d] bond %s bond-mode = %q, want %q", i, name, v, mode)
	}
	if v, _ := e.Get("bond-slaves"); v != strings.Join(slaves, " ") {
		t.Errorf("[%d] bond %s bond-slaves = %q, want %q", i, name, v, strings.Join(slaves, " "))
	}
	checkMTUAndAutostart(t, i, f, e, name, mtu, autostart)
}

func checkBridgeIntent(t *testing.T, i int, f *host.File, name string, ports []string, vlanAware bool, mtu int, autostart bool) {
	t.Helper()
	e, ok := f.Iface(name)
	if !ok {
		t.Fatalf("[%d] bridge %s not found after reparse", i, name)
	}
	if v, _ := e.Get("bridge-ports"); v != strings.Join(ports, " ") {
		t.Errorf("[%d] bridge %s bridge-ports = %q, want %q", i, name, v, strings.Join(ports, " "))
	}
	v, hasVA := e.Get("bridge-vlan-aware")
	if vlanAware && (!hasVA || v != "yes") {
		t.Errorf("[%d] bridge %s expected bridge-vlan-aware yes, got %q (present=%v)", i, name, v, hasVA)
	}
	if !vlanAware && hasVA {
		t.Errorf("[%d] bridge %s expected no bridge-vlan-aware option, found %q", i, name, v)
	}
	checkMTUAndAutostart(t, i, f, e, name, mtu, autostart)
}

func checkVlanIntent(t *testing.T, i int, f *host.File, name, parent string, mtu int, autostart bool) {
	t.Helper()
	e, ok := f.Iface(name)
	if !ok {
		t.Fatalf("[%d] vlan %s not found after reparse", i, name)
	}
	if v, _ := e.Get("vlan-raw-device"); v != parent {
		t.Errorf("[%d] vlan %s vlan-raw-device = %q, want %q", i, name, v, parent)
	}
	checkMTUAndAutostart(t, i, f, e, name, mtu, autostart)
}

func checkMTUAndAutostart(t *testing.T, i int, f *host.File, e *host.Entry, name string, mtu int, autostart bool) {
	t.Helper()
	v, hasMTU := e.Get("mtu")
	if mtu != 0 {
		if !hasMTU || v != strconv.Itoa(mtu) {
			t.Errorf("[%d] %s mtu = %q (present=%v), want %d", i, name, v, hasMTU, mtu)
		}
	} else if hasMTU {
		t.Errorf("[%d] %s expected no mtu option, found %q", i, name, v)
	}

	auto := false
	for _, n := range f.AutoIfaces() {
		if n == name {
			auto = true
			break
		}
	}
	if auto != autostart {
		t.Errorf("[%d] %s autostart-wired = %v, want %v", i, name, auto, autostart)
	}

	// Every newly created stanza must carry the managed-by-vnprox
	// provenance marker.
	found := false
	for _, b := range e.Body {
		if b.Kind == host.BodyComment && strings.Contains(b.Raw, "managed by vnprox") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("[%d] %s missing managed-by-vnprox comment", i, name)
	}
}
