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
// serves both read-only, straight from the fixture's sdn.ipams section;
// IPAM *writes* (reserve/release) are phase-4 change-engine work and are
// not implemented here yet.
func (srv *Server) mountIPAM(api chi.Router) {
	api.Get("/cluster/sdn/ipams", srv.requirePrivilege(PrivSDNAudit, srv.handleIPAMList))
	api.Get("/cluster/sdn/ipams/{ipam}", srv.requirePrivilege(PrivSDNAudit, srv.handleIPAMGet))
	api.Get("/cluster/sdn/ipams/{ipam}/status", srv.requirePrivilege(PrivSDNAudit, srv.handleIPAMStatus))
}

func (srv *Server) handleIPAMList(w http.ResponseWriter, _ *http.Request) {
	out := make([]SDNIpamSpec, 0, len(srv.state.fixture.SDN.Ipams))
	out = append(out, srv.state.fixture.SDN.Ipams...)
	writeData(w, http.StatusOK, out)
}

func (srv *Server) findIPAM(id string) (SDNIpamSpec, bool) {
	for _, ip := range srv.state.fixture.SDN.Ipams {
		if ip.ID == id {
			return ip, true
		}
	}
	return SDNIpamSpec{}, false
}

func (srv *Server) handleIPAMGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "ipam")
	ip, ok := srv.findIPAM(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("ipam %q not found", id))
		return
	}
	writeData(w, http.StatusOK, ip)
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
	ip, ok := srv.findIPAM(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("ipam %q not found", id))
		return
	}
	out := make([]ipamEntryWire, 0, len(ip.Entries))
	for _, e := range ip.Entries {
		out = append(out, ipamEntryWire{IPAMEntrySpec: e, Gateway: boolToInt(e.Gateway)})
	}
	writeData(w, http.StatusOK, out)
}
