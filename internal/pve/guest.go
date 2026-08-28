// SPDX-License-Identifier: Apache-2.0

package pve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
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

// ExecResult is one guest-agent exec-status poll's decoded state (T-802,
// docs/features/firewall.md §5's "verify live" P2 item): whether the
// in-guest command has exited yet and, once it has, its exit code and
// captured stdout/stderr. Mirrors real PVE's
// GET .../agent/exec-status response shape.
type ExecResult struct {
	OutData      string
	ErrData      string
	ExitCode     int
	Signal       int
	Exited       bool
	OutTruncated bool
	ErrTruncated bool
}

// execStatusWire is ExecResult's real wire shape: PVE reports exited/
// out-data-trunc/err-data-trunc as 0|1 ints, not JSON booleans (the same
// numeric-boolean convention netIfaceWire and internal/pvemock's pvebool.go
// document elsewhere in this codebase — PVE's API is consistently
// inconsistent about bool encoding across endpoints).
type execStatusWire struct {
	OutData      string `json:"out-data,omitempty"`
	ErrData      string `json:"err-data,omitempty"`
	ExitCode     int    `json:"exitcode,omitempty"`
	Signal       int    `json:"signal,omitempty"`
	Exited       int    `json:"exited"`
	OutTruncated int    `json:"out-data-trunc,omitempty"`
	ErrTruncated int    `json:"err-data-trunc,omitempty"`
}

// AgentExec calls POST /nodes/{node}/qemu/{vmid}/agent/exec: runs command
// (argv form — PVE's guest agent exec accepts either a single string or an
// array; this client always sends the array form, avoiding any in-guest
// shell-quoting ambiguity) inside the guest via the QEMU guest agent,
// returning the pid AgentExecStatus polls. qemu-only, matching
// GetGuestAgentInterfaces' precedent above (no LXC guest-agent equivalent —
// a container has no QEMU guest agent to exec through). Real PVE returns a
// 500 (mapped to *ErrPVEServer by this client's do) when the target guest's
// agent isn't installed/running/reachable — T-802's internal/probe engine
// treats that as the probe's own answer ("could not run": docs/features/
// firewall.md §5's honesty contract), not a transport fault to retry.
func (c *Client) AgentExec(ctx context.Context, node string, vmid int, command []string) (int, error) {
	var out struct {
		PID int `json:"pid"`
	}
	path := fmt.Sprintf("/nodes/%s/qemu/%d/agent/exec", node, vmid)
	body := map[string]any{"command": command}
	if err := c.do(ctx, "POST", path, requestParams{body: body}, &out); err != nil {
		return 0, err
	}
	return out.PID, nil
}

// AgentPing calls POST /nodes/{node}/qemu/{vmid}/agent/ping: the QEMU guest
// agent's transport-level "guest-ping" liveness check — succeeds iff the
// agent channel itself is up, independent of anything about the guest's
// own network/OS state. This is the honest signal T-806's "Verify live"
// button gating needs ("is there a guest agent to probe through at all")
// that GetGuestAgentInterfaces cannot supply on its own: a guest can have a
// perfectly reachable agent yet report zero interfaces (no network
// configured inside the guest), so an empty interfaces result does not by
// itself mean "agent unreachable" for this purpose. qemu-only, same
// precedent as AgentExec/GetGuestAgentInterfaces; a non-nil error means
// unreachable (mirrors AgentExec's own "500 -> the answer is no"
// contract — real PVE returns the same failure mode for every agent/*
// route when the guest agent isn't installed/running/reachable).
func (c *Client) AgentPing(ctx context.Context, node string, vmid int) error {
	path := fmt.Sprintf("/nodes/%s/qemu/%d/agent/ping", node, vmid)
	return c.do(ctx, "POST", path, requestParams{}, nil)
}

// AgentExecStatus calls GET /nodes/{node}/qemu/{vmid}/agent/exec-status?pid=
// once: the caller (internal/probe) is responsible for polling this to a
// bounded deadline until Exited is true. qemu-only, same precedent as
// AgentExec/GetGuestAgentInterfaces.
func (c *Client) AgentExecStatus(ctx context.Context, node string, vmid int, pid int) (ExecResult, error) {
	var wire execStatusWire
	path := fmt.Sprintf("/nodes/%s/qemu/%d/agent/exec-status", node, vmid)
	params := requestParams{query: url.Values{"pid": {strconv.Itoa(pid)}}}
	if err := c.do(ctx, "GET", path, params, &wire); err != nil {
		return ExecResult{}, err
	}
	return ExecResult{
		Exited: wire.Exited != 0, ExitCode: wire.ExitCode, Signal: wire.Signal,
		OutData: wire.OutData, ErrData: wire.ErrData,
		OutTruncated: wire.OutTruncated != 0, ErrTruncated: wire.ErrTruncated != 0,
	}, nil
}
