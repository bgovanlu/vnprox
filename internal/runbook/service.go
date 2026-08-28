// SPDX-License-Identifier: Apache-2.0

package runbook

import (
	"context"
	"fmt"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/fwlog"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// changeCreator is the minimal, stage-only subset of *change.Service this
// package binds to — deliberately the exact same two-method shape
// internal/plugin/stager.go's changeCreator and internal/mcp/stageonly.go's
// ChangesetStager already use for the plugin and MCP seams. Declaring the
// dependency this narrowly means the compiler, not a code review,
// guarantees a runbook cannot reach Apply/Confirm/Rollback: those methods
// are simply not in the interface Service holds. stageonly.go asserts this
// at compile time.
type changeCreator interface {
	Create(ctx context.Context, author, title string, ops []change.Op) (change.Changeset, error)
	Validate(ctx context.Context, id, author string) (change.Changeset, error)
}

// InventorySource is the seam Service uses for a fresh read snapshot to
// build a ReadContext from — mirrors internal/blueprint.InventorySource's
// and internal/change.InventorySource's identical one-method seam over
// *inventory.Graph.
type InventorySource interface {
	Snapshot() inventory.Snapshot
}

// FindingsProvider is the seam Service uses to look a finding up by id
// before preparing a runbook against it — the same one-method shape
// internal/api.FindingsService's own Findings() method already establishes
// for this exact "look through the live findings stream" need.
type FindingsProvider interface {
	Findings() []findings.Finding
}

// Config configures a Service.
type Config struct {
	Changes  changeCreator
	Findings FindingsProvider
	// Inventory backs Snapshot in the ReadContext every built-in template
	// consults.
	Inventory InventorySource
	// FwAnalytics is optional: nil disables TemplateDeleteUnusedFwRule's
	// read-check entirely (its Render call always returns an error rather
	// than propose an unverified delete — see renderDeleteUnusedFwRule),
	// the same "nil dependency -> that feature quietly can't propose
	// anything" degradation every other optional seam in this codebase
	// (e.g. findings.Config's own provider fields) already uses.
	FwAnalytics findings.FwAnalyticsProvider
	Now         func() time.Time
}

// Service prepares built-in runbooks against live findings: look the
// finding up, look the runbook up, gather a fresh ReadContext, call Render,
// and — only if Render succeeded — stage and validate the result through
// the narrow changeCreator seam above. It is the only exported way this
// package ever reaches the change engine.
type Service struct {
	changes     changeCreator
	findingsSvc FindingsProvider
	inv         InventorySource
	fwAnalytics findings.FwAnalyticsProvider
	now         func() time.Time
}

// New constructs a Service.
func New(cfg Config) *Service {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		changes:     cfg.Changes,
		findingsSvc: cfg.Findings,
		inv:         cfg.Inventory,
		fwAnalytics: cfg.FwAnalytics,
		now:         now,
	}
}

// Prepare is T-4003's whole surface: look up findingID, look up the named
// runbook, run its read-checks and render its ops (Render), and — if that
// succeeded — stage the result as a draft changeset and immediately
// validate it, exactly once, exactly like every other stage-only caller of
// this seam (internal/plugin.Stager, internal/mcp.ChangesetStager).
//
// Prepare never itself returns a Go error for a template that rendered ops
// which then fail validation — that changeset is still successfully staged
// (a human reviewing a red, still-draft changeset is the expected outcome
// of validation catching a real problem, the same as if they had built
// those ops by hand in the editor) and the returned Changeset carries the
// error findings explaining why it did not reach StatusValidated. Prepare
// returns a non-nil error only when nothing could be staged at all: no such
// finding, no such runbook, the runbook is not attached to that finding's
// check, or Render itself refused (ErrNothingToDo or any other Render
// error).
//
// T-4016 (planning/tasks/T-4016-stage-only-convergence-semantics.md): the
// returned Changeset's Status is always the real one (draft or validated —
// this package cannot reach applied, structurally, see stageonly.go) rather
// than any runbook-specific "prepared" gloss on top of it, exactly what
// that task's interim answer asks every stage-only integration in this
// phase to expose.
func (s *Service) Prepare(ctx context.Context, author, findingID, runbookName string) (change.Changeset, error) {
	f, ok := s.findFinding(findingID)
	if !ok {
		return change.Changeset{}, fmt.Errorf("%w: %s", ErrFindingNotFound, findingID)
	}
	rb, ok := ByName(runbookName)
	if !ok {
		return change.Changeset{}, fmt.Errorf("%w: %s", ErrRunbookNotFound, runbookName)
	}
	if rb.CheckName != f.Check {
		return change.Changeset{}, fmt.Errorf("%w: runbook %s attaches to check %s, finding %s is check %s",
			ErrNotAttached, rb.Name, rb.CheckName, findingID, f.Check)
	}

	rc := ReadContext{Snapshot: s.inv.Snapshot()}
	if s.fwAnalytics != nil {
		a := s.fwAnalytics.Analytics(s.now(), findings.DefaultFwRuleUnusedWindow, fwlog.DefaultTopN)
		rc.FwAnalytics = &a
	}

	ops, title, err := Render(rb, f, rc)
	if err != nil {
		return change.Changeset{}, fmt.Errorf("runbook %s: %w", rb.Name, err)
	}

	cs, createErr := s.changes.Create(ctx, author, title, ops)
	if createErr != nil {
		return change.Changeset{}, fmt.Errorf("runbook %s: staging changeset: %w", rb.Name, createErr)
	}
	cs, validateErr := s.changes.Validate(ctx, cs.ID, author)
	if validateErr != nil {
		return change.Changeset{}, fmt.Errorf("runbook %s: validating staged changeset %s: %w", rb.Name, cs.ID, validateErr)
	}
	return cs, nil
}

func (s *Service) findFinding(id string) (findings.Finding, bool) {
	for _, f := range s.findingsSvc.Findings() {
		if f.ID == id {
			return f, true
		}
	}
	return findings.Finding{}, false
}
