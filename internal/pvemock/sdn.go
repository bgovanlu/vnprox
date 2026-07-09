package pvemock

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (srv *Server) mountSDN(api chi.Router) {
	api.Get("/cluster/sdn/zones", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNZonesList))
	api.Post("/cluster/sdn/zones", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNZoneCreate))
	api.Get("/cluster/sdn/zones/{zone}", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNZoneGet))
	api.Put("/cluster/sdn/zones/{zone}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNZoneUpdate))
	api.Delete("/cluster/sdn/zones/{zone}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNZoneDelete))
	api.Get("/cluster/sdn/zones/{zone}/status", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNZoneStatus))

	api.Get("/cluster/sdn/vnets", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNVnetsList))
	api.Post("/cluster/sdn/vnets", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNVnetCreate))
	api.Get("/cluster/sdn/vnets/{vnet}", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNVnetGet))
	api.Put("/cluster/sdn/vnets/{vnet}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNVnetUpdate))
	api.Delete("/cluster/sdn/vnets/{vnet}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNVnetDelete))

	api.Get("/cluster/sdn/vnets/{vnet}/subnets", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNSubnetsList))
	api.Post("/cluster/sdn/vnets/{vnet}/subnets", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNSubnetCreate))
	api.Get("/cluster/sdn/vnets/{vnet}/subnets/{subnet}", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNSubnetGet))
	api.Put("/cluster/sdn/vnets/{vnet}/subnets/{subnet}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNSubnetUpdate))
	api.Delete("/cluster/sdn/vnets/{vnet}/subnets/{subnet}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNSubnetDelete))

	api.Get("/cluster/sdn", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNStatus))
	api.Put("/cluster/sdn", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNApply))
}

func (srv *Server) handleSDNZonesList(w http.ResponseWriter, r *http.Request) {
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	out := make([]SDNZoneSpec, 0, len(srv.state.sdn.zones))
	for _, z := range srv.state.sdn.zones {
		out = append(out, z)
	}
	writeData(w, http.StatusOK, out)
}

func (srv *Server) handleSDNZoneGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "zone")
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	z, ok := srv.state.sdn.zones[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("zone %q not found", id))
		return
	}
	writeData(w, http.StatusOK, z)
}

func (srv *Server) handleSDNZoneCreate(w http.ResponseWriter, r *http.Request) {
	var z SDNZoneSpec
	if err := decodeRequest(r, &z); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if z.ID == "" {
		writeError(w, http.StatusBadRequest, "zone id (\"zone\") is required")
		return
	}
	z.Pending = PendingNew
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, exists := srv.state.sdn.zones[z.ID]; exists {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("zone %q already exists", z.ID))
		return
	}
	srv.state.sdn.zones[z.ID] = z
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNZoneUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "zone")
	var z SDNZoneSpec
	if err := decodeRequest(r, &z); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	z.ID = id
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, ok := srv.state.sdn.zones[id]; !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("zone %q not found", id))
		return
	}
	z.Pending = PendingChanged
	srv.state.sdn.zones[id] = z
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNZoneDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "zone")
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	z, ok := srv.state.sdn.zones[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("zone %q not found", id))
		return
	}
	z.Pending = PendingDeleted
	srv.state.sdn.zones[id] = z
	writeData(w, http.StatusOK, nil)
}

// zoneStatusEntry is one node's realization status for a zone, as reported
// by GET /cluster/sdn/zones/{zone}/status. Real PVE surfaces per-node
// health this way so the UI can show "applied / pending / error" per node
// (docs/features/sdn.md §1).
type zoneStatusEntry struct {
	Node   string `json:"node"`
	Status string `json:"status"` // ok|pending|error
	Detail string `json:"detail,omitempty"`
}

func (srv *Server) handleSDNZoneStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "zone")
	srv.state.sdn.mu.RLock()
	z, ok := srv.state.sdn.zones[id]
	srv.state.sdn.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("zone %q not found", id))
		return
	}

	var out []zoneStatusEntry
	for _, nodeName := range z.Nodes {
		entry := zoneStatusEntry{Node: nodeName, Status: "ok"}
		if z.Pending != PendingNone {
			entry.Status = "pending"
			entry.Detail = "zone has unapplied changes"
		}
		if z.Bridge != "" && (z.Type == "simple" || z.Type == "vlan") {
			if ns, ok := srv.state.node(nodeName); ok {
				ns.mu.RLock()
				hasBridge := ifaceExists(ns.network, z.Bridge)
				ns.mu.RUnlock()
				if !hasBridge {
					entry.Status = "error"
					entry.Detail = fmt.Sprintf("bridge %q not found on node %q", z.Bridge, nodeName)
				}
			} else {
				entry.Status = "error"
				entry.Detail = fmt.Sprintf("node %q not found", nodeName)
			}
		}
		out = append(out, entry)
	}
	writeData(w, http.StatusOK, out)
}

func (srv *Server) handleSDNVnetsList(w http.ResponseWriter, r *http.Request) {
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	out := make([]SDNVnetSpec, 0, len(srv.state.sdn.vnets))
	for _, v := range srv.state.sdn.vnets {
		out = append(out, v)
	}
	writeData(w, http.StatusOK, out)
}

func (srv *Server) handleSDNVnetGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vnet")
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	v, ok := srv.state.sdn.vnets[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("vnet %q not found", id))
		return
	}
	writeData(w, http.StatusOK, v)
}

func (srv *Server) handleSDNVnetCreate(w http.ResponseWriter, r *http.Request) {
	var v SDNVnetSpec
	if err := decodeRequest(r, &v); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if v.ID == "" {
		writeError(w, http.StatusBadRequest, "vnet id (\"vnet\") is required")
		return
	}
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, exists := srv.state.sdn.vnets[v.ID]; exists {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("vnet %q already exists", v.ID))
		return
	}
	if _, ok := srv.state.sdn.zones[v.Zone]; !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("zone %q does not exist", v.Zone))
		return
	}
	v.Pending = PendingNew
	srv.state.sdn.vnets[v.ID] = v
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNVnetUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vnet")
	var v SDNVnetSpec
	if err := decodeRequest(r, &v); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	v.ID = id
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, ok := srv.state.sdn.vnets[id]; !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("vnet %q not found", id))
		return
	}
	v.Pending = PendingChanged
	srv.state.sdn.vnets[id] = v
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNVnetDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vnet")
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	v, ok := srv.state.sdn.vnets[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("vnet %q not found", id))
		return
	}
	v.Pending = PendingDeleted
	srv.state.sdn.vnets[id] = v
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNSubnetsList(w http.ResponseWriter, r *http.Request) {
	vnet := chi.URLParam(r, "vnet")
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	var out []SDNSubnetSpec
	for _, s := range srv.state.sdn.subnets {
		if s.Vnet == vnet {
			out = append(out, s)
		}
	}
	writeData(w, http.StatusOK, out)
}

func (srv *Server) handleSDNSubnetGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "subnet")
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	s, ok := srv.state.sdn.subnets[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("subnet %q not found", id))
		return
	}
	writeData(w, http.StatusOK, s)
}

func (srv *Server) handleSDNSubnetCreate(w http.ResponseWriter, r *http.Request) {
	vnet := chi.URLParam(r, "vnet")
	var s SDNSubnetSpec
	if err := decodeRequest(r, &s); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.Vnet = vnet
	if s.ID == "" {
		writeError(w, http.StatusBadRequest, "subnet id is required")
		return
	}
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, exists := srv.state.sdn.subnets[s.ID]; exists {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("subnet %q already exists", s.ID))
		return
	}
	if _, ok := srv.state.sdn.vnets[vnet]; !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("vnet %q does not exist", vnet))
		return
	}
	s.Pending = PendingNew
	srv.state.sdn.subnets[s.ID] = s
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNSubnetUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "subnet")
	var s SDNSubnetSpec
	if err := decodeRequest(r, &s); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.ID = id
	s.Vnet = chi.URLParam(r, "vnet")
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, ok := srv.state.sdn.subnets[id]; !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("subnet %q not found", id))
		return
	}
	s.Pending = PendingChanged
	srv.state.sdn.subnets[id] = s
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNSubnetDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "subnet")
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	s, ok := srv.state.sdn.subnets[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("subnet %q not found", id))
		return
	}
	s.Pending = PendingDeleted
	srv.state.sdn.subnets[id] = s
	writeData(w, http.StatusOK, nil)
}

// sdnStatusEntry is one row of GET /cluster/sdn: the full zone/vnet/subnet
// tree flattened with pending markers, mirroring PVE's own "pending config"
// view that vnprox's SDN cockpit (docs/features/sdn.md) turns into a
// staged-vs-running diff.
type sdnStatusEntry struct {
	Kind    string       `json:"type"` // zone|vnet|subnet
	ID      string       `json:"id"`
	Pending PendingState `json:"pending,omitempty"`
}

func (srv *Server) handleSDNStatus(w http.ResponseWriter, r *http.Request) {
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	var out []sdnStatusEntry
	for _, z := range srv.state.sdn.zones {
		out = append(out, sdnStatusEntry{Kind: "zone", ID: z.ID, Pending: z.Pending})
	}
	for _, v := range srv.state.sdn.vnets {
		out = append(out, sdnStatusEntry{Kind: "vnet", ID: v.ID, Pending: v.Pending})
	}
	for _, s := range srv.state.sdn.subnets {
		out = append(out, sdnStatusEntry{Kind: "subnet", ID: s.ID, Pending: s.Pending})
	}
	writeData(w, http.StatusOK, out)
}

// handleSDNApply implements `PUT /cluster/sdn`: apply all pending SDN
// changes cluster-wide via a task, matching docs/features/sdn.md §4
// ("vnprox wraps the SDN apply (PUT /cluster/sdn) with ..."). Deleted
// objects are removed; new/changed objects have their pending marker
// cleared. Like the network reload, failure injection rolls back: pending
// markers are left untouched (nothing is silently half-applied) rather than
// commiting partial state.
func (srv *Server) handleSDNApply(w http.ResponseWriter, r *http.Request) {
	sess, _ := srv.authenticate(r)
	user := "unknown"
	if sess != nil {
		user = sess.UserID
	}
	latency, fail, reason := resolveMockOverrides(r, srv.state.fixture.Mock)

	task := srv.state.tasks.Run("", "sdnapply", "sdn", user, latency, fail, reason, func(success bool) {
		if !success {
			return
		}
		srv.state.sdn.mu.Lock()
		defer srv.state.sdn.mu.Unlock()
		for id, z := range srv.state.sdn.zones {
			if z.Pending == PendingDeleted {
				delete(srv.state.sdn.zones, id)
				continue
			}
			z.Pending = PendingNone
			srv.state.sdn.zones[id] = z
		}
		for id, v := range srv.state.sdn.vnets {
			if v.Pending == PendingDeleted {
				delete(srv.state.sdn.vnets, id)
				continue
			}
			v.Pending = PendingNone
			srv.state.sdn.vnets[id] = v
		}
		for id, s := range srv.state.sdn.subnets {
			if s.Pending == PendingDeleted {
				delete(srv.state.sdn.subnets, id)
				continue
			}
			s.Pending = PendingNone
			srv.state.sdn.subnets[id] = s
		}
	})
	writeData(w, http.StatusOK, task.UPID)
}
