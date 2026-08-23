package inventory

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Entity is one normalized inventory object. Every concrete entity embeds a
// Ref (its identity) and implements this interface. The unexported methods
// power delta diffing (fieldMap) and cheap immutable snapshots (clone);
// callers outside the package use the concrete types via the Snapshot
// accessors.
type Entity interface {
	// GetRef returns the entity's identity.
	GetRef() Ref
	// fieldMap returns a canonical string form of every resolved field, used
	// to compute changed-field sets for deltas and to stringify provenance
	// conflicts. Keys are stable field names shared with the ownership table.
	fieldMap() map[string]string
	// clone returns a deep copy so a published (immutable) snapshot never
	// shares mutable slice/map backing with the writer's working copy.
	clone() Entity
	// rawSource returns the raw source text this contribution was derived
	// from (see rawSrc), or "" if none was attached.
	rawSource() string
}

// rawSrc carries the raw source text an entity contribution was derived
// from: the interfaces(5) stanza text for SourceHostInterfaces, the
// pretty-printed JSON of the PVE API object for the SourcePVE* sources, or
// a compact JSON rendering of the netlink link state for SourceHostNetlink
// (see the From* adapters in ingest.go). It is embedded in every concrete
// entity type. The text is metadata, not a merged field: it is excluded
// from fieldMap (so it never triggers deltas or provenance) and is exposed
// per Ref, per Source via Snapshot.RawSource, not on resolved entities.
type rawSrc struct{ raw string }

func (r rawSrc) rawSource() string      { return r.raw }
func (r *rawSrc) setRawSource(s string) { r.raw = s }

// VidRange is an inclusive VLAN ID range (a single VID has Low == High),
// mirroring how the kernel bridge VLAN table compacts contiguous trunks.
type VidRange struct{ Low, High int }

func (v VidRange) String() string {
	if v.Low == v.High {
		return strconv.Itoa(v.Low)
	}
	return strconv.Itoa(v.Low) + "-" + strconv.Itoa(v.High)
}

func vidsString(vs []VidRange) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = v.String()
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// sortedJoin canonicalizes an order-insensitive string slice (bridge ports,
// bond slaves, zone nodes) so that a reordered-but-equal poll does not
// register as a change.
func sortedJoin(ss []string) string {
	cp := append([]string(nil), ss...)
	sort.Strings(cp)
	return strings.Join(cp, ",")
}

func refsJoin(rs []Ref) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = r.String()
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// hostPCIJoin canonicalizes a Guest.HostPCI map into a stable, order
// insensitive string for fieldMap (sorted "key=value" pairs), the same
// map-to-deterministic-string convention this file uses throughout so a
// reordered-but-equal poll never registers as a spurious delta.
func hostPCIJoin(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + m[k]
	}
	return strings.Join(parts, ",")
}

// --- cluster node (single-source: pve-cluster) ---------------------------

// Node is one PVE cluster member.
type Node struct {
	Ref
	rawSrc
	Name    string
	IP      string
	Status  string // online|offline
	Quorate bool
	Local   bool
}

func (n *Node) GetRef() Ref { return n.Ref }
func (n *Node) clone() Entity {
	cp := *n
	return &cp
}
func (n *Node) fieldMap() map[string]string {
	return map[string]string{
		"name": n.Name, "ip": n.IP, "status": n.Status,
		"quorate": boolStr(n.Quorate), "local": boolStr(n.Local),
	}
}

// --- physical / L2 entities (multi-source: host-netlink, host-interfaces,
//     pve-network) ---------------------------------------------------------

// PhysNic is a physical NIC. Runtime fields (link/speed/duplex/mac/driver)
// come from host-netlink; MTUDeclared is the intended MTU from the
// interfaces file / PVE network API.
type PhysNic struct {
	Ref
	rawSrc
	Name    string
	Mac     string
	Driver  string
	PCIAddr string
	Duplex  string
	// MediaPort is the NIC's PORT_* media/connector type (T-3503: "tp",
	// "aui", "mii", "fibre", "bnc", "da", "none", "other" — see
	// internal/host/ethtool_linux.go's mediaPortString). Host-netlink-only,
	// like Driver/Duplex/SpeedMbps: it comes from the same SIOCETHTOOL
	// ioctl call that fills those, and is populated whenever that ioctl
	// succeeds — including when the link has no carrier (unlike SpeedMbps,
	// see planning/reports/evidence/pve-9.2.4-nic-media-and-speed.txt point
	// 2). "" when unreported or unrecognised — never guessed.
	MediaPort   string
	OperState   string
	Pending     string
	SRIOVVFs    []VirtualFunction
	SpeedMbps   int
	MTUDeclared int
	MTU         int
	LinkUp      bool
	LinkUpSet   bool
}

func (p *PhysNic) GetRef() Ref { return p.Ref }
func (p *PhysNic) clone() Entity {
	cp := *p
	cp.SRIOVVFs = append([]VirtualFunction(nil), p.SRIOVVFs...)
	return &cp
}
func (p *PhysNic) fieldMap() map[string]string {
	return map[string]string{
		"name": p.Name, "mac": p.Mac, "driver": p.Driver, "pciAddr": p.PCIAddr,
		"duplex": p.Duplex, "mediaPort": p.MediaPort, "operState": p.OperState,
		"speedMbps": strconv.Itoa(p.SpeedMbps), "mtu": strconv.Itoa(p.MTU),
		"mtuDeclared": strconv.Itoa(p.MTUDeclared),
		"linkUp":      boolStr(p.LinkUp), "pending": p.Pending,
	}
}

// VirtualFunction is one SR-IOV virtual function belonging to a PhysNic
// acting as a PF (T-1506, docs/data-model.md §1). PF names the owning
// PhysNic's Ref; Ref (this VF's own identity, Kind KindVF,
// "<pfName>/vf<ID>") lets it be named independently in findings
// (vf_spoofcheck_mismatch) and changeset validation output even though it
// is not itself tracked in the graph (see PhysNic.SRIOVVFs' doc comment).
// AssignedGuest is the zero Ref when no guest's hostpci config currently
// resolves to this VF's PCIAddr — "never guessed", the same honesty
// contract flow_samples' src_ref/dst_ref and T-1501's k8s node correlation
// document elsewhere in this codebase.
type VirtualFunction struct {
	Ref
	PF            Ref
	MacAddr       string
	PCIAddr       string
	AssignedGuest Ref
	VLAN          int
	SpoofCheck    bool
	Trust         bool
}

// BondSlaveState is one slave's runtime status inside a bond.
//
// T-804: the Actor*/Partner* fields (and LACPDetailSet) mirror
// host.BondSlave's own LACP actor/partner detail one-for-one — decoded
// from /proc/net/bonding's "details actor/partner lacp pdu" block,
// opportunistically refined by netlink AD-info where the kernel exposes it
// (see internal/host/bonding.go, internal/host/netlink_linux.go). Best-
// effort: LACPDetailSet is false (every other field below its zero value)
// for a bond not running 802.3ad, or on a kernel/driver that never emits
// the /proc detail block at all.
//
// Field order below is size-grouped (strings, then ints, then bools) to
// satisfy golangci-lint's fieldalignment check, exactly mirroring
// host.BondSlave's own layout.
type BondSlaveState struct {
	Name                  string
	MIIStatus             string
	PermHWAddr            string
	ActorSystemID         string
	PartnerSystemID       string
	LinkFailureCount      int
	ActorSystemPriority   int
	ActorKey              int
	PartnerSystemPriority int
	PartnerKey            int
	Active                bool
	ActorSynchronized     bool
	ActorCollecting       bool
	ActorDistributing     bool
	LACPDetailSet         bool
}

// Bond is a Linux bond (or OVS bond when Ref.Kind == KindOVSBond). Mode /
// per-slave status / active slave / runtime Slaves come from host-netlink
// (/proc/net/bonding); DeclaredSlaves and the declared mode come from the
// interfaces file.
type Bond struct {
	Ref
	MIIStatus      string
	Name           string
	Mode           string
	LACPRate       string
	XmitHashPolicy string
	rawSrc
	ActiveSlave string
	Pending     string

	// OVSBridge is the OVS bridge this bond attaches to, parsed from the
	// stanza's ovs_bridge option. It is meaningful only when Kind is
	// KindOVSBond and is empty for a Linux bond.
	//
	// It exists because an OVS bond is not re-creatable without it: unlike
	// a Linux bond, whose slaves fully describe it, an OVS bond is a member
	// of exactly one bridge and the create op requires that name
	// (BondCreateParams.Bridge, rendered as ovs_bridge by
	// internal/change/ifaces.BondCreate). Before T-3105 the model dropped
	// this field, so time-machine restore could not rebuild an OVS bond it
	// had faithfully snapshotted — internal/change/restore_ops.go refused
	// with ErrRestoreUnsupported naming this exact absence.
	OVSBridge string

	Slaves         []string
	DeclaredSlaves []string
	SlaveDetail    []BondSlaveState
	MTU            int
	MTUDeclared    int
}

func (b *Bond) GetRef() Ref { return b.Ref }
func (b *Bond) clone() Entity {
	cp := *b
	cp.Slaves = append([]string(nil), b.Slaves...)
	cp.DeclaredSlaves = append([]string(nil), b.DeclaredSlaves...)
	cp.SlaveDetail = append([]BondSlaveState(nil), b.SlaveDetail...)
	return &cp
}
func (b *Bond) fieldMap() map[string]string {
	sd := make([]string, len(b.SlaveDetail))
	for i, s := range b.SlaveDetail {
		// T-804: fold the LACP actor/partner detail into the same summary
		// key so a change to it (e.g. a slave losing sync, or the partner
		// system changing) registers as a delta exactly like MIIStatus/
		// Active already do — see slaveDetailKey (resolve.go), which calls
		// through to this same rendering.
		sd[i] = fmt.Sprintf("%s/%s/%v/%s/%d/%d/%v/%v/%v/%s/%d/%d/%v",
			s.Name, s.MIIStatus, s.Active,
			s.ActorSystemID, s.ActorSystemPriority, s.ActorKey,
			s.ActorSynchronized, s.ActorCollecting, s.ActorDistributing,
			s.PartnerSystemID, s.PartnerSystemPriority, s.PartnerKey,
			s.LACPDetailSet,
		)
	}
	sort.Strings(sd)
	return map[string]string{
		"name": b.Name, "mode": b.Mode, "lacpRate": b.LACPRate,
		"xmitHashPolicy": b.XmitHashPolicy, "miiStatus": b.MIIStatus,
		"activeSlave": b.ActiveSlave, "slaves": sortedJoin(b.Slaves),
		"declaredSlaves": sortedJoin(b.DeclaredSlaves), "slaveDetail": strings.Join(sd, ";"),
		"mtu": strconv.Itoa(b.MTU), "mtuDeclared": strconv.Itoa(b.MTUDeclared),
		"pending": b.Pending,
		// Declared config, so a bond moved between OVS bridges out of band
		// registers as drift exactly like a slave list change does.
		"ovsBridge": b.OVSBridge,
	}
}

// BridgeVirt distinguishes a Linux bridge from an OVS bridge.
type BridgeVirt string

const (
	BridgeLinux BridgeVirt = "linux"
	BridgeOVS   BridgeVirt = "ovs"
)

// Bridge is a Linux or OVS bridge. Ports (resolved Refs) and PortNames
// (raw runtime membership from netlink) reflect what is actually enslaved;
// DeclaredPortNames reflect the configured bridge_ports. VlanAware/Vids/STP
// are declared config. MTU is runtime, MTUDeclared intended.
type Bridge struct {
	Ref
	rawSrc
	Gateway           string
	Name              string
	Virt              BridgeVirt
	Comments          string
	Pending           string
	Addresses         []string
	Vids              []VidRange
	DeclaredPortNames []string
	PortNames         []string
	Ports             []Ref
	// FDB is this bridge's forwarding-database (MAC learning table),
	// host-netlink-only (T-306's MAC/FDB browser,
	// docs/features/lldp-discovery.md §4). Unlike every other field on this
	// type it is deliberately excluded from fieldMap below: FDB churns on
	// every poll as traffic moves (far more often than declared/runtime
	// link config), and there is only ever one contributing source, so
	// there is nothing to merge/flag conflicts on — including it in
	// fieldMap would spam topology.delta on every MAC learn/age instead of
	// only on actual topology changes. See resolveBridge for how it's
	// copied straight through from the host-netlink partial rather than
	// going through pick.
	FDB         []FDBEntry
	MTU         int
	MTUDeclared int
	VlanAware   bool
	STP         bool
	// VlanAwareSet / STPSet report whether the contributing source actually
	// declared/observed VlanAware / STP (merge treats an unset flagged bool
	// as "not reported", not as an implicit false). On a resolved entity
	// each is true iff any source in the field's precedence list reported
	// the field.
	VlanAwareSet bool
	STPSet       bool
}

// FDBEntry is one bridge forwarding-database entry (mirrors
// host.FDBEntry's field set; kept as this package's own type so entity.go
// does not need to import internal/host — see ingest.go's FromNetlinkLinks
// for the conversion).
type FDBEntry struct {
	Mac       string
	Port      string
	Vlan      int
	Master    bool
	Permanent bool
	Stale     bool
}

func (b *Bridge) GetRef() Ref { return b.Ref }
func (b *Bridge) clone() Entity {
	cp := *b
	cp.Ports = append([]Ref(nil), b.Ports...)
	cp.PortNames = append([]string(nil), b.PortNames...)
	cp.DeclaredPortNames = append([]string(nil), b.DeclaredPortNames...)
	cp.Vids = append([]VidRange(nil), b.Vids...)
	cp.Addresses = append([]string(nil), b.Addresses...)
	cp.FDB = append([]FDBEntry(nil), b.FDB...)
	return &cp
}
func (b *Bridge) fieldMap() map[string]string {
	return map[string]string{
		"name": b.Name, "virt": string(b.Virt), "ports": refsJoin(b.Ports),
		"portNames": sortedJoin(b.PortNames), "declaredPortNames": sortedJoin(b.DeclaredPortNames),
		"vids": vidsString(b.Vids), "addresses": sortedJoin(b.Addresses),
		"gateway": b.Gateway, "comments": b.Comments, "mtu": strconv.Itoa(b.MTU),
		"mtuDeclared": strconv.Itoa(b.MTUDeclared), "vlanAware": boolStr(b.VlanAware),
		"stp": boolStr(b.STP), "pending": b.Pending,
	}
}

// VlanIface is a VLAN sub-interface (e.g. eno1.100 or vmbr0.20) OR an OVS
// Int Port (e.g. ovs_type OVSIntPort). Parent is resolved during linking
// from ParentName; Vid/addresses are declared, MTU runtime.
//
// An OVS Int Port has no dedicated inventory.Kind of its own (unlike
// OVSBridge/OVSBond) — per docs/data-model.md, "OVSIntPort ... map[s] to
// KindVlan" — so Virt is what distinguishes the two: "" for a plain 802.1q
// VLAN sub-interface, "ovs" for an OVS Int Port. This mirrors Bridge.Virt's
// exact shape/precedence rules (T-407) rather than inventing a new pattern.
// Trunks is OVS-only (a plain 802.1q sub-interface always carries exactly
// one VID, already in Vid); an OVS Int Port may instead (or additionally)
// carry a trunk VID set, per ovs-vsctl's port "trunks" column.
type VlanIface struct {
	Ref
	Parent Ref
	rawSrc
	Name        string
	ParentName  string
	Pending     string
	Virt        string // "" (plain 802.1q) | "ovs" (OVS Int Port)
	Addresses   []string
	Trunks      []VidRange
	Vid         int
	MTU         int
	MTUDeclared int
}

func (v *VlanIface) GetRef() Ref { return v.Ref }
func (v *VlanIface) clone() Entity {
	cp := *v
	cp.Addresses = append([]string(nil), v.Addresses...)
	cp.Trunks = append([]VidRange(nil), v.Trunks...)
	return &cp
}
func (v *VlanIface) fieldMap() map[string]string {
	return map[string]string{
		"name": v.Name, "parent": v.Parent.String(), "parentName": v.ParentName,
		"addresses": sortedJoin(v.Addresses), "vid": strconv.Itoa(v.Vid),
		"mtu": strconv.Itoa(v.MTU), "mtuDeclared": strconv.Itoa(v.MTUDeclared),
		"pending": v.Pending, "virt": v.Virt, "trunks": vidsString(v.Trunks),
	}
}

// --- SDN entities (single-source: pve-sdn) -------------------------------

// SdnZone is a cluster-scoped SDN zone. NodeStatus records per-node
// realization status, reconciled from real PVE's per-node
// GET /nodes/{node}/sdn/zones (T-3701 — pve.ReconcileSDNZoneStatus, one
// call per cluster node rather than an invented per-zone status route PVE
// 9.2.4 returns 501 for), and can carry the vnprox-synthesized "unknown"
// for a declared member node PVE had nothing to report for at all. Pending
// mirrors PVE's own staged-edit marker ("" | "new" | "changed" | "deleted",
// see pve.PendingState) — added by T-401 alongside the same-named field
// T-305 gave PhysNic/Bond/Bridge/VlanIface (docs/data-model.md's Pending
// doc comment), since SDN objects carry the identical staging concept.
// Structural (badge/topology) use only: the authoritative staged-vs-running
// field-level diff is internal/sdn.Service's job (a live PVE comparison,
// docs/features/sdn.md §1), not this entity.
type SdnZone struct {
	NodeStatus map[string]string
	Ref
	rawSrc
	ID         string
	Type       string
	Bridge     string
	Controller string
	IPAM       string
	Pending    string
	Nodes      []string
	ExitNodes  []string
	Peers      []string
	VrfVxlan   int
	MTU        int
}

func (z *SdnZone) GetRef() Ref { return z.Ref }
func (z *SdnZone) clone() Entity {
	cp := *z
	cp.Nodes = append([]string(nil), z.Nodes...)
	cp.ExitNodes = append([]string(nil), z.ExitNodes...)
	cp.Peers = append([]string(nil), z.Peers...)
	cp.NodeStatus = make(map[string]string, len(z.NodeStatus))
	for k, v := range z.NodeStatus {
		cp.NodeStatus[k] = v
	}
	return &cp
}
func (z *SdnZone) fieldMap() map[string]string {
	ns := make([]string, 0, len(z.NodeStatus))
	for k, v := range z.NodeStatus {
		ns = append(ns, k+"="+v)
	}
	sort.Strings(ns)
	return map[string]string{
		"id": z.ID, "type": z.Type, "bridge": z.Bridge, "controller": z.Controller,
		"ipam": z.IPAM, "vrfVxlan": strconv.Itoa(z.VrfVxlan), "mtu": strconv.Itoa(z.MTU),
		"nodes": sortedJoin(z.Nodes), "exitNodes": sortedJoin(z.ExitNodes),
		"peers": sortedJoin(z.Peers), "nodeStatus": strings.Join(ns, ","),
		"pending": z.Pending,
	}
}

// SdnVnet is a cluster-scoped VNet inside a zone. Pending: see SdnZone's
// doc comment.
type SdnVnet struct {
	Ref
	rawSrc
	ID        string
	Zone      string
	Alias     string
	Pending   string
	Tag       int
	VlanAware bool
}

func (n *SdnVnet) GetRef() Ref { return n.Ref }
func (n *SdnVnet) clone() Entity {
	cp := *n
	return &cp
}
func (n *SdnVnet) fieldMap() map[string]string {
	return map[string]string{
		"id": n.ID, "zone": n.Zone, "alias": n.Alias,
		"tag": strconv.Itoa(n.Tag), "vlanAware": boolStr(n.VlanAware),
		"pending": n.Pending,
	}
}

// SdnSubnet is a cluster-scoped subnet inside a VNet. ID is the CIDR.
// Pending: see SdnZone's doc comment.
type SdnSubnet struct {
	Ref
	rawSrc
	ID            string
	Vnet          string
	Gateway       string
	DNSZonePrefix string
	Pending       string
	DHCPRanges    []string
	SNAT          bool
}

func (s *SdnSubnet) GetRef() Ref { return s.Ref }
func (s *SdnSubnet) clone() Entity {
	cp := *s
	cp.DHCPRanges = append([]string(nil), s.DHCPRanges...)
	return &cp
}
func (s *SdnSubnet) fieldMap() map[string]string {
	return map[string]string{
		"id": s.ID, "vnet": s.Vnet, "gateway": s.Gateway,
		"dhcpRanges": sortedJoin(s.DHCPRanges), "dnsZonePrefix": s.DNSZonePrefix,
		"snat": boolStr(s.SNAT), "pending": s.Pending,
	}
}

// --- guests (single-source: pve-guest) -----------------------------------

// Guest is a qemu VM or lxc container.
type Guest struct {
	HostPCI map[string]string
	Ref
	rawSrc
	Name   string
	Type   string
	Node   string
	Status string
	VMID   int
}

func (g *Guest) GetRef() Ref { return g.Ref }
func (g *Guest) clone() Entity {
	cp := *g
	if g.HostPCI != nil {
		cp.HostPCI = make(map[string]string, len(g.HostPCI))
		for k, v := range g.HostPCI {
			cp.HostPCI[k] = v
		}
	}
	return &cp
}
func (g *Guest) fieldMap() map[string]string {
	return map[string]string{
		"vmid": strconv.Itoa(g.VMID), "name": g.Name, "type": g.Type,
		"node": g.Node, "status": g.Status, "hostPci": hostPCIJoin(g.HostPCI),
	}
}

// GuestNic is one NIC of a guest. TargetName is the raw bridge/vnet name
// from the guest config; BridgeOrVnet is resolved during linking to the
// matching Bridge or SdnVnet Ref. EffectiveVid is the VLAN the traffic
// actually rides after propagating any VNet tag (see attachment.go).
type GuestNic struct {
	Ref
	rawSrc
	Guest        Ref
	Key          string // "net0"
	TargetName   string
	BridgeOrVnet Ref // resolved during linking
	Model        string
	Mac          string
	Vid          int // tag from the guest config (0 = untagged / access)
	EffectiveVid int // resolved VLAN incl. VNet tag propagation
	RateMbps     int
	Firewall     bool
	LinkDown     bool
}

func (n *GuestNic) GetRef() Ref { return n.Ref }
func (n *GuestNic) clone() Entity {
	cp := *n
	return &cp
}
func (n *GuestNic) fieldMap() map[string]string {
	return map[string]string{
		"guest": n.Guest.String(), "key": n.Key, "targetName": n.TargetName,
		"bridgeOrVnet": n.BridgeOrVnet.String(), "model": n.Model, "mac": n.Mac,
		"vid": strconv.Itoa(n.Vid), "effectiveVid": strconv.Itoa(n.EffectiveVid),
		"rateMbps": strconv.Itoa(n.RateMbps), "firewall": boolStr(n.Firewall),
		"linkDown": boolStr(n.LinkDown),
	}
}

// --- LLDP (single-source: host-lldp) -------------------------------------

// LldpNeighbor is one LLDP- (or CDP-decoded) neighbor seen on a local NIC.
// LocalNic is resolved during linking from LocalIface + Node.
//
// VLAN is the neighbor's native/untagged (PVID) VLAN, kept under its
// original T-104 field name for backward compatibility; TaggedVLANs is the
// switch port's additional advertised tagged VLANs
// (docs/features/lldp-discovery.md §1: "advertised VLANs (PVID + tagged)").
// LastSeen is the unix-seconds timestamp of the poll that most recently
// observed this neighbor — the basis for spec §3's staleness lifecycle
// (internal/topology computes grey/drop state from it, clock-injectably, so
// it stays a plain fact here rather than a derived status).
type LldpNeighbor struct {
	LocalNic Ref
	Ref
	MgmtIP        string
	SpeedDescr    string
	Node          string
	Protocol      string
	ChassisName   string
	ChassisID     string
	ChassisIDType string
	ChassisDescr  string
	rawSrc
	LocalIface  string
	PortID      string
	PortIDType  string
	PortDescr   string
	MgmtIPs     []string
	TaggedVLANs []int
	SpeedMbps   int
	VLAN        int
	TTL         int
	LastSeen    int64
}

func (l *LldpNeighbor) GetRef() Ref { return l.Ref }
func (l *LldpNeighbor) clone() Entity {
	cp := *l
	cp.MgmtIPs = append([]string(nil), l.MgmtIPs...)
	cp.TaggedVLANs = append([]int(nil), l.TaggedVLANs...)
	return &cp
}

// fieldMap deliberately omits LastSeen: it refreshes on every successful
// poll for an unchanged neighbor (like the interface counters
// hostPollOnce's doc comment describes), so including it here would mark
// every still-present neighbor "updated" every poll cycle and spam
// topology.delta — the same reasoning that keeps raw counters out of the
// entity model entirely. LastSeen is still exposed via the entity's plain
// exported field (topology.Detail's Fields uses JSON reflection, not
// fieldMap) for the ports table / staleness computation to read directly.
func (l *LldpNeighbor) fieldMap() map[string]string {
	tagged := make([]string, len(l.TaggedVLANs))
	for i, v := range l.TaggedVLANs {
		tagged[i] = strconv.Itoa(v)
	}
	return map[string]string{
		"localNic": l.LocalNic.String(), "localIface": l.LocalIface, "node": l.Node,
		"protocol":    l.Protocol,
		"chassisName": l.ChassisName, "chassisId": l.ChassisID, "chassisIdType": l.ChassisIDType,
		"chassisDescr": l.ChassisDescr,
		"portId":       l.PortID, "portIdType": l.PortIDType, "portDescr": l.PortDescr,
		"mgmtIP": l.MgmtIP, "mgmtIPs": sortedJoin(l.MgmtIPs),
		"vlan": strconv.Itoa(l.VLAN), "taggedVlans": strings.Join(tagged, ","),
		"speedMbps": strconv.Itoa(l.SpeedMbps), "speedDescr": l.SpeedDescr,
		"ttl": strconv.Itoa(l.TTL),
	}
}

// --- firewall (single-source: pve-firewall) ------------------------------

// FwRule is one firewall rule inside a ruleset.
type FwRule struct {
	Dest      string
	Direction string
	Action    string
	Proto     string
	Source    string
	Sport     string
	Dport     string
	Iface     string
	Macro     string
	Log       string
	Comment   string
	Pos       int
	Enabled   bool
}

func (r FwRule) canonical() string {
	return fmt.Sprintf("%d|%v|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s",
		r.Pos, r.Enabled, r.Direction, r.Action, r.Proto, r.Source, r.Dest,
		r.Sport, r.Dport, r.Iface, r.Macro, r.Log, r.Comment)
}

// FwScope names a firewall ruleset scope.
type FwScope string

const (
	FwScopeCluster FwScope = "cluster"
	FwScopeNode    FwScope = "node"
	FwScopeGuest   FwScope = "guest"
	// FwScopeVNet (T-3103) is PVE 9.2's fourth firewall scope:
	// /cluster/sdn/vnets/{vnet}/firewall/{options,rules}, a forward-chain
	// ruleset governing traffic routed through an SDN vnet's L3 gateway.
	// Hardware-captured (planning/reports/evidence/pve-9.2.4-sdn-schema.txt,
	// "### ls /cluster/sdn/vnets/labnet/firewall"): unlike cluster/node/
	// guest scope, a vnet ruleset exposes only rules+options — real PVE has
	// no aliases/ipset endpoint under this prefix at all (mirrors the
	// node-scope gap FwRuleset's own doc comment already documents, one
	// step further) — and its options carry only enable/policy_forward/
	// log_level_forward, never policy_in/policy_out (see FwRuleset's
	// DefaultForward/LogLevelForward doc comments).
	FwScopeVNet FwScope = "vnet"
)

// FwAlias is a named IP/CIDR alias defined within a ruleset's scope. Per
// real pve-firewall semantics (docs/features/firewall.md §1/§2), a
// cluster-scope alias is visible from every scope (referenced by bare
// name from node or guest rules too); a node- or guest-scope alias is only
// visible within the ruleset that defines it. internal/fw's resolver
// applies this visibility rule when expanding rule references and
// counting object usage.
type FwAlias struct {
	Name    string
	CIDR    string
	Comment string
}

func (a FwAlias) canonical() string {
	return a.Name + "|" + a.CIDR + "|" + a.Comment
}

// FwIPSetEntry is one member of an FwIPSet.
type FwIPSetEntry struct {
	CIDR    string
	Comment string
	NoMatch bool
}

func (e FwIPSetEntry) canonical() string {
	return fmt.Sprintf("%s|%v|%s", e.CIDR, e.NoMatch, e.Comment)
}

// FwIPSet is a named set of CIDR entries, with the same scope-visibility
// rule as FwAlias (referenced in rule source/dest fields with a leading
// "+", e.g. "+blocklist").
type FwIPSet struct {
	Name    string
	Comment string
	Entries []FwIPSetEntry
}

func (s FwIPSet) canonical() string {
	entries := make([]string, len(s.Entries))
	for i, e := range s.Entries {
		entries[i] = e.canonical()
	}
	sort.Strings(entries)
	return s.Name + "|" + s.Comment + "|" + strings.Join(entries, ",")
}

// FwGroup is a reusable, cluster-scope-only security group of rules,
// referenced from any ruleset's own rule list via a rule whose Direction
// is "group" and whose Action names the group (see FwRule.Direction and
// internal/fw's resolver for expansion semantics). Real PVE only exposes
// security groups at the cluster level (pvemock mounts the group CRUD
// routes once, at /cluster/firewall/groups, never per node/guest), so
// Groups is only ever populated on the cluster-scope FwRuleset.
type FwGroup struct {
	Name    string
	Comment string
	Rules   []FwRule
}

func (g FwGroup) canonical() string {
	rules := make([]string, len(g.Rules))
	for i, r := range g.Rules {
		rules[i] = r.canonical()
	}
	return g.Name + "|" + g.Comment + "|" + strings.Join(rules, "\n")
}

// FwRuleset is a firewall ruleset at cluster, node, or guest scope. Rules
// are ordered by Pos; order is significant, so it is not sorted for
// canonicalization. Aliases/IPSets/Groups are the scope's own object
// definitions (docs/features/firewall.md §2's alias/ipset/security-group
// editors); Groups is populated only for FwScopeCluster (see FwGroup).
type FwRuleset struct {
	Ref
	rawSrc
	Scope      FwScope
	DefaultIn  string
	DefaultOut string
	// DefaultForward is policy_forward (T-3103): the forward chain's
	// fallthrough policy, hardware-captured at cluster and vnet scope
	// (planning/reports/evidence/pve-9.2.4-sdn-schema.txt's "--policy_forward
	// <ACCEPT | DROP>" — note REJECT is not a valid forward policy, unlike
	// DefaultIn/DefaultOut's ACCEPT|DROP|REJECT). Node scope is assumed
	// symmetric with cluster (same shared PVE options schema every scope
	// already uses for policy_in/policy_out) but was not itself captured;
	// empty at guest scope, which has no forward chain.
	DefaultForward string
	// LogLevelForward is log_level_forward (T-3103): only hardware-captured
	// at vnet scope (the same evidence file's cluster/firewall/options
	// excerpt shows policy_forward but never independently matches
	// log_level_forward the way the vnet section does) — left empty and
	// never written at cluster/node/guest scope until that is captured too.
	// See internal/change's schemaFwOptionsForScope for the write-side
	// guard this asymmetry drives.
	LogLevelForward string
	Rules           []FwRule
	Aliases         []FwAlias
	IPSets          []FwIPSet
	Groups          []FwGroup
	Enabled         bool
}

func (f *FwRuleset) GetRef() Ref { return f.Ref }
func (f *FwRuleset) clone() Entity {
	cp := *f
	cp.Rules = append([]FwRule(nil), f.Rules...)
	cp.Aliases = append([]FwAlias(nil), f.Aliases...)
	cp.IPSets = make([]FwIPSet, len(f.IPSets))
	for i, s := range f.IPSets {
		cp.IPSets[i] = FwIPSet{Name: s.Name, Comment: s.Comment, Entries: append([]FwIPSetEntry(nil), s.Entries...)}
	}
	cp.Groups = make([]FwGroup, len(f.Groups))
	for i, g := range f.Groups {
		cp.Groups[i] = FwGroup{Name: g.Name, Comment: g.Comment, Rules: append([]FwRule(nil), g.Rules...)}
	}
	return &cp
}
func (f *FwRuleset) fieldMap() map[string]string {
	rules := make([]string, len(f.Rules))
	for i, r := range f.Rules {
		rules[i] = r.canonical()
	}
	aliases := make([]string, len(f.Aliases))
	for i, a := range f.Aliases {
		aliases[i] = a.canonical()
	}
	sort.Strings(aliases)
	ipsets := make([]string, len(f.IPSets))
	for i, s := range f.IPSets {
		ipsets[i] = s.canonical()
	}
	sort.Strings(ipsets)
	groups := make([]string, len(f.Groups))
	for i, g := range f.Groups {
		groups[i] = g.canonical()
	}
	sort.Strings(groups)
	return map[string]string{
		"scope": string(f.Scope), "enabled": boolStr(f.Enabled),
		"defaultIn": f.DefaultIn, "defaultOut": f.DefaultOut,
		"defaultForward": f.DefaultForward, "logLevelForward": f.LogLevelForward,
		"rules":   strings.Join(rules, "\n"),
		"aliases": strings.Join(aliases, "\n"), "ipsets": strings.Join(ipsets, "\n"),
		"groups": strings.Join(groups, "\n"),
	}
}
