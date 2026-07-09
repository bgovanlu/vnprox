package pvemock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// networkView merges live and staged (interfaces.new) configs the way real
// PVE's `GET /nodes/{node}/network` does, annotating each entry with
// whether it is new/changed/deleted relative to the live config. A nil
// pending slice means "no staged changes" and the live config is returned
// unannotated.
func networkView(live, pending []NetIface) []NetIface {
	if pending == nil {
		return append([]NetIface(nil), live...)
	}
	liveByName := make(map[string]NetIface, len(live))
	for _, i := range live {
		liveByName[i.Iface] = i
	}
	seen := make(map[string]bool, len(pending))
	out := make([]NetIface, 0, len(live)+len(pending))
	for _, p := range pending {
		seen[p.Iface] = true
		entry := p
		if l, ok := liveByName[p.Iface]; ok {
			if !ifaceContentEqual(l, p) {
				entry.Pending = PendingChanged
			}
		} else {
			entry.Pending = PendingNew
		}
		out = append(out, entry)
	}
	for _, l := range live {
		if seen[l.Iface] {
			continue
		}
		entry := l
		entry.Pending = PendingDeleted
		out = append(out, entry)
	}
	return out
}

// ifaceContentEqual compares two NetIface entries ignoring the Pending
// annotation field (which only ever appears in API responses, never in the
// stored config itself).
func ifaceContentEqual(a, b NetIface) bool {
	a.Pending, b.Pending = "", ""
	return a == b
}

func (srv *Server) handleNetworkList(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}
	ns.mu.RLock()
	view := networkView(ns.network, ns.networkPending)
	ns.mu.RUnlock()
	writeData(w, http.StatusOK, view)
}

func (srv *Server) handleNetworkGet(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	iface := chi.URLParam(r, "iface")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}
	ns.mu.RLock()
	view := networkView(ns.network, ns.networkPending)
	ns.mu.RUnlock()
	for _, e := range view {
		if e.Iface == iface {
			writeData(w, http.StatusOK, e)
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("iface %q not found on node %q", iface, node))
}

// ensureStaged returns ns.networkPending, initializing it as a working copy
// of the live config on first write — mirroring ifupdown2's behavior of
// creating /etc/network/interfaces.new from the current file on first edit.
// Caller must hold ns.mu (write lock).
func ensureStaged(ns *nodeState) []NetIface {
	if ns.networkPending == nil {
		ns.networkPending = append([]NetIface(nil), ns.network...)
	}
	return ns.networkPending
}

// netIfaceFields reads a request body (JSON or form-encoded) into a flat
// string-keyed map, preserving which keys were actually present. This is
// what lets handleNetworkUpdate merge a partial edit onto an existing
// staged entry (PVE's real PUT semantics) instead of blindly zeroing every
// field the caller didn't mention.
func netIfaceFields(r *http.Request) (map[string]string, error) {
	ct := r.Header.Get("Content-Type")
	if ct != "" && ct != "application/x-www-form-urlencoded" {
		var raw map[string]any
		if r.ContentLength != 0 {
			dec := json.NewDecoder(r.Body)
			if err := dec.Decode(&raw); err != nil {
				return nil, fmt.Errorf("decoding JSON body: %w", err)
			}
		}
		out := make(map[string]string, len(raw))
		for k, v := range raw {
			switch vv := v.(type) {
			case string:
				out[k] = vv
			case bool:
				out[k] = strconv.FormatBool(vv)
			case float64:
				out[k] = strconv.FormatInt(int64(vv), 10)
			default:
				b, _ := json.Marshal(vv)
				out[k] = string(b)
			}
		}
		return out, nil
	}

	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("parsing form body: %w", err)
	}
	out := make(map[string]string, len(r.PostForm))
	for k := range r.PostForm {
		out[k] = r.PostForm.Get(k)
	}
	return out, nil
}

// applyNetIfaceField sets one field on iface by its PVE API param name,
// used for both full construction (create) and partial merge (update).
func applyNetIfaceField(iface *NetIface, key, value string) {
	truthy := value == "1" || value == "true"
	switch key {
	case "iface":
		iface.Iface = value
	case "type":
		iface.Type = value
	case "method":
		iface.Method = value
	case "address":
		iface.Address = value
	case "gateway":
		iface.Gateway = value
	case "autostart":
		iface.Autostart = truthy
	case "mtu":
		iface.MTU = atoiOr(value, iface.MTU)
	case "comments":
		iface.Comments = value
	case "bridge_ports":
		iface.BridgePorts = value
	case "bridge_vlan_aware":
		iface.BridgeVlanAware = truthy
	case "slaves":
		iface.Slaves = value
	case "bond_mode":
		iface.BondMode = value
	case "vlan_raw_device":
		iface.VlanRawDevice = value
	case "vlan_id":
		iface.VlanID = atoiOr(value, iface.VlanID)
	}
}

// applyNetIfaceDelete clears one field by name, for the PVE-style
// comma-separated "delete" param (e.g. "gateway,comments").
func applyNetIfaceDelete(iface *NetIface, key string) {
	switch key {
	case "address":
		iface.Address = ""
	case "gateway":
		iface.Gateway = ""
	case "comments":
		iface.Comments = ""
	case "mtu":
		iface.MTU = 0
	case "bridge_ports":
		iface.BridgePorts = ""
	case "bridge_vlan_aware":
		iface.BridgeVlanAware = false
	case "slaves":
		iface.Slaves = ""
	case "bond_mode":
		iface.BondMode = ""
	case "vlan_raw_device":
		iface.VlanRawDevice = ""
	case "vlan_id":
		iface.VlanID = 0
	case "autostart":
		iface.Autostart = false
	}
}

func (srv *Server) handleNetworkCreate(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}
	fields, err := netIfaceFields(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var iface NetIface
	for k, v := range fields {
		applyNetIfaceField(&iface, k, v)
	}
	if iface.Iface == "" {
		iface.Iface = chi.URLParam(r, "iface")
	}
	if iface.Iface == "" {
		writeError(w, http.StatusBadRequest, "iface is required")
		return
	}
	ns.mu.Lock()
	defer ns.mu.Unlock()
	staged := ensureStaged(ns)
	for _, e := range staged {
		if e.Iface == iface.Iface {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("iface %q already exists", iface.Iface))
			return
		}
	}
	ns.networkPending = append(staged, iface)
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleNetworkUpdate(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	iface := chi.URLParam(r, "iface")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}
	fields, err := netIfaceFields(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ns.mu.Lock()
	defer ns.mu.Unlock()
	staged := ensureStaged(ns)
	for i, e := range staged {
		if e.Iface != iface {
			continue
		}
		// Merge: only fields actually present in the request body are
		// changed, matching PVE's real PUT semantics — everything else on
		// the staged entry is left as-is.
		updated := e
		for k, v := range fields {
			if k == "delete" {
				continue
			}
			applyNetIfaceField(&updated, k, v)
		}
		for _, k := range strings.Split(fields["delete"], ",") {
			if k = strings.TrimSpace(k); k != "" {
				applyNetIfaceDelete(&updated, k)
			}
		}
		updated.Iface = iface
		staged[i] = updated
		writeData(w, http.StatusOK, nil)
		return
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("iface %q not found on node %q", iface, node))
}

func (srv *Server) handleNetworkDelete(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	iface := chi.URLParam(r, "iface")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}
	ns.mu.Lock()
	defer ns.mu.Unlock()
	staged := ensureStaged(ns)
	out := staged[:0]
	found := false
	for _, e := range staged {
		if e.Iface == iface {
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		writeError(w, http.StatusNotFound, fmt.Sprintf("iface %q not found on node %q", iface, node))
		return
	}
	ns.networkPending = out
	writeData(w, http.StatusOK, nil)
}

// handleNetworkRevert implements `DELETE /nodes/{node}/network`: discard
// all staged changes, matching PVE's "Revert" button (synchronous, no
// task).
func (srv *Server) handleNetworkRevert(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}
	ns.mu.Lock()
	ns.networkPending = nil
	ns.mu.Unlock()
	writeData(w, http.StatusOK, nil)
}

// handleNetworkReload implements `PUT /nodes/{node}/network`: apply staged
// changes via a task (mirroring PVE's ifupdown2-backed reload). On success
// the staged config becomes live. On failure — whether injected via fixture
// default or a per-request override — the staged config is discarded and
// the node rolls back to its pre-staging state, so a failed apply never
// leaves the mock in a half-applied condition.
func (srv *Server) handleNetworkReload(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}

	ns.mu.RLock()
	mock := ns.mock
	ns.mu.RUnlock()

	latency, fail, reason := resolveMockOverrides(r, mock)

	sess, _ := srv.authenticate(r)
	user := "unknown"
	if sess != nil {
		user = sess.UserID
	}

	task := srv.state.tasks.Run(node, "srvreload", node, user, latency, fail, reason, func(success bool) {
		ns.mu.Lock()
		defer ns.mu.Unlock()
		if success {
			if ns.networkPending != nil {
				ns.network = ns.networkPending
			}
			ns.networkPending = nil
		} else {
			// Roll back to pre-staging: discard the staged edits so the
			// node is left exactly as it was before this apply attempt,
			// never half-applied.
			ns.networkPending = nil
		}
	})
	writeData(w, http.StatusOK, task.UPID)
}

// resolveMockOverrides applies query-string overrides on top of a node's
// configured MockOptions, so tests can force latency/failure per request
// without touching fixture YAML:
//
//	?mock_latency_ms=500&mock_fail=1&mock_fail_reason=ifupdown2%20error
func resolveMockOverrides(r *http.Request, base MockOptions) (latency time.Duration, fail bool, reason string) {
	q := r.URL.Query()
	latency = time.Duration(base.TaskLatencyMS) * time.Millisecond
	fail = base.NetworkReloadFail
	if v := q.Get("mock_latency_ms"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			latency = time.Duration(ms) * time.Millisecond
		}
	}
	if v := q.Get("mock_fail"); v != "" {
		fail = v == "1" || v == "true"
	}
	if v := q.Get("mock_fail_reason"); v != "" {
		reason = v
	}
	return latency, fail, reason
}
