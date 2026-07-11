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
	Name        string
	Mac         string
	Driver      string
	PCIAddr     string
	Duplex      string
	OperState   string
	SpeedMbps   int
	MTU         int // runtime (host-netlink authoritative)
	MTUDeclared int // intended (host-interfaces authoritative, pve-network cross-check)
	SRIOVVFs    int
	LinkUp      bool
	// LinkUpSet reports whether the contributing source actually observed
	// LinkUp (merge treats an unset flagged bool as "not reported", not as
	// an implicit false). On a resolved entity it is true iff any source in
	// the field's precedence list reported the field.
	LinkUpSet bool
}

func (p *PhysNic) GetRef() Ref { return p.Ref }
func (p *PhysNic) clone() Entity {
	cp := *p
	return &cp
}
func (p *PhysNic) fieldMap() map[string]string {
	return map[string]string{
		"name": p.Name, "mac": p.Mac, "driver": p.Driver, "pciAddr": p.PCIAddr,
		"duplex": p.Duplex, "operState": p.OperState,
		"speedMbps": strconv.Itoa(p.SpeedMbps), "mtu": strconv.Itoa(p.MTU),
		"mtuDeclared": strconv.Itoa(p.MTUDeclared), "sriovVFs": strconv.Itoa(p.SRIOVVFs),
		"linkUp": boolStr(p.LinkUp),
	}
}

// BondSlaveState is one slave's runtime status inside a bond.
type BondSlaveState struct {
	Name             string
	MIIStatus        string
	PermHWAddr       string
	LinkFailureCount int
	Active           bool
}

// Bond is a Linux bond (or OVS bond when Ref.Kind == KindOVSBond). Mode /
// per-slave status / active slave / runtime Slaves come from host-netlink
// (/proc/net/bonding); DeclaredSlaves and the declared mode come from the
// interfaces file.
type Bond struct {
	Ref
	rawSrc
	Name           string
	Mode           string
	LACPRate       string
	XmitHashPolicy string
	MIIStatus      string
	ActiveSlave    string
	Slaves         []string // runtime membership (host-netlink)
	DeclaredSlaves []string // configured membership (host-interfaces / pve-network)
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
		sd[i] = fmt.Sprintf("%s/%s/%v", s.Name, s.MIIStatus, s.Active)
	}
	sort.Strings(sd)
	return map[string]string{
		"name": b.Name, "mode": b.Mode, "lacpRate": b.LACPRate,
		"xmitHashPolicy": b.XmitHashPolicy, "miiStatus": b.MIIStatus,
		"activeSlave": b.ActiveSlave, "slaves": sortedJoin(b.Slaves),
		"declaredSlaves": sortedJoin(b.DeclaredSlaves), "slaveDetail": strings.Join(sd, ";"),
		"mtu": strconv.Itoa(b.MTU), "mtuDeclared": strconv.Itoa(b.MTUDeclared),
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
	Ports             []Ref
	Vids              []VidRange
	Addresses         []string
	DeclaredPortNames []string
	PortNames         []string
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
		"stp": boolStr(b.STP),
	}
}

// VlanIface is a VLAN sub-interface (e.g. eno1.100 or vmbr0.20). Parent is
// resolved during linking from ParentName; Vid/addresses are declared, MTU
// runtime.
type VlanIface struct {
	Ref
	rawSrc
	Name        string
	Parent      Ref // resolved during linking from ParentName
	ParentName  string
	Addresses   []string
	Vid         int
	MTU         int
	MTUDeclared int
}

func (v *VlanIface) GetRef() Ref { return v.Ref }
func (v *VlanIface) clone() Entity {
	cp := *v
	cp.Addresses = append([]string(nil), v.Addresses...)
	return &cp
}
func (v *VlanIface) fieldMap() map[string]string {
	return map[string]string{
		"name": v.Name, "parent": v.Parent.String(), "parentName": v.ParentName,
		"addresses": sortedJoin(v.Addresses), "vid": strconv.Itoa(v.Vid),
		"mtu": strconv.Itoa(v.MTU), "mtuDeclared": strconv.Itoa(v.MTUDeclared),
	}
}

// --- SDN entities (single-source: pve-sdn) -------------------------------

// SdnZone is a cluster-scoped SDN zone. NodeStatus records per-node
// realization status from GET /cluster/sdn/zones/{zone}/status.
type SdnZone struct {
	NodeStatus map[string]string
	Ref
	rawSrc
	ID         string
	Type       string
	Bridge     string
	Controller string
	IPAM       string
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
	}
}

// SdnVnet is a cluster-scoped VNet inside a zone.
type SdnVnet struct {
	Ref
	rawSrc
	ID        string
	Zone      string
	Alias     string
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
	}
}

// SdnSubnet is a cluster-scoped subnet inside a VNet. ID is the CIDR.
type SdnSubnet struct {
	Ref
	rawSrc
	ID            string
	Vnet          string
	Gateway       string
	DNSZonePrefix string
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
		"snat": boolStr(s.SNAT),
	}
}

// --- guests (single-source: pve-guest) -----------------------------------

// Guest is a qemu VM or lxc container.
type Guest struct {
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
	return &cp
}
func (g *Guest) fieldMap() map[string]string {
	return map[string]string{
		"vmid": strconv.Itoa(g.VMID), "name": g.Name, "type": g.Type,
		"node": g.Node, "status": g.Status,
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
)

// FwRuleset is a firewall ruleset at cluster, node, or guest scope. Rules
// are ordered by Pos; order is significant, so it is not sorted for
// canonicalization.
type FwRuleset struct {
	Ref
	rawSrc
	Scope      FwScope
	DefaultIn  string
	DefaultOut string
	Rules      []FwRule
	Enabled    bool
}

func (f *FwRuleset) GetRef() Ref { return f.Ref }
func (f *FwRuleset) clone() Entity {
	cp := *f
	cp.Rules = append([]FwRule(nil), f.Rules...)
	return &cp
}
func (f *FwRuleset) fieldMap() map[string]string {
	rules := make([]string, len(f.Rules))
	for i, r := range f.Rules {
		rules[i] = r.canonical()
	}
	return map[string]string{
		"scope": string(f.Scope), "enabled": boolStr(f.Enabled),
		"defaultIn": f.DefaultIn, "defaultOut": f.DefaultOut,
		"rules": strings.Join(rules, "\n"),
	}
}
