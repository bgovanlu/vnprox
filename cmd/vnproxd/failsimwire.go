// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/failsim"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// failsimwire.go wires T-1604's internal/failsim into this daemon's three
// consumers: api.FailsimService (GET /failsim/spof-score, POST
// /changesets/{id}/preflight-impact) and change.ImpactPreflighter (the
// scheduler's additive windowStart veto). The adapter gathers the pure
// simulator's inputs — the live inventory snapshot plus the corosync/Ceph
// side-tables it already reads for T-1503's Ceph findings (cephProviderAdapter
// already holds both) — and calls failsim's pure functions. Nothing here
// persists or mutates; the simulator is re-run against the current snapshot on
// every call (the same "continuously computed, never a shadow copy" contract
// the Ceph overlay follows).
//
// Like mgmtAdapter/scheduleAdapter (findings.go/server.go), the changeset seam
// is left unset at construction (findings/change wiring order) and filled with
// the real change.Service via set() once change.NewService succeeds — the
// PreflightImpact(refs) method the scheduler uses needs no change.Service, so
// only the HTTP changeset path is late-bound.
type failsimAdapter struct {
	graph *inventory.Graph
	ceph  *cephProviderAdapter
	// changeSvc backs PreflightImpactForChangeset only; set() fills it in.
	changeSvc changesetOpsSource
}

// changesetOpsSource is the minimal view of *change.Service the HTTP
// preflight-impact route needs: fetch a changeset (for its ops).
type changesetOpsSource interface {
	Get(ctx context.Context, id string) (change.Changeset, error)
}

func newFailsimAdapter(graph *inventory.Graph, cephAdapter *cephProviderAdapter) *failsimAdapter {
	return &failsimAdapter{graph: graph, ceph: cephAdapter}
}

func (a *failsimAdapter) set(svc changesetOpsSource) { a.changeSvc = svc }

// input builds the pure simulator's Input from the current snapshot plus the
// corosync/Ceph side-tables cephProviderAdapter already read once at startup.
// A nil/empty side-table degrades its dimension to NotEvaluated inside
// failsim — never a false "no impact". Tunnels are not yet threaded here (the
// SPOF inventory lists WireGuard tunnels "where present"; absent them, the
// tunnels dimension is honestly reported not-evaluated).
func (a *failsimAdapter) input() (failsim.Input, time.Time) {
	snap := a.graph.Snapshot()
	in := failsim.Input{Snapshot: snap}
	if a.ceph != nil {
		in.Corosync = a.ceph.cor
		status := a.ceph.status
		in.Ceph = &status
	}
	return in, snap.GeneratedAt()
}

// SPOFScore implements api.FailsimService.
func (a *failsimAdapter) SPOFScore(_ context.Context) (failsim.SPOFScore, time.Time, error) {
	in, generatedAt := a.input()
	return failsim.ScoreInventory(in), generatedAt, nil
}

// PreflightImpactForChangeset implements api.FailsimService: the worst failure
// impact among the changeset's touched entities.
func (a *failsimAdapter) PreflightImpactForChangeset(ctx context.Context, changesetID string) (failsim.Impact, error) {
	cs, err := a.changeSvc.Get(ctx, changesetID)
	if err != nil {
		return failsim.Impact{}, err
	}
	in, _ := a.input()
	return failsim.Preflight(in, changesetTouchedRefs(cs.Ops)), nil
}

// PreflightImpact implements change.ImpactPreflighter: the scheduler's
// additive windowStart veto. It computes the worst impact among refs and
// applies failsim's veto rule (quorum risk or mgmt-path loss).
func (a *failsimAdapter) PreflightImpact(_ context.Context, refs []inventory.Ref) (bool, string, map[string]any, error) {
	in, _ := a.input()
	im := failsim.Preflight(in, refs)
	veto, reason := failsim.PreflightUnsafe(im)
	if !veto {
		return false, "", nil, nil
	}
	detail := map[string]any{
		"target":       im.Target.String(),
		"severity":     im.Severity,
		"quorumRisk":   im.QuorumRisk,
		"mgmtPathLoss": im.MgmtPathLoss,
	}
	return true, reason, detail, nil
}

// changesetTouchedRefs is the op-target extraction the scheduler's own
// touchedTargetRefs performs internally (kept here for the HTTP path, which
// has the changeset's ops rather than going through the scheduler).
func changesetTouchedRefs(ops []change.Op) []inventory.Ref {
	seen := map[inventory.Ref]bool{}
	var out []inventory.Ref
	for _, op := range ops {
		if op.Target.IsZero() || seen[op.Target] {
			continue
		}
		seen[op.Target] = true
		out = append(out, op.Target)
	}
	return out
}
