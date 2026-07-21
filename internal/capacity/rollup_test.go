package capacity

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeSource struct {
	gotStart, gotEnd time.Time
	aggs             []Aggregate
	calls            int
}

func (s *fakeSource) DayAggregates(_ context.Context, dayStart, dayEnd time.Time) ([]Aggregate, error) {
	s.calls++
	s.gotStart, s.gotEnd = dayStart, dayEnd
	return s.aggs, nil
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRollupJob_RunOnceStampsYesterdayAndIsIdempotent(t *testing.T) {
	now := time.Date(2026, 7, 20, 3, 30, 0, 0, time.UTC) // 03:30 today
	wantDayStart := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	wantDayEnd := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)

	src := &fakeSource{aggs: []Aggregate{
		{Ref: "iface:pve1:vmbr1", Kind: KindLink, AvgUtil: 40, MaxUtil: 55},
		{Ref: "10.0.0.0/24", Kind: KindIPAMPool, AvgUtil: 12, MaxUtil: 12},
	}}
	sink := newFakeSink()
	job := NewRollupJob(src, sink, func() time.Time { return now }, quietLogger())

	ctx := context.Background()
	if err := job.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce #1: %v", err)
	}
	if !src.gotStart.Equal(wantDayStart) || !src.gotEnd.Equal(wantDayEnd) {
		t.Fatalf("DayAggregates window = [%s, %s), want [%s, %s)", src.gotStart, src.gotEnd, wantDayStart, wantDayEnd)
	}
	// Re-run the same day: idempotent, no duplicate rows.
	if err := job.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce #2: %v", err)
	}
	if len(sink.rows) != 2 {
		t.Fatalf("sink has %d rows after two runs, want 2 (idempotent upsert)", len(sink.rows))
	}
	for _, a := range sink.rows {
		if !a.BucketAt.Equal(wantDayStart) {
			t.Errorf("row %s/%s stamped at %s, want yesterday start %s", a.Kind, a.Ref, a.BucketAt, wantDayStart)
		}
	}
}
