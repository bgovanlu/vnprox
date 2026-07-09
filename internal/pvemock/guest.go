package pvemock

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (srv *Server) guestMap(ns *nodeState, kind string) map[string]*GuestSpec {
	if kind == "lxc" {
		return ns.lxc
	}
	return ns.qemu
}

// handleGuestConfigGet returns a closure implementing
// `GET /nodes/{node}/{kind}/{vmid}/config`, where kind is "qemu" or "lxc".
func (srv *Server) handleGuestConfigGet(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node := chi.URLParam(r, "node")
		vmid := chi.URLParam(r, "vmid")
		ns, ok := srv.state.node(node)
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
			return
		}
		ns.mu.RLock()
		defer ns.mu.RUnlock()
		g, ok := srv.guestMap(ns, kind)[vmid]
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("%s %s not found on node %q", kind, vmid, node))
			return
		}
		writeData(w, http.StatusOK, g.Config)
	}
}

// handleGuestConfigPut returns a closure implementing
// `PUT /nodes/{node}/{kind}/{vmid}/config`. Matching real PVE semantics: the
// body's keys are merged into the existing config (not a full replace), and
// a `delete` field/query param (comma-separated key list) removes keys.
func (srv *Server) handleGuestConfigPut(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node := chi.URLParam(r, "node")
		vmid := chi.URLParam(r, "vmid")
		ns, ok := srv.state.node(node)
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
			return
		}

		var body map[string]string
		if err := decodeRequest(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		ns.mu.Lock()
		defer ns.mu.Unlock()
		g, ok := srv.guestMap(ns, kind)[vmid]
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("%s %s not found on node %q", kind, vmid, node))
			return
		}
		if g.Config == nil {
			g.Config = map[string]string{}
		}
		var deleteKeys string
		for k, v := range body {
			if k == "delete" {
				deleteKeys = v
				continue
			}
			g.Config[k] = v
		}
		for _, k := range strings.Split(deleteKeys, ",") {
			k = strings.TrimSpace(k)
			if k != "" {
				delete(g.Config, k)
			}
		}
		writeData(w, http.StatusOK, nil)
	}
}
