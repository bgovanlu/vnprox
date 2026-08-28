// SPDX-License-Identifier: Apache-2.0

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

// --- randomized update/delete/port ops (audit finding F-08) ----------------

// mutTarget is one existing entity in a parsed fixture that a randomized
// update/delete/port op can target, classified from the stanza's own
// options (the on-disk declaration is ground truth for its kind).
type mutTarget struct {
	name  string
	kind  inventory.Kind
	ports []string // current port list, bridges only
}

// collectMutTargets classifies every distinct iface stanza in f into the
// bond/bridge/vlan entity kinds this package's update/delete/port ops
// understand, skipping plain interfaces (loopback, NICs, templates).
func collectMutTargets(f *host.File) []mutTarget {
	var out []mutTarget
	seen := map[string]bool{}
	for _, e := range f.Ifaces() {
		if seen[e.Name] {
			continue
		}
		seen[e.Name] = true
		ovsType, _ := e.Get("ovs_type")
		switch ovsType {
		case "OVSBond":
			out = append(out, mutTarget{name: e.Name, kind: inventory.KindOVSBond})
		case "OVSBridge":
			v, _ := e.Get("ovs_ports")
			out = append(out, mutTarget{name: e.Name, kind: inventory.KindOVSBridge, ports: strings.Fields(v)})
		default:
			if _, ok := e.Get("bond-slaves"); ok {
				out = append(out, mutTarget{name: e.Name, kind: inventory.KindBond})
			} else if v, ok := e.Get("bridge-ports"); ok {
				out = append(out, mutTarget{name: e.Name, kind: inventory.KindBridge, ports: strings.Fields(v)})
			} else if _, ok := e.Get("vlan-raw-device"); ok {
				out = append(out, mutTarget{name: e.Name, kind: inventory.KindVlan})
			}
		}
	}
	return out
}

var propMTUs = []int{1500, 9000}

// TestProperty_RandomizedMutationOpsMatchIntent extends the create-only
// property test to the update/delete/port op families (bond.update,
// bond.delete, bridge.update, bridge.delete, bridge.port.add,
// bridge.port.remove, vlan.update, vlan.delete): for randomized existing
// targets picked out of each parsed corpus fixture with randomized
// parameters, it applies the op, re-parses the rendered bytes from scratch,
// and checks the entity-level effect matches the op's intent. Seeded and
// deterministic, matching the create-op property test's pattern.
func TestProperty_RandomizedMutationOpsMatchIntent(t *testing.T) {
	r := rand.New(rand.NewSource(43))
	const iterations = 400

	for i := 0; i < iterations; i++ {
		base := corpusFiles[r.Intn(len(corpusFiles))]
		f, _ := parseCorpus(t, base)
		targets := collectMutTargets(f)
		if len(targets) == 0 {
			continue // fixture has no bond/bridge/vlan entity to mutate
		}
		target := targets[r.Intn(len(targets))]
		tref := ref(target.kind, "pve1", target.name)

		switch target.kind {
		case inventory.KindBond, inventory.KindOVSBond:
			ovs := target.kind == inventory.KindOVSBond
			if r.Intn(3) == 0 { // delete
				mutateAndCheckDeleted(t, i, base, f, BondDelete{Target: tref}, target.name)
				continue
			}
			op := BondUpdate{Target: tref, MTU: propMTUs[r.Intn(2)]}
			if r.Intn(2) == 0 {
				op.Slaves = randNames(r, fmt.Sprintf("ms%d_", i), 2+r.Intn(2))
			}
			if !ovs {
				if r.Intn(2) == 0 {
					op.Mode = bondModes[r.Intn(len(bondModes))]
				}
				op.RemoveLacpRate = r.Intn(2) == 0
				op.RemoveXmitHashPolicy = r.Intn(2) == 0
			}
			reparsed := mutateAndReparse(t, i, base, f, op)
			e, ok := reparsed.Iface(target.name)
			if !ok {
				t.Fatalf("[%d %s] bond %s vanished after update", i, base, target.name)
			}
			slavesKey := "bond-slaves"
			if ovs {
				slavesKey = "ovs_bonds"
			}
			if len(op.Slaves) > 0 {
				if v, _ := e.Get(slavesKey); v != strings.Join(op.Slaves, " ") {
					t.Errorf("[%d %s] bond %s %s = %q, want %q", i, base, target.name, slavesKey, v, strings.Join(op.Slaves, " "))
				}
			}
			if op.Mode != "" {
				if v, _ := e.Get("bond-mode"); v != op.Mode {
					t.Errorf("[%d %s] bond %s bond-mode = %q, want %q", i, base, target.name, v, op.Mode)
				}
			}
			if op.RemoveLacpRate {
				if v, ok := e.Get("bond-lacp-rate"); ok {
					t.Errorf("[%d %s] bond %s bond-lacp-rate should be removed, got %q", i, base, target.name, v)
				}
			}
			if op.RemoveXmitHashPolicy {
				if v, ok := e.Get("bond-xmit-hash-policy"); ok {
					t.Errorf("[%d %s] bond %s bond-xmit-hash-policy should be removed, got %q", i, base, target.name, v)
				}
			}
			checkUpdatedMTU(t, i, base, e, target.name, op.MTU)

		case inventory.KindBridge, inventory.KindOVSBridge:
			ovs := target.kind == inventory.KindOVSBridge
			portsKey := "bridge-ports"
			if ovs {
				portsKey = "ovs_ports"
			}
			switch r.Intn(4) {
			case 0: // delete
				mutateAndCheckDeleted(t, i, base, f, BridgeDelete{Target: tref}, target.name)
			case 1: // port add
				port := fmt.Sprintf("mp%d", i)
				reparsed := mutateAndReparse(t, i, base, f, BridgePortAdd{Target: tref, Port: port})
				e, ok := reparsed.Iface(target.name)
				if !ok {
					t.Fatalf("[%d %s] bridge %s vanished after port.add", i, base, target.name)
				}
				v, _ := e.Get(portsKey)
				want := strings.Join(append(append([]string{}, target.ports...), port), " ")
				if v != want {
					t.Errorf("[%d %s] bridge %s %s = %q, want %q (existing ports preserved, new port appended)", i, base, target.name, portsKey, v, want)
				}
			case 2: // port remove (skipped when the bridge has no ports)
				if len(target.ports) == 0 {
					continue
				}
				victim := target.ports[r.Intn(len(target.ports))]
				reparsed := mutateAndReparse(t, i, base, f, BridgePortRemove{Target: tref, Port: victim})
				e, ok := reparsed.Iface(target.name)
				if !ok {
					t.Fatalf("[%d %s] bridge %s vanished after port.remove", i, base, target.name)
				}
				v, _ := e.Get(portsKey)
				var want []string
				for _, p := range target.ports {
					if p != victim {
						want = append(want, p)
					}
				}
				if v != strings.Join(want, " ") {
					t.Errorf("[%d %s] bridge %s %s = %q after removing %q, want %q", i, base, target.name, portsKey, v, victim, strings.Join(want, " "))
				}
			default: // update
				op := BridgeUpdate{Target: tref, MTU: propMTUs[r.Intn(2)]}
				if r.Intn(2) == 0 {
					op.Ports = randNames(r, fmt.Sprintf("mbp%d_", i), 1+r.Intn(3))
				}
				if !ovs && r.Intn(2) == 0 {
					vlanAware := r.Intn(2) == 0
					op.VlanAware = &vlanAware
				}
				reparsed := mutateAndReparse(t, i, base, f, op)
				e, ok := reparsed.Iface(target.name)
				if !ok {
					t.Fatalf("[%d %s] bridge %s vanished after update", i, base, target.name)
				}
				if len(op.Ports) > 0 {
					if v, _ := e.Get(portsKey); v != strings.Join(op.Ports, " ") {
						t.Errorf("[%d %s] bridge %s %s = %q, want %q", i, base, target.name, portsKey, v, strings.Join(op.Ports, " "))
					}
				}
				if op.VlanAware != nil {
					v, has := e.Get("bridge-vlan-aware")
					if *op.VlanAware && (!has || v != "yes") {
						t.Errorf("[%d %s] bridge %s expected bridge-vlan-aware yes, got %q (present=%v)", i, base, target.name, v, has)
					}
					if !*op.VlanAware && has {
						t.Errorf("[%d %s] bridge %s expected no bridge-vlan-aware, found %q", i, base, target.name, v)
					}
				}
				checkUpdatedMTU(t, i, base, e, target.name, op.MTU)
			}

		case inventory.KindVlan:
			if r.Intn(3) == 0 { // delete
				mutateAndCheckDeleted(t, i, base, f, VlanDelete{Target: tref}, target.name)
				continue
			}
			op := VlanUpdate{Target: tref, MTU: propMTUs[r.Intn(2)]}
			if r.Intn(2) == 0 {
				op.Addresses = []string{fmt.Sprintf("10.%d.%d.5/24", r.Intn(255), r.Intn(255))}
			}
			reparsed := mutateAndReparse(t, i, base, f, op)
			e, ok := reparsed.Iface(target.name)
			if !ok {
				t.Fatalf("[%d %s] vlan %s vanished after update", i, base, target.name)
			}
			if len(op.Addresses) > 0 {
				if v, _ := e.Get("address"); v != op.Addresses[0] {
					t.Errorf("[%d %s] vlan %s address = %q, want %q", i, base, target.name, v, op.Addresses[0])
				}
			}
			checkUpdatedMTU(t, i, base, e, target.name, op.MTU)
		}
	}
}

// mutateAndReparse applies op to f and re-parses the rendered bytes from
// scratch (the same round-trip-through-bytes oracle the create property
// test uses).
func mutateAndReparse(t *testing.T, i int, base string, f *host.File, op Op) *host.File {
	t.Helper()
	if err := Mutate(f, op, "PROPCS"); err != nil {
		t.Fatalf("[%d %s] Mutate %s: %v", i, base, op.Kind(), err)
	}
	reparsed, err := host.ParseInterfaces([]byte(f.Render()))
	if err != nil {
		t.Fatalf("[%d %s] mutated output does not reparse after %s: %v\n%s", i, base, op.Kind(), err, f.Render())
	}
	return reparsed
}

// mutateAndCheckDeleted applies a delete op and asserts the entity's iface
// stanza(s) and auto/allow wiring are gone from the re-parsed output.
func mutateAndCheckDeleted(t *testing.T, i int, base string, f *host.File, op Op, name string) {
	t.Helper()
	reparsed := mutateAndReparse(t, i, base, f, op)
	if _, ok := reparsed.Iface(name); ok {
		t.Errorf("[%d %s] %s: iface stanza for %s still present after delete", i, base, op.Kind(), name)
	}
	for _, n := range reparsed.AutoIfaces() {
		if n == name {
			t.Errorf("[%d %s] %s: %s still autostart-wired after delete", i, base, op.Kind(), name)
		}
	}
}

// checkUpdatedMTU asserts the mtu option matches what an update op set
// (update ops in this test always set a nonzero MTU).
func checkUpdatedMTU(t *testing.T, i int, base string, e *host.Entry, name string, mtu int) {
	t.Helper()
	if v, ok := e.Get("mtu"); !ok || v != strconv.Itoa(mtu) {
		t.Errorf("[%d %s] %s mtu = %q (present=%v), want %d", i, base, name, v, ok, mtu)
	}
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
