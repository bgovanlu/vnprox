// k8s.go implements T-1501's Kubernetes overlay mapping engine routes
// (docs/api.md's new Kubernetes section):
//
//   - GET    /k8s/clusters             — list registered clusters (kubeconfig never echoed back)
//   - POST   /k8s/clusters             — register a cluster {name, kubeconfig} → 201
//   - DELETE /k8s/clusters/{id}        — deregister a cluster; 204 whether or not it previously existed
//   - GET    /k8s/{clusterId}/overlay  — live poll: pod/service CIDR model, node<->guest
//     correlation, detected CNI, NodePort-exposure findings
//
// GET routes are netRead-gated; POST/DELETE are netWrite+CSRF, matching
// every other read/write route family in this package. Kubernetes
// integration is READ-ONLY FOREVER (internal/k8s's own doc comment,
// carried forward from docs/roadmap-universal.md's Phase 15 Invariants):
// this file mounts no route that could ever cause vnprox to write to a
// k8s cluster — POST/DELETE /k8s/clusters only ever add/remove a *local*
// registration row, never touch the cluster itself.
//
// Kubeconfig credential material is encrypted at rest via SecretCipher
// (alertrules.go's own seam, reused verbatim, identical AES-256-GCM
// primitive every other secret column in this codebase uses) — this file
// never returns plaintext or ciphertext back to the client.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/k8s"
	"github.com/bgovanlu/vnprox/internal/store"
)

// capK8sRead/capK8sWrite reuse the standard netRead/netWrite capability
// flags (docs/api.md's documented `/auth/me` capability set has no
// dedicated k8sRead/k8sWrite — registering a read-only discovery source
// is the same class of action ingress/WAN-target registration already
// gates on netRead/netWrite) rather than inventing a new capability flag,
// the identical "smallest reasonable extension" call T-405's IPAM routes
// made for capIPAMRead.
const (
	capK8sRead  = capNetRead
	capK8sWrite = capNetWrite
)

// maxK8sClusterBodyBytes bounds a create-cluster request body — a
// kubeconfig with an embedded CA bundle can run a few KB, so this is
// generous headroom, not a realistic limit (mirrors
// maxIngressTargetBodyBytes' reasoning at a larger scale since a
// kubeconfig is bigger than an ingress target's {kind,address,credential}).
const maxK8sClusterBodyBytes = 64 << 10 // 64 KiB

// k8sPollTimeout bounds one GET /k8s/{clusterId}/overlay's live poll
// against the cluster's own API server — the outer per-request ceiling
// above internal/k8s.Client's own per-call DefaultTimeout.
const k8sPollTimeout = 20 * time.Second

// K8sClusterStore is the subset of *store.K8sClusterRepo the router needs.
type K8sClusterStore interface {
	List(ctx context.Context) ([]store.K8sCluster, error)
	Get(ctx context.Context, id string) (store.K8sCluster, error)
	Insert(ctx context.Context, c store.K8sCluster) error
	Delete(ctx context.Context, id string) error
	UpdateStatus(ctx context.Context, id, cniDetected, status string) error
}

// K8sGraph resolves the live inventory snapshot node<->guest correlation
// and the NodePort-exposure firewall check both need — the same
// one-method seam FirewallGraph/SpecInventory/SimulatorGraph already use
// in this package.
type K8sGraph interface {
	Snapshot() inventory.Snapshot
}

// K8sIPAMSource backs node<->guest correlation: *ipam.Service's existing
// AllAllocations method (already exported for T-406's DHCP-range-overlap
// check). Nil simply omits guest correlation (every k8s node reports
// unmatched) rather than failing the whole overlay read — the same
// optional-dependency degrade-gracefully convention every other read view
// in this package follows (e.g. IPAM's own nil-safety elsewhere).
type K8sIPAMSource interface {
	AllAllocations(ctx context.Context) (map[string][]ipam.Allocation, error)
}

// K8sPoller is the subset of *k8s.Poller the router needs.
type K8sPoller interface {
	Poll(ctx context.Context, clusterID string, client *k8s.Client, index k8s.GuestIPIndex, lookup k8s.FwLookup, now func() time.Time) (k8s.Overlay, []k8s.NodePortFinding, error)
	Forget(clusterID string)
}

// k8sAuditWriter is the minimal audit-log seam this route family needs —
// exactly *store.AuditRepo's own Append signature (the identical seam
// lldpInstallAuditor already defines for its own routes), declared
// locally so this file's dependency on the concrete repo stays a
// one-method interface.
type k8sAuditWriter interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

type k8sClusterResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AddedBy     string `json:"addedBy"`
	CNIDetected string `json:"cniDetected,omitempty"`
	Status      string `json:"status"`
	AddedAt     int64  `json:"addedAt"`
}

func toK8sClusterResponse(c store.K8sCluster) k8sClusterResponse {
	return k8sClusterResponse{
		ID: c.ID, Name: c.Name, AddedBy: c.AddedBy, AddedAt: c.AddedAt,
		CNIDetected: c.CNIDetected, Status: c.Status,
	}
}

type k8sClustersListResponse struct {
	Items []k8sClusterResponse `json:"items"`
}

// k8sClusterCreateRequest is POST /k8s/clusters' request body. Kubeconfig
// is the raw kubeconfig YAML text (internal/k8s.ParseKubeconfig's input),
// never persisted in plaintext — see mountK8sRoutes' doc comment.
type k8sClusterCreateRequest struct {
	Name       string `json:"name"`
	Kubeconfig string `json:"kubeconfig"`
}

// mountK8sRoutes registers the routes above. clusters/cipher/poller/graph/
// audit/auth are required together (any one nil skips mounting the whole
// family, matching every other optional Options field's degraded-mode
// convention); ipamSrc is optional (nil narrows node<->guest correlation
// rather than failing the whole read, same as EdgeIPAMSource-shaped seams
// elsewhere in this codebase).
func mountK8sRoutes(r chi.Router, clusters K8sClusterStore, cipher SecretCipher, poller K8sPoller, graph K8sGraph, ipamSrc K8sIPAMSource, audit k8sAuditWriter, auth AuthService) {
	if clusters == nil || cipher == nil || poller == nil || graph == nil || audit == nil || auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capK8sRead))
		r.Get("/k8s/clusters", handleListK8sClusters(clusters))
		r.Get("/k8s/{clusterId}/overlay", handleK8sOverlay(clusters, cipher, poller, graph, ipamSrc))
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capK8sWrite))
		r.Post("/k8s/clusters", handleCreateK8sCluster(clusters, cipher, audit, lookup))
		r.Delete("/k8s/clusters/{id}", handleDeleteK8sCluster(clusters, poller, audit, lookup))
	})
}

func handleListK8sClusters(clusters K8sClusterStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := clusters.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list k8s clusters")
			return
		}
		items := make([]k8sClusterResponse, 0, len(list))
		for _, c := range list {
			items = append(items, toK8sClusterResponse(c))
		}
		writeJSON(w, http.StatusOK, k8sClustersListResponse{Items: items})
	}
}

func handleCreateK8sCluster(clusters K8sClusterStore, cipher SecretCipher, audit k8sAuditWriter, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		var req k8sClusterCreateRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxK8sClusterBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed k8s cluster body: "+err.Error())
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "name is required")
			return
		}
		kc, err := k8s.ParseKubeconfig([]byte(req.Kubeconfig))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "could not parse kubeconfig: "+err.Error())
			return
		}
		if _, resolveErr := k8s.ResolveContext(kc); resolveErr != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "kubeconfig is not usable: "+resolveErr.Error())
			return
		}

		enc, err := cipher.Encrypt([]byte(req.Kubeconfig))
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not encrypt kubeconfig")
			return
		}

		c := store.K8sCluster{
			ID: store.NewULID(), Name: req.Name, KubeconfigEnc: enc,
			AddedBy: username, AddedAt: time.Now().Unix(), Status: "unpolled",
		}
		if err := clusters.Insert(r.Context(), c); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save k8s cluster")
			return
		}

		auditK8sAction(r.Context(), audit, username, "k8s.cluster.add", c.ID, map[string]any{"name": c.Name})
		writeJSON(w, http.StatusCreated, toK8sClusterResponse(c))
	}
}

func handleDeleteK8sCluster(clusters K8sClusterStore, poller K8sPoller, audit k8sAuditWriter, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		if _, err := clusters.Get(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such k8s cluster")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not look up k8s cluster")
			return
		}
		if err := clusters.Delete(r.Context(), id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not delete k8s cluster")
			return
		}
		poller.Forget(id)
		auditK8sAction(r.Context(), audit, username, "k8s.cluster.remove", id, nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

// k8sNodePortFindingResponse mirrors k8s.NodePortFinding's JSON shape —
// carried alongside the overlay so a caller (T-1502's map layer, or a
// manual poll) sees the exact findings this poll just computed, without a
// second round-trip to GET /findings (which reports the same data via
// internal/findings' K8sProvider seam, sourced from the same Poller
// cache this handler populates).
type k8sOverlayResponse struct {
	Findings []k8s.NodePortFinding `json:"nodePortFindings,omitempty"`
	k8s.Overlay
}

func handleK8sOverlay(clusters K8sClusterStore, cipher SecretCipher, poller K8sPoller, graph K8sGraph, ipamSrc K8sIPAMSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "clusterId")
		row, err := clusters.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such k8s cluster")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not look up k8s cluster")
			return
		}

		raw, err := cipher.Decrypt(row.KubeconfigEnc)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not decrypt kubeconfig")
			return
		}
		kc, err := k8s.ParseKubeconfig(raw)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "stored kubeconfig no longer parses")
			return
		}
		rc, err := k8s.ResolveContext(kc)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "stored kubeconfig is no longer usable: "+err.Error())
			return
		}
		client, err := k8s.NewClient(rc)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not build k8s client: "+err.Error())
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), k8sPollTimeout)
		defer cancel()

		index := k8sGuestIPIndex(ctx, ipamSrc, graph)
		lookup := k8sFwLookup(graph)

		overlay, findings, pollErr := poller.Poll(ctx, id, client, index, lookup, nil)
		if pollErr != nil {
			_ = clusters.UpdateStatus(r.Context(), id, row.CNIDetected, "unreachable")
			writeJSONError(w, http.StatusBadGateway, "k8s_unreachable", "could not poll k8s cluster: "+pollErr.Error())
			return
		}
		_ = clusters.UpdateStatus(r.Context(), id, string(overlay.CNI), "ok")

		writeJSON(w, http.StatusOK, k8sOverlayResponse{Overlay: overlay, Findings: findings})
	}
}

// k8sGuestIPIndex adapts ipamSrc's raw allocation map plus graph's live
// inventory snapshot into a k8s.GuestIPIndex: resolve an address to the
// VMID an IPAM allocation records, then to that VMID's known Guest
// entity's ref. An address with no allocation, or an allocation whose
// VMID names no currently known guest, correlates to nothing — the
// identical "never guessed" resolution this codebase's other IP->guest
// lookups (e.g. ipam's own conflict detection) already apply. Returns nil
// (every node then surfaces unmatched) if ipamSrc/graph is nil, or the
// allocation read fails.
func k8sGuestIPIndex(ctx context.Context, ipamSrc K8sIPAMSource, graph K8sGraph) k8s.GuestIPIndex {
	if ipamSrc == nil || graph == nil {
		return nil
	}
	allocs, err := ipamSrc.AllAllocations(ctx)
	if err != nil {
		return nil
	}

	byVMID := make(map[int]*inventory.Guest)
	for _, e := range graph.Snapshot().All() {
		if g, ok := e.(*inventory.Guest); ok {
			byVMID[g.VMID] = g
		}
	}
	ipToVMID := make(map[string]int)
	for _, list := range allocs {
		for _, a := range list {
			if a.VMID != 0 {
				ipToVMID[a.IP] = a.VMID
			}
		}
	}

	return func(ip string) (string, bool) {
		vmid, ok := ipToVMID[ip]
		if !ok {
			return "", false
		}
		g, ok := byVMID[vmid]
		if !ok {
			return "", false
		}
		return g.GetRef().String(), true
	}
}

// k8sFwLookup adapts graph's live inventory snapshot into a k8s.FwLookup:
// resolve a correlated k8s node's guest ref to its own guest-scope
// FwRuleset (Ref shape "guest/<qemu|lxc>/<vmid>", the exact scheme
// internal/collect's pollFirewall constructs — see that package's pve.go)
// plus the single cluster-scope ruleset every guest also sees (real
// pve-firewall's own visibility rule). Returns nil if graph is nil.
func k8sFwLookup(graph K8sGraph) k8s.FwLookup {
	if graph == nil {
		return nil
	}
	snap := graph.Snapshot()

	guestByRef := make(map[string]*inventory.Guest)
	fwByRef := make(map[string]*inventory.FwRuleset)
	var clusterRS *inventory.FwRuleset
	for _, e := range snap.All() {
		switch v := e.(type) {
		case *inventory.Guest:
			guestByRef[v.GetRef().String()] = v
		case *inventory.FwRuleset:
			fwByRef[v.GetRef().String()] = v
			if v.Scope == inventory.FwScopeCluster {
				clusterRS = v
			}
		}
	}

	return func(guestRef string) (guest, cluster *inventory.FwRuleset) {
		g, ok := guestByRef[guestRef]
		if !ok {
			return nil, clusterRS
		}
		fwRef := inventory.Ref{Kind: inventory.KindFwRuleset, Node: g.Node, ID: "guest/" + g.Type + "/" + strconv.Itoa(g.VMID)}
		return fwByRef[fwRef.String()], clusterRS
	}
}

func auditK8sAction(ctx context.Context, audit k8sAuditWriter, username, action, target string, detail map[string]any) {
	if audit == nil {
		return
	}
	entry := store.AuditEntry{At: time.Now().Unix(), Username: username, Action: action, Result: "ok"}
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
