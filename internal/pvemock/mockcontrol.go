// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// handleMockMess returns the fixture's documented "mess" list — plain
// English descriptions of the deliberate drift/conflict/stale-config
// scenarios it encodes. Only messy-brownfield.yaml populates this; other
// fixtures return an empty list. Not part of the real PVE API.
func (srv *Server) handleMockMess(w http.ResponseWriter, _ *http.Request) {
	writeData(w, http.StatusOK, srv.state.fixture.Mess)
}

// handleMockSetNetworkReloadFail lets a test flip failure injection for a
// node's next network reload without editing YAML or restarting the
// server: POST /mock/nodes/{node}/network-reload-fail {"fail": true}.
func (srv *Server) handleMockSetNetworkReloadFail(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}
	var body struct {
		Fail bool `json:"fail"`
	}
	if err := decodeRequest(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ns.mu.Lock()
	ns.mock.NetworkReloadFail = body.Fail
	ns.mu.Unlock()
	writeData(w, http.StatusOK, nil)
}

// handleMockSetSDNZoneStatusFail lets a test flip failure injection for a
// node's SDN zone realization status without editing YAML or restarting the
// server (T-402): POST /mock/nodes/{node}/sdn-status-fail {"fail": true} —
// mirrors handleMockSetNetworkReloadFail's exact pattern (see
// MockOptions.SDNZoneStatusFail's doc comment for why this, and not a
// missing-bridge fixture, is the right way to model a post-apply-only
// failure no pre-apply validator could have predicted).
func (srv *Server) handleMockSetSDNZoneStatusFail(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}
	var body struct {
		Fail bool `json:"fail"`
	}
	if err := decodeRequest(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ns.mu.Lock()
	ns.mock.SDNZoneStatusFail = body.Fail
	ns.mu.Unlock()
	writeData(w, http.StatusOK, nil)
}

// handleMockSetSDNZonesUnavailable lets a test flip a node's SDN zone
// status endpoint to answer an empty array unconditionally (T-3701),
// without editing YAML or restarting the server: POST
// /mock/nodes/{node}/sdn-zones-unavailable {"unavailable": true} — mirrors
// handleMockSetSDNZoneStatusFail's exact pattern (see
// MockOptions.SDNZonesUnavailable's doc comment for the real, observed
// cross-node divergence this models).
func (srv *Server) handleMockSetSDNZonesUnavailable(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}
	var body struct {
		Unavailable bool `json:"unavailable"`
	}
	if err := decodeRequest(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ns.mu.Lock()
	ns.mock.SDNZonesUnavailable = body.Unavailable
	ns.mu.Unlock()
	writeData(w, http.StatusOK, nil)
}

// handleMockSetFirewallCompileFail lets a test flip failure injection for a
// node's firewall compile status (T-502's post-apply verification, spec §3)
// without editing YAML: POST /mock/nodes/{node}/firewall-compile-fail
// {"fail": true}.
func (srv *Server) handleMockSetFirewallCompileFail(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}
	var body struct {
		Fail bool `json:"fail"`
	}
	if err := decodeRequest(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ns.mu.Lock()
	ns.mock.FirewallCompileFail = body.Fail
	ns.mu.Unlock()
	writeData(w, http.StatusOK, nil)
}
