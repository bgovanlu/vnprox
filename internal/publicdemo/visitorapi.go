// SPDX-License-Identifier: Apache-2.0

package publicdemo

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// VisitorSession is GET /demo/visitor/session's body: what the SPA needs in
// order to know it is in a public demo and which scratch namespace is its
// own.
//
// A normal daemon serves no such route, so a 404 here is the SPA's "this is
// not a public demo" — no config flag, no health field, and therefore
// nothing for a normal daemon to get wrong.
type VisitorSession struct {
	Visitor    string       `json:"visitor"`
	Caps       VisitorLimit `json:"caps"`
	PublicDemo bool         `json:"publicDemo"`
}

// VisitorLimit is the subset of Caps a visitor is told about: their own
// limits, so the UI can say why a save was refused. MaxVisitors is
// deliberately absent — how busy the instance is, is not a visitor's
// business.
type VisitorLimit struct {
	RequestBurst    int `json:"requestBurst"`
	MaxStateBytes   int `json:"maxStateBytes"`
	MaxStateEntries int `json:"maxStateEntries"`
}

// VisitorState is one scratch key's body, in both directions. The payload
// is opaque JSON, exactly as internal/api's /layouts routes treat a layout.
type VisitorState struct {
	Name  string          `json:"name"`
	State json.RawMessage `json:"state"`
}

// serveVisitor routes the edge's own surface.
func (e *Edge) serveVisitor(w http.ResponseWriter, r *http.Request, v *visitor) {
	switch {
	case r.URL.Path == VisitorSessionPath:
		if r.Method != http.MethodGet {
			e.refuse(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
			return
		}
		writeJSON(w, http.StatusOK, VisitorSession{
			PublicDemo: true,
			Visitor:    v.id,
			Caps: VisitorLimit{
				RequestBurst:    e.caps.RequestBurst,
				MaxStateBytes:   e.caps.MaxStateBytes,
				MaxStateEntries: e.caps.MaxStateEntries,
			},
		})
	case strings.HasPrefix(r.URL.Path, VisitorStatePrefix):
		e.serveVisitorState(w, r, v)
	default:
		writeJSONError(w, http.StatusNotFound, "not_found", "no such visitor route")
	}
}

func (e *Edge) serveVisitorState(w http.ResponseWriter, r *http.Request, v *visitor) {
	name := strings.TrimPrefix(r.URL.Path, VisitorStatePrefix)
	if !validStateName(name) {
		writeJSONError(w, http.StatusBadRequest, "validation_failed",
			"a state key is 1-64 characters of letters, digits, '-', '_', '.' or ':'")
		return
	}

	switch r.Method {
	case http.MethodGet:
		raw, ok := v.readState(name)
		if !ok {
			// 404 and not an empty body: "never saved" and "saved
			// something empty" are different, and the SPA starts a fresh
			// tour on the first but resumes on the second.
			writeJSONError(w, http.StatusNotFound, "not_found", "this visitor has nothing saved under that key")
			return
		}
		writeJSON(w, http.StatusOK, VisitorState{Name: name, State: raw})
	case http.MethodPut:
		e.putVisitorState(w, r, v, name)
	default:
		e.refuse(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or PUT only")
	}
}

func (e *Edge) putVisitorState(w http.ResponseWriter, r *http.Request, v *visitor, name string) {
	// One byte over the cap is enough to reject on: reading the whole of a
	// hostile body in order to measure it is the cap paying for its own
	// circumvention.
	limited := http.MaxBytesReader(w, r.Body, int64(e.caps.MaxStateBytes)+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		e.refuse(w, http.StatusRequestEntityTooLarge, codeStateTooBig,
			"this demo session's scratch state is capped; the value was not stored, and nothing already stored was changed. Every other visitor is unaffected.")
		return
	}

	var payload VisitorState
	if unmarshalErr := json.Unmarshal(body, &payload); unmarshalErr != nil || payload.State == nil {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", `expected {"state": <json>}`)
		return
	}

	if writeErr := v.writeState(name, payload.State, e.caps); writeErr != nil {
		if errors.Is(writeErr, errStateCapExceeded) {
			e.refuse(w, http.StatusRequestEntityTooLarge, codeStateTooBig,
				"this demo session's scratch state is capped; the value was not stored, and nothing already stored was changed. Every other visitor is unaffected.")
			return
		}
		e.log.Error("publicdemo: storing visitor state", "error", writeErr)
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not store the value")
		return
	}
	writeJSON(w, http.StatusOK, VisitorState{Name: name, State: payload.State})
}

// validStateName keeps the scratch key space to something a path can carry
// unambiguously. It is not a security boundary — the key is only ever a map
// index in this visitor's own map, never a filesystem path — but an
// unbounded key is an unbounded allocation.
func validStateName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.', c == ':':
		default:
			return false
		}
	}
	return true
}
