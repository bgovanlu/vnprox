// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// slaveMeta is one bond slave's identity + current MII/active state, as
// reported by host.Reader.Links via inventory's Bond.SlaveDetail.
type slaveMeta struct {
	Name   string
	Active bool
}

// refMeta is everything the sampler needs to know about one sampleable
// interface on a node besides its raw counters: its inventory Ref (so
// callers/consumers can address it the same way the rest of the API does),
// its link speed for utilization math, and — for a Bond — its slave list
// for the per-slave balance view (docs/features/monitoring.md §1: "Bond
// member balance shown per-slave").
type refMeta struct {
	Ref       inventory.Ref
	Slaves    []slaveMeta
	SpeedMbps int
}

// refMetasFromLinks derives one refMeta per sampleable interface (physical
// NIC, bond, bridge, VLAN sub-interface) from a node's netlink-equivalent
// link state, reusing inventory.FromNetlinkLinks rather than duplicating
// its Kind-classification switch (host.LinkState.Kind -> inventory.Kind).
// veth/OVS-internal/unknown links FromNetlinkLinks itself does not model
// are silently absent here too.
//
// A Bond has no ethtool-reported speed of its own (the kernel bonding
// master does not negotiate a link speed), so its effective capacity for
// utilization purposes is approximated as the sum of its *active* slaves'
// negotiated speeds — the actual aggregate bandwidth 802.3ad/active-backup
// can currently push. If no slave is marked active (a stale/incomplete
// bonding read), it falls back to summing every declared slave, which is
// still a better estimate than reporting "no speed data" for a bond that
// plainly has some.
func refMetasFromLinks(node string, links []host.LinkState) []refMeta {
	entities := inventory.FromNetlinkLinks(node, links)

	nameSpeed := make(map[string]int, len(entities))
	for _, e := range entities {
		if p, ok := e.(*inventory.PhysNic); ok {
			nameSpeed[p.Name] = p.SpeedMbps
		}
	}

	out := make([]refMeta, 0, len(entities))
	for _, e := range entities {
		switch v := e.(type) {
		case *inventory.PhysNic:
			out = append(out, refMeta{Ref: v.Ref, SpeedMbps: v.SpeedMbps})
		case *inventory.Bond:
			slaves := make([]slaveMeta, 0, len(v.SlaveDetail))
			activeSpeed, allSpeed := 0, 0
			for _, sd := range v.SlaveDetail {
				slaves = append(slaves, slaveMeta{Name: sd.Name, Active: sd.Active})
				sp := nameSpeed[sd.Name]
				allSpeed += sp
				if sd.Active {
					activeSpeed += sp
				}
			}
			speed := activeSpeed
			if speed == 0 {
				speed = allSpeed
			}
			out = append(out, refMeta{Ref: v.Ref, SpeedMbps: speed, Slaves: slaves})
		case *inventory.Bridge:
			out = append(out, refMeta{Ref: v.Ref})
		case *inventory.VlanIface:
			// Inherit the parent interface's speed when known, so a VLAN
			// sub-interface's utilization is still computable.
			out = append(out, refMeta{Ref: v.Ref, SpeedMbps: nameSpeed[v.ParentName]})
		}
	}
	return out
}

// countersFromIfaceStats adapts host.IfaceStats to this package's Counters.
func countersFromIfaceStats(s host.IfaceStats) Counters {
	return Counters{
		RxBytes: s.RxBytes, TxBytes: s.TxBytes,
		RxPkts: s.RxPackets, TxPkts: s.TxPackets,
		RxErrs: s.RxErrors, TxErrs: s.TxErrors,
		RxDrop: s.RxDropped, TxDrop: s.TxDropped,
	}
}
