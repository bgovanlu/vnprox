// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/ceph"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// ceph.go implements docs/api.md's Ceph section (T-1503): a single
// read-only GET /ceph/status route serving the map-layer projection —
// public/cluster network CIDRs plus per-node/per-OSD physical bond
// attribution — internal/ceph.Project computes from live inventory. There
// is no write route anywhere in this file, or anywhere else in this
// codebase, for Ceph — PVE's own Ceph tooling keeps sole ownership of Ceph
// configuration (this task's read-only invariant, internal/ceph's own doc
// comment).

// CephService is the router's seam onto T-1503's Ceph overlay — typically
// cmd/vnproxd's *cephProviderAdapter (cephwire.go).
type CephService interface {
	Overlay(ctx context.Context) (ceph.Overlay, error)
}

// cephOverlayResponse is GET /ceph/status's response body.
type cephOverlayResponse struct {
	PublicNetwork  string             `json:"publicNetwork,omitempty"`
	ClusterNetwork string             `json:"clusterNetwork,omitempty"`
	Nodes          []cephNodeResponse `json:"nodes"`
	OSDs           []cephOSDResponse  `json:"osds"`
}

// cephNodeResponse is one OSD-hosting node's resolved physical path for
// Ceph's public/cluster networks — refs are Ref.String()-encoded, the same
// convention every other Ref-carrying API response in this codebase uses
// (round-trippable through inventory.ParseRef via GET /inventory/{ref}).
// A carrier/riding-on field is omitted (empty string) when unresolved —
// "never a guess", matching ceph.NodeAttribution's own contract.
type cephNodeResponse struct {
	Node            string   `json:"node"`
	PublicCarrier   string   `json:"publicCarrier,omitempty"`
	PublicRidingOn  string   `json:"publicRidingOn,omitempty"`
	ClusterCarrier  string   `json:"clusterCarrier,omitempty"`
	ClusterRidingOn string   `json:"clusterRidingOn,omitempty"`
	PublicPath      []string `json:"publicPath,omitempty"`
	ClusterPath     []string `json:"clusterPath,omitempty"`
	PublicMTU       int      `json:"publicMtu,omitempty"`
	ClusterMTU      int      `json:"clusterMtu,omitempty"`
}

// cephOSDResponse is one OSD plus the bond/NIC ref its node's public/
// cluster traffic rides — "which OSDs ride which bonds", denormalized per
// OSD exactly like ceph.OSDAttribution.
type cephOSDResponse struct {
	Ref         string `json:"ref"`
	Device      string `json:"device,omitempty"`
	Node        string `json:"node"`
	PublicBond  string `json:"publicBond,omitempty"`
	ClusterBond string `json:"clusterBond,omitempty"`
	ID          int    `json:"id"`
	Up          bool   `json:"up"`
	In          bool   `json:"in"`
}

func refString(r inventory.Ref) string {
	if r.IsZero() {
		return ""
	}
	return r.String()
}

func refStrings(refs []inventory.Ref) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.String()
	}
	return out
}

func toCephOverlayResponse(o ceph.Overlay) cephOverlayResponse {
	nodes := make([]cephNodeResponse, len(o.Nodes))
	for i, na := range o.Nodes {
		nodes[i] = cephNodeResponse{
			Node:            na.Node,
			PublicCarrier:   refString(na.PublicCarrier),
			PublicPath:      refStrings(na.PublicPath),
			PublicRidingOn:  refString(na.PublicRidingOn),
			PublicMTU:       na.PublicMTU,
			ClusterCarrier:  refString(na.ClusterCarrier),
			ClusterPath:     refStrings(na.ClusterPath),
			ClusterRidingOn: refString(na.ClusterRidingOn),
			ClusterMTU:      na.ClusterMTU,
		}
	}
	osds := make([]cephOSDResponse, len(o.OSDs))
	for i, oa := range o.OSDs {
		osds[i] = cephOSDResponse{
			Ref: refString(oa.OSD.Ref),
			ID:  oa.OSD.ID, Node: oa.OSD.Node, Device: oa.OSD.Device, Up: oa.OSD.Up, In: oa.OSD.In,
			PublicBond:  refString(oa.PublicBond),
			ClusterBond: refString(oa.ClusterBond),
		}
	}
	return cephOverlayResponse{
		PublicNetwork:  o.PublicNetwork,
		ClusterNetwork: o.ClusterNetwork,
		Nodes:          nodes,
		OSDs:           osds,
	}
}

// mountCephRoutes registers GET /ceph/status (netRead-gated, read-only).
// Nil svc/auth skips mounting the route, the same degraded-mode convention
// every other optional Options field in this package follows.
func mountCephRoutes(r chi.Router, svc CephService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/ceph/status", handleCephStatus(svc))
	})
}

func handleCephStatus(svc CephService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		overlay, err := svc.Overlay(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read Ceph status")
			return
		}
		writeJSON(w, http.StatusOK, toCephOverlayResponse(overlay))
	}
}
