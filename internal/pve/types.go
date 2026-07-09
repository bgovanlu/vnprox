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
	ID         string       `json:"zone"`
	Type       string       `json:"type"`
	Bridge     string       `json:"bridge,omitempty"`
	Controller string       `json:"controller,omitempty"`
	Pending    PendingState `json:"pending,omitempty"`
	Nodes      []string     `json:"nodes,omitempty"`
	ExitNodes  []string     `json:"exit_nodes,omitempty"`
	Peers      []string     `json:"peers,omitempty"`
	MTU        int          `json:"mtu,omitempty"`
	VrfVxlan   int          `json:"vrf_vxlan,omitempty"`
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
	ID        string       `json:"vnet"`
	Zone      string       `json:"zone"`
	Alias     string       `json:"alias,omitempty"`
	Pending   PendingState `json:"pending,omitempty"`
	Tag       int          `json:"tag,omitempty"`
	VlanAware bool         `json:"vlan_aware,omitempty"`
}

// SDNSubnet is one subnet inside a VNet
// (GET /cluster/sdn/vnets/{vnet}/subnets[/{subnet}]).
type SDNSubnet struct {
	ID             string       `json:"subnet"`
	Vnet           string       `json:"vnet"`
	CIDR           string       `json:"cidr"`
	Gateway        string       `json:"gateway,omitempty"`
	DHCPRangeStart string       `json:"dhcp_range_start,omitempty"`
	DHCPRangeEnd   string       `json:"dhcp_range_end,omitempty"`
	Pending        PendingState `json:"pending,omitempty"`
	SNAT           bool         `json:"snat,omitempty"`
}

// SDNStatusEntry is one row of GET /cluster/sdn: the full zone/vnet/subnet
// tree flattened with pending markers.
type SDNStatusEntry struct {
	Kind    string       `json:"type"` // zone|vnet|subnet
	ID      string       `json:"id"`
	Pending PendingState `json:"pending,omitempty"`
}

// --- firewall ----------------------------------------------------------

// FirewallScope names one of PVE's three firewall scopes (cluster, node,
// or guest) and resolves to the corresponding API path prefix. Construct
// with ClusterFirewallScope, NodeFirewallScope, or GuestFirewallScope.
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
	Enabled bool   `json:"enable"`
}

// FirewallOptions is the ruleset-level policy/enable state at any scope.
type FirewallOptions struct {
	PolicyIn  string `json:"policy_in,omitempty"`
	PolicyOut string `json:"policy_out,omitempty"`
	Enable    bool   `json:"enable"`
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
