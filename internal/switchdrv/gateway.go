package switchdrv

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// DriverFactory opens a SwitchDriver bound to the switch identified by
// switchID (its mgmt address + decrypted credentials, resolved from the
// switches app-store table). cmd/vnproxd supplies the production factory;
// tests supply one returning an internal/switchmock double.
type DriverFactory func(ctx context.Context, switchID string) (SwitchDriver, error)

// Gateway is the daemon-level seam the change engine drives for switch.port.*
// ops (change.SwitchGateway). Like the interfaces-file NodeAgent — and unlike
// the ticket-scoped PVEGateway — it is daemon-level and needs no live user
// session, so its rollback (RestoreSwitchPort) works on the unattended
// commit-confirm-timeout path too (T-1205 AC6).
//
// Its defining safety property: ApplySwitchOp re-reads the target port's live
// LLDP neighbor and hard-aborts before any write if it no longer matches the
// PVE-node neighbor the op was scoped against (T-1205 AC4). This check is
// unconditional — no op field, config flag, or param can bypass it.
type Gateway struct {
	factory DriverFactory
}

// NewGateway constructs a Gateway over factory.
func NewGateway(factory DriverFactory) *Gateway { return &Gateway{factory: factory} }

// parsePortRef splits a switch-port Ref ("switch-port::<switchID>/<port>")
// into its switch id and driver-native port name. The port name may itself
// contain '/' (chassis switches), so only the first '/' is structural.
func parsePortRef(portRef string) (switchID, port string, err error) {
	ref, perr := inventory.ParseRef(portRef)
	if perr != nil {
		return "", "", fmt.Errorf("switchdrv: parsing switch-port ref %q: %w", portRef, perr)
	}
	if ref.Kind != inventory.KindSwitchPort {
		return "", "", fmt.Errorf("switchdrv: ref %q is not a switch-port ref", portRef)
	}
	i := strings.IndexByte(ref.ID, '/')
	if i <= 0 || i == len(ref.ID)-1 {
		return "", "", fmt.Errorf("switchdrv: switch-port ref %q has no <switchID>/<port> id", portRef)
	}
	return ref.ID[:i], ref.ID[i+1:], nil
}

// ApplySwitchOp applies one switch.port.update op: it opens the switch's
// driver, re-verifies the port's live LLDP neighbor against the op's recorded
// PVE-node neighbor (aborting hard on mismatch — no write reaches the switch),
// then overlays the op's set fields onto the port's current config and writes
// the result. change.SwitchGateway.
func (g *Gateway) ApplySwitchOp(ctx context.Context, op change.Op) error {
	switchID, port, err := parsePortRef(op.Target.String())
	if err != nil {
		return err
	}
	params, ok := op.Params.(*change.SwitchPortUpdateParams)
	if !ok {
		return fmt.Errorf("switchdrv: op %s has unexpected params type %T", op.Type, op.Params)
	}
	drv, err := g.factory(ctx, switchID)
	if err != nil {
		return fmt.Errorf("switchdrv: opening driver for switch %s: %w", switchID, err)
	}
	defer func() { _ = drv.Close() }()

	// Mandatory pre-write identity re-check (T-1205 AC4): the cable must still
	// face the same PVE node it was scoped against. This runs before any read
	// of desired state and before any write.
	live, err := drv.PortNeighbor(ctx, port)
	if err != nil {
		return fmt.Errorf("switchdrv: reading live neighbor on %s port %s: %w", switchID, port, err)
	}
	want := Neighbor{ChassisID: params.ExpectNeighbor.ChassisID, PortID: params.ExpectNeighbor.PortID}
	if !live.Matches(want) {
		return fmt.Errorf("%w: switch %s port %s now sees %+v, expected %+v", ErrNeighborMismatch, switchID, port, live, want)
	}

	base, err := drv.PortConfig(ctx, port)
	if err != nil {
		return fmt.Errorf("switchdrv: reading current config on %s port %s: %w", switchID, port, err)
	}
	next := applyParams(base, params)
	if err := drv.SetPortConfig(ctx, port, next); err != nil {
		return fmt.Errorf("switchdrv: writing config to %s port %s: %w", switchID, port, err)
	}
	return nil
}

// SnapshotSwitchPort reads and JSON-encodes portRef's current PortConfig — the
// pre-image the change engine stores and rollback re-pushes.
// change.SwitchGateway.
func (g *Gateway) SnapshotSwitchPort(ctx context.Context, portRef string) (string, error) {
	switchID, port, err := parsePortRef(portRef)
	if err != nil {
		return "", err
	}
	drv, err := g.factory(ctx, switchID)
	if err != nil {
		return "", fmt.Errorf("switchdrv: opening driver for switch %s: %w", switchID, err)
	}
	defer func() { _ = drv.Close() }()
	cfg, err := drv.PortConfig(ctx, port)
	if err != nil {
		return "", fmt.Errorf("switchdrv: snapshotting %s port %s: %w", switchID, port, err)
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("switchdrv: encoding snapshot for %s port %s: %w", switchID, port, err)
	}
	return string(b), nil
}

// RestoreSwitchPort re-pushes a pre-image captured by SnapshotSwitchPort onto
// portRef. It deliberately does NOT re-run the neighbor identity check:
// restoring the exact pre-apply configuration is the safe, intended action,
// and gating it on a live neighbor read would block recovery precisely when a
// port is in a disturbed state. If the switch is unreachable at this moment the
// factory/write fails and the caller records a distinguishable "rollback
// incomplete" outcome (T-1205 AC6) rather than a clean rolled_back.
// change.SwitchGateway.
func (g *Gateway) RestoreSwitchPort(ctx context.Context, portRef, snapshot string) error {
	switchID, port, err := parsePortRef(portRef)
	if err != nil {
		return err
	}
	var cfg PortConfig
	if err := json.Unmarshal([]byte(snapshot), &cfg); err != nil {
		return fmt.Errorf("switchdrv: decoding snapshot for %s port %s: %w", switchID, port, err)
	}
	drv, err := g.factory(ctx, switchID)
	if err != nil {
		return fmt.Errorf("switchdrv: opening driver for switch %s: %w", switchID, err)
	}
	defer func() { _ = drv.Close() }()
	if err := drv.SetPortConfig(ctx, port, cfg); err != nil {
		return fmt.Errorf("switchdrv: restoring %s port %s from pre-image: %w", switchID, port, err)
	}
	return nil
}

// applyParams overlays a switch.port.update op's set (non-nil) fields onto base
// — the "update" net effect. Unset fields leave base unchanged.
func applyParams(base PortConfig, p *change.SwitchPortUpdateParams) PortConfig {
	out := base
	out.Tagged = append([]int(nil), base.Tagged...)
	if p.Untagged != nil {
		out.Untagged = *p.Untagged
	}
	if p.Tagged != nil {
		t := append([]int(nil), (*p.Tagged)...)
		sort.Ints(t)
		out.Tagged = t
	}
	if p.Description != nil {
		out.Description = *p.Description
	}
	if p.LacpMode != nil {
		out.LACP.Mode = LACPMode(*p.LacpMode)
	}
	if p.LacpRate != nil {
		out.LACP.Rate = LACPRate(*p.LacpRate)
	}
	return out
}
