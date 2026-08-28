// SPDX-License-Identifier: Apache-2.0

package gitsync

// adopt.go is T-2703's "adopt reality" half: a drift finding becomes a
// proposed spec commit that matches the cluster.
//
// # Why this could not be Propose
//
// Propose (propose.go) moves the DOCUMENT to where a changeset says the
// cluster should go, and it verifies that by requiring
//
//	Import(proposed, live)  ==  Import(base, live) + ops
//
// Adoption goes the other way: it moves the document to where the cluster
// already IS, so the plan it must produce is the empty one. Feeding an
// adoption through Propose would trip its own round-trip guard on every call
// (the base plan's ops would all read as "removed"), and — more fundamentally
// — the changeset op vocabulary has no delete, so an entity the document
// declares and the cluster no longer has is not expressible as ops at all.
// That is why internal/spec grew RemoveEntities/AdoptEntities and why this is
// a separate entry point rather than a flag on the old one. What the two DO
// share is the host write ordering (publication/publish), because that
// ordering is what keeps AC3's "either both the branch and the request exist,
// or neither" true.
//
// # The convergence check is enforced here, not merely tested (AC1)
//
// "Adopt reality" means the proposed document re-imports to a plan that is
// empty against current live. Before a single host call that writes anything,
// this file requires, for adopted refs R, base document B, adopted document A
// and live snapshot L:
//
//	Import(A, L) has NO op targeting any ref in R          (it converged)
//	Import(A, L) \ Import(B, L)  ==  {}                     (nothing new elsewhere)
//	A != B                                                  (there is something to propose)
//
// The first is AC1 exactly. The second is what stops an adoption of one bridge
// from quietly re-planning the rest of the cluster. The third is what keeps a
// finding from offering an action that opens an empty pull request — the state
// AC5 forbids, enforced on the executing side as well as the advertising one.
//
// # Still not a merge, still not automatic
//
// Nothing here merges, approves, or polls, for the same reason propose.go does
// not: the Host seam has no verb for any of them. And nothing here runs on a
// timer — ProposeAdoption is only ever reached from an explicit operator
// request (internal/reconcile), never from the drift cycle.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/change/ifaces"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/spec"
	"github.com/bgovanlu/vnprox/internal/store"
)

// AdoptBranchPrefix is what an adoption's branch is named after. Like
// DefaultBranchPrefix it is a constant plus a derived, credential-free
// identifier — the drift finding's own id — which is what makes adopting the
// same finding twice address the same branch and therefore update the same
// pull request rather than opening a second.
const AdoptBranchPrefix = "vnprox/adopt-"

// adoptionKeyPrefix namespaces an adoption inside the changeset_proposals
// table, whose primary key is a changeset id. An adoption has no changeset —
// it moves the document, not the cluster — so it is stored under a key that
// cannot collide with a ULID, and proposalFromRow turns it back into a
// FindingID on the way out. The alternative, a second table, would need a
// migration for one column's worth of difference in a row that is otherwise
// identical.
const adoptionKeyPrefix = "drift-adoption:"

// AdoptionRequest is one "adopt reality" proposal.
//
//nolint:govet // fieldalignment: field order reads as the request being made, not packing.
type AdoptionRequest struct {
	// FindingID is the drift finding being adopted. It is the proposal's
	// identity: the branch name is derived from it, so re-adopting the same
	// finding updates the same pull request.
	FindingID string
	// Refs are the entities whose live state is to be written into the
	// document. They come from internal/drift's own lookup by finding id,
	// never from a request body.
	Refs []inventory.Ref
	// Detail is the finding's own sentence, reproduced in the pull-request
	// body so the review context travels with the review.
	Detail string
	// Actor is the acting user, for the audit row and the proposal record.
	Actor string
}

// ProposeAdoption renders the live state of req.Refs into the spec document,
// commits it on a deterministic branch, and opens (or updates) a pull request
// for it.
//
//nolint:gocyclo // one linear transaction: read -> adopt -> verify -> branch -> commit -> request. Splitting it would hide the ordering AC3 rests on.
func (p *Proposer) ProposeAdoption(ctx context.Context, req AdoptionRequest) (Proposal, error) {
	if !p.Enabled() {
		return Proposal{}, ErrProposeNotConfigured
	}
	if req.FindingID == "" {
		return Proposal{}, fmt.Errorf("%w: an adoption must name the finding it came from", ErrNothingToPropose)
	}
	if len(req.Refs) == 0 {
		return Proposal{}, fmt.Errorf("%w: adoption %s names no entity", ErrNothingToPropose, req.FindingID)
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	baseDoc, baseSpec, err := p.readBase(ctx)
	if err != nil {
		return Proposal{}, err
	}

	snap := p.cfg.Inventory.Snapshot()
	adopted, err := spec.AdoptEntities(baseSpec, req.Refs, snap)
	if err != nil {
		return Proposal{}, fmt.Errorf("%w: %w", ErrNotExpressible, err)
	}
	if verifyErr := p.verifyAdoption(baseSpec, adopted, req.Refs, snap); verifyErr != nil {
		return Proposal{}, verifyErr
	}
	adoptedDoc, err := spec.Marshal(adopted)
	if err != nil {
		return Proposal{}, fmt.Errorf("gitsync: rendering the adopted spec for %s: %w", req.FindingID, err)
	}

	in := PullRequestInput{
		Branch: p.adoptBranchName(req.FindingID),
		Base:   p.cfg.Ref,
		Title:  adoptionTitle(req.Refs),
		Body:   p.adoptionBody(req, string(baseDoc), string(adoptedDoc)),
	}
	result, err := p.publish(ctx, publication{
		key: req.FindingID, subject: "drift finding " + req.FindingID, in: in,
		content: adoptedDoc, commitMessage: adoptionCommitMessage(req),
	})
	if err != nil {
		return Proposal{}, err
	}
	result.FindingID = req.FindingID
	result.ProposedBy = firstNonEmptyString(req.Actor, ProposeAuthor)
	p.record(ctx, &result)
	p.auditAdoption(ctx, result, req)
	p.log.Info("gitsync: proposed adopting live state into the spec",
		"findingId", req.FindingID, "refs", refLabels(req.Refs), "branch", result.Branch,
		"pullRequest", result.PullRequestURL, "created", result.Created, "remote", p.cfg.Host.Describe())
	return result, nil
}

// GetAdoption returns the adoption proposal recorded for a drift finding, or
// ErrNoProposal.
func (p *Proposer) GetAdoption(ctx context.Context, findingID string) (Proposal, error) {
	if p.cfg.Proposals == nil {
		return Proposal{}, ErrNoProposal
	}
	row, err := p.cfg.Proposals.Get(ctx, adoptionKeyPrefix+findingID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Proposal{}, ErrNoProposal
		}
		return Proposal{}, fmt.Errorf("gitsync: reading the adoption proposal for finding %s: %w", findingID, err)
	}
	return proposalFromRow(row), nil
}

// verifyAdoption is AC1, enforced in production rather than only in a test.
func (p *Proposer) verifyAdoption(baseSpec, adopted spec.Spec, refs []inventory.Ref, snap inventory.Snapshot) error {
	same, err := spec.SameIntent(baseSpec, adopted)
	if err != nil {
		return err
	}
	if same {
		return fmt.Errorf("%w: the spec at %s already describes %s as the cluster has it",
			ErrNothingToPropose, p.cfg.Path, strings.Join(refLabels(refs), ", "))
	}

	basePlan, _, err := spec.Import(baseSpec, snap)
	if err != nil {
		return fmt.Errorf("gitsync: planning the current spec against live state: %w", err)
	}
	adoptedPlan, _, err := spec.Import(adopted, snap)
	if err != nil {
		return fmt.Errorf("gitsync: planning the adopted spec against live state: %w", err)
	}

	adoptedSet := map[inventory.Ref]bool{}
	for _, ref := range refs {
		adoptedSet[ref] = true
	}
	var unconverged []string
	for _, op := range adoptedPlan {
		if adoptedSet[op.Target] {
			unconverged = append(unconverged, fmt.Sprintf("%s %s", op.Type, op.Target))
		}
	}
	if len(unconverged) > 0 {
		sort.Strings(unconverged)
		return fmt.Errorf("%w: adopting %s would still leave %s to reconcile — the document does not describe live state after all",
			ErrRoundTrip, strings.Join(refLabels(refs), ", "), strings.Join(unconverged, ", "))
	}

	added, err := opsExcept(adoptedPlan, basePlan)
	if err != nil {
		return err
	}
	if len(added) > 0 {
		return fmt.Errorf("%w: adopting %s would introduce %s elsewhere in the cluster; an adoption must only ever narrow the plan",
			ErrRoundTrip, strings.Join(refLabels(refs), ", "), describeOps(added))
	}
	return nil
}

// --- pull-request text ------------------------------------------------------

func adoptionTitle(refs []inventory.Ref) string {
	return fmt.Sprintf("vnprox: adopt live state for %s", strings.Join(refLabels(refs), ", "))
}

// adoptionCommitMessage names the finding and the entities. It carries no
// credential: the only inputs are a finding id and entity refs.
func adoptionCommitMessage(req AdoptionRequest) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "spec: adopt live state for %s\n\n", strings.Join(refLabels(req.Refs), ", "))
	fmt.Fprintf(&sb, "Raised by vnprox as drift finding %s.\n\n", req.FindingID)
	for _, label := range refLabels(req.Refs) {
		fmt.Fprintf(&sb, "  - %s\n", label)
	}
	return sb.String()
}

// adoptionBody renders the review context. The central claim it makes — that
// this document re-imports to an empty plan for these entities — is one the
// code above has already verified, so the body states a checked fact rather
// than an intention.
func (p *Proposer) adoptionBody(req AdoptionRequest, baseDoc, adoptedDoc string) string {
	var sb strings.Builder

	sb.WriteString("Opened by **vnprox** to adopt live state into the spec.\n\n")
	sb.WriteString("A drift finding reported that the spec, the interfaces file and the running kernel disagree about the entities below. This request takes the **adopt reality** side of that decision: it moves the document to describe the cluster as it is. The other side — bringing the cluster back to the spec — is a changeset staged in vnprox and is not this request.\n\n")
	fmt.Fprintf(&sb, "| | |\n|---|---|\n| Finding | `%s` |\n| Entities | %s |\n| Requested by | %s |\n\n",
		req.FindingID, "`"+strings.Join(refLabels(req.Refs), "`, `")+"`", firstNonEmptyString(req.Actor, ProposeAuthor))

	if req.Detail != "" {
		sb.WriteString("## What diverged\n\n")
		sb.WriteString(req.Detail + "\n\n")
	}

	sb.WriteString("## Spec diff\n\n```diff\n")
	sb.WriteString(ifaces.UnifiedDiff(p.cfg.Path, p.cfg.Path, baseDoc, adoptedDoc))
	sb.WriteString("```\n\n")

	sb.WriteString("## Checked before this request was opened\n\n")
	sb.WriteString("- Re-importing this document against the current live state plans **no** change for the entities above — which is what having adopted them means.\n")
	sb.WriteString("- It introduces no new change anywhere else in the cluster.\n\n")

	sb.WriteString("---\n\n")
	sb.WriteString("vnprox does not merge, gate, or poll this request, and it applied nothing to the cluster to open it. Once it lands, the document returns through the ordinary git spec sync.\n")
	return sb.String()
}

// --- bookkeeping ------------------------------------------------------------

// adoptBranchName derives a branch from a finding id. Finding ids contain
// characters git refnames forbid (`|`, `:`), so everything outside
// [A-Za-z0-9_-] is folded to a dash — deterministically, so the same finding
// always addresses the same branch.
func (p *Proposer) adoptBranchName(findingID string) string {
	return AdoptBranchPrefix + slugForBranch(findingID)
}

func slugForBranch(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			sb.WriteRune(r)
		default:
			sb.WriteByte('-')
		}
	}
	out := strings.Trim(sb.String(), "-")
	if out == "" {
		return "unnamed"
	}
	return out
}

// proposalStoreKey is the changeset_proposals primary key for a proposal: the
// changeset id for a changeset proposal, the namespaced finding id for an
// adoption.
func proposalStoreKey(p Proposal) string {
	if p.FindingID != "" {
		return adoptionKeyPrefix + p.FindingID
	}
	return p.ChangesetID
}

func refLabels(refs []inventory.Ref) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.String())
	}
	sort.Strings(out)
	return out
}

// auditAdoption writes one row per adoption. It records the finding, the refs,
// the branch and the request URL — never the document, never the credential.
func (p *Proposer) auditAdoption(ctx context.Context, result Proposal, req AdoptionRequest) {
	if p.cfg.Audit == nil {
		return
	}
	detail, err := json.Marshal(map[string]any{
		"remote":         result.Remote,
		"ref":            p.cfg.Ref,
		"path":           result.Path,
		"branch":         result.Branch,
		"commitSha":      result.CommitSHA,
		"pullRequestUrl": result.PullRequestURL,
		"findingId":      req.FindingID,
		"refs":           refLabels(req.Refs),
		"created":        result.Created,
	})
	if err != nil {
		p.log.Error("gitsync: encoding audit detail for an adoption", "error", err)
		return
	}
	action := "drift.adopt.update"
	if result.Created {
		action = "drift.adopt"
	}
	_, _ = p.cfg.Audit.Append(ctx, store.AuditEntry{
		At:         p.now().Unix(),
		Username:   result.ProposedBy,
		Action:     action,
		Result:     "ok",
		DetailJSON: sql.NullString{String: string(detail), Valid: true},
	})
}
