package pve

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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
	var raw map[string]any
	path := fmt.Sprintf("/nodes/%s/%s/%d/config", node, kind, vmid)
	if err := c.do(ctx, "GET", path, requestParams{}, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = stringifyConfigValue(v)
	}
	return out, nil
}

// stringifyConfigValue renders one GET .../config value as text, matching
// PVE's own on-disk config-file representation (every field in
// /etc/pve/{qemu,lxc}-server/*.conf is plain text). Hardware validation
// against a real PVE 9.2.4 node found this endpoint returns a genuine mix
// of JSON types per field on the very same guest (e.g. "cores":4,
// "numa":0 as numbers, "memory":"4096" as a string) rather than the
// all-strings shape this client previously assumed — a gap pvemock's
// fixtures (which always emit strings) never exposed. Every caller of
// GetGuestConfig expects map[string]string, so numbers/bools are
// stringified here rather than pushing the type mix out to every
// consumer.
func stringifyConfigValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "1"
		}
		return "0"
	case nil:
		return ""
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
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

// AgentIface is one NIC reported by a qemu guest's QEMU guest agent, as
// returned by GetGuestAgentInterfaces — T-405's guest-agent-reported-IP
// IPAM enrichment source (docs/features/ipam.md §1).
type AgentIface struct {
	Name         string           `json:"name"`
	HardwareAddr string           `json:"hardware-address,omitempty"`
	IPAddresses  []AgentIPAddress `json:"ip-addresses,omitempty"`
}

// AgentIPAddress is one address reported for an AgentIface.
type AgentIPAddress struct {
	IPAddress     string `json:"ip-address"`
	IPAddressType string `json:"ip-address-type,omitempty"` // ipv4|ipv6
	Prefix        int    `json:"prefix,omitempty"`
}

// GetGuestAgentInterfaces calls
// GET /nodes/{node}/qemu/{vmid}/agent/network-get-interfaces: the QEMU
// guest agent's live in-guest network state, if the agent is installed,
// enabled, and running inside the guest. Real PVE returns a 500
// (agent-not-reachable) for a guest with no agent or the agent not
// running; callers (internal/ipam) treat any error from this method as
// "no agent data available for this guest" rather than surfacing it, since
// that is the overwhelmingly common case, not an operational fault. LXC
// guests have no equivalent route (a container's interfaces are read
// directly from its netns, not a guest agent) — this method is qemu-only.
func (c *Client) GetGuestAgentInterfaces(ctx context.Context, node string, vmid int) ([]AgentIface, error) {
	var wrap struct {
		Result []AgentIface `json:"result"`
	}
	path := fmt.Sprintf("/nodes/%s/qemu/%d/agent/network-get-interfaces", node, vmid)
	if err := c.do(ctx, "GET", path, requestParams{}, &wrap); err != nil {
		return nil, err
	}
	return wrap.Result, nil
}
