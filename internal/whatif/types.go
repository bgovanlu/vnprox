// SPDX-License-Identifier: Apache-2.0

package whatif

import (
	"github.com/bgovanlu/vnprox/internal/capacity"
	"github.com/bgovanlu/vnprox/internal/ceph"
	"github.com/bgovanlu/vnprox/internal/failsim"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/wireguard"
)

// Attachment kind values, matching inventory.GuestNic.TargetName's
// convention: a plain bridge name, or a bare SDN VNet id.
const (
	AttachBridge = "bridge"
	AttachVNet   = "vnet"
)

// Attachment names what a GuestProfile's NICs plug into. All of a profile's
// NICs are assumed to share this one attachment — the common "N identical
// guests of one type" case the card describes, not a per-NIC topology.
type Attachment struct {
	// Kind is AttachBridge or AttachVNet.
	Kind string
	// Node is the PVE node the new guests are placed on. Required even for
	// a VNet attachment: a VNet realizes to a specific node's underlay
	// bridge, and the synthetic guest/NIC entities this package builds are
	// node-scoped, matching how a real guest's placement works.
	Node string
	// Name is the bridge name (Kind==AttachBridge) or the bare VNet id
	// (Kind==AttachVNet) — the exact string a real GuestNic.TargetName
	// would carry for this attachment.
	Name string
}

// GuestProfile is "profile X" from the card: what one guest of this profile
// looks like, for capacity/IPAM/failure-impact purposes.
type GuestProfile struct {
	Attachment   Attachment
	Name         string
	NICCount     int
	ExpectedMbps float64
}

// CapacityInput is the capacity axis's already-resolved data: the target
// link's identity/speed and its real daily-rollup history. Gathering this
// from internal/store/internal/capacity is a composition-root concern (see
// doc.go); this package only projects over it.
type CapacityInput struct {
	LinkRef       string
	History       []capacity.Aggregate
	LinkSpeedMbps int
}

// IPAMInput is the IPAM axis's already-resolved data: the subnet pool(s) the
// profile's Attachment draws addresses from. Usually one entry; two for a
// dual-stack (v4+v6) VNet, each checked independently.
type IPAMInput struct {
	Subnets []ipam.Subnet
}

// FailsimInput is the failure-impact axis's already-resolved data: the live
// snapshot plus failsim's own optional side-tables, and the entity whose
// failure is being evaluated against the resulting footprint.
type FailsimInput struct {
	Snapshot inventory.Snapshot
	Corosync *host.CorosyncConfig
	Ceph     *ceph.Status
	Target   inventory.Ref
	Tunnels  []wireguard.Tunnel
}

// Request is one what-if evaluation: add N guests of Profile, and check the
// resulting footprint against all three axes.
type Request struct {
	Failsim  FailsimInput
	IPAM     IPAMInput
	Profile  GuestProfile
	Capacity CapacityInput
	N        int
}

// AxisStatus is one axis's evaluability, independent of whether it breaks.
type AxisStatus string

const (
	// AxisOK: evaluated, and does not break within the requested N.
	AxisOK AxisStatus = "ok"
	// AxisBreaks: evaluated, and breaks at or before the requested N.
	AxisBreaks AxisStatus = "breaks"
	// AxisUnavailable: could not be evaluated — never treated as
	// unconstrained, and never eligible to be named the binding constraint.
	AxisUnavailable AxisStatus = "unavailable"
)

// CapacityAxis is the bandwidth-headroom axis's result. Always an ESTIMATE
// (see doc.go) — Basis states exactly what it is derived from and what it
// assumes, so a caller never sees a confident number with no stated basis.
type CapacityAxis struct {
	BreaksAtN        *int
	Status           AxisStatus
	Basis            string
	Reason           string
	ConsumedPct      float64
	AlreadyOverToday bool
	Estimated        bool
}

// IPAMAxis is the address-pool-exhaustion axis's result. Always EXACT (see
// doc.go) — Total/Allocated/Free are the live pool counts, and the
// exhaustion guest count is arithmetic over them, not a projection.
type IPAMAxis struct {
	BreaksAtN     *int
	Status        AxisStatus
	Subnet        string
	Reason        string
	FreeAddresses int
	AddrsPerGuest int
	Estimated     bool
}

// FailsimAxis is the failure-impact axis's result: reused verbatim from
// internal/failsim.Simulate, not re-derived. Before is the impact of Target
// failing today; After is the impact of Target failing once the N guests
// exist. AddedDisconnected is the number of guests newly at risk (almost
// always 0 or N, since a profile's NICs all share one Attachment — either
// the carrier survives Target's failure or it doesn't).
type FailsimAxis struct {
	BreaksAtN         *int
	Status            AxisStatus
	Reason            string
	Before            failsim.Impact
	After             failsim.Impact
	AddedDisconnected int
}

// Verdict is Evaluate's answer: one combined result citing all three axes by
// name, plus which one binds first.
type Verdict struct {
	BindingAtN  *int
	Binding     string
	Summary     string
	Unavailable []string
	Profile     GuestProfile
	Capacity    CapacityAxis
	Failsim     FailsimAxis
	IPAM        IPAMAxis
	N           int
}
