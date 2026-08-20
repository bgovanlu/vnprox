package inventory

import (
	"fmt"
	"sort"
	"testing"
)

// refFor returns a fixed test Ref of the given kind.
func refFor(kind Kind) Ref { return Ref{Kind: kind, Node: "n", ID: "x"} }

func newEmptyPartial(kind Kind) Entity {
	r := refFor(kind)
	switch kind {
	case KindBond, KindOVSBond:
		return &Bond{Ref: r}
	case KindBridge, KindOVSBridge:
		return &Bridge{Ref: r}
	case KindVlan:
		return &VlanIface{Ref: r}
	default:
		return &PhysNic{Ref: r}
	}
}

// setField sets exactly one field on a partial to an idx-distinct, non-zero
// value so that different sources contribute distinguishable values.
func setField(t *testing.T, e Entity, field string, idx int) {
	t.Helper()
	s := fmt.Sprintf("%s-%d", field, idx)
	n := 1000 + idx
	b := idx == 0
	set := func(ok bool) {
		if !ok {
			t.Fatalf("setField: unhandled field %q for %T", field, e)
		}
	}
	switch v := e.(type) {
	case *PhysNic:
		switch field {
		case "name":
			v.Name = s
		case "mac":
			v.Mac = s
		case "driver":
			v.Driver = s
		case "pciAddr":
			v.PCIAddr = s
		case "duplex":
			v.Duplex = s
		case "mediaPort":
			v.MediaPort = s
		case "operState":
			v.OperState = s
		case "speedMbps":
			v.SpeedMbps = n
		case "mtu":
			v.MTU = n
		case "mtuDeclared":
			v.MTUDeclared = n
		case "linkUp":
			// Flagged optional bool: a contribution only counts as reported
			// when the companion Set flag is raised (see merge.go).
			v.LinkUp, v.LinkUpSet = b, true
		case "pending":
			v.Pending = s
		default:
			set(false)
		}
	case *Bond:
		switch field {
		case "name":
			v.Name = s
		case "mode":
			v.Mode = s
		case "lacpRate":
			v.LACPRate = s
		case "xmitHashPolicy":
			v.XmitHashPolicy = s
		case "miiStatus":
			v.MIIStatus = s
		case "activeSlave":
			v.ActiveSlave = s
		case "mtu":
			v.MTU = n
		case "mtuDeclared":
			v.MTUDeclared = n
		case "slaves":
			v.Slaves = []string{s}
		case "declaredSlaves":
			v.DeclaredSlaves = []string{s}
		case "slaveDetail":
			v.SlaveDetail = []BondSlaveState{{Name: s}}
		case "pending":
			v.Pending = s
		default:
			set(false)
		}
	case *Bridge:
		switch field {
		case "name":
			v.Name = s
		case "virt":
			if b {
				v.Virt = BridgeLinux
			} else {
				v.Virt = BridgeOVS
			}
		case "gateway":
			v.Gateway = s
		case "comments":
			v.Comments = s
		case "portNames":
			v.PortNames = []string{s}
		case "declaredPortNames":
			v.DeclaredPortNames = []string{s}
		case "addresses":
			v.Addresses = []string{s}
		case "vids":
			v.Vids = []VidRange{{Low: 10 + idx, High: 10 + idx}}
		case "mtu":
			v.MTU = n
		case "mtuDeclared":
			v.MTUDeclared = n
		case "vlanAware":
			v.VlanAware, v.VlanAwareSet = b, true
		case "stp":
			v.STP, v.STPSet = b, true
		case "pending":
			v.Pending = s
		default:
			set(false)
		}
	case *VlanIface:
		switch field {
		case "name":
			v.Name = s
		case "parentName":
			v.ParentName = s
		case "vid":
			v.Vid = n
		case "mtu":
			v.MTU = n
		case "mtuDeclared":
			v.MTUDeclared = n
		case "addresses":
			v.Addresses = []string{s}
		case "pending":
			v.Pending = s
		case "virt":
			v.Virt = s
		case "trunks":
			v.Trunks = []VidRange{{Low: 10 + idx, High: 10 + idx}}
		default:
			set(false)
		}
	default:
		t.Fatalf("setField: unhandled entity type %T", e)
	}
}

// TestOwnershipTableExhaustive is acceptance criterion #2: for EVERY
// (kind, field) in the ownership table, with every precedence source
// present and contributing a distinct value, assert the resolved value came
// from the highest-precedence source and that lower-precedence disagreements
// are recorded as provenance conflicts (never silently dropped) exactly when
// the rule flags conflicts.
func TestOwnershipTableExhaustive(t *testing.T) {
	for kind, fields := range ownershipRules {
		for field, rule := range fields {
			kind, field, rule := kind, field, rule
			t.Run(string(kind)+"/"+field, func(t *testing.T) {
				if len(rule.Precedence) == 0 {
					t.Fatalf("field %s.%s has empty precedence", kind, field)
				}
				ref := refFor(kind)
				parts := map[Source]Entity{}
				for i, src := range rule.Precedence {
					e := newEmptyPartial(kind)
					setField(t, e, field, i)
					parts[src] = e
				}

				// Expected winner value: resolve with only the top-precedence
				// source present.
				top := rule.Precedence[0]
				solo := map[Source]Entity{top: parts[top]}
				wantVal := resolveEntity(ref, solo).entity.fieldMap()[field]

				res := resolveEntity(ref, parts)
				if got := res.entity.fieldMap()[field]; got != wantVal {
					t.Errorf("resolved %s.%s = %q, want winner (%s) value %q", kind, field, got, top, wantVal)
				}
				fp, ok := res.prov.Fields[field]
				if !ok {
					t.Fatalf("no provenance recorded for %s.%s", kind, field)
				}
				if fp.Owner != top {
					t.Errorf("%s.%s owner = %s, want %s", kind, field, fp.Owner, top)
				}
				gotConflicts := conflictSources(fp)
				if rule.FlagConflict {
					want := append([]Source(nil), rule.Precedence[1:]...)
					if !sameSources(gotConflicts, want) {
						t.Errorf("%s.%s conflicts = %v, want %v (all lower-precedence sources disagree)", kind, field, gotConflicts, want)
					}
				} else if len(gotConflicts) != 0 {
					t.Errorf("%s.%s is not a conflict-flagged field but recorded conflicts %v", kind, field, gotConflicts)
				}
			})
		}
	}
}

// TestOwnershipSubsetPresence checks the fallback rule: when the top
// precedence source is absent, the next present source wins and owns the
// field.
func TestOwnershipSubsetPresence(t *testing.T) {
	ref := refFor(KindBridge)
	// mtuDeclared precedence: [host-interfaces, pve-network]. Only pve
	// present -> pve wins.
	pve := &Bridge{Ref: ref, MTUDeclared: 1500}
	res := resolveEntity(ref, map[Source]Entity{SourcePVENetwork: pve})
	if res.entity.(*Bridge).MTUDeclared != 1500 {
		t.Fatalf("MTUDeclared = %d, want 1500", res.entity.(*Bridge).MTUDeclared)
	}
	if res.prov.Fields["mtuDeclared"].Owner != SourcePVENetwork {
		t.Errorf("owner = %s, want pve-network", res.prov.Fields["mtuDeclared"].Owner)
	}
	if len(res.prov.Fields["mtuDeclared"].Conflicts) != 0 {
		t.Errorf("single-source field should have no conflicts")
	}
}

// TestOptionalBoolNotReported checks the F-14 fix: a flagged boolean field
// a source did not actually set (companion *Set flag false) is treated as
// "not reported" — it neither wins the merge nor registers a spurious
// provenance conflict — instead of implicitly reporting false.
func TestOptionalBoolNotReported(t *testing.T) {
	ref := refFor(KindBridge)

	// Netlink observed the bridge but without bridge detail (VlanAwareSet
	// unset); PVE genuinely declares vlan-aware true. PVE must win with no
	// conflict, even though netlink outranks it in precedence.
	res := resolveEntity(ref, map[Source]Entity{
		SourceHostNetlink: &Bridge{Ref: ref, Name: "x", MTU: 1500},
		SourcePVENetwork:  &Bridge{Ref: ref, Name: "x", VlanAware: true, VlanAwareSet: true},
	})
	b := res.entity.(*Bridge)
	if !b.VlanAware || !b.VlanAwareSet {
		t.Errorf("vlanAware = (%v, set=%v), want (true, set=true) from pve-network", b.VlanAware, b.VlanAwareSet)
	}
	fp := res.prov.Fields["vlanAware"]
	if fp.Owner != SourcePVENetwork {
		t.Errorf("vlanAware owner = %s, want pve-network", fp.Owner)
	}
	if len(fp.Conflicts) != 0 {
		t.Errorf("unreported netlink bool must not conflict, got %+v", fp.Conflicts)
	}

	// stp: netlink genuinely reports false (set), interfaces genuinely
	// declares true (set) -> netlink's false wins, interfaces conflicts.
	res = resolveEntity(ref, map[Source]Entity{
		SourceHostNetlink:    &Bridge{Ref: ref, Name: "x", STP: false, STPSet: true},
		SourceHostInterfaces: &Bridge{Ref: ref, Name: "x", STP: true, STPSet: true},
	})
	b = res.entity.(*Bridge)
	if b.STP || !b.STPSet {
		t.Errorf("stp = (%v, set=%v), want (false, set=true): an explicit false report must win", b.STP, b.STPSet)
	}
	if got := res.prov.Fields["stp"].Conflicts; len(got) != 1 || got[0].Source != SourceHostInterfaces || got[0].Value != "1" {
		t.Errorf("stp conflicts = %+v, want one host-interfaces=1 conflict", got)
	}

	// Nobody reported: no provenance entry at all, Set flag false.
	res = resolveEntity(ref, map[Source]Entity{
		SourcePVENetwork: &Bridge{Ref: ref, Name: "x", MTUDeclared: 1500},
	})
	b = res.entity.(*Bridge)
	if b.STPSet {
		t.Errorf("stp reported by nobody must resolve with STPSet=false")
	}
	if _, ok := res.prov.Fields["stp"]; ok {
		t.Errorf("stp reported by nobody must carry no provenance entry")
	}
}

// TestMTUDualExposure documents the flagship rule: runtime MTU (host-netlink)
// and declared MTU (host-interfaces) are exposed as separate fields, not
// merged into one number, and a runtime≠declared difference is NOT a
// conflict (they are different facts).
func TestMTUDualExposure(t *testing.T) {
	ref := refFor(KindBridge)
	netlink := &Bridge{Ref: ref, MTU: 1500}
	intf := &Bridge{Ref: ref, MTUDeclared: 9000}
	res := resolveEntity(ref, map[Source]Entity{
		SourceHostNetlink:    netlink,
		SourceHostInterfaces: intf,
	})
	b := res.entity.(*Bridge)
	if b.MTU != 1500 {
		t.Errorf("runtime MTU = %d, want 1500", b.MTU)
	}
	if b.MTUDeclared != 9000 {
		t.Errorf("declared MTU = %d, want 9000", b.MTUDeclared)
	}
	if res.prov.HasConflicts() {
		t.Errorf("runtime vs declared MTU must not be a conflict, got %+v", res.prov.Fields)
	}
	if res.prov.Fields["mtu"].Owner != SourceHostNetlink {
		t.Errorf("mtu owner = %s, want host-netlink", res.prov.Fields["mtu"].Owner)
	}
	if res.prov.Fields["mtuDeclared"].Owner != SourceHostInterfaces {
		t.Errorf("mtuDeclared owner = %s, want host-interfaces", res.prov.Fields["mtuDeclared"].Owner)
	}
}

// TestDeclaredConflictTagged checks that two sources both claiming to
// describe the DECLARED MTU that disagree produce a provenance-tagged
// entity, not silent last-write-wins.
func TestDeclaredConflictTagged(t *testing.T) {
	ref := refFor(KindBridge)
	intf := &Bridge{Ref: ref, MTUDeclared: 9000}
	pve := &Bridge{Ref: ref, MTUDeclared: 1500}
	res := resolveEntity(ref, map[Source]Entity{
		SourceHostInterfaces: intf,
		SourcePVENetwork:     pve,
	})
	if res.entity.(*Bridge).MTUDeclared != 9000 {
		t.Errorf("declared MTU winner = %d, want 9000 (interfaces file authoritative)", res.entity.(*Bridge).MTUDeclared)
	}
	fp := res.prov.Fields["mtuDeclared"]
	if len(fp.Conflicts) != 1 || fp.Conflicts[0].Source != SourcePVENetwork || fp.Conflicts[0].Value != "1500" {
		t.Errorf("expected pve-network conflict value 1500, got %+v", fp.Conflicts)
	}
}

// TestPVEDoesNotClobberRuntime checks the cross-source guarantee end to end
// through the Graph: a PVE (declared) poll cannot overwrite host-netlink
// runtime-owned fields, and vice versa.
func TestPVEDoesNotClobberRuntime(t *testing.T) {
	g := NewGraph()
	ref := Ref{Kind: KindBridge, Node: "pve1", ID: "vmbr0"}
	g.ApplyPoll(SourceHostNetlink, Scope{Node: "pve1"}, []Entity{
		&Bridge{Ref: ref, Name: "vmbr0", MTU: 1500, VlanAware: true, VlanAwareSet: true},
	})
	g.ApplyPoll(SourcePVENetwork, Scope{Node: "pve1"}, []Entity{
		&Bridge{Ref: ref, Name: "vmbr0", MTUDeclared: 9000, Comments: "uplink"},
	})
	e, _ := g.Snapshot().Get(ref)
	b := e.(*Bridge)
	if b.MTU != 1500 {
		t.Errorf("runtime MTU clobbered: %d, want 1500", b.MTU)
	}
	if !b.VlanAware {
		t.Errorf("runtime vlanAware clobbered")
	}
	if b.MTUDeclared != 9000 {
		t.Errorf("declared MTU = %d, want 9000", b.MTUDeclared)
	}
	if b.Comments != "uplink" {
		t.Errorf("declared comments = %q, want uplink", b.Comments)
	}
}

// TestOVSReusesBridgeRules checks OVS variants resolve through the same
// ownership table as their linux counterparts.
func TestOVSReusesBridgeRules(t *testing.T) {
	ref := Ref{Kind: KindOVSBridge, Node: "pve1", ID: "vmbr1"}
	res := resolveEntity(ref, map[Source]Entity{
		SourceHostNetlink: &Bridge{Ref: ref, MTU: 9000},
	})
	if res.entity.(*Bridge).MTU != 9000 {
		t.Errorf("ovs-bridge MTU = %d, want 9000", res.entity.(*Bridge).MTU)
	}
}

func conflictSources(fp FieldProv) []Source {
	out := make([]Source, len(fp.Conflicts))
	for i, c := range fp.Conflicts {
		out[i] = c.Source
	}
	return out
}

func sameSources(a, b []Source) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]Source(nil), a...)
	bc := append([]Source(nil), b...)
	sort.Slice(ac, func(i, j int) bool { return ac[i] < ac[j] })
	sort.Slice(bc, func(i, j int) bool { return bc[i] < bc[j] })
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}
