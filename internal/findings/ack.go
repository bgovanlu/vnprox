// ack.go implements T-2402's finding acknowledgement: an operator's record
// that a finding is understood and deliberate, so the unified stream can be
// triaged instead of merely watched.
//
// ACKNOWLEDGEMENT IS NOT SUPPRESSION. Nothing in this file removes a finding,
// stops a check running, or changes what a check computes. An acked finding is
// still produced by its producer, still returned by GET /findings with its ack
// attached, and still counted — in its own bucket
// (vnprox_findings_acked) rather than the open one. If a check is wrong, the
// remedy is to fix the check; this is for the checks that are right about a
// state someone has deliberately chosen.
//
// Two properties are worth stating because they are the difference between a
// triage tool and a mute button:
//
//   - A REASON IS REQUIRED. An acknowledgement with no reason is an
//     unexplained silence, which is worse than the noise it replaces — the
//     next operator cannot tell a considered decision from a stray click.
//   - EXPIRY IS EVALUATED AT READ TIME, never by a sweeper. A daemon that is
//     stopped, crashed, or simply not running a cleanup tick must not be able
//     to leave a finding muted past the date its operator chose. There is
//     deliberately no background job that deletes expired rows, and
//     Apply/Decorate take the clock as a parameter so this is testable.
//
// Layering: internal/findings never imports internal/store (the same rule
// findingevents.go/webhook.go document for FindingEventRecorder and
// AlertRuleProvider). AckStore is the storage seam; cmd/vnproxd adapts
// *store.FindingAckRepo onto it at the composition root.

package findings

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Ack is one finding's acknowledgement as the API renders it. ExpiresAt is
// unix seconds, or 0 for "until explicitly un-acked".
type Ack struct {
	Reason    string `json:"reason"`
	AckedBy   string `json:"ackedBy"`
	AckedAt   int64  `json:"ackedAt"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
}

// Active reports whether this acknowledgement still applies at now. An
// ExpiresAt of 0 never expires; any other value expires the instant it is
// reached, so an ack "until 12:00" is not still muting at 12:00.
func (a Ack) Active(now time.Time) bool {
	return a.ExpiresAt == 0 || now.Unix() < a.ExpiresAt
}

// AckStore is the storage seam AckService reads and writes through.
// *store.FindingAckRepo is adapted onto it in cmd/vnproxd.
type AckStore interface {
	ListAcks(ctx context.Context) (map[string]Ack, error)
	UpsertAck(ctx context.Context, findingID string, a Ack) error
	DeleteAck(ctx context.Context, findingID string) error
}

// ErrAckReasonRequired is returned by AckService.Ack when the caller supplies
// no reason, or only whitespace.
var ErrAckReasonRequired = errors.New("findings: an acknowledgement requires a reason")

// ErrAckExpiryInPast is returned by AckService.Ack when the caller supplies an
// expiry that has already passed — which would record an acknowledgement that
// never applies, silently.
var ErrAckExpiryInPast = errors.New("findings: an acknowledgement's expiry is already in the past")

// ErrAckTooEarly is returned by AckService.Ack when the finding declares an
// AckableAt (T-2604) that has not been reached yet. It carries the instant
// the finding becomes acknowledgeable so the API can say when, not merely
// that it refused.
type ErrAckTooEarly struct {
	FindingID string
	AckableAt int64
}

func (e *ErrAckTooEarly) Error() string {
	return fmt.Sprintf("findings: %s cannot be acknowledged until %s",
		e.FindingID, time.Unix(e.AckableAt, 0).UTC().Format(time.RFC3339))
}

// ErrNoSuchFinding is returned when the caller acks an id no producer is
// currently reporting. Recording one would leave a dangling row that nothing
// can ever clear from the UI, because the UI only shows acks alongside their
// finding.
var ErrNoSuchFinding = errors.New("findings: no finding with that id")

// maxAckReasonLen bounds the stored reason. Generous for a sentence of
// justification, bounded so an acknowledgement cannot be used as a blob store.
const maxAckReasonLen = 1000

// AckService applies and records acknowledgements over an AckStore.
type AckService struct {
	store AckStore
	now   func() time.Time
}

// NewAckService builds an AckService over st. now defaults to time.Now.
func NewAckService(st AckStore, now func() time.Time) *AckService {
	if now == nil {
		now = time.Now
	}
	return &AckService{store: st, now: now}
}

// Decorate attaches each finding's currently-active acknowledgement, and
// reports how many of the returned findings are acked. A finding whose ack has
// expired comes back undecorated — the stored row is left alone, because
// deleting it here would make a read path a writer and would race a second
// reader doing the same.
//
// The returned slice is a copy; the caller's input is not mutated.
func (s *AckService) Decorate(ctx context.Context, in []Finding) (out []Finding, acked int, err error) {
	if s == nil || s.store == nil {
		return in, 0, nil
	}
	acks, err := s.store.ListAcks(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("findings: loading acknowledgements: %w", err)
	}
	now := s.now()
	out = make([]Finding, len(in))
	copy(out, in)
	for i := range out {
		a, ok := acks[out[i].ID]
		if !ok || !a.Active(now) {
			continue
		}
		ackCopy := a
		out[i].Ack = &ackCopy
		acked++
	}
	return out, acked, nil
}

// Ack records (or replaces) an acknowledgement for findingID. present is the
// findings currently reported by the engine, keyed by id (PresentFindings);
// acking an id absent from it is refused rather than creating a dangling row.
//
// It carries the whole finding, not merely its id, because a finding may
// declare a floor on when it becomes acknowledgeable (AckableAt, T-2604) and
// that floor has to be enforced HERE — at the one write path — rather than
// by whichever caller happens to remember to check it.
//
// Re-acking an already-acked finding replaces the reason, actor, and expiry:
// an operator extending a mute should not have to un-ack first.
func (s *AckService) Ack(ctx context.Context, findingID, reason, actor string, expiresAt int64, present map[string]Finding) (Ack, error) {
	if s == nil || s.store == nil {
		return Ack{}, errors.New("findings: acknowledgement storage is not configured")
	}
	f, ok := present[findingID]
	if !ok {
		return Ack{}, ErrNoSuchFinding
	}
	// T-2604: the acknowledgement floor. Evaluated against the same injected
	// clock the expiry rule uses, so it is provable by setting the clock
	// rather than by waiting — and checked before the reason/expiry
	// validation below so a too-early ack is refused for the reason that
	// actually applies.
	if f.AckableAt != 0 && s.now().Unix() < f.AckableAt {
		return Ack{}, &ErrAckTooEarly{FindingID: findingID, AckableAt: f.AckableAt}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Ack{}, ErrAckReasonRequired
	}
	if len(reason) > maxAckReasonLen {
		return Ack{}, fmt.Errorf("findings: acknowledgement reason exceeds %d characters", maxAckReasonLen)
	}
	now := s.now()
	if expiresAt != 0 && expiresAt <= now.Unix() {
		return Ack{}, ErrAckExpiryInPast
	}
	a := Ack{Reason: reason, AckedBy: actor, AckedAt: now.Unix(), ExpiresAt: expiresAt}
	if err := s.store.UpsertAck(ctx, findingID, a); err != nil {
		return Ack{}, fmt.Errorf("findings: recording acknowledgement for %s: %w", findingID, err)
	}
	return a, nil
}

// Unack removes any acknowledgement for findingID. Unlike Ack, this does not
// require the finding to still be present: an operator must always be able to
// clear a stale row, including one whose finding has since gone away.
func (s *AckService) Unack(ctx context.Context, findingID string) error {
	if s == nil || s.store == nil {
		return errors.New("findings: acknowledgement storage is not configured")
	}
	if err := s.store.DeleteAck(ctx, findingID); err != nil {
		return fmt.Errorf("findings: clearing acknowledgement for %s: %w", findingID, err)
	}
	return nil
}

// PresentFindings builds the map Ack takes, from a findings slice: every
// currently-reported finding keyed by its id. It carries whole findings
// rather than ids so Ack can enforce a per-finding acknowledgement floor
// (Finding.AckableAt) without a second lookup the caller could skip.
func PresentFindings(in []Finding) map[string]Finding {
	out := make(map[string]Finding, len(in))
	for _, f := range in {
		out[f.ID] = f
	}
	return out
}
