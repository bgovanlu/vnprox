// SPDX-License-Identifier: Apache-2.0

package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/gitsync"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Sentinel errors, per docs/development.md's errors-in-one-place convention.
var (
	// ErrNotOffered is returned when the named finding does not exist, or
	// exists and does not offer the requested action. It is a plain answer
	// rather than a failure: most findings offer neither.
	ErrNotOffered = errors.New("reconcile: this finding does not offer that action")

	// ErrAdoptNotConfigured is returned when adopting is asked for on a
	// deployment with no write-capable spec repository ([gitsync]
	// push_token_file unset, or gitsync off). Nothing is contacted.
	ErrAdoptNotConfigured = errors.New("reconcile: adopting reality is not configured on this deployment")
)

// Findings is the drift seam: two LOOKUPS BY FINDING ID, and nothing else.
//
// Neither method takes an op list, a ref list or a document from the caller.
// The only thing an operator can name is a finding, and what that finding
// means is decided by internal/drift's own check logic against the current
// snapshot — so an adoption can never be widened past the entity the finding
// is about, and a "restore" can never carry ops nobody computed. This is the
// same server-side-lookup discipline POST /drift/{id}/fix already uses.
type Findings interface {
	RestoreIntentOps(id string) (ops []change.Op, title string, ok bool)
	AdoptRealityRefs(id string) (refs []inventory.Ref, detail string, ok bool)
}

// Stager is the ONLY change-engine surface this package holds: one method,
// which stages a draft.
//
// No Apply, Confirm, Approve, Validate, Rollback or Discard. Restoring intent
// produces a draft a human then takes through the ordinary review; the type
// system is what guarantees this package cannot skip that.
type Stager interface {
	Create(ctx context.Context, author, title string, ops []change.Op) (change.Changeset, error)
}

// Adopter is the git seam — *gitsync.Proposer satisfies it. It opens a pull
// request and stops; there is no merge verb here or anywhere beneath it.
type Adopter interface {
	Enabled() bool
	ProposeAdoption(ctx context.Context, req gitsync.AdoptionRequest) (gitsync.Proposal, error)
	GetAdoption(ctx context.Context, findingID string) (gitsync.Proposal, error)
}

// Config wires a Service. Findings is required; a nil Stager or Adopter simply
// means that half is unavailable and says so, rather than panicking.
type Config struct {
	Findings Findings
	Stager   Stager
	Adopter  Adopter
	Logger   *slog.Logger
}

// Service executes the two reconciliation actions.
//
// It has no state and no goroutine. Every method here runs on the caller's
// request goroutine, which is the whole point: there is nothing for a timer to
// drive.
type Service struct {
	cfg Config
	log *slog.Logger
}

// New builds a Service. It performs no I/O and starts nothing.
func New(cfg Config) *Service {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{cfg: cfg, log: logger}
}

// AdoptEnabled reports whether this deployment can adopt at all.
func (s *Service) AdoptEnabled() bool {
	return s.cfg.Adopter != nil && s.cfg.Adopter.Enabled()
}

// RestoreIntent stages a draft changeset bringing the cluster back to what the
// spec declares for the named finding's entity.
//
// It stages and stops. The returned changeset is a DRAFT: validating, applying
// and confirming it are the operator's own subsequent, separately authorised
// steps through the ordinary change engine.
func (s *Service) RestoreIntent(ctx context.Context, findingID, actor string) (change.Changeset, error) {
	if s.cfg.Findings == nil || s.cfg.Stager == nil {
		return change.Changeset{}, ErrNotOffered
	}
	ops, title, ok := s.cfg.Findings.RestoreIntentOps(findingID)
	if !ok || len(ops) == 0 {
		return change.Changeset{}, fmt.Errorf("%w: restoring intent for %s", ErrNotOffered, findingID)
	}
	cs, err := s.cfg.Stager.Create(ctx, actor, title, ops)
	if err != nil {
		return change.Changeset{}, fmt.Errorf("reconcile: staging the changeset restoring intent for %s: %w", findingID, err)
	}
	s.log.Info("reconcile: staged a changeset restoring intent",
		"findingId", findingID, "changesetId", cs.ID, "ops", len(ops), "actor", actor)
	return cs, nil
}

// AdoptReality proposes a spec commit describing the cluster as it is for the
// named finding's entity.
//
// It changes nothing about the cluster. What comes back is a pull request URL
// a human opens; whatever happens to that request returns through T-2701's
// ordinary sync, which stages a draft changeset for review like any other.
func (s *Service) AdoptReality(ctx context.Context, findingID, actor string) (gitsync.Proposal, error) {
	if s.cfg.Findings == nil {
		return gitsync.Proposal{}, ErrNotOffered
	}
	if !s.AdoptEnabled() {
		return gitsync.Proposal{}, ErrAdoptNotConfigured
	}
	refs, detail, ok := s.cfg.Findings.AdoptRealityRefs(findingID)
	if !ok || len(refs) == 0 {
		return gitsync.Proposal{}, fmt.Errorf("%w: adopting reality for %s", ErrNotOffered, findingID)
	}
	proposal, err := s.cfg.Adopter.ProposeAdoption(ctx, gitsync.AdoptionRequest{
		FindingID: findingID, Refs: refs, Detail: detail, Actor: actor,
	})
	if err != nil {
		return gitsync.Proposal{}, err
	}
	s.log.Info("reconcile: proposed adopting live state into the spec",
		"findingId", findingID, "pullRequest", proposal.PullRequestURL, "actor", actor)
	return proposal, nil
}

// Adoption returns the pull request a finding was already adopted as, or
// gitsync.ErrNoProposal. It is a read: the review surface links it.
func (s *Service) Adoption(ctx context.Context, findingID string) (gitsync.Proposal, error) {
	if s.cfg.Adopter == nil {
		return gitsync.Proposal{}, gitsync.ErrNoProposal
	}
	return s.cfg.Adopter.GetAdoption(ctx, findingID)
}
