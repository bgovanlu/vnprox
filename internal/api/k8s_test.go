// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/k8s"
	"github.com/bgovanlu/vnprox/internal/k8smock"
	"github.com/bgovanlu/vnprox/internal/store"
)

const testKubeconfigYAML = `
apiVersion: v1
kind: Config
current-context: test
clusters:
  - name: c1
    cluster:
      server: https://k8s.example.internal:6443
contexts:
  - name: test
    context:
      cluster: c1
      user: u1
users:
  - name: u1
    user:
      token: test-token-value
`

// fakeK8sClusterStore is an in-memory K8sClusterStore test double.
type fakeK8sClusterStore struct {
	items map[string]store.K8sCluster
}

func newFakeK8sClusterStore() *fakeK8sClusterStore {
	return &fakeK8sClusterStore{items: map[string]store.K8sCluster{}}
}

func (f *fakeK8sClusterStore) List(context.Context) ([]store.K8sCluster, error) {
	var out []store.K8sCluster
	for _, c := range f.items {
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeK8sClusterStore) Get(_ context.Context, id string) (store.K8sCluster, error) {
	c, ok := f.items[id]
	if !ok {
		return store.K8sCluster{}, store.ErrNotFound
	}
	return c, nil
}

func (f *fakeK8sClusterStore) Insert(_ context.Context, c store.K8sCluster) error {
	f.items[c.ID] = c
	return nil
}

func (f *fakeK8sClusterStore) Delete(_ context.Context, id string) error {
	delete(f.items, id)
	return nil
}

func (f *fakeK8sClusterStore) UpdateStatus(_ context.Context, id, cni, status string) error {
	c, ok := f.items[id]
	if !ok {
		return nil
	}
	c.CNIDetected, c.Status = cni, status
	f.items[id] = c
	return nil
}

// fakeK8sIPAMSource returns a fixed allocation map.
type fakeK8sIPAMSource struct {
	allocs map[string][]ipam.Allocation
}

func (f fakeK8sIPAMSource) AllAllocations(context.Context) (map[string][]ipam.Allocation, error) {
	return f.allocs, nil
}

// fakeK8sAuditWriter records every appended entry.
type fakeK8sAuditWriter struct {
	entries []store.AuditEntry
}

func (f *fakeK8sAuditWriter) Append(_ context.Context, e store.AuditEntry) (int64, error) {
	f.entries = append(f.entries, e)
	return int64(len(f.entries)), nil
}

func k8sTestAuth(authenticated bool) fakeAuthWithUser {
	return fakeAuthWithUser{username: "root@pam", fakeAuth: fakeAuth{authenticated: authenticated}}
}

func newK8sTestRouter(clusters K8sClusterStore, cipher SecretCipher, poller K8sPoller, graph K8sGraph, ipamSrc K8sIPAMSource, audit k8sAuditWriter, auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:        auth,
		K8sClusters: clusters, K8sSecretCipher: cipher, K8sPoller: poller,
		K8sGraph: graph, K8sIPAM: ipamSrc, K8sAudit: audit,
	})
}

func TestK8sClusters_CreateListDelete(t *testing.T) {
	clusters := newFakeK8sClusterStore()
	cipher := fakeSecretCipher{}
	audit := &fakeK8sAuditWriter{}
	poller := k8s.NewPoller()
	graph := inventory.NewGraph()
	h := newK8sTestRouter(clusters, cipher, poller, graph, nil, audit, k8sTestAuth(true))

	// Create.
	body, _ := json.Marshal(k8sClusterCreateRequest{Name: "prod", Kubeconfig: testKubeconfigYAML})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/clusters", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST /k8s/clusters status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var created k8sClusterResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}
	if created.Name != "prod" || created.Status != "unpolled" {
		t.Errorf("created = %+v", created)
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("test-token-value")) {
		t.Error("create response must never echo the kubeconfig token")
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != "k8s.cluster.add" {
		t.Errorf("audit entries = %+v, want one k8s.cluster.add", audit.entries)
	}

	// A real AES-256-GCM SessionCipher's own encrypted-at-rest guarantee is
	// asserted directly against store.K8sClusterRepo in
	// internal/store/k8sclusters_test.go (T-1501 AC5); fakeSecretCipher
	// here is a deterministic passthrough stand-in purely for exercising
	// this route's wiring, so it is not meaningful to assert non-plaintext
	// ciphertext against it.
	if _, ok := clusters.items[created.ID]; !ok {
		t.Fatal("created cluster row not found in store")
	}

	// List.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/k8s/clusters", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /k8s/clusters status = %d", rr.Code)
	}
	var list k8sClustersListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding list response: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != created.ID {
		t.Errorf("list = %+v", list)
	}

	// Delete.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/k8s/clusters/"+created.ID, nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if len(audit.entries) != 2 || audit.entries[1].Action != "k8s.cluster.remove" {
		t.Errorf("audit entries after delete = %+v", audit.entries)
	}

	// Deleting again is not an error (mirrors every other repo's Delete
	// convention), and a 404 from the pre-delete Get lookup only occurs
	// when the row is genuinely already gone from the store — assert that
	// separately here.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/k8s/clusters/"+created.ID, nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("DELETE (already gone) status = %d, want 404", rr.Code)
	}
}

func TestK8sClusters_CreateRejectsMalformedKubeconfig(t *testing.T) {
	clusters := newFakeK8sClusterStore()
	h := newK8sTestRouter(clusters, fakeSecretCipher{}, k8s.NewPoller(), inventory.NewGraph(), nil, &fakeK8sAuditWriter{}, k8sTestAuth(true))

	body, _ := json.Marshal(k8sClusterCreateRequest{Name: "bad", Kubeconfig: "not: valid: yaml: at: all: ::: ["})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/clusters", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if len(clusters.items) != 0 {
		t.Error("a malformed kubeconfig must never be stored")
	}
}

func TestK8sClusters_CreateRejectsUnresolvableKubeconfig(t *testing.T) {
	clusters := newFakeK8sClusterStore()
	h := newK8sTestRouter(clusters, fakeSecretCipher{}, k8s.NewPoller(), inventory.NewGraph(), nil, &fakeK8sAuditWriter{}, k8sTestAuth(true))

	// Valid YAML, but no current-context — ResolveContext must reject it.
	body, _ := json.Marshal(k8sClusterCreateRequest{Name: "bad", Kubeconfig: "apiVersion: v1\nkind: Config\n"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/clusters", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rr.Code, rr.Body.String())
	}
}

func TestK8sClusters_Unauthenticated(t *testing.T) {
	clusters := newFakeK8sClusterStore()
	h := newK8sTestRouter(clusters, fakeSecretCipher{}, k8s.NewPoller(), inventory.NewGraph(), nil, &fakeK8sAuditWriter{}, k8sTestAuth(false))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/k8s/clusters", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

// TestK8sOverlay_LiveEndToEnd exercises GET /k8s/{clusterId}/overlay
// against a real internal/k8smock server, wired through a real
// internal/k8s.Poller — the node<->guest correlation (via a fake IPAM
// source + a real inventory graph carrying a matching Guest + FwRuleset)
// and the NodePort-exposure finding both come out the other end of the
// full HTTP round trip, not just a unit-level call into internal/k8s
// directly.
func TestK8sOverlay_LiveEndToEnd(t *testing.T) {
	f, err := k8smock.LoadFixtureFile("../../testdata/k8s/cluster-flannel.yaml")
	if err != nil {
		t.Fatalf("LoadFixtureFile: %v", err)
	}
	mockSrv, _ := k8smock.NewServer(f)
	defer mockSrv.Close()

	kubeconfig := `
apiVersion: v1
kind: Config
current-context: test
clusters:
  - name: c1
    cluster:
      server: ` + mockSrv.URL + `
contexts:
  - name: test
    context:
      cluster: c1
      user: u1
users:
  - name: u1
    user:
      token: test-token-value
`

	clusters := newFakeK8sClusterStore()
	clusters.items["c1"] = store.K8sCluster{ID: "c1", Name: "prod", AddedBy: "root@pam", AddedAt: 1, Status: "unpolled"}
	cipher := fakeSecretCipher{}
	enc, err := cipher.Encrypt([]byte(kubeconfig))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	c := clusters.items["c1"]
	c.KubeconfigEnc = enc
	clusters.items["c1"] = c

	// Build a live inventory graph carrying the guest node1's k8s node
	// (10.10.0.11) correlates to (guest:pve1:105), plus that guest's
	// firewall ruleset (deliberately missing an ACCEPT rule for the
	// fixture's NodePort 30080, so the finding must fire).
	graph := inventory.NewGraph()
	guest := &inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "105"}, VMID: 105, Node: "pve1", Type: "qemu", Status: "running"}
	guestFW := &inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "guest/qemu/105"}, Scope: inventory.FwScopeGuest, Enabled: true}
	graph.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Kinds: []inventory.Kind{inventory.KindGuest}}, []inventory.Entity{guest})
	graph.ApplyPoll(inventory.SourcePVEFirewall, inventory.Scope{Kinds: []inventory.Kind{inventory.KindFwRuleset}}, []inventory.Entity{guestFW})

	ipamSrc := fakeK8sIPAMSource{allocs: map[string][]ipam.Allocation{
		"10.10.0.0/24": {{IP: "10.10.0.11", VMID: 105}},
	}}

	poller := k8s.NewPoller()
	audit := &fakeK8sAuditWriter{}
	h := newK8sTestRouter(clusters, cipher, poller, graph, ipamSrc, audit, k8sTestAuth(true))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/k8s/c1/overlay", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var resp k8sOverlayResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding overlay response: %v", err)
	}
	if resp.CNI != k8s.CNIFlannel {
		t.Errorf("CNI = %q, want flannel", resp.CNI)
	}
	if len(resp.Nodes) != 2 {
		t.Fatalf("len(Nodes) = %d, want 2", len(resp.Nodes))
	}
	var matched, unmatched int
	for _, n := range resp.Nodes {
		if n.Matched {
			matched++
			if n.GuestRef != "guest:pve1:105" {
				t.Errorf("matched node GuestRef = %q", n.GuestRef)
			}
		} else {
			unmatched++
		}
	}
	if matched != 1 || unmatched != 1 {
		t.Errorf("matched=%d unmatched=%d, want 1/1", matched, unmatched)
	}
	if len(resp.Findings) != 1 {
		t.Fatalf("Findings = %+v, want 1 (no covering fw rule for NodePort 30080)", resp.Findings)
	}
	if resp.Findings[0].NodePort != 30080 {
		t.Errorf("finding.NodePort = %d, want 30080", resp.Findings[0].NodePort)
	}

	// The cluster's cached status must have been updated to "ok"/"flannel".
	updated := clusters.items["c1"]
	if updated.Status != "ok" || updated.CNIDetected != "flannel" {
		t.Errorf("cluster cache after poll = %+v", updated)
	}

	// The poller's own cache must now also carry this finding, so
	// internal/findings' K8sProvider seam would report it too.
	if len(poller.CachedFindings()) != 1 {
		t.Errorf("poller.CachedFindings() = %+v, want 1", poller.CachedFindings())
	}
}

func TestK8sOverlay_UnknownClusterIs404(t *testing.T) {
	clusters := newFakeK8sClusterStore()
	h := newK8sTestRouter(clusters, fakeSecretCipher{}, k8s.NewPoller(), inventory.NewGraph(), nil, &fakeK8sAuditWriter{}, k8sTestAuth(true))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/k8s/nope/overlay", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

// TestK8sRoutes_ZeroWriteSurface is part of T-1501 AC4's regression: the
// registered route set for this family is exactly the four documented
// read/registration routes — no route anywhere lets a client mutate the
// k8s cluster itself (POST/DELETE only ever touch the local
// k8s_clusters registration row).
func TestK8sRoutes_ZeroWriteSurface(t *testing.T) {
	clusters := newFakeK8sClusterStore()
	h := newK8sTestRouter(clusters, fakeSecretCipher{}, k8s.NewPoller(), inventory.NewGraph(), nil, &fakeK8sAuditWriter{}, k8sTestAuth(true))

	// PUT/PATCH on the clusters collection must not be a valid mutation
	// path (only POST is registered there).
	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		req := httptest.NewRequest(method, "/api/v1/k8s/clusters", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code == http.StatusOK || rr.Code == http.StatusNoContent {
			t.Errorf("%s /k8s/clusters unexpectedly succeeded (status %d)", method, rr.Code)
		}
	}
	// The overlay route is GET-only.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/c1/overlay", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code == http.StatusOK {
		t.Error("POST /k8s/{id}/overlay unexpectedly succeeded")
	}
}
