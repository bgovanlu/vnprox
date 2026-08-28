// SPDX-License-Identifier: Apache-2.0

package main

// autocapture.go is the composition root's half of T-4101: arming a
// bounded internal/capture session when a baseline anomaly's finding
// (source "baseline", internal/findings/adapt_baseline.go) first appears in
// the unified findings stream, instead of an operator manually starting a
// capture after the fact.
//
// WHY HOOKED OFF THE FINDINGS STREAM, NOT A PARALLEL DETECTOR. An anomaly is
// already a finding: adapt_baseline.go's checkBaselineAnomalies debounces
// every internal/baseline.Anomaly through the standard 2-cycle-each-way
// hysteresis every other continuously-recomputed producer uses before it
// ever becomes a Finding. Re-running internal/baseline.Detect here and
// reacting to raw Anomalies would both duplicate that debounce and fire a
// second time for the exact same deviation findings.go already reported —
// two divergent "is this anomaly real" answers from one input. Instead this
// file is a THIRD leg of findings.Config.OnCycle (findingsGuardVal and
// siemFindingsTrackerVal, both wired in server.go, are the other two): it
// diffs the stream cycle-over-cycle for newly-appeared source="baseline"
// findings — reusing the exact same "first-seen" idea findingsGuard already
// established for canary evidence — and looks the underlying Anomaly back up
// via baselineService.anomalyForFinding (cmd/vnproxd/baseline.go) to recover
// the Ref/Class/Subject a bare Finding doesn't carry.
//
// PURE WIRING, NOT A NEW CAP PATH. capture.Coordinator already owns every
// cap that matters for the capture ITSELF (duration/bytes/packets,
// server-enforced and un-overridable — internal/capture/doc.go) and its own
// audit/retention/restart-orphan-purge machinery. This file's
// capture.StartRequest never sets DurationSec/MaxBytes/MaxPackets, so
// clampCaps applies the exact same ceiling a manual capture gets — see
// internal/config's BaselineConfig doc comment for why that is deliberate
// (T-4101 AC2). The one genuinely NEW cap this file owns is the storm cap
// (autoCaptureConfig.MaxPerWindow/Window): a bound on how many anomaly-armed
// captures may START within a sliding window, independent of any single
// capture's own duration/size.
//
// AUDIT TRAIL. StartRequest.StartedBy is set to
// "system:baseline:<findingID>" — capture.Coordinator's own audit() writes
// this verbatim as the capture.start audit row's actor (agent.go's
// AuditEvent.Actor) AND as the persisted Session.StartedBy an operator sees
// on the capture itself, so "why does this pcap exist" is answerable two
// ways (the audit log, or the capture list) without internal/capture having
// to know anything about baseline anomalies — no change to that package at
// all, matching the "pure wiring" framing above.
//
// RESTART SURVIVAL. An anomaly-armed session is an ordinary capture
// Coordinator session once Start returns — indistinguishable, in
// capture_sessions, from one an operator started by hand. It inherits
// Coordinator.RunSweepLoop's existing restart handling unmodified: primed
// immediately on daemon startup (RunSweepLoop's doc comment), Sweep purges
// any row still marked "running" whose StartedAt has passed its retention
// age even though the live process behind it is long gone (coordinator.go's
// "orphaned by a daemon restart mid-capture" comment) — the SAME mechanism
// T-4014's tcmirror_expiry.go independently arrived at for its own session
// kind. This file adds no second orphan sweep. What IS specific to this
// file is in-memory only and does NOT survive a restart: the "which finding
// IDs have I already seen" set and the storm-window counter both reset to
// empty. A restart can therefore make every still-firing baseline anomaly
// look "new" again for one cycle — but the storm cap (on by construction,
// independent of the enable gate's history) bounds the fallout to at most
// MaxPerWindow captures, the same bound a genuine anomaly burst gets, not an
// unbounded re-arm-everything sweep.
import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/capture"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/findings"
)

// autoCaptureActor prefixes every anomaly-armed session's StartedBy, mirroring
// internal/change's systemScheduleActor/systemRollbackActor/systemTcMirrorActor
// "system:<subsystem>" convention — with the triggering finding id appended
// (":<findingID>") so the actor string alone answers "why does this pcap
// exist" without a second lookup.
const autoCaptureActor = "system:baseline"

// autoCaptureConfig gates and bounds T-4101's anomaly-triggered capture.
// The zero value is Enabled=false — inert, matching internal/config's
// BaselineConfig off-by-default field.
type autoCaptureConfig struct {
	MaxPerWindow int
	Window       time.Duration
	Enabled      bool
}

func newAutoCaptureConfig(cfg config.BaselineConfig) autoCaptureConfig {
	return autoCaptureConfig{
		Enabled:      cfg.AutoCaptureEnabled,
		MaxPerWindow: cfg.AutoCaptureMaxPerWindow,
		Window:       time.Duration(cfg.AutoCaptureWindowMinutes) * time.Minute,
	}
}

// autoCaptureTracker is findings.Config.OnCycle's baseline-anomaly-triggered
// capture consumer. Safe for concurrent use (OnCycle calls are serialized by
// findings.Engine's own run loop — see engine.go — so the mutex here is
// belt-and-braces, the same posture siemFindingsTracker's doc comment takes).
// Field order is densest-pointer-first (docs/development.md's Go standards):
// pointers/funcs/maps (one word each), then the slice, then the two
// pointer-free-tail structs — never re-run fieldalignment -fix on this.
type autoCaptureTracker struct {
	// coord is late-bound: this tracker is constructed before captureCoord
	// (server.go builds it ahead of setupFindings so it can be one of
	// findingsOnCycle's fan-out legs, the same two-step wiring
	// fedTunnelAdapter/peerTrustAdapterVal use), and set once immediately
	// after captureCoord exists.
	coord   *capture.Coordinator
	baseSvc *baselineService
	log     *slog.Logger
	now     func() time.Time
	prev    map[string]bool
	fired   []int64 // unix-second start times within the storm window
	cfg     autoCaptureConfig
	mu      sync.Mutex
}

func newAutoCaptureTracker(baseSvc *baselineService, cfg autoCaptureConfig, log *slog.Logger) *autoCaptureTracker {
	if log == nil {
		log = slog.Default()
	}
	if cfg.MaxPerWindow <= 0 {
		cfg.MaxPerWindow = config.DefaultBaselineAutoCaptureMaxPerWindow
	}
	if cfg.Window <= 0 {
		cfg.Window = time.Duration(config.DefaultBaselineAutoCaptureWindowMinutes) * time.Minute
	}
	return &autoCaptureTracker{
		baseSvc: baseSvc,
		cfg:     cfg,
		log:     log,
		now:     time.Now,
		prev:    map[string]bool{},
	}
}

// set late-binds the capture coordinator. Called once, right after it is
// built in server.go.
func (t *autoCaptureTracker) set(coord *capture.Coordinator) {
	t.mu.Lock()
	t.coord = coord
	t.mu.Unlock()
}

// observe is registered as (one fan-out leg of) findings.Config.OnCycle. A
// nil tracker, or the feature gate off, costs nothing beyond the two checks
// — no capture is ever attempted, matching T-4101 AC1 (off by default).
func (t *autoCaptureTracker) observe(ctx context.Context, fs []findings.Finding) {
	if t == nil || !t.cfg.Enabled {
		return
	}

	t.mu.Lock()
	prev := t.prev
	t.mu.Unlock()

	cur := make(map[string]bool, len(fs))
	var newlyAppeared []findings.Finding
	for _, f := range fs {
		cur[f.ID] = true
		if f.Source == findings.SourceBaseline && !prev[f.ID] {
			newlyAppeared = append(newlyAppeared, f)
		}
	}

	t.mu.Lock()
	t.prev = cur
	t.mu.Unlock()

	for _, f := range newlyAppeared {
		t.arm(ctx, f)
	}
}

// arm starts one bounded capture scoped to the anomaly behind finding f, if
// the storm cap has budget left this window. Errors (unresolvable target,
// coordinator failure) are logged, never fatal to the findings cycle — the
// same "log and continue" degradation Coordinator.Start itself uses for a
// per-target failure.
func (t *autoCaptureTracker) arm(ctx context.Context, f findings.Finding) {
	if t.baseSvc == nil {
		return
	}
	anomaly, ok := t.baseSvc.anomalyForFinding(f.ID)
	if !ok {
		t.log.Warn("autocapture: no cached anomaly for newly-appeared baseline finding, skipping",
			"finding", f.ID)
		return
	}

	t.mu.Lock()
	coord := t.coord
	t.mu.Unlock()
	if coord == nil {
		t.log.Warn("autocapture: capture coordinator not yet wired, skipping", "finding", f.ID)
		return
	}

	if !t.budgetAvailable() {
		t.log.Warn("autocapture: storm cap reached, suppressing anomaly-triggered capture",
			"finding", f.ID, "ref", anomaly.Ref, "maxPerWindow", t.cfg.MaxPerWindow, "window", t.cfg.Window)
		return
	}

	req := capture.StartRequest{
		TargetRef: anomaly.Ref,
		Filter:    anomaly.CaptureFilter(),
		StartedBy: autoCaptureActor + ":" + f.ID,
		// DurationSec/MaxBytes/MaxPackets deliberately left zero: clampCaps
		// treats zero as "use the configured ceiling", the exact ceiling a
		// manual capture gets — no separate cap arithmetic here (T-4101 AC2).
	}
	grp, err := coord.Start(ctx, req)
	if err != nil {
		// Budget is deliberately NOT consumed on a failed Start (e.g. a
		// guest-nic Ref, which capture.RefResolver cannot scope to a
		// capture interface yet — an existing, pre-T-4101 limitation of
		// internal/capture, not something worked around here): a request
		// that started nothing should not eat into the window a genuine
		// anomaly burst needs.
		t.log.Error("autocapture: starting anomaly-triggered capture",
			"finding", f.ID, "ref", anomaly.Ref, "class", anomaly.Class, "error", err)
		return
	}
	t.recordStart()
	t.log.Info("autocapture: armed capture for baseline anomaly",
		"finding", f.ID, "ref", anomaly.Ref, "class", anomaly.Class, "group", grp.ID)
}

// budgetAvailable reports whether the storm cap has room for one more start
// this window, pruning stale entries first. It does not itself reserve a
// slot — recordStart does that, only once a capture has actually started
// (see arm's doc comment on why a failed Start must not consume budget).
func (t *autoCaptureTracker) budgetAvailable() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneFiredLocked()
	return len(t.fired) < t.cfg.MaxPerWindow
}

// recordStart reserves one storm-cap slot for a capture that just started.
func (t *autoCaptureTracker) recordStart() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneFiredLocked()
	t.fired = append(t.fired, t.now().Unix())
}

// pruneFiredLocked drops fired entries older than the trailing cfg.Window.
// Caller must hold t.mu.
func (t *autoCaptureTracker) pruneFiredLocked() {
	cutoff := t.now().Unix() - int64(t.cfg.Window/time.Second)
	n := 0
	for _, ts := range t.fired {
		if ts >= cutoff {
			t.fired[n] = ts
			n++
		}
	}
	t.fired = t.fired[:n]
}
