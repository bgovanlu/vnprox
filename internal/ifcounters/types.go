// SPDX-License-Identifier: Apache-2.0

package ifcounters

import (
	"context"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// NeighborLister is the small seam this package needs off
// internal/topology.Service — exactly LLDPNeighbors(), mirroring
// internal/mtuprobe.Config's Discoverer seam pattern ("small interface,
// real type satisfies it implicitly"; *topology.Service needs no adapter).
type NeighborLister interface {
	LLDPNeighbors() []*inventory.LldpNeighbor
}

// Target is one switch's resolved SNMP poll configuration for this tick —
// the decrypted, ready-to-dial form of a switch_snmp_targets row.
// internal/store never hands back a decrypted community string on its own;
// TargetStore's real implementation (cmd/vnproxd's wiring) decrypts fresh
// per call, the same "decrypt only for the duration of use" discipline the
// guarded-push driver factory (doc.go) follows for its own switch
// credentials — this package borrows the discipline, not the import.
type Target struct {
	ChassisID string
	MgmtAddr  string // operator-pinned management address; empty means "use the LLDP-advertised MgmtIP"
	Community []byte
	Port      int // UDP port, defaults to snmp.DefaultPort (161) if zero
}

// TargetStore lists the currently-enabled SNMP poll targets, keyed by
// ChassisID. Service.Tick calls it once per tick and treats a listing error
// as "every switch is not configured this tick" (logged, not fatal — a
// transient store error must never make the poller stop rendering its
// existing not-configured state).
type TargetStore interface {
	ListEnabled(ctx context.Context) ([]Target, error)
}

// State is one of the honest facts a Result can report — see doc.go.
type State string

const (
	// StateNotConfigured: this switch's chassis has no enabled
	// switch_snmp_targets row. No SNMP poll was attempted.
	StateNotConfigured State = "not_configured"
	// StateUnreachable: a poll was attempted and failed at the transport
	// level (timeout, connection refused, wrong community rejected outright)
	// — the switch, or the network path to it, did not answer usefully.
	StateUnreachable State = "unreachable"
	// StateNoCounters: the switch answered other queries against it this
	// tick, but this specific port's counters could not be obtained — no
	// ifIndex correlated to the LLDP-advertised port, or the agent returned
	// one of RFC 3416's exception values for it (interface removed,
	// reindexed since discovery, etc).
	StateNoCounters State = "no_counters"
	// StateOK: real counters, from this tick's poll.
	StateOK State = "ok"
)

// Counters is one port's IF-MIB reading, valid only when a Result's State is
// StateOK.
type Counters struct {
	InErrors    uint64
	OutErrors   uint64
	InDiscards  uint64
	OutDiscards uint64
	InOctets    uint64 // ifHCInOctets
	OutOctets   uint64 // ifHCOutOctets
	OperUp      bool
}

// Result is one LLDP-observed local-port<->switch-port edge's current
// polled state — Service's in-memory current-state store (mirrors
// internal/mtuprobe.Result's "current state, not a ring" convention: MTU/
// counter freshness matters, history does not, so there is no SQLite table
// here either).
type Result struct {
	ChassisID  string
	SwitchName string
	Node       string // the local PVE node this LLDP neighbor was observed from
	LocalIface string
	SwitchPort string // the LLDP-advertised remote port id — for display, not necessarily the same string as an ifDescr/ifName
	State      State
	Counters
	At int64 // unix seconds this tick ran
}
