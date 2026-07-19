package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/ipam"
)

// IPAMService is the subset of T-405's *ipam.Service the router needs:
// docs/api.md's `GET /ipam/subnets` and
// `GET /ipam/subnets/{cidr}/allocations` (+ its `?format=csv` export).
// Declared as an interface (the same "small seam" pattern as SDNService
// above) so this package's dependency on the concrete service stays
// reviewable and test-doubleable.
type IPAMService interface {
	Subnets(ctx context.Context) (ipam.SubnetsResponse, error)
	Allocations(ctx context.Context, cidr string) (ipam.AllocationList, error)
	AllocationsCSV(ctx context.Context, cidr string) ([]byte, error)
	// V6Plan backs T-1404's `GET /ipam/subnets/{prefix}/v6-plan` (the
	// IPv6 planning grid: given a delegated prefix, propose /64-aligned
	// subnets against existing VLANs/VNets).
	V6Plan(ctx context.Context, prefix string) (ipam.V6PlanResponse, error)
}

// capIPAMRead reuses the sdnRead capability (docs/api.md's documented
// `/auth/me` capability flags has no dedicated ipamRead — IPAM is an
// SDN-adjacent read view, docs/features/ipam.md §1: "vnprox reads through
// PVE's plugin transparently" for the same plugin real PVE's SDN stack
// configures) rather than inventing a new capability flag. Flagged in
// T-405's completion report as the smallest reasonable extension where the
// documented contract didn't fully specify one.
const capIPAMRead = capSDNRead

// allocationsSuffix is the literal trailing path segment after a subnet
// CIDR in docs/api.md's `GET /ipam/subnets/{cidr}/allocations` route.
const allocationsSuffix = "/allocations"

// v6PlanSuffix is the literal trailing path segment after a delegated
// prefix in docs/api.md's `GET /ipam/subnets/{prefix}/v6-plan` route
// (T-1404) — same trailing-wildcard reasoning as allocationsSuffix above
// (a CIDR/prefix contains a literal '/', which a chi {param} can't span).
const v6PlanSuffix = "/v6-plan"

// mountIPAMRoutes registers docs/api.md's `GET /ipam/subnets` and
// `GET /ipam/subnets/{cidr}/allocations`. svc == nil (no PVE client — see
// SDNService's doc comment) simply skips mounting, matching every other
// optional Options field's degraded-mode treatment.
//
// The allocations route uses a trailing chi wildcard rather than
// `/ipam/subnets/{cidr}/allocations` with a `{cidr}` param: a CIDR
// contains a literal '/' (docs/api.md's Ref-triplet routes hit the same
// issue — see topology.go's handleInventoryDetail doc comment), which chi
// params cannot span. handleIPAMAllocations splits the wildcard's trailing
// "/allocations" segment off itself.
func mountIPAMRoutes(r chi.Router, svc IPAMService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capIPAMRead))
		r.Get("/ipam/subnets", handleIPAMSubnets(svc))
		r.Get("/ipam/subnets/*", handleIPAMAllocations(svc))
	})
}

func handleIPAMSubnets(svc IPAMService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := svc.Subnets(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "pve_unreachable", "could not read IPAM subnets from PVE")
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleIPAMAllocations(svc IPAMService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := chi.URLParam(r, "*")
		if strings.HasSuffix(raw, v6PlanSuffix) {
			handleIPAMV6Plan(svc, w, r, strings.TrimSuffix(raw, v6PlanSuffix))
			return
		}
		if !strings.HasSuffix(raw, allocationsSuffix) {
			writeJSONError(w, http.StatusNotFound, "not_found", "unknown /ipam/subnets route")
			return
		}
		cidrPart := strings.TrimSuffix(raw, allocationsSuffix)
		if unescaped, uerr := url.PathUnescape(cidrPart); uerr == nil {
			cidrPart = unescaped
		}
		if cidrPart == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "subnet cidr is required")
			return
		}

		if r.URL.Query().Get("format") == "csv" {
			data, err := svc.AllocationsCSV(r.Context(), cidrPart)
			if err != nil {
				writeIPAMLookupError(w, err)
				return
			}
			w.Header().Set("Content-Type", "text/csv")
			w.Header().Set("Content-Disposition", `attachment; filename="ipam-`+sanitizeFilename(cidrPart)+`.csv"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
			return
		}

		list, err := svc.Allocations(r.Context(), cidrPart)
		if err != nil {
			writeIPAMLookupError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// handleIPAMV6Plan implements `GET /ipam/subnets/{prefix}/v6-plan`
// (T-1404), dispatched from handleIPAMAllocations' shared wildcard route.
// prefixPart is raw's "{prefix}" segment with the "/v6-plan" suffix
// already stripped.
func handleIPAMV6Plan(svc IPAMService, w http.ResponseWriter, r *http.Request, prefixPart string) {
	if unescaped, uerr := url.PathUnescape(prefixPart); uerr == nil {
		prefixPart = unescaped
	}
	if prefixPart == "" {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "delegated prefix is required")
		return
	}
	resp, err := svc.V6Plan(r.Context(), prefixPart)
	if err != nil {
		if errors.Is(err, ipam.ErrInvalidPrefix) || errors.Is(err, ipam.ErrPrefixTooLarge) {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
			return
		}
		writeJSONError(w, http.StatusServiceUnavailable, "pve_unreachable", "could not build IPv6 planning grid")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeIPAMLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, ipam.ErrSubnetNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "no such subnet")
		return
	}
	writeJSONError(w, http.StatusServiceUnavailable, "pve_unreachable", "could not read IPAM allocations from PVE")
}

// sanitizeFilename replaces characters a Content-Disposition filename
// shouldn't carry verbatim (a CIDR's '/' above all) with '-'.
func sanitizeFilename(s string) string {
	return strings.NewReplacer("/", "-", `"`, "", "\\", "-").Replace(s)
}
