// SPDX-License-Identifier: Apache-2.0

// ifcounters.go implements T-4013's read-only SNMP switch-counter surface
// (docs/api.md's Switch counters (SNMP) section):
//
//   - GET    /snmp/counters          — every currently-known polled port
//     result (internal/ifcounters.Service.Results, current state, not a
//     history — mirrors mtuprobe.go's identical GET /mtuprobe/results shape)
//   - GET    /snmp/targets           — every configured per-switch poll
//     target (community never echoed back — hasCommunity bool only,
//     matching alertrules.go's hasSecret convention)
//   - PUT    /snmp/targets/{chassisId} — create-or-replace one switch's poll
//     config, keyed by its LLDP ChassisID (the same identity
//     internal/ifcounters.Service.Tick groups neighbors by). `community` is
//     a *string with the same three-state contract PUT /alert-rules/{id}
//     uses: absent/null leaves the stored community untouched (update only),
//     "" clears it, non-empty replaces it.
//   - DELETE /snmp/targets/{chassisId} — remove a switch's poll config
//
// GET routes are netRead-gated; PUT/DELETE are netWrite+CSRF-gated, matching
// alertrules.go's read/write split. None of this ever touches
// internal/switchdrv or produces a changeset op — configuring which
// switches this daemon polls over SNMP is ordinary app config, exactly like
// registering an alert rule's webhook URL, not a network mutation (T-4013's
// card: read-only end to end).

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/ifcounters"
	"github.com/bgovanlu/vnprox/internal/snmp"
	"github.com/bgovanlu/vnprox/internal/store"
)

// maxIfCounterTargetBodyBytes bounds a PUT /snmp/targets/{chassisId} body —
// generous headroom for the handful of fields this config carries, mirroring
// maxAlertRuleBodyBytes' reasoning.
const maxIfCounterTargetBodyBytes = 4 << 10 // 4 KiB

// IfCountersService is the subset of *ifcounters.Service the router needs
// for GET /snmp/counters.
type IfCountersService interface {
	Results() []ifcounters.Result
}

// IfCounterTargetStore is the subset of *store.SwitchSNMPTargetRepo the
// router needs.
type IfCounterTargetStore interface {
	List(ctx context.Context) ([]store.SwitchSNMPTarget, error)
	GetByChassisID(ctx context.Context, chassisID string) (store.SwitchSNMPTarget, error)
	Insert(ctx context.Context, t store.SwitchSNMPTarget) error
	Update(ctx context.Context, t store.SwitchSNMPTarget) error
	DeleteByChassisID(ctx context.Context, chassisID string) error
}

type ifCounterResultResponse struct {
	ChassisID   string `json:"chassisId"`
	SwitchName  string `json:"switchName"`
	Node        string `json:"node"`
	LocalIface  string `json:"localIface"`
	SwitchPort  string `json:"switchPort"`
	State       string `json:"state"`
	InErrors    uint64 `json:"inErrors,omitempty"`
	OutErrors   uint64 `json:"outErrors,omitempty"`
	InDiscards  uint64 `json:"inDiscards,omitempty"`
	OutDiscards uint64 `json:"outDiscards,omitempty"`
	InOctets    uint64 `json:"inOctets,omitempty"`
	OutOctets   uint64 `json:"outOctets,omitempty"`
	OperUp      bool   `json:"operUp,omitempty"`
	At          int64  `json:"at"`
}

func toIfCounterResultResponse(r ifcounters.Result) ifCounterResultResponse {
	return ifCounterResultResponse{
		ChassisID: r.ChassisID, SwitchName: r.SwitchName, Node: r.Node,
		LocalIface: r.LocalIface, SwitchPort: r.SwitchPort, State: string(r.State),
		InErrors: r.InErrors, OutErrors: r.OutErrors,
		InDiscards: r.InDiscards, OutDiscards: r.OutDiscards,
		InOctets: r.InOctets, OutOctets: r.OutOctets,
		OperUp: r.OperUp, At: r.At,
	}
}

type ifCounterTargetResponse struct {
	ChassisID     string `json:"chassisId"`
	ChassisIDType string `json:"chassisIdType,omitempty"`
	MgmtAddr      string `json:"mgmtAddr,omitempty"`
	AddedBy       string `json:"addedBy"`
	Port          int    `json:"port"`
	AddedAt       int64  `json:"addedAt"`
	Enabled       bool   `json:"enabled"`
	HasCommunity  bool   `json:"hasCommunity"`
}

func toIfCounterTargetResponse(t store.SwitchSNMPTarget) ifCounterTargetResponse {
	return ifCounterTargetResponse{
		ChassisID: t.ChassisID, ChassisIDType: t.ChassisIDType, MgmtAddr: t.MgmtAddr,
		Port: t.Port, Enabled: t.Enabled, HasCommunity: len(t.CommunityEnc) > 0,
		AddedBy: t.AddedBy, AddedAt: t.AddedAt,
	}
}

type ifCounterTargetRequest struct {
	Community     *string `json:"community"`
	ChassisIDType string  `json:"chassisIdType"`
	MgmtAddr      string  `json:"mgmtAddr"`
	Port          int     `json:"port"`
	Enabled       bool    `json:"enabled"`
}

// mountIfCountersRoutes registers the routes above. Nil-safe: any missing
// dependency simply skips mounting every route in this file, the same
// degraded-mode convention every other optional Options field here uses.
func mountIfCountersRoutes(r chi.Router, svc IfCountersService, targets IfCounterTargetStore, cipher SecretCipher, auth AuthService) {
	if auth == nil {
		return
	}
	if svc != nil {
		r.Group(func(r chi.Router) {
			r.Use(auth.SessionMiddleware)
			r.Use(auth.RequireCap(capNetRead))
			r.Get("/snmp/counters", handleIfCounterResults(svc))
		})
	}
	if targets == nil || cipher == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/snmp/targets", handleListIfCounterTargets(targets))
	})
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Put("/snmp/targets/{chassisId}", handlePutIfCounterTarget(targets, cipher, lookup))
		r.Delete("/snmp/targets/{chassisId}", handleDeleteIfCounterTarget(targets))
	})
}

func handleIfCounterResults(svc IfCountersService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		results := svc.Results()
		items := make([]ifCounterResultResponse, len(results))
		for i, res := range results {
			items[i] = toIfCounterResultResponse(res)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func handleListIfCounterTargets(targets IfCounterTargetStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := targets.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list SNMP targets")
			return
		}
		items := make([]ifCounterTargetResponse, 0, len(list))
		for _, t := range list {
			items = append(items, toIfCounterTargetResponse(t))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// handlePutIfCounterTarget creates or replaces one switch's poll config,
// keyed by its LLDP ChassisID. This never validates that a live LLDP
// neighbor with this ChassisID currently exists — configuring a target for
// a switch that hasn't been seen yet (or has aged out) is harmless:
// internal/ifcounters.Service.Tick only ever polls a target whose ChassisID
// ALSO appears in this tick's live LLDPNeighbors() set (that package's
// doc.go), so an orphaned target row simply never gets polled until/unless
// that chassis reappears.
func handlePutIfCounterTarget(targets IfCounterTargetStore, cipher SecretCipher, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		chassisID := chi.URLParam(r, "chassisId")
		if chassisID == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "chassisId is required")
			return
		}
		var req ifCounterTargetRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIfCounterTargetBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed SNMP target body: "+err.Error())
			return
		}
		if req.Port < 0 || req.Port > 65535 {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "port must be between 0 and 65535")
			return
		}
		port := req.Port
		if port == 0 {
			port = snmp.DefaultPort
		}

		existing, err := targets.GetByChassisID(r.Context(), chassisID)
		switch {
		case err == nil:
			// Update: only overwrite the community ciphertext if the
			// request actually supplied one (the three-state *string
			// contract in this file's own doc comment).
			communityEnc := existing.CommunityEnc
			if req.Community != nil {
				communityEnc, err = encryptOrClear(cipher, *req.Community)
				if err != nil {
					writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not encrypt SNMP community string")
					return
				}
			}
			existing.ChassisIDType = req.ChassisIDType
			existing.MgmtAddr = req.MgmtAddr
			existing.Port = port
			existing.Enabled = req.Enabled
			existing.CommunityEnc = communityEnc
			if updateErr := targets.Update(r.Context(), existing); updateErr != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not update SNMP target")
				return
			}
			writeJSON(w, http.StatusOK, toIfCounterTargetResponse(existing))
			return
		case errors.Is(err, store.ErrNotFound):
			communityEnc, encErr := encryptOrClear(cipher, derefOrEmpty(req.Community))
			if encErr != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not encrypt SNMP community string")
				return
			}
			t := store.SwitchSNMPTarget{
				ID: store.NewULID(), ChassisID: chassisID, ChassisIDType: req.ChassisIDType,
				MgmtAddr: req.MgmtAddr, Port: port, CommunityEnc: communityEnc, Enabled: req.Enabled,
				AddedBy: username, AddedAt: time.Now().Unix(),
			}
			if err := targets.Insert(r.Context(), t); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save SNMP target")
				return
			}
			writeJSON(w, http.StatusCreated, toIfCounterTargetResponse(t))
			return
		default:
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
	}
}

func handleDeleteIfCounterTarget(targets IfCounterTargetStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chassisID := chi.URLParam(r, "chassisId")
		if err := targets.DeleteByChassisID(r.Context(), chassisID); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not delete SNMP target")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// encryptOrClear encrypts plaintext with cipher, or returns a nil ciphertext
// if plaintext is empty — the "" (clear the stored community) case in this
// file's own three-state *string contract.
func encryptOrClear(cipher SecretCipher, plaintext string) ([]byte, error) {
	if plaintext == "" {
		return nil, nil
	}
	return cipher.Encrypt([]byte(plaintext))
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
