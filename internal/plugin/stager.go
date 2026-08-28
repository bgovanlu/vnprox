// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"errors"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/change"
)

// ErrCapabilityExceeded is returned by a Stager when a plugin tries to stage an
// op whose required capability its declared scope does not cover. The op is
// rejected here, inside the SDK, before it ever reaches internal/change — the
// enforcement point for T-1702 AC2. It is never a warning; there is no override.
var ErrCapabilityExceeded = errors.New("plugin: op requires a capability outside the plugin's declared scope")

// Stager is the ONLY change-engine surface a plugin can reach. It exposes
// exactly the stage-only pair — Create (stage a draft changeset) and Validate
// (run the validator pipeline over it) — and deliberately NOTHING else. There is
// no Apply, Confirm, or Rollback method on this interface, in-process or over the
// out-of-process transport: a plugin extends read/ingest/render seams and can
// stage work for a human to apply, but is never itself a mutation path (T-1702
// AC3, verified by an interface-surface test). A human (or the confirm
// machinery) remains the sole apply authority, exactly as for every other
// changeset since T-205.
type Stager interface {
	// Create stages a draft changeset authored as the plugin's own actor. Every
	// op is capability-checked against the plugin's declared scope first; if any
	// op's RequiredCap is outside that scope, Create returns ErrCapabilityExceeded
	// and stages nothing.
	Create(ctx context.Context, title string, ops []change.Op) (change.Changeset, error)
	// Validate runs the validator pipeline over a draft the plugin staged,
	// promoting/demoting its draft<->validated status exactly like the ordinary
	// flow. It never advances a changeset past validated.
	Validate(ctx context.Context, changesetID string) (change.Changeset, error)
}

// changeCreator is the minimal, stage-only subset of *change.Service the SDK
// binds to. Declaring the dependency this narrowly means the compiler itself —
// not a code review — guarantees the plugin seam cannot reach Apply/Confirm/
// Rollback: those methods are simply not in the interface the seam holds.
// *change.Service satisfies this.
type changeCreator interface {
	Create(ctx context.Context, author, title string, ops []change.Op) (change.Changeset, error)
	Validate(ctx context.Context, id, author string) (change.Changeset, error)
}

// scopedStager is the capability-enforcing Stager implementation. It binds a
// plugin's identity (for the author/actor stamped on staged changesets, so the
// audit trail shows plugin origin) and its declared Scope (the capability
// ceiling) to a stage-only change surface.
type scopedStager struct {
	svc   changeCreator
	scope Scope
	actor string
}

// newScopedStager binds a stage-only change surface to one plugin's actor and
// scope. actor is the plugin's audit identity ("plugin:<id>").
func newScopedStager(svc changeCreator, actor string, scope Scope) *scopedStager {
	return &scopedStager{svc: svc, actor: actor, scope: scope}
}

// Create implements Stager. It enforces the capability ceiling before staging:
// each op's RequiredCap must be covered by the plugin's scope, or the whole
// create is refused with ErrCapabilityExceeded (all-or-nothing — a changeset is
// never partially staged). Only when every op passes does it delegate to the
// underlying stage-only Create.
func (s *scopedStager) Create(ctx context.Context, title string, ops []change.Op) (change.Changeset, error) {
	if err := s.checkScope(ops); err != nil {
		return change.Changeset{}, err
	}
	cs, err := s.svc.Create(ctx, s.actor, title, ops)
	if err != nil {
		return change.Changeset{}, fmt.Errorf("plugin %s staging changeset: %w", s.actor, err)
	}
	return cs, nil
}

// Validate implements Stager, delegating to the stage-only validate with the
// plugin's actor. It cannot advance a changeset past validated.
func (s *scopedStager) Validate(ctx context.Context, changesetID string) (change.Changeset, error) {
	cs, err := s.svc.Validate(ctx, changesetID, s.actor)
	if err != nil {
		return change.Changeset{}, fmt.Errorf("plugin %s validating changeset %s: %w", s.actor, changesetID, err)
	}
	return cs, nil
}

// checkScope rejects the whole op batch if any single op requires a capability
// the plugin's scope does not grant. The error names the offending op and the
// capability it needed, so an operator can see exactly why a plugin was refused.
func (s *scopedStager) checkScope(ops []change.Op) error {
	for _, op := range ops {
		need := RequiredCap(op.Type)
		if !s.scope.Has(need) {
			return fmt.Errorf("%w: op %q requires %q (actor %s)", ErrCapabilityExceeded, op.Type, need, s.actor)
		}
	}
	return nil
}
