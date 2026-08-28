// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
)

// fakeBreakGlassProvider is a BreakGlassProvider test double. It counts
// calls so a "no findings" assertion can distinguish "the check ran and
// found nothing" from "the check never ran".
type fakeBreakGlassProvider struct {
	events []change.BreakGlassRecord
	calls  int
}

func (f *fakeBreakGlassProvider) BreakGlassEvents() []change.BreakGlassRecord {
	f.calls++
	return f.events
}

const testInvokedAt = int64(1_700_000_000)

func testBreakGlassRecord(changesetID string) change.BreakGlassRecord {
	return change.BreakGlassRecord{
		ChangesetID: changesetID,
		Reason:      "corosync down at 03:00, nobody else on call",
		InvokedBy:   "alice",
		InvokedAt:   testInvokedAt,
		AckableAt:   testInvokedAt + int64(change.BreakGlassAckFloor.Seconds()),
	}
}

func TestCheckBreakGlass(t *testing.T) {
	tests := []struct {
		name   string
		events []change.BreakGlassRecord
		want   int
	}{
		{name: "no invocations, no findings", events: nil, want: 0},
		{name: "one invocation, one finding", events: []change.BreakGlassRecord{testBreakGlassRecord("cs1")}, want: 1},
		{
			name:   "one finding per invocation, not one per cycle",
			events: []change.BreakGlassRecord{testBreakGlassRecord("cs1"), testBreakGlassRecord("cs2")},
			want:   2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &fakeBreakGlassProvider{events: tt.events}
			got := checkBreakGlass(p)
			if p.calls != 1 {
				t.Fatalf("provider calls = %d, want 1 — the check must actually run", p.calls)
			}
			if len(got) != tt.want {
				t.Fatalf("findings = %d, want %d (%+v)", len(got), tt.want, got)
			}
			for _, f := range got {
				if f.Severity != SeverityError {
					t.Errorf("severity = %q, want error — an override nobody notices is not an override", f.Severity)
				}
				if f.Check != CheckBreakGlass {
					t.Errorf("check = %q, want %q", f.Check, CheckBreakGlass)
				}
				if f.AckableAt != testInvokedAt+int64(change.BreakGlassAckFloor.Seconds()) {
					t.Errorf("AckableAt = %d, want invokedAt + 24h", f.AckableAt)
				}
				if !strings.Contains(f.Detail, "alice") || !strings.Contains(f.Detail, "nobody else on call") {
					t.Errorf("detail %q must name who took the override and why", f.Detail)
				}
			}
		})
	}
}

// A nil provider (not wired) contributes nothing rather than panicking —
// the same degradation every other optional producer documents.
func TestCheckBreakGlass_NilProviderIsQuiet(t *testing.T) {
	if got := checkBreakGlass(nil); got != nil {
		t.Fatalf("checkBreakGlass(nil) = %+v, want nil", got)
	}
}

// AC5: the break-glass finding cannot be acknowledged before 24 hours have
// passed — proved by SETTING THE CLOCK, never by waiting. Every leg below
// runs against the same finding and the same store; only the clock moves.
//
// The control legs are what make this meaningful: an ack one second after
// the floor SUCCEEDS (so the refusal is about the floor, not about the
// finding being unackable in general), and an ordinary finding with no floor
// acks fine at the very instant the break-glass one is refused (so the
// refusal is about this finding, not about the clock).
func TestAck_BreakGlassFindingIsRefusedUntilTwentyFourHoursHavePassed(t *testing.T) {
	f := checkBreakGlass(&fakeBreakGlassProvider{events: []change.BreakGlassRecord{testBreakGlassRecord("cs1")}})[0]
	ordinary := ackFinding("health:mtu_mismatch|bridge:pve1:vmbr0")
	present := PresentFindings([]Finding{f, ordinary})
	floor := f.AckableAt

	tests := []struct {
		name        string
		now         int64
		wantRefusal bool
	}{
		{name: "immediately after the override", now: testInvokedAt, wantRefusal: true},
		{name: "one second before the floor", now: floor - 1, wantRefusal: true},
		{name: "one hour before the floor", now: floor - 3600, wantRefusal: true},
		{name: "exactly at the floor", now: floor, wantRefusal: false},
		{name: "one second after the floor", now: floor + 1, wantRefusal: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeAckStore()
			svc := NewAckService(st, fixedClock(time.Unix(tt.now, 0)))
			_, err := svc.Ack(context.Background(), f.ID, "reviewed at the postmortem", "brian", 0, present)

			var tooEarly *ErrAckTooEarly
			if tt.wantRefusal {
				if !errors.As(err, &tooEarly) {
					t.Fatalf("err = %v, want *ErrAckTooEarly", err)
				}
				if tooEarly.AckableAt != floor {
					t.Errorf("AckableAt = %d, want %d — the refusal must say WHEN", tooEarly.AckableAt, floor)
				}
				if st.upserts != 0 {
					t.Fatalf("a refused ack still wrote %d rows", st.upserts)
				}
			} else if err != nil {
				t.Fatalf("err = %v, want the ack to succeed once the floor has passed", err)
			} else if st.upserts != 1 {
				t.Fatalf("upserts = %d, want 1", st.upserts)
			}

			// CONTROL LEG: at this same instant, a finding with no floor is
			// acknowledgeable — so a refusal above is about the break-glass
			// finding's floor and not about the clock, the store, or the
			// service refusing everything.
			if _, err := svc.Ack(context.Background(), ordinary.ID, "known and deliberate", "brian", 0, present); err != nil {
				t.Fatalf("ordinary finding ack at the same instant: %v", err)
			}
		})
	}
}

// The floor is not a suppression: the finding is still produced, still
// returned, and still counted while it cannot be acked.
func TestBreakGlassFindingIsStillReportedWhileUnackable(t *testing.T) {
	f := checkBreakGlass(&fakeBreakGlassProvider{events: []change.BreakGlassRecord{testBreakGlassRecord("cs1")}})[0]
	st := newFakeAckStore()
	svc := NewAckService(st, fixedClock(time.Unix(testInvokedAt+60, 0)))

	got, acked, err := svc.Decorate(context.Background(), []Finding{f})
	if err != nil {
		t.Fatalf("Decorate: %v", err)
	}
	if len(got) != 1 || got[0].Severity != SeverityError || got[0].Ack != nil {
		t.Fatalf("Decorate = %+v (acked %d), want the finding still reported and unacked", got, acked)
	}
}
