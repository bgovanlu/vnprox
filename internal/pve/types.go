package pve

import "fmt"

// GuestKind distinguishes qemu (KVM) guests from lxc containers, matching
// PVE's own path segments ("qemu"/"lxc").
type GuestKind string

const (
	GuestQemu GuestKind = "qemu"
	GuestLXC  GuestKind = "lxc"
)

// --- cluster -------------------------------------------------------------

// clusterStatusWire mirrors GET /cluster/status's wire shape exactly
// (PVE/pvemock report booleans as 0/1 ints); ClusterStatusEntry is the
// converted, ergonomic form callers see.
type clusterStatusWire struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	IP      string `json:"ip,omitempty"`
	Online  int    `json:"online,omitempty"`
	Nodes   int    `json:"nodes,omitempty"`
	Quorate int    `json:"quorate,omitempty"`
	Local   int    `json:"local,omitempty"`
}

// ClusterStatusEntry is one row of GET /cluster/status: either the single
// "cluster" summary row or one "node" row per member.
type ClusterStatusEntry struct {
	Type    string // "cluster" | "node"
	Name    string
	IP      string
	Nodes   int
	Online  bool
	Quorate bool
	Local   bool
}

func (w clusterStatusWire) toEntry() ClusterStatusEntry {
	return ClusterStatusEntry{
		Type:    w.Type,
		Name:    w.Name,
		IP:      w.IP,
		Online:  w.Online != 0,
		Nodes:   w.Nodes,
		Quorate: w.Quorate != 0,
		Local:   w.Local != 0,
	}
}

// ClusterResource is one row of GET /cluster/resources: a node, qemu
// guest, or lxc container summary.
type ClusterResource struct {
	Type   string `json:"type"` // "node" | "qemu" | "lxc" | "storage"
	ID     string `json:"id"`
	Node   string `json:"node"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
	VMID   int    `json:"vmid,omitempty"`
}

// --- node network ----------------------------------------------------------

// PendingState mirrors the "pending" marker PVE reports on network/SDN
// objects awaiting an apply/reload.
type PendingState string

const (
	PendingNone    PendingState = ""
	PendingNew     PendingState = "new"
	PendingChanged PendingState = "changed"
	PendingDeleted PendingState = "deleted"
)

// NetworkInterface is one stanza of a node's /etc/network/interfaces, as
// returned by GET /nodes/{node}/network[/{iface}].
type NetworkInterface struct {
	Iface           string       `json:"iface"`
	Type            string       `json:"type"`
	Method          string       `json:"method,omitempty"`
	Address         string       `json:"address,omitempty"`
	Gateway         string       `json:"gateway,omitempty"`
	Comments        string       `json:"comments,omitempty"`
	BridgePorts     string       `json:"bridge_ports,omitempty"`
	VlanRawDevice   string       `json:"vlan_raw_device,omitempty"`
	Slaves          string       `json:"slaves,omitempty"`
	BondMode        string       `json:"bond_mode,omitempty"`
	Pending         PendingState `json:"pending,omitempty"`
	MTU             int          `json:"mtu,omitempty"`
	VlanID          int          `json:"vlan_id,omitempty"`
	BridgeVlanAware bool         `json:"bridge_vlan_aware,omitempty"`
	Autostart       bool         `json:"autostart"`
}

// networkInterfaceWire mirrors GET /nodes/{node}/network[/{iface}]'s wire
// shape exactly (hardware validation against a real PVE 9.2.4 node found it
// reports autostart/bridge_vlan_aware as 0/1 ints, like cluster/status's
// online/quorate/local flags above — pvemock previously modeled these as
// plain JSON booleans, which is why no test caught this); NetworkInterface
// is the converted, ergonomic form callers see.
type networkInterfaceWire struct {
	Iface           string       `json:"iface"`
	Type            string       `json:"type"`
	Method          string       `json:"method,omitempty"`
	Address         string       `json:"address,omitempty"`
	Gateway         string       `json:"gateway,omitempty"`
	Comments        string       `json:"comments,omitempty"`
	BridgePorts     string       `json:"bridge_ports,omitempty"`
	VlanRawDevice   string       `json:"vlan_raw_device,omitempty"`
	Slaves          string       `json:"slaves,omitempty"`
	BondMode        string       `json:"bond_mode,omitempty"`
	Pending         PendingState `json:"pending,omitempty"`
	MTU             int          `json:"mtu,omitempty"`
	VlanID          int          `json:"vlan_id,omitempty"`
	BridgeVlanAware int          `json:"bridge_vlan_aware,omitempty"`
	Autostart       int          `json:"autostart,omitempty"`
}

func (w networkInterfaceWire) toEntry() NetworkInterface {
	return NetworkInterface{
		Iface:           w.Iface,
		Type:            w.Type,
		Method:          w.Method,
		Address:         w.Address,
		Gateway:         w.Gateway,
		Comments:        w.Comments,
		BridgePorts:     w.BridgePorts,
		VlanRawDevice:   w.VlanRawDevice,
		Slaves:          w.Slaves,
		BondMode:        w.BondMode,
		Pending:         w.Pending,
		MTU:             w.MTU,
		VlanID:          w.VlanID,
		BridgeVlanAware: w.BridgeVlanAware != 0,
		Autostart:       w.Autostart != 0,
	}
}

// NetworkInterfaceUpdate is a partial edit for PUT
// /nodes/{node}/network/{iface}: only non-nil fields are sent, matching
// PVE's merge-not-replace PUT semantics (internal/pvemock/network.go's
// applyNetIfaceField). Delete lists param names to clear (PVE's
// comma-separated "delete" field).
type NetworkInterfaceUpdate struct {
	Type            *string
	Method          *string
	Address         *string
	Gateway         *string
	Comments        *string
	BridgePorts     *string
	VlanRawDevice   *string
	Slaves          *string
	BondMode        *string
	MTU             *int
	VlanID          *int
	BridgeVlanAware *bool
	Autostart       *bool
	Delete          []string
}

// --- guests ----------------------------------------------------------------

// GuestConfigUpdate is a partial edit for PUT
// /nodes/{node}/{qemu,lxc}/{vmid}/config: Set keys are merged onto the
// existing config (PVE's real PUT semantics), Delete keys are removed.
type GuestConfigUpdate struct {
	Set    map[string]string
	Delete []string
}

// --- SDN ---------------------------------------------------------------

// SDNZone is one zone in the cluster-wide SDN tree
// (GET /cluster/sdn/zones[/{zone}]).
type SDNZone struct {
	ID         string `json:"zone"`
	Type       string `json:"type"`
	Bridge     string `json:"bridge,omitempty"`
	Controller string `json:"controller,omitempty"`
	// IPAM (T-3104) is a real, captured zone parameter
	// (planning/reports/evidence/pve-9.2.4-sdn-schema.txt's zone create
	// usage block: `--ipam <string> use a specific ipam`) — the field
	// change.SdnZoneCreateParams/inventory.SdnZone already carried before
	// this task, but which nothing in this package, internal/pvemock, or
	// cmd/vnproxd's changeagent.go ever actually read or wrote: a zone's
	// chosen ipam plugin instance was captured in a changeset's params and
	// promptly dropped on the floor before reaching PVE, and never
	// populated from a live poll either. T-3104 wires it end to end
	// (ingest.go's FromPVESDN, changeagent.go's SDNStageOp/SDNConfig,
	// internal/sdn.Zone) because its own delete-in-use check
	// (checkSdnIpamDeletable) is meaningless against a zone.IPAM that a
	// live poll never populates.
	IPAM string `json:"ipam,omitempty"`
	// Pending decodes whatever "pending" key this struct's DEFAULT list/get
	// view (no query param) returns — which, against real PVE 9.2.4, is
	// none: confirmed live and against PVE::Network::SDN's own source
	// (planning/reports/evidence/pve-9.2.4-sdn-pending-state.txt) that the
	// default view is the raw staged config file with no diff computed at
	// all, so this field always decodes PendingNone off real hardware
	// regardless of actual pending state. It is NOT a reliable signal —
	// callers that need real foreign-pending detection must call
	// ListSDNZonesPending (sdn.go's "?pending=1" view) instead, which
	// internal/sdn.Service.Tree now does for docs/features/sdn.md §1's
	// staged-vs-running cockpit. Kept (not removed) only because
	// internal/pvemock's own default view still populates it (a
	// pre-existing, deliberately-unchanged mock divergence from real PVE —
	// see the evidence file's §5), which other packages' tests still
	// exercise directly against SDNZone/ListSDNZones (T-401-era gap,
	// debt-sweep 2026-08-19).
	Pending   PendingState `json:"pending,omitempty"`
	Nodes     []string     `json:"nodes,omitempty"`
	ExitNodes []string     `json:"exit_nodes,omitempty"`
	Peers     []string     `json:"peers,omitempty"`
	MTU       int          `json:"mtu,omitempty"`
	VrfVxlan  int          `json:"vrf_vxlan,omitempty"`
}

// SDNZoneStatus is one node's realization status for a zone
// (GET /cluster/sdn/zones/{zone}/status).
type SDNZoneStatus struct {
	Node   string `json:"node"`
	Status string `json:"status"` // ok|pending|error
	Detail string `json:"detail,omitempty"`
}

// SDNVnet is one VNet inside a zone
// (GET /cluster/sdn/vnets[/{vnet}]).
type SDNVnet struct {
	ID    string `json:"vnet"`
	Zone  string `json:"zone"`
	Alias string `json:"alias,omitempty"`
	// Pending — see SDNZone.Pending's doc comment; the same "default view
	// never carries it against real PVE, use ListSDNVnetsPending instead"
	// caveat applies verbatim.
	Pending   PendingState `json:"pending,omitempty"`
	Tag       int          `json:"tag,omitempty"`
	VlanAware bool         `json:"vlan_aware,omitempty"`
}

// SDNSubnet is one subnet inside a VNet
// (GET /cluster/sdn/vnets/{vnet}/subnets[/{subnet}]).
type SDNSubnet struct {
	ID             string `json:"subnet"`
	Vnet           string `json:"vnet"`
	CIDR           string `json:"cidr"`
	Gateway        string `json:"gateway,omitempty"`
	DHCPRangeStart string `json:"dhcp_range_start,omitempty"`
	DHCPRangeEnd   string `json:"dhcp_range_end,omitempty"`
	// Pending — see SDNZone.Pending's doc comment; the same "default view
	// never carries it against real PVE, use ListSDNSubnetsPending instead"
	// caveat applies verbatim.
	Pending PendingState `json:"pending,omitempty"`
	SNAT    bool         `json:"snat,omitempty"`
}

// --- firewall ----------------------------------------------------------

// FirewallScope names one of PVE's four firewall scopes (cluster, node,
// guest, or vnet) and resolves to the corresponding API path prefix.
// Construct with ClusterFirewallScope, NodeFirewallScope, GuestFirewallScope,
// or VnetFirewallScope.
type FirewallScope struct {
	prefix string
}

// ClusterFirewallScope is the cluster-wide firewall ruleset.
func ClusterFirewallScope() FirewallScope {
	return FirewallScope{prefix: "/cluster/firewall"}
}

// NodeFirewallScope is one node's firewall ruleset.
func NodeFirewallScope(node string) FirewallScope {
	return FirewallScope{prefix: "/nodes/" + node + "/firewall"}
}

// GuestFirewallScope is one guest's firewall ruleset.
func GuestFirewallScope(node string, kind GuestKind, vmid int) FirewallScope {
	return FirewallScope{prefix: fmt.Sprintf("/nodes/%s/%s/%d/firewall", node, kind, vmid)}
}

// VnetFirewallScope is one SDN vnet's firewall ruleset (T-3103): GET/PUT
// /cluster/sdn/vnets/{vnet}/firewall/{rules,options} — hardware-captured
// (planning/reports/evidence/pve-9.2.4-sdn-schema.txt's "### ls
// /cluster/sdn/vnets/labnet/firewall"). vnet names are unique cluster-wide
// (real PVE's own vnet id namespace), so the path needs only the vnet name,
// not its owning zone.
func VnetFirewallScope(vnet string) FirewallScope {
	return FirewallScope{prefix: "/cluster/sdn/vnets/" + vnet + "/firewall"}
}

// FirewallRule is one rule at any scope.
type FirewallRule struct {
	Dest    string `json:"dest,omitempty"`
	Type    string `json:"type"`
	Action  string `json:"action"`
	Proto   string `json:"proto,omitempty"`
	Source  string `json:"source,omitempty"`
	Sport   string `json:"sport,omitempty"`
	Dport   string `json:"dport,omitempty"`
	Iface   string `json:"iface,omitempty"`
	Macro   string `json:"macro,omitempty"`
	Log     string `json:"log,omitempty"`
	Comment string `json:"comment,omitempty"`
	Pos     int    `json:"pos"`
	// Enabled marshals/unmarshals via pveBool (pvebool.go): real PVE
	// (validated on a 9.2.10 node, T-3202) both returns "enable" as the
	// number 0/1 on read AND rejects a literal JSON true/false on write
	// (POST .../rules with "enable":true fails "Parameter verification
	// failed" — the create endpoint's schema types this field as an
	// integer) — the same convention SDNSubnet.SNAT/SDNVnet.VlanAware/
	// FirewallOptions.Enable already needed pveBool for, just discovered
	// on this field's write path specifically, since this project's first
	// real hardware fw.rule.create call.
	Enabled bool `json:"enable"`
}

// FirewallOptions is the ruleset-level policy/enable state at any scope.
//
// PolicyForward/LogLevelForward (T-3103) are the forward chain's own
// fallthrough policy and log level. PolicyForward is hardware-captured at
// cluster and vnet scope (planning/reports/evidence/
// pve-9.2.4-sdn-schema.txt); LogLevelForward only at vnet scope — the same
// capture's cluster/firewall/options excerpt shows policy_forward but never
// independently matches a log_level_forward line the way the vnet section
// does, so it is modelled here (for round-tripping whatever a scope's GET
// actually returns) but only ever written at vnet scope — see
// internal/change's schemaFwOptionsForScope.
type FirewallOptions struct {
	PolicyIn        string `json:"policy_in,omitempty"`
	PolicyOut       string `json:"policy_out,omitempty"`
	PolicyForward   string `json:"policy_forward,omitempty"`
	LogLevelForward string `json:"log_level_forward,omitempty"`
	Enable          bool   `json:"enable"`
}

// FirewallAlias is a named IP/CIDR alias at any scope.
type FirewallAlias struct {
	Name    string `json:"name"`
	CIDR    string `json:"cidr"`
	Comment string `json:"comment,omitempty"`
}

// FirewallIPSetSummary is one row of an ipset list at any scope.
type FirewallIPSetSummary struct {
	Name    string `json:"name"`
	Comment string `json:"comment,omitempty"`
}

// FirewallIPSetEntry is one member of an IPSet.
type FirewallIPSetEntry struct {
	CIDR    string `json:"cidr"`
	Comment string `json:"comment,omitempty"`
	NoMatch bool   `json:"nomatch,omitempty"`
}

// FirewallGroupSummary is one row of the cluster-scope security group
// list (GET /cluster/firewall/groups).
type FirewallGroupSummary struct {
	Name    string `json:"group"`
	Comment string `json:"comment,omitempty"`
}

// --- tasks -----------------------------------------------------------------

// TaskStatus is the result of GET /nodes/{node}/tasks/{upid}/status.
type TaskStatus struct {
	UPID       string `json:"upid"`
	Node       string `json:"node"`
	Type       string `json:"type"`
	User       string `json:"user"`
	Status     string `json:"status"` // "running" | "stopped"
	ExitStatus string `json:"exitstatus,omitempty"`
	StartTime  int64  `json:"starttime"`
	EndTime    int64  `json:"endtime,omitempty"`
}

// Running reports whether the task has not yet reached a terminal status.
func (t TaskStatus) Running() bool { return t.Status == "running" }

// Failed reports whether a terminal task's exit status indicates failure
// (PVE's "failed: <reason>" convention).
func (t TaskStatus) Failed() bool {
	return !t.Running() && len(t.ExitStatus) >= 6 && t.ExitStatus[:6] == "failed"
}

// TaskLogLine is one line of GET /nodes/{node}/tasks/{upid}/log.
type TaskLogLine struct {
	T string `json:"t"`
	N int    `json:"n"`
}
