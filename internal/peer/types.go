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

// nodeRequest is POST /api/peer/host/{ifreload,discard-staged}'s body: just
// the target node.
type nodeRequest struct {
	Node string `json:"node"`
}

// installLLDPRequest is POST /api/peer/host/lldp/install's body: an
// explicit confirmation flag (docs/features/lldp-discovery.md §1's
// "changeset-like confirmation" for the guided-install flow).
type installLLDPRequest struct {
	Confirm bool `json:"confirm"`
}

// okResponse is the generic success body for the write endpoints.
type okResponse struct {
	OK bool `json:"ok"`
}

// armTimerRequest is POST /api/peer/timer/arm's body: T-304's local-timer
// protocol arms a rollback deadline on the receiving node before the
// coordinator's first mutating step for it (docs/features/
// change-management.md §4).
type armTimerRequest struct {
	ChangesetID string `json:"changesetId"`
	Node        string `json:"node"`
	Content     string `json:"content"`
	Deadline    int64  `json:"deadline"`
}

// timerRequest is POST /api/peer/timer/cancel and GET /api/peer/timer/
// status's shared identifying key.
type timerRequest struct {
	ChangesetID string `json:"changesetId"`
	Node        string `json:"node"`
}

// timerResponse is the body every /api/peer/timer/* route returns: the
// resulting (or current, for status) TimerRecord.
type timerResponse struct {
	Record TimerRecord `json:"record"`
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
