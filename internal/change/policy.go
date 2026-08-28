// SPDX-License-Identifier: Apache-2.0

// policy.go implements T-2601's declarative policy-as-code guardrail: the
// rule-set data model, its load-time validation, and its rule-set diff.
//
// DESIGN CONSTRAINT (the card's own, and the arc risk register's): a policy
// is **declarative data, not a scripting language**. There is no expression
// interpreter here and no policy-engine dependency. A rule is a pair of
// condition lists — `match` (which ops the rule is about) and `assert` (what
// must be true of them) — where every condition is a fixed
// `{field, op, value}` triple over the *same op and inventory shapes the
// change engine already uses*: an op's own documented JSON wire shape
// (docs/data-model.md §3) plus a small, closed, documented set of derived
// inventory facts (policy_eval.go's factFields). Anything a rule wants to
// say that those shapes cannot express is a decision to record, not a
// language feature to add.
//
// protected.go encodes exactly one organisational rule (don't cut the
// management path) in Go, hard-coded. This file is that pattern generalized:
// the same kind of statement, written as data, versioned in the store, and
// evaluated at the same validate stage — never a second gate somewhere else
// in the lifecycle.

package change

import (
	"fmt"
	"path"
	"reflect"
	"sort"
	"strings"
)

// PolicyFormatVersion is the policy document's on-disk/on-wire schema
// version — a single integer bumped only on a breaking shape change, the
// same convention protectedConfigVersion (protected.go) already uses. It is
// deliberately NOT the store revision: a stored policy set additionally
// carries a monotonically increasing `revision` (policy_service.go), which
// is what "policies are versioned in the store" means.
const PolicyFormatVersion = 1

// PolicySeverity is a rule's blocking-ness. Deliberately only two values:
// a rule either refuses the changeset or annotates it.
type PolicySeverity string

const (
	// PolicyDeny blocks validation. Its finding is SeverityError, so every
	// existing gate that already refuses to diff/apply a changeset with a
	// blocking finding refuses this one too — no new enforcement point.
	PolicyDeny PolicySeverity = "deny"
	// PolicyWarn annotates the changeset. Its finding is SeverityWarning,
	// so it rides the changeset's existing Findings array all the way to
	// the review surface without blocking anything.
	PolicyWarn PolicySeverity = "warn"
)

// PolicyCondOp is the closed set of comparison operators a condition may
// use. There is no operator that takes an expression: every operator
// compares one named field against one literal (or a literal list).
type PolicyCondOp string

const (
	PolicyOpEq          PolicyCondOp = "eq"
	PolicyOpNe          PolicyCondOp = "ne"
	PolicyOpIn          PolicyCondOp = "in"
	PolicyOpNotIn       PolicyCondOp = "notIn"
	PolicyOpGt          PolicyCondOp = "gt"
	PolicyOpGte         PolicyCondOp = "gte"
	PolicyOpLt          PolicyCondOp = "lt"
	PolicyOpLte         PolicyCondOp = "lte"
	PolicyOpMatches     PolicyCondOp = "matches"
	PolicyOpNotMatches  PolicyCondOp = "notMatches"
	PolicyOpExists      PolicyCondOp = "exists"
	PolicyOpNotExists   PolicyCondOp = "notExists"
	PolicyOpContains    PolicyCondOp = "contains"
	PolicyOpNotContains PolicyCondOp = "notContains"
)

// policyCondOps is the closed operator set, with the arity each one
// expects: valueScalar wants a single literal, valueList wants a list,
// valueNone wants no value at all. An operator outside this map, or a
// value of the wrong arity, is a load error (AC5).
var policyCondOps = map[PolicyCondOp]policyValueArity{
	PolicyOpEq:          valueScalar,
	PolicyOpNe:          valueScalar,
	PolicyOpIn:          valueList,
	PolicyOpNotIn:       valueList,
	PolicyOpGt:          valueNumber,
	PolicyOpGte:         valueNumber,
	PolicyOpLt:          valueNumber,
	PolicyOpLte:         valueNumber,
	PolicyOpMatches:     valueString,
	PolicyOpNotMatches:  valueString,
	PolicyOpExists:      valueNone,
	PolicyOpNotExists:   valueNone,
	PolicyOpContains:    valueScalar,
	PolicyOpNotContains: valueScalar,
}

type policyValueArity int

const (
	valueScalar policyValueArity = iota
	valueList
	valueNumber
	valueString
	valueNone
)

// PolicyCondition is one `{field, op, value}` triple. Field names a fact
// (policy_eval.go's factFields) or a `params.<jsonField>` path into the op's
// own params object; Op is one of the closed PolicyCondOp set; Value is a
// literal (or a literal list, for in/notIn).
type PolicyCondition struct {
	// Value is a YAML/JSON literal: string, number, bool, or (for
	// in/notIn) a list of those. It is never an expression.
	Value any          `yaml:"value,omitempty" json:"value,omitempty"`
	Field string       `yaml:"field" json:"field"`
	Op    PolicyCondOp `yaml:"op" json:"op"`
}

// PolicyRule is one organisational rule, exactly the card's shape:
// `{id, description, severity, match, assert}` (plus Tags, which T-2604
// consumes to declare op classes requiring N approvers — see
// PolicyResult.TaggedOps).
//
// Semantics: for every op the whole of Match accepts (conditions are ANDed),
// the whole of Assert must hold. An op that matches and fails any assertion
// violates the rule. **An empty Assert means the match itself is the
// violation** — the "never touch vmbr9 on the storage nodes" shape, which
// has nothing to assert beyond "this op should not exist".
type PolicyRule struct {
	ID          string            `yaml:"id" json:"id"`
	Description string            `yaml:"description" json:"description"`
	Severity    PolicySeverity    `yaml:"severity" json:"severity"`
	Tags        []string          `yaml:"tags,omitempty" json:"tags,omitempty"`
	Match       []PolicyCondition `yaml:"match" json:"match"`
	Assert      []PolicyCondition `yaml:"assert,omitempty" json:"assert,omitempty"`
}

// PolicySet is a whole policy document: a format version plus an ordered
// rule list. The zero value is a valid, empty set that changes nothing —
// which is what makes the engine safe to run unconditionally inside
// ValidateWithSafety (AC6).
type PolicySet struct {
	Rules   []PolicyRule `yaml:"rules" json:"rules"`
	Version int          `yaml:"version" json:"version"`
}

// IsEmpty reports whether s carries no rules at all.
func (s PolicySet) IsEmpty() bool { return len(s.Rules) == 0 }

// RuleIDs returns every rule id in document order.
func (s PolicySet) RuleIDs() []string {
	out := make([]string, 0, len(s.Rules))
	for _, r := range s.Rules {
		out = append(out, r.ID)
	}
	return out
}

// PolicyLoadError is the single error type every policy load/validation
// failure takes, so the message always names the file, the offending rule,
// and the offending field (AC5). Any of the three may legitimately be empty
// (a document-level syntax error has no rule id yet); the message simply
// omits what it does not know.
type PolicyLoadError struct {
	File   string
	RuleID string
	Field  string
	Msg    string
}

func (e *PolicyLoadError) Error() string {
	var b strings.Builder
	b.WriteString("change: policy")
	if e.File != "" {
		fmt.Fprintf(&b, " file %s", e.File)
	}
	if e.RuleID != "" {
		fmt.Fprintf(&b, ": rule %q", e.RuleID)
	}
	if e.Field != "" {
		fmt.Fprintf(&b, ": field %q", e.Field)
	}
	fmt.Fprintf(&b, ": %s", e.Msg)
	return b.String()
}

// Validate checks s for every statically-decidable defect, returning the
// first as a *PolicyLoadError naming file, rule, and field. file is only
// used to build the message (an in-memory set can pass "").
//
// Beyond the obvious well-formedness checks, this deliberately enforces the
// card's "**a policy that matches nothing is an error, not a silent pass**"
// in its statically-decidable half:
//
//   - an `op` condition whose pattern/values match ZERO op types in the v1
//     vocabulary can never fire — that is a typo, not a rule;
//   - a `params.<name>` condition whose root field exists on NONE of the op
//     types the rule can match can never resolve — likewise a typo;
//   - a `target.kind` value outside inventory's closed Kind set likewise.
//
// The other half (a well-formed rule that simply never matches real
// traffic) cannot be decided statically and is reported at runtime instead,
// from the per-rule store stats — see Service.PolicyStatus.
func (s PolicySet) Validate(file string) error {
	if s.Version != 0 && s.Version != PolicyFormatVersion {
		return &PolicyLoadError{File: file, Field: "version", Msg: fmt.Sprintf("unsupported policy format version %d (this build understands %d)", s.Version, PolicyFormatVersion)}
	}
	seen := map[string]bool{}
	for i, r := range s.Rules {
		if err := validatePolicyRule(file, i, r, seen); err != nil {
			return err
		}
		seen[r.ID] = true
	}
	return nil
}

func validatePolicyRule(file string, i int, r PolicyRule, seen map[string]bool) error {
	at := func(field string) string { return fmt.Sprintf("rules[%d].%s", i, field) }
	fail := func(field, msg string, args ...any) error {
		return &PolicyLoadError{File: file, RuleID: r.ID, Field: at(field), Msg: fmt.Sprintf(msg, args...)}
	}

	if strings.TrimSpace(r.ID) == "" {
		return fail("id", "rule id is required")
	}
	if seen[r.ID] {
		return fail("id", "duplicate rule id %q", r.ID)
	}
	if strings.TrimSpace(r.Description) == "" {
		// The description is not decoration: AC1 requires the blocking
		// error to name it, so a rule without one produces an error the
		// operator cannot act on.
		return fail("description", "rule description is required (it is what the blocking error tells the operator)")
	}
	if r.Severity != PolicyDeny && r.Severity != PolicyWarn {
		return fail("severity", "severity must be %q or %q, got %q", PolicyDeny, PolicyWarn, r.Severity)
	}
	if len(r.Match) == 0 {
		return fail("match", "match must have at least one condition (a rule that matches every op in every changeset is never what was meant)")
	}

	candidates := r.candidateOpTypes()
	if len(candidates) == 0 {
		return fail("match", "no op type in the v1 vocabulary can ever satisfy this rule's `op` conditions, so it can never fire")
	}

	for j, c := range r.Match {
		if err := validatePolicyCondition(file, r.ID, at(fmt.Sprintf("match[%d]", j)), c, candidates); err != nil {
			return err
		}
	}
	for j, c := range r.Assert {
		if err := validatePolicyCondition(file, r.ID, at(fmt.Sprintf("assert[%d]", j)), c, candidates); err != nil {
			return err
		}
	}
	return nil
}

func validatePolicyCondition(file, ruleID, at string, c PolicyCondition, candidates map[OpType]bool) error {
	fail := func(field, msg string, args ...any) error {
		return &PolicyLoadError{File: file, RuleID: ruleID, Field: at + "." + field, Msg: fmt.Sprintf(msg, args...)}
	}

	if strings.TrimSpace(c.Field) == "" {
		return fail("field", "field is required")
	}
	arity, known := policyCondOps[c.Op]
	if !known {
		return fail("op", "unknown operator %q (known: %s)", c.Op, strings.Join(knownPolicyCondOps(), ", "))
	}
	if err := validatePolicyValueArity(c, arity, fail); err != nil {
		return err
	}

	switch {
	case strings.HasPrefix(c.Field, policyParamsPrefix):
		root := strings.TrimPrefix(c.Field, policyParamsPrefix)
		root, _, _ = strings.Cut(root, ".")
		if root == "" {
			return fail("field", "%q names no params field", c.Field)
		}
		if !anyOpHasParamField(candidates, root) {
			return fail("field", "no op type this rule can match has a params field %q", root)
		}
	case factFields[c.Field]:
		// A known derived fact: nothing more to check statically.
	default:
		return fail("field", "unknown field %q (known: %s, or params.<field>)", c.Field, strings.Join(knownFactFields(), ", "))
	}

	if c.Field == policyFieldOpType {
		if len(opTypesForCondition(c)) == 0 {
			return fail("value", "no op type in the v1 vocabulary matches this condition, so it can never fire")
		}
	}
	if c.Field == policyFieldTargetKind {
		for _, v := range conditionLiterals(c) {
			s, ok := v.(string)
			if !ok {
				return fail("value", "target.kind values must be strings, got %T", v)
			}
			if !knownTargetKind(s) {
				return fail("value", "unknown entity kind %q", s)
			}
		}
	}
	return nil
}

func validatePolicyValueArity(c PolicyCondition, arity policyValueArity, fail func(string, string, ...any) error) error {
	switch arity {
	case valueNone:
		if c.Value != nil {
			return fail("value", "operator %q takes no value", c.Op)
		}
	case valueList:
		if _, ok := c.Value.([]any); !ok {
			return fail("value", "operator %q requires a list of values", c.Op)
		}
		if len(c.Value.([]any)) == 0 {
			return fail("value", "operator %q requires a non-empty list", c.Op)
		}
	case valueNumber:
		if _, ok := policyNumber(c.Value); !ok {
			return fail("value", "operator %q requires a numeric value, got %v", c.Op, c.Value)
		}
	case valueString:
		if _, ok := c.Value.(string); !ok {
			return fail("value", "operator %q requires a string pattern, got %v", c.Op, c.Value)
		}
	case valueScalar:
		if c.Value == nil {
			return fail("value", "operator %q requires a value", c.Op)
		}
		if _, isList := c.Value.([]any); isList {
			return fail("value", "operator %q takes a single value, not a list", c.Op)
		}
	}
	return nil
}

func knownPolicyCondOps() []string {
	out := make([]string, 0, len(policyCondOps))
	for op := range policyCondOps {
		out = append(out, string(op))
	}
	sort.Strings(out)
	return out
}

// conditionLiterals returns every literal a condition compares against (one
// for a scalar operator, several for in/notIn, none for exists/notExists).
func conditionLiterals(c PolicyCondition) []any {
	if c.Value == nil {
		return nil
	}
	if list, ok := c.Value.([]any); ok {
		return list
	}
	return []any{c.Value}
}

// candidateOpTypes narrows the v1 op vocabulary to the types this rule's
// `op` conditions can possibly select. With no `op` condition it is the
// whole vocabulary. It is used both for the load-time "can never fire"
// check and for the `params.<field>` existence check.
func (r PolicyRule) candidateOpTypes() map[OpType]bool {
	out := map[OpType]bool{}
	for t := range paramFactories {
		out[t] = true
	}
	for _, c := range r.Match {
		if c.Field != policyFieldOpType {
			continue
		}
		allowed := opTypesForCondition(c)
		for t := range out {
			if !allowed[t] {
				delete(out, t)
			}
		}
	}
	return out
}

// opTypesForCondition returns the op types a single `op`-field condition
// admits. Operators that cannot be decided against the vocabulary alone
// (numeric comparisons, contains) admit everything rather than pretending
// to narrow.
func opTypesForCondition(c PolicyCondition) map[OpType]bool {
	all := map[OpType]bool{}
	for t := range paramFactories {
		all[t] = true
	}
	admit := func(keep func(OpType) bool) map[OpType]bool {
		out := map[OpType]bool{}
		for t := range all {
			if keep(t) {
				out[t] = true
			}
		}
		return out
	}
	literals := conditionLiterals(c)
	switch c.Op {
	case PolicyOpEq, PolicyOpIn:
		return admit(func(t OpType) bool {
			for _, v := range literals {
				if s, ok := v.(string); ok && s == string(t) {
					return true
				}
			}
			return false
		})
	case PolicyOpNe, PolicyOpNotIn:
		return admit(func(t OpType) bool {
			for _, v := range literals {
				if s, ok := v.(string); ok && s == string(t) {
					return false
				}
			}
			return true
		})
	case PolicyOpMatches, PolicyOpNotMatches:
		pattern, _ := c.Value.(string)
		want := c.Op == PolicyOpMatches
		return admit(func(t OpType) bool { return policyGlobMatch(pattern, string(t)) == want })
	case PolicyOpNotExists:
		return map[OpType]bool{}
	default:
		return all
	}
}

// policyGlobMatch is the one and only pattern syntax a policy may use:
// path.Match's shell globbing, whose separator is '/' and therefore treats
// the dots in op types and entity ids as ordinary characters ("fw.*" matches
// "fw.rule.create"). A malformed pattern matches nothing rather than
// panicking; PolicySet.Validate's "can never fire" check turns that into a
// load error for `op` conditions.
func policyGlobMatch(pattern, s string) bool {
	ok, err := path.Match(pattern, s)
	return err == nil && ok
}

// paramFieldCache memoizes the top-level JSON field names of each op type's
// params struct, derived by reflection over the struct tags — the same
// shapes the change engine already decodes ops into, never a second
// hand-maintained schema that could drift from them.
var paramFieldCache = map[OpType]map[string]bool{}

func paramFieldNames(t OpType) map[string]bool {
	if cached, ok := paramFieldCache[t]; ok {
		return cached
	}
	out := map[string]bool{}
	factory, ok := paramFactories[t]
	if !ok {
		paramFieldCache[t] = out
		return out
	}
	rt := reflect.TypeOf(factory())
	for rt != nil && rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt != nil && rt.Kind() == reflect.Struct {
		for i := range rt.NumField() {
			f := rt.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" {
				name = f.Name
			}
			if name != "-" {
				out[name] = true
			}
		}
	}
	paramFieldCache[t] = out
	return out
}

func anyOpHasParamField(candidates map[OpType]bool, field string) bool {
	for t := range candidates {
		if paramFieldNames(t)[field] {
			return true
		}
	}
	return false
}

// PolicyRuleChange is one rule that differs between two policy sets: both
// sides are carried in full, so the audit entry alone reconstructs the
// change (AC7).
type PolicyRuleChange struct {
	Before PolicyRule `json:"before"`
	After  PolicyRule `json:"after"`
}

// PolicyDiff is the rule-set delta between two policy sets, in full-body
// form. It is what the policy.update audit entry carries: reading that one
// entry tells you exactly which rules appeared, vanished, or changed, and
// what they said before and after — no need to reconstruct from a chain of
// prior entries.
type PolicyDiff struct {
	Added   []PolicyRule       `json:"added,omitempty"`
	Removed []PolicyRule       `json:"removed,omitempty"`
	Changed []PolicyRuleChange `json:"changed,omitempty"`
}

// IsEmpty reports whether the two sets carried identical rules.
func (d PolicyDiff) IsEmpty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// DiffPolicySets computes the rule-set delta from old to new, keyed by rule
// id and ordered deterministically (by id) so the audit payload of the same
// change is byte-identical whoever makes it.
func DiffPolicySets(oldSet, newSet PolicySet) PolicyDiff {
	oldByID := map[string]PolicyRule{}
	for _, r := range oldSet.Rules {
		oldByID[r.ID] = r
	}
	newByID := map[string]PolicyRule{}
	for _, r := range newSet.Rules {
		newByID[r.ID] = r
	}

	var d PolicyDiff
	ids := make([]string, 0, len(oldByID)+len(newByID))
	for id := range oldByID {
		ids = append(ids, id)
	}
	for id := range newByID {
		if _, dup := oldByID[id]; !dup {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	for _, id := range ids {
		before, hadBefore := oldByID[id]
		after, hasAfter := newByID[id]
		switch {
		case !hadBefore:
			d.Added = append(d.Added, after)
		case !hasAfter:
			d.Removed = append(d.Removed, before)
		case !policyRulesEqual(before, after):
			d.Changed = append(d.Changed, PolicyRuleChange{Before: before, After: after})
		}
	}
	return d
}

func policyRulesEqual(a, b PolicyRule) bool { return reflect.DeepEqual(a, b) }
