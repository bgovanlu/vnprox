// Package switchdrv is T-1205's driver abstraction for pushing a strictly
// bounded set of configuration to a physical switch port: VLAN membership,
// port description, and LACP settings — and nothing else. It is the
// read-write physical step beyond LLDP-read (docs/features/lldp-discovery.md),
// deliberately scoped to exactly the operations facing a PVE node's uplink so
// a switch push can never do more than the change engine's `switch.port.update`
// op family allows.
//
// The driver interface is transport-agnostic; the shipped implementation
// targets OpenConfig over gNMI (openconfig.go), with vendor drivers a
// documented future extension point behind the same interface (not implemented
// this task). Development and tests run against internal/switchmock, an
// in-memory SwitchDriver double — the real gNMI wire behavior against physical
// hardware is a needs-hardware-validation item (see planning/reports/T-1205.md).
//
// Nothing in this package ever applies a change of its own accord: every
// mutation is driven by the change engine's staged→validated→confirmed
// changeset lifecycle (CLAUDE.md's change-engine invariant). The Gateway type
// here is the daemon-level seam the change engine calls, and it re-verifies
// the target port's live LLDP neighbor immediately before every write — a
// mandatory interlock no op can bypass (T-1205 safety analysis).
package switchdrv

// LACPMode is the LACP participation mode of a switch port.
type LACPMode string

const (
	// LACPOff disables LACP on the port (a plain access/trunk port).
	LACPOff LACPMode = "off"
	// LACPActive makes the port actively initiate LACP negotiation.
	LACPActive LACPMode = "active"
	// LACPPassive makes the port respond to, but not initiate, LACP.
	LACPPassive LACPMode = "passive"
)

// LACPRate is the LACPDU transmit rate of an LACP-enabled port.
type LACPRate string

const (
	// LACPRateSlow requests slow (30s) LACPDU transmission (the 802.3ad default).
	LACPRateSlow LACPRate = "slow"
	// LACPRateFast requests fast (1s) LACPDU transmission.
	LACPRateFast LACPRate = "fast"
)

// LACPConfig is a port's LACP settings — one of the exactly-three attribute
// groups a SwitchDriver may read or write (VLAN membership, description, LACP).
type LACPConfig struct {
	Mode LACPMode `json:"mode"`
	Rate LACPRate `json:"rate,omitempty"`
}

// PortConfig is the complete, bounded configuration of one switch port the
// SwitchDriver reads (for a pre-image snapshot) and writes. It carries exactly
// the three attribute groups T-1205 scopes the driver to — VLAN membership
// (Untagged/native PVID plus Tagged trunk VIDs), Description, and LACP — and
// nothing else. There is deliberately no full-config or arbitrary-attribute
// surface: a SwitchDriver cannot express any other port operation.
type PortConfig struct {
	LACP        LACPConfig `json:"lacp"`
	Description string     `json:"description"`
	Tagged      []int      `json:"tagged"`
	Untagged    int        `json:"untagged"`
}

// Neighbor is the LLDP neighbor a switch reports seeing on one of its ports —
// the identity the driver re-reads immediately before any write to confirm the
// cable still faces the PVE node it was scoped against (T-1205 safety
// analysis: "LLDP-verified port identity before any write").
type Neighbor struct {
	ChassisID string `json:"chassisId"`
	PortID    string `json:"portId"`
}

// Matches reports whether the live neighbor n is the same as want. An empty
// want.ChassisID never matches: a port scoped with no recorded neighbor cannot
// pass the pre-write identity check (fail-closed), rather than silently
// allowing a write against an unidentified port.
func (n Neighbor) Matches(want Neighbor) bool {
	if want.ChassisID == "" {
		return false
	}
	return n.ChassisID == want.ChassisID && n.PortID == want.PortID
}
