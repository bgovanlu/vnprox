// ipam_external.go implements T-1203's cross-cluster IPAM surfaces:
//
//   - GET/POST   /ipam/external-subnets       — list / create app-owned external subnets
//   - GET/PUT/DELETE /ipam/external-subnets/{id}
//   - POST /ipam/external-sync/preview        — dry-run NetBox/phpIPAM sync (never writes)
//   - POST /ipam/external-sync/apply {confirm} — apply the sync (confirm-gated, audited)
//   - GET  /federation/ipam/conflicts         — cross-cluster duplicate-subnet findings
//
// External subnets are app-owned intent (IP space Proxmox has no knowledge of),
// read/write ONLY via these CRUD routes — never via ipam.alloc.* changeset ops,
// since they are not PVE SDN subnets. External-IPAM sync writes sit outside the
// change engine by nature (an external IPAM system is not Proxmox config), but
// mirror its stage/review/confirm/audit contract: preview is a pure dry-run,
// apply demands an explicit {confirm:true} (mirroring POST /lldp/install), and
// every write is audit-logged ipam.external_sync with before/after per record.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/store"
)

// maxIPAMExternalBodyBytes bounds an external-subnet / sync request body — a
// CIDR + a few short strings is small; this is generous defensive headroom.
const maxIPAMExternalBodyBytes = 16 << 10 // 16 KiB

// IPAMExternalService is the subset of *ipam.Service the external-subnet CRUD
// and external-IPAM sync routes need. Declared separately from IPAMService (the
// read view) so the existing read seam stays unchanged; the same concrete
// *ipam.Service satisfies both.
type IPAMExternalService interface {
	ListExternalSubnets(ctx context.Context) ([]ipam.ExternalSubnet, error)
	GetExternalSubnet(ctx context.Context, id string) (ipam.ExternalSubnet, error)
	CreateExternalSubnet(ctx context.Context, cidr, label, source, description, createdBy string) (ipam.ExternalSubnet, error)
	UpdateExternalSubnet(ctx context.Context, id, cidr, label, source, description string) (ipam.ExternalSubnet, error)
	DeleteExternalSubnet(ctx context.Context, id string) error
	ExternalSyncPreview(ctx context.Context) (ipam.SyncPlan, error)
	ExternalSyncApply(ctx context.Context, confirm bool) (ipam.SyncResult, error)
}

// FederationIPAMSource is the subset of the federation aggregator the
// cross-cluster IPAM conflict route needs — the per-cluster SDN subnet fan-out.
// It yields ipam.ClusterSubnets directly (the same type CrossClusterConflicts
// consumes), so cmd/vnproxd's thin adapter over *federation.Aggregator just
// maps federation.ClusterSubnets into this shape.
type FederationIPAMSource interface {
	IPAMSubnets(ctx context.Context) (results []ipam.ClusterSubnets, partial bool, failedClusters []string, err error)
}

// ipamExternalAuditWriter is the minimal audit seam this route family needs
// (exactly *store.AuditRepo's Append). Optional (nil skips audit rows).
type ipamExternalAuditWriter interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

type externalSubnetRequest struct {
	CIDR        string `json:"cidr"`
	Label       string `json:"label"`
	Source      string `json:"source"`
	Description string `json:"description"`
}

type externalSyncApplyRequest struct {
	Confirm bool `json:"confirm"`
}

// mountIPAMExternalRoutes registers the external-subnet CRUD and sync routes.
// svc/auth are required together; audit is optional.
func mountIPAMExternalRoutes(r chi.Router, svc IPAMExternalService, audit ipamExternalAuditWriter, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capIPAMRead))
		r.Get("/ipam/external-subnets", handleListExternalSubnets(svc))
		r.Get("/ipam/external-subnets/{id}", handleGetExternalSubnet(svc))
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/ipam/external-subnets", handleCreateExternalSubnet(svc, audit, lookup))
		r.Put("/ipam/external-subnets/{id}", handleUpdateExternalSubnet(svc, audit, lookup))
		r.Delete("/ipam/external-subnets/{id}", handleDeleteExternalSubnet(svc, audit, lookup))
		r.Post("/ipam/external-sync/preview", handleExternalSyncPreview(svc))
		r.Post("/ipam/external-sync/apply", handleExternalSyncApply(svc, audit, lookup))
	})
}

func handleListExternalSubnets(svc IPAMExternalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.ListExternalSubnets(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list external subnets")
			return
		}
		if list == nil {
			list = []ipam.ExternalSubnet{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": list})
	}
}

func handleGetExternalSubnet(svc IPAMExternalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sub, err := svc.GetExternalSubnet(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such external subnet")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not look up external subnet")
			return
		}
		writeJSON(w, http.StatusOK, sub)
	}
}

func handleCreateExternalSubnet(svc IPAMExternalService, audit ipamExternalAuditWriter, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		req, ok := decodeExternalSubnetRequest(w, r)
		if !ok {
			return
		}
		if strings.TrimSpace(req.CIDR) == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "cidr is required")
			return
		}
		sub, err := svc.CreateExternalSubnet(r.Context(), req.CIDR, req.Label, req.Source, req.Description, username)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "could not create external subnet: "+err.Error())
			return
		}
		auditIPAMExternal(r.Context(), audit, username, "ipam.external_subnet_add", sub.CIDR, map[string]any{"id": sub.ID, "source": sub.Source})
		writeJSON(w, http.StatusCreated, sub)
	}
}

func handleUpdateExternalSubnet(svc IPAMExternalService, audit ipamExternalAuditWriter, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		req, ok := decodeExternalSubnetRequest(w, r)
		if !ok {
			return
		}
		sub, err := svc.UpdateExternalSubnet(r.Context(), id, req.CIDR, req.Label, req.Source, req.Description)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such external subnet")
				return
			}
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "could not update external subnet: "+err.Error())
			return
		}
		auditIPAMExternal(r.Context(), audit, username, "ipam.external_subnet_update", sub.CIDR, map[string]any{"id": sub.ID})
		writeJSON(w, http.StatusOK, sub)
	}
}

func handleDeleteExternalSubnet(svc IPAMExternalService, audit ipamExternalAuditWriter, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		if err := svc.DeleteExternalSubnet(r.Context(), id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not delete external subnet")
			return
		}
		auditIPAMExternal(r.Context(), audit, username, "ipam.external_subnet_remove", id, nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleExternalSyncPreview(svc IPAMExternalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		plan, err := svc.ExternalSyncPreview(r.Context())
		if err != nil {
			if errors.Is(err, ipam.ErrSyncNotConfigured) {
				writeJSONError(w, http.StatusNotFound, "not_found", "external IPAM sync is not configured")
				return
			}
			writeJSONError(w, http.StatusServiceUnavailable, "external_ipam_unreachable", "could not read external IPAM records: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, plan)
	}
}

func handleExternalSyncApply(svc IPAMExternalService, audit ipamExternalAuditWriter, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIPAMExternalBodyBytes))
		var req externalSyncApplyRequest
		if err := dec.Decode(&req); err != nil || !req.Confirm {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", `request body must be {"confirm": true}`)
			return
		}
		res, err := svc.ExternalSyncApply(r.Context(), req.Confirm)
		if err != nil {
			if errors.Is(err, ipam.ErrSyncConfirmRequired) {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", `request body must be {"confirm": true}`)
				return
			}
			if errors.Is(err, ipam.ErrSyncNotConfigured) {
				writeJSONError(w, http.StatusNotFound, "not_found", "external IPAM sync is not configured")
				return
			}
			writeJSONError(w, http.StatusServiceUnavailable, "external_ipam_unreachable", "could not apply external IPAM sync: "+err.Error())
			return
		}
		for _, rec := range res.Applied {
			auditIPAMExternal(r.Context(), audit, username, "ipam.external_sync", rec.IP, map[string]any{
				"kind": string(rec.Kind), "ok": rec.OK, "before": rec.Before, "after": rec.After, "error": rec.Error,
			})
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func decodeExternalSubnetRequest(w http.ResponseWriter, r *http.Request) (externalSubnetRequest, bool) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIPAMExternalBodyBytes))
	var req externalSubnetRequest
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed external subnet body: "+err.Error())
		return externalSubnetRequest{}, false
	}
	return req, true
}

func auditIPAMExternal(ctx context.Context, audit ipamExternalAuditWriter, username, action, target string, detail map[string]any) {
	if audit == nil {
		return
	}
	entry := store.AuditEntry{At: time.Now().Unix(), Username: username, Action: action, Result: "success"}
	if target != "" {
		entry.Target.String, entry.Target.Valid = target, true
	}
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			entry.DetailJSON.String, entry.DetailJSON.Valid = string(b), true
		}
	}
	_, _ = audit.Append(ctx, entry)
}

// mountFederationIPAMRoutes registers GET /federation/ipam/conflicts. src/auth
// required together; nil skips mounting (single-cluster deployments simply have
// no cross-cluster conflicts to surface).
func mountFederationIPAMRoutes(r chi.Router, src FederationIPAMSource, auth AuthService) {
	if src == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capFederationRead))
		r.Get("/federation/ipam/conflicts", handleFederationIPAMConflicts(src))
	})
}

func handleFederationIPAMConflicts(src FederationIPAMSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clusters, partial, failed, err := src.IPAMSubnets(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not aggregate cluster subnets")
			return
		}
		conflicts := ipam.CrossClusterConflicts(clusters)
		if conflicts == nil {
			conflicts = []ipam.Conflict{}
		}
		if failed == nil {
			failed = []string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":          conflicts,
			"partial":        partial,
			"failedClusters": failed,
		})
	}
}
