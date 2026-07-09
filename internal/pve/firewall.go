package pve

import (
	"context"
	"fmt"
)

// ListFirewallRules calls GET {scope}/rules.
func (c *Client) ListFirewallRules(ctx context.Context, scope FirewallScope) ([]FirewallRule, error) {
	var out []FirewallRule
	if err := c.do(ctx, "GET", scope.prefix+"/rules", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetFirewallRule calls GET {scope}/rules/{pos}.
func (c *Client) GetFirewallRule(ctx context.Context, scope FirewallScope, pos int) (*FirewallRule, error) {
	var out FirewallRule
	path := fmt.Sprintf("%s/rules/%d", scope.prefix, pos)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetFirewallOptions calls GET {scope}/options: the ruleset-level
// enable/policy state.
func (c *Client) GetFirewallOptions(ctx context.Context, scope FirewallScope) (*FirewallOptions, error) {
	var out FirewallOptions
	if err := c.do(ctx, "GET", scope.prefix+"/options", requestParams{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListFirewallAliases calls GET {scope}/aliases.
func (c *Client) ListFirewallAliases(ctx context.Context, scope FirewallScope) ([]FirewallAlias, error) {
	var out []FirewallAlias
	if err := c.do(ctx, "GET", scope.prefix+"/aliases", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetFirewallAlias calls GET {scope}/aliases/{name}.
func (c *Client) GetFirewallAlias(ctx context.Context, scope FirewallScope, name string) (*FirewallAlias, error) {
	var out FirewallAlias
	path := fmt.Sprintf("%s/aliases/%s", scope.prefix, name)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListFirewallIPSets calls GET {scope}/ipset: ipset names + comments
// (without entries — see ListFirewallIPSetEntries).
func (c *Client) ListFirewallIPSets(ctx context.Context, scope FirewallScope) ([]FirewallIPSetSummary, error) {
	var out []FirewallIPSetSummary
	if err := c.do(ctx, "GET", scope.prefix+"/ipset", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListFirewallIPSetEntries calls GET {scope}/ipset/{name}: the CIDR
// entries of one ipset.
func (c *Client) ListFirewallIPSetEntries(ctx context.Context, scope FirewallScope, name string) ([]FirewallIPSetEntry, error) {
	var out []FirewallIPSetEntry
	path := fmt.Sprintf("%s/ipset/%s", scope.prefix, name)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListFirewallGroups calls GET /cluster/firewall/groups: the cluster-scope
// list of reusable security groups (a cluster-only concept in real PVE,
// referenced by rules at any scope).
func (c *Client) ListFirewallGroups(ctx context.Context) ([]FirewallGroupSummary, error) {
	var out []FirewallGroupSummary
	if err := c.do(ctx, "GET", "/cluster/firewall/groups", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetFirewallGroupRules calls GET /cluster/firewall/groups/{group}: the
// ordered rule list inside one security group.
func (c *Client) GetFirewallGroupRules(ctx context.Context, group string) ([]FirewallRule, error) {
	var out []FirewallRule
	path := fmt.Sprintf("/cluster/firewall/groups/%s", group)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}
