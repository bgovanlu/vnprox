// SPDX-License-Identifier: Apache-2.0

package whatif

import (
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/capacity"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
)

// --- test world builder (mirrors internal/failsim/testhelpers_test.go's
// pattern: apply entities through a real inventory.Graph so attachment
// resolution runs exactly as it would in production) ------------------------

type world struct {
	host   map[string][]inventory.Entity
	nodes  []inventory.Entity
	guests []inventory.Entity
}

func newWorld() *world { return &world{host: map[string][]inventory.Entity{}} }

func (w *world) node(name string) *world {
	w.nodes = append(w.nodes, &inventory.Node{
		Ref: inventory.Ref{Kind: inventory.KindNode, Node: name, ID: name}, Name: name, Status: "online",
	})
	return w
}

func (w *world) physnic(node, name string, linkUp bool) *world {
	w.host[node] = append(w.host[node], &inventory.PhysNic{
		Ref: inventory.Ref{Kind: inventory.KindPhysNic, Node: node, ID: name}, Name: name,
		LinkUp: linkUp, LinkUpSet: true, SpeedMbps: 1000,
	})
	return w
}

func (w *world) bridge(node, name string, ports ...string) *world {
	w.host[node] = append(w.host[node], &inventory.Bridge{
		Ref: inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name}, Name: name,
		Virt: inventory.BridgeLinux, PortNames: ports, DeclaredPortNames: ports,
	})
	return w
}

func (w *world) guest(node, vmid string) *world {
	w.guests = append(w.guests, &inventory.Guest{
		Ref: inventory.Ref{Kind: inventory.KindGuest, Node: node, ID: vmid}, Node: node, Type: "qemu", Status: "running",
	})
	return w
}

func (w *world) nic(node, vmid, key, target string) *world {
	w.guests = append(w.guests, &inventory.GuestNic{
		Ref:   inventory.Ref{Kind: inventory.KindGuestNic, Node: node, ID: vmid + "/" + key},
		Guest: inventory.Ref{Kind: inventory.KindGuest, Node: node, ID: vmid},
		Key:   key, TargetName: target,
	})
	return w
}

func (w *world) build() inventory.Snapshot {
	g := inventory.NewGraph()
	for node, ents := range w.host {
		g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node}, ents)
	}
	if len(w.nodes) > 0 {
		g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, w.nodes)
	}
	if len(w.guests) > 0 {
		g.ApplyPoll(inventory.SourcePVEGuest,
			inventory.Scope{Kinds: []inventory.Kind{inventory.KindGuest, inventory.KindGuestNic}}, w.guests)
	}
	return g.Snapshot()
}

// redundantWorld: vmbr9 on pve1 rides a 2-NIC-redundant path (both uplinks
// modeled as directly-bridged physnics — losing one still leaves the other),
// so failing either uplink alone never disconnects a guest on vmbr9.
func redundantWorld() (inventory.Snapshot, inventory.Ref) {
	w := newWorld()
	w.node("pve1")
	w.physnic("pve1", "eno1", true)
	w.physnic("pve1", "eno2", true)
	w.bridge("pve1", "vmbr9", "eno1", "eno2")
	snap := w.build()
	return snap, inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
}

// singlePointWorld: vmbr9 on pve1 has exactly one uplink (eno3) — a single
// point of failure. Failing it disconnects every guest on vmbr9.
func singlePointWorld() (inventory.Snapshot, inventory.Ref) {
	w := newWorld()
	w.node("pve1")
	w.physnic("pve1", "eno3", true)
	w.bridge("pve1", "vmbr9", "eno3")
	snap := w.build()
	return snap, inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno3"}
}

func healthyCapacity() CapacityInput {
	return CapacityInput{
		LinkRef: "vmbr9", LinkSpeedMbps: 1000,
		History: []capacity.Aggregate{
			{BucketAt: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC), Ref: "vmbr9", Kind: capacity.KindLink, MaxUtil: 5},
		},
	}
}

func healthyIPAM() IPAMInput {
	return IPAMInput{Subnets: []ipam.Subnet{{CIDR: "10.20.0.0/24", Total: 250, Allocated: 5}}}
}

func baseProfile() GuestProfile {
	return GuestProfile{
		Name: "standard-vm", NICCount: 1, ExpectedMbps: 10,
		Attachment: Attachment{Kind: AttachBridge, Node: "pve1", Name: "vmbr9"},
	}
}

// --- tests -----------------------------------------------------------------

func TestEvaluate_FineOnAllAxes(t *testing.T) {
	snap, target := redundantWorld()
	req := Request{
		Profile:  baseProfile(),
		N:        5,
		Capacity: healthyCapacity(),
		IPAM:     healthyIPAM(),
		Failsim:  FailsimInput{Snapshot: snap, Target: target},
	}
	v := Evaluate(req)

	if v.Capacity.Status != AxisOK {
		t.Errorf("capacity status = %v, want AxisOK", v.Capacity.Status)
	}
	if v.IPAM.Status != AxisOK {
		t.Errorf("ipam status = %v, want AxisOK", v.IPAM.Status)
	}
	if v.Failsim.Status != AxisOK {
		t.Errorf("failsim status = %v, want AxisOK", v.Failsim.Status)
	}
	if v.Binding != "" {
		t.Errorf("binding = %q, want none", v.Binding)
	}
	if len(v.Unavailable) != 0 {
		t.Errorf("unavailable = %v, want none", v.Unavailable)
	}
}

func TestEvaluate_BreaksOnCapacityOnly(t *testing.T) {
	snap, target := redundantWorld()
	cap := healthyCapacity()
	cap.History = []capacity.Aggregate{
		{BucketAt: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC), Ref: "vmbr9", Kind: capacity.KindLink, MaxUtil: 95},
	}
	req := Request{
		Profile:  baseProfile(), // 10Mbps/1000Mbps = 1% per guest
		N:        10,
		Capacity: cap,
		IPAM:     healthyIPAM(),
		Failsim:  FailsimInput{Snapshot: snap, Target: target},
	}
	v := Evaluate(req)

	if v.Capacity.Status != AxisBreaks {
		t.Fatalf("capacity status = %v, want AxisBreaks", v.Capacity.Status)
	}
	if v.IPAM.Status != AxisOK {
		t.Errorf("ipam status = %v, want AxisOK", v.IPAM.Status)
	}
	if v.Failsim.Status != AxisOK {
		t.Errorf("failsim status = %v, want AxisOK", v.Failsim.Status)
	}
	if v.Binding != "capacity" {
		t.Errorf("binding = %q, want capacity", v.Binding)
	}
	if v.BindingAtN == nil || *v.BindingAtN != 5 {
		t.Errorf("bindingAtN = %v, want 5 (95%%+5*1%%=100%%)", v.BindingAtN)
	}
	if !v.Capacity.Estimated {
		t.Errorf("capacity axis must be flagged Estimated")
	}
	if v.Capacity.Basis == "" {
		t.Errorf("capacity axis must state a basis for the estimate")
	}
}

func TestEvaluate_BreaksOnIPAMOnly(t *testing.T) {
	snap, target := redundantWorld()
	req := Request{
		Profile:  baseProfile(),
		N:        5,
		Capacity: healthyCapacity(),
		IPAM:     IPAMInput{Subnets: []ipam.Subnet{{CIDR: "10.20.0.0/29", Total: 6, Allocated: 4}}}, // 2 free
		Failsim:  FailsimInput{Snapshot: snap, Target: target},
	}
	v := Evaluate(req)

	if v.Capacity.Status != AxisOK {
		t.Errorf("capacity status = %v, want AxisOK", v.Capacity.Status)
	}
	if v.IPAM.Status != AxisBreaks {
		t.Fatalf("ipam status = %v, want AxisBreaks", v.IPAM.Status)
	}
	if v.Failsim.Status != AxisOK {
		t.Errorf("failsim status = %v, want AxisOK", v.Failsim.Status)
	}
	if v.Binding != "ipam" {
		t.Errorf("binding = %q, want ipam", v.Binding)
	}
	if v.BindingAtN == nil || *v.BindingAtN != 3 {
		t.Errorf("bindingAtN = %v, want 3 (2 free addrs, 1/guest -> breaks on the 3rd)", v.BindingAtN)
	}
	if v.IPAM.Estimated {
		t.Errorf("ipam axis must never be flagged Estimated — it is exact")
	}
	if v.IPAM.Subnet != "10.20.0.0/29" {
		t.Errorf("ipam axis must name which subnet exhausts, got %q", v.IPAM.Subnet)
	}
}

func TestEvaluate_BreaksOnFailsimOnly(t *testing.T) {
	snap, target := singlePointWorld()
	req := Request{
		Profile:  baseProfile(),
		N:        3,
		Capacity: healthyCapacity(),
		IPAM:     healthyIPAM(),
		Failsim:  FailsimInput{Snapshot: snap, Target: target},
	}
	v := Evaluate(req)

	if v.Capacity.Status != AxisOK {
		t.Errorf("capacity status = %v, want AxisOK", v.Capacity.Status)
	}
	if v.IPAM.Status != AxisOK {
		t.Errorf("ipam status = %v, want AxisOK", v.IPAM.Status)
	}
	if v.Failsim.Status != AxisBreaks {
		t.Fatalf("failsim status = %v, want AxisBreaks", v.Failsim.Status)
	}
	if v.Binding != "failsim-impact" {
		t.Errorf("binding = %q, want failsim-impact", v.Binding)
	}
	if v.BindingAtN == nil || *v.BindingAtN != 1 {
		t.Errorf("bindingAtN = %v, want 1 (a single point of failure exposes the first added guest)", v.BindingAtN)
	}
	if v.Failsim.AddedDisconnected != 3 {
		t.Errorf("addedDisconnected = %d, want 3 (all N guests share the vulnerable attachment)", v.Failsim.AddedDisconnected)
	}
}

// TestEvaluate_FailsimDeltaExcludesPreexisting checks the failsim axis
// diffs Before/After rather than reporting After's raw count: a guest
// already exposed by Target's failure today must not be double-counted as
// "newly" at risk once N guests are added alongside it.
func TestEvaluate_FailsimDeltaExcludesPreexisting(t *testing.T) {
	w := newWorld()
	w.node("pve1")
	w.physnic("pve1", "eno3", true)
	w.bridge("pve1", "vmbr9", "eno3")
	w.guest("pve1", "500")
	w.nic("pve1", "500", "net0", "vmbr9")
	snap := w.build()
	target := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno3"}

	req := Request{
		Profile:  baseProfile(),
		N:        2,
		Capacity: healthyCapacity(),
		IPAM:     healthyIPAM(),
		Failsim:  FailsimInput{Snapshot: snap, Target: target},
	}
	v := Evaluate(req)

	if len(v.Failsim.Before.DisconnectedGuests) != 1 {
		t.Fatalf("Before.DisconnectedGuests = %v, want the 1 pre-existing guest already exposed", v.Failsim.Before.DisconnectedGuests)
	}
	if v.Failsim.AddedDisconnected != 2 {
		t.Errorf("addedDisconnected = %d, want 2 (the pre-existing guest must not be double-counted)", v.Failsim.AddedDisconnected)
	}
}

// TestEvaluate_UnavailableDegradesHonestly is the required "signal
// unavailable" case: capacity has no rollup history yet. It must NOT be
// reported as AxisOK (unconstrained) and must NOT be eligible to be named
// binding even though nothing else breaks.
func TestEvaluate_UnavailableDegradesHonestly(t *testing.T) {
	snap, target := redundantWorld()
	req := Request{
		Profile:  baseProfile(),
		N:        5,
		Capacity: CapacityInput{LinkRef: "vmbr9", LinkSpeedMbps: 1000, History: nil}, // no rollup yet
		IPAM:     healthyIPAM(),
		Failsim:  FailsimInput{Snapshot: snap, Target: target},
	}
	v := Evaluate(req)

	if v.Capacity.Status != AxisUnavailable {
		t.Fatalf("capacity status = %v, want AxisUnavailable", v.Capacity.Status)
	}
	if v.Capacity.Status == AxisOK {
		t.Fatalf("an unavailable signal must never be reported as AxisOK (unconstrained)")
	}
	if v.Capacity.Reason == "" {
		t.Errorf("an unavailable axis must state why")
	}
	found := false
	for _, u := range v.Unavailable {
		if u == "capacity" {
			found = true
		}
	}
	if !found {
		t.Errorf("Unavailable = %v, want it to list capacity", v.Unavailable)
	}
	// Nothing else breaks, so Binding is legitimately empty — but that must
	// be distinguishable from "everything was checked" via Unavailable,
	// which the assertions above already cover.
	if v.Binding != "" {
		t.Errorf("binding = %q, want none (only unavailable/ok axes present)", v.Binding)
	}
}

func TestEvaluate_IPAMUnavailableWhenNoSubnetResolved(t *testing.T) {
	snap, target := redundantWorld()
	req := Request{
		Profile:  baseProfile(),
		N:        5,
		Capacity: healthyCapacity(),
		IPAM:     IPAMInput{Subnets: nil},
		Failsim:  FailsimInput{Snapshot: snap, Target: target},
	}
	v := Evaluate(req)
	if v.IPAM.Status != AxisUnavailable {
		t.Fatalf("ipam status = %v, want AxisUnavailable", v.IPAM.Status)
	}
	if v.IPAM.Reason == "" {
		t.Errorf("an unavailable ipam axis must state why")
	}
}

func TestEvaluate_FailsimUnavailableWhenAttachmentUnresolvable(t *testing.T) {
	snap, target := redundantWorld()
	profile := baseProfile()
	profile.Attachment.Name = "vmbr-ghost" // does not exist in the snapshot
	req := Request{
		Profile:  profile,
		N:        3,
		Capacity: healthyCapacity(),
		IPAM:     healthyIPAM(),
		Failsim:  FailsimInput{Snapshot: snap, Target: target},
	}
	v := Evaluate(req)
	if v.Failsim.Status != AxisUnavailable {
		t.Fatalf("failsim status = %v, want AxisUnavailable", v.Failsim.Status)
	}
	if v.Failsim.Reason == "" {
		t.Errorf("an unavailable failsim axis must state why")
	}
}

func TestEvaluate_FailsimUnavailableWhenNoTarget(t *testing.T) {
	snap, _ := redundantWorld()
	req := Request{
		Profile:  baseProfile(),
		N:        3,
		Capacity: healthyCapacity(),
		IPAM:     healthyIPAM(),
		Failsim:  FailsimInput{Snapshot: snap}, // Target left zero
	}
	v := Evaluate(req)
	if v.Failsim.Status != AxisUnavailable {
		t.Fatalf("failsim status = %v, want AxisUnavailable", v.Failsim.Status)
	}
}

// TestEvaluate_LowestNWinsTies checks the binding constraint is the axis
// that breaks at the lowest N when more than one axis breaks.
func TestEvaluate_LowestNWinsTies(t *testing.T) {
	snap, target := redundantWorld()
	cap := healthyCapacity()
	cap.History = []capacity.Aggregate{
		{BucketAt: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC), Ref: "vmbr9", Kind: capacity.KindLink, MaxUtil: 95},
	}
	req := Request{
		Profile:  baseProfile(), // capacity breaks at N=5 (95+5*1=100)
		N:        10,
		Capacity: cap,
		IPAM:     IPAMInput{Subnets: []ipam.Subnet{{CIDR: "10.20.0.0/29", Total: 6, Allocated: 4}}}, // breaks at N=3
		Failsim:  FailsimInput{Snapshot: snap, Target: target},
	}
	v := Evaluate(req)
	if v.Binding != "ipam" {
		t.Fatalf("binding = %q, want ipam (breaks earlier: N=3 vs capacity's N=5)", v.Binding)
	}
	if v.BindingAtN == nil || *v.BindingAtN != 3 {
		t.Errorf("bindingAtN = %v, want 3", v.BindingAtN)
	}
}

// TestEvaluate_CitesAllThreeSignals checks AC1: one request returns a
// combined verdict citing all three signals by name — the Verdict always
// carries all three axis results (never just the binding one), and the
// prose Summary names whichever one binds.
func TestEvaluate_CitesAllThreeSignals(t *testing.T) {
	snap, target := singlePointWorld()
	req := Request{
		Profile:  baseProfile(),
		N:        2,
		Capacity: healthyCapacity(),
		IPAM:     healthyIPAM(),
		Failsim:  FailsimInput{Snapshot: snap, Target: target},
	}
	v := Evaluate(req)
	if v.Capacity.Status == "" || v.IPAM.Status == "" || v.Failsim.Status == "" {
		t.Fatal("verdict must carry a result for all three axes, not just the binding one")
	}
	if v.Summary == "" {
		t.Fatal("summary must not be empty")
	}
	if v.Binding != "failsim-impact" {
		t.Fatalf("binding = %q, want failsim-impact", v.Binding)
	}
}
