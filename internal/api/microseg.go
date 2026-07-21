package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/microseg"
	"github.com/bgovanlu/vnprox/internal/store"
)

// microseg.go implements docs/api.md's Microsegmentation section (T-1602): two
// read-only synthesis routes — POST /microseg/propose (compute the minimal
// covering-set firewall policy for a guest from its observed flows) and POST
// /microseg/dry-run (replay a corpus against that policy and report
// would-have-blocked flows). Both are netRead-gated: they only READ flows and
// firewall state and COMPUTE a proposal; nothing here mutates or stages. The
// proposal's staged ops are returned for T-1603's review UI to hand into the
// ordinary ChangesetDrawer — the planner itself never applies.

// MicrosegService is the router's seam onto internal/microseg — typically
// cmd/vnproxd's microseg adapter, which gathers the guest's observed flow
// corpus, its learned baseline, and its existing firewall view, then calls the
// planner's pure functions.
type MicrosegService interface {
	// Propose computes the minimal-covering-set policy for guestRef and the
	// changeset ops that would stage it. A malformed/unknown guest ref returns
	// an error the handler maps to 400/404.
	Propose(ctx context.Context, guestRef string) (microseg.Proposal, []change.Op, error)
	// DryRun computes the proposal for guestRef and replays a corpus against it:
	// the training window (heldOut=false) or a more recent held-out window
	// (heldOut=true).
	DryRun(ctx context.Context, guestRef string, heldOut bool) (microseg.Proposal, microseg.Report, error)
}

// ErrMicrosegBadRef signals a malformed/empty guest ref (=> 400). The adapter
// returns store.ErrNotFound for a well-formed ref with no observable flows/guest
// (=> 404).
var ErrMicrosegBadRef = errors.New("microseg: invalid guest ref")

type microsegProposeRequest struct {
	GuestRef string `json:"guestRef"`
}

type microsegDryRunRequest struct {
	GuestRef string `json:"guestRef"`
	HeldOut  bool   `json:"heldOut,omitempty"`
}

// proposalView is POST /microseg/propose's body: the proposed rules, the
// honesty fields (coverage percentage + uncovered-flow count, never rounded to
// "everything"), the auditing counters, and the ready-to-stage changeset ops.
type proposalView struct {
	GuestRef              string      `json:"guestRef"`
	RulesetRef            string      `json:"rulesetRef"`
	Directions            []string    `json:"directions"`
	Rules                 []ruleView  `json:"rules"`
	StagedOps             []change.Op `json:"stagedOps"`
	CoveragePct           float64     `json:"coveragePct"`
	ObservedGoodBytes     int64       `json:"observedGoodBytes"`
	CoveredBytes          int64       `json:"coveredBytes"`
	ObservedGoodFlowCount int         `json:"observedGoodFlowCount"`
	UncoveredFlowCount    int         `json:"uncoveredFlowCount"`
	ExcludedAnomalyFlows  int         `json:"excludedAnomalyFlows"`
	AlreadyCoveredGroups  int         `json:"alreadyCoveredGroups"`
}

func toProposalView(p microseg.Proposal, ops []change.Op) proposalView {
	return proposalView{
		GuestRef:              p.Subject.GuestRef.String(),
		RulesetRef:            refString(p.Subject.RulesetRef),
		Directions:            strsOrEmpty(p.Directions),
		Rules:                 toRuleViews(p.Rules),
		StagedOps:             opsOrEmpty(ops),
		CoveragePct:           p.CoveragePct,
		ObservedGoodBytes:     p.ObservedGoodBytes,
		CoveredBytes:          p.CoveredBytes,
		ObservedGoodFlowCount: p.ObservedGoodFlowCount,
		UncoveredFlowCount:    p.UncoveredFlowCount,
		ExcludedAnomalyFlows:  p.ExcludedAnomalyFlows,
		AlreadyCoveredGroups:  p.AlreadyCoveredGroups,
	}
}

// dryRunView is POST /microseg/dry-run's body. cannotDetermine is emitted
// explicitly (never merged into wouldAllow): a flow the evaluator could not
// prove permitted is surfaced loudly, upholding the simulator's honesty
// contract. All four buckets serialize as [] not null so a consumer never
// confuses "checked, none" with "absent".
type dryRunView struct {
	GuestRef        string             `json:"guestRef"`
	WouldAllow      []microseg.FlowRef `json:"wouldAllow"`
	WouldBlock      []microseg.FlowRef `json:"wouldBlock"`
	CannotDetermine []microseg.FlowRef `json:"cannotDetermine"`
	Ungoverned      []microseg.FlowRef `json:"ungoverned"`
	CoveragePct     float64            `json:"coveragePct"`
}

func toDryRunView(guestRef string, r microseg.Report) dryRunView {
	return dryRunView{
		GuestRef:        guestRef,
		WouldAllow:      flowRefsOrEmpty(r.WouldAllow),
		WouldBlock:      flowRefsOrEmpty(r.WouldBlock),
		CannotDetermine: flowRefsOrEmpty(r.CannotDetermine),
		Ungoverned:      flowRefsOrEmpty(r.Ungoverned),
		CoveragePct:     r.CoveragePct,
	}
}

func flowRefsOrEmpty(fs []microseg.FlowRef) []microseg.FlowRef {
	if fs == nil {
		return []microseg.FlowRef{}
	}
	return fs
}

func opsOrEmpty(ops []change.Op) []change.Op {
	if ops == nil {
		return []change.Op{}
	}
	return ops
}

// mountMicrosegRoutes registers the two microsegmentation routes (netRead-gated,
// read-only synthesis). Nil svc/auth skips mounting, the standard degraded-mode
// convention.
func mountMicrosegRoutes(r chi.Router, svc MicrosegService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Post("/microseg/propose", handleMicrosegPropose(svc))
		r.Post("/microseg/dry-run", handleMicrosegDryRun(svc))
	})
}

func handleMicrosegPropose(svc MicrosegService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req microsegProposeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.GuestRef == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "guestRef is required")
			return
		}
		prop, ops, err := svc.Propose(r.Context(), req.GuestRef)
		if err != nil {
			writeMicrosegError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toProposalView(prop, ops))
	}
}

func handleMicrosegDryRun(svc MicrosegService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req microsegDryRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.GuestRef == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "guestRef is required")
			return
		}
		prop, report, err := svc.DryRun(r.Context(), req.GuestRef, req.HeldOut)
		if err != nil {
			writeMicrosegError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toDryRunView(prop.Subject.GuestRef.String(), report))
	}
}

func writeMicrosegError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrMicrosegBadRef):
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "invalid guest ref")
	case errors.Is(err, store.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not_found", "no observable flows for this guest")
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not compute microsegmentation proposal")
	}
}
