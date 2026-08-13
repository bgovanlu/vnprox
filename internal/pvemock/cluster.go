package pvemock

import (
	"net/http"
	"sort"
)

type clusterStatusEntry struct {
	Type    string `json:"type"` // "cluster" | "node"
	Name    string `json:"name"`
	IP      string `json:"ip,omitempty"`
	Online  int    `json:"online,omitempty"`
	Nodes   int    `json:"nodes,omitempty"`
	Quorate int    `json:"quorate,omitempty"`
	Local   int    `json:"local,omitempty"`
}

func (srv *Server) handleClusterStatus(w http.ResponseWriter, _ *http.Request) {
	f := srv.state.fixture
	entries := []clusterStatusEntry{
		{
			Type:    "cluster",
			Name:    f.Cluster.Name,
			Nodes:   len(f.Cluster.Nodes),
			Quorate: boolToInt(f.Cluster.Quorate),
		},
	}
	for i, n := range f.Cluster.Nodes {
		entries = append(entries, clusterStatusEntry{
			Type: "node",
			Name: n.Name,
			IP:   n.IP,
			// T-2504: the fixture's declared flag unless churn.go's
			// SetNodeOnline has overridden it for this member.
			Online: boolToInt(srv.state.nodeOnline(n.Name, n.Online)),
			Local:  boolToInt(i == 0),
		})
	}
	writeData(w, http.StatusOK, entries)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

type clusterResource struct {
	Type   string `json:"type"` // node|qemu|lxc|storage
	ID     string `json:"id"`
	Node   string `json:"node"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
	VMID   int    `json:"vmid,omitempty"`
}

func (srv *Server) handleClusterResources(w http.ResponseWriter, _ *http.Request) {
	f := srv.state.fixture
	var out []clusterResource
	for _, n := range f.Cluster.Nodes {
		status := "offline"
		if srv.state.nodeOnline(n.Name, n.Online) {
			status = "online"
		}
		out = append(out, clusterResource{Type: "node", ID: "node/" + n.Name, Node: n.Name, Name: n.Name, Status: status})
	}
	// T-2502-followup-01: f.Nodes and each node's qemu/lxc maps iterate in
	// randomized order. Sort node names alphabetically and VMIDs
	// numerically so this response is deterministic byte-for-byte across
	// runs and processes, rather than depending on Go's map iteration
	// seed.
	for _, nodeName := range sortedKeys(f.Nodes) {
		ns, _ := srv.state.node(nodeName)
		ns.mu.RLock()
		for _, vmid := range sortedVMIDs(ns.qemu) {
			g := ns.qemu[vmid]
			out = append(out, clusterResource{Type: "qemu", ID: "qemu/" + vmid, Node: nodeName, Name: g.Name, Status: g.Status, VMID: atoiOr(vmid, 0)})
		}
		for _, vmid := range sortedVMIDs(ns.lxc) {
			g := ns.lxc[vmid]
			out = append(out, clusterResource{Type: "lxc", ID: "lxc/" + vmid, Node: nodeName, Name: g.Name, Status: g.Status, VMID: atoiOr(vmid, 0)})
		}
		ns.mu.RUnlock()
	}
	writeData(w, http.StatusOK, out)
}

// sortedVMIDs returns m's keys (VMIDs, e.g. "100") in ascending numeric
// order. VMIDs are the map's own identifying key, but a plain string sort
// would put "20" after "100"; guests are a resource users reason about
// numerically, so sort on that value instead.
func sortedVMIDs[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return atoiOr(keys[i], 0) < atoiOr(keys[j], 0) })
	return keys
}
