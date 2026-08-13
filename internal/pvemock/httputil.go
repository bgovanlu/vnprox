package pvemock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
)

// decodeRequest populates dst from the request body. PVE's real API accepts
// both JSON and form-encoded bodies for most endpoints; the mock mirrors
// that so curl one-liners (form-encoded, the common case for
// /access/ticket) and JSON clients both work.
func decodeRequest(r *http.Request, dst any) error {
	ct := r.Header.Get("Content-Type")
	switch ct {
	case "", "application/x-www-form-urlencoded":
		if err := r.ParseForm(); err != nil {
			return fmt.Errorf("parsing form body: %w", err)
		}
		switch v := dst.(type) {
		case *ticketRequest:
			v.Username = r.PostForm.Get("username")
			v.Password = r.PostForm.Get("password")
			v.Realm = r.PostForm.Get("realm")
			v.OTP = r.PostForm.Get("otp")
			return nil
		default:
			return jsonFromForm(r, dst)
		}
	default:
		dec := json.NewDecoder(r.Body)
		if r.ContentLength == 0 {
			return nil
		}
		if err := dec.Decode(dst); err != nil {
			return fmt.Errorf("decoding JSON body: %w", err)
		}
		return nil
	}
}

// jsonFromForm is a permissive fallback: form values are re-marshaled as a
// flat map so handlers that expect map[string]string/any still work when a
// client posts form-encoded data instead of JSON.
func jsonFromForm(r *http.Request, dst any) error {
	m, ok := dst.(*map[string]string)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for k := range r.PostForm {
		out[k] = r.PostForm.Get(k)
	}
	*m = out
	return nil
}

// pveEnvelope is the {"data": ...} (or {"data": null, "errors": {...}})
// shape every real PVE API response uses.
type pveEnvelope struct {
	Data    any            `json:"data"`
	Errors  map[string]any `json:"errors,omitempty"`
	Message string         `json:"message,omitempty"`
}

func writeData(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(pveEnvelope{Data: data})
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(pveEnvelope{Data: nil, Message: message})
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// sortedKeys returns m's keys in ascending string order.
//
// Go deliberately randomizes map iteration order, so any handler that
// ranges over a map to build a JSON array (as opposed to a JSON object,
// where encoding/json already sorts keys on Marshal) returns its elements
// in a different order on roughly one run in three. That makes pvemock
// itself the source of test flakiness and cassette churn rather than a
// stable fixture for tests to build on — see T-2502-followup-01. Handlers
// that build a list from a map keyed by the resource's own id/name should
// range over sortedKeys(m) instead of the map directly.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
