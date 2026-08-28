// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/whatif"
)

// whatif.go implements docs/api.md's What-if capacity planner section
// (T-4103): one read-only route, POST /capacity/what-if, composing
// internal/capacity, internal/ipam and internal/failsim into a single
// combined verdict. Evaluate-and-discard — nothing here is persisted.

// WhatIfService is the router's seam onto internal/whatif — typically
// cmd/vnproxd's what-if adapter, which resolves the profile's attachment to
// a link/IPAM pool, gathers the live inventory snapshot plus failsim's
// side-tables, and calls whatif.Evaluate. Declared as an interface (the same
// seam pattern as every other *Service in this package) so tests can
// substitute a fake without any of that resolution machinery.
type WhatIfService interface {
	// Evaluate resolves profile/n/failTarget's inputs and returns the
	// combined verdict. failTarget may be the zero Ref (no failure scenario
	// specified); the failsim axis then reports AxisUnavailable rather than
	// erroring.
	Evaluate(ctx context.Context, profile whatif.GuestProfile, n int, failTarget inventory.Ref) (whatif.Verdict, error)
}

// --- wire shapes -------------------------------------------------------

type attachmentRequest struct {
	Kind string `json:"kind"`
	Node string `json:"node"`
	Name string `json:"name"`
}

type guestProfileRequest struct {
	Attachment   attachmentRequest `json:"attachment"`
	Name         string            `json:"name"`
	NICCount     int               `json:"nicCount"`
	ExpectedMbps float64           `json:"expectedMbps"`
}

//nolint:govet // fieldalignment: wire request struct; field order mirrors the JSON shape, not packing.
type whatIfRequest struct {
	Profile    guestProfileRequest `json:"profile"`
	N          int                 `json:"n"`
	FailTarget string              `json:"failTarget,omitempty"`
}

type capacityAxisResponse struct {
	BreaksAtN        *int    `json:"breaksAtN,omitempty"`
	Status           string  `json:"status"`
	Basis            string  `json:"basis,omitempty"`
	Reason           string  `json:"reason,omitempty"`
	ConsumedPct      float64 `json:"consumedPct"`
	AlreadyOverToday bool    `json:"alreadyOverToday,omitempty"`
	Estimated        bool    `json:"estimated"`
}

type ipamAxisResponse struct {
	BreaksAtN     *int   `json:"breaksAtN,omitempty"`
	Status        string `json:"status"`
	Subnet        string `json:"subnet,omitempty"`
	Reason        string `json:"reason,omitempty"`
	FreeAddresses int    `json:"freeAddresses"`
	AddrsPerGuest int    `json:"addrsPerGuest"`
	Estimated     bool   `json:"estimated"`
}

type failsimAxisResponse struct {
	BreaksAtN         *int           `json:"breaksAtN,omitempty"`
	Status            string         `json:"status"`
	Reason            string         `json:"reason,omitempty"`
	Before            impactResponse `json:"before"`
	After             impactResponse `json:"after"`
	AddedDisconnected int            `json:"addedDisconnected"`
}

// verdictResponse is POST /capacity/what-if's response body: one combined
// verdict citing all three axes by name (docs/api.md).
//
//nolint:govet // fieldalignment: wire response struct; field order mirrors whatif.Verdict, not packing.
type verdictResponse struct {
	N           int                  `json:"n"`
	Capacity    capacityAxisResponse `json:"capacity"`
	IPAM        ipamAxisResponse     `json:"ipam"`
	Failsim     failsimAxisResponse  `json:"failsim"`
	Binding     string               `json:"binding"`
	BindingAtN  *int                 `json:"bindingAtN,omitempty"`
	Unavailable []string             `json:"unavailable"`
	Summary     string               `json:"summary"`
}

func toVerdictResponse(v whatif.Verdict) verdictResponse {
	return verdictResponse{
		N: v.N,
		Capacity: capacityAxisResponse{
			Status: string(v.Capacity.Status), BreaksAtN: v.Capacity.BreaksAtN,
			AlreadyOverToday: v.Capacity.AlreadyOverToday, ConsumedPct: v.Capacity.ConsumedPct,
			Basis: v.Capacity.Basis, Estimated: v.Capacity.Estimated, Reason: v.Capacity.Reason,
		},
		IPAM: ipamAxisResponse{
			Status: string(v.IPAM.Status), Subnet: v.IPAM.Subnet, BreaksAtN: v.IPAM.BreaksAtN,
			FreeAddresses: v.IPAM.FreeAddresses, AddrsPerGuest: v.IPAM.AddrsPerGuest,
			Estimated: v.IPAM.Estimated, Reason: v.IPAM.Reason,
		},
		Failsim: failsimAxisResponse{
			Status: string(v.Failsim.Status), Before: toImpactResponse(v.Failsim.Before),
			After: toImpactResponse(v.Failsim.After), AddedDisconnected: v.Failsim.AddedDisconnected,
			BreaksAtN: v.Failsim.BreaksAtN, Reason: v.Failsim.Reason,
		},
		Binding:     v.Binding,
		BindingAtN:  v.BindingAtN,
		Unavailable: strsOrEmpty(v.Unavailable),
		Summary:     v.Summary,
	}
}

// mountWhatIfRoutes registers POST /capacity/what-if (netRead-gated,
// read-only — evaluate-and-discard, nothing persisted). Nil svc/auth skips
// mounting, the standard degraded-mode convention every mountXRoutes in this
// package follows.
func mountWhatIfRoutes(r chi.Router, svc WhatIfService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Post("/capacity/what-if", handleWhatIf(svc))
	})
}

func handleWhatIf(svc WhatIfService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req whatIfRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
			return
		}
		if req.N <= 0 {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "n must be positive")
			return
		}
		if req.Profile.Attachment.Kind != whatif.AttachBridge && req.Profile.Attachment.Kind != whatif.AttachVNet {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", `profile.attachment.kind must be "bridge" or "vnet"`)
			return
		}

		var target inventory.Ref
		if req.FailTarget != "" {
			t, err := inventory.ParseRef(req.FailTarget)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "failTarget is not a valid ref")
				return
			}
			target = t
		}

		profile := whatif.GuestProfile{
			Name: req.Profile.Name, NICCount: req.Profile.NICCount, ExpectedMbps: req.Profile.ExpectedMbps,
			Attachment: whatif.Attachment{
				Kind: req.Profile.Attachment.Kind, Node: req.Profile.Attachment.Node, Name: req.Profile.Attachment.Name,
			},
		}

		v, err := svc.Evaluate(r.Context(), profile, req.N, target)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not evaluate what-if request")
			return
		}
		writeJSON(w, http.StatusOK, toVerdictResponse(v))
	}
}
