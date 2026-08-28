// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// LLDPService is the subset of *topology.Service the router needs for
// T-302's LLDP/ports routes. Declared as an interface (the same seam
// pattern as TopologyService above) so this package's dependency on the
// concrete topology.Service stays small.
type LLDPService interface {
	LLDPNeighbors() []*inventory.LldpNeighbor
	VlanFindings() []topology.VlanFinding
	Ports() []topology.PortRow
}

// lldpNeighborResponse is one GET /lldp item: the canonical LLDP neighbor
// field set (docs/data-model.md §1's LldpNeighbor contract), flattened for
// the API rather than exposing the inventory.LldpNeighbor Go type directly.
type lldpNeighborResponse struct {
	PortIDType    string   `json:"portIdType,omitempty"`
	PortDescr     string   `json:"portDescr,omitempty"`
	LocalIface    string   `json:"localIface"`
	Protocol      string   `json:"protocol"`
	ChassisName   string   `json:"chassisName"`
	ChassisID     string   `json:"chassisId"`
	ChassisIDType string   `json:"chassisIdType,omitempty"`
	PortID        string   `json:"portId"`
	Node          string   `json:"node"`
	SpeedDescr    string   `json:"speedDescr,omitempty"`
	Ref           string   `json:"ref"`
	TaggedVLANs   []int    `json:"taggedVlans,omitempty"`
	MgmtIPs       []string `json:"mgmtIps,omitempty"`
	PVID          int      `json:"pvid,omitempty"`
	SpeedMbps     int      `json:"speedMbps,omitempty"`
	TTL           int      `json:"ttl,omitempty"`
	LastSeen      int64    `json:"lastSeen,omitempty"`
}

func toLLDPNeighborResponse(n *inventory.LldpNeighbor) lldpNeighborResponse {
	return lldpNeighborResponse{
		Ref: n.GetRef().String(), Node: n.Node, LocalIface: n.LocalIface,
		Protocol: n.Protocol, ChassisName: n.ChassisName, ChassisID: n.ChassisID,
		ChassisIDType: n.ChassisIDType, PortID: n.PortID, PortIDType: n.PortIDType,
		PortDescr: n.PortDescr, MgmtIPs: n.MgmtIPs, PVID: n.VLAN, TaggedVLANs: n.TaggedVLANs,
		SpeedMbps: n.SpeedMbps, SpeedDescr: n.SpeedDescr, TTL: n.TTL, LastSeen: n.LastSeen,
	}
}

// mountLLDPRoutes registers docs/api.md's `GET /lldp` plus T-302's
// additional read views: the VLAN cross-check findings and the flat ports
// table (JSON and CSV export) — docs/features/lldp-discovery.md §2's "Ports
// view: a flat table ... exportable CSV". All are netRead-gated reads,
// mounted the same way mountTopologyRoutes is.
//
// The ports/vlan-check routes are not yet named in docs/api.md; flagged in
// the T-302 completion report as a doc addition made in this change
// (docs/development.md's "definition of done" #4 allows this when the doc
// is updated in the same change).
func mountLLDPRoutes(r chi.Router, svc LLDPService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/lldp", handleLLDPNeighbors(svc))
		r.Get("/lldp/vlan-check", handleVlanCheck(svc))
		r.Get("/ports", handlePorts(svc))
	})
}

func handleLLDPNeighbors(svc LLDPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		neighbors := svc.LLDPNeighbors()
		items := make([]lldpNeighborResponse, len(neighbors))
		for i, n := range neighbors {
			items[i] = toLLDPNeighborResponse(n)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func handleVlanCheck(svc LLDPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"items": svc.VlanFindings()})
	}
}

// handlePorts serves the ports table as JSON by default, or as CSV when
// ?format=csv is given (docs/features/lldp-discovery.md §2: "exportable
// CSV").
func handlePorts(svc LLDPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows := svc.Ports()
		if r.URL.Query().Get("format") == "csv" {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="ports.csv"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(topology.PortsCSV(rows)))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": rows})
	}
}
