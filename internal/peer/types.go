package peer

import (
	"encoding/json"
	"net/http"

	"github.com/bgovanlu/vnprox/internal/host"
)

// Wire shapes for the documented /api/peer/host/* routes (docs/api.md's
// Peer API section names the routes; the request/response bodies are this
// package's own internal-only contract, never exposed to the SPA).

// interfacesResponse is GET /api/peer/host/interfaces's body.
type interfacesResponse struct {
	Content string `json:"content"`
}

// statsResponse is GET /api/peer/host/stats's body.
type statsResponse struct {
	Stats map[string]host.IfaceStats `json:"stats"`
}

// stageRequest is POST /api/peer/host/stage-interfaces and
// POST /api/peer/host/restore's shared body shape: the target node and the
// full interfaces(5) content to write.
type stageRequest struct {
	Node    string `json:"node"`
	Content string `json:"content"`
}

// nodeRequest is POST /api/peer/host/ifreload's body: just the target node.
type nodeRequest struct {
	Node string `json:"node"`
}

// linksResponse is GET /api/peer/host/links's body (T-303: the
// netlink-equivalent link state a remote peer-API-backed host.Reader needs
// for internal/collect's cluster-wide host poller — docs/architecture.md
// §1's "peer vnproxd instances on other cluster nodes for node-local data").
type linksResponse struct {
	Links []host.LinkState `json:"links"`
}

// AuditRecord is one row of GET /api/peer/audit's page (T-303). Its fields
// mirror docs/api.md's GET /audit list item shape field-for-field so
// internal/api's cluster merge can decode a peer's page directly into the
// same representation it renders locally, with no lossy conversion.
type AuditRecord struct {
	Username    string          `json:"username"`
	Action      string          `json:"action"`
	Target      string          `json:"target,omitempty"`
	ChangesetID string          `json:"changesetId,omitempty"`
	Result      string          `json:"result"`
	Detail      json.RawMessage `json:"detail,omitempty"`
	ID          int64           `json:"id"`
	At          int64           `json:"at"`
}

// AuditFilter narrows GET /api/peer/audit exactly like docs/api.md's GET
// /audit query params. This is peer's own copy of that filter shape (rather
// than importing internal/store's store.AuditFilter) so this package never
// depends on internal/store — see server.go's AuditReader doc comment for
// why that direction matters.
type AuditFilter struct {
	User        string `json:"user,omitempty"`
	Action      string `json:"action,omitempty"`
	Target      string `json:"target,omitempty"`
	Result      string `json:"result,omitempty"`
	ChangesetID string `json:"changesetId,omitempty"`
	From        int64  `json:"from,omitempty"`
	To          int64  `json:"to,omitempty"`
}

// auditPageResponse is GET /api/peer/audit's body: one page of this node's
// own local audit log, same envelope shape as docs/api.md's GET /audit.
type auditPageResponse struct {
	NextCursor string        `json:"nextCursor,omitempty"`
	Items      []AuditRecord `json:"items"`
}

// SnapshotRecord is one row of GET /api/peer/snapshots' page (T-303),
// mirroring docs/api.md's GET /snapshots list item shape field-for-field.
type SnapshotRecord struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	ChangesetID string   `json:"changesetId,omitempty"`
	Note        string   `json:"note,omitempty"`
	Nodes       []string `json:"nodes"`
	TakenAt     int64    `json:"takenAt"`
}

// snapshotPageResponse is GET /api/peer/snapshots' body.
type snapshotPageResponse struct {
	NextCursor string           `json:"nextCursor,omitempty"`
	Items      []SnapshotRecord `json:"items"`
}

// okResponse is the generic success body for the write endpoints.
type okResponse struct {
	OK bool `json:"ok"`
}

// errorEnvelope mirrors docs/api.md's global error shape:
// {"error":{"code","message","details"}}. Used both to write server
// responses and to decode them back out client-side.
type errorEnvelope struct {
	Error struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Details json.RawMessage `json:"details,omitempty"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	var env errorEnvelope
	env.Error.Code = code
	env.Error.Message = message
	writeJSON(w, status, env)
}
