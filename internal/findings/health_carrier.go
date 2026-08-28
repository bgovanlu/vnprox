// SPDX-License-Identifier: Apache-2.0

// health_carrier.go implements docs/features/monitoring.md §5's "bridge
// with no carrier uplink" check: a bridge that declares one or more uplink
// ports (directly, or through an enslaving bond) but every one of them
// currently reports no carrier (inventory.PhysNic.LinkUp == false) — the
// bridge is up but has no path off the box. A bridge with zero configured
// ports (a pure NAT/internal bridge) is never flagged: it was never meant
// to have an uplink. Detection only: bringing a link back up is a physical
// action (recable, enable a switch port), not a changeset op.

package findings

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

const CheckBridgeNoCarrier = "bridge_no_carrier"

const bridgeNoCarrierDocsLink = "docs/features/monitoring.md#5-health-checks"

const (
	bridgeCarrierRiseCycles = 2
	bridgeCarrierFallCycles = 2
)

// checkBridgeNoCarrier evaluates every Bridge's resolved ports (the same
// bridge->[bond]->physnic traversal internal/drift's mtu.go uses) and
// returns one finding per bridge whose every resolved physical uplink is
// reporting no carrier.
func checkBridgeNoCarrier(snap inventory.Snapshot, db *debouncer) []Finding {
	var out []Finding
	live := map[string]bool{}

	for _, e := range snap.All() {
		br, ok := e.(*inventory.Bridge)
		if !ok {
			continue
		}
		nics := resolvePhysNics(snap, br.Ports)
		if len(nics) == 0 {
			continue // no configured uplink at all: nothing to flag
		}

		anyUp, allReported := false, true
		var nicRefs []string
		for _, n := range nics {
			nicRefs = append(nicRefs, n.GetRef().String())
			if !n.LinkUpSet {
				allReported = false
				continue
			}
			if n.LinkUp {
				anyUp = true
			}
		}
		if !allReported {
			continue // haven't observed carrier state for every uplink yet
		}

		key := br.GetRef().String()
		live[key] = true
		down := !anyUp
		active := db.Evaluate(key, down, bridgeCarrierRiseCycles, bridgeCarrierFallCycles)
		if !active {
			continue
		}

		refs := append([]string{key}, nicRefs...)
		detail := fmt.Sprintf("bridge %s on node %s has no carrier on any uplink (%s) — this bridge currently has no path off the node",
			br.Name, br.GetRef().Node, strings.Join(nicRefs, ", "))
		f := newHealthFinding(CheckBridgeNoCarrier, SeverityError, detail, []string{br.GetRef().Node}, refs)
		f.DocsLink = bridgeNoCarrierDocsLink
		out = append(out, f)
	}

	db.Prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// resolvePhysNics resolves ports (a bridge's Ports, direct PhysNic or Bond
// refs) down to their underlying PhysNic entities, following one level of
// bond enslavement (the same NIC->bond->bridge chain internal/drift/mtu.go
// walks) — a bond's own carrier concept doesn't exist independently of its
// slaves' physical carrier state.
func resolvePhysNics(snap inventory.Snapshot, ports []inventory.Ref) []*inventory.PhysNic {
	var out []*inventory.PhysNic
	for _, ref := range ports {
		e, ok := snap.Get(ref)
		if !ok {
			continue
		}
		switch v := e.(type) {
		case *inventory.PhysNic:
			out = append(out, v)
		case *inventory.Bond:
			slaves := v.Slaves
			if len(slaves) == 0 {
				slaves = v.DeclaredSlaves
			}
			for _, sname := range slaves {
				sref := inventory.Ref{Kind: inventory.KindPhysNic, Node: ref.Node, ID: sname}
				if sn, ok := snap.Get(sref); ok {
					if p, ok := sn.(*inventory.PhysNic); ok {
						out = append(out, p)
					}
				}
			}
		}
	}
	return out
}
