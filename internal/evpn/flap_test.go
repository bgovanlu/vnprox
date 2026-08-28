// SPDX-License-Identifier: Apache-2.0

package evpn

import (
	"testing"
	"time"
)

// TestFlapTracker_ScriptedOscillation_RaisesFinding is T-404 AC3's core
// assertion: a session whose state is scripted to oscillate (Established
// -> Idle -> Established -> Idle -> Established, five observations well
// inside one window) must cross the flap threshold; a stable session
// observed the same number of times must not.
func TestFlapTracker_ScriptedOscillation_RaisesFinding(t *testing.T) {
	tr := newFlapTracker(10*time.Minute, 3)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	states := []string{"Established", "Idle", "Established", "Idle", "Established"}
	var last int
	for i, s := range states {
		last = tr.observe("pve1", "10.20.0.12", base.Add(time.Duration(i)*time.Minute), s)
	}
	// 5 samples, 4 adjacent transitions (each consecutive pair differs).
	if last != 4 {
		t.Fatalf("transitions = %d, want 4", last)
	}
	if !tr.flapping(last) {
		t.Errorf("flapping(%d) = false, want true (threshold %d)", last, tr.threshold)
	}
}

func TestFlapTracker_StableSession_NoFinding(t *testing.T) {
	tr := newFlapTracker(10*time.Minute, 3)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	var last int
	for i := 0; i < 5; i++ {
		last = tr.observe("pve1", "10.20.0.12", base.Add(time.Duration(i)*time.Minute), "Established")
	}
	if last != 0 {
		t.Fatalf("transitions = %d, want 0 for a stable session", last)
	}
	if tr.flapping(last) {
		t.Errorf("flapping(%d) = true, want false for a stable session", last)
	}
}

// TestFlapTracker_BelowThreshold_NoFinding checks the threshold boundary:
// exactly 2 transitions with a threshold of 3 must not flap.
func TestFlapTracker_BelowThreshold_NoFinding(t *testing.T) {
	tr := newFlapTracker(10*time.Minute, 3)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	states := []string{"Established", "Idle", "Established"} // 2 transitions
	var last int
	for i, s := range states {
		last = tr.observe("pve1", "10.20.0.12", base.Add(time.Duration(i)*time.Minute), s)
	}
	if last != 2 {
		t.Fatalf("transitions = %d, want 2", last)
	}
	if tr.flapping(last) {
		t.Error("flapping should be false at exactly threshold-1 transitions")
	}
}

// TestFlapTracker_OldTransitionsAgeOut verifies the trailing-window
// behavior: oscillation that happened long before the window, followed by
// a long stable period, must not still report as flapping once the old
// transitions have aged out.
func TestFlapTracker_OldTransitionsAgeOut(t *testing.T) {
	tr := newFlapTracker(10*time.Minute, 3)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Oscillate rapidly in the first minute (4 transitions).
	tr.observe("pve1", "10.20.0.12", base, "Established")
	tr.observe("pve1", "10.20.0.12", base.Add(10*time.Second), "Idle")
	tr.observe("pve1", "10.20.0.12", base.Add(20*time.Second), "Established")
	tr.observe("pve1", "10.20.0.12", base.Add(30*time.Second), "Idle")
	last := tr.observe("pve1", "10.20.0.12", base.Add(40*time.Second), "Established")
	if !tr.flapping(last) {
		t.Fatalf("expected flapping immediately after oscillation, got %d transitions", last)
	}

	// Now stay Established, well past the 10-minute window.
	last = tr.observe("pve1", "10.20.0.12", base.Add(30*time.Minute), "Established")
	if tr.flapping(last) {
		t.Errorf("expected old transitions to have aged out of the window, got %d transitions", last)
	}
}

// TestFlapTracker_DistinctSessionsIndependent verifies per-(node,peerAddr)
// isolation: a flapping session on one peer must not affect another.
func TestFlapTracker_DistinctSessionsIndependent(t *testing.T) {
	tr := newFlapTracker(10*time.Minute, 3)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// pve1<->10.20.0.12 flaps.
	tr.observe("pve1", "10.20.0.12", base, "Established")
	tr.observe("pve1", "10.20.0.12", base.Add(time.Minute), "Idle")
	tr.observe("pve1", "10.20.0.12", base.Add(2*time.Minute), "Established")
	last12 := tr.observe("pve1", "10.20.0.12", base.Add(3*time.Minute), "Idle")

	// pve1<->10.20.0.13 stays stable the whole time.
	tr.observe("pve1", "10.20.0.13", base, "Established")
	tr.observe("pve1", "10.20.0.13", base.Add(time.Minute), "Established")
	tr.observe("pve1", "10.20.0.13", base.Add(2*time.Minute), "Established")
	last13 := tr.observe("pve1", "10.20.0.13", base.Add(3*time.Minute), "Established")

	if !tr.flapping(last12) {
		t.Errorf("expected pve1<->10.20.0.12 to be flapping, got %d transitions", last12)
	}
	if tr.flapping(last13) {
		t.Errorf("expected pve1<->10.20.0.13 to be stable, got %d transitions", last13)
	}
}

func TestFlapTracker_Forget(t *testing.T) {
	tr := newFlapTracker(10*time.Minute, 3)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tr.observe("pve1", "10.20.0.12", base, "Established")
	tr.observe("pve1", "10.20.0.12", base.Add(time.Minute), "Idle")
	tr.forget("pve1", "10.20.0.12")
	if hist := tr.history[flapKey{"pve1", "10.20.0.12"}]; len(hist) != 0 {
		t.Errorf("expected history cleared after forget, got %d samples", len(hist))
	}
}

func TestNewFlapTracker_Defaults(t *testing.T) {
	tr := newFlapTracker(0, 0)
	if tr.window != DefaultFlapWindow {
		t.Errorf("window = %v, want default %v", tr.window, DefaultFlapWindow)
	}
	if tr.threshold != DefaultFlapThreshold {
		t.Errorf("threshold = %d, want default %d", tr.threshold, DefaultFlapThreshold)
	}
}
