// SPDX-License-Identifier: Apache-2.0

package mtuprobe_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/mtuprobe"
)

// TestProbeWireGuardLink_NoOpUntilCapabilityExists is AC4: the WireGuard
// probe seam exists and, while WireGuardCapability (always false today, no
// Phase 14 flag exists yet) reports false, always returns
// ErrWireGuardNotAvailable rather than attempting a probe.
func TestProbeWireGuardLink_NoOpUntilCapabilityExists(t *testing.T) {
	svc := mtuprobe.New(mtuprobe.Config{})

	mtu, err := svc.ProbeWireGuardLink(context.Background(), "wg:pve1:wg0")
	if !errors.Is(err, mtuprobe.ErrWireGuardNotAvailable) {
		t.Fatalf("err = %v, want ErrWireGuardNotAvailable", err)
	}
	if mtu != 0 {
		t.Fatalf("mtu = %d, want 0 on a not-available response", mtu)
	}
}

// TestWireGuardCapability_DefaultsFalse: today's build has no WireGuard
// capability flag at all — the default must be false so the seam stays
// dark until Phase 14's T-1401 wires a real check in.
func TestWireGuardCapability_DefaultsFalse(t *testing.T) {
	if mtuprobe.WireGuardCapability() {
		t.Fatal("WireGuardCapability defaults to true; must default to false until T-1401 lands")
	}
}

// TestProbeWireGuardLink_StillNoOpEvenIfCapabilityFlips: even if a caller
// (a future test, or T-1401's own in-progress wiring) flips
// WireGuardCapability true, ProbeWireGuardLink has no real implementation
// yet — it must still report ErrWireGuardNotAvailable, never fabricate a
// probe result.
func TestProbeWireGuardLink_StillNoOpEvenIfCapabilityFlips(t *testing.T) {
	orig := mtuprobe.WireGuardCapability
	mtuprobe.WireGuardCapability = func() bool { return true }
	defer func() { mtuprobe.WireGuardCapability = orig }()

	svc := mtuprobe.New(mtuprobe.Config{})
	_, err := svc.ProbeWireGuardLink(context.Background(), "wg:pve1:wg0")
	if !errors.Is(err, mtuprobe.ErrWireGuardNotAvailable) {
		t.Fatalf("err = %v, want ErrWireGuardNotAvailable even with the capability flag flipped true (no real implementation exists yet)", err)
	}
}
