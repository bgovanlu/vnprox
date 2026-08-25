package hubreg

// sigstoregate_test.go exercises SigstoreGate over real HTTP (httptest),
// the same way gate_test.go exercises Gate: this is the seam
// cmd/vnproxd/hubinstall.go actually wires into hub.Client, so its
// contract — verify once at index fetch, gate every artifact fetch off the
// verified document, refuse offline (no second network access) when
// revoked — is what an operator's daemon actually runs.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/testing/ca"

	"github.com/bgovanlu/vnprox/internal/hub"
)

// sigstoreTestServer serves a Sigstore-signed index.json plus its sibling
// bundle, and one artifact, built from real ca.VirtualSigstore material
// exactly as sigstore_test.go does.
func sigstoreTestServer(t *testing.T, vs *ca.VirtualSigstore, doc Document) *httptest.Server {
	t.Helper()
	indexRaw := marshalDoc(t, doc)
	te, err := vs.Sign(testIdentity, testIssuer, indexRaw)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	bundleRaw := bundleJSON(t, vs, te)

	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(indexRaw) })
	mux.HandleFunc("/"+SigstoreBundleName, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(bundleRaw) })
	mux.HandleFunc("/artifacts/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"bundleVersion":1,"blueprint":{"id":"seed-a","name":"Seed A","blueprintVersion":1,"nodeSelector":{"mode":"all"},"params":null,"entities":null}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSigstoreGate_GoodIndexAndArtifact(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	doc := testDocument()
	srv := sigstoreTestServer(t, vs, doc)

	sv := newTestVerifier(t, vs, SigstoreIdentity{Issuer: testIssuer, SAN: testIdentity})
	gate := NewSigstoreGate(nil, sv)
	client, err := hub.NewClient(srv.URL, hub.WithHTTPClient(gate), hub.WithCacheTTL(0))
	if err != nil {
		t.Fatalf("hub.NewClient: %v", err)
	}

	idx, err := client.Index(context.Background())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(idx.Entries) != 1 || idx.Entries[0].ID != "seed-a" {
		t.Fatalf("entries = %+v", idx.Entries)
	}
	if _, err := client.FetchBlueprintBundle(context.Background(), idx.Entries[0]); err != nil {
		t.Fatalf("FetchBlueprintBundle: %v", err)
	}
}

func TestSigstoreGate_RevokedArtifactRefusedOffline(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	doc := testDocument(Revocation{Type: hub.TypeBlueprint, ID: "seed-a", Reason: "withdrawn"})
	srv := sigstoreTestServer(t, vs, doc)

	sv := newTestVerifier(t, vs, SigstoreIdentity{Issuer: testIssuer, SAN: testIdentity})
	gate := NewSigstoreGate(nil, sv)
	client, err := hub.NewClient(srv.URL, hub.WithHTTPClient(gate), hub.WithCacheTTL(0))
	if err != nil {
		t.Fatalf("hub.NewClient: %v", err)
	}

	idx, err := client.Index(context.Background())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(idx.Entries) != 0 {
		t.Fatalf("entries = %+v, want the revoked entry withdrawn", idx.Entries)
	}

	// Fetching it anyway (a caller that built its own hub.Entry, or a stale
	// UI reference) is refused entirely offline — no request ever reaches
	// the server's artifact handler, matching AC3's "no second network
	// access" contract Gate (Ed25519) already asserts.
	entry := hub.Entry{Type: hub.TypeBlueprint, ID: "seed-a", Version: "1.0.0", ArtifactURL: "/artifacts/blueprint/seed-a/1.0.0.json"}
	if _, err := client.FetchBlueprintBundle(context.Background(), entry); !errors.Is(err, ErrRevoked) {
		t.Fatalf("FetchBlueprintBundle err = %v, want ErrRevoked", err)
	}
}

func TestSigstoreGate_WrongIdentityYieldsNoCatalog(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	srv := sigstoreTestServer(t, vs, testDocument())

	sv := newTestVerifier(t, vs, SigstoreIdentity{Issuer: testIssuer, SAN: "https://github.com/someone-else/evil/.github/workflows/x.yml@refs/heads/main"})
	gate := NewSigstoreGate(nil, sv)
	client, err := hub.NewClient(srv.URL, hub.WithHTTPClient(gate), hub.WithCacheTTL(0))
	if err != nil {
		t.Fatalf("hub.NewClient: %v", err)
	}
	if _, err := client.Index(context.Background()); err == nil {
		t.Fatal("Index succeeded against a bundle signed for a different identity")
	}
}
