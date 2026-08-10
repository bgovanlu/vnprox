package topology_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/topology"
)

// --- fixtures -------------------------------------------------------------

// baseInterfaces is a plausible PVE node file: a loopback (which contributes
// no entity), two NICs, and one bridge.
const baseInterfaces = `auto lo
iface lo inet loopback

auto eno1
iface eno1 inet manual
	mtu 1500

auto eno2
iface eno2 inet manual
	mtu 1500

auto vmbr0
iface vmbr0 inet static
	address 192.168.1.10/24
	gateway 192.168.1.1
	bridge-ports eno1
	bridge-stp off
	bridge-fd 0
	mtu 1500
`

// wideInterfaces declares enough entities that a map-ordered implementation
// would shuffle its output between runs. It is the fixture behind the
// determinism guard below — without it, that guard would pass on a
// single-entity file no matter how the entities were ordered.
func wideInterfaces() string {
	var b strings.Builder
	b.WriteString("auto lo\niface lo inet loopback\n\n")
	for _, name := range []string{"vmbr9", "vmbr3", "vmbr7", "vmbr1", "vmbr5", "vmbr0", "vmbr8", "vmbr2", "vmbr6", "vmbr4"} {
		b.WriteString("auto " + name + "\niface " + name + " inet manual\n\tbridge-ports none\n\tmtu 1500\n\n")
	}
	for _, name := range []string{"eno4", "eno1", "eno3", "eno2"} {
		b.WriteString("auto " + name + "\niface " + name + " inet manual\n\tmtu 9000\n\n")
	}
	return b.String()
}

func mustEntities(t *testing.T, node, content string) []topology.PointEntity {
	t.Helper()
	ents, err := topology.EntitiesFromInterfaces(node, content)
	if err != nil {
		t.Fatalf("EntitiesFromInterfaces(%s): %v", node, err)
	}
	return ents
}

// findDiff returns the diff row for ref, or fails the test naming everything
// that WAS reported — a diff that omitted the row is the failure mode worth
// reading about.
func findDiff(t *testing.T, diffs []topology.EntityDiff, ref string) topology.EntityDiff {
	t.Helper()
	for _, d := range diffs {
		if d.Ref == ref {
			return d
		}
	}
	got := make([]string, 0, len(diffs))
	for _, d := range diffs {
		got = append(got, string(d.Change)+" "+d.Ref)
	}
	t.Fatalf("no diff row for %s; reported: %v", ref, got)
	return topology.EntityDiff{}
}

func fieldChange(t *testing.T, d topology.EntityDiff, field string) topology.FieldChange {
	t.Helper()
	for _, f := range d.Fields {
		if f.Field == field {
			return f
		}
	}
	got := make([]string, 0, len(d.Fields))
	for _, f := range d.Fields {
		got = append(got, f.Field)
	}
	t.Fatalf("%s has no field-level change for %q; it reports: %v", d.Ref, field, got)
	return topology.FieldChange{}
}

// --- entity extraction ----------------------------------------------------

func TestEntitiesFromInterfaces_ClassifiesAndSkipsLoopback(t *testing.T) {
	ents := mustEntities(t, "pve1", baseInterfaces)

	var refs []string
	for _, e := range ents {
		refs = append(refs, e.Ref.String())
	}
	want := []string{"bridge:pve1:vmbr0", "physnic:pve1:eno1", "physnic:pve1:eno2"}
	if len(refs) != len(want) {
		t.Fatalf("entities = %v, want %v", refs, want)
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Fatalf("entities = %v, want %v (sorted by ref)", refs, want)
		}
	}

	br := ents[0]
	for field, wantVal := range map[string]string{
		"Name":              "vmbr0",
		"Virt":              "linux",
		"Gateway":           "192.168.1.1",
		"Addresses":         "192.168.1.10/24",
		"DeclaredPortNames": "eno1",
		"MTUDeclared":       "1500",
	} {
		if got := br.Fields[field]; got != wantVal {
			t.Errorf("vmbr0 field %s = %q, want %q", field, got, wantVal)
		}
	}
}

func TestEntitiesFromInterfaces_MalformedFileIsAnErrorNotAnEmptySet(t *testing.T) {
	// An unparseable file must never quietly extract to zero entities: a
	// zero-entity side would make every entity on the other side render as
	// added or removed, which is the same false statement as an empty diff.
	_, err := topology.EntitiesFromInterfaces("pve1", "iface\n")
	if err == nil {
		t.Fatal("a malformed interfaces file must return an error, not an empty entity set")
	}
}

// --- the diff itself ------------------------------------------------------

func TestDiffPoints_Table(t *testing.T) {
	withSecondBridge := baseInterfaces + "\nauto vmbr1\niface vmbr1 inet manual\n\tbridge-ports eno2\n\tbridge-vlan-aware yes\n\tbridge-vids 2-100\n\tmtu 9000\n"

	tests := []struct {
		wantChange map[string]topology.DiffChange
		wantFields map[string][2]string // ref/field -> {before, after}
		name       string
		from       string
		to         string
	}{
		{
			name:       "identical content reports nothing",
			from:       baseInterfaces,
			to:         baseInterfaces,
			wantChange: map[string]topology.DiffChange{},
		},
		{
			name:       "a new bridge is added",
			from:       baseInterfaces,
			to:         withSecondBridge,
			wantChange: map[string]topology.DiffChange{"bridge:pve1:vmbr1": topology.DiffAdded},
			wantFields: map[string][2]string{
				"bridge:pve1:vmbr1|MTUDeclared": {"", "9000"},
				"bridge:pve1:vmbr1|Vids":        {"", "2-100"},
			},
		},
		{
			name:       "a deleted bridge is removed",
			from:       withSecondBridge,
			to:         baseInterfaces,
			wantChange: map[string]topology.DiffChange{"bridge:pve1:vmbr1": topology.DiffRemoved},
			wantFields: map[string][2]string{
				"bridge:pve1:vmbr1|DeclaredPortNames": {"eno2", ""},
			},
		},
		{
			name:       "an MTU edit is modified with before and after",
			from:       baseInterfaces,
			to:         strings.Replace(baseInterfaces, "\tbridge-ports eno1\n\tbridge-stp off\n\tbridge-fd 0\n\tmtu 1500\n", "\tbridge-ports eno1\n\tbridge-stp off\n\tbridge-fd 0\n\tmtu 9000\n", 1),
			wantChange: map[string]topology.DiffChange{"bridge:pve1:vmbr0": topology.DiffModified},
			wantFields: map[string][2]string{
				"bridge:pve1:vmbr0|MTUDeclared": {"1500", "9000"},
			},
		},
		{
			name:       "a port list edit is modified with both lists",
			from:       baseInterfaces,
			to:         strings.Replace(baseInterfaces, "bridge-ports eno1", "bridge-ports eno1 eno2", 1),
			wantChange: map[string]topology.DiffChange{"bridge:pve1:vmbr0": topology.DiffModified},
			wantFields: map[string][2]string{
				"bridge:pve1:vmbr0|DeclaredPortNames": {"eno1", "eno1 eno2"},
			},
		},
		{
			name:       "an address change is modified",
			from:       baseInterfaces,
			to:         strings.Replace(baseInterfaces, "192.168.1.10/24", "10.0.0.10/24", 1),
			wantChange: map[string]topology.DiffChange{"bridge:pve1:vmbr0": topology.DiffModified},
			wantFields: map[string][2]string{
				"bridge:pve1:vmbr0|Addresses": {"192.168.1.10/24", "10.0.0.10/24"},
			},
		},
		{
			name: "a renamed NIC is one removal and one addition",
			from: baseInterfaces,
			to:   strings.Replace(baseInterfaces, "auto eno2\niface eno2 inet manual", "auto enp5s0\niface enp5s0 inet manual", 1),
			wantChange: map[string]topology.DiffChange{
				"physnic:pve1:eno2":   topology.DiffRemoved,
				"physnic:pve1:enp5s0": topology.DiffAdded,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			diffs := topology.DiffPoints(mustEntities(t, "pve1", tc.from), mustEntities(t, "pve1", tc.to))
			if diffs == nil {
				t.Fatal("DiffPoints returned nil; it must return an empty slice so `[]` marshals as \"compared, found nothing\"")
			}
			if len(diffs) != len(tc.wantChange) {
				got := make([]string, 0, len(diffs))
				for _, d := range diffs {
					got = append(got, string(d.Change)+" "+d.Ref)
				}
				t.Fatalf("reported %v, want exactly %v", got, tc.wantChange)
			}
			for ref, want := range tc.wantChange {
				if got := findDiff(t, diffs, ref).Change; got != want {
					t.Errorf("%s change = %q, want %q", ref, got, want)
				}
			}
			for key, want := range tc.wantFields {
				ref, field, _ := strings.Cut(key, "|")
				fc := fieldChange(t, findDiff(t, diffs, ref), field)
				if fc.Before != want[0] || fc.After != want[1] {
					t.Errorf("%s.%s = %q -> %q, want %q -> %q", ref, field, fc.Before, fc.After, want[0], want[1])
				}
			}
		})
	}
}

// AC6 lives or dies on this: "modified" alone is not an answer an operator can
// act on. Every modified row must carry at least one field with a distinct
// before and after.
func TestDiffPoints_ModifiedAlwaysCarriesFieldLevelBeforeAndAfter(t *testing.T) {
	to := strings.Replace(baseInterfaces, "\tmtu 1500\n\nauto vmbr0", "\tmtu 9000\n\nauto vmbr0", 1)
	diffs := topology.DiffPoints(mustEntities(t, "pve1", baseInterfaces), mustEntities(t, "pve1", to))

	modified := 0
	for _, d := range diffs {
		if d.Change != topology.DiffModified {
			continue
		}
		modified++
		if len(d.Fields) == 0 {
			t.Errorf("%s is reported modified with no field-level detail", d.Ref)
		}
		for _, f := range d.Fields {
			if f.Before == f.After {
				t.Errorf("%s.%s reports an identical before/after (%q) — that is not a change", d.Ref, f.Field, f.Before)
			}
		}
	}
	if modified == 0 {
		t.Fatal("fixture produced no modified entity; the assertion above proved nothing")
	}
}

// AC5 at the pure level: a point against itself is empty. Asserted on a file
// with many entities so "empty" is a real result, not an artifact of there
// being nothing to compare.
func TestDiffPoints_PointAgainstItselfIsEmpty(t *testing.T) {
	ents := mustEntities(t, "pve1", wideInterfaces())
	if len(ents) < 10 {
		t.Fatalf("fixture yielded %d entities; too few for this assertion to mean anything", len(ents))
	}
	if diffs := topology.DiffPoints(ents, ents); len(diffs) != 0 {
		t.Fatalf("diffing a point against itself reported %d changes, want 0: %+v", len(diffs), diffs)
	}
}

// The output order must not depend on Go map iteration. internal/pvemock's
// list endpoints are order-nondeterministic (T-2502-followup-01), and a diff
// that inherited that would report spurious rows roughly one run in three.
func TestDiffPoints_OutputOrderIsStableAcrossRuns(t *testing.T) {
	from := wideInterfaces()
	to := strings.ReplaceAll(from, "mtu 1500", "mtu 1400")

	var first []string
	for run := range 40 {
		diffs := topology.DiffPoints(mustEntities(t, "pve1", from), mustEntities(t, "pve1", to))
		order := make([]string, 0, len(diffs))
		for _, d := range diffs {
			fields := make([]string, 0, len(d.Fields))
			for _, f := range d.Fields {
				fields = append(fields, f.Field)
			}
			order = append(order, d.Ref+"["+strings.Join(fields, ",")+"]")
		}
		if run == 0 {
			first = order
			if len(first) < 10 {
				t.Fatalf("fixture produced %d changed entities; too few for map-order nondeterminism to show", len(first))
			}
			continue
		}
		if strings.Join(order, ";") != strings.Join(first, ";") {
			t.Fatalf("run %d order\n got %v\nwant %v", run, order, first)
		}
	}
}

// A node captured on only one side is the caller's problem to scope, but the
// pure function must at least be honest about what it was handed: entities it
// was given only on one side ARE added/removed. This pins the contract that
// change.TopologyDiff's node intersection relies on.
func TestDiffPoints_TreatsWhatItIsHandedAsComplete(t *testing.T) {
	from := mustEntities(t, "pve1", baseInterfaces)
	to := mustEntities(t, "pve2", baseInterfaces)

	diffs := topology.DiffPoints(from, to)
	if len(diffs) != len(from)+len(to) {
		t.Fatalf("reported %d changes, want %d (every pve1 entity removed, every pve2 entity added)", len(diffs), len(from)+len(to))
	}
}
