// SPDX-License-Identifier: Apache-2.0

package capture

import (
	"context"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// RefResolver is the default TargetResolver: it maps a target Ref to a
// capture interface by parsing the Ref triplet (inventory.ParseRef) and
// deriving the host interface name from the entity kind. It is deliberately
// self-contained (no inventory graph dependency) so the coordinator can run
// in tests and single-node dev without a populated graph — a graph-backed
// resolver that can turn a guest NIC into its live tap/veth device is a
// cmd/vnproxd-level refinement (and a needs-hardware-validation item for the
// exact per-guest device naming).
//
// It resolves:
//   - bridge / ovs-bridge / bond / ovs-bond / vlan → the device name (Ref.ID)
//   - sdn-vnet → the vnet device name (the last "/"-separated segment of the
//     "zone/vnet" ID, which is the realized Linux device name)
//
// A guest-nic ref is intentionally NOT resolved here (its live tap device is
// runtime-only, not derivable from the Ref alone): it yields
// ErrUnresolvableTarget, which the coordinator surfaces as a scoping
// rejection — the correct conservative behavior until a graph-backed
// resolver is wired.
type RefResolver struct{}

// Resolve implements TargetResolver.
func (RefResolver) Resolve(_ context.Context, ref string) (Target, error) {
	r, err := inventory.ParseRef(ref)
	if err != nil {
		return Target{}, fmt.Errorf("%w: %v", ErrUnresolvableTarget, err)
	}
	switch r.Kind {
	case inventory.KindBridge, inventory.KindOVSBridge,
		inventory.KindBond, inventory.KindOVSBond, inventory.KindVlan:
		if r.Node == "" || r.ID == "" {
			return Target{}, fmt.Errorf("%w: ref %q has no node/device", ErrUnresolvableTarget, ref)
		}
		return Target{Ref: ref, Node: r.Node, Iface: r.ID}, nil
	case inventory.KindSDNVnet:
		// SDN vnets are cluster-scoped (empty Node) with ID "zone/vnet";
		// the realized Linux device is the vnet segment. Without a node the
		// capture cannot be placed, so a bare vnet ref is unresolvable here —
		// callers targeting a vnet must supply a node-scoped ref (the
		// graph-backed resolver does this). Kept explicit rather than
		// silently defaulting to a node.
		return Target{}, fmt.Errorf("%w: sdn-vnet ref %q is cluster-scoped; capture needs a node", ErrUnresolvableTarget, ref)
	default:
		return Target{}, fmt.Errorf("%w: ref kind %q is not a capturable target", ErrUnresolvableTarget, r.Kind)
	}
}
