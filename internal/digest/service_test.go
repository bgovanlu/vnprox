package digest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/docexport"
	"github.com/bgovanlu/vnprox/internal/findings"
)

// service_test.go covers T-2807's acceptance criteria end to end: a quiet
// digest stays one line (AC1), deltas are measured against the PREVIOUS
// DIGEST and a first-ever digest says it has none (AC2), delivery goes through
// T-2407's own path and therefore obeys quiet hours (AC3), a failed delivery
// retries and is recorded (AC4), and a schedule change is picked up without a
// restart (AC5).
//
// Every clock here is a variable the test writes. Nothing sleeps.

const weekly = 7 * 24 * time.Hour

// ---------------------------------------------------------------------
// AC1 — a quiet period produces the one-line digest, end to end.
// ---------------------------------------------------------------------

func TestTick_QuietPeriodDeliversTheOneLineDigest(t *testing.T) {
	now := mustTime("2025-06-22T09:00:00Z")
	store := &fakeStore{}
	store.setSchedule(Schedule{Every: weekly, Enabled: true})
	store.runs = []Run{{
		PeriodStart: mustTime("2025-06-08T09:00:00Z").Unix(),
		PeriodEnd:   mustTime("2025-06-15T09:00:00Z").Unix(),
		// Same score as the current one, so there is no movement either.
		PostureOverall: 82,
		Status:         StatusDelivered,
	}}
	notifier := &captureNotifier{}

	svc := New(Config{
		Store:    store,
		Posture:  &stubPosture{p: scoredPosture(82, now.Unix()), ok: true},
		Findings: stubFindings{},
		History:  &stubHistory{},
		Notifier: notifier,
		Logger:   quietLogger(),
		Now:      func() time.Time { return now },
	})

	sent, err := svc.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if !sent {
		t.Fatal("Tick did not send a digest although one was due")
	}

	f := notifier.only()
	if f.Severity != findings.SeverityInfo {
		t.Errorf("digest severity = %q, want %q — a digest is a report, not an alarm",
			f.Severity, findings.SeverityInfo)
	}
	if f.Check != CheckScheduledDigest {
		t.Errorf("digest check = %q, want %q", f.Check, CheckScheduledDigest)
	}

	body := f.Detail
	if n := len(body); n > docexport.DigestQuietMaxBytes {
		t.Errorf("a quiet digest delivered %d bytes, over the stated bound of %d:\n%s",
			n, docexport.DigestQuietMaxBytes, body)
	}
	if lines := strings.Count(strings.TrimRight(body, "\n"), "\n"); lines != 0 {
		t.Errorf("a quiet digest delivered %d lines, want one:\n%s", lines+1, body)
	}
	if !strings.Contains(body, "nothing to report") {
		t.Errorf("a quiet digest does not say so:\n%s", body)
	}

	runs := store.recorded()
	if len(runs) != 2 {
		t.Fatalf("recorded %d runs, want 2 (the seeded baseline plus this digest)", len(runs))
	}
	fresh := runs[len(runs)-1]
	if !fresh.Quiet {
		t.Error("the recorded run is not marked quiet; 'why was last week's digest one line' is unanswerable")
	}
	if fresh.Status != StatusDelivered {
		t.Errorf("recorded status = %q, want %q", fresh.Status, StatusDelivered)
	}
}

// ---------------------------------------------------------------------
// AC2 — deltas against the previous digest, both directions.
// ---------------------------------------------------------------------

// TestTick_FirstEverDigestStatesItHasNoBaseline is the direction that
// silently regresses: with nothing to compare against, a naive implementation
// renders a delta against zero and nobody notices, because the number looks
// plausible.
func TestTick_FirstEverDigestStatesItHasNoBaseline(t *testing.T) {
	now := mustTime("2025-06-22T09:00:00Z")
	store := &fakeStore{}
	store.setSchedule(Schedule{Every: weekly, Enabled: true})
	notifier := &captureNotifier{}
	history := &stubHistory{transitions: []Transition{
		{FindingID: "health:carrier_down|iface:pve1:eno2", Transition: "new", At: now.Add(-time.Hour).Unix()},
	}}

	svc := New(Config{
		Store:    store,
		Posture:  &stubPosture{p: scoredPosture(82, now.Unix()), ok: true},
		Findings: stubFindings{},
		History:  history,
		Notifier: notifier,
		Logger:   quietLogger(),
		Now:      func() time.Time { return now },
	})

	if _, err := svc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	body := notifier.only().Detail
	if !strings.Contains(body, "no previous digest to compare against") {
		t.Errorf("a first-ever digest does not say it has no baseline:\n%s", body)
	}
	if !strings.Contains(body, "first digest for this schedule") {
		t.Errorf("a first-ever digest does not carry the no-baseline note:\n%s", body)
	}
	// A delta against a zero baseline would render "+82".
	for _, spurious := range []string{"+82", "since the last digest"} {
		if strings.Contains(body, spurious) {
			t.Errorf("a first-ever digest renders %q — a delta against no baseline:\n%s", spurious, body)
		}
	}

	// With no previous digest, the window falls back to exactly one cadence
	// and says so; it does not reach back to the epoch.
	runs := store.recorded()
	if len(runs) != 1 {
		t.Fatalf("recorded %d runs, want 1", len(runs))
	}
	wantStart := now.Add(-weekly).Unix()
	if runs[0].PeriodStart != wantStart {
		t.Errorf("first digest window start = %d, want %d (now minus one cadence)", runs[0].PeriodStart, wantStart)
	}
	if runs[0].PostureOverall != 82 {
		t.Errorf("recorded PostureOverall = %d, want 82 — the next digest's baseline is wrong", runs[0].PostureOverall)
	}
}

// TestTick_DeltaIsMeasuredFromThePreviousDigest is the other direction, and it
// asserts the window as well as the number: the period a digest reports on
// STARTS at the previous digest's end, which is what "not an arbitrary window"
// means structurally. A regression to "the last seven days" would keep the
// delta correct and silently drop or double-count whatever happened while a
// digest was late.
func TestTick_DeltaIsMeasuredFromThePreviousDigest(t *testing.T) {
	now := mustTime("2025-06-24T09:00:00Z") // nine days after the previous digest
	prevEnd := mustTime("2025-06-15T09:00:00Z")

	store := &fakeStore{}
	store.setSchedule(Schedule{Every: weekly, Enabled: true})
	store.runs = []Run{{
		PeriodStart:    mustTime("2025-06-08T09:00:00Z").Unix(),
		PeriodEnd:      prevEnd.Unix(),
		PostureOverall: 75,
		Status:         StatusDelivered,
	}}
	notifier := &captureNotifier{}
	history := &stubHistory{}

	svc := New(Config{
		Store:    store,
		Posture:  &stubPosture{p: scoredPosture(82, now.Unix()), ok: true},
		Findings: stubFindings{},
		History:  history,
		Notifier: notifier,
		Logger:   quietLogger(),
		Now:      func() time.Time { return now },
	})

	if _, err := svc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	body := notifier.only().Detail
	if !strings.Contains(body, "Posture 82/100 (+7 since the last digest)") {
		t.Errorf("digest does not carry the delta against the previous digest:\n%s", body)
	}

	if len(history.fromSeen) != 1 {
		t.Fatalf("the transition history was queried %d times, want 1", len(history.fromSeen))
	}
	if history.fromSeen[0] != prevEnd.Unix() {
		t.Errorf("the reported window starts at %d, want the previous digest's end %d — "+
			"the window is arbitrary, not anchored to the last digest",
			history.fromSeen[0], prevEnd.Unix())
	}
	if history.toSeen[0] != now.Unix() {
		t.Errorf("the reported window ends at %d, want now %d", history.toSeen[0], now.Unix())
	}

	runs := store.recorded()
	if runs[len(runs)-1].PeriodStart != prevEnd.Unix() {
		t.Errorf("recorded window start = %d, want the previous digest's end %d",
			runs[len(runs)-1].PeriodStart, prevEnd.Unix())
	}
}

// TestTick_PreviousDigestWithoutAScoreYieldsNoDelta covers the third state:
// there IS a previous digest, but it carried no posture score. That is not a
// baseline of zero.
func TestTick_PreviousDigestWithoutAScoreYieldsNoDelta(t *testing.T) {
	now := mustTime("2025-06-22T09:00:00Z")
	store := &fakeStore{}
	store.setSchedule(Schedule{Every: weekly, Enabled: true})
	store.runs = []Run{{
		PeriodEnd:      mustTime("2025-06-15T09:00:00Z").Unix(),
		PostureOverall: PostureNotScored,
		Status:         StatusDelivered,
	}}
	notifier := &captureNotifier{}

	svc := New(Config{
		Store:    store,
		Posture:  &stubPosture{p: scoredPosture(82, now.Unix()), ok: true},
		Findings: stubFindings{},
		History:  &stubHistory{},
		Notifier: notifier,
		Logger:   quietLogger(),
		Now:      func() time.Time { return now },
	})

	if _, err := svc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	body := notifier.only().Detail
	if strings.Contains(body, "+82") {
		t.Errorf("a previous digest with no score was treated as a baseline of zero:\n%s", body)
	}
	if !strings.Contains(body, "no previous digest to compare against") {
		t.Errorf("digest does not state that no comparison is possible:\n%s", body)
	}
}

// ---------------------------------------------------------------------
// AC3 — delivery reuses T-2407's path, asserted through quiet hours.
// ---------------------------------------------------------------------

// quietHoursHarness builds a REAL findings.WebhookNotifier over a real
// findings.Scheduler and a real deferral queue, plus an HTTP target that
// counts what actually arrives. Nothing about delivery is stubbed: if the
// digest stopped going through Notify, the quiet-hours leg below would start
// receiving requests.
type quietHoursHarness struct {
	notifier *findings.WebhookNotifier
	pending  *memPending
	recorder *recordingRecorder
	hits     *atomic.Int64
	bodies   *sync.Map
	server   *httptest.Server
}

func newQuietHoursHarness(t *testing.T, now func() time.Time, quiet findings.QuietHours) *quietHoursHarness {
	t.Helper()

	var hits atomic.Int64
	var bodies sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		bodies.Store(n, string(buf))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	pending := &memPending{}
	recorder := &recordingRecorder{}
	rules := fixedRules{rules: []findings.AlertRule{{
		ID: "ar-1", Name: "ops", TargetKind: findings.TargetGeneric,
		TargetURL: srv.URL, Enabled: true, QuietHours: quiet,
	}}}

	notifier := findings.NewWebhookNotifier(findings.WebhookNotifierConfig{
		Rules:    rules,
		Recorder: recorder,
		Scheduler: findings.NewScheduler(findings.SchedulerConfig{
			Store: pending, Recorder: recorder, Logger: quietLogger(),
		}),
		Logger:      quietLogger(),
		Now:         now,
		Sleep:       func(context.Context, time.Duration) {},
		MaxAttempts: 3,
	})

	return &quietHoursHarness{
		notifier: notifier, pending: pending, recorder: recorder,
		hits: &hits, bodies: &bodies, server: srv,
	}
}

func TestTick_DeliveryGoesThroughTheAlertPathAndRespectsQuietHours(t *testing.T) {
	quiet := findings.QuietHours{Start: "22:00", End: "06:00", Zone: "UTC"}

	tests := []struct {
		name       string
		at         string
		wantStatus string
		wantHTTP   int64
		wantHeld   int
	}{
		{
			name: "inside quiet hours the digest is held, not delivered",
			at:   "2025-06-22T23:30:00Z", wantHTTP: 0, wantHeld: 1,
			wantStatus: findings.StatusDeferred,
		},
		{
			name: "outside quiet hours the same digest is delivered",
			at:   "2025-06-22T12:00:00Z", wantHTTP: 1, wantHeld: 0,
			wantStatus: findings.StatusDelivered,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := mustTime(tc.at)
			h := newQuietHoursHarness(t, func() time.Time { return now }, quiet)

			store := &fakeStore{}
			store.setSchedule(Schedule{Every: weekly, Enabled: true})
			svc := New(Config{
				Store:    store,
				Posture:  &stubPosture{p: scoredPosture(82, now.Unix()), ok: true},
				Findings: stubFindings{},
				History:  &stubHistory{},
				Notifier: h.notifier,
				Logger:   quietLogger(),
				Now:      func() time.Time { return now },
			})

			sent, err := svc.Tick(context.Background())
			if err != nil {
				t.Fatalf("Tick: %v", err)
			}
			if !sent {
				t.Fatal("Tick did not generate a digest")
			}

			if got := h.hits.Load(); got != tc.wantHTTP {
				t.Errorf("the webhook target received %d request(s), want %d — "+
					"the digest is not going through T-2407's scheduler", got, tc.wantHTTP)
			}
			if got := h.pending.count(); got != tc.wantHeld {
				t.Errorf("the deferral queue holds %d event(s), want %d", got, tc.wantHeld)
			}

			var statuses []string
			for _, d := range h.recorder.all() {
				statuses = append(statuses, d.Status)
			}
			if len(statuses) == 0 || statuses[len(statuses)-1] != tc.wantStatus {
				t.Errorf("delivery log statuses = %v, want the last to be %q", statuses, tc.wantStatus)
			}
		})
	}
}

// TestTick_HeldDigestSurvivesToTheFlush closes the loop on the deferral: the
// digest a quiet window held is the digest that goes out when the window ends,
// through the same Flush every deferred alert uses. Without this, "respects
// quiet hours" could be satisfied by dropping the digest entirely.
func TestTick_HeldDigestSurvivesToTheFlush(t *testing.T) {
	now := mustTime("2025-06-22T23:30:00Z")
	clock := func() time.Time { return now }
	h := newQuietHoursHarness(t, clock, findings.QuietHours{Start: "22:00", End: "06:00", Zone: "UTC"})

	store := &fakeStore{}
	store.setSchedule(Schedule{Every: weekly, Enabled: true})
	svc := New(Config{
		Store:    store,
		Posture:  &stubPosture{p: scoredPosture(82, now.Unix()), ok: true},
		Findings: stubFindings{},
		History:  &stubHistory{},
		Notifier: h.notifier,
		Logger:   quietLogger(),
		Now:      clock,
	})

	if _, err := svc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if h.hits.Load() != 0 {
		t.Fatalf("the digest was delivered inside quiet hours")
	}

	// Morning: the same notifier's flush loop, driven one pass by hand.
	now = mustTime("2025-06-23T06:00:00Z")
	delivered, err := h.notifier.Flush(context.Background())
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("Flush made %d deliveries, want 1", delivered)
	}
	if got := h.hits.Load(); got != 1 {
		t.Fatalf("the webhook target received %d request(s) after the quiet window ended, want 1", got)
	}
	body, ok := h.bodies.Load(int64(1))
	if !ok {
		t.Fatal("no body captured for the flushed delivery")
	}
	if !strings.Contains(body.(string), CheckScheduledDigest) {
		t.Errorf("the flushed delivery is not the digest: %s", body)
	}
}

// ---------------------------------------------------------------------
// AC4 — a failed delivery is retried and recorded, matching alert semantics.
// ---------------------------------------------------------------------

func TestTick_FailedDeliveryIsRetriedAndRecordedLikeAnyAlert(t *testing.T) {
	now := mustTime("2025-06-22T12:00:00Z")

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	recorder := &recordingRecorder{}
	rules := fixedRules{rules: []findings.AlertRule{{
		ID: "ar-1", Name: "ops", TargetKind: findings.TargetGeneric,
		TargetURL: srv.URL, Enabled: true,
	}}}
	const maxAttempts = 3
	notifier := findings.NewWebhookNotifier(findings.WebhookNotifierConfig{
		Rules:       rules,
		Recorder:    recorder,
		Logger:      quietLogger(),
		Now:         func() time.Time { return now },
		Sleep:       func(context.Context, time.Duration) {},
		MaxAttempts: maxAttempts,
	})

	store := &fakeStore{}
	store.setSchedule(Schedule{Every: weekly, Enabled: true})
	svc := New(Config{
		Store:    store,
		Posture:  &stubPosture{p: scoredPosture(82, now.Unix()), ok: true},
		Findings: stubFindings{},
		History:  &stubHistory{},
		Notifier: notifier,
		Logger:   quietLogger(),
		Now:      func() time.Time { return now },
	})

	sent, err := svc.Tick(context.Background())
	if !sent {
		t.Fatal("Tick did not generate a digest")
	}
	if err == nil {
		t.Fatal("Tick reported success although every delivery attempt failed")
	}

	if got := hits.Load(); got != maxAttempts {
		t.Errorf("the target was called %d time(s), want %d — the digest is not being retried",
			got, maxAttempts)
	}

	rows := recorder.all()
	if len(rows) != maxAttempts {
		t.Fatalf("delivery log has %d row(s), want one per attempt (%d)", len(rows), maxAttempts)
	}
	// Exactly the shape an ordinary failing alert produces: retrying until the
	// last attempt, then a terminal failure, each numbered, each carrying the
	// target's own refusal.
	for i, row := range rows {
		wantAttempt := i + 1
		wantStatus := findings.StatusRetrying
		if wantAttempt == maxAttempts {
			wantStatus = findings.StatusFailed
		}
		if row.Attempt != wantAttempt || row.Status != wantStatus {
			t.Errorf("delivery row %d = (attempt %d, status %q), want (attempt %d, status %q)",
				i, row.Attempt, row.Status, wantAttempt, wantStatus)
		}
		if row.Error == "" || !strings.Contains(row.Error, "500") {
			t.Errorf("delivery row %d records error %q, want the target's 500 response", i, row.Error)
		}
		if !strings.HasPrefix(row.FindingID, "digest:") {
			t.Errorf("delivery row %d is recorded against %q, want the digest's own id", i, row.FindingID)
		}
	}

	// And the digest's own run records the failure with detail, so the outcome
	// is answerable without joining against the alert log.
	runs := store.recorded()
	if len(runs) != 1 {
		t.Fatalf("recorded %d runs, want 1", len(runs))
	}
	if runs[0].Status != StatusFailed {
		t.Errorf("recorded run status = %q, want %q", runs[0].Status, StatusFailed)
	}
	if runs[0].Detail == "" || !strings.Contains(runs[0].Detail, "500") {
		t.Errorf("recorded run detail = %q, want the delivery failure's own message", runs[0].Detail)
	}
	// The period was still covered: a target that was down must not cause the
	// same window to be reported twice.
	if runs[0].PeriodEnd != now.Unix() {
		t.Errorf("recorded run period end = %d, want %d", runs[0].PeriodEnd, now.Unix())
	}
}

// ---------------------------------------------------------------------
// AC5 — a schedule change takes effect without a restart.
// ---------------------------------------------------------------------

// TestTick_ScheduleChangeTakesEffectWithoutARestart proves it with a clock the
// test owns and a single, never-rebuilt Service.
//
// The load-bearing step is the last pair: two ticks AT THE SAME INSTANT, with
// nothing changed between them except the stored schedule. The first must not
// fire and the second must. No amount of waiting can produce that difference,
// which is precisely why it is asserted this way.
func TestTick_ScheduleChangeTakesEffectWithoutARestart(t *testing.T) {
	now := mustTime("2025-06-22T09:00:00Z")
	store := &fakeStore{}
	store.setSchedule(Schedule{Every: weekly, Enabled: true})
	notifier := &captureNotifier{}

	svc := New(Config{
		Store:    store,
		Posture:  &stubPosture{p: scoredPosture(82, now.Unix()), ok: true},
		Findings: stubFindings{},
		History:  &stubHistory{},
		Notifier: notifier,
		Logger:   quietLogger(),
		Now:      func() time.Time { return now },
	})

	ticks := 0
	tick := func(t *testing.T, when time.Time, want bool, why string) {
		t.Helper()
		now = when
		ticks++
		sent, err := svc.Tick(context.Background())
		if err != nil {
			t.Fatalf("Tick at %s: %v", when.Format(time.RFC3339), err)
		}
		if sent != want {
			t.Fatalf("Tick at %s sent = %v, want %v: %s", when.Format(time.RFC3339), sent, want, why)
		}
	}

	base := now
	tick(t, base, true, "the first digest after enabling goes out immediately")
	tick(t, base.Add(2*time.Hour), false, "two hours into a weekly cadence, nothing is due")

	// The operator shortens the cadence. Nothing is restarted, nothing is
	// rebuilt, and the clock does not move.
	store.setSchedule(Schedule{Every: time.Hour, Enabled: true})
	tick(t, base.Add(2*time.Hour), true, "the shortened cadence must apply to the very next tick")

	// And disabling it stops the next one, again with no restart.
	store.setSchedule(Schedule{Every: time.Hour, Enabled: false})
	tick(t, base.Add(72*time.Hour), false, "a disabled schedule must stop firing immediately")

	// Re-enabling resumes against the same running Service.
	store.setSchedule(Schedule{Every: time.Hour, Enabled: true})
	tick(t, base.Add(72*time.Hour), true, "re-enabling must resume without a restart")

	// One read per tick, exactly. Fewer means the schedule is cached
	// somewhere, which is the failure mode this criterion exists to catch.
	if got := store.reads(); got != ticks {
		t.Errorf("the schedule was read %d time(s) across %d ticks; it is being cached", got, ticks)
	}
}

func TestTick_NoScheduleAndNoNotifierAreQuietNoOps(t *testing.T) {
	now := mustTime("2025-06-22T09:00:00Z")

	// No schedule row at all: an unconfigured daemon.
	store := &fakeStore{}
	svc := New(Config{Store: store, Logger: quietLogger(), Now: func() time.Time { return now }})
	sent, err := svc.Tick(context.Background())
	if err != nil || sent {
		t.Errorf("Tick with no schedule = (%v, %v), want (false, nil)", sent, err)
	}
	if len(store.recorded()) != 0 {
		t.Error("an unconfigured daemon recorded a digest run")
	}

	// A schedule but no notifier wired: the digest is still generated and
	// recorded, so the baseline advances rather than the feature silently
	// stalling.
	store2 := &fakeStore{}
	store2.setSchedule(Schedule{Every: weekly, Enabled: true})
	svc2 := New(Config{
		Store: store2, Findings: stubFindings{}, History: &stubHistory{},
		Logger: quietLogger(), Now: func() time.Time { return now },
	})
	sent, err = svc2.Tick(context.Background())
	if err != nil || !sent {
		t.Fatalf("Tick with no notifier = (%v, %v), want (true, nil)", sent, err)
	}
	if len(store2.recorded()) != 1 {
		t.Errorf("recorded %d runs, want 1", len(store2.recorded()))
	}
}

func TestTick_StoreErrorsAreWrappedNotSwallowed(t *testing.T) {
	now := mustTime("2025-06-22T09:00:00Z")
	boom := errors.New("boom")

	store := &fakeStore{schedErr: boom}
	svc := New(Config{Store: store, Logger: quietLogger(), Now: func() time.Time { return now }})
	if _, err := svc.Tick(context.Background()); !errors.Is(err, boom) {
		t.Errorf("Tick with an unreadable schedule: err = %v, want it to wrap %v", err, boom)
	}

	store2 := &fakeStore{recErr: boom}
	store2.setSchedule(Schedule{Every: weekly, Enabled: true})
	svc2 := New(Config{
		Store: store2, Findings: stubFindings{}, History: &stubHistory{},
		Logger: quietLogger(), Now: func() time.Time { return now },
	})
	if _, err := svc2.Tick(context.Background()); !errors.Is(err, boom) {
		t.Errorf("Tick with an unwritable run log: err = %v, want it to wrap %v", err, boom)
	}
}

// ---------------------------------------------------------------------
// Content assembly.
// ---------------------------------------------------------------------

func TestTick_ContentComesFromTheLiveStreamAndTheTransitionHistory(t *testing.T) {
	now := mustTime("2025-06-22T09:00:00Z")
	prevEnd := mustTime("2025-06-15T09:00:00Z")

	store := &fakeStore{}
	store.setSchedule(Schedule{Every: weekly, Enabled: true})
	store.runs = []Run{{PeriodEnd: prevEnd.Unix(), PostureOverall: 82, Status: StatusDelivered}}

	live := stubFindings{items: []findings.Finding{
		{
			ID: "capacity:capacity_link_forecast|iface:pve1:vmbr1", Source: findings.SourceCapacity,
			Check: "capacity_link_forecast", Severity: "warning",
			Detail: "vmbr1 projected to reach 100% in ~34 days", Nodes: []string{"pve1"},
		},
		{
			ID: "drift:bridge|pve2:vmbr0", Source: findings.SourceDrift,
			Check: "bridge", Severity: "warning", Detail: "vmbr0 has an unapplied pending change",
			Nodes: []string{"pve2"},
		},
		{
			ID: "health:carrier_down|iface:pve1:eno2", Source: findings.SourceHealth,
			Check: "carrier_down", Severity: "error", Detail: "eno2 has no carrier", Nodes: []string{"pve1"},
		},
	}}
	history := &stubHistory{transitions: []Transition{
		{FindingID: "health:carrier_down|iface:pve1:eno2", Transition: "new", At: prevEnd.Add(time.Hour).Unix()},
		// The same finding transitioning twice must not produce two rows.
		{FindingID: "health:carrier_down|iface:pve1:eno2", Transition: "escalated", At: prevEnd.Add(2 * time.Hour).Unix()},
		// A finding that has since left the stream: reported by id, described
		// as gone rather than invented.
		{FindingID: "health:mtu_mismatch|iface:pve3:eno1", Transition: "resolved", At: prevEnd.Add(3 * time.Hour).Unix()},
	}}

	notifier := &captureNotifier{}
	svc := New(Config{
		Store:    store,
		Posture:  &stubPosture{p: scoredPosture(82, now.Unix()), ok: true},
		Findings: live,
		History:  history,
		Notifier: notifier,
		Logger:   quietLogger(),
		Now:      func() time.Time { return now },
	})

	if _, err := svc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	body := notifier.only().Detail
	for _, want := range []string{
		"capacity_link_forecast", "vmbr1 projected to reach 100%",
		"bridge", "vmbr0 has an unapplied pending change",
		"carrier_down", "mtu_mismatch", resolvedDetail,
		docexport.HeadingDigestCapacity, docexport.HeadingDigestDrift,
		docexport.HeadingDigestOpened, docexport.HeadingDigestClosed,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("digest does not contain %q:\n%s", want, body)
		}
	}
	if got := strings.Count(body, "carrier_down"); got != 1 {
		t.Errorf("carrier_down appears %d times, want 1 — a finding that transitioned twice is reported twice", got)
	}

	runs := store.recorded()
	last := runs[len(runs)-1]
	if last.Opened != 1 || last.Closed != 1 || last.Drift != 1 || last.Capacity != 1 {
		t.Errorf("recorded counts = (opened %d, closed %d, drift %d, capacity %d), want (1, 1, 1, 1)",
			last.Opened, last.Closed, last.Drift, last.Capacity)
	}
	if last.Quiet {
		t.Error("a digest with four sections of content was recorded as quiet")
	}
}

func TestCheckFromID(t *testing.T) {
	tests := []struct{ id, want string }{
		{id: "health:carrier_down|iface:pve1:eno2", want: "carrier_down"},
		{id: "drift:bridge|pve2:vmbr0", want: "bridge"},
		{id: "capacity:capacity_link_forecast|iface:pve1:vmbr1", want: "capacity_link_forecast"},
		{id: "lldp:some-producer-key", want: "some-producer-key"},
		{id: "noseparator", want: "noseparator"},
		{id: "", want: ""},
	}
	for _, tc := range tests {
		if got := checkFromID(tc.id); got != tc.want {
			t.Errorf("checkFromID(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestRunLoop_ReturnsNilOnCancellation(t *testing.T) {
	svc := New(Config{Store: &fakeStore{}, Logger: quietLogger()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.RunLoop(ctx, time.Millisecond); err != nil {
		t.Errorf("RunLoop on a cancelled context = %v, want nil — a run-group actor that "+
			"returns an error takes the daemon down", err)
	}
}
