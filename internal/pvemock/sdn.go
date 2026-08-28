// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
)

// sdnIDPattern is real PVE's SDN zone/vnet id charset (case-insensitively
// `[a-z][a-z0-9]*`). A create with an id outside it is rejected with a
// PVE-style "Parameter verification failed" 400 — the literal mid-apply
// error issue #3 reported, kept here so a raw/non-wizard caller that slips
// past change.schemaSDNName can't silently re-hide the gap in CI. Exact
// real-PVE wording/length-cap is unconfirmed against live hardware
// (planning/reports/needs-hardware-validation.md); this mirrors vnprox's
// own charset check, not any length rule.
var sdnIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// sdnParamVerifyError returns a PVE-style rejection string if id is not a
// valid SDN object id, or "" if it is acceptable.
func sdnParamVerifyError(kind, id string) string {
	if id != "" && !sdnIDPattern.MatchString(id) {
		return fmt.Sprintf("Parameter verification failed. - %s: value '%s' does not match the regex pattern", kind, id)
	}
	return ""
}

// runningZone derives one zone's last-applied ("?running=1") value from its
// fixture-loaded (staged) value, per SDNZoneSpec.Running's doc comment:
// absent for a staged "new" object (ok=false), the fixture-declared
// Running override (or, absent that, the staged value itself with the
// marker cleared) for "changed", and the staged value with the marker
// cleared for "" and "deleted" — a "deleted" object is still fully present
// in the running config until an apply actually removes it.
func runningZone(z SDNZoneSpec) (SDNZoneSpec, bool) {
	if z.Pending == PendingNew {
		return SDNZoneSpec{}, false
	}
	r := z
	if z.Pending == PendingChanged && z.Running != nil {
		r = *z.Running
		r.ID = z.ID
	}
	r.Pending = PendingNone
	r.Running = nil
	return r, true
}

func runningVnet(v SDNVnetSpec) (SDNVnetSpec, bool) {
	if v.Pending == PendingNew {
		return SDNVnetSpec{}, false
	}
	r := v
	if v.Pending == PendingChanged && v.Running != nil {
		r = *v.Running
		r.ID = v.ID
		r.Zone = v.Zone
	}
	r.Pending = PendingNone
	r.Running = nil
	return r, true
}

func runningSubnet(s SDNSubnetSpec) (SDNSubnetSpec, bool) {
	if s.Pending == PendingNew {
		return SDNSubnetSpec{}, false
	}
	r := s
	if s.Pending == PendingChanged && s.Running != nil {
		r = *s.Running
		r.ID = s.ID
		r.Vnet = s.Vnet
	}
	r.Pending = PendingNone
	r.Running = nil
	return r, true
}

// isRunningRequest reports whether r asked for the last-applied
// ("?running=1") view of an SDN listing/get route, rather than the default
// staged (pending-merged) view — T-401's addition, docs/api.md's /sdn
// section and internal/pve/sdn.go's ListSDN*Running doc comments.
func isRunningRequest(r *http.Request) bool {
	return r.URL.Query().Get("running") == "1"
}

// isPendingRequest reports whether r asked for the real-PVE "?pending=1"
// view (T-3101-followup-01, planning/reports/evidence/
// pve-9.2.4-sdn-pending-state.txt) — a THIRD view, distinct from both the
// default (staged) view and "?running=1" (isRunningRequest above): a
// per-object "state" (new|changed|deleted, absent when in sync) plus a
// "pending" sub-object of the object's own field values, mirroring real
// PVE's own pending_config() merge. Added for foreign-pending-state
// detection; deliberately does not touch the existing default/"?running=1"
// handling above it.
func isPendingRequest(r *http.Request) bool {
	return r.URL.Query().Get("pending") == "1"
}

// sdnObjectPendingWire renders one zone/vnet/subnet/controller/fabric spec
// as one "?pending=1" list entry (isPendingRequest's doc comment) — reused
// as-is by sdn_controller.go/sdn_fabric.go's own list handlers (debt-sweep
// 2026-08-19 follow-up), since the shape this function builds is generic
// over any spec type, not specific to zone/vnet/subnet. v is
// marshaled through its own JSON tags (so the identity field — "zone"/
// "vnet"/"subnet"/"controller"/"id" — and every other exported field
// round-trip exactly as the plain default-view handlers already serve
// them), then:
//   - its existing top-level "pending" key (SDNZoneSpec/SDNVnetSpec/
//     SDNSubnetSpec's own PendingState string field) is removed — real
//     PVE's "?pending=1" view uses "pending" for a DIFFERENT purpose (a
//     sub-object), never as that string marker, and the default view
//     never carries a "pending" key at all (confirmed against real PVE:
//     see the evidence file's §1) — so nothing here can collide with it.
//   - if state is PendingNone (in sync), the object is returned
//     otherwise unchanged, mirroring real PVE's own in-sync response
//     (confirmed live against pvecube's labz: no "state"/"pending" keys).
//   - otherwise a top-level "state" is set to state, and "pending" is set
//     to every OTHER field's current value. Real PVE's own pending_config
//     narrows this to only the fields that actually differ from the
//     running config; this mock intentionally returns the object's full
//     current field set instead (a superset, not a subset) — still an
//     honest representation of exactly what a sdn.apply would commit for
//     this object (never omitting anything, never fabricating a value),
//     just not byte-identical to real PVE's narrower field-diff. Flagged
//     as a known simplification in this task's completion report.
func sdnObjectPendingWire(v any, state PendingState) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{}
	}
	delete(m, "pending")
	if state == PendingNone {
		return m
	}
	fields := make(map[string]any, len(m))
	for k, val := range m {
		fields[k] = val
	}
	m["state"] = string(state)
	m["pending"] = fields
	return m
}

func (srv *Server) mountSDN(api chi.Router) {
	api.Get("/cluster/sdn/zones", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNZonesList))
	api.Post("/cluster/sdn/zones", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNZoneCreate))
	api.Get("/cluster/sdn/zones/{zone}", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNZoneGet))
	api.Put("/cluster/sdn/zones/{zone}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNZoneUpdate))
	api.Delete("/cluster/sdn/zones/{zone}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNZoneDelete))
	// T-3701: GET /cluster/sdn/zones/{zone}/status does not exist on real PVE
	// 9.2.4 (it returns 501) — this mock used to invent it. The real
	// endpoint is per-NODE: GET /nodes/{node}/sdn/zones (registered below,
	// alongside this package's other /nodes/{node}/... routes).
	api.Get("/nodes/{node}/sdn/zones", srv.requirePrivilege(PrivSDNAudit, srv.handleNodeSDNZonesStatus))

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
	if isPendingRequest(r) {
		zones := srv.state.sdn.zones
		out := make([]map[string]any, 0, len(zones))
		for _, id := range sortedKeys(zones) {
			out = append(out, sdnObjectPendingWire(zones[id], zones[id].Pending))
		}
		writeData(w, http.StatusOK, out)
		return
	}
	zones := srv.state.sdn.zones
	if isRunningRequest(r) {
		zones = srv.state.sdn.zonesRunning
	}
	// T-2502-followup-01: zones is a map, whose iteration order is
	// randomized; sort by ID (the map's own key, and the field callers
	// identify a zone by) so the response is deterministic.
	out := make([]SDNZoneSpec, 0, len(zones))
	for _, id := range sortedKeys(zones) {
		out = append(out, zones[id])
	}
	writeData(w, http.StatusOK, out)
}

func (srv *Server) handleSDNZoneGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "zone")
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	zones := srv.state.sdn.zones
	if isRunningRequest(r) {
		zones = srv.state.sdn.zonesRunning
	}
	z, ok := zones[id]
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
	if msg := sdnParamVerifyError("zone", z.ID); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
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

// zoneStatusEntry is one zone's realization status on a single node, as
// reported by GET /nodes/{node}/sdn/zones — the endpoint real PVE 9.2.4
// actually serves (confirmed live,
// planning/reports/evidence/pve-9.2.4-sdn-zone-status.txt). This mock used
// to invent GET /cluster/sdn/zones/{zone}/status instead (T-3701), which
// real PVE returns 501 for. Real PVE's response carries only "zone" and
// "status" per entry — no "node" (the {node} path segment already says
// which node this is) and no detail/message explaining a non-ok status;
// this mock matches that exactly (json tags below) rather than inventing an
// explanation PVE itself doesn't give — see internal/pve.SDNZoneStatus's
// doc comment, which this wire shape must keep matching.
type zoneStatusEntry struct {
	Zone   string `json:"zone"`
	Status string `json:"status"` // ok|pending|error
}

// zoneAssignedToNode reports whether zone z is realized on node: every name
// in z.Nodes, or — when z.Nodes is empty — every node, matching real
// Proxmox SDN's own well-documented "no --nodes restriction deploys the
// zone cluster-wide" convention (the same assumption
// pve.ReconcileSDNZoneStatus makes; the two must agree, or a zone created
// with no explicit node list — the common case, e.g. every existing
// SdnZoneCreateParams in this codebase's own test suite that omits Nodes —
// would report a fabricated "unknown" on every node instead of the "ok" a
// real deployment would show). Unconfirmed on this project's own cluster
// specifically (creating an unrestricted zone would be a mutating write
// against a live host) — flagged in
// planning/reports/needs-hardware-validation.md.
func zoneAssignedToNode(z SDNZoneSpec, node string) bool {
	if len(z.Nodes) == 0 {
		return true
	}
	for _, n := range z.Nodes {
		if n == node {
			return true
		}
	}
	return false
}

// handleNodeSDNZonesStatus serves GET /nodes/{node}/sdn/zones: node's own
// realization status for every zone assigned to it (T-3701). Confirmed live
// against a real two-node cluster
// (planning/reports/evidence/pve-9.2.4-cluster-vnprox-dev.txt) that PVE can
// legitimately answer this with an empty array for a node whose local SDN
// config has not yet been generated, regardless of what's actually
// assigned there — MockOptions.SDNZonesUnavailable (settable per-node via
// fixture or POST /mock/nodes/{node}/sdn-zones-unavailable, mirroring
// SDNZoneStatusFail's exact pattern) reproduces that case so a test can
// exercise real cross-node divergence (one node "error", a sibling node
// answering the very same zone with nothing at all) against this mock
// rather than only against hand-rolled fakes.
func (srv *Server) handleNodeSDNZonesStatus(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}

	ns.mu.RLock()
	unavailable := ns.mock.SDNZonesUnavailable
	fail := ns.mock.SDNZoneStatusFail
	ifaces := ns.network
	ns.mu.RUnlock()

	if unavailable {
		writeData(w, http.StatusOK, []zoneStatusEntry{})
		return
	}

	srv.state.sdn.mu.RLock()
	zones := srv.state.sdn.zones
	ids := sortedKeys(zones)
	out := make([]zoneStatusEntry, 0, len(ids))
	for _, id := range ids {
		z := zones[id]
		if !zoneAssignedToNode(z, node) {
			continue
		}
		entry := zoneStatusEntry{Zone: z.ID, Status: "ok"}
		switch {
		case fail:
			// T-402: a node-level injected failure (POST /mock/nodes/{node}/
			// sdn-status-fail) always wins — it models a node whose SDN apply
			// task itself reported success but which nonetheless failed to
			// realize the config, independent of (and not detectable by) any
			// static pre-apply check like bridge existence.
			entry.Status = "error"
		case z.Pending != PendingNone:
			entry.Status = "pending"
		case z.Bridge != "" && (z.Type == "simple" || z.Type == "vlan") && !ifaceExists(ifaces, z.Bridge):
			entry.Status = "error"
		}
		out = append(out, entry)
	}
	srv.state.sdn.mu.RUnlock()
	writeData(w, http.StatusOK, out)
}

func (srv *Server) handleSDNVnetsList(w http.ResponseWriter, r *http.Request) {
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	if isPendingRequest(r) {
		vnets := srv.state.sdn.vnets
		out := make([]map[string]any, 0, len(vnets))
		for _, id := range sortedKeys(vnets) {
			out = append(out, sdnObjectPendingWire(vnets[id], vnets[id].Pending))
		}
		writeData(w, http.StatusOK, out)
		return
	}
	vnets := srv.state.sdn.vnets
	if isRunningRequest(r) {
		vnets = srv.state.sdn.vnetsRunning
	}
	// T-2502-followup-01: vnets is a map, whose iteration order is
	// randomized; sort by ID (the map's own key) so the response is
	// deterministic.
	out := make([]SDNVnetSpec, 0, len(vnets))
	for _, id := range sortedKeys(vnets) {
		out = append(out, vnets[id])
	}
	writeData(w, http.StatusOK, out)
}

func (srv *Server) handleSDNVnetGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vnet")
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	vnets := srv.state.sdn.vnets
	if isRunningRequest(r) {
		vnets = srv.state.sdn.vnetsRunning
	}
	v, ok := vnets[id]
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
	if msg := sdnParamVerifyError("vnet", v.ID); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
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
	if isPendingRequest(r) {
		subnets := srv.state.sdn.subnets
		var out []map[string]any
		for _, id := range sortedKeys(subnets) {
			s := subnets[id]
			if s.Vnet != vnet {
				continue
			}
			out = append(out, sdnObjectPendingWire(s, s.Pending))
		}
		writeData(w, http.StatusOK, out)
		return
	}
	subnets := srv.state.sdn.subnets
	if isRunningRequest(r) {
		subnets = srv.state.sdn.subnetsRunning
	}
	// T-2502-followup-01: subnets is a map, whose iteration order is
	// randomized; sort by ID (the map's own key) so the response is
	// deterministic.
	var out []SDNSubnetSpec
	for _, id := range sortedKeys(subnets) {
		s := subnets[id]
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
	subnets := srv.state.sdn.subnets
	if isRunningRequest(r) {
		subnets = srv.state.sdn.subnetsRunning
	}
	s, ok := subnets[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("subnet %q not found", id))
		return
	}
	writeData(w, http.StatusOK, s)
}

// subnetGatewayError returns a PVE-style 400 rejection message for s, or ""
// if s is acceptable — T-701 pvemock fidelity: real PVE's SubnetPlugin
// rejects both shapes at subnet create/update time (T-701 root-cause
// analysis §4): SNAT enabled with no gateway, and a gateway that falls
// outside the subnet's own CIDR. This mirrors this codebase's own
// change.schemaGatewayInCIDR/codeSNATRequiresGateway checks so the same
// two shapes vnprox's own validators block are also rejected server-side
// (closing the "pvemock is more permissive than real PVE" gap raw/
// non-wizard callers could otherwise slip through in CI) — exact PVE error
// wording/version is unconfirmed against a live cluster, flagged in
// planning/reports/needs-hardware-validation.md.
func subnetGatewayError(s SDNSubnetSpec) string {
	if s.SNAT && s.Gateway == "" {
		return fmt.Sprintf("subnet %q: snat requires a gateway", s.ID)
	}
	if s.Gateway == "" || s.CIDR == "" {
		return ""
	}
	_, ipnet, err := net.ParseCIDR(s.CIDR)
	if err != nil {
		return ""
	}
	ip := net.ParseIP(s.Gateway)
	if ip == nil || !ipnet.Contains(ip) {
		return fmt.Sprintf("subnet %q: gateway %q is not contained in subnet %q", s.ID, s.Gateway, s.CIDR)
	}
	return ""
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
	if msg := subnetGatewayError(s); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, exists := srv.state.sdn.subnets[s.ID]; exists {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("subnet %q already exists", s.ID))
		return
	}
	vnetSpec, ok := srv.state.sdn.vnets[vnet]
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("vnet %q does not exist", vnet))
		return
	}
	s.Pending = PendingNew
	srv.state.sdn.subnets[s.ID] = s
	srv.registerSubnetGateway(vnetSpec.Zone, vnet, s.CIDR, s.Gateway)
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
	if msg := subnetGatewayError(s); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, ok := srv.state.sdn.subnets[id]; !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("subnet %q not found", id))
		return
	}
	s.Pending = PendingChanged
	srv.state.sdn.subnets[id] = s
	vnetSpec := srv.state.sdn.vnets[s.Vnet]
	srv.registerSubnetGateway(vnetSpec.Zone, s.Vnet, s.CIDR, s.Gateway)
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
	// T-2502-followup-01: zones/vnets/subnets are maps, whose iteration
	// order is randomized; sort each group by ID (the map's own key) so
	// the response is deterministic.
	var out []sdnStatusEntry
	for _, id := range sortedKeys(srv.state.sdn.zones) {
		z := srv.state.sdn.zones[id]
		out = append(out, sdnStatusEntry{Kind: "zone", ID: z.ID, Pending: z.Pending})
	}
	for _, id := range sortedKeys(srv.state.sdn.vnets) {
		v := srv.state.sdn.vnets[id]
		out = append(out, sdnStatusEntry{Kind: "vnet", ID: v.ID, Pending: v.Pending})
	}
	for _, id := range sortedKeys(srv.state.sdn.subnets) {
		s := srv.state.sdn.subnets[id]
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
		// T-3101: fabrics apply through this same PUT /cluster/sdn commit —
		// no bespoke apply path (internal/change/op.go's OpSdnFabricCreate
		// doc comment).
		for id, f := range srv.state.sdn.fabrics {
			if f.Pending == PendingDeleted {
				delete(srv.state.sdn.fabrics, id)
				continue
			}
			f.Pending = PendingNone
			srv.state.sdn.fabrics[id] = f
		}
		// T-3102: controllers apply through this same commit too. This loop
		// (and controllersRunning's own resync below) was missing until
		// T-3104 added the identical ipam case right after it and noticed no
		// test had ever exercised a controller surviving an apply — a
		// pre-existing gap from T-3102, fixed here as a drive-by rather than
		// left to silently diverge from the pattern this same commit adds
		// for ipams.
		for id, c := range srv.state.sdn.controllers {
			if c.Pending == PendingDeleted {
				delete(srv.state.sdn.controllers, id)
				continue
			}
			c.Pending = PendingNone
			srv.state.sdn.controllers[id] = c
		}
		// T-3104: ipam plugin instances apply through this same commit too —
		// no bespoke apply path (internal/change/op.go's OpSdnIpamCreate doc
		// comment).
		for id, i := range srv.state.sdn.ipams {
			if i.Pending == PendingDeleted {
				delete(srv.state.sdn.ipams, id)
				continue
			}
			i.Pending = PendingNone
			srv.state.sdn.ipams[id] = i
		}

		// T-401: the running (last-applied) view now matches the
		// just-applied staged view exactly — a deep copy with every
		// Pending marker already cleared above and every Running override
		// dropped (it described the pre-apply value, now obsolete).
		srv.state.sdn.zonesRunning = make(map[string]SDNZoneSpec, len(srv.state.sdn.zones))
		for id, z := range srv.state.sdn.zones {
			z.Running = nil
			srv.state.sdn.zonesRunning[id] = z
		}
		srv.state.sdn.vnetsRunning = make(map[string]SDNVnetSpec, len(srv.state.sdn.vnets))
		for id, v := range srv.state.sdn.vnets {
			v.Running = nil
			srv.state.sdn.vnetsRunning[id] = v
		}
		srv.state.sdn.subnetsRunning = make(map[string]SDNSubnetSpec, len(srv.state.sdn.subnets))
		for id, s := range srv.state.sdn.subnets {
			s.Running = nil
			srv.state.sdn.subnetsRunning[id] = s
		}
		srv.state.sdn.fabricsRunning = make(map[string]SDNFabricSpec, len(srv.state.sdn.fabrics))
		for id, f := range srv.state.sdn.fabrics {
			f.Running = nil
			srv.state.sdn.fabricsRunning[id] = f
		}
		srv.state.sdn.controllersRunning = make(map[string]SDNControllerSpec, len(srv.state.sdn.controllers))
		for id, c := range srv.state.sdn.controllers {
			c.Running = nil
			srv.state.sdn.controllersRunning[id] = c
		}
		srv.state.sdn.ipamsRunning = make(map[string]SDNIpamSpec, len(srv.state.sdn.ipams))
		for id, i := range srv.state.sdn.ipams {
			i.Running = nil
			srv.state.sdn.ipamsRunning[id] = i
		}
	})
	writeData(w, http.StatusOK, task.UPID)
}
