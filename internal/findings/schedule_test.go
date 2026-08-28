// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// memPending is an in-memory PendingStore. It assigns ids the way the real
// store adapter does, so the batching and clearing logic is exercised
// identically.
type memPending struct {
	rows []PendingDelivery
	seq  int
	mu   sync.Mutex
}

func (m *memPending) AddPending(_ context.Context, p PendingDelivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	p.ID = string(rune('a'+m.seq%26)) + time.Duration(m.seq).String()
	m.rows = append(m.rows, p)
	return nil
}

func (m *memPending) PendingFlushAt(_ context.Context, ruleID string) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var earliest time.Time
	found := false
	for _, r := range m.rows {
		if r.RuleID != ruleID {
			continue
		}
		if !found || r.FlushAt.Before(earliest) {
			earliest, found = r.FlushAt, true
		}
	}
	return earliest, found, nil
}

func (m *memPending) DuePending(_ context.Context, now time.Time) ([]PendingDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []PendingDelivery
	for _, r := range m.rows {
		if !r.FlushAt.After(now) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

func (m *memPending) DeletePending(_ context.Context, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	kept := m.rows[:0]
	for _, r := range m.rows {
		if !drop[r.ID] {
			kept = append(kept, r)
		}
	}
	m.rows = kept
	return nil
}

func (m *memPending) len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rows)
}

// capturingTarget is a webhook endpoint that records every body it receives.
type capturingTarget struct {
	srv    *httptest.Server
	bodies []string
	mu     sync.Mutex
}

func newCapturingTarget(t *testing.T) *capturingTarget {
	t.Helper()
	c := &capturingTarget{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies = append(c.bodies, string(body))
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *capturingTarget) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.bodies)
}

func (c *capturingTarget) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.bodies...)
}

// fixedRules is a one-rule AlertRuleProvider whose rule the test can mutate.
type fixedRules struct {
	rule AlertRule
	mu   sync.Mutex
}

func (f *fixedRules) AlertRules(context.Context) ([]AlertRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []AlertRule{f.rule}, nil
}

// clock is a settable time source shared by the notifier and the test.
type clock struct {
	t  time.Time
	mu sync.Mutex
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

type scheduleHarness struct {
	notifier *WebhookNotifier
	target   *capturingTarget
	pending  *memPending
	rules    *fixedRules
	clock    *clock
}

func newScheduleHarness(t *testing.T, rule AlertRule, start time.Time) *scheduleHarness {
	t.Helper()
	target := newCapturingTarget(t)
	rule.TargetURL = target.srv.URL
	rule.TargetKind = TargetGeneric
	rule.Enabled = true
	if rule.ID == "" {
		rule.ID = "rule-1"
	}

	pending := &memPending{}
	rules := &fixedRules{rule: rule}
	clk := &clock{t: start}

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	n := NewWebhookNotifier(WebhookNotifierConfig{
		Rules:  rules,
		Logger: quiet,
		Now:    clk.now,
		Sleep:  func(context.Context, time.Duration) {},
		Scheduler: NewScheduler(SchedulerConfig{
			Store:  pending,
			Logger: quiet,
		}),
	})
	return &scheduleHarness{notifier: n, target: target, pending: pending, rules: rules, clock: clk}
}

func warning(id string) Finding {
	return Finding{ID: id, Source: SourceDrift, Check: "mtu_mismatch", Severity: SeverityWarning, Detail: "MTU differs on " + id, Nodes: []string{"pve1"}}
}

func errorFinding(id string) Finding {
	return Finding{ID: id, Source: SourceHealth, Check: "uplink_down", Severity: SeverityError, Detail: "uplink down on " + id, Nodes: []string{"pve1"}}
}

// TestDigest_TenEventsInOneWindowProduceOneDeliveryNamingTen is AC1.
func TestDigest_TenEventsInOneWindowProduceOneDeliveryNamingTen(t *testing.T) {
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	h := newScheduleHarness(t, AlertRule{DigestWindow: 5 * time.Minute}, start)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		h.clock.set(start.Add(time.Duration(i) * 10 * time.Second))
		if err := h.notifier.Notify(ctx, warning("f"+string(rune('0'+i))), TransitionNew); err != nil {
			t.Fatalf("Notify %d: %v", i, err)
		}
	}
	if got := h.target.count(); got != 0 {
		t.Fatalf("%d deliveries during the digest window, want 0 — nothing should go out until it closes", got)
	}
	if got := h.pending.len(); got != 10 {
		t.Fatalf("%d events queued, want 10", got)
	}

	// Still inside the window: a flush must deliver nothing.
	h.clock.set(start.Add(4 * time.Minute))
	if n, err := h.notifier.Flush(ctx); err != nil || n != 0 {
		t.Fatalf("Flush inside the window = (%d, %v), want (0, nil)", n, err)
	}

	// The window measures from the FIRST event, so it closes 5m after start,
	// not 5m after the last arrival.
	h.clock.set(start.Add(5*time.Minute + time.Second))
	n, err := h.notifier.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if n != 1 {
		t.Fatalf("Flush delivered %d times, want exactly 1", n)
	}
	if got := h.target.count(); got != 1 {
		t.Fatalf("target received %d requests, want exactly 1", got)
	}
	body := h.target.all()[0]
	if !strings.Contains(body, "10 alerts") {
		t.Errorf("digest body does not name ten events: %s", body)
	}
	if !strings.Contains(body, "10 warning") {
		t.Errorf("digest body does not summarise by severity: %s", body)
	}
	if !strings.Contains(body, "drift (10)") {
		t.Errorf("digest body does not summarise by source: %s", body)
	}
	if h.pending.len() != 0 {
		t.Errorf("%d events still queued after delivery", h.pending.len())
	}
}

// TestQuietHours_EventIsDeliveredAfterTheWindow is AC2, and it asserts the
// delivery rather than the absence: "not dropped" is the claim, and only a
// delivery proves it.
func TestQuietHours_EventIsDeliveredAfterTheWindow(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatalf("loading zone: %v", err)
	}
	night := time.Date(2026, 6, 1, 23, 0, 0, 0, loc)
	h := newScheduleHarness(t, AlertRule{
		QuietHours: QuietHours{Start: "22:00", End: "06:00", Zone: "Europe/Bucharest"},
	}, night)
	ctx := context.Background()

	if err := h.notifier.Notify(ctx, warning("f1"), TransitionNew); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if got := h.target.count(); got != 0 {
		t.Fatalf("%d deliveries at 23:00 inside quiet hours, want 0", got)
	}
	if h.pending.len() != 1 {
		t.Fatalf("%d events queued, want 1 — a deferred alert must be held, not dropped", h.pending.len())
	}

	// 05:00 is still inside a 22:00-06:00 window.
	h.clock.set(time.Date(2026, 6, 2, 5, 0, 0, 0, loc))
	if n, _ := h.notifier.Flush(ctx); n != 0 {
		t.Fatalf("delivered %d at 05:00, still inside the window", n)
	}

	h.clock.set(time.Date(2026, 6, 2, 6, 0, 1, 0, loc))
	if n, err := h.notifier.Flush(ctx); err != nil || n != 1 {
		t.Fatalf("Flush after the window = (%d, %v), want (1, nil)", n, err)
	}
	if got := h.target.count(); got != 1 {
		t.Fatalf("target received %d requests after the window, want exactly 1", got)
	}
	// A one-event batch delivers the original finding untouched — a "digest
	// of one" that looked different would make the window visible for no
	// benefit.
	if body := h.target.all()[0]; !strings.Contains(body, "MTU differs on f1") {
		t.Errorf("the delivered body is not the original finding: %s", body)
	}
}

// TestQuietHours_ErrorSeverityBypassesByDefault is AC3, plus the control that
// makes it meaningful: the same event with the bypass switched off is held.
// Without the control this test would pass on a build where quiet hours had
// stopped working entirely.
func TestQuietHours_ErrorSeverityBypassesByDefault(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatalf("loading zone: %v", err)
	}
	night := time.Date(2026, 6, 1, 23, 0, 0, 0, loc)
	quiet := QuietHours{Start: "22:00", End: "06:00", Zone: "Europe/Bucharest"}
	ctx := context.Background()

	h := newScheduleHarness(t, AlertRule{QuietHours: quiet, BypassQuietHoursOnError: true}, night)
	if err := h.notifier.Notify(ctx, errorFinding("f1"), TransitionNew); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if got := h.target.count(); got != 1 {
		t.Fatalf("an error-severity finding at 23:00 produced %d deliveries, want 1 — it must bypass quiet hours", got)
	}
	if h.pending.len() != 0 {
		t.Errorf("a bypassing event was queued; it should have gone straight out")
	}

	// Control 1: a warning at the same instant is held.
	h2 := newScheduleHarness(t, AlertRule{QuietHours: quiet, BypassQuietHoursOnError: true}, night)
	if err := h2.notifier.Notify(ctx, warning("f2"), TransitionNew); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if got := h2.target.count(); got != 0 {
		t.Errorf("a warning at 23:00 produced %d deliveries, want 0", got)
	}

	// Control 2: the bypass is a per-rule override, not a hard-coded rule.
	h3 := newScheduleHarness(t, AlertRule{QuietHours: quiet, BypassQuietHoursOnError: false}, night)
	if err := h3.notifier.Notify(ctx, errorFinding("f3"), TransitionNew); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if got := h3.target.count(); got != 0 {
		t.Errorf("an error finding was delivered with the bypass disabled (%d deliveries); the override does nothing", got)
	}
	if h3.pending.len() != 1 {
		t.Errorf("%d events queued with the bypass disabled, want 1", h3.pending.len())
	}
}

// TestQuietHours_CrossingMidnight is AC4.
func TestQuietHours_CrossingMidnight(t *testing.T) {
	q := QuietHours{Start: "22:00", End: "06:00", Zone: "Europe/Bucharest"}
	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatalf("loading zone: %v", err)
	}

	cases := []struct {
		at   time.Time
		name string
		want bool
	}{
		{name: "23:00 — after the start, before midnight", at: time.Date(2026, 6, 1, 23, 0, 0, 0, loc), want: true},
		{name: "05:00 — after midnight, before the end", at: time.Date(2026, 6, 2, 5, 0, 0, 0, loc), want: true},
		{name: "22:00 — the boundary is inclusive at the start", at: time.Date(2026, 6, 1, 22, 0, 0, 0, loc), want: true},
		{name: "06:00 — the boundary is exclusive at the end", at: time.Date(2026, 6, 2, 6, 0, 0, 0, loc), want: false},
		{name: "12:00 — the middle of the day", at: time.Date(2026, 6, 2, 12, 0, 0, 0, loc), want: false},
		{name: "21:59 — one minute before", at: time.Date(2026, 6, 1, 21, 59, 0, 0, loc), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := q.Contains(tc.at)
			if err != nil {
				t.Fatalf("Contains: %v", err)
			}
			if got != tc.want {
				t.Errorf("Contains(%s) = %v, want %v", tc.at.Format(time.RFC3339), got, tc.want)
			}
		})
	}

	// A same-day window must not accidentally inherit the wrap-around logic.
	day := QuietHours{Start: "09:00", End: "17:00", Zone: "Europe/Bucharest"}
	if got, _ := day.Contains(time.Date(2026, 6, 1, 23, 0, 0, 0, loc)); got {
		t.Error("a 09:00-17:00 window contains 23:00; the wrap-around branch is being taken for a same-day window")
	}
}

// TestQuietHours_AcrossADSTTransition is AC5.
//
// Both directions, over a real zone, driven by a fake clock ticking through
// the transition. The failure this guards against is not theoretical: an
// implementation that computed the window end as "now + 7h" rather than as a
// wall-clock instant delivers an hour early or an hour late twice a year, and
// one that recomputed the deadline on every tick can deliver twice.
func TestQuietHours_AcrossADSTTransition(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatalf("loading zone: %v", err)
	}
	quiet := QuietHours{Start: "22:00", End: "06:00", Zone: "Europe/Bucharest"}

	cases := []struct {
		enqueue time.Time
		wantEnd time.Time
		name    string
	}{
		{
			// Spring forward: 2026-03-29, 03:00 becomes 04:00. The night is
			// one hour shorter in absolute terms; 06:00 is still 06:00.
			name:    "spring forward",
			enqueue: time.Date(2026, 3, 28, 23, 0, 0, 0, loc),
			wantEnd: time.Date(2026, 3, 29, 6, 0, 0, 0, loc),
		},
		{
			// Fall back: 2026-10-25, 04:00 becomes 03:00. The night is one
			// hour longer.
			name:    "fall back",
			enqueue: time.Date(2026, 10, 24, 23, 0, 0, 0, loc),
			wantEnd: time.Date(2026, 10, 25, 6, 0, 0, 0, loc),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			end, err := quiet.NextEnd(tc.enqueue)
			if err != nil {
				t.Fatalf("NextEnd: %v", err)
			}
			if !end.Equal(tc.wantEnd) {
				t.Fatalf("NextEnd(%s) = %s, want %s (wall clock, not a fixed offset)",
					tc.enqueue.Format(time.RFC3339), end.Format(time.RFC3339), tc.wantEnd.Format(time.RFC3339))
			}

			h := newScheduleHarness(t, AlertRule{QuietHours: quiet}, tc.enqueue)
			ctx := context.Background()
			if err := h.notifier.Notify(ctx, warning("f1"), TransitionNew); err != nil {
				t.Fatalf("Notify: %v", err)
			}

			// Tick every 30 minutes from the enqueue to two hours past the
			// window end, exactly as the daemon's flush loop would.
			deliveries := 0
			for at := tc.enqueue; at.Before(tc.wantEnd.Add(2 * time.Hour)); at = at.Add(30 * time.Minute) {
				h.clock.set(at)
				n, flushErr := h.notifier.Flush(ctx)
				if flushErr != nil {
					t.Fatalf("Flush at %s: %v", at.Format(time.RFC3339), flushErr)
				}
				if n > 0 && at.Before(tc.wantEnd) {
					t.Errorf("delivered at %s, before the window ended at %s",
						at.Format(time.RFC3339), tc.wantEnd.Format(time.RFC3339))
				}
				deliveries += n
			}

			if deliveries != 1 {
				t.Fatalf("%d deliveries across the transition, want exactly 1 (0 = dropped, 2 = double-delivered)", deliveries)
			}
			if got := h.target.count(); got != 1 {
				t.Fatalf("target received %d requests, want exactly 1", got)
			}
		})
	}
}

// TestSchedule_NoWindowsMeansUnchangedImmediateDelivery is the regression
// guard for every existing deployment: a rule with neither feature configured
// must behave exactly as it did before T-2407.
func TestSchedule_NoWindowsMeansUnchangedImmediateDelivery(t *testing.T) {
	start := time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC)
	h := newScheduleHarness(t, AlertRule{}, start)
	if err := h.notifier.Notify(context.Background(), warning("f1"), TransitionNew); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if got := h.target.count(); got != 1 {
		t.Fatalf("%d deliveries with no quiet hours and no digest, want 1", got)
	}
	if h.pending.len() != 0 {
		t.Errorf("an event was queued by a rule with no scheduling configured")
	}
}

// TestFlush_DiscardsEventsHeldForARuleThatIsGone stops the queue growing
// without bound when an operator deletes a rule mid-window.
func TestFlush_DiscardsEventsHeldForARuleThatIsGone(t *testing.T) {
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	h := newScheduleHarness(t, AlertRule{DigestWindow: time.Minute}, start)
	ctx := context.Background()
	if err := h.notifier.Notify(ctx, warning("f1"), TransitionNew); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	h.rules.mu.Lock()
	h.rules.rule.Enabled = false
	h.rules.mu.Unlock()

	h.clock.set(start.Add(2 * time.Minute))
	if _, err := h.notifier.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := h.target.count(); got != 0 {
		t.Errorf("%d deliveries to a disabled rule, want 0 — the operator's most recent instruction wins", got)
	}
	if h.pending.len() != 0 {
		t.Errorf("%d events still queued for a disabled rule; the queue would grow without bound", h.pending.len())
	}
}

// TestQuietHours_ValidateRejectsWhatWouldFailSilently keeps a malformed
// window from being stored and only discovered at 22:00.
func TestQuietHours_ValidateRejectsWhatWouldFailSilently(t *testing.T) {
	cases := []struct {
		name string
		q    QuietHours
		ok   bool
	}{
		{"unset", QuietHours{}, true},
		{"valid crossing midnight", QuietHours{Start: "22:00", End: "06:00"}, true},
		{"only a start", QuietHours{Start: "22:00"}, false},
		{"only an end", QuietHours{End: "06:00"}, false},
		{"same start and end", QuietHours{Start: "22:00", End: "22:00"}, false},
		{"not HH:MM", QuietHours{Start: "10pm", End: "06:00"}, false},
		{"single-digit hour", QuietHours{Start: "9:00", End: "17:00"}, false},
		{"hour out of range", QuietHours{Start: "24:00", End: "06:00"}, false},
		{"minute out of range", QuietHours{Start: "22:60", End: "06:00"}, false},
		{"unknown zone", QuietHours{Start: "22:00", End: "06:00", Zone: "Mars/Olympus"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.q.Validate()
			if tc.ok && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
			if !tc.ok && err == nil {
				t.Error("Validate() = nil; this window would be accepted and then fail at delivery time")
			}
		})
	}
}
