package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/fwlog"
)

// FwLogService is the subset of *fwlog.Service the router needs for
// T-505's `GET /firewall/log` (docs/features/firewall.md §4): a filtered
// read of the shared, cluster-merged, rate-capped log buffer
// internal/fwlog.Service.Run continuously fills — see that method's doc
// comment for why REST and the `firewall.log.batch` WS push share exactly
// one buffer rather than two independently-fetched views.
type FwLogService interface {
	TailPage(filter fwlog.Filter, limit int) fwlog.Page
}

// mountFwLogRoutes registers `GET /firewall/log`, netRead-gated like every
// other firewall read route (mountFirewallRoutes). Not yet named in
// docs/api.md; flagged in the T-505 completion report as a doc addition
// made in this change (docs/development.md's definition-of-done #4).
func mountFwLogRoutes(r chi.Router, svc FwLogService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/firewall/log", handleFwLog(svc))
	})
}

const defaultFwLogLimit = 500

// handleFwLog implements `GET /firewall/log?node=&vmid=&direction=&action=&limit=`:
// a filtered snapshot of the merged cluster-wide log buffer (docs/features/
// firewall.md §4's "filterable stream (guest, direction, action, node)").
// Every filter param is optional and ANDed together, mirroring GET
// /audit's convention (docs/api.md).
func handleFwLog(svc FwLogService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		filter := fwlog.Filter{
			Node:      q.Get("node"),
			Direction: q.Get("direction"),
			Action:    q.Get("action"),
		}
		if v := q.Get("vmid"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "vmid must be a positive integer")
				return
			}
			filter.VMID = n
		}

		limit := defaultFwLogLimit
		if v := q.Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "limit must be a positive integer")
				return
			}
			limit = n
		}

		page := svc.TailPage(filter, limit)
		items := make([]fwlog.EntryView, len(page.Items))
		for i, se := range page.Items {
			items[i] = fwlog.ToEntryView(se)
		}
		resp := map[string]any{
			"items":        items,
			"droppedTotal": page.DroppedTotal,
		}
		if len(page.UnavailableNodes) > 0 {
			resp["unavailableNodes"] = page.UnavailableNodes
		}
		writeJSON(w, http.StatusOK, resp)
	}
}
