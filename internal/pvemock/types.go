package pvemock

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
	Links    map[string]LinkInfo     `yaml:"links"`
	LLDP     map[string]LLDPNeighbor `yaml:"lldp"`
	Stats    map[string]IfaceStats   `yaml:"stats"`
	Qemu     map[string]*GuestSpec   `yaml:"qemu"`
	Lxc      map[string]*GuestSpec   `yaml:"lxc"`
	Firewall *FirewallScope          `yaml:"firewall"`
	Mock     *MockOptions            `yaml:"mock"`
	// FRR is this node's fixture-declared FRR/BGP EVPN daemon state
	// (T-404, docs/features/sdn.md §3). Nil models a node with no FRR
	// installed/running at all — this package's HostReader.FRRBGPSummary/
	// FRREVPNVNI return ErrFRRUnavailable for such a node, so the
	// aggregation layer can report a clean per-node "no EVPN" rather than
	// treating it as an error (T-404 AC2).
	FRR            *FRRSpec   `yaml:"frr,omitempty"`
	Network        []NetIface `yaml:"network"`
	NetworkPending []NetIface `yaml:"network_pending"`
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
	BridgeVlanAware bool         `yaml:"bridge_vlan_aware,omitempty" json:"bridge_vlan_aware,omitempty"`
	Autostart       bool         `yaml:"autostart" json:"autostart"`
}

// LinkInfo is netlink-equivalent physical/virtual link state for one iface.
type LinkInfo struct {
	Mac       string         `yaml:"mac" json:"mac"`
	Driver    string         `yaml:"driver,omitempty" json:"driver,omitempty"`
	Duplex    string         `yaml:"duplex,omitempty" json:"duplex,omitempty"`
	PCIAddr   string         `yaml:"pci_addr,omitempty" json:"pci_addr,omitempty"`
	Members   []string       `yaml:"members,omitempty" json:"members,omitempty"`
	FDB       []FDBEntrySpec `yaml:"fdb,omitempty" json:"fdb,omitempty"`
	SpeedMbps int            `yaml:"speed_mbps,omitempty" json:"speed_mbps,omitempty"`
	MTU       int            `yaml:"mtu,omitempty" json:"mtu,omitempty"`
	LinkUp    bool           `yaml:"link_up" json:"link_up"`
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

// LLDPNeighbor is one LLDP-discovered neighbor on a local iface.
type LLDPNeighbor struct {
	ChassisName string `yaml:"chassis_name" json:"chassis_name"`
	ChassisID   string `yaml:"chassis_id" json:"chassis_id"`
	PortID      string `yaml:"port_id" json:"port_id"`
	PortDescr   string `yaml:"port_descr,omitempty" json:"port_descr,omitempty"`
	MgmtIP      string `yaml:"mgmt_ip,omitempty" json:"mgmt_ip,omitempty"`
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
type GuestSpec struct {
	Config   map[string]string `yaml:"config"`
	Firewall *FirewallScope    `yaml:"firewall"`
	Name     string            `yaml:"name"`
	Status   string            `yaml:"status"`
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
	return out
}
