package change

// FwRuleCreateParams is op "fw.rule.create". Target is the FwRuleset the
// rule is added to (cluster/node/guest/vnet scope, per internal/inventory's
// FwRuleset.Ref — a vnet-scope target's ID is "vnet/<zone>/<vnet>", the
// same "<kind>/<...>" shape guest's "guest/<kind>/<vmid>" already uses,
// added by T-3103). Pos is the position to insert at (docs/data-model.md's
// FwRule.Pos is order-significant).
type FwRuleCreateParams struct {
	Direction string `json:"direction"` // forward|group|in|out
	Action    string `json:"action"`    // ACCEPT|DROP|REJECT
	Proto     string `json:"proto,omitempty"`
	Source    string `json:"source,omitempty"`
	Dest      string `json:"dest,omitempty"`
	Sport     string `json:"sport,omitempty"`
	Dport     string `json:"dport,omitempty"`
	Iface     string `json:"iface,omitempty"`
	Macro     string `json:"macro,omitempty"`
	Log       string `json:"log,omitempty"`
	Comment   string `json:"comment,omitempty"`
	Pos       int    `json:"pos"`
	Enabled   bool   `json:"enabled"`
}

func (FwRuleCreateParams) isChangeParams() {}

// FwRuleUpdateParams is op "fw.rule.update": a partial update of the rule
// currently at Pos within the ruleset named by the op's target (firewall
// rules have no other stable identity — see docs/data-model.md's FwRule,
// which is keyed by position within its ruleset, not a Ref of its own).
type FwRuleUpdateParams struct {
	Direction *string `json:"direction,omitempty"`
	Action    *string `json:"action,omitempty"`
	Proto     *string `json:"proto,omitempty"`
	Source    *string `json:"source,omitempty"`
	Dest      *string `json:"dest,omitempty"`
	Sport     *string `json:"sport,omitempty"`
	Dport     *string `json:"dport,omitempty"`
	Iface     *string `json:"iface,omitempty"`
	Macro     *string `json:"macro,omitempty"`
	Log       *string `json:"log,omitempty"`
	Comment   *string `json:"comment,omitempty"`
	Enabled   *bool   `json:"enabled,omitempty"`
	Pos       int     `json:"pos"`
}

func (FwRuleUpdateParams) isChangeParams() {}

// FwRuleDeleteParams is op "fw.rule.delete": removes the rule at Pos.
type FwRuleDeleteParams struct {
	Pos int `json:"pos"`
}

func (FwRuleDeleteParams) isChangeParams() {}

// FwRuleFields is a firewall rule's content identity (every field except
// Pos): used by FwRuleMoveParams.Expect to detect a live position race
// (T-502 acceptance criterion 3) and, on the executor side, as the
// wire shape read back from PVE to compare against. Two FwRuleFields
// values are compared with reflect.DeepEqual-equivalent field-by-field
// equality (see Equal) rather than a hash, so a mismatch's failure message
// can name which field actually changed.
type FwRuleFields struct {
	Direction string `json:"direction"`
	Action    string `json:"action"`
	Proto     string `json:"proto,omitempty"`
	Source    string `json:"source,omitempty"`
	Dest      string `json:"dest,omitempty"`
	Sport     string `json:"sport,omitempty"`
	Dport     string `json:"dport,omitempty"`
	Iface     string `json:"iface,omitempty"`
	Macro     string `json:"macro,omitempty"`
	Log       string `json:"log,omitempty"`
	Comment   string `json:"comment,omitempty"`
	Enabled   bool   `json:"enabled"`
}

// Equal reports whether f and other carry identical rule content.
func (f FwRuleFields) Equal(other FwRuleFields) bool { return f == other }

// FwRuleMoveParams is op "fw.rule.move": relocates the rule at FromPos to
// ToPos within the same ruleset.
//
// Expect, when set, is the rule content the client observed at FromPos
// when the move was drafted (the drag-to-reorder UI captures it at drag
// start). The apply-time executor re-fetches the live rule at FromPos
// immediately before moving it and refuses the move (failing the whole
// step, per docs/features/firewall.md §2's "concurrent-edit-safe via
// revalidation against live position state at apply") if it no longer
// matches — acceptance criterion 3's "fixture position shifted between
// draft and apply" race. Expect is optional (nil skips revalidation) so
// a programmatic move — e.g. from a rollback/inverse-op replay, which by
// construction runs immediately after the forward move and cannot race
// with anything — doesn't need to fabricate one.
type FwRuleMoveParams struct {
	Expect  *FwRuleFields `json:"expect,omitempty"`
	FromPos int           `json:"fromPos"`
	ToPos   int           `json:"toPos"`
}

func (FwRuleMoveParams) isChangeParams() {}

// FwOptionsUpdateParams is op "fw.options.update": the ruleset-level
// enabled flag and default in/out/forward policies (docs/data-model.md's
// FwRuleset.Enabled/DefaultIn/DefaultOut/DefaultForward).
//
// DefaultForward/LogLevelForward (T-3103) are the forward chain's own
// fallthrough policy and log level. DefaultForward is valid at cluster,
// node, and vnet scope (never guest, which has no forward chain);
// LogLevelForward is only hardware-captured at vnet scope and is rejected
// at every other scope — see validate_schema.go's schemaFwOptionsForScope,
// which also rejects DefaultIn/DefaultOut at vnet scope (real PVE's vnet
// options endpoint has no policy_in/policy_out fields at all).
type FwOptionsUpdateParams struct {
	DefaultIn       *string `json:"defaultIn,omitempty"`
	DefaultOut      *string `json:"defaultOut,omitempty"`
	DefaultForward  *string `json:"defaultForward,omitempty"`
	LogLevelForward *string `json:"logLevelForward,omitempty"`
	Enabled         *bool   `json:"enabled,omitempty"`
}

func (FwOptionsUpdateParams) isChangeParams() {}

// Aliases, IPsets, and security groups (fw.alias.*, fw.ipset.*, fw.group.*)
// are cluster-scoped named firewall objects with no dedicated
// internal/inventory.Kind of their own (docs/data-model.md §1's entity
// list has no "fw-alias"/"fw-ipset"/"fw-group" kind) — they're referenced
// by name from FwRule.Source/Dest/Macro, not modeled as graph entities.
// Their ops therefore target the cluster firewall ruleset scope
// (Ref{Kind: KindFwRuleset, Node: "", ID: "cluster"}) and carry their own
// Name field to identify which alias/ipset/group is being
// created/updated/deleted, unlike the entity-backed ops above where the
// target Ref alone is the identity.

// FwAliasCreateParams is op "fw.alias.create".
type FwAliasCreateParams struct {
	Name    string `json:"name"`
	CIDR    string `json:"cidr"`
	Comment string `json:"comment,omitempty"`
}

func (FwAliasCreateParams) isChangeParams() {}

// FwAliasUpdateParams is op "fw.alias.update".
type FwAliasUpdateParams struct {
	CIDR    *string `json:"cidr,omitempty"`
	Comment *string `json:"comment,omitempty"`
	Name    string  `json:"name"`
}

func (FwAliasUpdateParams) isChangeParams() {}

// FwAliasDeleteParams is op "fw.alias.delete".
type FwAliasDeleteParams struct {
	Name string `json:"name"`
}

func (FwAliasDeleteParams) isChangeParams() {}

// FwIpsetCreateParams is op "fw.ipset.create".
type FwIpsetCreateParams struct {
	Name    string   `json:"name"`
	Comment string   `json:"comment,omitempty"`
	CIDRs   []string `json:"cidrs,omitempty"`
}

func (FwIpsetCreateParams) isChangeParams() {}

// FwIpsetUpdateParams is op "fw.ipset.update".
type FwIpsetUpdateParams struct {
	CIDRs   *[]string `json:"cidrs,omitempty"`
	Comment *string   `json:"comment,omitempty"`
	Name    string    `json:"name"`
}

func (FwIpsetUpdateParams) isChangeParams() {}

// FwIpsetDeleteParams is op "fw.ipset.delete".
type FwIpsetDeleteParams struct {
	Name string `json:"name"`
}

func (FwIpsetDeleteParams) isChangeParams() {}

// FwRuleSpec is one rule inside a security group's Rules list
// (FwGroupCreateParams/FwGroupUpdateParams). Unlike a top-level
// fw.rule.create op, a group's member rules have no independent Pos of
// their own on the wire — order in the JSON array is the order — so this
// intentionally omits Pos (list order carries it) though the group's
// applied rules are still positionable once referenced from an actual
// ruleset via a group macro, same as real PVE.
type FwRuleSpec struct {
	Direction string `json:"direction"`
	Action    string `json:"action"`
	Proto     string `json:"proto,omitempty"`
	Source    string `json:"source,omitempty"`
	Dest      string `json:"dest,omitempty"`
	Sport     string `json:"sport,omitempty"`
	Dport     string `json:"dport,omitempty"`
	Macro     string `json:"macro,omitempty"`
	Comment   string `json:"comment,omitempty"`
	Enabled   bool   `json:"enabled"`
}

// FwGroupCreateParams is op "fw.group.create" (a security group).
type FwGroupCreateParams struct {
	Name    string       `json:"name"`
	Comment string       `json:"comment,omitempty"`
	Rules   []FwRuleSpec `json:"rules,omitempty"`
}

func (FwGroupCreateParams) isChangeParams() {}

// FwGroupUpdateParams is op "fw.group.update".
type FwGroupUpdateParams struct {
	Comment *string       `json:"comment,omitempty"`
	Rules   *[]FwRuleSpec `json:"rules,omitempty"`
	Name    string        `json:"name"`
}

func (FwGroupUpdateParams) isChangeParams() {}

// FwGroupDeleteParams is op "fw.group.delete".
type FwGroupDeleteParams struct {
	Name string `json:"name"`
}

func (FwGroupDeleteParams) isChangeParams() {}
