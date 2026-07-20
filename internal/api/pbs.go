package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pbs"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// pbs.go implements T-1206's PBS network awareness surface: the read-only
// GET /pbs route (the inspector's datastore-network sizing hints) plus
// paintPBS, the handler-level decoration that injects pbs-host nodes and
// node->PBS backup-path edges into GET /topology. Both read the same
// internal/pbs.Overlay. There is no write route anywhere in this file, or
// anywhere else in this codebase, for PBS — PVE owns storage.cfg; vnprox's
// PBS awareness is discovery of PVE's own knowledge of itself, never a PBS
// integration (internal/pbs's own doc comment, this task's read-only
// invariant).

// PBSService is the router's read-only seam onto T-1206's PBS overlay —
// typically cmd/vnproxd's *pbsProviderAdapter. nil-safe on the /topology
// route (paintPBS is skipped) like every other optional badge/overlay input.
type PBSService interface {
	PBSOverlay(ctx context.Context) (pbs.Overlay, error)
}

// pbsHostNodeKind / pbsBackupEdgeKind are the topology node/edge kind tokens
// T-1206 adds (docs/data-model.md §1): a pbs-host synthetic node and the
// node->PBS backup-path edge. Kept as string literals matching
// inventory.KindPBSHost so the map and the Ref scheme never disagree.
const (
	pbsHostNodeKind   = string(inventory.KindPBSHost)
	pbsBackupEdgeKind = "backup-path"
	pbsHostBadge      = "pbs"
)

// paintPBS decorates t with T-1206's PBS awareness: one pbs-host node per
// discovered host (cluster-scoped, physical layer — it is backup
// infrastructure the node's physical egress reaches) and one backup-path
// edge per resolved node->host path, drawn from the node's egress carrier to
// the PBS host. Additive, the same "internal/api decorates the pure
// projection" seam paintMgmtStatus/paintFindings use — internal/topology's
// Project stays a pure function of the inventory snapshot, and PBS (read from
// PVE's storage config, not the inventory graph) is composed on top here. A
// path whose egress carrier vnprox could not resolve still contributes its
// host node, but no dangling edge (the honest "host known, path unresolved"
// state). An overlay-read error degrades to "no PBS decoration this request"
// (matching paintMgmtStatus's identical tolerance) rather than failing the
// whole /topology request over a display-only overlay.
func paintPBS(ctx context.Context, t *topology.Topology, svc PBSService) {
	overlay, err := svc.PBSOverlay(ctx)
	if err != nil {
		return
	}
	for _, h := range overlay.Hosts {
		t.Nodes = append(t.Nodes, topology.Node{
			ID:        h.Ref.String(),
			Kind:      pbsHostNodeKind,
			Label:     h.Address,
			Layer:     topology.LayerPhysical,
			NodeGroup: "", // cluster-scoped: PBS storage is shared cluster config
			Status:    topology.StatusOK,
			Badges:    []string{pbsHostBadge},
		})
	}
	for _, p := range overlay.Paths {
		if p.Carrier.IsZero() {
			continue
		}
		t.Edges = append(t.Edges, topology.Edge{
			From:   p.Carrier.String(),
			To:     p.Host.String(),
			Kind:   pbsBackupEdgeKind,
			Status: topology.StatusOK,
			Badges: nil,
		})
	}
}

// pbsOverlayResponse is GET /pbs's response body: the discovered PBS hosts
// and every resolved node->host backup path with its sizing hint.
type pbsOverlayResponse struct {
	Hosts []pbsHostResponse `json:"hosts"`
	Paths []pbsPathResponse `json:"paths"`
}

type pbsHostResponse struct {
	Ref         string   `json:"ref"`
	Address     string   `json:"address"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	Datastores  []string `json:"datastores,omitempty"`
	StorageIDs  []string `json:"storageIds,omitempty"`
	Port        int      `json:"port,omitempty"`
}

type pbsPathResponse struct {
	Node       string           `json:"node"`
	Host       string           `json:"host"`
	Carrier    string           `json:"carrier,omitempty"`
	RidingOn   string           `json:"ridingOn,omitempty"`
	SizingHint string           `json:"sizingHint"`
	Path       []string         `json:"path,omitempty"`
	StorageIDs []string         `json:"storageIds,omitempty"`
	Jobs       []pbsJobResponse `json:"jobs,omitempty"`
	LinkMbps   int              `json:"linkMbps,omitempty"`
	LinkKnown  bool             `json:"linkSpeedKnown"`
}

type pbsJobResponse struct {
	ID       string `json:"id"`
	Storage  string `json:"storage"`
	Schedule string `json:"schedule,omitempty"`
	Guests   int    `json:"guests"`
	All      bool   `json:"all"`
}

func toPBSOverlayResponse(o pbs.Overlay) pbsOverlayResponse {
	hosts := make([]pbsHostResponse, len(o.Hosts))
	for i, h := range o.Hosts {
		hosts[i] = pbsHostResponse{
			Ref:         h.Ref.String(),
			Address:     h.Address,
			Fingerprint: h.Fingerprint,
			Datastores:  h.Datastores,
			StorageIDs:  h.StorageIDs,
			Port:        h.Port,
		}
	}
	paths := make([]pbsPathResponse, len(o.Paths))
	for i, p := range o.Paths {
		jobs := make([]pbsJobResponse, len(p.Jobs))
		for k, j := range p.Jobs {
			jobs[k] = pbsJobResponse{ID: j.ID, Storage: j.Storage, Schedule: j.Schedule, Guests: j.Guests, All: j.All}
		}
		paths[i] = pbsPathResponse{
			Node:       p.Node,
			Host:       p.Host.String(),
			Carrier:    refString(p.Carrier),
			RidingOn:   refString(p.RidingOn),
			SizingHint: p.SizingHint,
			Path:       refStrings(p.Path),
			StorageIDs: p.StorageIDs,
			Jobs:       jobs,
			LinkMbps:   p.LinkMbps,
			LinkKnown:  p.LinkKnown,
		}
	}
	return pbsOverlayResponse{Hosts: hosts, Paths: paths}
}

// refString / refStrings live in ceph.go (same package): a Ref encoded as its
// string, or "" / nil for the zero Ref (an unresolved carrier/riding-on ref is
// omitted, "never a guess").

// mountPBSRoutes registers GET /pbs (netRead-gated, read-only). Nil svc/auth
// skips mounting, the same degraded-mode convention every other optional
// Options field follows.
func mountPBSRoutes(r chi.Router, svc PBSService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/pbs", handlePBSStatus(svc))
	})
}

func handlePBSStatus(svc PBSService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		overlay, err := svc.PBSOverlay(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read PBS status")
			return
		}
		writeJSON(w, http.StatusOK, toPBSOverlayResponse(overlay))
	}
}
