package mtuprobe

import (
	"context"
	"errors"
)

// WireGuardCapability reports whether this build/config has the WireGuard
// link MTU probing capability Phase 14's T-1401 introduces. Always false
// today — there is no WireGuard tunnel entity kind in internal/inventory
// yet, and no capability flag to check — so ProbeWireGuardLink stays dark
// (never attempts a probe) regardless of caller. A var, not a const, so
// T-1401's own wiring can replace it with a real capability check without
// touching this package's exported surface.
var WireGuardCapability = func() bool { return false }

// ErrWireGuardNotAvailable is ProbeWireGuardLink's fixed response until
// T-1401 lands a real WireGuard capability and tunnel model. It never wraps
// a transport error — today's answer is always "this feature doesn't exist
// yet", not "we tried and failed."
var ErrWireGuardNotAvailable = errors.New("mtuprobe: WireGuard link MTU probing is not yet available (Phase 14, T-1401)")

// ProbeWireGuardLink is the declared, capability-gated seam this task's
// card asks for and Phase 14's T-1401 should wire a real implementation
// into (see planning/reports/T-1306.md for the exact call site and the
// integration this method should grow: reading the tunnel's Ref from
// internal/inventory once WireGuard tunnels are modeled there, and probing
// its own endpoint pair the same way ProbeMTU probes a latmesh.Pair today).
// tunnelRef names the WireGuard tunnel's inventory Ref-to-be. Always
// returns ErrWireGuardNotAvailable while WireGuardCapability reports false
// — the seam exists and is exercised by a test (AC4), but never attempts a
// probe, per this task's card: "declared and no-op'd ... until a WireGuard
// capability flag exists — wiring deferred ... not implemented here."
func (s *Service) ProbeWireGuardLink(ctx context.Context, tunnelRef string) (mtu int, err error) {
	if !WireGuardCapability() {
		return 0, ErrWireGuardNotAvailable
	}
	// Capability flag exists but no real probe implementation has landed
	// yet either way — still not implemented, same answer either branch
	// takes today.
	return 0, ErrWireGuardNotAvailable
}
