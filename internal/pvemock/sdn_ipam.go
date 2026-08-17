package pvemock

import (
	"fmt"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
)

// SDN IPAM plugin-instance write path mock (T-3104), closely mirroring
// sdn_controller.go's structure — fixture-backed state under
// srv.state.sdn.ipams/ipamsRunning (state.go), guarded by the same sdn.mu
// mutex sdn.go/sdn_fabric.go/sdn_controller.go already use. See
// internal/pve/sdn_ipam.go for the real-shape package doc comment this file
// realizes server-side. The existing GET routes (ipam.go's
// mountIPAM/handleIPAMList/handleIPAMGet/handleIPAMStatus) are unchanged in
// shape — only their backing state moved from the immutable fixture to the
// mutable maps this file's handlers write into.

// sdnIpamIDPattern is real PVE's ipam id charset, captured verbatim
// (planning/reports/evidence/pve-9.2.4-sdn-schema.txt):
//
//	[a-zA-Z][a-zA-Z0-9]*[a-zA-Z0-9]
var sdnIpamIDPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]*[a-zA-Z0-9]$`)

// sdnIpamTypes is the three ipam types the capture's
// `--type <netbox | phpipam | pve>` enumerates.
var sdnIpamTypes = map[string]bool{
	"netbox": true, "phpipam": true, "pve": true,
}

// sdnIpamTypeFields mirrors internal/change/validate_schema.go's own
// sdnIpamTypeFields/sdnIpamRequiredFields exactly — the server-side half of
// the same (this task's own documented inference, not a captured fact —
// see that file's doc comment) rule, so the two can never quietly disagree.
var sdnIpamTypeFields = map[string]map[string]bool{
	"netbox":  {"url": true, "token": true, "section": true, "fingerprint": true},
	"phpipam": {"url": true, "token": true, "section": true, "fingerprint": true},
	"pve":     {},
}

var sdnIpamRequiredFields = map[string]map[string]bool{
	"netbox":  {"url": true, "token": true},
	"phpipam": {"url": true, "token": true},
	"pve":     {},
}

// sdnIpamParamVerifyError returns a PVE-style rejection string if id is not
// a valid ipam id, or "" if it is acceptable.
func sdnIpamParamVerifyError(id string) string {
	if id != "" && !sdnIpamIDPattern.MatchString(id) {
		return fmt.Sprintf("Parameter verification failed. - ipam: value '%s' does not match the regex pattern", id)
	}
	return ""
}

// sdnIpamTypeError returns a PVE-style rejection string if ip's type is
// unrecognized, ip carries a type-conditional field that does not belong to
// its own type, or ip's type is missing one of its required fields — or ""
// if ip is internally consistent. The ipam counterpart of
// sdnControllerTypeError/sdnFabricProtocolError.
func sdnIpamTypeError(ip SDNIpamSpec) string {
	if !sdnIpamTypes[ip.Type] {
		return fmt.Sprintf("Parameter verification failed. - type: value '%s' does not match the regex pattern", ip.Type)
	}

	type fieldCheck struct {
		name string
		set  bool
	}
	checks := []fieldCheck{
		{"url", ip.URL != ""},
		{"token", ip.Token != ""},
		{"section", ip.Section != 0},
		{"fingerprint", ip.Fingerprint != ""},
	}
	allowed := sdnIpamTypeFields[ip.Type]
	for _, c := range checks {
		if c.set && !allowed[c.name] {
			return fmt.Sprintf("Parameter verification failed. - %s: property is not valid for type '%s'", c.name, ip.Type)
		}
	}
	required := sdnIpamRequiredFields[ip.Type]
	if required["url"] && ip.URL == "" {
		return fmt.Sprintf("Parameter verification failed. - url: property is required for type '%s'", ip.Type)
	}
	if required["token"] && ip.Token == "" {
		return fmt.Sprintf("Parameter verification failed. - token: property is required for type '%s'", ip.Type)
	}
	return ""
}

// runningIpam derives one ipam instance's last-applied ("?running=1") value
// from its fixture-loaded (staged) value — the ipam-shaped counterpart of
// runningController/runningFabric (sdn_fabric.go's doc comment applies
// verbatim, substituting "ipam" for "fabric").
func runningIpam(ip SDNIpamSpec) (SDNIpamSpec, bool) {
	if ip.Pending == PendingNew {
		return SDNIpamSpec{}, false
	}
	r := ip
	if ip.Pending == PendingChanged && ip.Running != nil {
		r = *ip.Running
		r.ID = ip.ID
	}
	r.Pending = PendingNone
	r.Running = nil
	return r, true
}

func (srv *Server) mountSDNIpam(api chi.Router) {
	api.Post("/cluster/sdn/ipams", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNIpamCreate))
	api.Put("/cluster/sdn/ipams/{ipam}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNIpamUpdate))
	api.Delete("/cluster/sdn/ipams/{ipam}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNIpamDelete))
}

func (srv *Server) handleSDNIpamCreate(w http.ResponseWriter, r *http.Request) {
	var ip SDNIpamSpec
	if err := decodeRequest(r, &ip); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if ip.ID == "" {
		writeError(w, http.StatusBadRequest, "ipam id (\"ipam\") is required")
		return
	}
	if msg := sdnIpamParamVerifyError(ip.ID); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if msg := sdnIpamTypeError(ip); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	ip.Pending = PendingNew
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, exists := srv.state.sdn.ipams[ip.ID]; exists {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("ipam %q already exists", ip.ID))
		return
	}
	srv.state.sdn.ipams[ip.ID] = ip
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNIpamUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "ipam")
	var ip SDNIpamSpec
	if err := decodeRequest(r, &ip); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ip.ID = id
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	existing, ok := srv.state.sdn.ipams[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("ipam %q not found", id))
		return
	}
	// Type is not editable on update (params_sdn_ipam.go's documented
	// assumption — the capture has no `set`/PUT usage block for this path):
	// preserve the existing type regardless of what the caller sent, the
	// same convention handleSDNFabricUpdate/handleSDNControllerUpdate
	// already apply to their own type-like field.
	ip.Type = existing.Type
	if msg := sdnIpamTypeError(ip); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	ip.Pending = PendingChanged
	srv.state.sdn.ipams[id] = ip
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNIpamDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "ipam")
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	ip, ok := srv.state.sdn.ipams[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("ipam %q not found", id))
		return
	}
	ip.Pending = PendingDeleted
	srv.state.sdn.ipams[id] = ip
	writeData(w, http.StatusOK, nil)
}
