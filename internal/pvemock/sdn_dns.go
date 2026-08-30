// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// SDN DNS plugin-instance mock (T-1204, corrected by T-4112).
//
// This file used to serve four more routes than PVE has:
//
//	GET    /cluster/sdn/dns/{zone}/records
//	POST   /cluster/sdn/dns/{zone}/records
//	PUT    /cluster/sdn/dns/{zone}/records/{name}/{type}
//	DELETE /cluster/sdn/dns/{zone}/records/{name}/{type}
//	GET    /cluster/sdn/dns/{zone}/resolve
//
// None of them exist. `pvesh usage` answers `no such resource` for both
// sub-paths on PVE 9.2.4, and `pvesh ls /cluster/sdn/dns/<id>` reports the
// object "does not define child links"
// (planning/reports/evidence/pve-9.2.4-sdn-dns-surface.txt).
//
// They are deleted rather than left in place and unused, which is the same
// correction T-3701 had to make. A mock route with no real counterpart is not
// harmless: internal/collect called one of these once per DNS zone per poll
// cycle for months, and the reason nothing failed is that this file answered.
// CLAUDE.md's warning is exact — "a mock and the check that tests it, both
// derived from the same secondary source, will pass together forever."
//
// What remains is what PVE has: CRUD on the plugin instances themselves, each
// a PowerDNS server connection. The records those connections hold are not
// PVE's to serve; a test that needs records stands up a PowerDNS double
// instead (internal/powerdns's tests do exactly that with httptest).

func (srv *Server) mountSDNDNS(api chi.Router) {
	api.Get("/cluster/sdn/dns", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNDnsZonesList))
	api.Post("/cluster/sdn/dns", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNDnsZoneCreate))
	// Reading one instance returns its url and key, so it is gated on
	// SDN.Allocate rather than SDN.Audit — matching real PVE, whose read
	// method declares `check => ['perm', '/sdn/dns/{dns}', ['SDN.Allocate']]`
	// precisely because the object carries an API secret.
	api.Get("/cluster/sdn/dns/{zone}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNDnsZoneGet))
	api.Put("/cluster/sdn/dns/{zone}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNDnsZoneUpdate))
	api.Delete("/cluster/sdn/dns/{zone}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNDnsZoneDelete))
}

func (srv *Server) handleSDNDnsZonesList(w http.ResponseWriter, r *http.Request) {
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	// T-2502-followup-01: dnsZones is a map, whose iteration order is
	// randomized; sort by ID (the map's own key) so the response is
	// deterministic.
	out := make([]SDNDnsZoneSpec, 0, len(srv.state.sdn.dnsZones))
	for _, id := range sortedKeys(srv.state.sdn.dnsZones) {
		out = append(out, srv.state.sdn.dnsZones[id])
	}
	writeData(w, http.StatusOK, out)
}

func (srv *Server) handleSDNDnsZoneGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "zone")
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	z, ok := srv.state.sdn.dnsZones[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("dns plugin instance %q not found", id))
		return
	}
	writeData(w, http.StatusOK, z)
}

func (srv *Server) handleSDNDnsZoneCreate(w http.ResponseWriter, r *http.Request) {
	var z SDNDnsZoneSpec
	if err := decodeRequest(r, &z); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// PVE declares dns, type, url and key non-optional on create
	// (`pvesh usage /cluster/sdn/dns` in the evidence transcript). This mock
	// enforces them because vnprox's own sdn.dns.zone.create op carried only
	// dns and ttl until T-4112 — it had never been applied against anything
	// that would reject it, and so had never failed.
	if z.ID == "" {
		writeError(w, http.StatusBadRequest, "Parameter verification failed. - dns: parameter is missing")
		return
	}
	for field, value := range map[string]string{"type": z.Type, "url": z.URL, "key": z.Key} {
		if value == "" {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("Parameter verification failed. - %s: parameter is missing", field))
			return
		}
	}
	if z.Type != "powerdns" {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("Parameter verification failed. - type: value '%s' does not have a value in the enumeration 'powerdns'", z.Type))
		return
	}
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, exists := srv.state.sdn.dnsZones[z.ID]; exists {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("dns plugin instance %q already exists", z.ID))
		return
	}
	srv.state.sdn.dnsZones[z.ID] = z
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNDnsZoneUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "zone")
	var z SDNDnsZoneSpec
	if err := decodeRequest(r, &z); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, ok := srv.state.sdn.dnsZones[id]; !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("dns plugin instance %q not found", id))
		return
	}
	z.ID = id
	srv.state.sdn.dnsZones[id] = z
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNDnsZoneDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "zone")
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, ok := srv.state.sdn.dnsZones[id]; !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("dns plugin instance %q not found", id))
		return
	}
	delete(srv.state.sdn.dnsZones, id)
	writeData(w, http.StatusOK, nil)
}
