// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change/ifaces"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/ingress"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeIngressTargetStore is an in-memory IngressTargetStore test double.
type fakeIngressTargetStore struct {
	items map[string]store.IngressTarget
}

func newFakeIngressTargetStore() *fakeIngressTargetStore {
	return &fakeIngressTargetStore{items: map[string]store.IngressTarget{}}
}

func (f *fakeIngressTargetStore) List(context.Context) ([]store.IngressTarget, error) {
	var out []store.IngressTarget
	for _, t := range f.items {
		out = append(out, t)
	}
	return out, nil
}

func (f *fakeIngressTargetStore) Get(_ context.Context, id string) (store.IngressTarget, error) {
	t, ok := f.items[id]
	if !ok {
		return store.IngressTarget{}, store.ErrNotFound
	}
	return t, nil
}

func (f *fakeIngressTargetStore) Insert(_ context.Context, t store.IngressTarget) error {
	f.items[t.ID] = t
	return nil
}

func (f *fakeIngressTargetStore) Delete(_ context.Context, id string) error {
	delete(f.items, id)
	return nil
}

// fakeIngressDiscoverer returns a fixed ProxyState per target ID, and
// records every target it was called with (T-1406 AC5's regression check:
// only targets the caller explicitly handed it are ever contacted).
type fakeIngressDiscoverer struct {
	byID  map[string]ingress.ProxyState
	calls []ingress.Target
}

func (f *fakeIngressDiscoverer) Discover(_ context.Context, target ingress.Target) (ingress.ProxyState, error) {
	f.calls = append(f.calls, target)
	if st, ok := f.byID[target.ID]; ok {
		return st, nil
	}
	return ingress.ProxyState{TargetID: target.ID, Kind: target.Kind, Reachable: false, Error: "no fixture"}, nil
}

func newIngressTestRouter(t *testing.T, targets IngressTargetStore, cipher SecretCipher, discoverer ingress.IngressDiscoverer, ifacesSrc EdgeInterfacesSource, graph EdgeGraph, ipamSrc EdgeIPAMSource, audit tokenAuditor, auth AuthService) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	mountIngressRoutes(r, targets, cipher, discoverer, ifacesSrc, graph, ipamSrc, audit, auth)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

func TestCreateIngressTarget_EncryptsCredentialAndNeverReturnsIt(t *testing.T) {
	targets := newFakeIngressTargetStore()
	cipher := fakeSecretCipher{}
	audit := &fakeTokenAuditor{}
	auth := fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"}
	ts := newIngressTestRouter(t, targets, cipher, &fakeIngressDiscoverer{}, &fakeEdgeInterfacesSource{}, fakeEdgeGraph{}, nil, audit, auth)

	body, _ := json.Marshal(map[string]any{"kind": "haproxy", "address": "http://10.0.0.5:8404", "credential": "s3cret"})
	resp, err := http.Post(ts.URL+"/ingress/targets", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /ingress/targets: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	raw, _ := readBody(t, resp)
	if bytes.Contains(raw, []byte("s3cret")) {
		t.Errorf("response body leaked the plaintext credential: %s", raw)
	}

	var got ingressTargetResponse
	if unmarshalErr := json.Unmarshal(raw, &got); unmarshalErr != nil {
		t.Fatalf("decode: %v", unmarshalErr)
	}
	if !got.HasCredential {
		t.Errorf("HasCredential = false, want true")
	}
	if got.AddedBy != "alice" {
		t.Errorf("AddedBy = %q, want alice", got.AddedBy)
	}

	stored, err := targets.Get(context.Background(), got.ID)
	if err != nil {
		t.Fatalf("stored target missing: %v", err)
	}
	if bytes.Equal(stored.CredentialEnc, []byte("s3cret")) {
		t.Error("stored credential is plaintext, want encrypted")
	}
	decrypted, err := cipher.Decrypt(stored.CredentialEnc)
	if err != nil || string(decrypted) != "s3cret" {
		t.Errorf("stored credential does not round-trip through the cipher: %q, %v", decrypted, err)
	}

	found := false
	for _, e := range audit.entries {
		if e.Action == "ingress.target_add" {
			found = true
		}
	}
	if !found {
		t.Errorf("no ingress.target_add audit entry, got %+v", audit.entries)
	}
}

func TestCreateIngressTarget_ValidatesKindAndAddress(t *testing.T) {
	auth := fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"}
	ts := newIngressTestRouter(t, newFakeIngressTargetStore(), fakeSecretCipher{}, &fakeIngressDiscoverer{}, &fakeEdgeInterfacesSource{}, fakeEdgeGraph{}, nil, &fakeTokenAuditor{}, auth)

	tests := []map[string]any{
		{"kind": "envoy", "address": "http://10.0.0.5:8404"},
		{"kind": "haproxy", "address": "not-a-url"},
		{"kind": "haproxy", "address": "ftp://10.0.0.5"},
	}
	for _, body := range tests {
		b, _ := json.Marshal(body)
		resp, err := http.Post(ts.URL+"/ingress/targets", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatalf("POST /ingress/targets: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %v: status = %d, want 400", body, resp.StatusCode)
		}
	}
}

func TestListIngressTargets_NeverReturnsCredential(t *testing.T) {
	targets := newFakeIngressTargetStore()
	_ = targets.Insert(context.Background(), store.IngressTarget{
		ID: "t1", Kind: "caddy", Address: "http://10.0.0.7:2019", CredentialEnc: []byte("sealed-bytes"), AddedBy: "alice", AddedAt: 1,
	})
	auth := fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"}
	ts := newIngressTestRouter(t, targets, fakeSecretCipher{}, &fakeIngressDiscoverer{}, &fakeEdgeInterfacesSource{}, fakeEdgeGraph{}, nil, &fakeTokenAuditor{}, auth)

	resp, err := http.Get(ts.URL + "/ingress/targets")
	if err != nil {
		t.Fatalf("GET /ingress/targets: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := readBody(t, resp)
	if bytes.Contains(raw, []byte("sealed-bytes")) {
		t.Errorf("response leaked ciphertext: %s", raw)
	}
	var got ingressTargetsListResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 || !got.Items[0].HasCredential {
		t.Fatalf("got = %+v, want one item with hasCredential=true", got)
	}
}

func TestDeleteIngressTarget_Audited(t *testing.T) {
	targets := newFakeIngressTargetStore()
	_ = targets.Insert(context.Background(), store.IngressTarget{ID: "t1", Kind: "caddy", Address: "http://10.0.0.7:2019", AddedBy: "alice", AddedAt: 1})
	audit := &fakeTokenAuditor{}
	auth := fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"}
	ts := newIngressTestRouter(t, targets, fakeSecretCipher{}, &fakeIngressDiscoverer{}, &fakeEdgeInterfacesSource{}, fakeEdgeGraph{}, nil, audit, auth)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/ingress/targets/t1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if _, err := targets.Get(context.Background(), "t1"); err != store.ErrNotFound {
		t.Errorf("target still present after delete")
	}
	found := false
	for _, e := range audit.entries {
		if e.Action == "ingress.target_remove" {
			found = true
		}
	}
	if !found {
		t.Errorf("no ingress.target_remove audit entry, got %+v", audit.entries)
	}
}

func TestDeleteIngressTarget_NotFound(t *testing.T) {
	auth := fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"}
	ts := newIngressTestRouter(t, newFakeIngressTargetStore(), fakeSecretCipher{}, &fakeIngressDiscoverer{}, &fakeEdgeInterfacesSource{}, fakeEdgeGraph{}, nil, &fakeTokenAuditor{}, auth)
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/ingress/targets/nope", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// ingressStatusFixtureInterfaces builds an interfaces file with one
// port-forward whose IntIP (10.0.0.20) is the same address an
// ingress_targets row below is configured against — the exact "a
// port-forward and an ingress_targets entry line up" scenario T-1406 AC3
// requires.
func ingressStatusFixtureInterfaces(t *testing.T) string {
	t.Helper()
	start := "auto vmbr0\niface vmbr0 inet static\n\taddress 203.0.113.10/24\n\tgateway 203.0.113.1\n\tbridge-ports eno1\n"
	f, err := host.ParseInterfaces([]byte(start))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ref := func(kind inventory.Kind, id string) inventory.Ref {
		return inventory.Ref{Kind: kind, Node: "pve1", ID: id}
	}
	ops := []ifaces.Op{
		ifaces.NatPortForwardCreate{
			Target: ref(inventory.KindNatRule, "pf-proxy"), Iface: "vmbr0", Proto: "tcp",
			ExtPort: 443, IntIP: "10.0.0.20", IntPort: 8404,
		},
	}
	if err := ifaces.MutateAll(f, ops, "cs-fixture"); err != nil {
		t.Fatalf("MutateAll: %v", err)
	}
	return f.Render()
}

func buildIngressTestSnapshot() inventory.Snapshot {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", Status: "online"},
	})
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, []inventory.Entity{
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "200"}, VMID: 200, Node: "pve1", Name: "proxy1", Type: "qemu", Status: "running"},
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "201"}, VMID: 201, Node: "pve1", Name: "web1", Type: "qemu", Status: "running"},
	})
	return g.Snapshot()
}

// TestIngressStatus_CorrelatesBackendToKnownGuestRef is T-1406 AC2's golden
// test.
func TestIngressStatus_CorrelatesBackendToKnownGuestRef(t *testing.T) {
	content := ingressStatusFixtureInterfaces(t)
	ifacesSrc := &fakeEdgeInterfacesSource{byNode: map[string]string{"pve1": content}}
	graph := fakeEdgeGraph{snap: buildIngressTestSnapshot()}
	ipamSrc := fakeEdgeIPAMSource{allocs: map[string][]ipam.Allocation{
		"10.0.0.0/24": {
			{IP: "10.0.0.20", VMID: 200},
			{IP: "10.0.0.5", VMID: 201},
		},
	}}

	targets := newFakeIngressTargetStore()
	_ = targets.Insert(context.Background(), store.IngressTarget{ID: "ing1", Kind: "haproxy", Address: "http://10.0.0.20:8404", AddedBy: "alice", AddedAt: 1})

	discoverer := &fakeIngressDiscoverer{byID: map[string]ingress.ProxyState{
		"ing1": {TargetID: "ing1", Kind: ingress.KindHAProxy, Reachable: true, Backends: []ingress.Backend{
			{Address: "10.0.0.5:8080", Healthy: true},
		}},
	}}

	auth := fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"}
	ts := newIngressTestRouter(t, targets, fakeSecretCipher{}, discoverer, ifacesSrc, graph, ipamSrc, &fakeTokenAuditor{}, auth)

	resp, err := http.Get(ts.URL + "/ingress/status")
	if err != nil {
		t.Fatalf("GET /ingress/status: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got ingressStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Targets) != 1 || len(got.Targets[0].Backends) != 1 {
		t.Fatalf("Targets = %+v", got.Targets)
	}
	if got.Targets[0].Backends[0].GuestRef != "guest:pve1:201" {
		t.Errorf("backend GuestRef = %q, want guest:pve1:201", got.Targets[0].Backends[0].GuestRef)
	}
}

// TestIngressStatus_FullChainRendersWhenPortForwardAndTargetLineUp is
// T-1406 AC3's golden projection test at the HTTP layer.
func TestIngressStatus_FullChainRendersWhenPortForwardAndTargetLineUp(t *testing.T) {
	content := ingressStatusFixtureInterfaces(t)
	ifacesSrc := &fakeEdgeInterfacesSource{byNode: map[string]string{"pve1": content}}
	graph := fakeEdgeGraph{snap: buildIngressTestSnapshot()}
	ipamSrc := fakeEdgeIPAMSource{allocs: map[string][]ipam.Allocation{
		"10.0.0.0/24": {
			{IP: "10.0.0.20", VMID: 200},
			{IP: "10.0.0.5", VMID: 201},
		},
	}}

	targets := newFakeIngressTargetStore()
	_ = targets.Insert(context.Background(), store.IngressTarget{ID: "ing1", Kind: "haproxy", Address: "http://10.0.0.20:8404", AddedBy: "alice", AddedAt: 1})

	discoverer := &fakeIngressDiscoverer{byID: map[string]ingress.ProxyState{
		"ing1": {TargetID: "ing1", Kind: ingress.KindHAProxy, Reachable: true, Backends: []ingress.Backend{
			{Address: "10.0.0.5:8080", Healthy: true},
		}},
	}}

	auth := fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"}
	ts := newIngressTestRouter(t, targets, fakeSecretCipher{}, discoverer, ifacesSrc, graph, ipamSrc, &fakeTokenAuditor{}, auth)

	resp, err := http.Get(ts.URL + "/ingress/status")
	if err != nil {
		t.Fatalf("GET /ingress/status: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got ingressStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Chains) != 1 {
		t.Fatalf("Chains = %+v, want exactly one connected chain", got.Chains)
	}
	c := got.Chains[0]
	if c.PortForwardID != "pf-proxy" || c.ProxyGuestRef != "guest:pve1:200" || c.TargetID != "ing1" {
		t.Errorf("chain head = %+v, unexpected", c)
	}
	if len(c.Backends) != 1 || c.Backends[0].GuestRef != "guest:pve1:201" {
		t.Errorf("chain backends = %+v, want one backend resolved to guest:pve1:201", c.Backends)
	}
}

// TestIngressStatus_OnlyOperatorAddedTargetsAreContacted is T-1406 AC5's
// HTTP-layer regression test: the discoverer only ever sees the targets
// this store returns — nothing invents a target from anywhere else.
func TestIngressStatus_OnlyOperatorAddedTargetsAreContacted(t *testing.T) {
	targets := newFakeIngressTargetStore()
	_ = targets.Insert(context.Background(), store.IngressTarget{ID: "only-this-one", Kind: "caddy", Address: "http://10.0.0.7:2019", AddedBy: "alice", AddedAt: 1})
	discoverer := &fakeIngressDiscoverer{}
	auth := fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"}
	ts := newIngressTestRouter(t, targets, fakeSecretCipher{}, discoverer, &fakeEdgeInterfacesSource{}, fakeEdgeGraph{snap: inventory.NewGraph().Snapshot()}, nil, &fakeTokenAuditor{}, auth)

	resp, err := http.Get(ts.URL + "/ingress/status")
	if err != nil {
		t.Fatalf("GET /ingress/status: %v", err)
	}
	_ = resp.Body.Close()

	if len(discoverer.calls) != 1 || discoverer.calls[0].ID != "only-this-one" {
		t.Fatalf("discoverer.calls = %+v, want exactly one call for only-this-one", discoverer.calls)
	}
}

// TestIngressRoutes_MutationsRejectedWithoutNetWrite covers T-1406 AC4's
// route-level half: GET /ingress/status accepts no request body and issues
// no mutating call itself — asserted by confirming the route only ever
// mounts as GET (an unsupported method 404s/405s, never silently accepted).
func TestIngressStatus_NoOtherMethodSucceeds(t *testing.T) {
	auth := fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"}
	ts := newIngressTestRouter(t, newFakeIngressTargetStore(), fakeSecretCipher{}, &fakeIngressDiscoverer{}, &fakeEdgeInterfacesSource{}, fakeEdgeGraph{}, nil, &fakeTokenAuditor{}, auth)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req, _ := http.NewRequest(method, ts.URL+"/ingress/status", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s /ingress/status: %v", method, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s /ingress/status succeeded (200), want rejected", method)
		}
	}
}
