package change

// FwRuleCreateParams is op "fw.rule.create". Target is the FwRuleset the
// rule is added to (cluster/node/guest scope, per internal/inventory's
// FwRuleset.Ref). Pos is the position to insert at (docs/data-model.md's
// FwRule.Pos is order-significant).
type FwRuleCreateParams struct {
	Direction string `json:"direction"` // in|out
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

// FwRuleMoveParams is op "fw.rule.move": relocates the rule at FromPos to
// ToPos within the same ruleset.
type FwRuleMoveParams struct {
	FromPos int `json:"fromPos"`
	ToPos   int `json:"toPos"`
}

func (FwRuleMoveParams) isChangeParams() {}

// FwOptionsUpdateParams is op "fw.options.update": the ruleset-level
// enabled flag and default in/out policies (docs/data-model.md's
// FwRuleset.Enabled/DefaultIn/DefaultOut).
type FwOptionsUpdateParams struct {
	DefaultIn  *string `json:"defaultIn,omitempty"`
	DefaultOut *string `json:"defaultOut,omitempty"`
	Enabled    *bool   `json:"enabled,omitempty"`
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
