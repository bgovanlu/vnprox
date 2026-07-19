package pvemock

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ceph.go implements T-1503's two read-only Ceph routes: GET
// /cluster/ceph/config (cluster-wide public/cluster network CIDRs) and GET
// /nodes/{node}/ceph/osd (per-node OSD placement). Both serve directly off
// the loaded Fixture — Ceph placement never changes at runtime in this
// mock (no handler anywhere mutates it, matching the read-only invariant
// internal/ceph itself enforces: PVE's own Ceph tooling keeps ownership of
// Ceph configuration, this mock included) — so, unlike network/SDN/
// firewall state, there is no mutable nodeState field to thread through
// NewState: handlers below read srv.state.fixture directly.

type cephConfigWire struct {
	PublicNetwork  string `json:"public_network,omitempty"`
	ClusterNetwork string `json:"cluster_network,omitempty"`
}

// handleCephConfig serves GET /cluster/ceph/config from the fixture's
// top-level Ceph declaration. A fixture with no ceph: block at all
// (Fixture.Ceph's zero value) serves an empty object — "no Ceph installed"
// is a valid, unremarkable cluster state, never an error.
func (srv *Server) handleCephConfig(w http.ResponseWriter, _ *http.Request) {
	writeData(w, http.StatusOK, cephConfigWire(srv.state.fixture.Ceph))
}

type cephOSDWire struct {
	Device string `json:"device,omitempty"`
	ID     int    `json:"osd"`
	Up     int    `json:"up"`
	In     int    `json:"in"`
}

// handleCephOSDs serves GET /nodes/{node}/ceph/osd from node's fixture-
// declared CephOSDs. An unknown node name (no NodeSpec entry at all) serves
// an empty list rather than 404 — mirroring handleNetworkList's own
// tolerant treatment of a node absent from the fixture's nodes: map, since
// Fixture.Validate already rejects a cluster.nodes entry with no
// corresponding nodes: entry, so this path only exists for a genuinely
// unknown node name a test/client makes up.
func (srv *Server) handleCephOSDs(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	ns, ok := srv.state.fixture.Nodes[node]
	if !ok || ns == nil {
		writeData(w, http.StatusOK, []cephOSDWire{})
		return
	}
	out := make([]cephOSDWire, len(ns.CephOSDs))
	for i, o := range ns.CephOSDs {
		out[i] = cephOSDWire{Device: o.Device, ID: o.ID, Up: boolToInt(o.Up), In: boolToInt(o.In)}
	}
	writeData(w, http.StatusOK, out)
}
