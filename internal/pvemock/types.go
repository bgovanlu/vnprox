package pvemock

import "encoding/json"

// Fixture is the top-level shape of a YAML cluster fixture under
// testdata/clusters/. It is the single source of truth the mock server is
// built from: the PVE API surface, the host.Reader fixture backing, and the
// permission model are all derived from one Fixture value.
type Fixture struct {
	Nodes    map[string]*NodeSpec `yaml:"nodes"`
	SDN      SDNSpec              `yaml:"sdn"`
	Users    []UserSpec           `yaml:"users"`
	Mess     []string             `yaml:"mess"`
	Firewall FirewallSpec         `yaml:"firewall"`
	Cluster  ClusterSpec          `yaml:"cluster"`
	Mock     MockOptions          `yaml:"mock"`
}

// ClusterSpec describes cluster membership as reported by GET /cluster/status.
type ClusterSpec struct {
	Name    string            `yaml:"name"`
	Nodes   []ClusterNodeSpec `yaml:"nodes"`
	Quorate bool              `yaml:"quorate"`
}

// ClusterNodeSpec is one member node.
type ClusterNodeSpec struct {
	Name   string `yaml:"name" json:"name"`
	IP     string `yaml:"ip" json:"ip"`
	Online bool   `yaml:"online" json:"online"`
}

// UserSpec is a fixture-defined PVE user with a plaintext password (fixture
// only — never a real auth model) and a PVE privilege set. "*" grants every
// privilege the mock understands.
//
// TOTP, when non-empty, marks the user as requiring a second factor: the
// mock's POST /access/ticket rejects a login for this user unless the
// request's "otp" field equals this exact static code. Real PVE uses a
// time-based code (and, on newer versions, a two-step ticket-challenge
// flow); a static expected code is deliberately all the mock implements —
// enough to integration-test the client-side OTP passthrough.
type UserSpec struct {
	UserID     string      `yaml:"userid"` // e.g. "root@pam"
	Password   string      `yaml:"password"`
	TOTP       string      `yaml:"totp,omitempty"`
	Privileges []string    `yaml:"privileges"`
	Tokens     []TokenSpec `yaml:"tokens,omitempty"`
}

// TokenSpec is a fixture-defined PVE API token owned by a UserSpec,
// authenticating via "Authorization: PVEAPIToken=user@realm!tokenid=secret".
// The token carries the owning user's full privilege set: real PVE's
// privilege separation ("privsep", where a token can hold a *subset* of its
// owner's privileges) is intentionally out of scope for the mock — vnprox's
// documented daemon token (vnprox@pve!daemon, docs/security.md) is created
// with exactly the privileges it needs, so owner-equals-token is a faithful
// enough model for testing.
type TokenSpec struct {
	TokenID string `yaml:"tokenid"` // e.g. "daemon"
	Secret  string `yaml:"secret"`  // the UUID-ish value after "="
}

// HasPrivilege reports whether the user holds priv, honoring the "*"
// wildcard.
func (u UserSpec) HasPrivilege(priv string) bool {
	for _, p := range u.Privileges {
		if p == "*" || p == priv {
			return true
		}
	}
	return false
}

// NodeSpec is per-node state: network topology, host-level metadata, guests,
// and node-scope firewall.
type NodeSpec struct {
	Links map[string]LinkInfo     `yaml:"links"`
	LLDP  map[string]LLDPNeighbor `yaml:"lldp"`
	Stats map[string]IfaceStats   `yaml:"stats"`
	// Services is T-602's fixture-declared systemd unit status
	// (host.WatchedServices' keys: "dnsmasq", "frr") for this node's
	// FixtureHostReader.Services. A unit omitted from this map (including
	// when the whole map/key is unset — the common case for a fixture that
	// doesn't care about this check) defaults to active=true: most fixture
	// nodes should read as healthy unless a test deliberately declares
	// otherwise, mirroring how Stats/Links default to "unremarkable" absent
	// an explicit override.
	Services map[string]bool       `yaml:"services,omitempty"`
	Qemu     map[string]*GuestSpec `yaml:"qemu"`
	Lxc      map[string]*GuestSpec `yaml:"lxc"`
	Firewall *FirewallScope        `yaml:"firewall"`
	Mock     *MockOptions          `yaml:"mock"`
	// FRR is this node's fixture-declared FRR/BGP EVPN daemon state
	// (T-404, docs/features/sdn.md §3). Nil models a node with no FRR
	// installed/running at all — this package's HostReader.FRRBGPSummary/
	// FRREVPNVNI return ErrFRRUnavailable for such a node, so the
	// aggregation layer can report a clean per-node "no EVPN" rather than
	// treating it as an error (T-404 AC2).
	FRR *FRRSpec `yaml:"frr,omitempty"`
	// DHCPLeases is this node's fixture-declared raw dnsmasq DHCP
	// lease-file content (T-406, docs/features/sdn.md §5) — a YAML block
	// scalar in the standard dnsmasq .leases line format
	// ("<expiry> <mac> <ip> <hostname|*> <client-id|*>", one lease per
	// line), rendered verbatim by FixtureHostReader.DHCPLeases so a
	// fixture can include deliberately malformed lines to exercise
	// host.ParseDHCPLeases' defensive skip-and-count behavior. Empty
	// string (the default) models a node with no DHCP-managed SDN zone at
	// all — a clean "no leases" result, not an error.
	DHCPLeases string `yaml:"dhcp_leases,omitempty"`
	// Neighbors is this node's fixture-declared ARP/IPv6-neighbor table
	// (T-805, docs/features/ipam.md §1's ARP/neighbor enrichment source)
	// for this node's FixtureHostReader.Neighbors. Declared as already
	// structured {ip, mac, iface, state} entries (unlike DHCPLeases' raw
	// text blob above) since there is no single stable raw-text format
	// spanning both /proc/net/arp (IPv4) and the IPv6 neighbor table the
	// way dnsmasq's .leases format covers every lease — see
	// internal/host.ParseProcNetARP for the real IPv4 parser this
	// structured shape deliberately bypasses. Every declared entry is
	// returned unfiltered by FixtureHostReader.Neighbors below;
	// internal/host.FixtureReader.Neighbors applies the
	// resolved-states-only filter, exactly like the real Reader
	// implementation does at its own layer — so a fixture can declare a
	// FAILED/INCOMPLETE entry to exercise that filtering end-to-end.
	Neighbors []NeighborSpec `yaml:"neighbors,omitempty"`
	// Corosync is this node's fixture-declared corosync ring *status*
	// (T-803, docs/features/monitoring.md §5's `corosync_link_degraded`
	// check) — distinct from corosync.conf's static ring *addresses*
	// (ClusterSpec/internal/host.ParseCorosyncConf): this models
	// `corosync-cfgtool -s`'s live per-ring health as corosync's own
	// knet/totem layer currently reports it. Nil models a node running no
	// corosync at all (e.g. a single, not-yet-clustered node) — this
	// package's HostReader.CorosyncStatus returns ErrCorosyncUnavailable
	// for such a node, the same graceful-degradation convention FRR's
	// FRRSpec already establishes.
	Corosync *CorosyncSpec `yaml:"corosync,omitempty"`
	// Conntrack is this node's fixture-declared live conntrack/NAT table
	// (T-1305, docs/api.md's Conntrack section) for this node's
	// FixtureHostReader.Conntrack — already-structured entries (like
	// NeighborSpec above, not a raw-text blob) since a fixture only needs
	// to express the parsed shape the API/UI actually consume, not exercise
	// internal/host.ParseConntrackTable's own procfs-text parsing (that
	// parser has its own golden-fixture table tests, internal/host/
	// conntrack_test.go).
	Conntrack []ConntrackEntrySpec `yaml:"conntrack,omitempty"`
	// IPv6RA is this node's fixture-declared per-interface IPv6 Router
	// Advertisement / DHCPv6 observation (T-1404, docs/features/sdn.md §6)
	// for this node's FixtureHostReader.IPv6RA — already-structured
	// entries (like ConntrackEntrySpec/NeighborSpec above), since a
	// fixture only needs to express the observed shape the API/UI
	// consume, not exercise internal/host's own rdisc6-text parsing (that
	// parser has its own table tests). An interface absent from this list
	// models "no RA observed on this segment" — the common, unremarkable
	// case, not an error.
	IPv6RA         []IPv6RASpec `yaml:"ipv6_ra,omitempty"`
	Network        []NetIface   `yaml:"network"`
	NetworkPending []NetIface   `yaml:"network_pending"`
}

// IPv6RASpec is one fixture-declared interface's IPv6 RA/DHCPv6 observation
// (T-1404). DHCPv6ServerPresent, when true, always implies "inferred from
// the M-flag" in the rendered host.IPv6RAObservation (mirroring Real's own
// documented inference limitation — see host.IPv6RAObservation's doc
// comment) unless a fixture wants to model a directly-confirmed DHCPv6
// server instead, which this task does not need: no fixture scenario in
// this codebase's testdata distinguishes the two.
type IPv6RASpec struct {
	Iface               string   `yaml:"iface"`
	Prefixes            []string `yaml:"prefixes,omitempty"`
	RouterLifetimeSec   int      `yaml:"router_lifetime_sec,omitempty"`
	ManagedFlag         bool     `yaml:"managed_flag,omitempty"`
	OtherFlag           bool     `yaml:"other_flag,omitempty"`
	DHCPv6ServerPresent bool     `yaml:"dhcpv6_server_present,omitempty"`
}

// ConntrackEntrySpec is one fixture-declared live conntrack table entry
// (T-1305). NatSrc/NatDst are nil for an untranslated connection; State
// empty defaults to "" (unlike NeighborSpec's "REACHABLE" default, a
// conntrack entry genuinely can have no textual state — e.g. a
// once-replied UDP/ICMP flow the real kernel format also reports with no
// state word, see internal/host.ParseConntrackTable's doc comment) — a
// fixture author states exactly the state it wants shown, including empty.
type ConntrackEntrySpec struct {
	NatSrc     *NatAddrSpec `yaml:"nat_src,omitempty" json:"natSrc,omitempty"`
	NatDst     *NatAddrSpec `yaml:"nat_dst,omitempty" json:"natDst,omitempty"`
	SrcIP      string       `yaml:"src_ip" json:"srcIp"`
	DstIP      string       `yaml:"dst_ip" json:"dstIp"`
	State      string       `yaml:"state,omitempty" json:"state,omitempty"`
	Proto      int          `yaml:"proto" json:"proto"`
	SrcPort    int          `yaml:"src_port,omitempty" json:"srcPort,omitempty"`
	DstPort    int          `yaml:"dst_port,omitempty" json:"dstPort,omitempty"`
	TimeoutSec int          `yaml:"timeout_sec,omitempty" json:"timeoutSec,omitempty"`
}

// NatAddrSpec is one fixture-declared NAT-translated endpoint.
type NatAddrSpec struct {
	IP   string `yaml:"ip" json:"ip"`
	Port int    `yaml:"port,omitempty" json:"port,omitempty"`
}

// CorosyncSpec is a node's fixture-declared `corosync-cfgtool -s` ring
// status (T-803).
type CorosyncSpec struct {
	Rings []RingSpec `yaml:"rings"`
}

// RingSpec is one fixture-declared corosync ring's live status. Faulty
// models corosync-cfgtool reporting anything other than "active with no
// faults" for this ring (the exact wording varies by corosync
// version/transport — see planning/reports/needs-hardware-validation.md);
// StatusText, when set, overrides the rendered status line verbatim (so a
// fixture can exercise a specific real-world wording), defaulting to a
// representative healthy/faulty line derived from Faulty when empty.
type RingSpec struct {
	Addr       string `yaml:"addr"`
	StatusText string `yaml:"status_text,omitempty"`
	ID         int    `yaml:"id"`
	Faulty     bool   `yaml:"faulty,omitempty"`
}

// NeighborSpec is one fixture-declared ARP/IPv6-neighbor-table entry
// (T-805). State is one of "REACHABLE"/"STALE"/"PERMANENT"/"FAILED"/
// "INCOMPLETE" (internal/host's NeighborReachable/.../NeighborIncomplete
// constants, duplicated here as plain strings for the same
// host-package-free reason FRRSpec.Peers' State field is a raw string —
// see BGPPeerSpec's doc comment); empty defaults to "REACHABLE" (the
// "unremarkable unless declared otherwise" default every other optional
// NodeSpec field already follows).
type NeighborSpec struct {
	IP    string `yaml:"ip" json:"ip"`
	Mac   string `yaml:"mac,omitempty" json:"mac,omitempty"`
	Iface string `yaml:"iface,omitempty" json:"iface,omitempty"`
	State string `yaml:"state,omitempty" json:"state,omitempty"`
}

// FRRSpec is a node's fixture-declared FRR daemon state: its own BGP
// identity plus every configured peer session and EVPN VNI.
type FRRSpec struct {
	RouterID string        `yaml:"router_id"`
	Peers    []BGPPeerSpec `yaml:"peers"`
	VNIs     []EVPNVniSpec `yaml:"vnis"`
	ASN      int           `yaml:"asn"`
}

// BGPPeerSpec is one fixture-declared BGP neighbor session. State is a raw
// FRR FSM state string, matching real vtysh vocabulary exactly (e.g.
// "Established", "Active", "Idle", or "Idle (Admin)"/"Idle (PfxCt)" with a
// parenthetical reason — internal/host.ParseBGPSummary splits the reason
// out) — this lets fixtures model FRR's own "last error" signal without
// this package inventing a separate field for it.
type BGPPeerSpec struct {
	Addr          string `yaml:"addr"`
	Hostname      string `yaml:"hostname,omitempty"`
	State         string `yaml:"state"`
	AddressFamily string `yaml:"address_family,omitempty"` // "" defaults to "l2VpnEvpn"
	PeerUptime    string `yaml:"peer_uptime,omitempty"`    // e.g. "01:23:45", "never"
	RemoteAS      int    `yaml:"remote_as"`
	PfxRcd        int    `yaml:"pfx_rcd,omitempty"`
	PfxSnt        int    `yaml:"pfx_snt,omitempty"`
}

// EVPNVniSpec is one fixture-declared EVPN VNI (L2 tenant bridge domain or
// L3 tenant VRF).
type EVPNVniSpec struct {
	Type      string `yaml:"type"` // "L2" | "L3"
	VxlanIf   string `yaml:"vxlan_if,omitempty"`
	TenantVRF string `yaml:"tenant_vrf,omitempty"`
	VNI       int    `yaml:"vni"`
	NumMacs   int    `yaml:"num_macs,omitempty"`
	NumArpND  int    `yaml:"num_arp_nd,omitempty"`
}

// NetIface is one stanza of /etc/network/interfaces, matching the field
// names PVE's own `GET/PUT /nodes/{node}/network` API uses. It is
// marshaled directly as the JSON body of network API responses.
type NetIface struct {
	BondMode        string       `yaml:"bond_mode,omitempty" json:"bond_mode,omitempty"`
	Type            string       `yaml:"type" json:"type"`
	Method          string       `yaml:"method,omitempty" json:"method,omitempty"`
	Address         string       `yaml:"address,omitempty" json:"address,omitempty"`
	Gateway         string       `yaml:"gateway,omitempty" json:"gateway,omitempty"`
	Pending         PendingState `yaml:"-" json:"pending,omitempty"`
	Iface           string       `yaml:"iface" json:"iface"`
	Comments        string       `yaml:"comments,omitempty" json:"comments,omitempty"`
	BridgePorts     string       `yaml:"bridge_ports,omitempty" json:"bridge_ports,omitempty"`
	VlanRawDevice   string       `yaml:"vlan_raw_device,omitempty" json:"vlan_raw_device,omitempty"`
	Slaves          string       `yaml:"slaves,omitempty" json:"slaves,omitempty"`
	MTU             int          `yaml:"mtu,omitempty" json:"mtu,omitempty"`
	VlanID          int          `yaml:"vlan_id,omitempty" json:"vlan_id,omitempty"`
	BridgeVlanAware bool         `yaml:"bridge_vlan_aware,omitempty" json:"-"`
	Autostart       bool         `yaml:"autostart" json:"-"`
}

// netIfaceWire is NetIface's actual JSON wire shape: hardware validation
// against a real PVE 9.2.4 node found GET /nodes/{node}/network reports
// autostart/bridge_vlan_aware as 0/1 ints, not JSON booleans (see
// internal/pve/types.go's networkInterfaceWire, the client-side
// counterpart of this same fix). NetIface itself keeps plain Go bools for
// every internal use (YAML fixtures, applyNetIfaceField, render.go) — only
// MarshalJSON below needs to know about the wire quirk.
type netIfaceWire struct {
	BondMode        string       `json:"bond_mode,omitempty"`
	Type            string       `json:"type"`
	Method          string       `json:"method,omitempty"`
	Address         string       `json:"address,omitempty"`
	Gateway         string       `json:"gateway,omitempty"`
	Pending         PendingState `json:"pending,omitempty"`
	Iface           string       `json:"iface"`
	Comments        string       `json:"comments,omitempty"`
	BridgePorts     string       `json:"bridge_ports,omitempty"`
	VlanRawDevice   string       `json:"vlan_raw_device,omitempty"`
	Slaves          string       `json:"slaves,omitempty"`
	MTU             int          `json:"mtu,omitempty"`
	VlanID          int          `json:"vlan_id,omitempty"`
	BridgeVlanAware int          `json:"bridge_vlan_aware,omitempty"`
	Autostart       int          `json:"autostart"`
}

// MarshalJSON implements json.Marshaler, emitting autostart/
// bridge_vlan_aware as 0/1 ints per netIfaceWire's doc comment.
func (i NetIface) MarshalJSON() ([]byte, error) {
	w := netIfaceWire{
		BondMode: i.BondMode, Type: i.Type, Method: i.Method, Address: i.Address,
		Gateway: i.Gateway, Pending: i.Pending, Iface: i.Iface, Comments: i.Comments,
		BridgePorts: i.BridgePorts, VlanRawDevice: i.VlanRawDevice, Slaves: i.Slaves,
		MTU: i.MTU, VlanID: i.VlanID,
	}
	if i.BridgeVlanAware {
		w.BridgeVlanAware = 1
	}
	if i.Autostart {
		w.Autostart = 1
	}
	return json.Marshal(w)
}

// LinkInfo is netlink-equivalent physical/virtual link state for one iface.
type LinkInfo struct {
	Mac     string         `yaml:"mac" json:"mac"`
	Driver  string         `yaml:"driver,omitempty" json:"driver,omitempty"`
	Duplex  string         `yaml:"duplex,omitempty" json:"duplex,omitempty"`
	PCIAddr string         `yaml:"pci_addr,omitempty" json:"pci_addr,omitempty"`
	Members []string       `yaml:"members,omitempty" json:"members,omitempty"`
	FDB     []FDBEntrySpec `yaml:"fdb,omitempty" json:"fdb,omitempty"`
	// VFs (T-1506) is this (physical-kind) link's fixture-declared SR-IOV
	// virtual functions — nil for every non-physical link, same convention
	// as FDB above (bridge-only).
	VFs       []VFEntrySpec `yaml:"vfs,omitempty" json:"vfs,omitempty"`
	SpeedMbps int           `yaml:"speed_mbps,omitempty" json:"speed_mbps,omitempty"`
	MTU       int           `yaml:"mtu,omitempty" json:"mtu,omitempty"`
	LinkUp    bool          `yaml:"link_up" json:"link_up"`
}

// FDBEntrySpec is one fixture-declared bridge forwarding-database entry: a
// MAC learned on a given port/VLAN, optionally flagged permanent (static)
// or stale (aged past the bridge's learning timer without fresh traffic —
// T-306's staleness signal).
type FDBEntrySpec struct {
	Mac       string `yaml:"mac" json:"mac"`
	Port      string `yaml:"port,omitempty" json:"port,omitempty"`
	Vlan      int    `yaml:"vlan,omitempty" json:"vlan,omitempty"`
	Master    bool   `yaml:"master,omitempty" json:"master,omitempty"`
	Permanent bool   `yaml:"permanent,omitempty" json:"permanent,omitempty"`
	Stale     bool   `yaml:"stale,omitempty" json:"stale,omitempty"`
}

// VFEntrySpec is one fixture-declared SR-IOV virtual function on a PF link
// (T-1506): id (the VF's index on its PF, "vf N" in `ip link show`), an
// optionally-assigned MAC/VLAN, and its spoof-check/trust bits — the same
// fields internal/host.VF (a real netlink read) and internal/inventory.
// VirtualFunction (the resolved inventory projection) carry.
type VFEntrySpec struct {
	Mac        string `yaml:"mac,omitempty" json:"mac,omitempty"`
	PCIAddr    string `yaml:"pci_addr,omitempty" json:"pci_addr,omitempty"`
	ID         int    `yaml:"id" json:"id"`
	Vlan       int    `yaml:"vlan,omitempty" json:"vlan,omitempty"`
	SpoofCheck bool   `yaml:"spoof_check,omitempty" json:"spoof_check,omitempty"`
	Trust      bool   `yaml:"trust,omitempty" json:"trust,omitempty"`
}

// LLDPNeighbor is one LLDP-discovered neighbor on a local iface.
//
// TaggedVLANs (T-403) is the switch port's advertised *trunked* VLAN IDs,
// distinct from VLAN (the port's untagged/native PVID) — the VLAN zone
// wizard's LLDP trunk cross-check (docs/features/sdn.md §2) needs this to
// tell "port trunks VID 200" apart from "port's native VLAN happens to be
// 200". Added as its own optional fixture field rather than overloading
// VLAN, since real lldpd reports both independently (see
// internal/host/lldp.go's flexVlans, which already parses both from real
// `lldpctl -f json` output — only this package's flat fixture shape was
// missing the tagged half).
type LLDPNeighbor struct {
	ChassisName string `yaml:"chassis_name" json:"chassis_name"`
	ChassisID   string `yaml:"chassis_id" json:"chassis_id"`
	PortID      string `yaml:"port_id" json:"port_id"`
	PortDescr   string `yaml:"port_descr,omitempty" json:"port_descr,omitempty"`
	MgmtIP      string `yaml:"mgmt_ip,omitempty" json:"mgmt_ip,omitempty"`
	TaggedVLANs []int  `yaml:"tagged_vlans,omitempty" json:"tagged_vlans,omitempty"`
	VLAN        int    `yaml:"vlan,omitempty" json:"vlan,omitempty"`
	TTL         int    `yaml:"ttl,omitempty" json:"ttl,omitempty"`
}

// IfaceStats is a counters snapshot for one iface.
type IfaceStats struct {
	RxBytes   uint64 `yaml:"rx_bytes" json:"rx_bytes"`
	TxBytes   uint64 `yaml:"tx_bytes" json:"tx_bytes"`
	RxPackets uint64 `yaml:"rx_packets" json:"rx_packets"`
	TxPackets uint64 `yaml:"tx_packets" json:"tx_packets"`
	RxErrors  uint64 `yaml:"rx_errors" json:"rx_errors"`
	TxErrors  uint64 `yaml:"tx_errors" json:"tx_errors"`
	RxDropped uint64 `yaml:"rx_dropped" json:"rx_dropped"`
	TxDropped uint64 `yaml:"tx_dropped" json:"tx_dropped"`
}

// GuestSpec is a qemu or lxc guest. Config mirrors PVE's flat key/value
// guest config object (e.g. "net0": "virtio=AA:BB:...,bridge=vmbr0,tag=100").
// AgentInterfaces (T-405) is the fixture-declared response for
// GET .../agent/network-get-interfaces — a qemu guest with the QEMU guest
// agent installed and running reports its live in-guest network state this
// way, the enrichment source docs/features/ipam.md §1 calls "guest
// agent-reported IPs". A guest with no AgentInterfaces entries (no agent,
// or agent not running) simply returns an empty result — matching real
// PVE's behavior for an unreachable/absent agent on a stopped or
// agent-less guest, a normal state rather than a fixture bug.
type GuestSpec struct {
	Config   map[string]string `yaml:"config"`
	Firewall *FirewallScope    `yaml:"firewall"`
	Name     string            `yaml:"name"`
	Status   string            `yaml:"status"`

	// --- T-1304 guest-interior inspector fixture data -------------------
	//
	// Qemu guests: InteriorRoutesJSON/InteriorResolvConf/InteriorSockets
	// back three additional `POST .../agent/exec` command shapes
	// (parseInteriorCommand) beyond the ping/nc probe shapes
	// parseProbeCommand already recognizes — `ip -j route show`,
	// `cat /etc/resolv.conf`, `ss -H -tuln` — reusing the same guest-agent
	// exec/exec-status round trip AgentExecOutcomes mocks, rather than a
	// second generic command-matching table. Default-gateway reachability
	// for a qemu guest reuses AgentExecOutcomes itself (an icmp probe
	// tuple toward the claimed gateway IP): no separate field needed.
	//
	// Lxc guests (no QEMU guest agent — the interior view is read directly
	// from the host side instead): InteriorAddrJSON/InteriorRoutesJSON/
	// InteriorResolvConf/InteriorSockets back
	// FixtureHostReader.ContainerInterior directly (T-1304's LXC path);
	// InteriorPingOutcomes backs FixtureHostReader.ContainerPing's
	// default-gateway reachability check. Field names are shared between
	// the qemu and lxc cases (both are ultimately "this guest's own
	// interior read set"), scoped by which map (Qemu vs Lxc) the guest
	// lives in; InteriorAddrJSON/InteriorPingOutcomes are meaningful only
	// for an lxc guest (a qemu guest's addresses/gateway-reachability come
	// from AgentInterfaces/AgentExecOutcomes below instead).
	//
	// An unset (empty-string) field simply produces an empty parse result
	// for that piece — matching AgentInterfaces' own "unscripted is
	// silence, not failure" tolerance — never a synthesized error.
	InteriorAddrJSON   string `yaml:"interior_addr_json,omitempty"`
	InteriorRoutesJSON string `yaml:"interior_routes_json,omitempty"`
	InteriorResolvConf string `yaml:"interior_resolv_conf,omitempty"`
	InteriorSockets    string `yaml:"interior_sockets,omitempty"`

	// AgentInterfaces (T-405) is the fixture-declared response for
	// GET .../agent/network-get-interfaces — a qemu guest with the QEMU
	// guest agent installed and running reports its live in-guest network
	// state this way, the enrichment source docs/features/ipam.md §1 calls
	// "guest agent-reported IPs". A guest with no AgentInterfaces entries
	// (no agent, or agent not running) simply returns an empty result —
	// matching real PVE's behavior for an unreachable/absent agent on a
	// stopped or agent-less guest, a normal state rather than a fixture
	// bug.
	AgentInterfaces []AgentIfaceSpec `yaml:"agent_interfaces,omitempty"`

	// AgentExecOutcomes (T-802) is this qemu guest's fixture-scriptable
	// guest-agent exec outcome table, backing
	// `POST .../agent/exec` + `GET .../agent/exec-status`: a live probe
	// exec'd *from* this guest (internal/probe's Run) toward
	// (Proto,DstIP,Port) is matched against this list by exact tuple
	// equality (no wildcards — avoids ambiguity) and synthesizes an
	// exec-status result representative of Outcome, never running a real
	// command (see internal/pvemock's handleGuestAgentExec doc comment).
	// A tuple with no match resolves to Outcome "error" — unscripted is
	// "we don't know", not a guessed default, mirroring
	// internal/probe's own honesty contract.
	AgentExecOutcomes []AgentExecOutcomeSpec `yaml:"agent_exec_outcomes,omitempty"`

	// InteriorPingOutcomes (T-1304) is an lxc guest's fixture-scriptable
	// ContainerPing outcome table — see InteriorPingOutcomeSpec's own doc
	// comment.
	InteriorPingOutcomes []InteriorPingOutcomeSpec `yaml:"interior_ping_outcomes,omitempty"`

	// AgentUnreachable (T-802), when true, makes this guest's
	// `POST .../agent/exec` return the same 500 real PVE returns when the
	// QEMU guest agent isn't installed/running/reachable — the "could not
	// even attempt the probe" case internal/probe.Run's honesty contract
	// (OutcomeError) exists for. AgentExecOutcomes is not consulted when
	// this is true.
	AgentUnreachable bool `yaml:"agent_unreachable,omitempty"`
}

// InteriorPingOutcomeSpec is one scripted target-IP -> reachable entry in
// an lxc GuestSpec's InteriorPingOutcomes table (T-1304), backing
// FixtureHostReader.ContainerPing: matched by exact IP equality (same
// no-wildcards convention as AgentExecOutcomes). An IP with no match
// resolves to Reachable false — unscripted is "no reply", not a guessed
// default.
type InteriorPingOutcomeSpec struct {
	IP        string `yaml:"ip"`
	Reachable bool   `yaml:"reachable,omitempty"`
}

// AgentExecOutcomeSpec is one scripted (proto,dst,port) -> outcome entry in
// a GuestSpec's AgentExecOutcomes table (T-802). Outcome is one of
// internal/probe's Outcome values ("reachable"|"unreachable"|"timeout"|
// "error"); Detail (optional) becomes the synthesized exec's stdout/stderr
// text, matched back out by internal/probe's classify — mostly useful for
// exercising classify's "refused" text-sniffing branch from a fixture.
type AgentExecOutcomeSpec struct {
	Proto   string `yaml:"proto"`
	DstIP   string `yaml:"dst"`
	Outcome string `yaml:"outcome"`
	Detail  string `yaml:"detail,omitempty"`
	Port    int    `yaml:"port,omitempty"`
}

// AgentIfaceSpec is one NIC's entry in a qemu guest's
// network-get-interfaces guest-agent response.
type AgentIfaceSpec struct {
	Name         string               `yaml:"name" json:"name"`
	HardwareAddr string               `yaml:"hardware_address,omitempty" json:"hardware-address,omitempty"`
	IPAddresses  []AgentIPAddressSpec `yaml:"ip_addresses,omitempty" json:"ip-addresses,omitempty"`
}

// AgentIPAddressSpec is one address reported for an agent-interfaces NIC.
type AgentIPAddressSpec struct {
	IPAddress     string `yaml:"ip" json:"ip-address"`
	IPAddressType string `yaml:"type,omitempty" json:"ip-address-type,omitempty"` // ipv4|ipv6
	Prefix        int    `yaml:"prefix,omitempty" json:"prefix,omitempty"`
}

// SDNSpec is the cluster-wide SDN configuration tree.
type SDNSpec struct {
	Zones   []SDNZoneSpec   `yaml:"zones"`
	Vnets   []SDNVnetSpec   `yaml:"vnets"`
	Subnets []SDNSubnetSpec `yaml:"subnets"`
	Ipams   []SDNIpamSpec   `yaml:"ipams,omitempty"`
}

// SDNIpamSpec is one configured IPAM plugin instance, as listed by
// GET /cluster/sdn/ipams (real PVE ships a built-in "pve" IPAM and can be
// configured with external NetBox/phpIPAM plugins). Entries is the mock's
// backing data for GET /cluster/sdn/ipams/{ipam}/status — the current
// allocation set that endpoint reports.
type SDNIpamSpec struct {
	ID      string          `yaml:"id" json:"ipam"`
	Type    string          `yaml:"type" json:"type"` // pve|netbox|phpipam
	URL     string          `yaml:"url,omitempty" json:"url,omitempty"`
	Entries []IPAMEntrySpec `yaml:"entries" json:"-"`
}

// IPAMEntrySpec is one IPAM allocation row, as reported by
// GET /cluster/sdn/ipams/{ipam}/status.
type IPAMEntrySpec struct {
	Zone     string `yaml:"zone" json:"zone"`
	Vnet     string `yaml:"vnet" json:"vnet"`
	Subnet   string `yaml:"subnet" json:"subnet"` // CIDR, e.g. "10.100.0.0/24"
	IP       string `yaml:"ip" json:"ip"`
	MAC      string `yaml:"mac,omitempty" json:"mac,omitempty"`
	Hostname string `yaml:"hostname,omitempty" json:"hostname,omitempty"`
	VMID     int    `yaml:"vmid,omitempty" json:"vmid,omitempty"`
	Gateway  bool   `yaml:"gateway,omitempty" json:"-"` // rendered as 0/1 on the wire, see ipam.go
}

// PendingState mirrors the "state" PVE reports for SDN/network objects
// awaiting an apply/reload.
type PendingState string

const (
	// PendingNone means the object is applied; running config matches.
	PendingNone PendingState = ""
	// PendingNew means the object was created but never applied.
	PendingNew PendingState = "new"
	// PendingChanged means the object was edited but never applied.
	PendingChanged PendingState = "changed"
	// PendingDeleted means the object was deleted but the delete was
	// never applied.
	PendingDeleted PendingState = "deleted"
)

// SDNZoneSpec is one SDN zone.
type SDNZoneSpec struct {
	Running    *SDNZoneSpec `yaml:"running,omitempty" json:"-"`
	ID         string       `yaml:"id" json:"zone"`
	Type       string       `yaml:"type" json:"type"`
	Bridge     string       `yaml:"bridge,omitempty" json:"bridge,omitempty"`
	Controller string       `yaml:"controller,omitempty" json:"controller,omitempty"`
	Pending    PendingState `yaml:"pending,omitempty" json:"pending,omitempty"`
	Nodes      []string     `yaml:"nodes,omitempty" json:"nodes,omitempty"`
	ExitNodes  []string     `yaml:"exit_nodes,omitempty" json:"exit_nodes,omitempty"`
	Peers      []string     `yaml:"peers,omitempty" json:"peers,omitempty"`
	MTU        int          `yaml:"mtu,omitempty" json:"mtu,omitempty"`
	VrfVxlan   int          `yaml:"vrf_vxlan,omitempty" json:"vrf_vxlan,omitempty"`
}

// SDNVnetSpec is one VNet inside a zone.
type SDNVnetSpec struct {
	Running   *SDNVnetSpec `yaml:"running,omitempty" json:"-"`
	ID        string       `yaml:"id" json:"vnet"`
	Zone      string       `yaml:"zone" json:"zone"`
	Alias     string       `yaml:"alias,omitempty" json:"alias,omitempty"`
	Pending   PendingState `yaml:"pending,omitempty" json:"pending,omitempty"`
	Tag       int          `yaml:"tag,omitempty" json:"tag,omitempty"`
	VlanAware bool         `yaml:"vlan_aware,omitempty" json:"vlan_aware,omitempty"`
}

// SDNSubnetSpec is one subnet inside a VNet.
type SDNSubnetSpec struct {
	Running        *SDNSubnetSpec `yaml:"running,omitempty" json:"-"`
	ID             string         `yaml:"id" json:"subnet"`
	Vnet           string         `yaml:"vnet" json:"vnet"`
	CIDR           string         `yaml:"cidr" json:"cidr"`
	Gateway        string         `yaml:"gateway,omitempty" json:"gateway,omitempty"`
	DHCPRangeStart string         `yaml:"dhcp_range_start,omitempty" json:"dhcp_range_start,omitempty"`
	DHCPRangeEnd   string         `yaml:"dhcp_range_end,omitempty" json:"dhcp_range_end,omitempty"`
	Pending        PendingState   `yaml:"pending,omitempty" json:"pending,omitempty"`
	SNAT           bool           `yaml:"snat,omitempty" json:"snat,omitempty"`
}

// FirewallSpec is the cluster-scope firewall tree. Node-scope and
// guest-scope rulesets live on NodeSpec.Firewall / GuestSpec.Firewall.
type FirewallSpec struct {
	Cluster FirewallScope `yaml:"cluster"`
}

// FirewallScope is one ruleset at any of the three PVE firewall scopes
// (cluster/node/guest).
type FirewallScope struct {
	PolicyIn  string        `yaml:"policy_in,omitempty" json:"policy_in,omitempty"`
	PolicyOut string        `yaml:"policy_out,omitempty" json:"policy_out,omitempty"`
	Rules     []FwRuleSpec  `yaml:"rules" json:"-"`
	Aliases   []FwAliasSpec `yaml:"aliases" json:"-"`
	IPSets    []FwIPSetSpec `yaml:"ipsets" json:"-"`
	Groups    []FwGroupSpec `yaml:"groups" json:"-"`
	Enabled   bool          `yaml:"enabled" json:"enable"`
}

// FwRuleSpec is one firewall rule.
type FwRuleSpec struct {
	Dest    string `yaml:"dest,omitempty" json:"dest,omitempty"`
	Type    string `yaml:"type" json:"type"`
	Action  string `yaml:"action" json:"action"`
	Proto   string `yaml:"proto,omitempty" json:"proto,omitempty"`
	Source  string `yaml:"source,omitempty" json:"source,omitempty"`
	Sport   string `yaml:"sport,omitempty" json:"sport,omitempty"`
	Dport   string `yaml:"dport,omitempty" json:"dport,omitempty"`
	Iface   string `yaml:"iface,omitempty" json:"iface,omitempty"`
	Macro   string `yaml:"macro,omitempty" json:"macro,omitempty"`
	Log     string `yaml:"log,omitempty" json:"log,omitempty"`
	Comment string `yaml:"comment,omitempty" json:"comment,omitempty"`
	Pos     int    `yaml:"pos" json:"pos"`
	Enabled bool   `yaml:"enabled" json:"enable"`
}

// FwAliasSpec is a named IP/CIDR alias.
type FwAliasSpec struct {
	Name    string `yaml:"name" json:"name"`
	CIDR    string `yaml:"cidr" json:"cidr"`
	Comment string `yaml:"comment,omitempty" json:"comment,omitempty"`
}

// FwIPSetSpec is a named set of CIDR entries.
type FwIPSetSpec struct {
	Name    string         `yaml:"name" json:"name"`
	Comment string         `yaml:"comment,omitempty" json:"comment,omitempty"`
	Entries []FwIPSetEntry `yaml:"entries" json:"-"`
}

// FwIPSetEntry is one member of an IPSet.
type FwIPSetEntry struct {
	CIDR    string `yaml:"cidr" json:"cidr"`
	Comment string `yaml:"comment,omitempty" json:"comment,omitempty"`
	NoMatch bool   `yaml:"nomatch,omitempty" json:"nomatch,omitempty"`
}

// FwGroupSpec is a named, reusable security group of rules.
type FwGroupSpec struct {
	Name    string       `yaml:"name" json:"group"`
	Comment string       `yaml:"comment,omitempty" json:"comment,omitempty"`
	Rules   []FwRuleSpec `yaml:"rules" json:"-"`
}

// MockOptions configures task latency/failure injection. It can be set at
// fixture (global default), node, and per-request (query param) level; the
// most specific value wins.
type MockOptions struct {
	// TaskLatencyMS delays task completion (simulates slow ifreload/SDN
	// apply/etc.). Zero means "complete immediately".
	TaskLatencyMS int `yaml:"task_latency_ms,omitempty"`

	// TicketTTLMS, when non-zero, makes tickets issued by POST
	// /access/ticket expire that many milliseconds after issuance —
	// mirroring real PVE's 2h ticket lifetime, on a test-friendly
	// timescale. An expired ticket is rejected with 401 exactly like an
	// unknown one. Zero (the default) means tickets never expire,
	// preserving pre-TTL mock behavior. Fixture-level only (not per-node);
	// tests can also set it via the WithTicketTTL server option, which
	// takes precedence.
	TicketTTLMS int `yaml:"ticket_ttl_ms,omitempty"`

	// NetworkReloadFail, when true, makes the next (and every subsequent,
	// until cleared) `PUT /nodes/{node}/network` reload task fail.
	NetworkReloadFail bool `yaml:"network_reload_fail,omitempty"`

	// SDNZoneStatusFail, when true, makes this node report "error" on
	// GET /cluster/sdn/zones/{zone}/status for every zone it is a member
	// of, regardless of whether its bridge actually exists (T-402: models
	// a node whose SDN apply task itself reported success but the node
	// nonetheless failed to realize the config — a failure mode no
	// pre-apply validator can predict, unlike a genuinely-missing bridge,
	// which docs/features/sdn.md §4's own pre-apply validation already
	// catches before an apply is even attempted). Set per-node via the
	// fixture or POST /mock/nodes/{node}/sdn-status-fail, mirroring
	// NetworkReloadFail's exact pattern.
	SDNZoneStatusFail bool `yaml:"sdn_zone_status_fail,omitempty"`

	// FirewallCompileFail, when true, makes GET /nodes/{node}/firewall/status
	// (T-502's mock-only extension — see firewall.go's handleFirewallStatus
	// doc comment for why this route isn't part of the real PVE API) report
	// a compile error instead of "ok", so the change engine's post-apply
	// verification step (docs/features/firewall.md §3) has something to
	// actually catch in tests.
	FirewallCompileFail bool `yaml:"firewall_compile_fail,omitempty"`
}

// merge returns o overridden by any non-zero fields in override.
func (o MockOptions) merge(override *MockOptions) MockOptions {
	if override == nil {
		return o
	}
	out := o
	if override.TaskLatencyMS != 0 {
		out.TaskLatencyMS = override.TaskLatencyMS
	}
	if override.NetworkReloadFail {
		out.NetworkReloadFail = true
	}
	if override.SDNZoneStatusFail {
		out.SDNZoneStatusFail = true
	}
	if override.FirewallCompileFail {
		out.FirewallCompileFail = true
	}
	return out
}
