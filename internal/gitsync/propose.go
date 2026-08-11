package gitsync

// propose.go implements T-2702: a changeset staged in vnprox becomes a pull
// request against the repository that holds the cluster's intent.
//
// # The loop this closes
//
// T-2701 made a git repository the source of *intent*. Without this file, a
// change made in the vnprox GUI is a change made outside that system of
// record: the cluster moves, the repository does not, and the next sync
// reports the operator's own change back to them as divergence. Proposing
// puts the GUI change where the intent lives, as a reviewable commit.
//
// # What it does NOT do
//
// **vnprox does not merge, gate, or poll a pull request.** It opens one and
// stops. Whatever happens next — approval, changes requested, a merge, a
// close — comes back through T-2701's ordinary sync, which stages a draft
// changeset a human applies through the change engine. There is no method
// here that merges and no timer that watches; the Host seam (host.go) has no
// verb for either, so neither could be added without editing that interface.
//
// # The round-trip is CHECKED, not assumed (AC1)
//
// The proposed document is not trusted to mean what the changeset meant. For
// base document B (the file as the repository has it today), live snapshot L
// and the changeset's ops O, this file requires
//
//	Import(ApplyOps(B, O), L)  minus  Import(B, L)  ==  O      (as op sets)
//	Import(B, L)  minus  Import(ApplyOps(B, O), L)  ==  {}
//
// before a single host call that writes anything. The comparison is
// SEMANTIC: op ids are excluded (they are assigned by the change engine),
// ordering is excluded (Import emits its own deterministic order), and
// set-valued params (ports, slaves, addresses, dhcp ranges) are compared as
// sets, exactly the way Import's own diff compares them. A changeset whose
// spec rendering would plan to something else — a delete, a flag the document
// cannot express as false, a field the repository already declares
// differently — is refused with the specific difference named, because a pull
// request that does not mean what the changeset meant is worse than no pull
// request at all.
//
// # Ordering, and why there is no orphan branch (AC3)
//
// Every host write is ordered so the compensating action is always available:
// resolve the base sha, create the branch (remembering whether WE created
// it), commit, then open or update the request. Any failure after a branch we
// created deletes that branch before returning. The post-condition is the one
// AC3 asks for: either the branch and the pull request both exist, or
// neither does.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/change/ifaces"
	"github.com/bgovanlu/vnprox/internal/spec"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// DefaultBranchPrefix is what a proposal's branch is named after. The
// changeset id follows it, which is what makes the branch DETERMINISTIC:
// proposing the same changeset twice addresses the same branch and therefore
// the same pull request (AC4). It carries no credential and no user-supplied
// text — a ULID and this constant, nothing else (AC5).
const DefaultBranchPrefix = "vnprox/changeset-"

// ProposeAuthor is the audit actor recorded when a proposal is made without
// an authenticated user (a direct API-less call). The ordinary path records
// the acting user's own name.
const ProposeAuthor = "system:gitsync"

// ChangesetReader is the ONLY change-engine surface the Proposer holds: one
// read.
//
// Proposing is a pure read of a changeset plus writes to a git host. It does
// not stage, edit, validate, apply, confirm or discard anything — so this
// interface has one method, and an applying (or even mutating) propose path
// cannot be written without editing it. Same structural stance as
// ChangesetStager (service.go), one step further: the sync path may stage,
// the propose path may not even do that.
type ChangesetReader interface {
	Get(ctx context.Context, id string) (change.Changeset, error)
}

// ProposalStore persists what was opened, so the review surface can link it
// and a second propose can find it.
type ProposalStore interface {
	Get(ctx context.Context, changesetID string) (store.ChangesetProposal, error)
	Upsert(ctx context.Context, p store.ChangesetProposal) error
}

// PreviewSource is the optional T-2605 post-apply projection seam. When it is
// nil — which it is until T-2605 lands — the pull-request body says the
// projection is unavailable in this build rather than silently omitting a
// section the card asks for.
type PreviewSource interface {
	PreviewSummary(ctx context.Context, changesetID string) (string, error)
}

// Proposal is one opened (or brought-up-to-date) pull request.
//
//nolint:govet // fieldalignment: field order is the wire/report shape, not packing.
type Proposal struct {
	ChangesetID    string `json:"changesetId"`
	Remote         string `json:"remote"`
	Branch         string `json:"branch"`
	Path           string `json:"path"`
	CommitSHA      string `json:"commitSha,omitempty"`
	PullRequestID  string `json:"pullRequestId,omitempty"`
	PullRequestURL string `json:"pullRequestUrl,omitempty"`
	ProposedBy     string `json:"proposedBy,omitempty"`
	ProposedAt     int64  `json:"proposedAt,omitempty"`
	UpdatedAt      int64  `json:"updatedAt,omitempty"`
	// Created reports whether this call opened a new request (true) or
	// updated the one that was already open for this changeset (false).
	Created bool `json:"created"`
}

// ProposerConfig wires a Proposer. A zero value is inert: Enabled false (or a
// nil Host) means every Propose call answers ErrProposeNotConfigured without
// contacting anything.
//
//nolint:govet // fieldalignment: field order mirrors the [gitsync] config section, then the seams.
type ProposerConfig struct {
	Enabled bool
	// Source reads the base document. It is T-2701's READ seam, unchanged and
	// unextended — the Proposer holds one of each rather than one type that
	// does both (host.go's doc comment).
	Source Source
	// Host is the write seam.
	Host Host
	// Ref is the branch a proposal is based on and proposed INTO.
	Ref string
	// Path is the spec document's path within the repository.
	Path string
	// BranchPrefix overrides DefaultBranchPrefix.
	BranchPrefix string

	Changesets ChangesetReader
	Inventory  InventorySource
	Proposals  ProposalStore
	Audit      Auditor
	Preview    PreviewSource
	// MgmtPaths optionally supplies the resolved management paths T-2404's
	// blast radius evaluates touchesMgmtPath against. Nil is safe and
	// honest: the blast radius then reports it as not evaluated rather than
	// as false.
	MgmtPaths func(ctx context.Context) map[string][]topology.MgmtPath

	Logger *slog.Logger
	Now    func() time.Time
}

// Proposer turns a changeset into a pull request.
//
//nolint:govet // fieldalignment: wiring first, then the mutex and what it guards.
type Proposer struct {
	cfg ProposerConfig
	log *slog.Logger
	now func() time.Time
	// mu serializes proposals. Two concurrent proposals of the SAME changeset
	// would race on the same branch (both seeing it absent, both creating
	// it); two of different changesets would not, but proposing is a rare,
	// operator-initiated act against a rate-limited host API, so one lock for
	// all of them costs nothing and removes the whole class.
	mu sync.Mutex
}

// NewProposer builds a Proposer. It performs no I/O.
func NewProposer(cfg ProposerConfig) *Proposer {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	if cfg.Ref == "" {
		cfg.Ref = "main"
	}
	if cfg.BranchPrefix == "" {
		cfg.BranchPrefix = DefaultBranchPrefix
	}
	return &Proposer{cfg: cfg, log: logger, now: now}
}

// Enabled reports whether this Proposer can propose anything at all.
func (p *Proposer) Enabled() bool {
	return p.cfg.Enabled && p.cfg.Host != nil && p.cfg.Source != nil && p.cfg.Changesets != nil
}

// Describe returns the credential-free description of where proposals go.
func (p *Proposer) Describe() string {
	if p.cfg.Host == nil {
		return ""
	}
	return p.cfg.Host.Describe()
}

// Get returns the proposal recorded for changesetID, or ErrNoProposal.
func (p *Proposer) Get(ctx context.Context, changesetID string) (Proposal, error) {
	if p.cfg.Proposals == nil {
		return Proposal{}, ErrNoProposal
	}
	row, err := p.cfg.Proposals.Get(ctx, changesetID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Proposal{}, ErrNoProposal
		}
		return Proposal{}, fmt.Errorf("gitsync: reading the proposal for changeset %s: %w", changesetID, err)
	}
	return proposalFromRow(row), nil
}

// Propose renders changesetID as a spec delta, commits it on a deterministic
// branch, and opens (or updates) a pull request for it.
//
//nolint:gocyclo // one linear transaction: read -> render -> verify -> branch -> commit -> request, with one compensating path. Splitting it would hide the ordering AC3 rests on.
func (p *Proposer) Propose(ctx context.Context, changesetID, actor string) (Proposal, error) {
	if !p.Enabled() {
		return Proposal{}, ErrProposeNotConfigured
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	cs, err := p.cfg.Changesets.Get(ctx, changesetID)
	if err != nil {
		return Proposal{}, fmt.Errorf("gitsync: reading changeset %s to propose it: %w", changesetID, err)
	}
	if statusErr := proposable(cs); statusErr != nil {
		return Proposal{}, statusErr
	}

	baseDoc, baseSpec, err := p.readBase(ctx)
	if err != nil {
		return Proposal{}, err
	}

	proposedSpec, err := spec.ApplyOps(baseSpec, cs.Ops)
	if err != nil {
		return Proposal{}, fmt.Errorf("%w: %w", ErrNotExpressible, err)
	}
	if tripErr := p.verifyRoundTrip(baseSpec, proposedSpec, cs.Ops); tripErr != nil {
		return Proposal{}, tripErr
	}
	proposedDoc, err := spec.Marshal(proposedSpec)
	if err != nil {
		return Proposal{}, fmt.Errorf("gitsync: rendering the proposed spec for changeset %s: %w", cs.ID, err)
	}

	body := p.pullRequestBody(ctx, cs, string(baseDoc), string(proposedDoc))
	in := PullRequestInput{
		Branch: p.branchName(cs.ID),
		Base:   p.cfg.Ref,
		Title:  proposalTitle(cs),
		Body:   body,
	}

	result, err := p.publish(ctx, in, proposedDoc, cs)
	if err != nil {
		return Proposal{}, err
	}
	result.ProposedBy = firstNonEmptyString(actor, ProposeAuthor)
	p.record(ctx, &result)
	p.audit(ctx, result, len(cs.Ops))
	p.log.Info("gitsync: proposed a changeset as a pull request",
		"changesetId", cs.ID, "branch", result.Branch, "pullRequest", result.PullRequestURL,
		"created", result.Created, "remote", p.cfg.Host.Describe())
	return result, nil
}

// publish performs the host writes in the order AC3 requires, and undoes a
// branch it created if anything after that fails.
func (p *Proposer) publish(ctx context.Context, in PullRequestInput, content []byte, cs change.Changeset) (Proposal, error) {
	host := p.cfg.Host
	result := Proposal{
		ChangesetID: cs.ID, Remote: host.Describe(), Branch: in.Branch, Path: p.cfg.Path,
	}

	baseSHA, err := host.ResolveRef(ctx, p.cfg.Ref)
	if err != nil {
		return Proposal{}, fmt.Errorf("gitsync: resolving %s to propose changeset %s: %w", p.cfg.Ref, cs.ID, err)
	}

	_, branchExisted, err := host.BranchHead(ctx, in.Branch)
	if err != nil {
		return Proposal{}, fmt.Errorf("gitsync: checking branch %s: %w", in.Branch, err)
	}
	createdBranch := false
	if !branchExisted {
		if createErr := host.CreateBranch(ctx, in.Branch, baseSHA); createErr != nil {
			// Nothing was written: no branch, no commit, no request.
			return Proposal{}, createErr
		}
		createdBranch = true
	}

	// abandon is the compensating action. It runs on EVERY failure past the
	// branch creation, and only for a branch this call created: a branch that
	// already existed carries a previous proposal a human may be reviewing,
	// and deleting it would destroy that.
	abandon := func(cause error) (Proposal, error) {
		if !createdBranch {
			return Proposal{}, cause
		}
		if delErr := host.DeleteBranch(ctx, in.Branch); delErr != nil {
			// Report both: the operator needs to know a branch was left
			// behind, and why the proposal failed in the first place.
			return Proposal{}, fmt.Errorf("%w (and the branch %s created for it could not be removed: %v)", cause, in.Branch, delErr)
		}
		p.log.Warn("gitsync: proposal failed; removed the branch it had created",
			"changesetId", cs.ID, "branch", in.Branch, "error", cause)
		return Proposal{}, cause
	}

	// Commit only if the branch does not already carry byte-identical
	// content: re-proposing an unchanged changeset should refresh the pull
	// request, not stack empty commits on its branch.
	existing, hasFile, err := host.ReadFile(ctx, in.Branch, p.cfg.Path)
	if err != nil {
		return abandon(fmt.Errorf("gitsync: reading %s on %s: %w", p.cfg.Path, in.Branch, err))
	}
	if !hasFile || string(existing) != string(content) {
		commitSHA, commitErr := host.CommitFile(ctx, CommitRequest{
			Branch: in.Branch, Path: p.cfg.Path, Message: commitMessage(cs), Content: content,
		})
		if commitErr != nil {
			return abandon(commitErr)
		}
		result.CommitSHA = commitSHA
	}

	open, found, err := host.FindOpenPullRequest(ctx, in.Branch)
	if err != nil {
		return abandon(fmt.Errorf("gitsync: looking for an open pull request on %s: %w", in.Branch, err))
	}
	var pr PullRequest
	if found {
		// AC4: the same changeset updates the same request, never a second.
		if pr, err = host.UpdatePullRequest(ctx, open.ID, in); err != nil {
			return abandon(err)
		}
	} else {
		if pr, err = host.OpenPullRequest(ctx, in); err != nil {
			return abandon(err)
		}
		result.Created = true
	}
	result.PullRequestID = pr.ID
	result.PullRequestURL = pr.URL
	return result, nil
}

// readBase fetches the spec document the proposal is based on. A repository
// with no document at the configured path is refused rather than invented:
// writing a whole-cluster spec as a side effect of proposing one bridge edit
// would adopt every piece of live state into intent at once, which is
// T-2703's explicit, human-initiated decision and never this path's.
func (p *Proposer) readBase(ctx context.Context) ([]byte, spec.Spec, error) {
	rev, err := p.cfg.Source.Fetch(ctx, p.cfg.Ref, p.cfg.Path)
	if err != nil {
		if errors.Is(err, ErrRemoteNotFound) {
			return nil, spec.Spec{}, fmt.Errorf("%w: %s at %s", ErrNoSpecDocument, p.cfg.Path, p.cfg.Ref)
		}
		return nil, spec.Spec{}, fmt.Errorf("gitsync: reading %s at %s to base a proposal on: %w", p.cfg.Path, p.cfg.Ref, err)
	}
	parsed, err := spec.Parse(rev.Content)
	if err != nil {
		return nil, spec.Spec{}, fmt.Errorf("%w: %s at %s: %w", ErrSpecParse, p.cfg.Path, p.cfg.Ref, err)
	}
	return rev.Content, parsed, nil
}

// verifyRoundTrip is AC1, enforced in production rather than only in a test:
// the proposed document must plan, against live state, to exactly the base
// document's plan plus this changeset's ops — no more, no less.
func (p *Proposer) verifyRoundTrip(baseSpec, proposedSpec spec.Spec, ops []change.Op) error {
	snap := p.cfg.Inventory.Snapshot()

	basePlan, _, err := spec.Import(baseSpec, snap)
	if err != nil {
		return fmt.Errorf("gitsync: planning the current spec against live state: %w", err)
	}
	proposedPlan, _, err := spec.Import(proposedSpec, snap)
	if err != nil {
		return fmt.Errorf("gitsync: planning the proposed spec against live state: %w", err)
	}

	added, err := opsExcept(proposedPlan, basePlan)
	if err != nil {
		return err
	}
	removed, err := opsExcept(basePlan, proposedPlan)
	if err != nil {
		return err
	}
	if len(added) == 0 && len(removed) == 0 {
		// AC2: nothing to propose. The ops are real, but the document already
		// says what they would say — a pull request with an empty diff.
		return fmt.Errorf("%w: the spec at %s already describes this changeset's intent, so there is nothing to propose",
			ErrNothingToPropose, p.cfg.Path)
	}
	if len(removed) > 0 {
		return fmt.Errorf("%w: the spec at %s declares %s differently from this changeset; reconcile that divergence first (the proposal would silently drop it)",
			ErrRoundTrip, p.cfg.Path, strings.Join(opLabels(removed), ", "))
	}

	wantKeys, err := opKeys(ops)
	if err != nil {
		return err
	}
	gotKeys, err := opKeys(added)
	if err != nil {
		return err
	}
	if !sameMultiset(wantKeys, gotKeys) {
		return fmt.Errorf("%w: re-importing the proposed spec plans to %s, not to this changeset's %s — the change is not expressible in the spec as written",
			ErrRoundTrip, describeOps(added), describeOps(ops))
	}
	return nil
}

// --- pull-request text -----------------------------------------------------

func proposalTitle(cs change.Changeset) string {
	title := strings.TrimSpace(cs.Title)
	if title == "" {
		title = "network change"
	}
	return fmt.Sprintf("vnprox: %s", title)
}

// commitMessage names the changeset and summarises its ops. It carries no
// credential: the only inputs are the changeset's own title/id and op types
// (AC5).
func commitMessage(cs change.Changeset) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "spec: %s\n\n", strings.TrimSpace(firstNonEmptyString(cs.Title, "network change")))
	fmt.Fprintf(&sb, "Staged in vnprox as changeset %s by %s.\n\n", cs.ID, cs.Author)
	for _, line := range opLabels(cs.Ops) {
		fmt.Fprintf(&sb, "  - %s\n", line)
	}
	return sb.String()
}

// pullRequestBody renders the review context the card requires to travel WITH
// the review: what changes in the document, what it would disrupt (T-2404's
// blast radius), and the post-apply projection (T-2605) when this build has
// one.
func (p *Proposer) pullRequestBody(ctx context.Context, cs change.Changeset, baseDoc, proposedDoc string) string {
	var sb strings.Builder

	sb.WriteString("Opened by **vnprox** for changeset `" + cs.ID + "`.\n\n")
	sb.WriteString("This change was staged in the vnprox GUI against the live cluster; this request is the same change expressed in the spec, so the repository stays the source of intent.\n\n")
	fmt.Fprintf(&sb, "| | |\n|---|---|\n| Changeset | `%s` |\n| Title | %s |\n| Staged by | %s |\n| Origin | %s |\n| Ops | %d |\n\n",
		cs.ID, strings.TrimSpace(cs.Title), cs.Author, firstNonEmptyString(cs.Origin, change.OriginUI), len(cs.Ops))

	sb.WriteString("## Ops\n\n")
	for _, line := range opLabels(cs.Ops) {
		sb.WriteString("- `" + line + "`\n")
	}
	sb.WriteString("\n## Spec diff\n\n```diff\n")
	sb.WriteString(ifaces.UnifiedDiff(p.cfg.Path, p.cfg.Path, baseDoc, proposedDoc))
	sb.WriteString("```\n\n")

	sb.WriteString(p.blastRadiusSection(ctx, cs))
	sb.WriteString(p.previewSection(ctx, cs))

	sb.WriteString("---\n\n")
	sb.WriteString("vnprox does not merge, gate, or poll this request. Once it lands, the change returns through the ordinary git spec sync, which opens a draft changeset for a human to apply through the change engine — nothing here is applied automatically.\n")
	return sb.String()
}

// blastRadiusSection renders T-2404's server-computed impact. It is display
// metadata and says so: the enforcement backstops are the change engine's
// validation and the management-path ceremony, not this paragraph.
func (p *Proposer) blastRadiusSection(ctx context.Context, cs change.Changeset) string {
	var paths map[string][]topology.MgmtPath
	if p.cfg.MgmtPaths != nil {
		paths = p.cfg.MgmtPaths(ctx)
	}
	impact := change.ComputeImpact(cs.Ops, p.cfg.Inventory.Snapshot(), paths, nil, nil)

	var sb strings.Builder
	sb.WriteString("## Blast radius\n\n")
	fmt.Fprintf(&sb, "**Disruption: %s.**\n\n", impact.Disruption)
	if len(impact.Nodes) > 0 {
		sb.WriteString("- Nodes: " + strings.Join(impact.Nodes, ", ") + "\n")
	}
	if len(impact.Carriers) > 0 {
		sb.WriteString("- Carriers: " + strings.Join(impact.Carriers, ", ") + "\n")
	}
	if len(impact.Guests) == 0 {
		sb.WriteString("- Guests affected: none\n")
	} else {
		fmt.Fprintf(&sb, "- Guests affected: %d\n", len(impact.Guests))
		for _, g := range impact.Guests {
			fmt.Fprintf(&sb, "  - %s (`%s`) via %s on %s\n", firstNonEmptyString(g.Name, g.Ref), g.Ref, g.NIC, g.Carrier)
		}
	}
	switch {
	case p.cfg.MgmtPaths == nil:
		sb.WriteString("- Management path: not evaluated by this deployment's proposer\n")
	case impact.TouchesMgmtPath:
		sb.WriteString("- **Management path: touched.** Applying this needs the guided management-path ceremony.\n")
	default:
		sb.WriteString("- Management path: not touched\n")
	}
	sb.WriteString("\nPer-op verdicts:\n\n")
	for _, op := range impact.Ops {
		fmt.Fprintf(&sb, "- `%s %s` — %s (%s)\n", op.Op, op.Target, op.Disruption, op.Reason)
	}
	sb.WriteString("\n")
	return sb.String()
}

func (p *Proposer) previewSection(ctx context.Context, cs change.Changeset) string {
	if p.cfg.Preview == nil {
		return "## Post-apply preview\n\nNot available in this build.\n\n"
	}
	summary, err := p.cfg.Preview.PreviewSummary(ctx, cs.ID)
	if err != nil {
		p.log.Warn("gitsync: could not render the post-apply preview for a proposal", "changesetId", cs.ID, "error", err)
		return "## Post-apply preview\n\nCould not be computed for this changeset.\n\n"
	}
	return "## Post-apply preview\n\n" + summary + "\n\n"
}

// --- bookkeeping -----------------------------------------------------------

func (p *Proposer) branchName(changesetID string) string {
	return p.cfg.BranchPrefix + changesetID
}

// record persists the proposal. A failure here is logged and reported on the
// result rather than returned as the call's error: the pull request exists by
// this point, and answering "failed" for something that succeeded would send
// an operator looking for a request they would then open a second time.
func (p *Proposer) record(ctx context.Context, result *Proposal) {
	now := p.now().Unix()
	result.UpdatedAt = now
	result.ProposedAt = now
	if p.cfg.Proposals == nil {
		return
	}
	if prev, err := p.cfg.Proposals.Get(ctx, result.ChangesetID); err == nil && prev.CreatedAt > 0 {
		result.ProposedAt = prev.CreatedAt
	}
	row := store.ChangesetProposal{
		ChangesetID: result.ChangesetID, Remote: result.Remote, Branch: result.Branch, Path: result.Path,
		CommitSHA: result.CommitSHA, PRID: result.PullRequestID, PRURL: result.PullRequestURL,
		ProposedBy: result.ProposedBy, CreatedAt: result.ProposedAt, UpdatedAt: now,
	}
	if err := p.cfg.Proposals.Upsert(ctx, row); err != nil {
		p.log.Error("gitsync: the pull request was opened but could not be recorded",
			"changesetId", result.ChangesetID, "pullRequest", result.PullRequestURL, "error", err)
	}
}

// audit writes one row per proposal. It records the branch, the request URL
// and the op count — never the document and never the credential
// (docs/security.md: an audit detail never carries a secret).
func (p *Proposer) audit(ctx context.Context, result Proposal, opCount int) {
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
		"opCount":        opCount,
		"created":        result.Created,
	})
	if err != nil {
		p.log.Error("gitsync: encoding audit detail for a proposal", "error", err)
		return
	}
	action := "changeset.propose.update"
	if result.Created {
		action = "changeset.propose"
	}
	_, _ = p.cfg.Audit.Append(ctx, store.AuditEntry{
		At:          p.now().Unix(),
		Username:    result.ProposedBy,
		Action:      action,
		Result:      "ok",
		ChangesetID: sql.NullString{String: result.ChangesetID, Valid: result.ChangesetID != ""},
		DetailJSON:  sql.NullString{String: string(detail), Valid: true},
	})
}

func proposalFromRow(row store.ChangesetProposal) Proposal {
	return Proposal{
		ChangesetID: row.ChangesetID, Remote: row.Remote, Branch: row.Branch, Path: row.Path,
		CommitSHA: row.CommitSHA, PullRequestID: row.PRID, PullRequestURL: row.PRURL,
		ProposedBy: row.ProposedBy, ProposedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

// proposable refuses the changesets whose ops are not a statement of intent.
//
// An empty changeset has nothing to say (AC2). A discarded, rolled-back or
// failed one had its ops abandoned or undone — writing them into the document
// that defines what the cluster SHOULD look like would be recording an
// intention nobody holds.
func proposable(cs change.Changeset) error {
	if len(cs.Ops) == 0 {
		return fmt.Errorf("%w: changeset %s has no ops", ErrNothingToPropose, cs.ID)
	}
	switch cs.Status {
	case change.StatusDiscarded, change.StatusRolledBack, change.StatusFailed:
		return fmt.Errorf("%w: changeset %s is %s; its ops were abandoned or undone and are not a statement of intent",
			ErrNotProposable, cs.ID, cs.Status)
	case change.StatusDraft, change.StatusValidated, change.StatusRequested,
		change.StatusApplying, change.StatusAwaitingConfirm, change.StatusCommitted:
		return nil
	default:
		return nil
	}
}

// --- semantic op comparison ------------------------------------------------

// opKey renders one op as a comparable string: its type, its target and its
// params, with the op's own id excluded (assigned by the change engine at
// create time, so including it would make every re-plan look different) and
// with every string list sorted.
//
// Sorting the lists is not a shortcut — it is the same equality Import's own
// diff uses (setEqual over ports, slaves, addresses, dhcp ranges, zone
// nodes), so two ops that Import would treat as identical compare identical
// here. That is what makes AC1's round-trip assertion SEMANTIC rather than
// textual.
func opKey(op change.Op) (string, error) {
	raw, err := json.Marshal(op.Params)
	if err != nil {
		return "", fmt.Errorf("gitsync: encoding op %s %s for comparison: %w", op.Type, op.Target, err)
	}
	var decoded any
	if len(raw) > 0 {
		if decErr := json.Unmarshal(raw, &decoded); decErr != nil {
			return "", fmt.Errorf("gitsync: decoding op %s %s for comparison: %w", op.Type, op.Target, decErr)
		}
	}
	canonical, err := json.Marshal(sortStringLists(decoded))
	if err != nil {
		return "", fmt.Errorf("gitsync: canonicalising op %s %s for comparison: %w", op.Type, op.Target, err)
	}
	return string(op.Type) + "|" + op.Target.String() + "|" + string(canonical), nil
}

// sortStringLists walks a decoded JSON value and sorts every array whose
// elements are all strings. json.Marshal already emits object keys in sorted
// order, so the result is canonical.
func sortStringLists(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = sortStringLists(val)
		}
		return t
	case []any:
		allStrings := true
		for i, val := range t {
			t[i] = sortStringLists(val)
			if _, ok := val.(string); !ok {
				allStrings = false
			}
		}
		if allStrings {
			sort.Slice(t, func(i, j int) bool {
				a, _ := t[i].(string)
				b, _ := t[j].(string)
				return a < b
			})
		}
		return t
	default:
		return v
	}
}

func opKeys(ops []change.Op) ([]string, error) {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		k, err := opKey(op)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

// opsExcept returns the multiset difference a \ b.
func opsExcept(a, b []change.Op) ([]change.Op, error) {
	counts := map[string]int{}
	for _, op := range b {
		k, err := opKey(op)
		if err != nil {
			return nil, err
		}
		counts[k]++
	}
	var out []change.Op
	for _, op := range a {
		k, err := opKey(op)
		if err != nil {
			return nil, err
		}
		if counts[k] > 0 {
			counts[k]--
			continue
		}
		out = append(out, op)
	}
	return out, nil
}

func sameMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[string]int{}
	for _, s := range a {
		counts[s]++
	}
	for _, s := range b {
		counts[s]--
		if counts[s] < 0 {
			return false
		}
	}
	return true
}

func opLabels(ops []change.Op) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, fmt.Sprintf("%s %s", op.Type, op.Target.String()))
	}
	return out
}

func describeOps(ops []change.Op) string {
	if len(ops) == 0 {
		return "nothing"
	}
	return strings.Join(opLabels(ops), ", ")
}
