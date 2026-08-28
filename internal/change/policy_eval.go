// SPDX-License-Identifier: Apache-2.0

// policy_eval.go evaluates a PolicySet against a changeset's ops. It is
// pure: ops in, inventory snapshot in, findings out — no I/O, no store, no
// clock — so it is callable from table-driven tests and from every stage of
// the change engine that already has those two values.
//
// The evaluator's whole vocabulary is here: factFields (the closed set of
// derived inventory facts a rule may name) and the condition operators in
// policy.go. There is no `eval(expr)` anywhere in this file, and adding one
// would be a decision to record, not an implementation detail — see
// policy.go's design-constraint comment.

package change

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Field names a rule may use. `params.<jsonField>` is handled separately
// (it is open-ended by construction: the params shapes are the op
// vocabulary's own, docs/data-model.md §3).
const (
	policyFieldOpType           = "op"
	policyFieldTargetKind       = "target.kind"
	policyFieldTargetNode       = "target.node"
	policyFieldTargetID         = "target.id"
	policyFieldTargetRef        = "target.ref"
	policyFieldTargetExists     = "target.exists"
	policyFieldTargetProtected  = "target.protected"
	policyFieldTargetGuestCount = "target.guestCount"
	policyFieldTargetUplinks    = "target.uplinkCount"
	policyFieldTargetVlanAware  = "target.vlanAware"
	policyFieldChangesetOpCount = "changeset.opCount"

	policyParamsPrefix = "params."
)

// factFields is the closed, documented set of non-params fields a rule may
// name. Every one of them is either a field of the op itself or a fact
// derived from the changeset's NET EFFECT on the inventory snapshot (the
// base snapshot with every op in the changeset folded in — the same
// projection safetyValidate's own guards use, validate_projection.go).
//
// "Net effect" is the deliberate choice for the derived facts: an
// organisational rule is about the state the cluster is left in ("a bridge
// carrying guests has two uplinks"), not about the state it passed through
// mid-changeset. Documented in docs/features/change-management.md §2.
var factFields = map[string]bool{
	policyFieldOpType:           true,
	policyFieldTargetKind:       true,
	policyFieldTargetNode:       true,
	policyFieldTargetID:         true,
	policyFieldTargetRef:        true,
	policyFieldTargetExists:     true,
	policyFieldTargetProtected:  true,
	policyFieldTargetGuestCount: true,
	policyFieldTargetUplinks:    true,
	policyFieldTargetVlanAware:  true,
	policyFieldChangesetOpCount: true,
}

func knownFactFields() []string {
	out := make([]string, 0, len(factFields))
	for f := range factFields {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// knownTargetKind reports whether s names a kind in inventory's closed Kind
// set, asked via ParseRef so this file never keeps a second copy of that
// set that could drift from it.
func knownTargetKind(s string) bool {
	_, err := inventory.ParseRef(s + "::x")
	return err == nil
}

// PolicyInput carries everything the evaluator reads besides the ops and
// the snapshot. Its zero value (no rules, no protected set) is a complete
// no-op, which is what makes running the evaluator unconditionally inside
// ValidateWithSafety safe (AC6).
type PolicyInput struct {
	// Protected is the onboarding-confirmed protected-interface set
	// (protected.go), so a rule can say "target.protected eq true" — the
	// declarative generalization of the one organisational rule this
	// codebase used to hard-code.
	Protected ProtectedSet
	Set       PolicySet
}

// PolicyRuleResult is one rule's outcome over one changeset: which ops it
// matched and which of those violated it. Two later cards consume this —
// T-2604 reads Tags/ViolatingOps to decide which op classes need N
// approvers, T-2706 reads the whole slice as compliance evidence — so it
// reports per-rule detail rather than only the findings.
type PolicyRuleResult struct {
	RuleID      string         `json:"ruleId"`
	Description string         `json:"description"`
	Severity    PolicySeverity `json:"severity"`
	Tags        []string       `json:"tags,omitempty"`
	// MatchedOps and ViolatingOps are indices into the ops slice the
	// evaluation ran over, in ascending order.
	MatchedOps   []int `json:"matchedOps,omitempty"`
	ViolatingOps []int `json:"violatingOps,omitempty"`
}

// PolicyResult is a whole policy evaluation: the findings to fold into the
// changeset, plus per-rule detail.
type PolicyResult struct {
	Findings []Finding          `json:"findings,omitempty"`
	Rules    []PolicyRuleResult `json:"rules,omitempty"`
}

// Denied reports whether any deny rule fired (i.e. whether this evaluation
// blocks the changeset).
func (r PolicyResult) Denied() bool {
	for _, rr := range r.Rules {
		if rr.Severity == PolicyDeny && len(rr.ViolatingOps) > 0 {
			return true
		}
	}
	return false
}

// MatchCounts maps each evaluated rule id to how many ops it matched — the
// input Service.recordPolicyStats persists so an unmatched-for-N-days rule
// can be reported as probably-misconfigured.
func (r PolicyResult) MatchCounts() map[string]int {
	out := make(map[string]int, len(r.Rules))
	for _, rr := range r.Rules {
		out[rr.RuleID] = len(rr.MatchedOps)
	}
	return out
}

// TaggedOps maps each tag carried by a matching rule to the op indices that
// rule matched — T-2604's "op classes ... anything a T-2601 policy tags".
// Note it is keyed on MATCHED ops, not violating ones: a tag declares a
// class of change, independently of whether the rule's assertion held.
func (r PolicyResult) TaggedOps() map[string][]int {
	out := map[string][]int{}
	for _, rr := range r.Rules {
		if len(rr.MatchedOps) == 0 {
			continue
		}
		for _, tag := range rr.Tags {
			seen := map[int]bool{}
			for _, i := range out[tag] {
				seen[i] = true
			}
			for _, i := range rr.MatchedOps {
				if !seen[i] {
					out[tag] = append(out[tag], i)
					seen[i] = true
				}
			}
			sort.Ints(out[tag])
		}
	}
	return out
}

// EvaluatePolicy runs in.Set over ops against snap. It is the single
// evaluation entry point: the validate-stage class (policyValidate), the
// CLI's `vnproxctl policy test`, and T-2604/T-2705/T-2706 all call this one
// function, so there is exactly one implementation of what a rule means.
//
// An empty rule set returns a zero PolicyResult — no findings, no rules —
// which is bit-for-bit "nothing happened" for every caller (AC6).
func EvaluatePolicy(in PolicyInput, ops []Op, snap inventory.Snapshot) PolicyResult {
	if in.Set.IsEmpty() {
		return PolicyResult{}
	}
	ev := newPolicyEvaluator(ops, snap, in.Protected)

	var out PolicyResult
	for _, rule := range in.Set.Rules {
		rr := PolicyRuleResult{RuleID: rule.ID, Description: rule.Description, Severity: rule.Severity, Tags: rule.Tags}
		for i, op := range ops {
			get := ev.fieldLookup(i, op)
			if !allConditionsHold(rule.Match, get) {
				continue
			}
			rr.MatchedOps = append(rr.MatchedOps, i)
			// An empty Assert means the match itself is the violation (see
			// PolicyRule) — NOT a vacuously-satisfied assertion.
			var failed PolicyCondition
			if len(rule.Assert) > 0 {
				var ok bool
				failed, ok = firstFailingCondition(rule.Assert, get)
				if ok {
					continue
				}
			}
			rr.ViolatingOps = append(rr.ViolatingOps, i)
			out.Findings = append(out.Findings, policyFinding(rule, op, failed))
		}
		out.Rules = append(out.Rules, rr)
	}
	return out
}

// policyFinding renders one violation. AC1: the message names the rule id
// AND its description; reason names the specific assertion that failed (or
// says the match itself is the violation, for an assert-less rule).
func policyFinding(rule PolicyRule, op Op, failed PolicyCondition) Finding {
	reason := "the op is forbidden by this rule"
	if failed.Field != "" {
		reason = fmt.Sprintf("failed assertion %s %s %s", failed.Field, failed.Op, formatPolicyValue(failed.Value))
	}
	msg := fmt.Sprintf("policy rule %q: %s (op %s: %s)", rule.ID, rule.Description, op.Type, reason)
	severity := SeverityError
	if rule.Severity == PolicyWarn {
		severity = SeverityWarning
	}
	return Finding{Severity: severity, Code: codePolicyViolation, Message: msg, Ref: op.Target.String()}
}

func formatPolicyValue(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func allConditionsHold(conds []PolicyCondition, get policyFieldGetter) bool {
	_, ok := firstFailingCondition(conds, get)
	return ok
}

// firstFailingCondition returns the first condition in conds that does not
// hold, and whether every condition held. Conditions are ANDed; an empty
// list holds vacuously (which is what gives an assert-less rule its
// "matching is itself the violation" semantics — see PolicyRule).
func firstFailingCondition(conds []PolicyCondition, get policyFieldGetter) (PolicyCondition, bool) {
	for _, c := range conds {
		if !conditionHolds(c, get) {
			return c, false
		}
	}
	return PolicyCondition{}, true
}

type policyFieldGetter func(field string) (any, bool)

// conditionHolds is the whole of the comparison semantics. Absent fields
// are handled explicitly per operator rather than defaulting, because
// params fields are `omitempty` on the wire: `params.vlan` is simply absent
// on an op that did not set it, and "vlan is not 1" must hold there.
func conditionHolds(c PolicyCondition, get policyFieldGetter) bool {
	v, present := get(c.Field)
	switch c.Op {
	case PolicyOpExists:
		return present && v != nil
	case PolicyOpNotExists:
		return !present || v == nil
	case PolicyOpEq:
		return present && policyValuesEqual(v, c.Value)
	case PolicyOpNe:
		return !present || !policyValuesEqual(v, c.Value)
	case PolicyOpIn:
		return present && policyValueIn(v, conditionLiterals(c))
	case PolicyOpNotIn:
		return !present || !policyValueIn(v, conditionLiterals(c))
	case PolicyOpGt, PolicyOpGte, PolicyOpLt, PolicyOpLte:
		return policyCompare(c.Op, v, c.Value, present)
	case PolicyOpMatches:
		s, ok := policyString(v)
		pattern, pok := c.Value.(string)
		return present && ok && pok && policyGlobMatch(pattern, s)
	case PolicyOpNotMatches:
		s, ok := policyString(v)
		pattern, pok := c.Value.(string)
		return !present || !ok || !pok || !policyGlobMatch(pattern, s)
	case PolicyOpContains:
		return present && policyContains(v, c.Value)
	case PolicyOpNotContains:
		return !present || !policyContains(v, c.Value)
	default:
		// Unreachable for a set that passed Validate; a rule with an
		// unknown operator never holds rather than silently passing.
		return false
	}
}

func policyCompare(op PolicyCondOp, v, want any, present bool) bool {
	if !present {
		return false
	}
	lhs, lok := policyNumber(v)
	rhs, rok := policyNumber(want)
	if !lok || !rok {
		return false
	}
	switch op {
	case PolicyOpGt:
		return lhs > rhs
	case PolicyOpGte:
		return lhs >= rhs
	case PolicyOpLt:
		return lhs < rhs
	case PolicyOpLte:
		return lhs <= rhs
	default:
		return false
	}
}

func policyValueIn(v any, list []any) bool {
	for _, want := range list {
		if policyValuesEqual(v, want) {
			return true
		}
	}
	return false
}

// policyContains is membership for a list-valued field (params.ports
// contains "eno1") and substring for a string-valued one.
func policyContains(v, want any) bool {
	if list, ok := v.([]any); ok {
		return policyValueIn(want, list)
	}
	s, sok := policyString(v)
	w, wok := policyString(want)
	return sok && wok && strings.Contains(s, w)
}

// policyValuesEqual compares two literals across the YAML (int) and JSON
// (float64) decodings both sides may arrive in, so `value: 1` in a policy
// file matches a params field that decoded as 1.0.
func policyValuesEqual(a, b any) bool {
	if an, aok := policyNumber(a); aok {
		if bn, bok := policyNumber(b); bok {
			return an == bn
		}
		return false
	}
	if ab, aok := a.(bool); aok {
		bb, bok := b.(bool)
		return bok && ab == bb
	}
	as, aok := policyString(a)
	bs, bok := policyString(b)
	return aok && bok && as == bs
}

func policyNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func policyString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// policyEvaluator holds the per-changeset state every op's fact lookup
// shares: the net-effect projection, the effective guest attachment counts,
// the effective VLAN-awareness map, and the protected-ref index.
type policyEvaluator struct {
	proj        *projection
	guestCounts map[ifaceKey]int
	vnetGuests  map[string]int
	vlanAware   map[ifaceKey]bool
	protected   map[inventory.Ref]bool
	paramsJSON  []map[string]any
	opCount     int
}

func newPolicyEvaluator(ops []Op, snap inventory.Snapshot, protected ProtectedSet) *policyEvaluator {
	ev := &policyEvaluator{
		proj:        newProjection(snap),
		guestCounts: map[ifaceKey]int{},
		vnetGuests:  map[string]int{},
		vlanAware:   map[ifaceKey]bool{},
		protected:   map[inventory.Ref]bool{},
		paramsJSON:  make([]map[string]any, len(ops)),
		opCount:     len(ops),
	}
	for _, refs := range protected {
		for _, r := range refs {
			ev.protected[r] = true
		}
	}

	// Base VLAN-awareness, then the changeset's own net effect on it.
	for _, e := range snap.All() {
		if b, ok := e.(*inventory.Bridge); ok {
			ev.vlanAware[ifaceKey{b.GetRef().Node, b.GetRef().ID}] = b.VlanAware
		}
	}

	for _, op := range ops {
		ev.proj.fold(op)
		switch p := op.Params.(type) {
		case *BridgeCreateParams:
			ev.vlanAware[ifaceKey{op.Target.Node, op.Target.ID}] = p.VlanAware
		case *BridgeUpdateParams:
			if p.VlanAware != nil {
				ev.vlanAware[ifaceKey{op.Target.Node, op.Target.ID}] = *p.VlanAware
			}
		case *BridgeDeleteParams:
			delete(ev.vlanAware, ifaceKey{op.Target.Node, op.Target.ID})
		}
	}

	ev.indexGuestAttachments(ops, snap)
	return ev
}

// indexGuestAttachments counts, per (node, bridge name) and per vnet name,
// how many guest NICs are attached once this changeset's net effect is
// applied — the same "last guest.nic.update naming a bridge/vnet wins" fold
// guestBearingBridgeFindings uses (validate_safety.go), so the fact and the
// built-in guard can never disagree about what "carries guests" means.
func (ev *policyEvaluator) indexGuestAttachments(ops []Op, snap inventory.Snapshot) {
	finalAttach := map[inventory.Ref]string{}
	for _, op := range ops {
		if op.Type != OpGuestNicUpdate {
			continue
		}
		if params, ok := op.Params.(*GuestNicUpdateParams); ok && params.BridgeOrVnet != nil {
			finalAttach[op.Target] = *params.BridgeOrVnet
		}
	}
	for _, e := range snap.All() {
		nic, ok := e.(*inventory.GuestNic)
		if !ok {
			continue
		}
		ref := nic.GetRef()
		name := nic.BridgeOrVnet.ID
		if attached, updated := finalAttach[ref]; updated {
			name = attached
		}
		if name == "" {
			continue
		}
		ev.guestCounts[ifaceKey{ref.Node, name}]++
		ev.vnetGuests[name]++
	}
}

// fieldLookup returns the getter one op's conditions resolve against.
func (ev *policyEvaluator) fieldLookup(index int, op Op) policyFieldGetter {
	return func(field string) (any, bool) {
		if strings.HasPrefix(field, policyParamsPrefix) {
			return ev.paramValue(index, op, strings.TrimPrefix(field, policyParamsPrefix))
		}
		return ev.fact(field, op)
	}
}

func (ev *policyEvaluator) fact(field string, op Op) (any, bool) {
	switch field {
	case policyFieldOpType:
		return string(op.Type), true
	case policyFieldTargetKind:
		return string(op.Target.Kind), !op.Target.IsZero()
	case policyFieldTargetNode:
		return op.Target.Node, !op.Target.IsZero()
	case policyFieldTargetID:
		return op.Target.ID, !op.Target.IsZero()
	case policyFieldTargetRef:
		return op.Target.String(), !op.Target.IsZero()
	case policyFieldTargetExists:
		return ev.proj.exists(op.Target), !op.Target.IsZero()
	case policyFieldTargetProtected:
		return ev.protected[op.Target], !op.Target.IsZero()
	case policyFieldTargetGuestCount:
		return ev.guestCount(op.Target), !op.Target.IsZero()
	case policyFieldTargetUplinks:
		return ev.uplinkCount(op.Target), !op.Target.IsZero()
	case policyFieldTargetVlanAware:
		v, ok := ev.vlanAware[ifaceKey{op.Target.Node, op.Target.ID}]
		return v, ok
	case policyFieldChangesetOpCount:
		return ev.opCount, true
	default:
		return nil, false
	}
}

// guestCount is the number of guest NICs that end up attached to target
// once the changeset's net effect is applied. Non-attachable kinds report
// 0 rather than "absent", so a rule can say `target.guestCount gt 0`
// without having to also exclude every other kind.
func (ev *policyEvaluator) guestCount(target inventory.Ref) int {
	switch target.Kind {
	case inventory.KindBridge, inventory.KindOVSBridge:
		return ev.guestCounts[ifaceKey{target.Node, target.ID}]
	case inventory.KindSDNVnet:
		return ev.vnetGuests[target.ID]
	default:
		return 0
	}
}

// uplinkCount is how many physical NICs / bonds are enslaved to target once
// the changeset's net effect is applied — "uplink" deliberately excludes
// VLAN sub-interfaces and other bridges, since those carry traffic that has
// to reach a wire through something else anyway.
func (ev *policyEvaluator) uplinkCount(target inventory.Ref) int {
	switch target.Kind {
	case inventory.KindBridge, inventory.KindOVSBridge:
	default:
		return 0
	}
	n := 0
	for member, owner := range ev.proj.enslaved {
		if owner != target {
			continue
		}
		switch member.Kind {
		case inventory.KindPhysNic, inventory.KindBond, inventory.KindOVSBond:
			n++
		}
	}
	return n
}

// paramValue resolves a dotted path into the op's own params object, as
// encoded by that op type's JSON shape (docs/data-model.md §3) — the same
// bytes the API accepted and the store persisted, never a second view of
// the params invented here.
func (ev *policyEvaluator) paramValue(index int, op Op, path string) (any, bool) {
	if index < 0 || index >= len(ev.paramsJSON) {
		return nil, false
	}
	if ev.paramsJSON[index] == nil {
		ev.paramsJSON[index] = decodeParams(op)
	}
	var cur any = ev.paramsJSON[index]
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func decodeParams(op Op) map[string]any {
	out := map[string]any{}
	if op.Params == nil {
		return out
	}
	b, err := json.Marshal(op.Params)
	if err != nil {
		return out
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return map[string]any{}
	}
	return out
}
