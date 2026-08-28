// SPDX-License-Identifier: Apache-2.0

package pve

import (
	"context"
	"fmt"
)

// ceph.go: read-only access to PVE's own knowledge of its Ceph cluster
// (T-1503, docs/features/topology.md §1/§6) — the same "read the owning
// system's own knowledge of itself, add zero new credentials" boundary
// T-1206's PBS awareness pattern establishes and this task's card cites
// explicitly. PVE's own `pveceph`/GUI tooling keeps ownership of Ceph
// configuration; every method here is a plain GET against PVE's existing
// API surface (the same *Client every other read-only package in this
// codebase already uses), never a second, Ceph-specific API client and
// never a write. See this package's own doc comment and internal/ceph's
// package doc comment for the read-only invariant's full statement.
//
// Real PVE's exact `GET /cluster/ceph/config` / `GET /nodes/{node}/ceph/osd`
// response shapes are not independently verified against real hardware in
// this environment (docs/development.md: "you do not have a live Proxmox
// cluster, develop against internal/pvemock fixtures") — the wire shapes
// below are this task's best-effort modeling of PVE's documented API
// surface, exercised end-to-end against internal/pvemock's fixture-driven
// implementation of the same two routes. Flagged in
// planning/reports/needs-hardware-validation.md.

// CephConfig is GET /cluster/ceph/config's public/cluster network
// declaration — the two CIDRs T-1503's internal/ceph package registers with
// T-1504's flow.Classifier and projects onto the map. Either field may be
// empty (a cluster with no Ceph configured at all, or with only one network
// declared — real Ceph allows cluster_network to default to public_network,
// which this package does not infer on the caller's behalf: an empty
// ClusterNetwork here means "PVE reported none", never a guessed fallback).
type CephConfig struct {
	PublicNetwork  string `json:"public_network,omitempty"`
	ClusterNetwork string `json:"cluster_network,omitempty"`
}

// CephConfig calls GET /cluster/ceph/config: the cluster-wide ceph.conf
// [global] section's public_network/cluster_network declaration, the same
// values `pveceph init`/the PVE GUI's Ceph install wizard write and PVE's
// own Ceph tooling continues to own. Returns a zero CephConfig (both fields
// empty), not an error, when the cluster has no Ceph installed at all —
// internal/pvemock's fixture-driven mock and (per real PVE's documented
// behavior) the real endpoint both return an empty object in that case.
func (c *Client) CephConfig(ctx context.Context) (CephConfig, error) {
	var out CephConfig
	if err := c.do(ctx, "GET", "/cluster/ceph/config", requestParams{}, &out); err != nil {
		return CephConfig{}, fmt.Errorf("pve: reading Ceph config: %w", err)
	}
	return out, nil
}

// cephOSDWire mirrors GET /nodes/{node}/ceph/osd's per-row wire shape (PVE's
// numeric-boolean convention for up/in — see pvebool.go).
type cephOSDWire struct {
	Device string  `json:"device,omitempty"`
	ID     int     `json:"osd"`
	Up     pveBool `json:"up"`
	In     pveBool `json:"in"`
}

// CephOSD is one OSD PVE reports node as hosting, converted from
// cephOSDWire's numeric-boolean wire form to ergonomic Go types. Node is
// set by the caller (the endpoint itself is already node-scoped, so the
// wire row carries no node field) — internal/ceph.Discover fans this call
// out across every cluster node and stamps Node itself.
type CephOSD struct {
	Device string
	Node   string
	ID     int
	Up     bool
	In     bool
}

// CephOSDs calls GET /nodes/{node}/ceph/osd: the OSDs PVE reports as hosted
// on node, per-OSD up/in status and backing device. Returns an empty slice,
// not an error, for a node running no OSDs (including a node with no Ceph
// installed at all) — the same "absence is not failure" convention
// CephConfig's zero-value return follows.
func (c *Client) CephOSDs(ctx context.Context, node string) ([]CephOSD, error) {
	var wire []cephOSDWire
	path := fmt.Sprintf("/nodes/%s/ceph/osd", node)
	if err := c.do(ctx, "GET", path, requestParams{}, &wire); err != nil {
		return nil, fmt.Errorf("pve: reading Ceph OSDs for node %s: %w", node, err)
	}
	out := make([]CephOSD, len(wire))
	for i, w := range wire {
		out[i] = CephOSD{Device: w.Device, Node: node, ID: w.ID, Up: bool(w.Up), In: bool(w.In)}
	}
	return out, nil
}
