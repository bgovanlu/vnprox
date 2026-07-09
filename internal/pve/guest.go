package pve

import (
	"context"
	"fmt"
	"strings"
)

// ListGuests returns all qemu or lxc guests cluster-wide, derived from GET
// /cluster/resources filtered by kind.
//
// Real PVE also exposes per-node list endpoints (GET
// /nodes/{node}/qemu, GET /nodes/{node}/lxc) with a quicker per-node
// summary; internal/pvemock (T-004) does not implement those routes (only
// per-guest .../config), so this client uses the cluster-wide resources
// listing, which the mock does implement and which already covers every
// node. See this package's completion report for this documented gap.
func (c *Client) ListGuests(ctx context.Context, kind GuestKind) ([]ClusterResource, error) {
	all, err := c.ClusterResources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ClusterResource, 0, len(all))
	for _, r := range all {
		if r.Type == string(kind) {
			out = append(out, r)
		}
	}
	return out, nil
}

// GetGuestConfig calls GET /nodes/{node}/{qemu,lxc}/{vmid}/config: the
// guest's flat key/value config (e.g. "net0":
// "virtio=AA:BB:...,bridge=vmbr0,tag=100").
func (c *Client) GetGuestConfig(ctx context.Context, node string, kind GuestKind, vmid int) (map[string]string, error) {
	var out map[string]string
	path := fmt.Sprintf("/nodes/%s/%s/%d/config", node, kind, vmid)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateGuestConfig calls PUT /nodes/{node}/{qemu,lxc}/{vmid}/config: a
// partial (merge, not replace) edit of the guest's config, e.g. to attach
// a NIC to a different bridge/VLAN.
func (c *Client) UpdateGuestConfig(ctx context.Context, node string, kind GuestKind, vmid int, update GuestConfigUpdate) error {
	path := fmt.Sprintf("/nodes/%s/%s/%d/config", node, kind, vmid)
	body := map[string]string{}
	for k, v := range update.Set {
		body[k] = v
	}
	if len(update.Delete) > 0 {
		body["delete"] = strings.Join(update.Delete, ",")
	}
	return c.do(ctx, "PUT", path, requestParams{body: body}, nil)
}
