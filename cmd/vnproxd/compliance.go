// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/compliance"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/store"
)

// compliance.go wires T-2706's compliance reporter: the shipped profile, the
// three evidence sources it maps over (the unified findings stream, T-1607's
// posture read model, T-2601's installed policy set), and the retained
// finding-transition history an as-of report is reconstructed from.
//
// Every adapter here is a projection, not a second path: the findings come
// from the same engine GET /findings serves, the posture from the same read
// adapter GET /posture serves, the policy from the same
// change.Service.PolicyStatus GET /policies serves, and the history from the
// same finding_events repo GET /history/events reads. Nothing in this file
// can disagree with the surface it summarizes.

// complianceFindingsAdapter projects the live findings stream onto
// compliance.FindingRef, attaching each finding's active acknowledgement so
// the report can say a failure was acknowledged — without letting the
// acknowledgement clear the control.
type complianceFindingsAdapter struct {
	engine *findings.Engine
	acks   *findings.AckService
	log    *slog.Logger
}

func (a complianceFindingsAdapter) ComplianceFindings(ctx context.Context) ([]compliance.FindingRef, error) {
	if a.engine == nil {
		return nil, nil
	}
	all := a.engine.Findings()
	if a.acks != nil {
		decorated, _, err := a.acks.Decorate(ctx, all)
		if err != nil {
			// Degrade to undecorated findings rather than failing the
			// report: an unreadable ack table must not be able to make a
			// failing control disappear.
			a.log.Warn("compliance: decorating findings with acknowledgements", "error", err)
		} else {
			all = decorated
		}
	}
	out := make([]compliance.FindingRef, 0, len(all))
	for _, f := range all {
		out = append(out, compliance.FindingRef{
			ID: f.ID, Check: f.Check, Severity: f.Severity, Acked: f.Ack != nil,
		})
	}
	return out, nil
}

// complianceHistoryAdapter projects finding_events onto the transition
// history an as-of report replays.
type complianceHistoryAdapter struct {
	repo *store.FindingEventRepo
}

func (a complianceHistoryAdapter) Transitions(ctx context.Context, until time.Time) ([]compliance.Transition, error) {
	rows, err := a.repo.ListByTimeRange(ctx, 0, until.Unix())
	if err != nil {
		return nil, err
	}
	out := make([]compliance.Transition, 0, len(rows))
	for _, r := range rows {
		out = append(out, compliance.Transition{FindingID: r.FindingID, At: r.At, Transition: r.Transition})
	}
	return out, nil
}

func (a complianceHistoryAdapter) Earliest(ctx context.Context) (time.Time, bool, error) {
	at, ok, err := a.repo.EarliestAt(ctx)
	if err != nil || !ok {
		return time.Time{}, false, err
	}
	return time.Unix(at, 0), true, nil
}

// compliancePolicyAdapter projects T-2601's PolicyStatus onto the evidence
// projection. A daemon with no policy store reports Configured=false, which
// makes every policy evidence item NOT EVALUATED — never satisfied by
// default.
type compliancePolicyAdapter struct {
	svc *change.Service
}

func (a compliancePolicyAdapter) CompliancePolicy(ctx context.Context) (compliance.PolicyState, error) {
	if a.svc == nil {
		return compliance.PolicyState{}, nil
	}
	status, err := a.svc.PolicyStatus(ctx)
	if err != nil {
		var notConfigured *change.ErrPolicyNotConfigured
		if errors.As(err, &notConfigured) {
			return compliance.PolicyState{}, nil
		}
		return compliance.PolicyState{}, err
	}

	return projectPolicyStatus(status), nil
}

// projectPolicyStatus is the projection itself, split out so it is testable
// without a wired change engine.
//
// The load-bearing field is ProbablyMisconfigured: T-2601's own signal that
// a rule has been evaluated enough times, over a long enough window, and has
// never matched an op. Dropping it here would let an unexercised rule read
// as evidence — the exact thing T-2601's author asked this card not to do —
// and nothing downstream could tell.
func projectPolicyStatus(status change.PolicyStatus) compliance.PolicyState {
	stats := make(map[string]change.PolicyRuleStatus, len(status.Rules))
	for _, st := range status.Rules {
		stats[st.RuleID] = st
	}
	out := compliance.PolicyState{Configured: true, Revision: status.Revision}
	for _, rule := range status.Set.Rules {
		st := stats[rule.ID]
		out.Rules = append(out.Rules, compliance.PolicyRuleRef{
			ID:                    rule.ID,
			Tags:                  rule.Tags,
			ProbablyMisconfigured: st.ProbablyMisconfigured,
			EvalCount:             st.EvalCount,
			MatchCount:            st.MatchCount,
			LastMatchedAt:         st.LastMatchedAt,
		})
	}
	return out
}

// complianceCheckUniverse describes where the unmapped-check list is
// computed from, carried into every report so the list's completeness is
// legible rather than assumed.
const complianceCheckUniverse = "this build's findings-check catalog (findings.AllCheckNames), unioned with any check observed in this report's own evidence"

// setupCompliance builds the compliance reporter, or returns nil when the
// shipped profile cannot be loaded — in which case the routes are simply not
// mounted (the standard degraded-mode convention) rather than serving a
// report assembled from a profile this build could not parse.
func setupCompliance(
	engine *findings.Engine,
	acks *findings.AckService,
	events *store.FindingEventRepo,
	postureRead compliance.PostureSource,
	changeSvc *change.Service,
	version string,
	logger *slog.Logger,
) *compliance.Service {
	profiles, err := compliance.LoadBuiltins()
	if err != nil {
		logger.Error("compliance: built-in profiles are unusable; the compliance routes will not be mounted", "error", err)
		return nil
	}
	svc := &compliance.Service{
		Profiles:       profiles,
		Posture:        postureRead,
		ProductVersion: version,
		KnownChecks:    findings.AllCheckNames(),
		CheckUniverse:  complianceCheckUniverse,
		Log:            logger,
	}
	if engine != nil {
		svc.Findings = complianceFindingsAdapter{engine: engine, acks: acks, log: logger}
	}
	if events != nil {
		svc.History = complianceHistoryAdapter{repo: events}
	}
	if changeSvc != nil {
		svc.Policy = compliancePolicyAdapter{svc: changeSvc}
	}
	return svc
}
