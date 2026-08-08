package findings

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type recordedEvent struct {
	findingID  string
	transition string
	at         int64
}

type fakeFindingEventRecorder struct {
	err    error
	events []recordedEvent
}

func (f *fakeFindingEventRecorder) RecordFindingEvent(_ context.Context, findingID string, at int64, transition string) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, recordedEvent{findingID: findingID, transition: transition, at: at})
	return nil
}

func TestFindingEventsNotifier_Notify(t *testing.T) {
	fixedNow := time.Unix(1_700_000_000, 0)

	tests := []struct {
		finding    Finding
		name       string
		transition TransitionKind
		want       recordedEvent
	}{
		{
			name:       "new",
			finding:    Finding{ID: "health:mgmt_single_path|bridge:pve1:vmbr0"},
			transition: TransitionNew,
			want:       recordedEvent{findingID: "health:mgmt_single_path|bridge:pve1:vmbr0", transition: "new", at: fixedNow.Unix()},
		},
		{
			name:       "escalated",
			finding:    Finding{ID: "f1"},
			transition: TransitionEscalated,
			want:       recordedEvent{findingID: "f1", transition: "escalated", at: fixedNow.Unix()},
		},
		{
			name:       "resolved",
			finding:    Finding{ID: "f1"},
			transition: TransitionResolved,
			want:       recordedEvent{findingID: "f1", transition: "resolved", at: fixedNow.Unix()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &fakeFindingEventRecorder{}
			n := NewFindingEventsNotifier(rec, nil)
			n.now = func() time.Time { return fixedNow }

			if err := n.Notify(context.Background(), tt.finding, tt.transition); err != nil {
				t.Fatalf("Notify: %v", err)
			}
			if len(rec.events) != 1 {
				t.Fatalf("recorder got %d events, want 1", len(rec.events))
			}
			if rec.events[0] != tt.want {
				t.Errorf("recorded event = %+v, want %+v", rec.events[0], tt.want)
			}
		})
	}
}

func TestFindingEventsNotifier_NilRecorder(t *testing.T) {
	n := NewFindingEventsNotifier(nil, nil)
	if err := n.Notify(context.Background(), Finding{ID: "f1"}, TransitionNew); err != nil {
		t.Errorf("Notify with nil recorder = %v, want nil (harmless no-op)", err)
	}
}

func TestFindingEventsNotifier_RecorderErrorPropagates(t *testing.T) {
	wantErr := errors.New("disk full")
	rec := &fakeFindingEventRecorder{err: wantErr}
	n := NewFindingEventsNotifier(rec, nil)

	err := n.Notify(context.Background(), Finding{ID: "f1"}, TransitionNew)
	if !errors.Is(err, wantErr) {
		t.Errorf("Notify error = %v, want %v", err, wantErr)
	}
}

// Reuses notify.go's own evaluateNotifications machinery (via a minimal
// Engine cycle) to prove FindingEventsNotifier is fed by the EXISTING
// transition detection rather than a second, duplicated one — a steady-
// state finding across two cycles must fire exactly one "new" event, never
// once per cycle (mirrors notify.go's own doc comment: "AC5's contract ...
// exactly one notification, not N").
func TestFindingEventsNotifier_ViaEngineTransitionDetection(t *testing.T) {
	rec := &fakeFindingEventRecorder{}
	notifier := NewFindingEventsNotifier(rec, nil)

	e := &Engine{
		notifier: notifier,
		notified: map[string]string{},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	finding := Finding{ID: "f1", Severity: SeverityError}
	e.notifyMin = SeverityInfo // meets threshold regardless of severity ordering

	ctx := context.Background()
	e.evaluateNotifications(ctx, []Finding{finding})
	e.evaluateNotifications(ctx, []Finding{finding})
	e.evaluateNotifications(ctx, []Finding{finding})

	newCount := 0
	for _, ev := range rec.events {
		if ev.transition == "new" {
			newCount++
		}
	}
	if newCount != 1 {
		t.Errorf("got %d 'new' finding_events rows across 3 unchanged cycles, want exactly 1", newCount)
	}

	// Now the finding disappears: exactly one "resolved" event.
	e.evaluateNotifications(ctx, nil)
	resolvedCount := 0
	for _, ev := range rec.events {
		if ev.transition == "resolved" {
			resolvedCount++
		}
	}
	if resolvedCount != 1 {
		t.Errorf("got %d 'resolved' finding_events rows, want exactly 1", resolvedCount)
	}
}
