package pvemock

import "net/http"

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
			Type:   "node",
			Name:   n.Name,
			IP:     n.IP,
			Online: boolToInt(n.Online),
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
		if n.Online {
			status = "online"
		}
		out = append(out, clusterResource{Type: "node", ID: "node/" + n.Name, Node: n.Name, Name: n.Name, Status: status})
	}
	for nodeName := range f.Nodes {
		ns, _ := srv.state.node(nodeName)
		ns.mu.RLock()
		for vmid, g := range ns.qemu {
			out = append(out, clusterResource{Type: "qemu", ID: "qemu/" + vmid, Node: nodeName, Name: g.Name, Status: g.Status, VMID: atoiOr(vmid, 0)})
		}
		for vmid, g := range ns.lxc {
			out = append(out, clusterResource{Type: "lxc", ID: "lxc/" + vmid, Node: nodeName, Name: g.Name, Status: g.Status, VMID: atoiOr(vmid, 0)})
		}
		ns.mu.RUnlock()
	}
	writeData(w, http.StatusOK, out)
}
