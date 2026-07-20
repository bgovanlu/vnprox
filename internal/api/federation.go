// federation.go implements T-1201's cluster-registry routes (docs/api.md's
// Federation section):
//
//   - GET    /federation/clusters       — list attached clusters (credential never echoed back)
//   - POST   /federation/clusters       — attach a cluster {name, apiUrl, credential} → 201
//   - GET    /federation/clusters/{id}  — one attached cluster (credential-free)
//   - PUT    /federation/clusters/{id}  — edit name/apiUrl and optionally re-seal the credential
//   - DELETE /federation/clusters/{id}  — detach a cluster; 204 whether or not it existed
//
// GET routes are netRead-gated; POST/PUT/DELETE are netWrite+CSRF, matching
// every other read/write route family in this package. Credential material is
// sealed at rest by internal/federation.Service (the same AES-256-GCM
// primitive sessions.pve_ticket_enc uses); this file never returns plaintext
// or ciphertext to the client, and never accepts a cross-cluster mutation —
// the registry only ever adds/removes/edits a *local* registration row, never
// touches an attached cluster's own config (federation federates views, never
// config ownership).

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/federation"
	"github.com/bgovanlu/vnprox/internal/store"
)

// capFederationRead/capFederationWrite reuse the standard netRead/netWrite
// capability flags — attaching a read-aggregation source is the same class of
// action alert-rule / (in later phases) k8s-cluster registration already
// gates on netRead/netWrite, so this follows that "smallest reasonable
// extension" precedent rather than inventing a federation-specific flag.
const (
	capFederationRead  = capNetRead
	capFederationWrite = capNetWrite
)

// maxFederationClusterBodyBytes bounds an attach/edit request body — a
// name+apiUrl+credential is small; this is generous defensive headroom.
const maxFederationClusterBodyBytes = 16 << 10 // 16 KiB

// FederationService is the subset of *federation.Service the router needs.
type FederationService interface {
	Add(ctx context.Context, name, apiURL string, cred federation.Credential, addedBy string) (federation.Cluster, error)
	Get(ctx context.Context, id string) (federation.Cluster, error)
	List(ctx context.Context) ([]federation.Cluster, error)
	Update(ctx context.Context, id, name, apiURL string, cred *federation.Credential) (federation.Cluster, error)
	Delete(ctx context.Context, id string) error
}

// federationAuditWriter is the minimal audit seam this route family needs —
// exactly *store.AuditRepo's Append signature, declared locally so the
// dependency stays a one-method interface. Optional (nil skips audit).
type federationAuditWriter interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

type federationClusterResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	APIURL  string `json:"apiUrl"`
	Status  string `json:"status"`
	AddedBy string `json:"addedBy"`
	AddedAt int64  `json:"addedAt"`
}

func toFederationClusterResponse(c federation.Cluster) federationClusterResponse {
	return federationClusterResponse{ID: c.ID, Name: c.Name, APIURL: c.APIURL, Status: c.Status, AddedBy: c.AddedBy, AddedAt: c.AddedAt}
}

type federationClustersListResponse struct {
	Items []federationClusterResponse `json:"items"`
}

// federationCredentialRequest is the credential material an attach/edit
// carries. Exactly one form is provided (ticket username/password[/realm], or
// a PVE API token). It is never echoed back in any response.
type federationCredentialRequest struct {
	Kind     string `json:"kind"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Realm    string `json:"realm,omitempty"`
	Token    string `json:"token,omitempty"`
}

func (c federationCredentialRequest) toCredential() federation.Credential {
	return federation.Credential{Kind: c.Kind, Username: c.Username, Password: c.Password, Realm: c.Realm, Token: c.Token}
}

// federationClusterCreateRequest is POST /federation/clusters' body.
type federationClusterCreateRequest struct {
	Name       string                      `json:"name"`
	APIURL     string                      `json:"apiUrl"`
	Credential federationCredentialRequest `json:"credential"`
}

// federationClusterUpdateRequest is PUT /federation/clusters/{id}'s body. An
// absent/null credential leaves the stored one untouched (a rename must not
// force re-entering the token).
type federationClusterUpdateRequest struct {
	Name       string                       `json:"name"`
	APIURL     string                       `json:"apiUrl"`
	Credential *federationCredentialRequest `json:"credential"`
}

// mountFederationRoutes registers the routes above. svc/auth are required
// together (either nil skips mounting the whole family); audit is optional.
func mountFederationRoutes(r chi.Router, svc FederationService, audit federationAuditWriter, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capFederationRead))
		r.Get("/federation/clusters", handleListFederationClusters(svc))
		r.Get("/federation/clusters/{id}", handleGetFederationCluster(svc))
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capFederationWrite))
		r.Post("/federation/clusters", handleCreateFederationCluster(svc, audit, lookup))
		r.Put("/federation/clusters/{id}", handleUpdateFederationCluster(svc, audit, lookup))
		r.Delete("/federation/clusters/{id}", handleDeleteFederationCluster(svc, audit, lookup))
	})
}

func handleListFederationClusters(svc FederationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list clusters")
			return
		}
		items := make([]federationClusterResponse, 0, len(list))
		for _, c := range list {
			items = append(items, toFederationClusterResponse(c))
		}
		writeJSON(w, http.StatusOK, federationClustersListResponse{Items: items})
	}
}

func handleGetFederationCluster(svc FederationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := svc.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such cluster")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not look up cluster")
			return
		}
		writeJSON(w, http.StatusOK, toFederationClusterResponse(c))
	}
}

func handleCreateFederationCluster(svc FederationService, audit federationAuditWriter, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var req federationClusterCreateRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFederationClusterBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed cluster body: "+err.Error())
			return
		}
		if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.APIURL) == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "name and apiUrl are required")
			return
		}
		c, err := svc.Add(r.Context(), req.Name, req.APIURL, req.Credential.toCredential(), username)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "could not attach cluster: "+err.Error())
			return
		}
		auditFederationAction(r.Context(), audit, username, "federation.cluster.add", c.ID, map[string]any{"name": c.Name})
		writeJSON(w, http.StatusCreated, toFederationClusterResponse(c))
	}
}

func handleUpdateFederationCluster(svc FederationService, audit federationAuditWriter, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		var req federationClusterUpdateRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFederationClusterBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed cluster body: "+err.Error())
			return
		}
		var cred *federation.Credential
		if req.Credential != nil {
			c := req.Credential.toCredential()
			cred = &c
		}
		updated, err := svc.Update(r.Context(), id, req.Name, req.APIURL, cred)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such cluster")
				return
			}
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "could not update cluster: "+err.Error())
			return
		}
		auditFederationAction(r.Context(), audit, username, "federation.cluster.update", id, map[string]any{"name": updated.Name})
		writeJSON(w, http.StatusOK, toFederationClusterResponse(updated))
	}
}

func handleDeleteFederationCluster(svc FederationService, audit federationAuditWriter, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		if err := svc.Delete(r.Context(), id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not delete cluster")
			return
		}
		auditFederationAction(r.Context(), audit, username, "federation.cluster.remove", id, nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

func auditFederationAction(ctx context.Context, audit federationAuditWriter, username, action, target string, detail map[string]any) {
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
