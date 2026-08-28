// SPDX-License-Identifier: Apache-2.0

// ingress.go implements T-1406's ingress visibility routes (docs/api.md's
// Ingress visibility section):
//
//   - GET    /ingress/targets      — list every configured discovery target (credential never echoed back)
//   - POST   /ingress/targets      — add a target {kind, address, credential?}
//   - DELETE /ingress/targets/{id} — remove a target
//   - GET    /ingress/status       — discover every target fresh and correlate the WAN -> port-forward
//     -> proxy guest -> backend guest chain against T-1403's edge/NAT model
//
// GET is netRead-gated; POST/DELETE are netWrite + CSRF, matching every
// other write route family in this package. GET /ingress/status issues no
// write of its own either — it only calls internal/ingress.IngressDiscoverer.
// Discover (itself GET-only against the target, see that package's doc
// comment) fresh on every request, never persisting a target's discovered
// state (docs/architecture.md §7's new-domain invariant). Discovery only
// ever iterates rows already in the ingress_targets table — there is no
// code path here that adds a target on its own (T-1406 AC5).
//
// Credentials are encrypted at rest via SecretCipher (alertrules.go's own
// seam, reused verbatim) — this package never returns plaintext or
// ciphertext back to the client; GET responses carry only
// `hasCredential bool`, matching GET /alert-rules' `hasSecret` contract.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/edge"
	"github.com/bgovanlu/vnprox/internal/ingress"
	"github.com/bgovanlu/vnprox/internal/store"
)

// maxIngressTargetBodyBytes bounds a create-target request body — generous
// headroom for {kind, address, credential}, mirroring maxWebhookBodyBytes'
// own reasoning.
const maxIngressTargetBodyBytes = 16 << 10 // 16 KiB

// ingressDiscoverTimeout bounds how long GET /ingress/status waits on any
// one target's Discover call — each vendor discoverer already carries its
// own HTTP client timeout, this is the outer per-target ceiling so one
// hanging target can never stall the whole response past a fixed bound.
const ingressDiscoverTimeout = 8 * time.Second

// validIngressKinds mirrors internal/ingress.ValidKinds as a lookup set for
// POST /ingress/targets' `kind` validation.
var validIngressKinds = func() map[string]bool {
	m := make(map[string]bool, len(ingress.ValidKinds))
	for _, k := range ingress.ValidKinds {
		m[string(k)] = true
	}
	return m
}()

// IngressTargetStore is the subset of *store.IngressTargetRepo the router
// needs.
type IngressTargetStore interface {
	List(ctx context.Context) ([]store.IngressTarget, error)
	Get(ctx context.Context, id string) (store.IngressTarget, error)
	Insert(ctx context.Context, t store.IngressTarget) error
	Delete(ctx context.Context, id string) error
}

type ingressTargetResponse struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Address       string `json:"address"`
	AddedBy       string `json:"addedBy"`
	AddedAt       int64  `json:"addedAt"`
	HasCredential bool   `json:"hasCredential"`
}

type ingressTargetsListResponse struct {
	Items []ingressTargetResponse `json:"items"`
}

// ingressTargetRequest is POST /ingress/targets' request body.
type ingressTargetRequest struct {
	Kind       string `json:"kind"`
	Address    string `json:"address"`
	Credential string `json:"credential,omitempty"`
}

func toIngressTargetResponse(t store.IngressTarget) ingressTargetResponse {
	return ingressTargetResponse{
		ID: t.ID, Kind: t.Kind, Address: t.Address, AddedBy: t.AddedBy, AddedAt: t.AddedAt,
		HasCredential: len(t.CredentialEnc) > 0,
	}
}

func validateIngressTargetRequest(req ingressTargetRequest) string {
	if !validIngressKinds[req.Kind] {
		return "kind must be one of haproxy|nginx|caddy|traefik"
	}
	u, err := url.ParseRequestURI(req.Address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "address must be an absolute http(s) URL"
	}
	return ""
}

// ingressBackendResponse is one Chain-less, top-level discovered backend
// under GET /ingress/status's `targets[].backends` — every backend the
// target reports, correlated to a guest ref where resolvable (T-1406 AC2),
// independent of whether it also appears in a `chains[]` entry.
type ingressBackendResponse struct {
	Route    string `json:"route,omitempty"`
	Address  string `json:"address"`
	GuestRef string `json:"guestRef,omitempty"`
	Healthy  bool   `json:"healthy"`
}

type ingressTargetStatusResponse struct {
	ID        string                   `json:"id"`
	Kind      string                   `json:"kind"`
	Address   string                   `json:"address"`
	Error     string                   `json:"error,omitempty"`
	Backends  []ingressBackendResponse `json:"backends"`
	Reachable bool                     `json:"reachable"`
}

type ingressStatusResponse struct {
	Targets     []ingressTargetStatusResponse `json:"targets"`
	Chains      []ingress.Chain               `json:"chains"`
	GeneratedAt int64                         `json:"generatedAt"`
}

// mountIngressRoutes registers the routes above. targets/discoverer/
// ifacesSrc/graph/auth are required together (any nil skips mounting the
// whole family, matching every other optional Options field's degraded-
// mode convention); cipher is required for POST to encrypt a credential
// but GET/DELETE don't need it — still required together for simplicity,
// same as AlertSecretCipher's own all-or-nothing wiring; ipamSrc is
// optional (nil narrows GET /ingress/status's guest correlation rather
// than failing it, same as EdgeIPAMSource elsewhere).
func mountIngressRoutes(r chi.Router, targets IngressTargetStore, cipher SecretCipher, discoverer ingress.IngressDiscoverer, ifacesSrc EdgeInterfacesSource, graph EdgeGraph, ipamSrc EdgeIPAMSource, audit tokenAuditor, auth AuthService) {
	if targets == nil || cipher == nil || discoverer == nil || ifacesSrc == nil || graph == nil || audit == nil || auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/ingress/targets", handleListIngressTargets(targets))
		r.Get("/ingress/status", handleIngressStatus(targets, cipher, discoverer, ifacesSrc, graph, ipamSrc))
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/ingress/targets", handleCreateIngressTarget(targets, cipher, audit, lookup))
		r.Delete("/ingress/targets/{id}", handleDeleteIngressTarget(targets, audit, lookup))
	})
}

func handleListIngressTargets(targets IngressTargetStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := targets.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list ingress targets")
			return
		}
		items := make([]ingressTargetResponse, 0, len(list))
		for _, t := range list {
			items = append(items, toIngressTargetResponse(t))
		}
		writeJSON(w, http.StatusOK, ingressTargetsListResponse{Items: items})
	}
}

func handleCreateIngressTarget(targets IngressTargetStore, cipher SecretCipher, audit tokenAuditor, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		var req ingressTargetRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIngressTargetBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed ingress target body: "+err.Error())
			return
		}
		if msg := validateIngressTargetRequest(req); msg != "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", msg)
			return
		}

		var credEnc []byte
		if req.Credential != "" {
			enc, err := cipher.Encrypt([]byte(req.Credential))
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not encrypt credential")
				return
			}
			credEnc = enc
		}

		t := store.IngressTarget{
			ID: store.NewULID(), Kind: req.Kind, Address: req.Address, CredentialEnc: credEnc,
			AddedBy: username, AddedAt: time.Now().Unix(),
		}
		if err := targets.Insert(r.Context(), t); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save ingress target")
			return
		}

		auditTokenAction(r.Context(), audit, username, "ingress.target_add", t.ID, map[string]any{"kind": t.Kind, "address": t.Address})
		writeJSON(w, http.StatusCreated, toIngressTargetResponse(t))
	}
}

func handleDeleteIngressTarget(targets IngressTargetStore, audit tokenAuditor, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		if _, err := targets.Get(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such ingress target")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not look up ingress target")
			return
		}
		if err := targets.Delete(r.Context(), id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not delete ingress target")
			return
		}
		auditTokenAction(r.Context(), audit, username, "ingress.target_remove", id, nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleIngressStatus(targets IngressTargetStore, cipher SecretCipher, discoverer ingress.IngressDiscoverer, ifacesSrc EdgeInterfacesSource, graph EdgeGraph, ipamSrc EdgeIPAMSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		rows, err := targets.List(ctx)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list ingress targets")
			return
		}

		portForwards, guestLookup := gatherIngressPortForwards(ctx, ifacesSrc, graph, ipamSrc)

		states := discoverAll(ctx, discoverer, cipher, rows)

		targetResponses := make([]ingressTargetStatusResponse, 0, len(rows))
		chainInputs := make([]ingress.TargetChainInput, 0, len(rows))
		for i, row := range rows {
			state := states[i]
			backends := make([]ingressBackendResponse, 0, len(state.Backends))
			for _, b := range state.Backends {
				br := ingressBackendResponse{Route: b.Route, Address: b.Address, Healthy: b.Healthy}
				if guestLookup != nil {
					if ref, _, ok := guestLookup(ingress.HostOnly(b.Address)); ok {
						br.GuestRef = ref
					}
				}
				backends = append(backends, br)
			}
			targetResponses = append(targetResponses, ingressTargetStatusResponse{
				ID: row.ID, Kind: row.Kind, Address: row.Address,
				Reachable: state.Reachable, Error: state.Error, Backends: backends,
			})
			chainInputs = append(chainInputs, ingress.TargetChainInput{TargetID: row.ID, Address: row.Address, State: state})
		}

		chains := ingress.ProjectChains(portForwards, chainInputs, ingress.GuestLookup(guestLookup))

		writeJSON(w, http.StatusOK, ingressStatusResponse{
			Targets: targetResponses, Chains: chains, GeneratedAt: time.Now().Unix(),
		})
	}
}

// discoverAll calls discoverer.Discover against every row concurrently
// (each already individually timeout-bounded by its own HTTP client, plus
// ingressDiscoverTimeout as the outer per-target ceiling here), returning
// one ProxyState per row in the same order — a slow or unreachable target
// never delays any other target's result.
func discoverAll(ctx context.Context, discoverer ingress.IngressDiscoverer, cipher SecretCipher, rows []store.IngressTarget) []ingress.ProxyState {
	out := make([]ingress.ProxyState, len(rows))
	var wg sync.WaitGroup
	for i, row := range rows {
		wg.Add(1)
		go func(i int, row store.IngressTarget) {
			defer wg.Done()
			tCtx, cancel := context.WithTimeout(ctx, ingressDiscoverTimeout)
			defer cancel()

			var cred string
			if len(row.CredentialEnc) > 0 {
				if plain, err := cipher.Decrypt(row.CredentialEnc); err == nil {
					cred = string(plain)
				}
			}
			target := ingress.Target{ID: row.ID, Kind: ingress.Kind(row.Kind), Address: row.Address, Credential: cred}
			state, err := discoverer.Discover(tCtx, target)
			if err != nil {
				state = ingress.ProxyState{TargetID: row.ID, Kind: ingress.Kind(row.Kind), Reachable: false, Error: err.Error()}
			}
			out[i] = state
		}(i, row)
	}
	wg.Wait()
	return out
}

// gatherIngressPortForwards adapts edge.ProjectNAT's own port-forward list
// (the identical projection GET /edge/nat already computes — no second
// interfaces-file read path) into ingress.PortForwardRef, plus the same
// IPAM-based GuestLookup buildGuestLookup (edge.go) already builds, reused
// verbatim rather than duplicated. Best-effort: any projection failure
// (a node's interfaces file failed to parse) simply yields no port-forward
// input rather than failing the whole /ingress/status response — chain
// correlation then just finds nothing, the same degrade-gracefully
// treatment nil dependencies get elsewhere in this file.
func gatherIngressPortForwards(ctx context.Context, ifacesSrc EdgeInterfacesSource, graph EdgeGraph, ipamSrc EdgeIPAMSource) ([]ingress.PortForwardRef, edge.GuestLookup) {
	snap := graph.Snapshot()
	nodes := clusterNodeNames(snap)
	inputs := gatherNodeInterfaces(ctx, ifacesSrc, nodes)

	var lookup edge.GuestLookup
	if ipamSrc != nil {
		if allocs, err := ipamSrc.AllAllocations(ctx); err == nil {
			lookup = buildGuestLookup(allocs, snap)
		}
	}

	natView, err := edge.ProjectNAT(inputs, nil, lookup)
	if err != nil {
		return nil, lookup
	}
	out := make([]ingress.PortForwardRef, 0, len(natView.PortForwards))
	for _, pf := range natView.PortForwards {
		out = append(out, ingress.PortForwardRef{
			ID: pf.ID, Node: pf.Node, Proto: pf.Proto, IntIP: pf.IntIP,
			TargetGuestRef: pf.TargetGuestRef, ExtPort: pf.ExtPort, IntPort: pf.IntPort,
		})
	}
	return out, lookup
}
