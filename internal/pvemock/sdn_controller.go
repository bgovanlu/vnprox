package pvemock

import (
	"fmt"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
)

// SDN Controllers mock (T-3102), closely mirroring sdn_fabric.go's
// fixture-backed state under srv.state.sdn, guarded by the same sdn.mu
// mutex sdn.go/sdn_dns.go/sdn_fabric.go already use. See
// internal/pve/sdn_controller.go for the real-shape package doc comment
// this file realizes server-side.

// sdnControllerIDPattern is real PVE's controller id charset, captured
// verbatim (planning/reports/evidence/pve-9.2.4-sdn-schema.txt):
//
//	[a-zA-Z][a-zA-Z0-9_-]*[a-zA-Z0-9]
var sdnControllerIDPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*[a-zA-Z0-9]$`)

// sdnControllerTypes is the four controller types the capture's
// `--type <bgp | evpn | faucet | isis>` enumerates.
var sdnControllerTypes = map[string]bool{
	"bgp": true, "evpn": true, "faucet": true, "isis": true,
}

// sdnControllerTypeFields mirrors internal/change/validate_schema.go's own
// sdnControllerTypeFields exactly — the server-side half of the same rule,
// so the two can never quietly disagree about which combination is legal
// (the identical discipline sdnFabricProtocolError already keeps with
// validate_schema.go's own sdnFabricProtocolFields).
var sdnControllerTypeFields = map[string]map[string]bool{
	"bgp": {
		"asn": true, "bgp_mode": true, "bgp_multipath_as_path_relax": true,
		"ebgp": true, "ebgp_multihop": true, "peers": true,
	},
	"evpn": {
		"fabric": true, "peer_group_name": true, "route_map_in": true, "route_map_out": true,
	},
	"isis": {
		"isis_domain": true, "isis_ifaces": true, "isis_net": true,
	},
	"faucet": {},
}

// sdnControllerParamVerifyError returns a PVE-style rejection string if id
// is not a valid controller id, or "" if it is acceptable.
func sdnControllerParamVerifyError(id string) string {
	if id != "" && !sdnControllerIDPattern.MatchString(id) {
		return fmt.Sprintf("Parameter verification failed. - controller: value '%s' does not match the regex pattern", id)
	}
	return ""
}

// sdnControllerTypeError returns a PVE-style rejection string if ctl's type
// is unrecognized or ctl carries a type-conditional field that does not
// belong to its own type, or "" if ctl is internally consistent — the
// controller counterpart of sdnFabricProtocolError.
func sdnControllerTypeError(ctl SDNControllerSpec) string {
	if !sdnControllerTypes[ctl.Type] {
		return fmt.Sprintf("Parameter verification failed. - type: value '%s' does not match the regex pattern", ctl.Type)
	}

	type fieldCheck struct {
		name string
		set  bool
	}
	checks := []fieldCheck{
		{"asn", ctl.ASN != 0},
		{"bgp_mode", ctl.BgpMode != ""},
		{"bgp_multipath_as_path_relax", ctl.BgpMultipathAsPathRelax},
		{"ebgp", ctl.Ebgp},
		{"ebgp_multihop", ctl.EbgpMultihop != 0},
		{"peers", len(ctl.Peers) > 0},
		{"fabric", ctl.Fabric != ""},
		{"peer_group_name", ctl.PeerGroupName != ""},
		{"route_map_in", ctl.RouteMapIn != ""},
		{"route_map_out", ctl.RouteMapOut != ""},
		{"isis_domain", ctl.IsisDomain != ""},
		{"isis_ifaces", len(ctl.IsisIfaces) > 0},
		{"isis_net", ctl.IsisNet != ""},
	}
	allowed := sdnControllerTypeFields[ctl.Type]
	for _, c := range checks {
		if c.set && !allowed[c.name] {
			return fmt.Sprintf("Parameter verification failed. - %s: property is not valid for type '%s'", c.name, ctl.Type)
		}
	}
	if ctl.ASN < 0 || ctl.ASN > 4294967295 {
		return "Parameter verification failed. - asn: value must be between 0 and 4294967295"
	}
	if ctl.BgpMode != "" && ctl.BgpMode != "auto" && ctl.BgpMode != "external" && ctl.BgpMode != "internal" {
		return "Parameter verification failed. - bgp_mode: value must be one of auto, external, internal"
	}
	return ""
}

// runningController derives one controller's last-applied ("?running=1")
// value from its fixture-loaded (staged) value — the controller-shaped
// counterpart of runningFabric (sdn_fabric.go's doc comment applies
// verbatim, substituting "controller" for "fabric").
func runningController(ctl SDNControllerSpec) (SDNControllerSpec, bool) {
	if ctl.Pending == PendingNew {
		return SDNControllerSpec{}, false
	}
	r := ctl
	if ctl.Pending == PendingChanged && ctl.Running != nil {
		r = *ctl.Running
		r.ID = ctl.ID
	}
	r.Pending = PendingNone
	r.Running = nil
	return r, true
}

func (srv *Server) mountSDNController(api chi.Router) {
	api.Get("/cluster/sdn/controllers", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNControllersList))
	api.Post("/cluster/sdn/controllers", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNControllerCreate))
	api.Get("/cluster/sdn/controllers/{controller}", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNControllerGet))
	api.Put("/cluster/sdn/controllers/{controller}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNControllerUpdate))
	api.Delete("/cluster/sdn/controllers/{controller}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNControllerDelete))
}

func (srv *Server) handleSDNControllersList(w http.ResponseWriter, r *http.Request) {
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	// T-3101-followup-01 (debt-sweep 2026-08-19): the "?pending=1" view —
	// see sdn.go's isPendingRequest/sdnObjectPendingWire doc comments and
	// planning/reports/evidence/pve-9.2.4-sdn-pending-state.txt §6, which
	// confirmed a controller's real PVE GET handler uses the exact same
	// pending_config() mechanism zones/vnets/subnets already model here.
	if isPendingRequest(r) {
		controllers := srv.state.sdn.controllers
		out := make([]map[string]any, 0, len(controllers))
		for _, id := range sortedKeys(controllers) {
			out = append(out, sdnObjectPendingWire(controllers[id], controllers[id].Pending))
		}
		writeData(w, http.StatusOK, out)
		return
	}
	controllers := srv.state.sdn.controllers
	if isRunningRequest(r) {
		controllers = srv.state.sdn.controllersRunning
	}
	out := make([]SDNControllerSpec, 0, len(controllers))
	for _, id := range sortedKeys(controllers) {
		out = append(out, controllers[id])
	}
	writeData(w, http.StatusOK, out)
}

func (srv *Server) handleSDNControllerGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "controller")
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	controllers := srv.state.sdn.controllers
	if isRunningRequest(r) {
		controllers = srv.state.sdn.controllersRunning
	}
	ctl, ok := controllers[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("controller %q not found", id))
		return
	}
	writeData(w, http.StatusOK, ctl)
}

func (srv *Server) handleSDNControllerCreate(w http.ResponseWriter, r *http.Request) {
	var ctl SDNControllerSpec
	if err := decodeRequest(r, &ctl); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if ctl.ID == "" {
		writeError(w, http.StatusBadRequest, "controller id (\"controller\") is required")
		return
	}
	if msg := sdnControllerParamVerifyError(ctl.ID); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if msg := sdnControllerTypeError(ctl); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	ctl.Pending = PendingNew
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, exists := srv.state.sdn.controllers[ctl.ID]; exists {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("controller %q already exists", ctl.ID))
		return
	}
	srv.state.sdn.controllers[ctl.ID] = ctl
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNControllerUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "controller")
	var ctl SDNControllerSpec
	if err := decodeRequest(r, &ctl); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctl.ID = id
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	existing, ok := srv.state.sdn.controllers[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("controller %q not found", id))
		return
	}
	// Type is not editable on update (params_sdn_controller.go's documented
	// assumption — the capture has no `set`/PUT usage block for this path):
	// preserve the existing type regardless of what the caller sent, the
	// same convention handleSDNFabricUpdate already applies to a fabric's
	// protocol.
	ctl.Type = existing.Type
	if msg := sdnControllerTypeError(ctl); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	ctl.Pending = PendingChanged
	srv.state.sdn.controllers[id] = ctl
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNControllerDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "controller")
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	ctl, ok := srv.state.sdn.controllers[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("controller %q not found", id))
		return
	}
	ctl.Pending = PendingDeleted
	srv.state.sdn.controllers[id] = ctl
	writeData(w, http.StatusOK, nil)
}
