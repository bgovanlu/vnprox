# `switchDriver`

The **one write-adjacent extension point** — every other extension point is
strictly read-only. See [plugin-development.md](../plugin-development.md)
for the SDK overview, the stage-only boundary, and the security section this
page does not repeat; read the "What the plugin must not do" section below
carefully, it is not the same shape as the other four pages.

This point reuses `internal/switchdrv.SwitchDriver` **verbatim** — the SDK
does not fork a parallel contract for it.

## Interface (`internal/switchdrv/driver.go`)

```go
// SwitchDriver is the transport-agnostic contract for reading and writing one
// physical switch's port configuration, scoped to exactly VLAN membership,
// port description, and LACP settings (see PortConfig) — no other port
// operation, and no full-config push, is expressible through it.
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
```

`PortConfig` carries exactly `LACP` (mode/rate), `Description`, `Tagged`
(trunk VIDs), and `Untagged` (access VID) — nothing else is expressible
through this type, by design. `Neighbor` carries `ChassisID`/`PortID`, the
LLDP identity the host re-verifies against before every write.

Minimum capability to attach this point: **`netWrite`** — the only extension
point that requires it, because it is the only one the change engine ever
calls *into* to perform a write.

## What the host guarantees

- **You are invoked BY the change engine during a bounded `switch.port`
  op — never the other way round.** There is no path by which your driver
  code initiates a write on its own schedule; every `SetPortConfig` call
  traces back to a human-reviewed changeset apply.
- **Behind a dark-by-default feature guard.** This extension point is
  disabled by default at the change-engine level regardless of whether a
  driver plugin is installed — an operator opts in explicitly.
- **A neighbor mismatch aborts the write before it reaches your driver.**
  The Gateway re-verifies the live LLDP neighbor on the target port against
  the one recorded when the port was scoped; a mismatch (the cable moved)
  is a hard abort (`switchdrv.ErrNeighborMismatch`) — your `SetPortConfig`
  is never called in that case.
- **`PortConfig` is snapshotted before every write** for rollback — your
  `PortConfig` read is part of the change engine's own pre-image capture,
  not just informational.

## What the plugin must not do

This is the point where "must not" is enforced by the op family's own
capability gate (`RequiredCap("switch.port.update") == netWrite`), not by
this interface being narrower than a write path — it genuinely is one, just
a very bounded one:

- **Touch exactly VLAN membership, port description, and LACP — nothing
  else on the port.** The interface has no method for anything broader
  (no full-config push, no other attribute), and `SetPortConfig`'s contract
  explicitly says "must apply exactly the three bounded attribute groups and
  touch nothing else."
- **Never write outside the `port` argument you're given.** No
  cross-port, cross-switch, or "also fix up this related port while I'm
  here" behavior — the change engine's op is scoped to one port, and so must
  your implementation be.
- **Never skip the neighbor check by caching a stale one.** Re-fetch
  `PortNeighbor` honestly on every call; the whole safety property depends
  on it reflecting what's live on the wire right now, not what it was last
  time you asked.
- **`Close` must actually release the connection.** A driver that leaks a
  connection per push accumulates a resource the change engine has no way to
  see or bound from outside your implementation.

## Minimal working example

From `internal/plugin/plugintest/samples.go` — the SDK's own fixture, a
deterministic in-memory driver used by the transport-parity conformance
suite:

```go
type SampleSwitchDriver struct {
	written map[string]switchdrv.PortConfig
}

func NewSampleSwitchDriver() *SampleSwitchDriver {
	return &SampleSwitchDriver{written: make(map[string]switchdrv.PortConfig)}
}

func (d *SampleSwitchDriver) PortConfig(_ context.Context, port string) (switchdrv.PortConfig, error) {
	if port != SamplePort {
		return switchdrv.PortConfig{}, fmt.Errorf("sample switch: unknown port %q", port)
	}
	return switchdrv.PortConfig{
		LACP:        switchdrv.LACPConfig{Mode: switchdrv.LACPActive, Rate: switchdrv.LACPRateFast},
		Description: SampleDesc,
		Tagged:      append([]int(nil), SampleTaggedVIDs...),
		Untagged:    SampleUntaggedVID,
	}, nil
}

func (d *SampleSwitchDriver) SetPortConfig(_ context.Context, port string, cfg switchdrv.PortConfig) error {
	d.written[port] = cfg
	return nil
}

func (d *SampleSwitchDriver) PortNeighbor(_ context.Context, port string) (switchdrv.Neighbor, error) {
	if port != SamplePort {
		return switchdrv.Neighbor{}, fmt.Errorf("sample switch: unknown port %q", port)
	}
	return switchdrv.Neighbor{ChassisID: SampleChassisID, PortID: port}, nil
}

func (d *SampleSwitchDriver) Close() error { return nil }
```

A real driver replaces the in-memory map with a call to the physical
switch's own management protocol (gNMI/OpenConfig, as `internal/switchdrv`'s
own shipped driver does — see `internal/switchdrv/openconfig.go`) or a
vendor-specific one. Wire it into a `plugin.Manifest` declaring
`plugin.ExtSwitchDriver` and `netWrite`, and a `plugin.Registration.SwitchDriver`
field.
