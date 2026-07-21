package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/failsim"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// failsim.go implements docs/api.md's Failure-impact-simulation section
// (T-1604): two read-only routes — GET /failsim/spof-score (the standing
// single-point-of-failure dashboard tile) and POST
// /changesets/{id}/preflight-impact (the impact of a changeset's touched
// entities, the hook T-1103's scheduler consults). Both are pure computations
// over the live inventory snapshot; nothing here mutates or persists.

// FailsimService is the router's seam onto internal/failsim — typically
// cmd/vnproxd's failsim adapter, which gathers the inventory snapshot plus the
// corosync/Ceph/tunnel side-tables and calls failsim's pure functions.
type FailsimService interface {
	// SPOFScore returns the SPOF inventory + overall resilience score, and
	// the snapshot's generated-at timestamp for the response envelope.
	SPOFScore(ctx context.Context) (failsim.SPOFScore, time.Time, error)
	// PreflightImpactForChangeset computes the worst failure impact among a
	// changeset's touched entities. store.ErrNotFound => 404.
	PreflightImpactForChangeset(ctx context.Context, changesetID string) (failsim.Impact, error)
}

// impactResponse is the wire shape of an Impact — refs are Ref.String()
// encoded, the same convention every Ref-carrying response uses. NotEvaluated
// is always emitted (never omitted) so a consumer can distinguish "checked,
// nothing there" from an absent field.
type impactResponse struct {
	Target             string   `json:"target"`
	Severity           string   `json:"severity"`
	DisconnectedGuests []string `json:"disconnectedGuests"`
	StrandedVlans      []string `json:"strandedVlans"`
	MgmtPathLoss       []string `json:"mgmtPathLoss"`
	NotEvaluated       []string `json:"notEvaluated"`
	QuorumRisk         bool     `json:"quorumRisk"`
	CephRisk           bool     `json:"cephRisk"`
}

func toImpactResponse(im failsim.Impact) impactResponse {
	return impactResponse{
		Target:             refString(im.Target),
		Severity:           im.Severity,
		DisconnectedGuests: refStringsOrEmpty(im.DisconnectedGuests),
		StrandedVlans:      refStringsOrEmpty(im.StrandedVlans),
		MgmtPathLoss:       strsOrEmpty(im.MgmtPathLoss),
		NotEvaluated:       strsOrEmpty(im.NotEvaluated),
		QuorumRisk:         im.QuorumRisk,
		CephRisk:           im.CephRisk,
	}
}

// spofScoreResponse is GET /failsim/spof-score's body.
type spofScoreResponse struct {
	GeneratedAt string              `json:"generatedAt"`
	Entries     []spofEntryResponse `json:"entries"`
	Score       int                 `json:"score"`
}

type spofEntryResponse struct {
	Ref    string         `json:"ref"`
	Impact impactResponse `json:"impact"`
}

func refStringsOrEmpty(refs []inventory.Ref) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.String())
	}
	return out
}

func strsOrEmpty(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

// mountFailsimRoutes registers the two failure-impact routes (netRead-gated,
// read-only). Nil svc/auth skips mounting, the standard degraded-mode
// convention.
func mountFailsimRoutes(r chi.Router, svc FailsimService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/failsim/spof-score", handleSPOFScore(svc))
		r.Post("/changesets/{id}/preflight-impact", handlePreflightImpact(svc))
	})
}

func handleSPOFScore(svc FailsimService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, generatedAt, err := svc.SPOFScore(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not compute SPOF score")
			return
		}
		entries := make([]spofEntryResponse, 0, len(res.Entries))
		for _, e := range res.Entries {
			entries = append(entries, spofEntryResponse{Ref: e.Ref.String(), Impact: toImpactResponse(e.Impact)})
		}
		writeJSON(w, http.StatusOK, spofScoreResponse{
			Score:       res.Score,
			Entries:     entries,
			GeneratedAt: generatedAt.UTC().Format(time.RFC3339),
		})
	}
}

func handlePreflightImpact(svc FailsimService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		im, err := svc.PreflightImpactForChangeset(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "changeset not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not compute pre-flight impact")
			return
		}
		writeJSON(w, http.StatusOK, toImpactResponse(im))
	}
}
