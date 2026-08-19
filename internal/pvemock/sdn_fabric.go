package pvemock

import (
	"fmt"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
)

// SDN Fabrics mock (T-3101), closely mirroring sdn_dns.go's zone-CRUD
// structure — fixture-backed state under srv.state.sdn, guarded by the same
// sdn.mu mutex sdn.go/sdn_dns.go already use. See internal/pve/sdn_fabric.go
// for the real-shape package doc comment this file realizes server-side.

// sdnFabricIDPattern is real PVE's fabric id charset+length, captured
// verbatim (planning/reports/evidence/pve-9.2.4-sdn-schema.txt):
//
//	[a-zA-Z0-9][a-zA-Z0-9-]{0,6}[a-zA-Z0-9]
//
// — 2 to 8 characters, alphanumeric with interior hyphens allowed, shorter
// than any other SDN id vnprox validates (zone/vnet ids have no hyphen and
// no PVE-documented length cap — see sdnIDPattern above). A single regex
// captures both the charset and the length bound.
var sdnFabricIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,6}[a-zA-Z0-9]$`)

// sdnFabricProtocols is the four fabric protocols the capture's
// `--protocol <bgp | openfabric | ospf | wireguard>` enumerates.
var sdnFabricProtocols = map[string]bool{
	"bgp": true, "openfabric": true, "ospf": true, "wireguard": true,
}

// sdnFabricParamVerifyError returns a PVE-style rejection string if id is
// not a valid fabric id, or "" if it is acceptable — mirrors
// sdnParamVerifyError's convention (sdn.go) with the fabric-specific
// pattern above.
func sdnFabricParamVerifyError(id string) string {
	if id != "" && !sdnFabricIDPattern.MatchString(id) {
		return fmt.Sprintf("Parameter verification failed. - id: value '%s' does not match the regex pattern (must be 2-8 characters, alphanumeric with interior hyphens)", id)
	}
	return ""
}

// sdnFabricProtocolError returns a PVE-style rejection string if f's
// protocol is unrecognized or f carries a conditional field that does not
// belong to its own protocol (mirroring dnsRecordValueError's per-type
// switch discipline, sdn_dns.go), or "" if f is internally consistent.
// Real PVE's conditional-options schema (the capture's "Conditional
// options:" blocks) means a caller setting, say, csnp_interval on a bgp
// fabric is providing a parameter that create/set for that protocol simply
// does not define — this mock rejects it the same way pvesh's own
// parameter-schema enforcement would, rather than silently accepting and
// ignoring it (that would let a caller believe a field took effect when it
// never could).
func sdnFabricProtocolError(f SDNFabricSpec) string {
	if !sdnFabricProtocols[f.Protocol] {
		return fmt.Sprintf("Parameter verification failed. - protocol: value '%s' does not match the regex pattern", f.Protocol)
	}

	type fieldCheck struct {
		name string
		set  bool
	}
	checks := []fieldCheck{
		{"csnp_interval", f.CSNPInterval != 0},
		{"hello_interval", f.HelloInterval != 0},
		{"area", f.Area != ""},
		{"redistribute", len(f.Redistribute) > 0},
		{"route_filter", f.RouteFilter != ""},
		{"persistent_keepalive", f.PersistentKeepalive != 0},
	}
	var allowed map[string]bool
	switch f.Protocol {
	case "bgp":
		allowed = map[string]bool{"redistribute": true}
	case "openfabric":
		allowed = map[string]bool{"csnp_interval": true, "hello_interval": true, "route_filter": true}
	case "ospf":
		allowed = map[string]bool{"area": true, "redistribute": true, "route_filter": true}
	case "wireguard":
		allowed = map[string]bool{"persistent_keepalive": true}
	}
	for _, c := range checks {
		if c.set && !allowed[c.name] {
			return fmt.Sprintf("Parameter verification failed. - %s: property is not valid for protocol '%s'", c.name, f.Protocol)
		}
	}
	if f.CSNPInterval < 0 || f.CSNPInterval > 600 {
		return "Parameter verification failed. - csnp_interval: value must be between 1 and 600"
	}
	if f.HelloInterval < 0 || f.HelloInterval > 600 {
		return "Parameter verification failed. - hello_interval: value must be between 1 and 600"
	}
	if f.PersistentKeepalive < 0 || f.PersistentKeepalive > 65535 {
		return "Parameter verification failed. - persistent_keepalive: value must be between 0 and 65535"
	}
	return ""
}

// runningFabric derives one fabric's last-applied ("?running=1") value from
// its fixture-loaded (staged) value — the fabric-shaped counterpart of
// runningZone (sdn.go's doc comment on that function applies verbatim,
// substituting "fabric" for "zone").
func runningFabric(f SDNFabricSpec) (SDNFabricSpec, bool) {
	if f.Pending == PendingNew {
		return SDNFabricSpec{}, false
	}
	r := f
	if f.Pending == PendingChanged && f.Running != nil {
		r = *f.Running
		r.ID = f.ID
	}
	r.Pending = PendingNone
	r.Running = nil
	return r, true
}

func (srv *Server) mountSDNFabric(api chi.Router) {
	api.Get("/cluster/sdn/fabrics/fabric", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNFabricsList))
	api.Post("/cluster/sdn/fabrics/fabric", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNFabricCreate))
	api.Get("/cluster/sdn/fabrics/fabric/{fabric}", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNFabricGet))
	api.Put("/cluster/sdn/fabrics/fabric/{fabric}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNFabricUpdate))
	api.Delete("/cluster/sdn/fabrics/fabric/{fabric}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNFabricDelete))

	api.Get("/cluster/sdn/fabrics/all", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNFabricsAll))
	api.Get("/cluster/sdn/fabrics/node", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNFabricNodes))

	// prefix-lists/route-maps (T-3101): GET-only by design — no POST/PUT/
	// DELETE route is registered for either, which
	// TestSDNPrefixListsAndRouteMapsAreReadOnly (internal/change) checks for
	// from the outside via chi's own route table.
	api.Get("/cluster/sdn/prefix-lists", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNPrefixListsList))
	api.Get("/cluster/sdn/route-maps", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNRouteMapsList))
}

func (srv *Server) handleSDNFabricsList(w http.ResponseWriter, r *http.Request) {
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	// T-3101-followup-01 (debt-sweep 2026-08-19): the "?pending=1" view —
	// see sdn.go's isPendingRequest/sdnObjectPendingWire doc comments and
	// planning/reports/evidence/pve-9.2.4-sdn-pending-state.txt §6, which
	// confirmed this exact path (PVE::API2::Network::SDN::Fabrics::Fabric,
	// not the sibling /cluster/sdn/fabrics/all combined read) uses the same
	// pending_config() mechanism zones/vnets/subnets/controllers already
	// model here.
	if isPendingRequest(r) {
		fabrics := srv.state.sdn.fabrics
		out := make([]map[string]any, 0, len(fabrics))
		for _, id := range sortedKeys(fabrics) {
			out = append(out, sdnObjectPendingWire(fabrics[id], fabrics[id].Pending))
		}
		writeData(w, http.StatusOK, out)
		return
	}
	fabrics := srv.state.sdn.fabrics
	if isRunningRequest(r) {
		fabrics = srv.state.sdn.fabricsRunning
	}
	out := make([]SDNFabricSpec, 0, len(fabrics))
	for _, id := range sortedKeys(fabrics) {
		out = append(out, fabrics[id])
	}
	writeData(w, http.StatusOK, out)
}

func (srv *Server) handleSDNFabricGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "fabric")
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	fabrics := srv.state.sdn.fabrics
	if isRunningRequest(r) {
		fabrics = srv.state.sdn.fabricsRunning
	}
	f, ok := fabrics[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("fabric %q not found", id))
		return
	}
	writeData(w, http.StatusOK, f)
}

func (srv *Server) handleSDNFabricCreate(w http.ResponseWriter, r *http.Request) {
	var f SDNFabricSpec
	if err := decodeRequest(r, &f); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if f.ID == "" {
		writeError(w, http.StatusBadRequest, "fabric id (\"id\") is required")
		return
	}
	if msg := sdnFabricParamVerifyError(f.ID); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if msg := sdnFabricProtocolError(f); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	f.Pending = PendingNew
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, exists := srv.state.sdn.fabrics[f.ID]; exists {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("fabric %q already exists", f.ID))
		return
	}
	srv.state.sdn.fabrics[f.ID] = f
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNFabricUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "fabric")
	var f SDNFabricSpec
	if err := decodeRequest(r, &f); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f.ID = id
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	existing, ok := srv.state.sdn.fabrics[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("fabric %q not found", id))
		return
	}
	// Protocol is not editable on update (params_sdn_fabric.go's documented
	// assumption — the capture has no `set`/PUT usage block for this path):
	// preserve the existing protocol regardless of what the caller sent, so
	// this mock never realizes a protocol change real PVE's own `set` form
	// may not support either.
	f.Protocol = existing.Protocol
	if msg := sdnFabricProtocolError(f); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	f.Pending = PendingChanged
	srv.state.sdn.fabrics[id] = f
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNFabricDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "fabric")
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	f, ok := srv.state.sdn.fabrics[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("fabric %q not found", id))
		return
	}
	f.Pending = PendingDeleted
	srv.state.sdn.fabrics[id] = f
	writeData(w, http.StatusOK, nil)
}

// handleSDNFabricsAll implements GET /cluster/sdn/fabrics/all: PVE's
// combined fabrics+nodes read (the capture's `{"fabrics":[],"nodes":[]}`
// shape). Unlike compatServer's own hardcoded sdnFabricsAllResponse (which
// answers this same path for a version-gated caller that never reaches this
// mock's real routes at all), this handler serves live fixture-backed
// content for a caller that talks to the base *Server directly.
func (srv *Server) handleSDNFabricsAll(w http.ResponseWriter, r *http.Request) {
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	fabrics := make([]SDNFabricSpec, 0, len(srv.state.sdn.fabrics))
	for _, id := range sortedKeys(srv.state.sdn.fabrics) {
		fabrics = append(fabrics, srv.state.sdn.fabrics[id])
	}
	nodes := make([]SDNFabricNodeSpec, len(srv.state.sdn.fabricNodes))
	copy(nodes, srv.state.sdn.fabricNodes)
	// Both keys are always emitted as arrays (never null), matching the
	// capture's `{"fabrics":[],"nodes":[]}` shape exactly even when both are
	// empty — real PVE's own "no fabrics configured" response, not this
	// mock inventing a distinct "absent" shape.
	writeData(w, http.StatusOK, map[string]any{"fabrics": fabrics, "nodes": nodes})
}

func (srv *Server) handleSDNFabricNodes(w http.ResponseWriter, r *http.Request) {
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	nodes := make([]SDNFabricNodeSpec, len(srv.state.sdn.fabricNodes))
	copy(nodes, srv.state.sdn.fabricNodes)
	writeData(w, http.StatusOK, nodes)
}

func (srv *Server) handleSDNPrefixListsList(w http.ResponseWriter, r *http.Request) {
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	out := make([]SDNPrefixListSpec, len(srv.state.sdn.prefixLists))
	copy(out, srv.state.sdn.prefixLists)
	writeData(w, http.StatusOK, out)
}

func (srv *Server) handleSDNRouteMapsList(w http.ResponseWriter, r *http.Request) {
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	out := make([]SDNRouteMapSpec, len(srv.state.sdn.routeMaps))
	copy(out, srv.state.sdn.routeMaps)
	writeData(w, http.StatusOK, out)
}
