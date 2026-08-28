// SPDX-License-Identifier: Apache-2.0

// reconcile.go implements T-2703's finding: the one that names all THREE
// positions.
//
// # Why a third position changes the question
//
// The five original check families compare live state against live state
// across nodes. specdrift.go (T-1102) added a second axis — the declarative
// spec against live. With a spec in git (T-2701) the operator's real question
// stops being "are these two the same" and becomes "which of these three is
// right":
//
//	spec    the declarative document — what the cluster is SUPPOSED to be
//	config  /etc/network/interfaces as PVE reports it — what it will be after
//	        the next reload
//	live    the running kernel (netlink) — what it is right now
//
// Reporting that as two independent two-way findings (spec_drift says the spec
// and the file disagree; file_runtime_divergence says the file and the kernel
// disagree) makes the operator reassemble the third comparison in their head,
// and the third comparison is the one that decides what to do. So this family
// reports all three positions and all three pairs on one finding, per field
// (T-2703 AC4).
//
// # Two symmetric actions, and NEITHER of them fires here
//
// A finding carries what it offers, never what it did:
//
//	restore intent  stage a changeset bringing live back to the spec — the
//	                ops spec.Import already computes, handed to the change
//	                engine as an ordinary draft.
//	adopt reality   propose a spec commit matching the cluster — a pull
//	                request through T-2702's Proposer.
//
// Nothing in this file (or anywhere in this package) calls either. This
// package computes; internal/reconcile executes, and only when an operator
// asks. That is why the actions are expressed as booleans on a value type and
// the ops as a lookup by finding id, not as a callback.
//
// # An action is offered only when it is APPLICABLE (AC5)
//
// AC5 forbids a finding that offers both actions while neither can do
// anything. The strongest available answer is to make the state unreachable:
// each action is offered if and only if executing it would produce a
// non-empty artifact, decided here by the same two functions that would
// execute it (spec.Import for restore, spec.AdoptEntities + spec.SameIntent
// for adopt). A finding with neither offered is perfectly legitimate — a
// divergence that exists only between the file and the kernel is real, and no
// spec commit resolves it — but a finding that ADVERTISES an action it cannot
// perform is a lie the UI would render as a button.

package drift

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/spec"
)

// CheckSpecReconciliation is T-2703's check family: an entity the spec
// declares whose three positions do not agree.
const CheckSpecReconciliation = "spec_reconciliation"

// Position names one of the three positions a reconciliation compares.
type Position string

const (
	// PositionSpec is the declarative document — git (T-2701) or the pin.
	PositionSpec Position = "spec"
	// PositionConfig is /etc/network/interfaces as PVE reports it: the
	// *declared* fields (DeclaredPortNames, DeclaredSlaves, MTUDeclared).
	PositionConfig Position = "config"
	// PositionLive is the running kernel, as netlink reports it.
	PositionLive Position = "live"
)

// PositionValue is one position's rendering of one field.
//
// Known is not decoration. A position that never reported a field is not the
// same as one that reported it empty, and collapsing the two would invent
// divergence on every cluster where only one collector has run.
type PositionValue struct {
	Position Position `json:"position"`
	Value    string   `json:"value"`
	Known    bool     `json:"known"`
}

// FieldPositions is one field's value at all three positions, plus the pairs
// that disagree about it.
type FieldPositions struct {
	Field  string          `json:"field"`
	Values []PositionValue `json:"values"`
	// Differs names the disagreeing pairs as "spec/config", "config/live",
	// "spec/live". Only pairs where both positions reported the field appear.
	Differs []string `json:"differs"`
}

// PairDiff is one of the three pairwise comparisons. All three are always
// emitted, including the ones that agree: AC4's "not collapsed into a two-way
// diff" means a reader can see that config and live agree, which is exactly
// what tells them the divergence is the spec's.
type PairDiff struct {
	A Position `json:"a"`
	B Position `json:"b"`
	// Fields are the fields on which A and B disagree, sorted.
	Fields []string `json:"fields"`
	// Comparable is false when the two positions share no field either of
	// them reported — "they agree" and "there was nothing to compare" are
	// different statements.
	Comparable bool `json:"comparable"`
}

// Actions reports which of T-2703's two reconciliation actions this finding
// offers. An action is offered if and only if it is applicable.
type Actions struct {
	// AdoptReality: propose a spec commit matching the cluster (T-2702).
	AdoptReality bool `json:"adoptReality"`
	// RestoreIntent: stage a changeset bringing live back to the spec.
	RestoreIntent bool `json:"restoreIntent"`
}

// Reconciliation is the three-position report attached to a finding.
//
//nolint:govet // fieldalignment: field order is the wire shape — identity, then presence at each position, then the fields, the pairs and what is on offer. Packing would scramble the document a reader follows.
type Reconciliation struct {
	Ref string `json:"ref"`
	// InSpec/InConfig/InLive report whether each position has the entity at
	// all. An entity the document declares and the cluster does not have is
	// the case ApplyOps cannot express and spec.RemoveEntities exists for.
	InSpec   bool             `json:"inSpec"`
	InConfig bool             `json:"inConfig"`
	InLive   bool             `json:"inLive"`
	Fields   []FieldPositions `json:"fields"`
	Pairs    []PairDiff       `json:"pairs"`
	Actions  Actions          `json:"actions"`
}

// pairOrder is the fixed order the three pairs are reported in.
//
//nolint:gochecknoglobals // a constant table, read-only after init
var pairOrder = [3][2]Position{
	{PositionSpec, PositionConfig},
	{PositionConfig, PositionLive},
	{PositionSpec, PositionLive},
}

func pairLabel(a, b Position) string { return string(a) + "/" + string(b) }

// triple is one field's value at each of the three positions, during
// computation. A position that did not report the field has its Known flag
// false and its value is never compared.
type triple struct {
	field  string
	spec   string
	config string
	live   string

	specKnown   bool
	configKnown bool
	liveKnown   bool
}

// reconcileFindings computes the CheckSpecReconciliation family: one finding
// per spec-declared entity whose three positions disagree.
//
// Only bonds, bridges and VLAN sub-interfaces are considered. SDN zones, vnets
// and subnets have no distinct config and live positions — PVE reports one
// value for each — so a three-position report about them would name a
// distinction that does not exist; spec_drift already covers them.
func (s *Service) reconcileFindings(snap inventory.Snapshot) []Finding {
	doc, ok := s.parsedSpec()
	if !ok {
		return nil
	}
	plan, _, err := spec.Import(doc, snap)
	if err != nil {
		s.log.Error("drift: planning the spec against live state for reconciliation", "error", err)
		return nil
	}
	planByRef := map[inventory.Ref][]change.Op{}
	for _, op := range plan {
		planByRef[op.Target] = append(planByRef[op.Target], op)
	}

	var out []Finding
	for _, ref := range declaredRefs(doc) {
		f, ok := s.reconcileFinding(doc, snap, ref, planByRef[ref])
		if ok {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// reconcileFinding builds the finding for one spec-declared ref, or reports
// that the three positions agree.
func (s *Service) reconcileFinding(doc spec.Spec, snap inventory.Snapshot, ref inventory.Ref, restoreOps []change.Op) (Finding, bool) {
	rec := Reconciliation{Ref: ref.String(), InSpec: true}

	var triples []triple
	severity := ""
	e, found := snap.Get(ref)
	if !found {
		// The document declares an entity the cluster does not have. There is
		// nothing to compare field by field; the divergence IS the absence.
		severity = SeverityError
	} else {
		triples = entityTriples(doc, ref, e)
		rec.InConfig = anyKnown(triples, PositionConfig)
		rec.InLive = anyKnown(triples, PositionLive)
		var diverged []triple
		for _, t := range triples {
			sev, ok := t.severity()
			if !ok {
				continue
			}
			diverged = append(diverged, t)
			severity = worseSeverity(severity, sev)
		}
		if len(diverged) == 0 {
			return Finding{}, false
		}
	}

	rec.Fields = renderFields(triples)
	rec.Pairs = renderPairs(triples, found)

	adoptable, err := s.adoptable(doc, snap, ref)
	if err != nil {
		s.log.Error("drift: deciding whether a divergence can be adopted", "ref", ref.String(), "error", err)
		adoptable = false
	}
	rec.Actions = Actions{AdoptReality: adoptable, RestoreIntent: len(restoreOps) > 0}

	f := newFinding(CheckSpecReconciliation, severity, reconcileDetail(ref, rec, found), []string{ref.Node}, []string{ref.String()})
	f.Reconcile = &rec
	f.adoptRefs = []inventory.Ref{ref}
	if len(restoreOps) > 0 {
		f = f.withFix("drift: restore "+ref.String()+" to the spec", restoreOps)
	}
	return f, true
}

// adoptable reports whether adopting this ref would actually change the
// document. It is the same computation the propose path performs, run here so
// the finding never advertises an action that would answer "nothing to
// propose" (AC5).
func (s *Service) adoptable(doc spec.Spec, snap inventory.Snapshot, ref inventory.Ref) (bool, error) {
	adopted, err := spec.AdoptEntities(doc, []inventory.Ref{ref}, snap)
	if err != nil {
		return false, fmt.Errorf("drift: adopting %s into the spec: %w", ref, err)
	}
	same, err := spec.SameIntent(doc, adopted)
	if err != nil {
		return false, fmt.Errorf("drift: comparing the adopted spec for %s: %w", ref, err)
	}
	return !same, nil
}

// --- the three positions ---------------------------------------------------

// entityTriples reads one entity's comparable fields at all three positions.
// A field is only compared when the SPEC declares it: an omitted field means
// "not managed by this document" (import.go's omitempty=unmanaged rule), and
// reporting a divergence about something the operator did not ask to manage
// would be noise the two actions could not resolve.
func entityTriples(doc spec.Spec, ref inventory.Ref, e inventory.Entity) []triple {
	switch v := e.(type) {
	case *inventory.Bridge:
		declared := findBridgeSpec(doc, ref)
		if declared == nil {
			return nil
		}
		// v.PortNames is dropped of PVE's own runtime-owned members
		// (fwbr*/fwln*/fwpr*, guest tap*/veth*) before comparison, for the
		// same reason filerun.go's membershipFinding does — see
		// runtimeOwnedMemberPattern and T-3502's evidence file. Without
		// this, a spec-managed bridge with a firewall-enabled guest NIC
		// would report a spurious spec/live-only "ports" divergence here
		// exactly as file_runtime_divergence used to.
		return []triple{
			setTriple("ports", declared.Ports, v.DeclaredPortNames, dropRuntimeOwned(v.PortNames)),
			intTriple("mtu", declared.MTU, v.MTUDeclared, v.MTU),
		}
	case *inventory.Bond:
		declared := findBondSpec(doc, ref)
		if declared == nil {
			return nil
		}
		return []triple{
			setTriple("slaves", declared.Slaves, v.DeclaredSlaves, v.Slaves),
			intTriple("mtu", declared.MTU, v.MTUDeclared, v.MTU),
		}
	case *inventory.VlanIface:
		declared := findVLANSpec(doc, ref)
		if declared == nil {
			return nil
		}
		return []triple{
			intTriple("mtu", declared.MTU, v.MTUDeclared, v.MTU),
		}
	default:
		return nil
	}
}

func setTriple(field string, specVal, configVal, liveVal []string) triple {
	return triple{
		field:       field,
		spec:        strings.Join(sortedUnique(specVal), ","),
		config:      strings.Join(sortedUnique(configVal), ","),
		live:        strings.Join(sortedUnique(liveVal), ","),
		specKnown:   len(specVal) > 0,
		configKnown: len(configVal) > 0,
		liveKnown:   len(liveVal) > 0,
	}
}

func intTriple(field string, specVal, configVal, liveVal int) triple {
	return triple{
		field:       field,
		spec:        strconv.Itoa(specVal),
		config:      strconv.Itoa(configVal),
		live:        strconv.Itoa(liveVal),
		specKnown:   specVal != 0,
		configKnown: configVal != 0,
		liveKnown:   liveVal != 0,
	}
}

// severity classifies one field's three-position pattern, and reports whether
// it diverges at all.
//
// The vocabulary is docs/api.md's error|warning|info, and the mapping is by
// which position is the odd one out:
//
//   - error   all three positions differ. Nobody agrees; the operator has to
//     choose, and that is the whole reason both actions exist.
//   - warning the spec is the odd one out (the cluster does not match intent
//     anywhere), or the file is (the runtime still matches intent, but the
//     next reload will break it — latent, not harmless).
//   - info    the runtime is the odd one out. The document and the file agree,
//     so intent is intact and a reload restores it; no spec commit is
//     involved, which is also why neither action is applicable here.
func (t triple) severity() (string, bool) {
	if !t.specKnown {
		// The document makes no claim about this field.
		return "", false
	}
	sc := t.configKnown && t.spec != t.config
	sl := t.liveKnown && t.spec != t.live
	cl := t.configKnown && t.liveKnown && t.config != t.live
	switch {
	case sc && sl && cl:
		return SeverityError, true
	case sc && sl:
		return SeverityWarning, true
	case sc:
		return SeverityWarning, true
	case sl && t.configKnown:
		return SeverityInfo, true
	case sl:
		return SeverityWarning, true
	default:
		return "", false
	}
}

// differs returns the labels of the pairs that disagree about this field.
func (t triple) differs() []string {
	var out []string
	if t.specKnown && t.configKnown && t.spec != t.config {
		out = append(out, pairLabel(PositionSpec, PositionConfig))
	}
	if t.configKnown && t.liveKnown && t.config != t.live {
		out = append(out, pairLabel(PositionConfig, PositionLive))
	}
	if t.specKnown && t.liveKnown && t.spec != t.live {
		out = append(out, pairLabel(PositionSpec, PositionLive))
	}
	return out
}

func (t triple) at(p Position) (string, bool) {
	switch p {
	case PositionSpec:
		return t.spec, t.specKnown
	case PositionConfig:
		return t.config, t.configKnown
	case PositionLive:
		return t.live, t.liveKnown
	default:
		return "", false
	}
}

func anyKnown(triples []triple, p Position) bool {
	for _, t := range triples {
		if _, known := t.at(p); known {
			return true
		}
	}
	return false
}

// renderFields emits every field the spec declares — including the ones all
// three positions agree on, so the finding is a full statement of the three
// positions rather than only its disagreements.
func renderFields(triples []triple) []FieldPositions {
	out := make([]FieldPositions, 0, len(triples))
	for _, t := range triples {
		if !t.specKnown {
			continue
		}
		fp := FieldPositions{Field: t.field, Differs: t.differs()}
		for _, p := range []Position{PositionSpec, PositionConfig, PositionLive} {
			val, known := t.at(p)
			if !known {
				val = ""
			}
			fp.Values = append(fp.Values, PositionValue{Position: p, Value: val, Known: known})
		}
		out = append(out, fp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}

// renderPairs emits all three pairwise comparisons, always — AC4's "all three
// positions named, not collapsed into a two-way diff".
func renderPairs(triples []triple, presentLive bool) []PairDiff {
	out := make([]PairDiff, 0, len(pairOrder))
	for _, pair := range pairOrder {
		a, b := pair[0], pair[1]
		pd := PairDiff{A: a, B: b}
		for _, t := range triples {
			av, aKnown := t.at(a)
			bv, bKnown := t.at(b)
			if !aKnown || !bKnown {
				continue
			}
			pd.Comparable = true
			if av != bv {
				pd.Fields = append(pd.Fields, t.field)
			}
		}
		sort.Strings(pd.Fields)
		if !presentLive && (a != PositionSpec || b != PositionConfig) {
			// The entity is absent from the cluster entirely: config and live
			// have nothing to say, and saying "they agree" would be false.
			pd.Comparable = false
		}
		out = append(out, pd)
	}
	return out
}

// reconcileDetail renders the finding's human sentence. It names all three
// positions and their values explicitly (AC4) and says what each action would
// do — or, when an action is not offered, why there is nothing for it to do.
func reconcileDetail(ref inventory.Ref, rec Reconciliation, presentLive bool) string {
	var sb strings.Builder
	if !presentLive {
		fmt.Fprintf(&sb, "%s is declared by the spec but the cluster does not have it: spec=declared, config=absent, live=absent.",
			ref.String())
	} else {
		fmt.Fprintf(&sb, "%s: the spec, the interfaces file and the running kernel do not agree.", ref.String())
		for _, f := range rec.Fields {
			if len(f.Differs) == 0 {
				continue
			}
			fmt.Fprintf(&sb, " %s spec=%s config=%s live=%s (%s differ).",
				f.Field, valueOrUnreported(f, PositionSpec), valueOrUnreported(f, PositionConfig),
				valueOrUnreported(f, PositionLive), strings.Join(f.Differs, ", "))
		}
	}
	switch {
	case rec.Actions.AdoptReality && rec.Actions.RestoreIntent:
		sb.WriteString(" Adopt reality proposes a spec commit matching the cluster; restore intent stages a changeset bringing the cluster back to the spec. Neither happens until you ask for it.")
	case rec.Actions.RestoreIntent:
		sb.WriteString(" Restore intent stages a changeset bringing the cluster back to the spec; adopting reality would not change the document.")
	case rec.Actions.AdoptReality:
		sb.WriteString(" Adopt reality proposes a spec commit matching the cluster; there is nothing to stage against the cluster.")
	default:
		sb.WriteString(" Neither reconciliation action applies: the spec and the interfaces file already agree, so this divergence is between the file and the running kernel — reload or re-apply the interface, do not edit the spec.")
	}
	return sb.String()
}

func valueOrUnreported(f FieldPositions, p Position) string {
	for _, v := range f.Values {
		if v.Position != p {
			continue
		}
		if !v.Known {
			return "(unreported)"
		}
		return v.Value
	}
	return "(unreported)"
}

// --- spec lookups ----------------------------------------------------------

// declaredRefs is every bond/bridge/VLAN ref the document declares, in a
// deterministic order.
func declaredRefs(doc spec.Spec) []inventory.Ref {
	var out []inventory.Ref
	for _, n := range doc.Nodes {
		for _, b := range n.Bonds {
			out = append(out, inventory.Ref{Kind: inventory.KindBond, Node: n.Name, ID: b.Name})
		}
		for _, b := range n.Bridges {
			out = append(out, inventory.Ref{Kind: inventory.KindBridge, Node: n.Name, ID: b.Name})
		}
		for _, v := range n.VLANs {
			out = append(out, inventory.Ref{Kind: inventory.KindVlan, Node: n.Name, ID: v.Name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func findBridgeSpec(doc spec.Spec, ref inventory.Ref) *spec.BridgeSpec {
	for i := range doc.Nodes {
		if doc.Nodes[i].Name != ref.Node {
			continue
		}
		for j := range doc.Nodes[i].Bridges {
			if doc.Nodes[i].Bridges[j].Name == ref.ID {
				return &doc.Nodes[i].Bridges[j]
			}
		}
	}
	return nil
}

func findBondSpec(doc spec.Spec, ref inventory.Ref) *spec.BondSpec {
	for i := range doc.Nodes {
		if doc.Nodes[i].Name != ref.Node {
			continue
		}
		for j := range doc.Nodes[i].Bonds {
			if doc.Nodes[i].Bonds[j].Name == ref.ID {
				return &doc.Nodes[i].Bonds[j]
			}
		}
	}
	return nil
}

func findVLANSpec(doc spec.Spec, ref inventory.Ref) *spec.VLANSpec {
	for i := range doc.Nodes {
		if doc.Nodes[i].Name != ref.Node {
			continue
		}
		for j := range doc.Nodes[i].VLANs {
			if doc.Nodes[i].VLANs[j].Name == ref.ID {
				return &doc.Nodes[i].VLANs[j]
			}
		}
	}
	return nil
}

// worseSeverity returns the more severe of two severities in docs/api.md's
// error > warning > info order. "" (nothing seen yet) loses to everything.
func worseSeverity(a, b string) string {
	if severityRank(b) > severityRank(a) {
		return b
	}
	return a
}

func severityRank(s string) int {
	switch s {
	case SeverityError:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}
