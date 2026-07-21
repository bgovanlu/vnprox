package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/baseline"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/microseg"
	"github.com/bgovanlu/vnprox/internal/store"
)

// microsegwire.go wires T-1602's internal/microseg planner into this daemon as
// api.MicrosegService (POST /microseg/propose, POST /microseg/dry-run). The
// adapter gathers the planner's pure inputs — the guest's observed flow corpus
// (from flow_samples), its learned baseline (from baseline_profiles, so
// anomalous flows are excluded from what counts as observed-good), and its
// current resolved firewall view (from the live inventory graph, so the planner
// does not re-propose a rule PVE already effectively has) — and calls
// microseg's pure functions. Nothing here mutates or stages: Propose returns a
// draft's worth of ops for the review UI (T-1603) to hand into the ordinary
// ChangesetDrawer; the planner never applies.
//
// Like baselineService, the flow_samples source is late-bound: the API options
// are assembled around the same time, but flowRepo only exists once setupFlows
// returns, so set() fills it in (server.go's wiring order).

const (
	// microsegLearnWindowDays is the observed-good learning window Propose
	// synthesizes over (matches baseline's own default learn window).
	microsegLearnWindowDays = baseline.DefaultLearnWindowDays
	// microsegHeldOutDays is the trailing span DryRun(heldOut=true) replays
	// against a training-derived policy — a genuinely held-out slice (excluded
	// from the training window below) so a would-block there is an independent
	// proof point, not the training corpus dry-run against itself.
	microsegHeldOutDays = 2
	microsegRowCap      = 200_000
	microsegPageLimit   = 5000
)

// microsegAdapter implements api.MicrosegService.
type microsegAdapter struct {
	graph    *inventory.Graph
	profiles *store.BaselineProfileRepo
	flowRepo *store.FlowSampleRepo
	now      func() time.Time
	mu       sync.Mutex
}

func newMicrosegAdapter(graph *inventory.Graph, profiles *store.BaselineProfileRepo) *microsegAdapter {
	return &microsegAdapter{graph: graph, profiles: profiles, now: time.Now}
}

func (a *microsegAdapter) set(flowRepo *store.FlowSampleRepo) {
	a.mu.Lock()
	a.flowRepo = flowRepo
	a.mu.Unlock()
}

func (a *microsegAdapter) repo() *store.FlowSampleRepo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.flowRepo
}

// windows returns the training window and the held-out window: training is
// [now-(learn+heldOut) .. now-heldOut]; held-out is the trailing
// [now-heldOut .. now]. Keeping the held-out span out of the training window is
// what makes DryRun(heldOut=true) an independent check.
func (a *microsegAdapter) windows() (trainStart, trainEnd, heldStart, heldEnd int64) {
	now := a.now()
	heldEnd = now.Unix()
	heldStart = now.AddDate(0, 0, -microsegHeldOutDays).Unix()
	trainEnd = heldStart
	trainStart = now.AddDate(0, 0, -(microsegLearnWindowDays + microsegHeldOutDays)).Unix()
	return trainStart, trainEnd, heldStart, heldEnd
}

// subjectFor resolves the guest ref the request named into a planner Subject:
// it validates the ref is a guest, finds the guest in the live inventory to
// recover its qemu/lxc kind (the firewall ruleset id needs it), and builds the
// ruleset target Stage will emit ops against. A malformed ref =>
// api.ErrMicrosegBadRef; an unknown guest => store.ErrNotFound.
func (a *microsegAdapter) subjectFor(guestRef string) (microseg.Subject, error) {
	ref, err := inventory.ParseRef(guestRef)
	if err != nil || ref.Kind != inventory.KindGuest {
		return microseg.Subject{}, api.ErrMicrosegBadRef
	}
	ent, ok := a.graph.Snapshot().Get(ref)
	if !ok {
		return microseg.Subject{}, store.ErrNotFound
	}
	guest, ok := ent.(*inventory.Guest)
	if !ok {
		return microseg.Subject{}, store.ErrNotFound
	}
	kind := guest.Type
	if kind == "" {
		kind = "qemu"
	}
	return microseg.Subject{
		GuestRef:   ref,
		RulesetRef: microseg.GuestRulesetRef(ref.Node, kind, ref.ID),
	}, nil
}

// planContext bundles everything Propose/DryRun both need for one guest.
type planContext struct {
	train    []flow.Record
	subj     microseg.Subject
	existing microseg.Existing
	profile  baseline.Profile
}

func (a *microsegAdapter) prepare(ctx context.Context, guestRef string) (planContext, error) {
	subj, err := a.subjectFor(guestRef)
	if err != nil {
		return planContext{}, err
	}
	repo := a.repo()
	if repo == nil {
		return planContext{}, store.ErrNotFound
	}
	trainStart, trainEnd, _, _ := a.windows()
	train, err := a.gather(ctx, repo, subj.GuestRef.String(), trainStart, trainEnd)
	if err != nil {
		return planContext{}, err
	}
	return planContext{
		subj:     subj,
		profile:  a.loadProfile(ctx, subj.GuestRef, train, trainStart, trainEnd),
		existing: a.existingPolicy(subj.GuestRef),
		train:    train,
	}, nil
}

// Propose implements api.MicrosegService.
func (a *microsegAdapter) Propose(ctx context.Context, guestRef string) (microseg.Proposal, []change.Op, error) {
	pc, err := a.prepare(ctx, guestRef)
	if err != nil {
		return microseg.Proposal{}, nil, err
	}
	prop := microseg.Propose(pc.subj, pc.train, pc.profile, pc.existing, microseg.DefaultConfig())
	return prop, microseg.Stage(prop), nil
}

// DryRun implements api.MicrosegService.
func (a *microsegAdapter) DryRun(ctx context.Context, guestRef string, heldOut bool) (microseg.Proposal, microseg.Report, error) {
	pc, err := a.prepare(ctx, guestRef)
	if err != nil {
		return microseg.Proposal{}, microseg.Report{}, err
	}
	prop := microseg.Propose(pc.subj, pc.train, pc.profile, pc.existing, microseg.DefaultConfig())

	corpus := pc.train
	if heldOut {
		repo := a.repo()
		_, _, heldStart, heldEnd := a.windows()
		held, gerr := a.gather(ctx, repo, pc.subj.GuestRef.String(), heldStart, heldEnd)
		if gerr != nil {
			return microseg.Proposal{}, microseg.Report{}, gerr
		}
		corpus = held
	}
	return prop, microseg.DryRun(prop, corpus, microseg.DefaultConfig()), nil
}

// loadProfile returns the guest's stored baseline (learned by baseline.go's
// continuous job over a prior window, so it is independent of the training
// corpus — the property that makes anomaly exclusion load-bearing). If none is
// stored yet (cold start), it falls back to learning one from the training
// corpus itself; that fallback cannot exclude an anomaly hiding inside the
// training window (a baseline never flags its own data), so it is a documented
// degraded mode, not the intended path.
func (a *microsegAdapter) loadProfile(ctx context.Context, guestRef inventory.Ref, train []flow.Record, start, end int64) baseline.Profile {
	if a.profiles != nil {
		if row, err := a.profiles.Get(ctx, guestRef.String()); err == nil {
			if prof, uerr := baseline.Unmarshal(row.ProfileJSON); uerr == nil {
				return prof
			}
		}
	}
	return baseline.Learn(train, guestRef.String(), baseline.Window{Start: start, End: end})
}

// existingPolicy resolves the guest's current firewall view from the live
// inventory graph, so Propose can suppress a rule PVE already effectively has.
// A resolve error (e.g. a malformed ruleset) degrades to "no known existing
// policy" — the planner then proposes the rule rather than assuming coverage.
func (a *microsegAdapter) existingPolicy(guestRef inventory.Ref) microseg.Existing {
	snap := fw.BuildSnapshot(a.graph.Snapshot().All())
	view, err := fw.Resolve(snap, guestRef)
	if err != nil {
		return microseg.Existing{}
	}
	return microseg.Existing{View: &view}
}

// gather pages flow_samples from start (newest-first) and returns every record
// involving guestRef with an observation time in [start, end], up to the row
// cap — the corpus the planner treats as observed traffic for that guest.
func (a *microsegAdapter) gather(ctx context.Context, repo *store.FlowSampleRepo, guestRef string, start, end int64) ([]flow.Record, error) {
	var out []flow.Record
	scanned := 0
	cursor := ""
	for scanned < microsegRowCap {
		page, next, err := repo.Query(ctx, store.FlowFilter{FromTs: start}, cursor, microsegPageLimit)
		if err != nil {
			return nil, fmt.Errorf("cmd/vnproxd: scanning flows for microseg: %w", err)
		}
		for _, sm := range page {
			scanned++
			if sm.At > end {
				continue
			}
			if sm.SrcRef != guestRef && sm.DstRef != guestRef {
				continue
			}
			out = append(out, flowSampleToRecord(sm))
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return out, nil
}
