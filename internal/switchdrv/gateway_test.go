// SPDX-License-Identifier: Apache-2.0

package switchdrv_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/switchdrv"
	"github.com/bgovanlu/vnprox/internal/switchmock"
)

func portRef(switchID, port string) string {
	return inventory.Ref{Kind: inventory.KindSwitchPort, ID: switchID + "/" + port}.String()
}

func newGateway(sw *switchmock.Switch) *switchdrv.Gateway {
	return switchdrv.NewGateway(func(_ context.Context, switchID string) (switchdrv.SwitchDriver, error) {
		if switchID != "sw1" {
			return nil, errors.New("no such switch")
		}
		return sw, nil
	})
}

func switchOp(switchID, port string, want switchdrv.Neighbor, p *change.SwitchPortUpdateParams) change.Op {
	p.ExpectNeighbor = change.SwitchNeighbor{ChassisID: want.ChassisID, PortID: want.PortID}
	return change.Op{
		Type:   change.OpSwitchPortUpdate,
		Target: inventory.Ref{Kind: inventory.KindSwitchPort, ID: switchID + "/" + port},
		Params: p,
	}
}

// TestGateway_NeighborMismatchAborts covers T-1205 AC4: when the live LLDP
// neighbor on a target port no longer matches the PVE-node neighbor the op was
// scoped against (a cable has moved), the pre-write identity check aborts the
// push and ZERO writes reach the switch.
func TestGateway_NeighborMismatchAborts(t *testing.T) {
	sw := switchmock.New()
	sw.SetPort("Ethernet1", switchdrv.PortConfig{Untagged: 100, Tagged: []int{10}})
	// The switch now sees a DIFFERENT neighbor than the op expects.
	sw.SetNeighbor("Ethernet1", switchdrv.Neighbor{ChassisID: "moved:cable", PortID: "eno1"})

	gw := newGateway(sw)
	op := switchOp("sw1", "Ethernet1",
		switchdrv.Neighbor{ChassisID: "pve1:aa:bb", PortID: "eno1"}, // the scoped, expected neighbor
		&change.SwitchPortUpdateParams{Tagged: &[]int{10, 20}})

	err := gw.ApplySwitchOp(context.Background(), op)
	if !errors.Is(err, switchdrv.ErrNeighborMismatch) {
		t.Fatalf("ApplySwitchOp: want ErrNeighborMismatch, got %v", err)
	}
	if writes := sw.Writes(); len(writes) != 0 {
		t.Fatalf("expected zero writes to reach the switch after identity abort, got %d: %+v", len(writes), writes)
	}
}

// TestGateway_ApplyAndRollback covers the happy-path apply (identity matches →
// additive VLAN write reaches the switch) plus snapshot/restore round trip (the
// rollback pre-image mechanism T-1205 AC6 relies on).
func TestGateway_ApplyAndRollback(t *testing.T) {
	sw := switchmock.New()
	pre := switchdrv.PortConfig{Untagged: 100, Tagged: []int{10}}
	sw.SetPort("Ethernet1", pre)
	sw.SetNeighbor("Ethernet1", switchdrv.Neighbor{ChassisID: "pve1:aa:bb", PortID: "eno1"})
	gw := newGateway(sw)
	ctx := context.Background()
	ref := portRef("sw1", "Ethernet1")

	// Snapshot the pre-image first (what the change engine captures pre-apply).
	snap, err := gw.SnapshotSwitchPort(ctx, ref)
	if err != nil {
		t.Fatalf("SnapshotSwitchPort: %v", err)
	}

	// Apply an additive VLAN change (identity matches).
	op := switchOp("sw1", "Ethernet1",
		switchdrv.Neighbor{ChassisID: "pve1:aa:bb", PortID: "eno1"},
		&change.SwitchPortUpdateParams{Tagged: &[]int{10, 20}})
	if err := gw.ApplySwitchOp(ctx, op); err != nil {
		t.Fatalf("ApplySwitchOp: %v", err)
	}
	if got := sw.CurrentPort("Ethernet1"); got.Untagged != 100 || len(got.Tagged) != 2 {
		t.Fatalf("after apply: want VLAN 20 added (untagged 100, 2 tagged), got %+v", got)
	}

	// Restore the pre-image (rollback) — the port returns exactly to pre.
	if err := gw.RestoreSwitchPort(ctx, ref, snap); err != nil {
		t.Fatalf("RestoreSwitchPort: %v", err)
	}
	got := sw.CurrentPort("Ethernet1")
	if got.Untagged != pre.Untagged || len(got.Tagged) != len(pre.Tagged) || got.Tagged[0] != pre.Tagged[0] {
		t.Fatalf("after restore: want pre-image %+v, got %+v", pre, got)
	}
}

// TestGateway_RestoreFailsWhenUnreachable proves a switch that has dropped off
// the network surfaces the restore failure (feeding T-1205 AC6's distinguishable
// "rollback incomplete" outcome).
func TestGateway_RestoreFailsWhenUnreachable(t *testing.T) {
	sw := switchmock.New()
	sw.SetPort("Ethernet1", switchdrv.PortConfig{Untagged: 100})
	sw.SetNeighbor("Ethernet1", switchdrv.Neighbor{ChassisID: "pve1:aa:bb", PortID: "eno1"})
	gw := newGateway(sw)
	ctx := context.Background()
	ref := portRef("sw1", "Ethernet1")

	snap, err := gw.SnapshotSwitchPort(ctx, ref)
	if err != nil {
		t.Fatalf("SnapshotSwitchPort: %v", err)
	}
	sw.SetUnreachable(true)
	if err := gw.RestoreSwitchPort(ctx, ref, snap); err == nil {
		t.Fatal("RestoreSwitchPort on an unreachable switch: want error, got nil")
	}
}
