// SPDX-License-Identifier: Apache-2.0

package gitsync

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/spec"
	"github.com/bgovanlu/vnprox/internal/store"
)

// DefaultPollInterval is the cadence a configured sync polls at when
// `[gitsync] poll_interval` is unset. Five minutes: a spec repository is
// edited by humans through review, not by a machine in a loop, so a tighter
// cadence buys nothing and costs a request against a rate-limited host API.
const DefaultPollInterval = 5 * time.Minute

// SyncAuthor is the author recorded on every changeset this package stages.
// It is deliberately a `system:`-prefixed identity, matching the
// `system:rollback` convention docs/security.md's Audit section already
// establishes for a daemon-triggered action: nobody should be able to read a
// changeset list and mistake a sync draft for a person's edit.
const SyncAuthor = "system:gitsync"

// ChangesetStager is the ONLY change-engine surface this package holds.
//
// It has no Apply, Confirm, Rollback or Discard method — that omission is
// this card's central safety property expressed as a type, the same way
// internal/mcp's ChangesetStager (T-1701) and internal/plugin's Stager
// (T-1702) express theirs. A gitsync code path that applied a changeset
// could not be written without editing this interface, which is a reviewable
// event; an interface-surface test asserts the omission so the edit also
// fails the build.
type ChangesetStager interface {
	CreateWithOrigin(ctx context.Context, author, title string, ops []change.Op, origin, originTokenID string) (change.Changeset, error)
	UpdateDraft(ctx context.Context, id, author string, title *string, ops []change.Op) (change.Changeset, error)
	List(ctx context.Context, status string) ([]change.Changeset, error)
}

// InventorySource is the live-state seam spec.Import diffs against —
// *inventory.Graph satisfies it, the same one-method seam internal/api's
// SpecInventory already declares.
type InventorySource interface {
	Snapshot() inventory.Snapshot
}

// Auditor is the audit-log seam — *store.AuditRepo satisfies it directly,
// the same one-method seam internal/api's specPinAuditor declares.
type Auditor interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

// Finding check names this package produces. They are prefixed `gitsync_`
// so they cannot collide with any other producer's key space.
const (
	CheckUnreachable           = "gitsync_unreachable"
	CheckSpecUnparseable       = "gitsync_spec_unparseable"
	CheckCommitUnsigned        = "gitsync_commit_unsigned"
	CheckSignatureUnverifiable = "gitsync_signature_unverifiable"
	CheckDivergence            = "gitsync_divergence"
)

// Issue is one condition worth surfacing in the findings stream. It is a
// plain value rather than a findings.Finding so this package does not depend
// on internal/findings; cmd/vnproxd adapts it, the same shape every other
// out-of-package producer (federation, peer trust, store capacity) uses.
type Issue struct {
	Check    string `json:"check"`
	Severity string `json:"severity"`
	Detail   string `json:"detail"`
}

// Status is what `vnproxctl gitsync status` and `GET /gitsync/status`
// render: the last fetched sha, the last plan, and why the current draft
// exists.
//
// Nothing in this struct is or contains a credential. Remote is the
// operator's URL with any userinfo already refused at construction, and no
// field is ever populated from a token — asserted per-surface by
// TestCredentialNeverLeaks (T-2701 AC6).
//
//nolint:govet // fieldalignment: wire shape; field order is the documented JSON contract, not packing.
type Status struct {
	Enabled              bool     `json:"enabled"`
	Remote               string   `json:"remote,omitempty"`
	Ref                  string   `json:"ref,omitempty"`
	Path                 string   `json:"path,omitempty"`
	PollIntervalSeconds  int      `json:"pollIntervalSeconds,omitempty"`
	RequireSignedCommits bool     `json:"requireSignedCommits"`
	LastFetchedSHA       string   `json:"lastFetchedSha,omitempty"`
	LastFetchAt          int64    `json:"lastFetchAt,omitempty"`
	LastSuccessAt        int64    `json:"lastSuccessAt,omitempty"`
	LastSigner           string   `json:"lastSigner,omitempty"`
	LastError            string   `json:"lastError,omitempty"`
	PlanOpCount          int      `json:"planOpCount"`
	Plan                 []string `json:"plan,omitempty"`
	NotInSpec            []string `json:"notInSpec,omitempty"`
	OpenChangesetID      string   `json:"openChangesetId,omitempty"`
	OpenChangesetReason  string   `json:"openChangesetReason,omitempty"`
	Issues               []Issue  `json:"issues,omitempty"`
}

// Result reports what one Sync cycle did, for tests and for the caller's own
// logging. It never carries the fetched content.
type Result struct {
	// SHA is the revision the cycle read, when it got that far.
	SHA string
	// ChangesetID is the draft this cycle opened or updated, or "".
	ChangesetID string
	// Created is true when this cycle opened a new draft (as opposed to
	// updating the existing one, or doing nothing).
	Created bool
	// Updated is true when this cycle rewrote the existing draft's ops.
	Updated bool
	// Unchanged is true when the revision matched the last one already
	// reconciled, so the cycle did no planning and no store write at all.
	Unchanged bool
	// OpCount is the size of the plan the spec produced against live state.
	OpCount int
}

// Config wires a Service. Every field except Logger/Now is required for an
// enabled service; a Service built with Enabled false is inert — it contacts
// no endpoint and writes nothing, which is the "off by default" guarantee.
//
//nolint:govet // fieldalignment: field order mirrors the [gitsync] config section, then the seams — reviewable against docs/deployment.md, not packed.
type Config struct {
	Enabled              bool
	Source               Source
	Ref                  string
	Path                 string
	PollInterval         time.Duration
	RequireSignedCommits bool
	AllowedSigners       []AllowedSigner
	Changesets           ChangesetStager
	Inventory            InventorySource
	Audit                Auditor
	Logger               *slog.Logger
	Now                  func() time.Time
}

// Service is the poll loop. It fetches, verifies, plans, and opens or
// updates exactly one draft changeset — and stops.
//
//nolint:govet // fieldalignment: fields are grouped by lifetime (immutable wiring, then the mutex and everything it guards); regrouping for packing would separate the mutex from its own state.
type Service struct {
	cfg    Config
	log    *slog.Logger
	now    func() time.Time
	source Source

	mu sync.Mutex
	// lastRev is the (sha, content digest) pair of the revision most
	// recently reconciled. A poll that reads the same pair does no planning
	// and no store write (T-2701 AC2).
	lastRev revKey
	// lastPlanFP fingerprints the op list currently reflected in the open
	// draft, so a re-plan producing the same ops does not rewrite it.
	lastPlanFP string
	status     Status
	issues     map[string]Issue
}

type revKey struct {
	sha    string
	digest string
}

// New builds a Service. It performs no I/O and starts nothing; Run does that.
func New(cfg Config) *Service {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.Ref == "" {
		cfg.Ref = "main"
	}

	s := &Service{cfg: cfg, log: logger, now: now, source: cfg.Source, issues: map[string]Issue{}}
	s.status = Status{
		Enabled:              cfg.Enabled,
		Ref:                  cfg.Ref,
		Path:                 cfg.Path,
		PollIntervalSeconds:  int(cfg.PollInterval / time.Second),
		RequireSignedCommits: cfg.RequireSignedCommits,
	}
	if cfg.Source != nil {
		s.status.Remote = cfg.Source.Describe()
	}
	if !cfg.Enabled {
		// A disabled service reports nothing but the fact that it is off:
		// no remote, no ref, no path, nothing that could imply it is running.
		s.status = Status{Enabled: false}
	}
	return s
}

// Enabled reports whether this service will poll at all.
func (s *Service) Enabled() bool { return s.cfg.Enabled && s.source != nil }

// Status returns a snapshot of the current sync state. Safe for concurrent
// use; the returned slices are copies.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.status
	out.Plan = append([]string(nil), s.status.Plan...)
	out.NotInSpec = append([]string(nil), s.status.NotInSpec...)
	out.Issues = s.sortedIssuesLocked()
	return out
}

// Issues returns the current findings-worthy conditions, deterministically
// ordered. cmd/vnproxd adapts these into internal/findings' unified stream.
func (s *Service) Issues() []Issue {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sortedIssuesLocked()
}

func (s *Service) sortedIssuesLocked() []Issue {
	out := make([]Issue, 0, len(s.issues))
	for _, iss := range s.issues {
		out = append(out, iss)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Check < out[j].Check })
	return out
}

func (s *Service) raise(check, severity, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issues[check] = Issue{Check: check, Severity: severity, Detail: detail}
}

func (s *Service) clear(checks ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range checks {
		delete(s.issues, c)
	}
}

// Run is the supervised actor cmd/vnproxd registers in its run group.
//
// It blocks for the daemon's lifetime and returns **nil** on context
// cancellation — never an error, because runGroup stops every other actor as
// soon as one returns, so a sync that gave up would take the daemon with it.
// A disabled service still blocks (and still contacts nothing), so wiring is
// unconditional and the "off" case needs no branch at the call site.
//
// No failure inside a cycle escapes: an unreachable remote, a bad signature
// and an unparseable document all become findings and a retry on the next
// tick (T-2701 AC7).
func (s *Service) Run(ctx context.Context) error {
	if !s.Enabled() {
		<-ctx.Done()
		return nil
	}
	s.log.Info("gitsync: polling for spec changes",
		"remote", s.source.Describe(), "ref", s.cfg.Ref, "path", s.cfg.Path,
		"interval", s.cfg.PollInterval, "requireSignedCommits", s.cfg.RequireSignedCommits)

	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()
	for {
		// The first cycle runs immediately rather than after a full
		// interval, but it runs *here*, inside the actor's own goroutine —
		// so a remote that hangs delays nothing but this loop.
		if err := s.syncOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("gitsync: sync cycle failed, will retry", "error", err, "retryIn", s.cfg.PollInterval)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Sync runs exactly one cycle and reports what it did. Exported for tests
// and for a future operator-triggered "sync now"; it is the same code path
// Run drives.
func (s *Service) Sync(ctx context.Context) (Result, error) {
	if !s.Enabled() {
		return Result{}, ErrNotConfigured
	}
	return s.sync(ctx)
}

func (s *Service) syncOnce(ctx context.Context) error {
	_, err := s.sync(ctx)
	return err
}

//nolint:gocyclo // one linear cycle: fetch -> verify -> parse -> plan -> stage; splitting it would hide the order that matters.
func (s *Service) sync(ctx context.Context) (Result, error) {
	rev, err := s.source.Fetch(ctx, s.cfg.Ref, s.cfg.Path)
	s.noteFetchAttempt()
	if err != nil {
		return Result{}, s.onFetchError(err)
	}
	s.clear(CheckUnreachable)

	signer, err := s.verifySignature(rev)
	if err != nil {
		s.recordError(err)
		return Result{SHA: rev.SHA}, err
	}
	s.clear(CheckCommitUnsigned, CheckSignatureUnverifiable)

	key := revKey{sha: rev.SHA, digest: rev.ContentDigest()}
	if s.revAlreadyReconciled(key) {
		// Nothing changed in the remote: no planning, no store write, no
		// second draft (T-2701 AC2).
		s.noteSuccess(rev, signer)
		return Result{SHA: rev.SHA, Unchanged: true, OpCount: s.status.PlanOpCount, ChangesetID: s.status.OpenChangesetID}, nil
	}

	parsed, err := spec.Parse(rev.Content)
	if err != nil {
		// An unparseable document is explicitly NOT a reason to touch the
		// existing draft (T-2701 AC4): nothing below this line runs, so no
		// update and no discard is even reachable from here.
		detail := fmt.Sprintf("%s at %s (%s) does not parse: %v — the open sync changeset, if any, is untouched",
			s.cfg.Path, s.cfg.Ref, shortSHA(rev.SHA), err)
		s.raise(CheckSpecUnparseable, "error", detail)
		wrapped := fmt.Errorf("%w: %s: %w", ErrSpecParse, s.cfg.Path, err)
		s.recordError(wrapped)
		return Result{SHA: rev.SHA}, wrapped
	}
	s.clear(CheckSpecUnparseable)

	ops, notInSpec, err := spec.Import(parsed, s.cfg.Inventory.Snapshot())
	if err != nil {
		detail := fmt.Sprintf("%s at %s (%s) cannot be reconciled against live state: %v",
			s.cfg.Path, s.cfg.Ref, shortSHA(rev.SHA), err)
		s.raise(CheckSpecUnparseable, "error", detail)
		wrapped := fmt.Errorf("gitsync: importing %s: %w", s.cfg.Path, err)
		s.recordError(wrapped)
		return Result{SHA: rev.SHA}, wrapped
	}

	res, err := s.reconcile(ctx, rev, ops)
	if err != nil {
		s.recordError(err)
		return res, err
	}
	s.noteSuccess(rev, signer)
	s.notePlan(rev, ops, notInSpec, res.ChangesetID)
	return res, nil
}

// verifySignature enforces require_signed_commits. When the gate is off it
// returns immediately without inspecting anything; when it is on, every path
// that does not end in a locally verified signature is a refusal.
func (s *Service) verifySignature(rev Revision) (signer string, err error) {
	if !s.cfg.RequireSignedCommits {
		return "", nil
	}
	if rev.Signature == nil || rev.Signature.Armored == "" {
		detail := fmt.Sprintf("commit %s on %s carries no signature and [gitsync] require_signed_commits is set — refused, nothing was staged",
			shortSHA(rev.SHA), s.cfg.Ref)
		s.raise(CheckCommitUnsigned, "error", detail)
		return "", fmt.Errorf("%w: %s", ErrUnsigned, shortSHA(rev.SHA))
	}
	principal, err := VerifyCommit(rev.Signature.Payload, rev.Signature.Armored, s.cfg.AllowedSigners)
	if err != nil {
		detail := fmt.Sprintf("commit %s on %s has a signature this daemon could not verify: %v — refused, nothing was staged",
			shortSHA(rev.SHA), s.cfg.Ref, err)
		s.raise(CheckSignatureUnverifiable, "error", detail)
		return "", err
	}
	return principal, nil
}

// reconcile is the whole write half of this package: open the one draft, or
// update it. There is deliberately no other store-touching path.
func (s *Service) reconcile(ctx context.Context, rev Revision, ops []change.Op) (Result, error) {
	fp := opsFingerprint(ops)
	open, err := s.openSyncChangeset(ctx)
	if err != nil {
		return Result{SHA: rev.SHA}, err
	}

	if len(ops) == 0 {
		// Converged: intent and reality agree. An existing draft is left
		// exactly as it is — discarding it would be this package taking a
		// decision about a human's open review, which is precisely what it
		// must not do.
		s.rememberRev(revKey{sha: rev.SHA, digest: rev.ContentDigest()}, fp)
		return Result{SHA: rev.SHA, ChangesetID: open.ID}, nil
	}

	title := syncTitle(s.cfg.Path, s.cfg.Ref)
	if open.ID == "" {
		c, createErr := s.cfg.Changesets.CreateWithOrigin(ctx, SyncAuthor, title, ops, change.OriginGitSync, "")
		if createErr != nil {
			return Result{SHA: rev.SHA}, fmt.Errorf("gitsync: opening sync changeset for %s: %w", shortSHA(rev.SHA), createErr)
		}
		s.rememberRev(revKey{sha: rev.SHA, digest: rev.ContentDigest()}, fp)
		s.audit(ctx, "gitsync.changeset.open", c.ID, rev, len(ops))
		s.log.Info("gitsync: opened a draft changeset for review", "changesetId", c.ID, "sha", shortSHA(rev.SHA), "ops", len(ops))
		return Result{SHA: rev.SHA, ChangesetID: c.ID, Created: true, OpCount: len(ops)}, nil
	}

	if fp == s.planFingerprint() {
		// Same plan, already reflected in the open draft: no write.
		s.rememberRev(revKey{sha: rev.SHA, digest: rev.ContentDigest()}, fp)
		return Result{SHA: rev.SHA, ChangesetID: open.ID, OpCount: len(ops)}, nil
	}

	c, err := s.cfg.Changesets.UpdateDraft(ctx, open.ID, SyncAuthor, &title, ops)
	if err != nil {
		return Result{SHA: rev.SHA, ChangesetID: open.ID}, fmt.Errorf("gitsync: updating sync changeset %s: %w", open.ID, err)
	}
	s.rememberRev(revKey{sha: rev.SHA, digest: rev.ContentDigest()}, fp)
	s.audit(ctx, "gitsync.changeset.update", c.ID, rev, len(ops))
	s.log.Info("gitsync: updated the open draft changeset", "changesetId", c.ID, "sha", shortSHA(rev.SHA), "ops", len(ops))
	return Result{SHA: rev.SHA, ChangesetID: c.ID, Updated: true, OpCount: len(ops)}, nil
}

// openSyncChangeset returns the single open (draft or validated)
// gitsync-originated changeset, or a zero Changeset when there is none.
//
// "Open" is exactly the editable set: a sync draft a human already applied,
// discarded or let roll back is history, and a new divergence opens a fresh
// draft rather than resurrecting it. When more than one is somehow open
// (a hand-edited store, a pre-upgrade row), the newest wins and the rest are
// left alone — this package never discards a changeset.
func (s *Service) openSyncChangeset(ctx context.Context) (change.Changeset, error) {
	all, err := s.cfg.Changesets.List(ctx, "")
	if err != nil {
		return change.Changeset{}, fmt.Errorf("gitsync: listing changesets: %w", err)
	}
	var newest change.Changeset
	for _, c := range all {
		if c.Origin != change.OriginGitSync || !c.Editable() {
			continue
		}
		if newest.ID == "" || c.CreatedAt > newest.CreatedAt || (c.CreatedAt == newest.CreatedAt && c.ID > newest.ID) {
			newest = c
		}
	}
	return newest, nil
}

// --- state bookkeeping ----------------------------------------------------

func (s *Service) revAlreadyReconciled(k revKey) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRev != revKey{} && s.lastRev == k
}

func (s *Service) rememberRev(k revKey, fp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRev = k
	s.lastPlanFP = fp
}

func (s *Service) planFingerprint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastPlanFP
}

func (s *Service) noteFetchAttempt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.LastFetchAt = s.now().Unix()
}

func (s *Service) noteSuccess(rev Revision, signer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.LastFetchedSHA = rev.SHA
	s.status.LastSuccessAt = s.now().Unix()
	s.status.LastSigner = signer
	s.status.LastError = ""
}

func (s *Service) notePlan(rev Revision, ops []change.Op, notInSpec []inventory.Ref, changesetID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.PlanOpCount = len(ops)
	s.status.Plan = planSummary(ops)
	s.status.NotInSpec = refStrings(notInSpec)
	s.status.OpenChangesetID = changesetID
	switch {
	case changesetID == "":
		s.status.OpenChangesetReason = ""
	case len(ops) == 0:
		s.status.OpenChangesetReason = fmt.Sprintf(
			"the spec at %s matches live state; this draft is left from an earlier divergence and is a human's to apply or discard", s.cfg.Path)
	default:
		s.status.OpenChangesetReason = fmt.Sprintf(
			"the spec at %s @ %s differs from live state in %d place(s); vnprox staged the reconciling ops for review and applied nothing",
			s.cfg.Path, shortSHA(rev.SHA), len(ops))
	}
	if len(ops) == 0 {
		delete(s.issues, CheckDivergence)
		return
	}
	s.issues[CheckDivergence] = Issue{
		Check:    CheckDivergence,
		Severity: "info",
		Detail: fmt.Sprintf("%s at %s (%s) differs from live state in %d place(s); draft changeset %s is open for review — vnprox has applied nothing",
			s.cfg.Path, s.cfg.Ref, shortSHA(rev.SHA), len(ops), changesetID),
	}
}

// onFetchError degrades a transport failure to a finding plus a retry. It
// never returns a fatal condition, because Run must not take the daemon down
// over a remote being down (T-2701 AC7).
func (s *Service) onFetchError(err error) error {
	detail := fmt.Sprintf("could not read %s at %s from %s: %v — retrying in %s; nothing was staged",
		s.cfg.Path, s.cfg.Ref, s.source.Describe(), err, s.cfg.PollInterval)
	severity := "warning"
	if errors.Is(err, ErrRemoteStatus) {
		// A well-formed refusal (401/403/404) is a configuration or
		// credential problem an operator must fix, not a flaky link.
		severity = "error"
	}
	s.raise(CheckUnreachable, severity, detail)
	s.recordError(err)
	return err
}

func (s *Service) recordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.LastError = err.Error()
}

// audit writes one row per staged draft. It records the sha, the path and
// the op count — never the document, never the credential (docs/security.md:
// an audit detail never carries a secret).
func (s *Service) audit(ctx context.Context, action, changesetID string, rev Revision, opCount int) {
	if s.cfg.Audit == nil {
		return
	}
	detail, err := json.Marshal(map[string]any{
		"remote":  s.source.Describe(),
		"ref":     s.cfg.Ref,
		"path":    s.cfg.Path,
		"sha":     rev.SHA,
		"opCount": opCount,
	})
	if err != nil {
		s.log.Error("gitsync: encoding audit detail", "error", err)
		return
	}
	_, _ = s.cfg.Audit.Append(ctx, store.AuditEntry{
		At:          s.now().Unix(),
		Username:    SyncAuthor,
		Action:      action,
		Result:      "ok",
		ChangesetID: sql.NullString{String: changesetID, Valid: changesetID != ""},
		DetailJSON:  sql.NullString{String: string(detail), Valid: true},
	})
}

// --- helpers --------------------------------------------------------------

func syncTitle(path, ref string) string {
	return fmt.Sprintf("Git spec sync: %s @ %s", path, ref)
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// opsFingerprint is a stable digest of a plan, used to tell "the same plan
// again" from "a different plan". Op ids are not part of it: they are
// assigned by the change engine at Create time and would otherwise make
// every re-plan look different.
func opsFingerprint(ops []change.Op) string {
	type bare struct {
		Type   change.OpType   `json:"type"`
		Target inventory.Ref   `json:"target"`
		Params json.RawMessage `json:"params"`
	}
	out := make([]bare, 0, len(ops))
	for _, op := range ops {
		params, err := json.Marshal(op.Params)
		if err != nil {
			// A params value that will not marshal cannot be fingerprinted;
			// fall back to a value that can never compare equal, so the
			// worst case is a redundant write, never a missed one.
			return "unfingerprintable:" + time.Now().Format(time.RFC3339Nano)
		}
		out = append(out, bare{Type: op.Type, Target: op.Target, Params: params})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "unfingerprintable:" + time.Now().Format(time.RFC3339Nano)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// planSummary renders a plan as one readable line per op, for
// `vnproxctl gitsync status`.
func planSummary(ops []change.Op) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, fmt.Sprintf("%s %s", op.Type, op.Target.String()))
	}
	return out
}

func refStrings(refs []inventory.Ref) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.String())
	}
	return out
}
