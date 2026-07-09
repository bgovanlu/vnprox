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
