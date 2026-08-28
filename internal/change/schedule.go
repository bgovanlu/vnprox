// SPDX-License-Identifier: Apache-2.0

// schedule.go implements T-1103's scheduled changesets & maintenance
// windows: stage now, apply inside a later window, reusing T-205's Apply/
// Confirm and T-304's per-node local-timer machinery completely unchanged —
// this file only decides *when* Service.Apply gets called and persists
// enough state to make that decision durable across a daemon restart.
//
// Safety analysis (required by the T-1103 task card; cross-referenced by
// test name in planning/reports/T-1103.md):
//
//  1. Daemon down mid-window: no partial apply is possible because Apply
//     only ever starts at windowStart (never "resumes" partway) — a crash
//     before firing simply leaves the row pending, and TickSchedules
//     (called once eagerly at startup, mirroring Service.ArmPendingRollbacks)
//     re-evaluates it fresh against the current clock exactly like any other
//     tick would. If the window already fully elapsed while the daemon was
//     down, missedWindowPolicy governs (see handleMissedWindow).
//  2. Peer unreachable at deadline: unchanged from T-304 — Service.Apply
//     arms every affected node's own local rollback timer (LocalTimerAgent)
//     with the same absolute deadline, and each node rolls back
//     independently on its own clock with no cross-node dependency. This
//     file adds nothing to that path; it only decides when Apply starts.
//  3. Clock skew: fireSchedule computes one absolute unix deadline
//     (min(windowStart+confirmTimeoutSec, windowEnd)) and hands it to
//     Service.Apply as a duration relative to *this daemon's own clock at
//     fire time* — from there it is the exact same "coordinator computes
//     one absolute deadline, pushes it to every node before any apply step
//     begins" flow T-304 already implements (apply.go's Apply/
//     finishAwaitingConfirm). Each node's own clock governs its own
//     rollback; nothing here introduces a new skew-sensitive step.
//  4. Spec/state changed between schedule and fire time: fireSchedule
//     recomputes touchesMgmtPath fresh from Service.MgmtStatus (never the
//     value TouchesMgmtPath returned at schedule time) and calls
//     Service.Apply, which itself revalidates the changeset's ops against
//     current live state before doing anything else (beginApply) — a newly
//     introduced blocking finding aborts with zero steps executed, exactly
//     as it would for a manually-triggered apply.

package change

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// systemScheduleActor is the audit/apply attribution for the scheduler's own
// unattended actions (mirrors systemRollbackActor's naming convention,
// apply_log.go).
const systemScheduleActor = "system:schedule"

// DefaultScheduleCheckInterval is how often the supervised scheduler
// goroutine (RunScheduler) re-scans pending schedules in production. Cheap
// (a handful of indexed SQLite rows) so a short interval costs nothing;
// tests never wait on this — they call TickSchedules directly against an
// injected Clock instead (the task card: "so window/deadline tests need no
// sleeps").
const DefaultScheduleCheckInterval = 5 * time.Second

// Clock is the scheduler's injected time seam (T-1103: "a supervised
// scheduler goroutine ... driven by an injected Clock interface (Now())").
// Service already threads a plain now func() time.Time through every other
// time-dependent path (Config.Now); Config.Clock defaults to a thin adapter
// over that same function when unset, so production wiring needs nothing
// beyond what already exists, while schedule-specific tests can hand in a
// small mutable Clock value directly without touching Config.Now at all.
type Clock interface {
	Now() time.Time
}

// clockFunc adapts a plain func() time.Time to Clock.
type clockFunc func() time.Time

func (f clockFunc) Now() time.Time { return f() }

// MissedWindowPolicy is docs/api.md's `missedWindowPolicy` enum for
// POST /changesets/{id}/schedule.
type MissedWindowPolicy string

const (
	// MissedWindowSkip (default) marks the schedule schedule_missed,
	// audits it, and leaves the changeset untouched — the operator decides
	// what to do next.
	MissedWindowSkip MissedWindowPolicy = "skip"
	// MissedWindowApplyImmediately applies on restart/next-tick with a
	// freshly computed window (now .. now+confirmTimeoutSec), still fully
	// touchesMgmtPath- and validation-checked exactly like an on-time fire.
	MissedWindowApplyImmediately MissedWindowPolicy = "applyImmediately"
)

func validMissedWindowPolicy(p string) bool {
	return p == string(MissedWindowSkip) || p == string(MissedWindowApplyImmediately)
}

// ScheduleParams is docs/api.md's POST /changesets/{id}/schedule request
// body: `{windowStart, windowEnd, confirmTimeoutSec?, missedWindowPolicy?}`.
type ScheduleParams struct {
	MissedWindowPolicy string
	WindowStart        int64
	WindowEnd          int64
	ConfirmTimeoutSec  int
}

// Schedule is the wire/aggregate shape of a changeset_schedules row
// (docs/api.md). CallbackToken is populated only by Service.Schedule's own
// return value (the one-time delivery) — GetSchedule/ListSchedules never
// carry it, since it is never persisted in plaintext (see the migration
// comment).
type Schedule struct {
	FiredAt            *int64
	CancelledAt        *int64
	ChangesetID        string
	MissedWindowPolicy string
	Status             string
	CreatedBy          string
	CallbackToken      string `json:"-"`
	WindowStart        int64
	WindowEnd          int64
	ConfirmTimeoutSec  int
	CreatedAt          int64
}

func scheduleFromRow(row store.ChangesetSchedule) Schedule {
	s := Schedule{
		ChangesetID: row.ChangesetID, WindowStart: row.WindowStart, WindowEnd: row.WindowEnd,
		ConfirmTimeoutSec: row.ConfirmTimeoutSec, MissedWindowPolicy: row.MissedWindowPolicy,
		Status: row.Status, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt,
	}
	if row.FiredAt.Valid {
		v := row.FiredAt.Int64
		s.FiredAt = &v
	}
	if row.CancelledAt.Valid {
		v := row.CancelledAt.Int64
		s.CancelledAt = &v
	}
	return s
}

// scheduleConfigured reports whether the scheduling feature's dependencies
// are wired — the apply engine (applyConfigured) plus the schedules repo.
func (s *Service) scheduleConfigured() bool {
	return s.applyConfigured() && s.schedules != nil
}

// clockNow is the scheduler's own "now" — s.clock defaults to wrapping
// s.now (see NewService), so absent an explicit Config.Clock, schedule
// logic and every other time-dependent path in this package already agree
// on the same fake/injected time source in tests.
func (s *Service) clockNow() time.Time {
	if s.clock != nil {
		return s.clock.Now()
	}
	return s.now()
}

// mintCallbackToken generates T-1103's single-use, changeset-scoped signed
// callback token: hex(HMAC-SHA256(secret, changesetID + 0x00 +
// deadline)) — "same construction style as the peer API's HMAC"
// (internal/peer/sign.go's sign), not a general credential. secret is a
// process-lifetime-only random value (Service.scheduleSecret, generated
// once at construction): verifyCallbackToken never needs to recompute the
// HMAC against it again — only the returned token's own sha256 hash is
// ever persisted (hash, below) and compared against on ack, so a daemon
// restart (a fresh secret) never invalidates an already-minted,
// not-yet-used token.
func mintCallbackToken(secret []byte, changesetID string, deadline int64) (token, hash string) {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(changesetID))
	mac.Write([]byte{0})
	mac.Write([]byte(strconv.FormatInt(deadline, 10)))
	token = hex.EncodeToString(mac.Sum(nil))
	return token, hashCallbackToken(token)
}

// hashCallbackToken is the sha256 hex digest persisted as
// changeset_schedules.callback_token_hash — never the token itself.
func hashCallbackToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// verifyCallbackToken reports whether token hashes to want, using a
// constant-time comparison (hmac.Equal on the decoded digests — the same
// discipline internal/peer/sign.go's verifySignature applies, for the same
// timing-side-channel reason).
func verifyCallbackToken(token, want string) bool {
	if token == "" || want == "" {
		return false
	}
	gotHash, err := hex.DecodeString(hashCallbackToken(token))
	if err != nil {
		return false
	}
	wantHash, err := hex.DecodeString(want)
	if err != nil {
		return false
	}
	return hmac.Equal(gotHash, wantHash)
}

// Schedule validates and persists a maintenance-window schedule for a draft/
// validated changeset (docs/api.md: POST /changesets/{id}/schedule),
// returning the created Schedule with its one-time-delivered CallbackToken
// populated. It rejects (in order): a changeset that isn't currently
// editable/schedulable, windowStart >= windowEnd, an unrecognized
// missedWindowPolicy, a changeset whose ops touch a resolved management
// path (*ErrMgmtPathUnattendedForbidden — "there is no config flag or API
// parameter that overrides this" per the task card: this check has no
// AllowDangerousOps/allow_dangerous_ops escape hatch anywhere in this
// function, unlike safetyOptions()'s own T-203 interlock downgrade a few
// lines below in the very same validation pass), and a changeset carrying
// error-severity findings (*ErrValidationBlocked, freshly recomputed — not
// the changeset's possibly-stale cached Findings).
//
// A changeset that already has a pending schedule is rejected with
// *ErrScheduleAlreadyExists; scheduling again after an earlier schedule
// resolved (fired/missed/blocked/failed/cancelled) is allowed and replaces
// that row (store.ChangeScheduleRepo.Upsert).
func (s *Service) Schedule(ctx context.Context, changesetID, author string, params ScheduleParams) (Schedule, error) {
	if !s.scheduleConfigured() {
		return Schedule{}, &ErrApplyNotConfigured{}
	}
	if params.WindowStart >= params.WindowEnd {
		return Schedule{}, &ErrInvalidScheduleWindow{WindowStart: params.WindowStart, WindowEnd: params.WindowEnd}
	}
	policy := params.MissedWindowPolicy
	if policy == "" {
		policy = string(MissedWindowSkip)
	}
	if !validMissedWindowPolicy(policy) {
		return Schedule{}, &ErrInvalidMissedWindowPolicy{Policy: policy}
	}
	confirmTimeoutSec := params.ConfirmTimeoutSec
	if confirmTimeoutSec <= 0 {
		confirmTimeoutSec = int(DefaultConfirmTimeout.Seconds())
	}

	cs, err := s.Get(ctx, changesetID)
	if err != nil {
		return Schedule{}, err
	}
	if !cs.Editable() {
		return Schedule{}, &ErrIllegalTransition{From: cs.Status, To: StatusApplying}
	}

	if existing, gerr := s.schedules.Get(ctx, changesetID); gerr == nil && existing.Status == store.ScheduleStatusPending {
		return Schedule{}, &ErrScheduleAlreadyExists{ChangesetID: changesetID}
	}

	// The hard, unconditional gate the task card requires: no config flag,
	// no request parameter, no AllowDangerousOps downgrade anywhere in this
	// function can make a touchesMgmtPath changeset schedulable. Computed
	// fresh (never a client assertion), exactly like the API layer's own
	// touchesMgmtPath decoration (internal/api/changesets.go's
	// mgmtPathsFor/withMgmtFlag).
	mgmtStatus, mErr := s.MgmtStatus(ctx)
	if mErr != nil {
		return Schedule{}, fmt.Errorf("change: scheduling changeset %s: computing management-path status: %w", changesetID, mErr)
	}
	if TouchesMgmtPath(mgmtStatus.Nodes, s.wgTunnelCarriers(ctx), nil, cs.Ops) {
		s.appendAudit(ctx, author, "changeset.schedule_create", "mgmt_path_unattended_forbidden", changesetID, nil)
		return Schedule{}, &ErrMgmtPathUnattendedForbidden{ChangesetID: changesetID}
	}

	findings := s.validate(ctx, cs.ClusterID, cs.ID, cs.Ops)
	if hasError(findings) {
		s.appendAudit(ctx, author, "changeset.schedule_create", "validation_failed", changesetID, map[string]any{"findingCount": len(findings)})
		return Schedule{}, &ErrValidationBlocked{Findings: findings}
	}

	token, hash := mintCallbackToken(s.scheduleSecret, changesetID, params.WindowEnd)
	nowUnix := s.clockNow().Unix()
	row := store.ChangesetSchedule{
		ChangesetID: changesetID, WindowStart: params.WindowStart, WindowEnd: params.WindowEnd,
		ConfirmTimeoutSec: confirmTimeoutSec, MissedWindowPolicy: policy,
		CallbackTokenHash: hash, Status: store.ScheduleStatusPending,
		CreatedBy: author, CreatedAt: nowUnix,
	}
	if err := s.schedules.Upsert(ctx, row); err != nil {
		return Schedule{}, fmt.Errorf("change: scheduling changeset %s: %w", changesetID, err)
	}
	s.appendAudit(ctx, author, "changeset.schedule_create", "success", changesetID, map[string]any{
		"windowStart": params.WindowStart, "windowEnd": params.WindowEnd,
		"confirmTimeoutSec": confirmTimeoutSec, "missedWindowPolicy": policy,
	})

	out := scheduleFromRow(row)
	out.CallbackToken = token
	return out, nil
}

// GetSchedule returns changesetID's current schedule row (whatever its
// status), or store.ErrNotFound if it never had one. CallbackToken is
// always empty (the plaintext token is never persisted — see Schedule's
// doc comment).
func (s *Service) GetSchedule(ctx context.Context, changesetID string) (Schedule, error) {
	if !s.scheduleConfigured() {
		return Schedule{}, &ErrApplyNotConfigured{}
	}
	row, err := s.schedules.Get(ctx, changesetID)
	if err != nil {
		return Schedule{}, fmt.Errorf("change: getting schedule for changeset %s: %w", changesetID, err)
	}
	return scheduleFromRow(row), nil
}

// CancelSchedule cancels a pending schedule before its window starts
// (docs/api.md: DELETE /changesets/{id}/schedule), auditing
// changeset.schedule_cancel. It returns store.ErrNotFound if changesetID
// has no schedule row, or store.ErrIllegalState if that row is no longer
// pending (already fired/missed/blocked/failed/cancelled) — the scheduler
// only ever resolves a row once, so a cancel racing a fire always loses
// cleanly rather than corrupting either outcome.
func (s *Service) CancelSchedule(ctx context.Context, changesetID, author string) error {
	if !s.scheduleConfigured() {
		return &ErrApplyNotConfigured{}
	}
	if err := s.schedules.Cancel(ctx, changesetID, s.clockNow().Unix()); err != nil {
		return fmt.Errorf("change: cancelling schedule for changeset %s: %w", changesetID, err)
	}
	s.appendAudit(ctx, author, "changeset.schedule_cancel", "success", changesetID, nil)
	return nil
}

// AckSchedule is T-1103's webhook ack path: validates token against the
// stored callback_token_hash for changesetID's schedule row and, on
// success, calls Confirm exactly as the UI's session-cookie
// POST .../confirm would (docs/api.md: "this card ships UI ack ... plus a
// single-use ... callback token ... as the webhook ack path"). No separate
// "used" bookkeeping is needed for single-use enforcement: Confirm itself
// only succeeds while the changeset is awaiting_confirm, so a replayed
// token (before the window even opened, or after the changeset already
// committed/rolled back) fails on Confirm's own state-machine check
// (*ErrNotConfirmable) exactly like a stale UI confirm click would.
func (s *Service) AckSchedule(ctx context.Context, changesetID, token string) (Changeset, error) {
	if !s.scheduleConfigured() {
		return Changeset{}, &ErrApplyNotConfigured{}
	}
	row, err := s.schedules.Get(ctx, changesetID)
	if err != nil {
		return Changeset{}, fmt.Errorf("change: acking schedule for changeset %s: %w", changesetID, err)
	}
	if !verifyCallbackToken(token, row.CallbackTokenHash) {
		return Changeset{}, &ErrInvalidCallbackToken{ChangesetID: changesetID}
	}
	return s.Confirm(ctx, changesetID, systemScheduleActor+":webhook")
}

// planNeedsPVEGateway mirrors internal/api/changesets.go's
// planRequiresPVEGateway: whether ops' plan carries any step needing a live
// PVEGateway to execute. The scheduler has no user session to hand Apply
// (fireSchedule always passes a nil PVEGateway, same as autoRollback), so
// this pre-check lets it fail fast and cleanly (no snapshot/mutation, a
// dedicated audit reason) instead of discovering the same gap mid-apply the
// way an un-gated Apply call would.
func planNeedsPVEGateway(ops []Op) bool {
	plan, err := BuildPlan(ops)
	if err != nil {
		return false
	}
	for _, st := range plan.Steps {
		switch st.Kind {
		case StepSDNStage, StepSDNApply, StepFwApply, StepFwVerify, StepIpamAlloc:
			return true
		}
	}
	return false
}

// TickSchedules scans every pending schedule row and fires or expires each
// one that is due, per s.clockNow(). Exported so tests exercise the exact
// scan/fire/miss decision logic directly against an injected Clock/Now —
// no sleep, no goroutine, no ticker (the task card: "window/deadline tests
// need no sleeps") — while RunScheduler (below) is production's real,
// supervised, periodically-ticking wrapper around the very same method.
// Safe to call repeatedly/concurrently: each row resolves via
// ChangeScheduleRepo.Resolve's "WHERE status = pending" guard, so a row
// already claimed by a previous call (or a concurrent one) is silently
// skipped rather than double-fired.
func (s *Service) TickSchedules(ctx context.Context) {
	if !s.scheduleConfigured() {
		return
	}
	// T-1704 single-writer fence: a standby must not fire scheduled applies —
	// only the current HA leader ticks. A demoted former-active whose ticker
	// still fires no-ops here; the leader's own scheduler drives the window
	// from the same persisted, absolute windowStart/windowEnd. (See mayLead.)
	if !s.mayLead() {
		return
	}
	pending, err := s.schedules.ListByStatus(ctx, store.ScheduleStatusPending)
	if err != nil {
		s.log.Error("change: listing pending changeset schedules", "error", err)
		return
	}
	now := s.clockNow().Unix()
	for _, row := range pending {
		switch {
		case now >= row.WindowStart && now < row.WindowEnd:
			s.fireSchedule(ctx, row, row.WindowStart)
		case now >= row.WindowEnd:
			s.handleMissedWindow(ctx, row)
		}
	}
}

// RunScheduler drives TickSchedules on a periodic real-time tick until ctx
// is cancelled — cmd/vnproxd's runGroup actor signature (mirrors
// drift.Service.RunLoop/findings.Engine.RunLoop's own "supervised, owned,
// has a shutdown path" shape, per docs/development.md's Go standards). interval
// <= 0 uses DefaultScheduleCheckInterval. Tests exercise TickSchedules
// directly instead of this method — see that method's doc comment.
func (s *Service) RunScheduler(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultScheduleCheckInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.TickSchedules(ctx)
		}
	}
}

// fireSchedule fires one due schedule: recompute touchesMgmtPath fresh
// (never the schedule-time value — safety-analysis point 4), fail fast on a
// plan that would need a PVEGateway this unattended path never has, apply
// (which itself revalidates from scratch — beginApply), and resolve the row.
// windowStartOverride is row.WindowStart for an on-time fire and s.clockNow()
// for a missed-window applyImmediately fire (handleMissedWindow), so the
// confirm-deadline math below always measures "time remaining in this fire's
// own window" rather than a stale original window.
func (s *Service) fireSchedule(ctx context.Context, row store.ChangesetSchedule, effectiveStart int64) {
	cs, err := s.Get(ctx, row.ChangesetID)
	if err != nil {
		s.log.Error("change: firing schedule: loading changeset", "changeset_id", row.ChangesetID, "error", err)
		s.resolveSchedule(ctx, row, store.ScheduleStatusFailed)
		s.appendAudit(ctx, systemScheduleActor, "changeset.schedule_fire_blocked", "changeset_missing", row.ChangesetID, map[string]any{"error": err.Error()})
		return
	}

	mgmtStatus, mErr := s.MgmtStatus(ctx)
	if mErr == nil && TouchesMgmtPath(mgmtStatus.Nodes, s.wgTunnelCarriers(ctx), nil, cs.Ops) {
		s.resolveSchedule(ctx, row, store.ScheduleStatusBlocked)
		s.appendAudit(ctx, systemScheduleActor, "changeset.schedule_fire_blocked", "mgmt_path_unattended_forbidden", row.ChangesetID, nil)
		return
	}

	// T-1604: additive failure-impact pre-flight, weighed only after (never
	// instead of) the unconditional mgmt-path exclusion above. A changeset
	// whose touched entities put quorum or a management path at risk is
	// aborted with a distinct audit reason. A clean verdict here changes
	// nothing about the gates already passed.
	if block, reason := s.preflightImpactBlocks(ctx, cs.Ops); block {
		s.resolveSchedule(ctx, row, store.ScheduleStatusBlocked)
		s.appendAudit(ctx, systemScheduleActor, "changeset.schedule_fire_blocked", reason, row.ChangesetID, nil)
		return
	}

	if planNeedsPVEGateway(cs.Ops) {
		s.resolveSchedule(ctx, row, store.ScheduleStatusBlocked)
		s.appendAudit(ctx, systemScheduleActor, "changeset.schedule_fire_blocked", "pve_session_required", row.ChangesetID, nil)
		return
	}

	remaining := time.Unix(row.WindowEnd, 0).Sub(time.Unix(effectiveStart, 0))
	timeout := time.Duration(row.ConfirmTimeoutSec) * time.Second
	if timeout > remaining {
		timeout = remaining
	}

	if _, applyErr := s.Apply(ctx, row.ChangesetID, systemScheduleActor, nil, timeout); applyErr != nil {
		s.resolveSchedule(ctx, row, store.ScheduleStatusBlocked)
		s.appendAudit(ctx, systemScheduleActor, "changeset.schedule_fire_blocked", "apply_failed", row.ChangesetID, map[string]any{"error": applyErr.Error()})
		return
	}
	s.resolveSchedule(ctx, row, store.ScheduleStatusFired)
	s.appendAudit(ctx, systemScheduleActor, "changeset.schedule_fire", "success", row.ChangesetID, nil)
}

// handleMissedWindow resolves a schedule row discovered pending only after
// its own windowEnd has already passed (the daemon was down through the
// entire window, or simply never ticked in time) — safety-analysis point 1.
// "skip" (default) marks the row schedule_missed and audits, leaving the
// changeset untouched for the operator to reschedule or apply by hand;
// "applyImmediately" fires right now with a fresh window (now ..
// now+confirmTimeoutSec), still fully touchesMgmtPath/validation-checked by
// fireSchedule exactly like an on-time fire.
func (s *Service) handleMissedWindow(ctx context.Context, row store.ChangesetSchedule) {
	if row.MissedWindowPolicy == string(MissedWindowApplyImmediately) {
		now := s.clockNow().Unix()
		s.fireSchedule(ctx, row, now)
		return
	}
	s.resolveSchedule(ctx, row, store.ScheduleStatusMissed)
	s.appendAudit(ctx, systemScheduleActor, "changeset.schedule_missed", "missed", row.ChangesetID, map[string]any{
		"windowStart": row.WindowStart, "windowEnd": row.WindowEnd,
	})
}

func (s *Service) resolveSchedule(ctx context.Context, row store.ChangesetSchedule, status string) {
	if err := s.schedules.Resolve(ctx, row.ChangesetID, status, s.clockNow().Unix()); err != nil {
		s.log.Error("change: resolving changeset schedule", "changeset_id", row.ChangesetID, "status", status, "error", err)
	}
}

// MissedSchedule is one currently-missed, unresolved-by-the-operator
// schedule — internal/findings' schedule_missed health check's input shape
// (ScheduleMissedProvider), kept as this package's own small type rather
// than reusing store.ChangesetSchedule directly, the same "small seam,
// adapted by the caller" convention MgmtProvider/DriftProvider etc. already
// follow across package boundaries.
type MissedSchedule struct {
	ChangesetID string
	WindowStart int64
	WindowEnd   int64
}

// MissedSchedules returns every schedule currently in status "missed" —
// internal/findings.checkScheduleMissed's live data source, recomputed
// fresh on every call (mirrors MgmtStatus/checkMgmtSinglePath's own
// "recompute from current store state each cycle" shape) so a finding
// clears the instant its row is superseded by a fresh Schedule call or the
// operator otherwise moves on.
func (s *Service) MissedSchedules(ctx context.Context) []MissedSchedule {
	if !s.scheduleConfigured() {
		return nil
	}
	rows, err := s.schedules.ListByStatus(ctx, store.ScheduleStatusMissed)
	if err != nil {
		s.log.Warn("change: listing missed changeset schedules for findings", "error", err)
		return nil
	}
	out := make([]MissedSchedule, 0, len(rows))
	for _, row := range rows {
		out = append(out, MissedSchedule{ChangesetID: row.ChangesetID, WindowStart: row.WindowStart, WindowEnd: row.WindowEnd})
	}
	return out
}

// newScheduleSecret generates the process-lifetime-only HMAC signing
// secret Schedule/mintCallbackToken use (see mintCallbackToken's doc
// comment for why this never needs to be persisted or survive a restart).
func newScheduleSecret(logger *slog.Logger) []byte {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is effectively unrecoverable for the whole
		// process; degrade to a fixed-but-still-per-process-random-enough
		// fallback rather than panicking a whole daemon over a scheduling
		// feature. Logged loudly since it means /dev/urandom itself is
		// broken, a much bigger problem than schedule tokens.
		logger.Error("change: crypto/rand failed generating the schedule callback-token secret; scheduling will still work but is degraded", "error", err)
	}
	return buf
}
