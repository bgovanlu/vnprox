package change

import (
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// maxValidate100OpsDuration is T-202 acceptance criterion 4's budget.
const maxValidate100OpsDuration = 200 * time.Millisecond

// fixCase is one corpus entry for TestValidate_FixProperty: a single op
// that is guaranteed to produce exactly one error finding with a non-nil
// Fix, evaluated against a snapshot that satisfies every *other*
// (referential) requirement the op has — so that after the fix is applied,
// the only thing that could still be wrong is the one field the fix
// corrects. This is what makes "revalidates clean" a meaningful assertion
// about the fix itself, rather than an artifact of an under-specified
// snapshot.
type fixCase struct {
	snap inventory.Snapshot
	name string
	op   Op
}

func fixCases() []fixCase {
	pve1eno1 := &inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1"}
	pve1eno2 := &inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno2"), Name: "eno2"}
	vmbr0 := &inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0"}
	zone1 := &inventory.SdnZone{Ref: testRef(inventory.KindSDNZone, "", "zone1"), ID: "zone1", Type: "vxlan"}
	vnet1 := &inventory.SdnVnet{Ref: testRef(inventory.KindSDNVnet, "", "zone1/vnet1"), ID: "zone1/vnet1", Zone: "zone1"}
	bond0 := &inventory.Bond{Ref: testRef(inventory.KindBond, "pve1", "bond0"), Name: "bond0", Mode: "active-backup", Slaves: []string{"eno1"}}
	vlan20 := &inventory.VlanIface{Ref: testRef(inventory.KindVlan, "pve1", "vmbr0.20"), Name: "vmbr0.20", ParentName: "vmbr0", Vid: 20}

	return []fixCase{
		{
			name: "bond.create mtu clamp",
			snap: buildSnapshot(pve1eno1, pve1eno2),
			op: mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond1"),
				&BondCreateParams{Mode: "active-backup", Slaves: []string{"eno1", "eno2"}, MTU: 100}),
		},
		{
			name: "bond.update mtu clamp",
			snap: buildSnapshot(bond0, pve1eno1),
			op:   mkOp(OpBondUpdate, testRef(inventory.KindBond, "pve1", "bond0"), &BondUpdateParams{MTU: intPtr(100)}),
		},
		{
			name: "bridge.create mtu clamp",
			snap: buildSnapshot(),
			op: mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr9"),
				&BridgeCreateParams{Comments: "x", MTU: 20000}),
		},
		{
			name: "bridge.create vid range clamp",
			snap: buildSnapshot(),
			op: mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr9"),
				&BridgeCreateParams{Comments: "x", VlanAware: true, Vids: []VidRange{{Low: 5000, High: 6000}}}),
		},
		{
			name: "vlan.create vid clamp",
			snap: buildSnapshot(vmbr0),
			op: mkOp(OpVlanCreate, testRef(inventory.KindVlan, "pve1", "vmbr0.9000"),
				&VlanCreateParams{Parent: "vmbr0", Vid: 9000}),
		},
		{
			name: "vlan.update mtu clamp",
			snap: buildSnapshot(vmbr0, vlan20),
			op:   mkOp(OpVlanUpdate, testRef(inventory.KindVlan, "pve1", "vmbr0.20"), &VlanUpdateParams{MTU: intPtr(100)}),
		},
		{
			name: "iface.update mtu clamp",
			snap: buildSnapshot(pve1eno1),
			op:   mkOp(OpIfaceUpdate, testRef(inventory.KindPhysNic, "pve1", "eno1"), &IfaceUpdateParams{MTU: intPtr(20000)}),
		},
		{
			name: "sdn.zone.create mtu clamp",
			snap: buildSnapshot(),
			op:   mkOp(OpSdnZoneCreate, testRef(inventory.KindSDNZone, "", "zone9"), &SdnZoneCreateParams{Type: "simple", MTU: 20000}),
		},
		{
			name: "sdn.zone.update mtu clamp",
			snap: buildSnapshot(zone1),
			op:   mkOp(OpSdnZoneUpdate, testRef(inventory.KindSDNZone, "", "zone1"), &SdnZoneUpdateParams{MTU: intPtr(100)}),
		},
		{
			name: "sdn.vnet.create tag clamp",
			snap: buildSnapshot(zone1),
			op:   mkOp(OpSdnVnetCreate, testRef(inventory.KindSDNVnet, "", "zone1/vnet9"), &SdnVnetCreateParams{Zone: "zone1", Tag: 5000}),
		},
		{
			name: "sdn.vnet.update tag clamp",
			snap: buildSnapshot(zone1, vnet1),
			op:   mkOp(OpSdnVnetUpdate, testRef(inventory.KindSDNVnet, "", "zone1/vnet1"), &SdnVnetUpdateParams{Tag: intPtr(6000)}),
		},
	}
}

// TestValidate_FixProperty is T-202 acceptance criterion 3: every emitted
// `fix` patch, when substituted for the offending op it was computed from,
// revalidates with no error-severity findings.
func TestValidate_FixProperty(t *testing.T) {
	for _, tc := range fixCases() {
		t.Run(tc.name, func(t *testing.T) {
			before := Validate([]Op{tc.op}, tc.snap)
			if !hasError(before) {
				t.Fatalf("fixture op did not produce an error finding to fix: %+v", before)
			}

			var fix []Op
			for _, f := range before {
				if f.Severity == SeverityError && f.Fix != nil {
					fix = f.Fix
					break
				}
			}
			if fix == nil {
				t.Fatalf("no error finding in %+v carried a Fix", before)
			}
			if len(fix) != 1 {
				t.Fatalf("Fix = %+v, want exactly one replacement op", fix)
			}
			if fix[0].Type != tc.op.Type || fix[0].Target != tc.op.Target {
				t.Fatalf("Fix op = %+v, want same Type/Target as the original op %+v", fix[0], tc.op)
			}

			after := Validate(fix, tc.snap)
			if hasError(after) {
				t.Errorf("after applying fix %+v, still have error findings: %+v", fix[0], after)
			}
		})
	}
}

// --- benchmark (T-202 acceptance criterion 4) ------------------------------

// hundredOps builds a 100-op changeset of independent, schema/referential-
// clean bridge.create ops (unique targets, no snapshot dependency) — enough
// to exercise the full pipeline's per-op cost at a representative scale
// without needing a large fixture snapshot.
func hundredOps() []Op {
	ops := make([]Op, 0, 100)
	for i := 0; i < 100; i++ {
		id := "vmbr" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		ops = append(ops, mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", id),
			&BridgeCreateParams{Comments: "bench", MTU: 1500}))
	}
	return ops
}

func TestValidate_100OpsUnder200ms(t *testing.T) {
	ops := hundredOps()
	snap := buildSnapshot()

	start := time.Now()
	findings := Validate(ops, snap)
	elapsed := time.Since(start)

	if hasError(findings) {
		t.Fatalf("unexpected error findings over a clean 100-op changeset: %+v", findings)
	}
	if elapsed > maxValidate100OpsDuration {
		t.Errorf("Validate(100 ops) took %s, want < %s", elapsed, maxValidate100OpsDuration)
	}
}

func BenchmarkValidate_100Ops(b *testing.B) {
	ops := hundredOps()
	snap := buildSnapshot()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Validate(ops, snap)
	}
}
