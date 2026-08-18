package pve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// This file adds T-502's write-side firewall API surface: rule/options/
// alias/ipset/group CRUD plus the moveto reposition semantics — firewall.go
// (added by T-501) only needed the read side. Every method mirrors the read
// methods' shape (thin c.do wrapper, no client-side business logic; that
// lives in internal/change's op validators/executor).

// CreateFirewallRule calls POST {scope}/rules: appends rule to the end of
// the ruleset. Real PVE's create endpoint has no "insert at position"
// parameter — a rule is always appended, then repositioned with
// UpdateFirewallRule's moveTo if an earlier position was wanted (exactly
// how a human operator uses the same real API). This is why
// FwRuleCreateParams.Pos may require a follow-up move; see the op
// executor (cmd/vnproxd/changeagent.go) for that two-step realization.
func (c *Client) CreateFirewallRule(ctx context.Context, scope FirewallScope, rule FirewallRule) error {
	return c.do(ctx, "POST", scope.prefix+"/rules", requestParams{body: rule}, nil)
}

// UpdateFirewallRule calls PUT {scope}/rules/{pos}: replaces the rule's
// field content and, when moveTo is non-nil, also relocates it to a new
// position in the same call (real PVE's own "moveto" param on this same
// endpoint). T-502's fw.rule.move op executor passes the rule's own
// unchanged fields (read via GetFirewallRule first) plus moveTo; a plain
// fw.rule.update passes the merged field content with moveTo nil.
//
// Builds the body via a map, not struct embedding: `struct{ Moveto *int;
// FirewallRule }` looks like it would emit both moveto and rule's own
// fields side by side, but FirewallRule.MarshalJSON (pvebool.go, T-3202)
// gets promoted to the anonymous struct's own method set the moment
// FirewallRule implements json.Marshaler — Go then marshals the WHOLE
// struct as FirewallRule alone, silently dropping moveto. Verified this
// really does drop the field (not a hypothetical) before choosing the map
// form below.
func (c *Client) UpdateFirewallRule(ctx context.Context, scope FirewallScope, pos int, rule FirewallRule, moveTo *int) error {
	ruleJSON, err := json.Marshal(rule)
	if err != nil {
		return fmt.Errorf("pve: marshaling firewall rule for update: %w", err)
	}
	body := map[string]any{}
	if err := json.Unmarshal(ruleJSON, &body); err != nil {
		return fmt.Errorf("pve: re-decoding firewall rule for update: %w", err)
	}
	if moveTo != nil {
		body["moveto"] = *moveTo
	}
	path := fmt.Sprintf("%s/rules/%d", scope.prefix, pos)
	return c.do(ctx, "PUT", path, requestParams{body: body}, nil)
}

// DeleteFirewallRule calls DELETE {scope}/rules/{pos}.
func (c *Client) DeleteFirewallRule(ctx context.Context, scope FirewallScope, pos int) error {
	path := fmt.Sprintf("%s/rules/%d", scope.prefix, pos)
	return c.do(ctx, "DELETE", path, requestParams{}, nil)
}

// FirewallOptionsUpdate is a partial update to a ruleset's enable/policy
// state (PUT {scope}/options); a nil field is left unchanged server-side,
// matching every other partial-update Params shape in this codebase
// (internal/change's pointer-field "unset means don't touch" convention).
type FirewallOptionsUpdate struct {
	Enable    *bool
	PolicyIn  *string
	PolicyOut *string
	// PolicyForward/LogLevelForward (T-3103): the forward chain's own
	// fallthrough policy and log level — see FirewallOptions' doc comment
	// for which scopes are hardware-confirmed to accept each.
	PolicyForward   *string
	LogLevelForward *string
}

// UpdateFirewallOptions calls PUT {scope}/options with only the fields
// upd sets (real PVE's options endpoint accepts a form body of whichever
// fields the caller wants to change).
func (c *Client) UpdateFirewallOptions(ctx context.Context, scope FirewallScope, upd FirewallOptionsUpdate) error {
	body := map[string]string{}
	if upd.Enable != nil {
		if *upd.Enable {
			body["enable"] = "1"
		} else {
			body["enable"] = "0"
		}
	}
	if upd.PolicyIn != nil {
		body["policy_in"] = *upd.PolicyIn
	}
	if upd.PolicyOut != nil {
		body["policy_out"] = *upd.PolicyOut
	}
	if upd.PolicyForward != nil {
		body["policy_forward"] = *upd.PolicyForward
	}
	if upd.LogLevelForward != nil {
		body["log_level_forward"] = *upd.LogLevelForward
	}
	return c.do(ctx, "PUT", scope.prefix+"/options", requestParams{body: body}, nil)
}

// CreateFirewallAlias calls POST {scope}/aliases.
func (c *Client) CreateFirewallAlias(ctx context.Context, scope FirewallScope, alias FirewallAlias) error {
	return c.do(ctx, "POST", scope.prefix+"/aliases", requestParams{body: alias}, nil)
}

// UpdateFirewallAlias calls PUT {scope}/aliases/{name}.
func (c *Client) UpdateFirewallAlias(ctx context.Context, scope FirewallScope, name string, alias FirewallAlias) error {
	path := fmt.Sprintf("%s/aliases/%s", scope.prefix, url.PathEscape(name))
	return c.do(ctx, "PUT", path, requestParams{body: alias}, nil)
}

// DeleteFirewallAlias calls DELETE {scope}/aliases/{name}.
func (c *Client) DeleteFirewallAlias(ctx context.Context, scope FirewallScope, name string) error {
	path := fmt.Sprintf("%s/aliases/%s", scope.prefix, url.PathEscape(name))
	return c.do(ctx, "DELETE", path, requestParams{}, nil)
}

// CreateFirewallIPSet calls POST {scope}/ipset.
func (c *Client) CreateFirewallIPSet(ctx context.Context, scope FirewallScope, name, comment string) error {
	body := FirewallIPSetSummary{Name: name, Comment: comment}
	return c.do(ctx, "POST", scope.prefix+"/ipset", requestParams{body: body}, nil)
}

// UpdateFirewallIPSet calls PUT {scope}/ipset/{name}: updates the ipset's
// comment (Name is not editable — fw.ipset.update's Name field is the
// op's own identity, matching fw.alias.update's convention).
func (c *Client) UpdateFirewallIPSet(ctx context.Context, scope FirewallScope, name, comment string) error {
	path := fmt.Sprintf("%s/ipset/%s", scope.prefix, url.PathEscape(name))
	body := struct {
		Comment string `json:"comment"`
	}{Comment: comment}
	return c.do(ctx, "PUT", path, requestParams{body: body}, nil)
}

// DeleteFirewallIPSet calls DELETE {scope}/ipset/{name}.
func (c *Client) DeleteFirewallIPSet(ctx context.Context, scope FirewallScope, name string) error {
	path := fmt.Sprintf("%s/ipset/%s", scope.prefix, url.PathEscape(name))
	return c.do(ctx, "DELETE", path, requestParams{}, nil)
}

// CreateFirewallIPSetEntry calls POST {scope}/ipset/{name}.
func (c *Client) CreateFirewallIPSetEntry(ctx context.Context, scope FirewallScope, name string, entry FirewallIPSetEntry) error {
	path := fmt.Sprintf("%s/ipset/%s", scope.prefix, url.PathEscape(name))
	return c.do(ctx, "POST", path, requestParams{body: entry}, nil)
}

// DeleteFirewallIPSetEntry calls DELETE {scope}/ipset/{name}/{cidr}.
func (c *Client) DeleteFirewallIPSetEntry(ctx context.Context, scope FirewallScope, name, cidr string) error {
	path := fmt.Sprintf("%s/ipset/%s/%s", scope.prefix, url.PathEscape(name), url.PathEscape(cidr))
	return c.do(ctx, "DELETE", path, requestParams{}, nil)
}

// CreateFirewallGroup calls POST /cluster/firewall/groups: registers a new
// (initially empty) security group. Real PVE populates a group's rules via
// separate calls to CreateFirewallGroupRule, one at a time — see
// FwGroupCreateParams's doc comment in internal/change/params_fw.go.
func (c *Client) CreateFirewallGroup(ctx context.Context, name, comment string) error {
	body := FirewallGroupSummary{Name: name, Comment: comment}
	return c.do(ctx, "POST", "/cluster/firewall/groups", requestParams{body: body}, nil)
}

// DeleteFirewallGroup calls DELETE /cluster/firewall/groups/{group}.
func (c *Client) DeleteFirewallGroup(ctx context.Context, name string) error {
	path := fmt.Sprintf("/cluster/firewall/groups/%s", url.PathEscape(name))
	return c.do(ctx, "DELETE", path, requestParams{}, nil)
}

// CreateFirewallGroupRule calls POST /cluster/firewall/groups/{group}.
func (c *Client) CreateFirewallGroupRule(ctx context.Context, group string, rule FirewallRule) error {
	path := fmt.Sprintf("/cluster/firewall/groups/%s", url.PathEscape(group))
	return c.do(ctx, "POST", path, requestParams{body: rule}, nil)
}

// DeleteFirewallGroupRule calls DELETE /cluster/firewall/groups/{group}/{pos}.
func (c *Client) DeleteFirewallGroupRule(ctx context.Context, group string, pos int) error {
	path := fmt.Sprintf("/cluster/firewall/groups/%s/%d", url.PathEscape(group), pos)
	return c.do(ctx, "DELETE", path, requestParams{}, nil)
}

// FirewallCompileStatus is one node's pve-firewall compile status (T-502's
// post-apply verification, docs/features/firewall.md §3). See
// internal/pvemock's handleFirewallStatus doc comment: this is a mock-only
// extension, not real PVE's own REST surface — flagged in the T-502 report
// as needing a real internal/host-based implementation and hardware
// validation.
type FirewallCompileStatus struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// OK reports whether s represents a clean compile.
func (s FirewallCompileStatus) OK() bool { return s.Status == "ok" }

// GetFirewallCompileStatus calls GET /nodes/{node}/firewall/status.
func (c *Client) GetFirewallCompileStatus(ctx context.Context, node string) (FirewallCompileStatus, error) {
	var out FirewallCompileStatus
	path := fmt.Sprintf("/nodes/%s/firewall/status", node)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return FirewallCompileStatus{}, err
	}
	return out, nil
}
