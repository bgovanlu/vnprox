package host

import (
	"context"
	"errors"
)

// Reader is vnprox's contract for local host-level network state: the
// literal /etc/network/interfaces(5) file content (declared intent),
// netlink-equivalent link/bridge/bond/vlan state and addresses (live
// runtime), LLDP neighbor data, and interface counters (docs/architecture.md
// §3, docs/data-model.md §1).
//
// Its method set is deliberately the same four methods —
// InterfacesFile/Links/LLDP/Stats — that T-004 anticipated in
// internal/pvemock.HostReader (see that file's doc comment). The two
// interfaces are not literally interchangeable in Go's type system (they
// use different concrete result types: host.LinkState/host.IfaceStats here
// vs. pvemock.LinkState/pvemock.IfaceStats there — Go's structural typing
// requires identical result types, not just structurally similar ones, so
// a *pvemock.FixtureHostReader value cannot be assigned directly to a
// host.Reader variable). What's preserved is the conceptual shape: same
// four operations, same per-node cluster-aware signature, so a FixtureReader
// in this package (fixture.go) can trivially adapt one to the other. Richer
// netlink detail this package's Links() exposes beyond pvemock's minimal
// LinkState (bond runtime, bridge VLAN table, FDB, addresses) lives inside
// LinkState's substructures rather than as additional interface methods, to
// keep that parity intentional rather than incidental.
//
// Every method takes a node name because vnprox is cluster-aware: any
// node's daemon may need another node's host state via the peer API. The
// `real` implementation (Real, in real.go) only ever serves its own node —
// callers are responsible for routing reads of a peer node to that peer's
// daemon (docs/architecture.md §1, §5); this package does not implement
// that routing.
type Reader interface {
	// InterfacesFile returns the literal content of /etc/network/interfaces
	// (or interfaces.new when includePending is true and a staged config
	// exists) for node, in ifupdown2(5) stanza syntax. Use ParseInterfaces
	// to obtain a structured, lossless view.
	InterfacesFile(ctx context.Context, node string, includePending bool) (string, error)

	// Links returns netlink-equivalent link state (physical NICs, bonds,
	// bridges, VLAN sub-interfaces, veths, ...) for node, including bond
	// runtime detail, bridge VLAN/FDB tables, and addresses.
	Links(ctx context.Context, node string) ([]LinkState, error)

	// LLDP returns raw LLDP neighbor JSON for node, matching the shape
	// `lldpctl -f json` produces closely enough to be parsed into
	// inventory.LldpNeighbor.
	LLDP(ctx context.Context, node string) ([]byte, error)

	// Stats returns interface counters for node, keyed by interface name.
	Stats(ctx context.Context, node string) (map[string]IfaceStats, error)

	// FRRBGPSummary returns raw `vtysh -c "show bgp summary json"` output
	// for node (T-404's EVPN/BGP observability, docs/features/sdn.md §3).
	// Returns an error wrapping ErrFRRUnavailable when FRR is not
	// installed/running on node at all — a documented, cleanly-degraded
	// condition distinct from a transient/parse failure. Use
	// ParseBGPSummary to obtain a structured view.
	FRRBGPSummary(ctx context.Context, node string) ([]byte, error)

	// FRREVPNVNI returns raw `vtysh -c "show evpn vni json"` output for
	// node. Same ErrFRRUnavailable convention as FRRBGPSummary. Use
	// ParseEVPNVNI to obtain a structured view.
	FRREVPNVNI(ctx context.Context, node string) ([]byte, error)
}

// ErrUnsupportedPlatform is returned by real.go's OS-specific paths (raw
// ethtool ioctls, netlink) when built for or run on a platform that does
// not support them. vnprox only ships for Linux, but this package is
// exercised by `go build`/`go vet` on any GOOS during development.
var ErrUnsupportedPlatform = errors.New("host: unsupported platform")

// ErrNotFound indicates the requested node is not known to a Reader.
// FixtureReader returns this for unknown node names, mirroring
// pvemock.ErrNotFound; Real never returns it since it only ever serves its
// own node (any node name is accepted).
var ErrNotFound = errors.New("host: not found")

// LinkState is one netlink-equivalent link (physical NIC, bond, bridge,
// VLAN sub-interface, veth, OVS bridge/bond, ...) as observed on a node.
type LinkState struct {
	Bond        *BondDetail
	Bridge      *BridgeDetail
	OperState   string
	VlanParent  string
	Driver      string
	PCIAddr     string
	Kind        string
	Mac         string
	Name        string
	Master      string
	Duplex      string
	Members     []string
	Addresses   []string
	SpeedMbps   int
	VlanID      int
	SRIOVNumVFs int
	MTU         int
	Index       int
	LinkUp      bool
}

// BondDetail is bond runtime state as reported by
// /proc/net/bonding/<name> (mode, active slave, per-slave MII status) —
// state netlink's IFLA_BOND attributes alone do not fully expose in a form
// this package can rely on portably across kernel versions.
type BondDetail struct {
	Mode           string // e.g. "802.3ad (4)", normalized to just the name where possible
	LACPRate       string
	XmitHashPolicy string
	MIIStatus      string // aggregate bond MII status, e.g. "up"/"down"
	ActiveSlave    string
	Slaves         []BondSlave
}

// BondSlave is one slave interface's status within a bond.
type BondSlave struct {
	Name             string
	MIIStatus        string
	PermHWAddr       string
	LinkFailureCount int
	Active           bool
}

// BridgeDetail is bridge-specific state: whether it is VLAN-aware, its
// global VLAN table, per-port VLAN membership, and its FDB.
type BridgeDetail struct {
	PortVLANs map[string][]PortVlan
	VLANs     []VidRange
	FDB       []FDBEntry
	VlanAware bool
	STP       bool
}

// VidRange is an inclusive VLAN ID range, e.g. {Low: 2, High: 4094}. A
// single VLAN is represented as Low == High.
type VidRange struct{ Low, High int }

// PortVlan is one bridge port's membership in a VLAN or contiguous VLAN
// range (a single VID is represented as Vids.Low == Vids.High), mirroring
// how the kernel's bridge VLAN table itself compacts contiguous trunked
// ranges into one begin/end pair rather than one entry per VID.
type PortVlan struct {
	Vids     VidRange
	PVID     bool
	Untagged bool
}

// FDBEntry is one bridge forwarding database entry.
type FDBEntry struct {
	Mac       string
	Port      string // ifname
	Vlan      int
	Master    bool // learned on the bridge itself, not a port
	Permanent bool
	// Stale mirrors the kernel's NUD_STALE neighbor state (netlink_linux.go)
	// for a dynamically-learned entry that has aged past the bridge's
	// ageing timer without fresh traffic — T-306's MAC/FDB browser staleness
	// signal (docs/features/lldp-discovery.md §4). It is independent of
	// Permanent (a permanent entry is never stale) and of collector
	// staleness (docs/features/topology.md §5's poll-freshness banner):
	// this is the switch's own "have I seen this MAC recently" bit.
	Stale bool
}

// IfaceStats is a counters snapshot for one interface, from
// /sys/class/net/<iface>/statistics/*.
type IfaceStats struct {
	RxBytes   uint64
	TxBytes   uint64
	RxPackets uint64
	TxPackets uint64
	RxErrors  uint64
	TxErrors  uint64
	RxDropped uint64
	TxDropped uint64
}
