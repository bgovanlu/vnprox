package pvemock

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// IPAM endpoints. Real PVE exposes the configured IPAM plugin instances
// (built-in "pve", NetBox, phpIPAM) under /cluster/sdn/ipams, and each
// instance's current allocation set under
// /cluster/sdn/ipams/{ipam}/status — the data source for vnprox's
// /ipam/* read views (docs/api.md, docs/features/ipam.md). The mock
// serves both from the mutable ipamState (state.go), seeded from the
// fixture's sdn.ipams section at load and mutated by
// handleIPAMCreateIP/handleIPAMDeleteIP below (T-405's `ipam.alloc.*`
// write path — reserve/release an address on a vnet).
func (srv *Server) mountIPAM(api chi.Router) {
	api.Get("/cluster/sdn/ipams", srv.requirePrivilege(PrivSDNAudit, srv.handleIPAMList))
	api.Get("/cluster/sdn/ipams/{ipam}", srv.requirePrivilege(PrivSDNAudit, srv.handleIPAMGet))
	api.Get("/cluster/sdn/ipams/{ipam}/status", srv.requirePrivilege(PrivSDNAudit, srv.handleIPAMStatus))

	// Real PVE resolves which configured IPAM plugin instance backs a vnet
	// from the vnet's zone config — the caller never names the plugin
	// explicitly (docs/features/ipam.md §1: "vnprox reads through PVE's
	// plugin transparently"). handleIPAMCreateIP/handleIPAMDeleteIP mirror
	// that: they resolve the owning plugin id themselves (ipamForVnet)
	// rather than taking it as a path/body parameter.
	api.Post("/cluster/sdn/vnets/{vnet}/ips", srv.requirePrivilege(PrivSDNAllocate, srv.handleIPAMCreateIP))
	api.Delete("/cluster/sdn/vnets/{vnet}/ips", srv.requirePrivilege(PrivSDNAllocate, srv.handleIPAMDeleteIP))
}

func (srv *Server) handleIPAMList(w http.ResponseWriter, _ *http.Request) {
	out := make([]SDNIpamSpec, 0, len(srv.state.fixture.SDN.Ipams))
	out = append(out, srv.state.fixture.SDN.Ipams...)
	writeData(w, http.StatusOK, out)
}

// ipamSpec returns the ID/Type/URL metadata for one configured IPAM plugin
// instance (from the immutable fixture — that part of an instance's
// identity never changes at runtime, only its Entries do, in state.ipam).
func (srv *Server) ipamSpec(id string) (SDNIpamSpec, bool) {
	for _, ip := range srv.state.fixture.SDN.Ipams {
		if ip.ID == id {
			return ip, true
		}
	}
	return SDNIpamSpec{}, false
}

func (srv *Server) handleIPAMGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "ipam")
	spec, ok := srv.ipamSpec(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("ipam %q not found", id))
		return
	}
	writeData(w, http.StatusOK, spec)
}

// ipamEntryWire is IPAMEntrySpec's on-the-wire form: PVE reports the
// gateway marker as a 0/1 int (the same convention as cluster/status's
// online/quorate flags), not a JSON bool.
type ipamEntryWire struct {
	IPAMEntrySpec
	Gateway int `json:"gateway,omitempty"`
}

func (srv *Server) handleIPAMStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "ipam")
	if _, ok := srv.ipamSpec(id); !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("ipam %q not found", id))
		return
	}
	srv.state.ipam.mu.RLock()
	entries := append([]IPAMEntrySpec(nil), srv.state.ipam.entries[id]...)
	srv.state.ipam.mu.RUnlock()

	out := make([]ipamEntryWire, 0, len(entries))
	for _, e := range entries {
		out = append(out, ipamEntryWire{IPAMEntrySpec: e, Gateway: boolToInt(e.Gateway)})
	}
	writeData(w, http.StatusOK, out)
}

// ipamForVnet resolves which configured IPAM plugin instance owns vnet:
// the plugin with an existing entry referencing it, or — for a vnet with
// no allocations yet — the sole configured plugin (real single-"pve"-IPAM
// deployments, the common case fixtures model) when there is exactly one.
// A fixture wiring multiple IPAM plugins must therefore seed at least one
// entry per vnet-to-plugin mapping it wants create/delete requests to
// resolve unambiguously — reasonable for a mock, since real PVE resolves
// this server-side from the zone's own config, which this mock's
// SDNZoneSpec does not (yet) model.
func (srv *Server) ipamForVnet(vnet string) (string, bool) {
	srv.state.ipam.mu.RLock()
	defer srv.state.ipam.mu.RUnlock()
	for id, entries := range srv.state.ipam.entries {
		for _, e := range entries {
			if e.Vnet == vnet {
				return id, true
			}
		}
	}
	if len(srv.state.fixture.SDN.Ipams) == 1 {
		return srv.state.fixture.SDN.Ipams[0].ID, true
	}
	return "", false
}

// ipamCreateIPRequest is POST /cluster/sdn/vnets/{vnet}/ips' body: reserve
// ip inside vnet's IPAM (T-405's ipam.alloc.create op).
type ipamCreateIPRequest struct {
	IP       string `json:"ip"`
	Mac      string `json:"mac,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Zone     string `json:"zone,omitempty"`
	Subnet   string `json:"subnet,omitempty"`
}

// vnetExists reports whether vnet names a currently-configured SDN vnet —
// checked before every IPAM write below so a typo'd or already-deleted
// vnet is rejected outright rather than silently falling through to
// ipamForVnet's single-plugin fallback (which would otherwise resolve
// *any* vnet name to the sole configured plugin in the common
// one-plugin-per-cluster case).
func (srv *Server) vnetExists(vnet string) bool {
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	_, ok := srv.state.sdn.vnets[vnet]
	return ok
}

func (srv *Server) handleIPAMCreateIP(w http.ResponseWriter, r *http.Request) {
	vnet := chi.URLParam(r, "vnet")
	var req ipamCreateIPRequest
	if err := decodeRequest(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.IP == "" {
		writeError(w, http.StatusBadRequest, "ip is required")
		return
	}
	if !srv.vnetExists(vnet) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("vnet %q does not exist", vnet))
		return
	}

	ipamID, ok := srv.ipamForVnet(vnet)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("could not resolve an IPAM plugin for vnet %q", vnet))
		return
	}

	srv.state.ipam.mu.Lock()
	defer srv.state.ipam.mu.Unlock()
	for _, e := range srv.state.ipam.entries[ipamID] {
		if e.Subnet == req.Subnet && e.IP == req.IP {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("ip %q already allocated in subnet %q", req.IP, req.Subnet))
			return
		}
	}
	srv.state.ipam.entries[ipamID] = append(srv.state.ipam.entries[ipamID], IPAMEntrySpec{
		Zone:     req.Zone,
		Vnet:     vnet,
		Subnet:   req.Subnet,
		IP:       req.IP,
		MAC:      req.Mac,
		Hostname: req.Hostname,
	})
	writeData(w, http.StatusOK, nil)
}

// ipamDeleteIPRequest is DELETE /cluster/sdn/vnets/{vnet}/ips' body:
// release ip from vnet's IPAM (T-405's ipam.alloc.delete op). Real PVE's
// DELETE for this route also takes its identifying fields in the request
// body rather than the query string (mirrored by internal/pve.Client).
type ipamDeleteIPRequest struct {
	IP     string `json:"ip"`
	Subnet string `json:"subnet,omitempty"`
}

func (srv *Server) handleIPAMDeleteIP(w http.ResponseWriter, r *http.Request) {
	vnet := chi.URLParam(r, "vnet")
	var req ipamDeleteIPRequest
	if err := decodeRequest(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.IP == "" {
		writeError(w, http.StatusBadRequest, "ip is required")
		return
	}
	if !srv.vnetExists(vnet) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("vnet %q does not exist", vnet))
		return
	}

	ipamID, ok := srv.ipamForVnet(vnet)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("could not resolve an IPAM plugin for vnet %q", vnet))
		return
	}

	srv.state.ipam.mu.Lock()
	defer srv.state.ipam.mu.Unlock()
	entries := srv.state.ipam.entries[ipamID]
	for i, e := range entries {
		if e.Vnet == vnet && e.IP == req.IP && (req.Subnet == "" || e.Subnet == req.Subnet) {
			srv.state.ipam.entries[ipamID] = append(entries[:i:i], entries[i+1:]...)
			writeData(w, http.StatusOK, nil)
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("ip %q not allocated on vnet %q", req.IP, vnet))
}
