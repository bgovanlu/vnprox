package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/fwlog"
)

// FwLogService is the subset of *fwlog.Service the router needs for
// T-505's `GET /firewall/log` (docs/features/firewall.md §4) and T-1006's
// `GET /firewall/analytics`: a filtered read of the shared, cluster-merged,
// rate-capped log buffer internal/fwlog.Service.Run continuously fills —
// see that method's doc comment for why REST and the `firewall.log.batch`
// WS push share exactly one buffer rather than two independently-fetched
// views — plus T-1006's read-only aggregation over that same buffer.
type FwLogService interface {
	TailPage(filter fwlog.Filter, limit int) fwlog.Page
	Analytics(now time.Time, window time.Duration, topN int) fwlog.Analytics
}

// mountFwLogRoutes registers `GET /firewall/log` and `GET
// /firewall/analytics`, netRead-gated like every other firewall read route
// (mountFirewallRoutes). Not yet named in docs/api.md when T-505 first
// shipped `/firewall/log`; both routes are now documented in
// docs/api.md's Firewall section per docs/development.md's
// definition-of-done #4.
func mountFwLogRoutes(r chi.Router, svc FwLogService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/firewall/log", handleFwLog(svc))
		r.Get("/firewall/analytics", handleFwAnalytics(svc))
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

// --- GET /firewall/analytics?scope=&ref=&windowHours= (T-1006) -------------

// ruleHitCountView is one entry of fwAnalyticsResponse.HitCounts.
type ruleHitCountView struct {
	Rule       fwlog.RuleRefView `json:"rule"`
	Hits       int               `json:"hits"`
	LastSeenAt int64             `json:"lastSeenAt,omitempty"`
}

// endpointCountView is one entry of topBlockedView.Sources/Destinations.
type endpointCountView struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type topBlockedView struct {
	Sources      []endpointCountView `json:"sources"`
	Destinations []endpointCountView `json:"destinations"`
}

// unusedRuleView is one entry of fwAnalyticsResponse.UnusedRules.
// DaysSinceLastHit mirrors fwlog.UnusedRule's honest "-1 means never
// observed in the retained buffer" sentinel — never fabricated as 0 or the
// window length.
type unusedRuleView struct {
	Rule             fwlog.RuleRefView `json:"rule"`
	DaysSinceLastHit int               `json:"daysSinceLastHit"`
}

type fwAnalyticsResponse struct {
	HitCounts   []ruleHitCountView `json:"hitCounts"`
	TopBlocked  topBlockedView     `json:"topBlocked"`
	UnusedRules []unusedRuleView   `json:"unusedRules"`
}

func toRuleHitCountView(h fwlog.RuleHitCount) ruleHitCountView {
	v := ruleHitCountView{Rule: fwlog.ToRuleRefView(h.Rule), Hits: h.Hits}
	if !h.LastSeenAt.IsZero() {
		v.LastSeenAt = h.LastSeenAt.Unix()
	}
	return v
}

func toEndpointCountViews(cs []fwlog.EndpointCount) []endpointCountView {
	out := make([]endpointCountView, len(cs))
	for i, c := range cs {
		out[i] = endpointCountView{Value: c.Value, Count: c.Count}
	}
	return out
}

func toUnusedRuleView(u fwlog.UnusedRule) unusedRuleView {
	return unusedRuleView{Rule: fwlog.ToRuleRefView(u.Rule), DaysSinceLastHit: u.DaysSinceLastHit}
}

// fwAnalyticsGuestScoped reports whether v's rule belongs to the guest
// named by ref (docs/api.md: "scope=guest with ?ref=<guest ref> narrows
// every facet to that one guest").
func fwAnalyticsGuestScoped(guestRef, ref string) bool { return guestRef == ref }

// handleFwAnalytics implements `GET /firewall/analytics?scope=&ref=&windowHours=`
// (T-1006, docs/features/firewall.md §4): T-1006's read-only aggregation
// over the same shared, cluster-merged log buffer GET /firewall/log
// serves — per-rule hit counts, top-N blocked sources/destinations, and an
// unused-rule report, computed by internal/fwlog.Analyze (re-correlating
// each buffered entry against the CURRENT resolved ruleset, not any
// entry's own ingestion-time-cached correlation — see that function's doc
// comment). `windowHours` defaults to fwlog.DefaultAnalyticsWindow (24h)
// and governs hitCounts/topBlocked/unusedRules uniformly, matching
// fwlog.Analyze's own single-window contract. `scope=guest&ref=<guest ref>`
// narrows hitCounts/unusedRules to that one guest (topBlocked is left
// cluster-wide regardless of scope — a blocked source/destination isn't a
// per-guest concept the same way a rule is).
func handleFwAnalytics(svc FwLogService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		window := fwlog.DefaultAnalyticsWindow
		if v := q.Get("windowHours"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "windowHours must be a positive integer")
				return
			}
			window = time.Duration(n) * time.Hour
		}

		scope := q.Get("scope")
		ref := q.Get("ref")
		if scope == "guest" && ref == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "ref is required when scope=guest")
			return
		}

		analytics := svc.Analytics(time.Now(), window, fwlog.DefaultTopN)

		resp := fwAnalyticsResponse{
			TopBlocked:  topBlockedView{Sources: toEndpointCountViews(analytics.TopBlocked.Sources), Destinations: toEndpointCountViews(analytics.TopBlocked.Destinations)},
			HitCounts:   []ruleHitCountView{},
			UnusedRules: []unusedRuleView{},
		}
		for _, h := range analytics.HitCounts {
			if scope == "guest" && !fwAnalyticsGuestScoped(h.Rule.GuestRef, ref) {
				continue
			}
			resp.HitCounts = append(resp.HitCounts, toRuleHitCountView(h))
		}
		for _, u := range analytics.UnusedRules {
			if scope == "guest" && !fwAnalyticsGuestScoped(u.Rule.GuestRef, ref) {
				continue
			}
			resp.UnusedRules = append(resp.UnusedRules, toUnusedRuleView(u))
		}

		writeJSON(w, http.StatusOK, resp)
	}
}
