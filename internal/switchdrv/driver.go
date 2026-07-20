package switchdrv

import (
	"context"
	"errors"
)

// SwitchDriver is the transport-agnostic contract for reading and writing one
// physical switch's port configuration, scoped to exactly VLAN membership,
// port description, and LACP settings (see PortConfig) — no other port
// operation, and no full-config push, is expressible through it. An
// OpenConfig/gNMI implementation lives in openconfig.go; vendor drivers are a
// documented future extension point behind this same interface.
//
// A driver instance is bound to a single switch (its mgmt address + decrypted
// credentials), constructed per push and Closed after — the change engine
// never holds a long-lived connection to a switch it is not actively pushing to.
type SwitchDriver interface {
	// PortConfig reads port's current VLAN-membership/description/LACP
	// configuration — the pre-image the change engine snapshots before any
	// write, and the state rollback re-pushes.
	PortConfig(ctx context.Context, port string) (PortConfig, error)

	// SetPortConfig writes cfg to port. It must apply exactly the three
	// bounded attribute groups and touch nothing else on the port.
	SetPortConfig(ctx context.Context, port string, cfg PortConfig) error

	// PortNeighbor reads the switch's live LLDP-observed neighbor on port —
	// the identity the Gateway re-verifies immediately before every write
	// (protection against a cable having moved since the port was scoped).
	PortNeighbor(ctx context.Context, port string) (Neighbor, error)

	// Close releases the driver's connection to the switch.
	Close() error
}

// ErrNeighborMismatch is returned when the live LLDP neighbor on a target port
// no longer matches the PVE-node neighbor recorded when the port was scoped —
// a hard abort before any write reaches the switch (T-1205 AC4). It is never a
// warning and no op can bypass it.
var ErrNeighborMismatch = errors.New("switchdrv: live LLDP neighbor does not match the port's scoped PVE-node neighbor; aborting push (cable may have moved)")

// ErrTransportUnavailable is returned by a driver whose real wire transport
// (a live gNMI session against physical hardware) is not implemented/available
// — see OpenConfigDriver. Development and tests use internal/switchmock
// instead; the real transport is a needs-hardware-validation item.
var ErrTransportUnavailable = errors.New("switchdrv: gNMI transport not available (needs hardware validation; use internal/switchmock for development)")
