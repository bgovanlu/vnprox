// SPDX-License-Identifier: Apache-2.0

package main

// autocapture_test.go covers T-4101's anomaly-triggered-capture wiring: the
// gate defaults to inert, a newly-appeared baseline finding arms exactly one
// capture scoped to its own Ref, caps are never re-derived (the manual
// ceiling applies unmodified), a burst of simultaneous new anomalies is
// bounded by the storm cap, a failed Start does not consume that cap, and an
// anomaly-armed session survives a daemon restart / expires past retention
// through capture.Coordinator's own existing sweep — no T-4101-specific
// orphan handling exists to test separately.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/baseline"
	"github.com/bgovanlu/vnprox/internal/capture"
	"github.com/bgovanlu/vnprox/internal/capturemock"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/findings"
)

// --- fakes: a real *capture.Coordinator over an in-memory store/auditor,
// mirroring internal/capture/coordinator_test.go's own fakes (unexported
// there, so re-declared here against the same capture.SessionStore/Auditor
// seams) plus the scripted capturemock.Agent every other cmd/vnproxd capture
// wiring test uses in place of a real tcpdump/AF_PACKET backend. ---

type acFakeStore struct {
	rows map[string]capture.Session
	mu   sync.Mutex
}

func newACFakeStore() *acFakeStore { return &acFakeStore{rows: map[string]capture.Session{}} }

func (s *acFakeStore) Upsert(_ context.Context, sess capture.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[sess.ID] = sess
	return nil
}

func (s *acFakeStore) Get(_ context.Context, id string) (capture.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return capture.Session{}, capture.ErrNotFound
	}
	return r, nil
}

func (s *acFakeStore) ByGroup(_ context.Context, groupID string) ([]capture.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []capture.Session
	for _, r := range s.rows {
		if r.GroupID == groupID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *acFakeStore) List(_ context.Context) ([]capture.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]capture.Session, 0, len(s.rows))
	for _, r := range s.rows {
		out = append(out, r)
	}
	return out, nil
}

func (s *acFakeStore) ListGroups(_ context.Context) ([]string, error) { return nil, nil }

func (s *acFakeStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, id)
	return nil
}

func (s *acFakeStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}

type acFakeAuditor struct {
	events []capture.AuditEvent
	mu     sync.Mutex
}

func (a *acFakeAuditor) AppendCapture(_ context.Context, e capture.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	return nil
}

func (a *acFakeAuditor) byAction(action string) []capture.AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []capture.AuditEvent
	for _, e := range a.events {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

// newACCoordinator builds a real *capture.Coordinator identical in shape to
// cmd/vnproxd/capture.go's production setupCapture, differing only in the
// in-memory store/audit backing and the injectable clock.
func newACCoordinator(t *testing.T, store *acFakeStore, auditor *acFakeAuditor, now func() time.Time, ceilings capture.Caps) *capture.Coordinator {
	t.Helper()
	return capture.New(capture.Config{
		Agent:     capturemock.NewAgent(),
		Resolver:  capture.RefResolver{},
		Store:     store,
		Audit:     auditor,
		LocalNode: func() string { return "pve1" },
		Now:       now,
		Root:      t.TempDir(),
		Ceilings:  ceilings,
	})
}

// testBaselineSvc builds a *baselineService whose anomalyForFinding cache is
// pre-seeded directly (same package, direct field access; the real service
// would fill this from a RecentAnomalies call this test never needs to
// exercise — see baseline.go's lastAnomalies doc comment).
func testBaselineSvc(anomalies map[string]baseline.Anomaly) *baselineService {
	svc := newBaselineService(nil, config.BaselineConfig{}, testLogger())
	svc.lastAnomalies = anomalies
	return svc
}

func newPortAnomaly(ref, subject string) baseline.Anomaly {
	return baseline.Anomaly{Ref: ref, Class: baseline.ClassNewPort, Subject: subject}
}

func baselineFindingFor(a baseline.Anomaly) findings.Finding {
	return findings.Finding{
		ID:       findings.BaselineFindingID(a),
		Source:   findings.SourceBaseline,
		Check:    string(a.Class),
		Severity: findings.SeverityWarning,
		Refs:     []string{a.Ref},
	}
}

func cacheOf(anomalies ...baseline.Anomaly) map[string]baseline.Anomaly {
	out := make(map[string]baseline.Anomaly, len(anomalies))
	for _, a := range anomalies {
		out[findings.BaselineFindingID(a)] = a
	}
	return out
}

// TestAutoCaptureTracker_GateOff is T-4101 AC1: off by default, and an
// explicit disabled config starts nothing at all — not even a partial
// attempt — when a baseline anomaly finding appears.
func TestAutoCaptureTracker_GateOff(t *testing.T) {
	store := newACFakeStore()
	auditor := &acFakeAuditor{}
	coord := newACCoordinator(t, store, auditor, time.Now, capture.Caps{})

	a := newPortAnomaly("bridge:pve1:vmbr0", "tcp/6667")
	svc := testBaselineSvc(cacheOf(a))
	tr := newAutoCaptureTracker(svc, autoCaptureConfig{Enabled: false, MaxPerWindow: 3, Window: time.Hour}, testLogger())
	tr.set(coord)

	tr.observe(context.Background(), []findings.Finding{baselineFindingFor(a)})

	if got := store.count(); got != 0 {
		t.Fatalf("gate off: expected 0 capture sessions, got %d", got)
	}
	if got := len(auditor.byAction("capture.start")); got != 0 {
		t.Fatalf("gate off: expected 0 capture.start audit rows, got %d", got)
	}

	// Also verify the zero-value autoCaptureConfig (what cfg.Baseline decodes
	// to when [baseline] is entirely absent from a TOML file) is inert too —
	// the actual "off by default" contract, not just an explicit false.
	var zero autoCaptureConfig
	tr2 := newAutoCaptureTracker(svc, zero, testLogger())
	tr2.set(coord)
	tr2.observe(context.Background(), []findings.Finding{baselineFindingFor(a)})
	if got := store.count(); got != 0 {
		t.Fatalf("zero-value config: expected 0 capture sessions, got %d", got)
	}
}

// TestAutoCaptureTracker_FiresOnceOnNewAnomaly is T-4101's core trigger path:
// a newly-appeared baseline finding arms exactly one capture scoped to the
// anomaly's own Ref, and a persisting (not newly-appeared) finding does not
// re-fire on a later cycle. The audit row's actor recovers the triggering
// finding id (AC3: "recoverable from the audit row").
func TestAutoCaptureTracker_FiresOnceOnNewAnomaly(t *testing.T) {
	store := newACFakeStore()
	auditor := &acFakeAuditor{}
	coord := newACCoordinator(t, store, auditor, time.Now, capture.Caps{})

	a := newPortAnomaly("bridge:pve1:vmbr0", "tcp/6667")
	fid := findings.BaselineFindingID(a)
	svc := testBaselineSvc(cacheOf(a))
	tr := newAutoCaptureTracker(svc, autoCaptureConfig{Enabled: true, MaxPerWindow: 3, Window: time.Hour}, testLogger())
	tr.set(coord)

	ctx := context.Background()
	fs := []findings.Finding{baselineFindingFor(a)}
	tr.observe(ctx, fs) // cycle 1: newly appeared -> fires
	tr.observe(ctx, fs) // cycle 2: same finding still present -> not new again

	if got := store.count(); got != 1 {
		t.Fatalf("expected exactly 1 capture session across two cycles with a persisting finding, got %d", got)
	}
	starts := auditor.byAction("capture.start")
	if len(starts) != 1 {
		t.Fatalf("expected exactly 1 capture.start audit row, got %d", len(starts))
	}
	wantActor := autoCaptureActor + ":" + fid
	if starts[0].Actor != wantActor {
		t.Errorf("audit actor = %q, want %q (must recover the triggering finding id)", starts[0].Actor, wantActor)
	}
	if starts[0].TargetRef != a.Ref {
		t.Errorf("audit targetRef = %q, want %q", starts[0].TargetRef, a.Ref)
	}
}

// TestAutoCaptureTracker_ScopedToAnomalyRefOnly: two distinct anomalies on
// two distinct Refs each arm their OWN capture against their OWN Ref, never
// each other's.
func TestAutoCaptureTracker_ScopedToAnomalyRefOnly(t *testing.T) {
	store := newACFakeStore()
	auditor := &acFakeAuditor{}
	coord := newACCoordinator(t, store, auditor, time.Now, capture.Caps{})

	a1 := newPortAnomaly("bridge:pve1:vmbr0", "tcp/6667")
	a2 := newPortAnomaly("bridge:pve1:vmbr1", "tcp/4444")
	svc := testBaselineSvc(cacheOf(a1, a2))
	tr := newAutoCaptureTracker(svc, autoCaptureConfig{Enabled: true, MaxPerWindow: 5, Window: time.Hour}, testLogger())
	tr.set(coord)

	tr.observe(context.Background(), []findings.Finding{baselineFindingFor(a1), baselineFindingFor(a2)})

	sessions, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions (one per anomaly Ref), got %d", len(sessions))
	}
	gotRefs := map[string]bool{}
	for _, s := range sessions {
		gotRefs[s.TargetRef] = true
		if s.Filter != "port 6667" && s.Filter != "port 4444" {
			t.Errorf("session for ref %q has unexpected filter %q", s.TargetRef, s.Filter)
		}
	}
	if !gotRefs[a1.Ref] || !gotRefs[a2.Ref] {
		t.Fatalf("session TargetRefs = %v, want exactly {%q, %q}", gotRefs, a1.Ref, a2.Ref)
	}
}

// TestAutoCaptureTracker_CapsMatchManualCeilings is T-4101 AC2: an
// anomaly-armed session's caps are whatever the SAME configured ceiling a
// manual session gets — never a second cap path.
func TestAutoCaptureTracker_CapsMatchManualCeilings(t *testing.T) {
	store := newACFakeStore()
	auditor := &acFakeAuditor{}
	ceilings := capture.Caps{MaxDurationSec: 120, MaxBytes: 4096, MaxPackets: 50, RetentionHours: 2}
	coord := newACCoordinator(t, store, auditor, time.Now, ceilings)

	a := newPortAnomaly("bridge:pve1:vmbr0", "tcp/6667")
	svc := testBaselineSvc(cacheOf(a))
	tr := newAutoCaptureTracker(svc, autoCaptureConfig{Enabled: true, MaxPerWindow: 3, Window: time.Hour}, testLogger())
	tr.set(coord)

	tr.observe(context.Background(), []findings.Finding{baselineFindingFor(a)})

	sessions, err := store.List(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("expected exactly 1 session, got %d (err=%v)", len(sessions), err)
	}

	// A manual capture.StartRequest with zero cap fields gets clamped to the
	// exact same ceiling (Coordinator.clampCaps) — reproduced here as the
	// independent expectation, not read back off the session under test.
	want := capture.Caps{
		MaxDurationSec: ceilings.MaxDurationSec, MaxBytes: ceilings.MaxBytes,
		MaxPackets: ceilings.MaxPackets, RetentionHours: ceilings.RetentionHours,
	}
	if sessions[0].Caps != want {
		t.Errorf("anomaly-armed session caps = %+v, want the manual ceiling %+v", sessions[0].Caps, want)
	}
}

// TestAutoCaptureTracker_StormSuppression is T-4101's storm cap: three
// distinct anomalies newly appear in the SAME cycle; MaxPerWindow=2 must cap
// the fallout at exactly 2 captures, not 3 (nor a hundred).
func TestAutoCaptureTracker_StormSuppression(t *testing.T) {
	store := newACFakeStore()
	auditor := &acFakeAuditor{}
	fixed := time.Unix(1_700_000_000, 0)
	nowFn := func() time.Time { return fixed }
	coord := newACCoordinator(t, store, auditor, nowFn, capture.Caps{})

	a1 := newPortAnomaly("bridge:pve1:vmbr0", "tcp/1")
	a2 := newPortAnomaly("bridge:pve1:vmbr1", "tcp/2")
	a3 := newPortAnomaly("bridge:pve1:vmbr2", "tcp/3")
	svc := testBaselineSvc(cacheOf(a1, a2, a3))
	tr := newAutoCaptureTracker(svc, autoCaptureConfig{Enabled: true, MaxPerWindow: 2, Window: time.Hour}, testLogger())
	tr.now = nowFn
	tr.set(coord)

	fs := []findings.Finding{baselineFindingFor(a1), baselineFindingFor(a2), baselineFindingFor(a3)}
	tr.observe(context.Background(), fs)

	if got := store.count(); got != 2 {
		t.Fatalf("MaxPerWindow=2 with 3 simultaneous new anomalies: expected exactly 2 captures started, got %d", got)
	}

	// The window has NOT elapsed: a later cycle with the same three findings
	// (none newly-appeared, since prev now holds all three ids) must not
	// start any more captures either, storm cap or not.
	tr.observe(context.Background(), fs)
	if got := store.count(); got != 2 {
		t.Fatalf("a later cycle over the same (non-new) findings started more captures: got %d, want 2", got)
	}
}

// TestAutoCaptureTracker_StormSuppression_WindowSlides shows the storm cap
// is a rolling window, not a lifetime cap: once cfg.Window has elapsed, a
// GENUINELY new anomaly may fire again.
func TestAutoCaptureTracker_StormSuppression_WindowSlides(t *testing.T) {
	store := newACFakeStore()
	auditor := &acFakeAuditor{}
	clock := time.Unix(1_700_000_000, 0)
	nowFn := func() time.Time { return clock }
	coord := newACCoordinator(t, store, auditor, nowFn, capture.Caps{})

	a1 := newPortAnomaly("bridge:pve1:vmbr0", "tcp/1")
	svc := testBaselineSvc(cacheOf(a1))
	tr := newAutoCaptureTracker(svc, autoCaptureConfig{Enabled: true, MaxPerWindow: 1, Window: time.Hour}, testLogger())
	tr.now = nowFn
	tr.set(coord)

	tr.observe(context.Background(), []findings.Finding{baselineFindingFor(a1)})
	if got := store.count(); got != 1 {
		t.Fatalf("expected first anomaly to fire, got %d sessions", got)
	}

	// A second, distinct anomaly arrives inside the same window: suppressed.
	a2 := newPortAnomaly("bridge:pve1:vmbr1", "tcp/2")
	svc.lastAnomalies[findings.BaselineFindingID(a2)] = a2
	tr.observe(context.Background(), []findings.Finding{baselineFindingFor(a1), baselineFindingFor(a2)})
	if got := store.count(); got != 1 {
		t.Fatalf("second anomaly inside the same window: expected still 1 session, got %d", got)
	}

	// The window slides past: a third, distinct anomaly now gets budget.
	clock = clock.Add(2 * time.Hour)
	a3 := newPortAnomaly("bridge:pve1:vmbr2", "tcp/3")
	svc.lastAnomalies[findings.BaselineFindingID(a3)] = a3
	tr.observe(context.Background(), []findings.Finding{baselineFindingFor(a1), baselineFindingFor(a2), baselineFindingFor(a3)})
	if got := store.count(); got != 2 {
		t.Fatalf("after the window slid past, expected a new anomaly to get budget: got %d sessions, want 2", got)
	}
}

// TestAutoCaptureTracker_FailedStartDoesNotConsumeBudget: an anomaly whose
// Ref internal/capture's RefResolver cannot scope (a guest-nic ref — an
// existing, pre-T-4101 limitation, not something this tracker works around)
// fails Start without ever writing a session or audit row, and — because no
// capture actually started — must not burn a storm-cap slot a genuinely
// resolvable anomaly needs.
func TestAutoCaptureTracker_FailedStartDoesNotConsumeBudget(t *testing.T) {
	store := newACFakeStore()
	auditor := &acFakeAuditor{}
	coord := newACCoordinator(t, store, auditor, time.Now, capture.Caps{})

	unresolvable := newPortAnomaly("guest-nic:pve1:100/net0", "tcp/22")
	resolvable := newPortAnomaly("bridge:pve1:vmbr0", "tcp/6667")
	svc := testBaselineSvc(cacheOf(unresolvable, resolvable))
	tr := newAutoCaptureTracker(svc, autoCaptureConfig{Enabled: true, MaxPerWindow: 1, Window: time.Hour}, testLogger())
	tr.set(coord)

	fs := []findings.Finding{baselineFindingFor(unresolvable), baselineFindingFor(resolvable)}
	tr.observe(context.Background(), fs)

	if got := store.count(); got != 1 {
		t.Fatalf("expected the resolvable anomaly's capture to start despite the unresolvable one failing first, got %d sessions", got)
	}
	sessions, _ := store.List(context.Background())
	if len(sessions) == 1 && sessions[0].TargetRef != resolvable.Ref {
		t.Errorf("started session targets %q, want the resolvable anomaly's ref %q", sessions[0].TargetRef, resolvable.Ref)
	}
}

// TestAutoCaptureTracker_ExpiresPastRetention is T-4101's retention
// requirement: an anomaly-armed session is an ordinary capture.Coordinator
// session, so it is purged by the SAME Sweep every capture uses, once its
// own configured retention window elapses — not before.
func TestAutoCaptureTracker_ExpiresPastRetention(t *testing.T) {
	store := newACFakeStore()
	auditor := &acFakeAuditor{}
	start := time.Unix(1_700_000_000, 0)
	clock := start
	nowFn := func() time.Time { return clock }
	ceilings := capture.Caps{MaxDurationSec: 300, MaxBytes: 1 << 20, MaxPackets: 1000, RetentionHours: 1}
	coord := newACCoordinator(t, store, auditor, nowFn, ceilings)

	a := newPortAnomaly("bridge:pve1:vmbr0", "tcp/6667")
	svc := testBaselineSvc(cacheOf(a))
	tr := newAutoCaptureTracker(svc, autoCaptureConfig{Enabled: true, MaxPerWindow: 3, Window: time.Hour}, testLogger())
	tr.now = nowFn
	tr.set(coord)

	tr.observe(context.Background(), []findings.Finding{baselineFindingFor(a)})
	sessions, _ := store.List(context.Background())
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	sid := sessions[0].ID

	// Still inside the 1h retention window: Sweep must not purge yet.
	clock = start.Add(30 * time.Minute)
	if err := coord.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got, err := store.Get(context.Background(), sid); err != nil || got.Status == capture.StatusPurged {
		t.Fatalf("session purged before its retention window elapsed (status=%v, err=%v)", got.Status, err)
	}

	// Past retention: Sweep purges it, an operator's disk is never filled by
	// a forgotten auto-capture session left running.
	clock = start.Add(2 * time.Hour)
	if err := coord.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	got, err := store.Get(context.Background(), sid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != capture.StatusPurged {
		t.Errorf("status after retention window elapsed = %v, want %v", got.Status, capture.StatusPurged)
	}
}

// TestAutoCaptureTracker_RestartSurvival answers T-4101's "what happens if
// the daemon restarts while an auto-capture is running" question the same
// way T-4014 answered it for tcmirror: an eager sweep on the next
// Coordinator's construction/startup purges the orphaned row rather than
// leaving it running forever. Modeled here by building a SECOND Coordinator
// over the SAME persisted store with no live-process handle for the session
// (exactly what a restart loses) and confirming Sweep purges it once its
// retention age has passed — no T-4101-specific restart handling exists,
// because none is needed: the session is indistinguishable from a manual
// one to capture.Coordinator.
func TestAutoCaptureTracker_RestartSurvival(t *testing.T) {
	store := newACFakeStore()
	auditor := &acFakeAuditor{}
	start := time.Unix(1_700_000_000, 0)
	clock := start
	nowFn := func() time.Time { return clock }
	root := t.TempDir()
	ceilings := capture.Caps{MaxDurationSec: 300, MaxBytes: 1 << 20, MaxPackets: 1000, RetentionHours: 1}

	coord := capture.New(capture.Config{
		Agent: capturemock.NewAgent(), Resolver: capture.RefResolver{},
		Store: store, Audit: auditor, LocalNode: func() string { return "pve1" },
		Root: root, Ceilings: ceilings, Now: nowFn,
	})

	a := newPortAnomaly("bridge:pve1:vmbr0", "tcp/6667")
	svc := testBaselineSvc(cacheOf(a))
	tr := newAutoCaptureTracker(svc, autoCaptureConfig{Enabled: true, MaxPerWindow: 3, Window: time.Hour}, testLogger())
	tr.now = nowFn
	tr.set(coord)

	tr.observe(context.Background(), []findings.Finding{baselineFindingFor(a)})
	sessions, _ := store.List(context.Background())
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session before the simulated restart, got %d", len(sessions))
	}
	sid := sessions[0].ID

	// Simulate the daemon restart: a brand-new Coordinator over the SAME
	// root/store, so its in-memory live-process map starts empty — the
	// orphaning a real restart causes.
	clock = start.Add(2 * time.Hour) // past the 1h retention ceiling
	restarted := capture.New(capture.Config{
		Agent: capturemock.NewAgent(), Resolver: capture.RefResolver{},
		Store: store, Audit: auditor, LocalNode: func() string { return "pve1" },
		Root: root, Ceilings: ceilings, Now: nowFn,
	})
	if err := restarted.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep after restart: %v", err)
	}

	got, err := store.Get(context.Background(), sid)
	if err != nil {
		t.Fatalf("Get after restart+sweep: %v", err)
	}
	if got.Status != capture.StatusPurged {
		t.Errorf("anomaly-armed session status after restart+sweep = %v, want %v (no orphan left behind)", got.Status, capture.StatusPurged)
	}
}
