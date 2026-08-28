// SPDX-License-Identifier: Apache-2.0

package drift_test

// T-2703 at the detection layer: the three-position finding (spec / config /
// live), the two actions it offers, and the invariant AC5 states.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/spec"
)

// --- fixture construction ---------------------------------------------------

// reconcileGraph builds a one-node graph. A bridge is contributed as a
// pve-network entity (the CONFIG position: MTUDeclared/DeclaredPortNames) and,
// when live values are given, additionally as a host-netlink entity (the LIVE
// position: MTU/PortNames) — exactly the two sources internal/inventory
// resolves those field pairs from.
type bridgeState struct {
	name        string
	configPorts []string
	livePorts   []string
	configMTU   int
	liveMTU     int
	present     bool
}

func reconcileGraph(t *testing.T, states ...bridgeState) *inventory.Graph {
	t.Helper()
	g := newGraphWithNodes("pve1")
	// One poll per source, carrying every bridge: an ApplyPoll REPLACES the
	// whole (source, scope) entity set, so polling them one at a time would
	// leave only the last state in the graph.
	var declared, running []inventory.Entity
	for _, st := range states {
		if !st.present {
			continue
		}
		ref := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: st.name}
		declared = append(declared, &inventory.Bridge{
			Ref: ref, Name: st.name, Virt: inventory.BridgeLinux,
			MTUDeclared: st.configMTU, DeclaredPortNames: st.configPorts,
		})
		if st.liveMTU == 0 && len(st.livePorts) == 0 {
			continue
		}
		running = append(running, &inventory.Bridge{
			Ref: ref, Name: st.name, Virt: inventory.BridgeLinux,
			MTU: st.liveMTU, PortNames: st.livePorts,
		})
	}
	scope := inventory.Scope{Node: "pve1", Kinds: []inventory.Kind{inventory.KindBridge}}
	if len(declared) > 0 {
		g.ApplyPoll(inventory.SourcePVENetwork, scope, declared)
	}
	if len(running) > 0 {
		g.ApplyPoll(inventory.SourceHostNetlink, scope, running)
	}
	return g
}

// pinFor marshals a document and hands it back as a drift.PinProvider.
func pinFor(t *testing.T, doc spec.Spec) drift.PinProvider {
	t.Helper()
	content, err := spec.Marshal(doc)
	if err != nil {
		t.Fatalf("spec.Marshal: %v", err)
	}
	return staticPin(string(content))
}

func bridgeDoc(bridges ...spec.BridgeSpec) spec.Spec {
	return spec.Spec{SpecVersion: 1, Nodes: []spec.NodeSpec{{Name: "pve1", Bridges: bridges}}}
}

func reconcileFindings(t *testing.T, svc *drift.Service) []drift.Finding {
	t.Helper()
	return findByCheck(t, svc.Findings(), drift.CheckSpecReconciliation)
}

func onlyReconcileFinding(t *testing.T, svc *drift.Service) drift.Finding {
	t.Helper()
	found := reconcileFindings(t, svc)
	if len(found) != 1 {
		t.Fatalf("spec_reconciliation findings = %d, want exactly 1: %+v", len(found), found)
	}
	return found[0]
}

// --- AC4: all three positions named -----------------------------------------

// TestReconcile_ThreeWayDivergence_NamesAllThreePositions is AC4: spec, config
// and live all differ, and the finding reports all three — with all three
// pairwise comparisons — rather than collapsing to a two-way diff.
func TestReconcile_ThreeWayDivergence_NamesAllThreePositions(t *testing.T) {
	g := reconcileGraph(t, bridgeState{name: "vmbr0", present: true, configMTU: 1500, liveMTU: 1400})
	svc := drift.New(drift.Config{Graph: g, Pins: pinFor(t, bridgeDoc(spec.BridgeSpec{Name: "vmbr0", MTU: 9000}))})

	f := onlyReconcileFinding(t, svc)
	if f.Severity != drift.SeverityError {
		t.Errorf("severity = %q, want error (all three positions differ)", f.Severity)
	}
	if f.Reconcile == nil {
		t.Fatalf("finding carries no reconciliation report")
	}

	// Every position is named with its own value.
	var mtu *drift.FieldPositions
	for i := range f.Reconcile.Fields {
		if f.Reconcile.Fields[i].Field == "mtu" {
			mtu = &f.Reconcile.Fields[i]
		}
	}
	if mtu == nil {
		t.Fatalf("reconciliation reports no mtu field: %+v", f.Reconcile.Fields)
	}
	want := map[drift.Position]string{
		drift.PositionSpec: "9000", drift.PositionConfig: "1500", drift.PositionLive: "1400",
	}
	for _, v := range mtu.Values {
		if !v.Known {
			t.Errorf("position %s reported unknown, want %s", v.Position, want[v.Position])
			continue
		}
		if v.Value != want[v.Position] {
			t.Errorf("position %s = %q, want %q", v.Position, v.Value, want[v.Position])
		}
	}
	if len(mtu.Values) != 3 {
		t.Errorf("mtu is reported at %d position(s), want 3", len(mtu.Values))
	}

	// All three pairs are reported, and all three disagree.
	if len(f.Reconcile.Pairs) != 3 {
		t.Fatalf("pairs = %d, want 3 (spec/config, config/live, spec/live): %+v", len(f.Reconcile.Pairs), f.Reconcile.Pairs)
	}
	for _, p := range f.Reconcile.Pairs {
		if !p.Comparable {
			t.Errorf("pair %s/%s reported as not comparable, but both positions have an mtu", p.A, p.B)
		}
		if len(p.Fields) != 1 || p.Fields[0] != "mtu" {
			t.Errorf("pair %s/%s differing fields = %v, want [mtu]", p.A, p.B, p.Fields)
		}
	}

	// The sentence a human reads names all three too — the finding must not
	// need the structured payload to be understood.
	for _, want := range []string{"spec=9000", "config=1500", "live=1400"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail %q does not name %q", f.Detail, want)
		}
	}
}

// TestReconcile_Severities walks the three-position patterns and asserts the
// severity vocabulary is error|warning|info, with each one reachable — AC3
// needs a finding at every severity to assert against.
func TestReconcile_Severities(t *testing.T) {
	cases := []struct {
		name      string
		want      string
		specMTU   int
		configMTU int
		liveMTU   int
		present   bool
	}{
		{name: "all three differ", specMTU: 9000, configMTU: 1500, liveMTU: 1400, present: true, want: drift.SeverityError},
		{name: "declared but absent from the cluster", specMTU: 9000, present: false, want: drift.SeverityError},
		{name: "spec is the odd one out", specMTU: 9000, configMTU: 1500, liveMTU: 1500, present: true, want: drift.SeverityWarning},
		{name: "the file is the odd one out", specMTU: 9000, configMTU: 1500, liveMTU: 9000, present: true, want: drift.SeverityWarning},
		{name: "no live position at all", specMTU: 9000, configMTU: 1500, liveMTU: 0, present: true, want: drift.SeverityWarning},
		{name: "the runtime is the odd one out", specMTU: 1500, configMTU: 1500, liveMTU: 1400, present: true, want: drift.SeverityInfo},
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := reconcileGraph(t, bridgeState{name: "vmbr0", present: tc.present, configMTU: tc.configMTU, liveMTU: tc.liveMTU})
			svc := drift.New(drift.Config{Graph: g, Pins: pinFor(t, bridgeDoc(spec.BridgeSpec{Name: "vmbr0", MTU: tc.specMTU}))})
			f := onlyReconcileFinding(t, svc)
			if f.Severity != tc.want {
				t.Errorf("severity = %q, want %q (detail: %s)", f.Severity, tc.want, f.Detail)
			}
			seen[f.Severity] = true
		})
	}
	for _, sev := range []string{drift.SeverityError, drift.SeverityWarning, drift.SeverityInfo} {
		if !seen[sev] {
			t.Errorf("no case in this table produced a %s finding; AC3 needs one at every severity", sev)
		}
	}
}

// TestReconcile_AgreementProducesNoFinding: when all three positions agree
// there is nothing to reconcile and nothing is reported.
func TestReconcile_AgreementProducesNoFinding(t *testing.T) {
	g := reconcileGraph(t, bridgeState{
		name: "vmbr0", present: true, configMTU: 1500, liveMTU: 1500,
		configPorts: []string{"eno1"}, livePorts: []string{"eno1"},
	})
	svc := drift.New(drift.Config{Graph: g, Pins: pinFor(t, bridgeDoc(
		spec.BridgeSpec{Name: "vmbr0", MTU: 1500, Ports: []string{"eno1"}}))})
	if found := reconcileFindings(t, svc); len(found) != 0 {
		t.Fatalf("spec_reconciliation findings = %+v, want none (all three positions agree)", found)
	}
}

// TestReconcile_NoSpec_NoFindings: without a document there is no third
// position, so this family contributes nothing — the pre-T-2703 behaviour for
// every caller that has not wired a spec.
func TestReconcile_NoSpec_NoFindings(t *testing.T) {
	g := reconcileGraph(t, bridgeState{name: "vmbr0", present: true, configMTU: 1500, liveMTU: 1400})
	if found := reconcileFindings(t, drift.New(drift.Config{Graph: g})); len(found) != 0 {
		t.Fatalf("spec_reconciliation findings without a spec = %+v, want none", found)
	}
	if found := reconcileFindings(t, drift.New(drift.Config{Graph: g, Pins: fakePinProvider{}})); len(found) != 0 {
		t.Fatalf("spec_reconciliation findings with nothing pinned = %+v, want none", found)
	}
}

// TestReconcile_SetValuedField covers a port list, where the three positions
// are sets rather than scalars.
func TestReconcile_SetValuedField(t *testing.T) {
	g := reconcileGraph(t, bridgeState{
		name: "vmbr0", present: true,
		configPorts: []string{"eno1"}, livePorts: []string{"eno1", "eno2"},
		configMTU: 1500, liveMTU: 1500,
	})
	svc := drift.New(drift.Config{Graph: g, Pins: pinFor(t, bridgeDoc(
		spec.BridgeSpec{Name: "vmbr0", MTU: 1500, Ports: []string{"eno1", "eno3"}}))})

	f := onlyReconcileFinding(t, svc)
	if f.Severity != drift.SeverityError {
		t.Errorf("severity = %q, want error (spec, config and live all name different port sets)", f.Severity)
	}
	if !strings.Contains(f.Detail, "spec=eno1,eno3") || !strings.Contains(f.Detail, "config=eno1") || !strings.Contains(f.Detail, "live=eno1,eno2") {
		t.Errorf("detail does not name all three port sets: %s", f.Detail)
	}
}

// TestReconcile_PortOrderIsNotDivergence: a port list written in a different
// order is the same intent. Reporting it as drift would put a permanent
// finding on a correctly configured cluster.
func TestReconcile_PortOrderIsNotDivergence(t *testing.T) {
	g := reconcileGraph(t, bridgeState{
		name: "vmbr0", present: true, configMTU: 1500, liveMTU: 1500,
		configPorts: []string{"eno2", "eno1"}, livePorts: []string{"eno1", "eno2"},
	})
	svc := drift.New(drift.Config{Graph: g, Pins: pinFor(t, bridgeDoc(
		spec.BridgeSpec{Name: "vmbr0", MTU: 1500, Ports: []string{"eno1", "eno2"}}))})
	if found := reconcileFindings(t, svc); len(found) != 0 {
		t.Fatalf("findings = %+v, want none (the three port lists are the same set)", found)
	}
}

// --- AC5: the invariant, over an exhaustive corpus --------------------------

// TestReconcile_ActionInvariant_OverTheWholeCorpus is AC5, asserted as a
// property over every three-position state this model can be in rather than on
// a hand-picked example.
//
// The corpus is the full cross product of (what the spec declares) x (whether
// the cluster has the entity) x (what the interfaces file says) x (what netlink
// says), 30 states in all. For every finding in every state it asserts:
//
//   - an offered action is EXECUTABLE — restore intent returns a non-empty op
//     patch, and adopt reality returns refs whose adoption both changes the
//     document and converges it (AC1's property, per finding);
//   - an action that is NOT offered is not executable either, so the offer is
//     an honest description rather than a UI hint;
//   - and, literally, no finding offers both actions while neither applies.
//
// The offers are computed by internal/drift; the applicability is decided here
// by re-running internal/spec's own Import/AdoptEntities against the same
// snapshot. The two are not the same code path, which is what keeps this from
// being a tautology.
func TestReconcile_ActionInvariant_OverTheWholeCorpus(t *testing.T) {
	mtus := []int{0, 1500, 9000}
	states := 0
	sawOffered := map[string]int{}

	for _, specMTU := range mtus {
		for _, present := range []bool{false, true} {
			configMTUs, liveMTUs := []int{0}, []int{0}
			if present {
				configMTUs, liveMTUs = mtus, mtus
			}
			for _, configMTU := range configMTUs {
				for _, liveMTU := range liveMTUs {
					states++
					name := fmt.Sprintf("spec=%d/present=%v/config=%d/live=%d", specMTU, present, configMTU, liveMTU)
					t.Run(name, func(t *testing.T) {
						doc := bridgeDoc()
						if specMTU != 0 {
							doc = bridgeDoc(spec.BridgeSpec{Name: "vmbr0", MTU: specMTU})
						}
						g := reconcileGraph(t, bridgeState{name: "vmbr0", present: present, configMTU: configMTU, liveMTU: liveMTU})
						svc := drift.New(drift.Config{Graph: g, Pins: pinFor(t, doc)})
						for _, f := range svc.Findings() {
							assertActionInvariant(t, svc, doc, g.Snapshot(), f, sawOffered)
						}
					})
				}
			}
		}
	}

	if states != 30 {
		t.Errorf("corpus covered %d states, want the full 30-state cross product", states)
	}
	// A corpus in which no finding ever offered anything would satisfy every
	// implication above vacuously.
	for _, action := range []string{"adoptReality", "restoreIntent"} {
		if sawOffered[action] == 0 {
			t.Errorf("no state in the corpus offered %s; the invariant held vacuously", action)
		}
	}
}

// assertActionInvariant is the body of AC5's property, applied to one finding.
func assertActionInvariant(t *testing.T, svc *drift.Service, doc spec.Spec, snap inventory.Snapshot, f drift.Finding, sawOffered map[string]int) {
	t.Helper()

	offersAdopt, offersRestore := false, false
	if f.Reconcile != nil {
		offersAdopt = f.Reconcile.Actions.AdoptReality
		offersRestore = f.Reconcile.Actions.RestoreIntent
	}

	// Restore intent: offered iff the lookup yields ops.
	ops, _, restoreOK := svc.RestoreIntentOps(f.ID)
	restoreApplicable := restoreOK && len(ops) > 0
	if offersRestore != restoreApplicable {
		t.Errorf("finding %s offers restoreIntent=%v but it is applicable=%v", f.ID, offersRestore, restoreApplicable)
	}

	// Adopt reality: offered iff adopting the finding's refs would change the
	// document AND converge it (there is no op left for the ref afterwards).
	refs, _, adoptOK := svc.AdoptRealityRefs(f.ID)
	adoptApplicable := false
	if adoptOK && len(refs) > 0 {
		adopted, err := spec.AdoptEntities(doc, refs, snap)
		if err != nil {
			t.Fatalf("finding %s offers adoption but AdoptEntities refuses it: %v", f.ID, err)
		}
		same, err := spec.SameIntent(doc, adopted)
		if err != nil {
			t.Fatalf("SameIntent: %v", err)
		}
		adoptApplicable = !same
		if adoptApplicable {
			after, _, err := spec.Import(adopted, snap)
			if err != nil {
				t.Fatalf("Import(adopted): %v", err)
			}
			for _, op := range after {
				for _, ref := range refs {
					if op.Target == ref {
						t.Errorf("finding %s: after adopting %s the plan still has %s for it", f.ID, ref, op.Type)
					}
				}
			}
		}
	}
	if offersAdopt != adoptApplicable {
		t.Errorf("finding %s offers adoptReality=%v but it is applicable=%v", f.ID, offersAdopt, adoptApplicable)
	}

	// AC5, literally.
	if offersAdopt && offersRestore && !adoptApplicable && !restoreApplicable {
		t.Errorf("finding %s offers both actions and neither is applicable", f.ID)
	}
	if offersAdopt {
		sawOffered["adoptReality"]++
	}
	if offersRestore {
		sawOffered["restoreIntent"]++
	}
}

// TestReconcile_RuntimeOnlyDrift_OffersNeitherAction is the case worth naming
// on its own: the document and the interfaces file agree, and only the running
// kernel differs. No spec commit resolves that, so neither action is offered —
// and the finding says so instead of rendering two buttons that do nothing.
func TestReconcile_RuntimeOnlyDrift_OffersNeitherAction(t *testing.T) {
	g := reconcileGraph(t, bridgeState{name: "vmbr0", present: true, configMTU: 1500, liveMTU: 1400})
	svc := drift.New(drift.Config{Graph: g, Pins: pinFor(t, bridgeDoc(spec.BridgeSpec{Name: "vmbr0", MTU: 1500}))})

	f := onlyReconcileFinding(t, svc)
	if f.Reconcile.Actions.AdoptReality || f.Reconcile.Actions.RestoreIntent {
		t.Fatalf("actions = %+v, want neither offered", f.Reconcile.Actions)
	}
	if _, _, ok := svc.RestoreIntentOps(f.ID); ok {
		t.Errorf("RestoreIntentOps answered for a finding that does not offer it")
	}
	if _, _, ok := svc.AdoptRealityRefs(f.ID); ok {
		t.Errorf("AdoptRealityRefs answered for a finding that does not offer it")
	}
	if !strings.Contains(f.Detail, "Neither reconciliation action applies") {
		t.Errorf("detail does not explain why no action is offered: %s", f.Detail)
	}
}

// TestReconcile_DeclaredButAbsent_OffersBoth is the case internal/spec's
// RemoveEntities exists for: the document declares an entity the cluster does
// not have. Adopting means deleting the declaration — the direction ApplyOps
// cannot express — and restoring means creating the entity.
func TestReconcile_DeclaredButAbsent_OffersBoth(t *testing.T) {
	g := reconcileGraph(t, bridgeState{name: "vmbr0", present: true, configMTU: 1500, liveMTU: 1500})
	doc := bridgeDoc(
		spec.BridgeSpec{Name: "vmbr0", MTU: 1500},
		spec.BridgeSpec{Name: "vmbr9", MTU: 1500},
	)
	svc := drift.New(drift.Config{Graph: g, Pins: pinFor(t, doc)})

	f := onlyReconcileFinding(t, svc)
	if f.Refs[0] != "bridge:pve1:vmbr9" {
		t.Fatalf("finding is about %v, want bridge:pve1:vmbr9", f.Refs)
	}
	if !f.Reconcile.InSpec || f.Reconcile.InConfig || f.Reconcile.InLive {
		t.Errorf("presence = spec:%v config:%v live:%v, want spec-only",
			f.Reconcile.InSpec, f.Reconcile.InConfig, f.Reconcile.InLive)
	}
	if !f.Reconcile.Actions.AdoptReality || !f.Reconcile.Actions.RestoreIntent {
		t.Fatalf("actions = %+v, want both offered", f.Reconcile.Actions)
	}

	refs, _, ok := svc.AdoptRealityRefs(f.ID)
	if !ok || len(refs) != 1 {
		t.Fatalf("AdoptRealityRefs = %v, %v", refs, ok)
	}
	adopted, err := spec.AdoptEntities(doc, refs, g.Snapshot())
	if err != nil {
		t.Fatalf("AdoptEntities: %v", err)
	}
	ops, _, err := spec.Import(adopted, g.Snapshot())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(ops) != 0 {
		t.Errorf("plan after adopting the absence = %+v, want empty", ops)
	}
}
