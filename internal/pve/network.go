package pve

import (
	"context"
	"fmt"
	"strings"
)

// ListNodeNetwork calls GET /nodes/{node}/network: the node's full
// interfaces list, live config annotated with any staged-but-unapplied
// ("pending") edits.
func (c *Client) ListNodeNetwork(ctx context.Context, node string) ([]NetworkInterface, error) {
	var out []NetworkInterface
	path := fmt.Sprintf("/nodes/%s/network", node)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetNodeNetworkInterface calls GET /nodes/{node}/network/{iface} for a
// single interface.
func (c *Client) GetNodeNetworkInterface(ctx context.Context, node, iface string) (*NetworkInterface, error) {
	var out NetworkInterface
	path := fmt.Sprintf("/nodes/%s/network/%s", node, iface)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateNodeNetworkInterface calls PUT /nodes/{node}/network/{iface}: a
// partial (merge, not replace) edit staged against the node's pending
// network config. It does not take effect until ReloadNodeNetwork is
// called (PVE's staged-apply model; docs/architecture.md §4).
func (c *Client) UpdateNodeNetworkInterface(ctx context.Context, node, iface string, update NetworkInterfaceUpdate) error {
	path := fmt.Sprintf("/nodes/%s/network/%s", node, iface)
	body := networkUpdateBody(update)
	return c.do(ctx, "PUT", path, requestParams{body: body}, nil)
}

// networkUpdateBody flattens a NetworkInterfaceUpdate into the
// param-name-keyed map PVE's network API (and internal/pvemock's
// applyNetIfaceField) expects, including only fields the caller actually
// set and a comma-joined "delete" key for cleared fields.
func networkUpdateBody(u NetworkInterfaceUpdate) map[string]any {
	body := map[string]any{}
	if u.Type != nil {
		body["type"] = *u.Type
	}
	if u.Method != nil {
		body["method"] = *u.Method
	}
	if u.Address != nil {
		body["address"] = *u.Address
	}
	if u.Gateway != nil {
		body["gateway"] = *u.Gateway
	}
	if u.Comments != nil {
		body["comments"] = *u.Comments
	}
	if u.BridgePorts != nil {
		body["bridge_ports"] = *u.BridgePorts
	}
	if u.VlanRawDevice != nil {
		body["vlan_raw_device"] = *u.VlanRawDevice
	}
	if u.Slaves != nil {
		body["slaves"] = *u.Slaves
	}
	if u.BondMode != nil {
		body["bond_mode"] = *u.BondMode
	}
	if u.MTU != nil {
		body["mtu"] = *u.MTU
	}
	if u.VlanID != nil {
		body["vlan_id"] = *u.VlanID
	}
	if u.BridgeVlanAware != nil {
		body["bridge_vlan_aware"] = *u.BridgeVlanAware
	}
	if u.Autostart != nil {
		body["autostart"] = *u.Autostart
	}
	if len(u.Delete) > 0 {
		body["delete"] = strings.Join(u.Delete, ",")
	}
	return body
}

// ReloadNodeNetwork calls PUT /nodes/{node}/network (no iface segment):
// applies every staged edit on the node via an async task (ifupdown2
// reload in real PVE). It returns the task's UPID immediately; poll it
// with GetTask or WaitTask.
func (c *Client) ReloadNodeNetwork(ctx context.Context, node string) (string, error) {
	var upid string
	path := fmt.Sprintf("/nodes/%s/network", node)
	if err := c.do(ctx, "PUT", path, requestParams{}, &upid); err != nil {
		return "", err
	}
	return upid, nil
}
