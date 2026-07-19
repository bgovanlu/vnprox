package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/mtuprobe"
)

// MTUProbeService is the subset of *mtuprobe.Service the router needs for
// docs/api.md's Path MTU prober section (T-1306): every currently-known
// verified per-link MTU reading.
type MTUProbeService interface {
	Results() []mtuprobe.Result
}

// mtuProbeResultResponse is one GET /mtuprobe/results item — exactly
// docs/api.md's MTUProbeResult shape, mirroring latMeshLinkResponse's own
// field-naming convention for the shared LinkID/Fabric/FromNode/ToNode
// identity.
type mtuProbeResultResponse struct {
	LinkID     string `json:"linkId"`
	Fabric     string `json:"fabric"`
	FromNode   string `json:"fromNode"`
	ToNode     string `json:"toNode"`
	MTU        int    `json:"mtu"`
	At         int64  `json:"at"`
	ProbeCount int    `json:"probeCount"`
}

func toMTUProbeResultResponse(r mtuprobe.Result) mtuProbeResultResponse {
	return mtuProbeResultResponse{
		LinkID: r.LinkID, Fabric: string(r.Fabric), FromNode: r.FromNode, ToNode: r.ToNode,
		MTU: r.MTU, At: r.At, ProbeCount: r.ProbeCount,
	}
}

// mountMTUProbeRoutes registers docs/api.md's Path MTU prober section
// (T-1306): a netRead-gated, node-local-only read (same scope as
// mountLatMeshRoutes — see internal/mtuprobe's package doc comment). A nil
// svc/auth simply skips mounting the route, the same degraded-mode
// convention every other optional Options field follows.
func mountMTUProbeRoutes(r chi.Router, svc MTUProbeService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/mtuprobe/results", handleMTUProbeResults(svc))
	})
}

func handleMTUProbeResults(svc MTUProbeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		results := svc.Results()
		items := make([]mtuProbeResultResponse, len(results))
		for i, res := range results {
			items[i] = toMTUProbeResultResponse(res)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}
