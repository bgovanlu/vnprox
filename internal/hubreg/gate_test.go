package hubreg

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/hub"
)

// countingDoer wraps an http.Client and counts every request that actually
// leaves for the network. It is the instrument AC3's "no network access
// beyond the already-fetched signed index" is measured with: the revoked-fetch
// leg must leave this counter untouched, and the control leg must move it
// (otherwise the counter proves nothing).
//
//nolint:govet // fieldalignment: a test double; the counter sits with what it counts.
type countingDoer struct {
	mu     sync.Mutex
	inner  http.Client
	calls  int
	paths  []string
	refuse bool // when set, any call fails the test's expectation loudly
}

func (c *countingDoer) Do(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.calls++
	c.paths = append(c.paths, req.URL.Path)
	refuse := c.refuse
	c.mu.Unlock()
	if refuse {
		return nil, errors.New("countingDoer: network was reached when it must not have been")
	}
	return c.inner.Do(req)
}

func (c *countingDoer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// registryFixture serves a signed index plus a static artifact tree — the
// exact static-hosting shape the published registry has (index.json next to
// /artifacts/**), with no service behind it.
//
//nolint:govet // fieldalignment: a test fixture; field order tells the fixture's story, not packing.
type registryFixture struct {
	srv       *httptest.Server
	doer      *countingDoer
	indexKey  ed25519.PrivateKey
	indexFP   string
	artifacts map[string][]byte
	doc       Document
	// tamper, when set, corrupts the served index bytes after signing —
	// standing in for anything between the static hosting and the client.
	tamper tamperFunc
}

// tamperFunc corrupts served index bytes.
type tamperFunc func([]byte) []byte

func newRegistryFixture(t *testing.T, doc Document) *registryFixture {
	t.Helper()
	key, fp := testKey(t)
	f := &registryFixture{indexKey: key, indexFP: fp, artifacts: map[string][]byte{}, doc: doc}

	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) {
		signed, err := Sign(f.doc, f.indexKey)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		raw, err := json.Marshal(signed)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if f.tamper != nil {
			raw = f.tamper(raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("/artifacts/", func(w http.ResponseWriter, r *http.Request) {
		body, ok := f.artifacts[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	f.doer = &countingDoer{}
	return f
}

// client builds the *real, unmodified* hub.Client against this registry, with
// the gate installed on the client's existing WithHTTPClient seam.
func (f *registryFixture) client(t *testing.T, trusted []string) (*hub.Client, *Gate) {
	t.Helper()
	gate := NewGate(f.doer, trusted)
	c, err := hub.NewClient(f.srv.URL, hub.WithHTTPClient(gate), hub.WithCacheTTL(0))
	if err != nil {
		t.Fatalf("hub.NewClient: %v", err)
	}
	return c, gate
}

// TestGate_ClientConsumesRealIndexUnmodified is AC1: the index this repository
// generates is consumed by the existing internal/hub client as-is — same
// package, same types, no shim. The additive signed fields are invisible to
// it.
func TestGate_ClientConsumesRealIndexUnmodified(t *testing.T) {
	doc := testDoc()
	doc.Entries[0].Publisher = "acme"
	f := newRegistryFixture(t, doc)
	c, _ := f.client(t, []string{f.indexFP})

	idx, err := c.Index(context.Background())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if idx.SchemaVersion != hub.CurrentIndexSchema {
		t.Fatalf("schemaVersion = %d, want %d", idx.SchemaVersion, hub.CurrentIndexSchema)
	}
	if len(idx.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(idx.Entries))
	}
	if idx.Entries[0].ID != "bp-a" || idx.Entries[0].Publisher != "acme" || idx.Entries[0].ArtifactURL == "" {
		t.Fatalf("entry = %+v", idx.Entries[0])
	}

	// The same bytes also decode into hub.Index directly — i.e. a client with
	// no gate at all still parses the published file (the additive fields are
	// ignored, not fatal).
	signed, err := Sign(doc, f.indexKey)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, err := json.Marshal(signed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var plain hub.Index
	if err := json.Unmarshal(raw, &plain); err != nil {
		t.Fatalf("the published index must decode into hub.Index unmodified: %v", err)
	}
	if plain.SchemaVersion != hub.CurrentIndexSchema || len(plain.Entries) != 2 {
		t.Fatalf("hub.Index = %+v", plain)
	}
}

// TestGate_RevokedArtifactRefusedOffline is AC3, both halves.
func TestGate_RevokedArtifactRefusedOffline(t *testing.T) {
	doc := testDoc()
	doc.Revocations = []Revocation{{Type: hub.TypePlugin, ID: "pl-a", Version: "2.1.0", Reason: "capability scope escalated in a silent update", At: 42}}
	f := newRegistryFixture(t, doc)
	f.artifacts["/artifacts/blueprint/bp-a/1.0.0.json"] = mustJSON(t, blueprint.Bundle{BundleVersion: blueprint.CurrentBundleVersion, Blueprint: blueprint.Blueprint{BlueprintVersion: blueprint.CurrentBlueprintVersion, ID: "bp-a", Name: "BP-A"}})
	f.artifacts["/artifacts/plugin/pl-a/2.1.0.json"] = mustJSON(t, hub.PluginArtifact{Manifest: hub.PluginManifest{ID: "pl-a", Version: "2.1.0"}})

	c, _ := f.client(t, []string{f.indexFP})

	// One index fetch. From here on, the revoked verdict must need no network.
	idx, err := c.Index(context.Background())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(idx.Entries) != 1 || idx.Entries[0].ID != "bp-a" {
		t.Fatalf("the revoked entry must not be offered: %+v", idx.Entries)
	}
	afterIndex := f.doer.count()
	if afterIndex != 1 {
		t.Fatalf("index fetches = %d, want 1", afterIndex)
	}

	// A caller that still holds the revoked entry (a stale cache, a
	// hand-built entry) is refused at the fetch.
	revoked := hub.Entry{Type: hub.TypePlugin, ID: "pl-a", Version: "2.1.0", ArtifactURL: "/artifacts/plugin/pl-a/2.1.0.json"}
	f.doer.mu.Lock()
	f.doer.refuse = true // any outbound call from here is a failure
	f.doer.mu.Unlock()

	if _, err := c.FetchPluginArtifact(context.Background(), revoked); !errors.Is(err, ErrRevoked) {
		t.Fatalf("err = %v, want ErrRevoked", err)
	}
	if got := f.doer.count(); got != afterIndex {
		t.Fatalf("the revocation verdict made %d network call(s); it must be decided from the already-fetched index alone", got-afterIndex)
	}

	// Control leg: the same transport DOES register a call when something
	// legitimately reaches for the network — so the assertion above measures
	// the absence of a call, not a broken counter.
	f.doer.mu.Lock()
	f.doer.refuse = false
	f.doer.mu.Unlock()
	live := hub.Entry{Type: hub.TypeBlueprint, ID: "bp-a", Version: "1.0.0", ArtifactURL: "/artifacts/blueprint/bp-a/1.0.0.json"}
	if _, err := c.FetchBlueprintBundle(context.Background(), live); err != nil {
		t.Fatalf("FetchBlueprintBundle: %v", err)
	}
	if got := f.doer.count(); got != afterIndex+1 {
		t.Fatalf("calls = %d, want %d — the counter must move for a permitted fetch", got, afterIndex+1)
	}
}

// TestGate_RevokedBySigner covers the key-compromise case: one revocation
// withdraws every artifact that key signed, without enumerating them.
func TestGate_RevokedBySigner(t *testing.T) {
	pubKey, pubFP := testKey(t)
	pub, ok := pubKey.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("signing key has no Ed25519 public half")
	}
	sig := &blueprint.BundleSignature{Alg: blueprint.SignatureAlgEd25519, PublicKeyFingerprint: pubFP, PublicKey: b64(pub)}

	doc := testDoc()
	doc.Entries[0].Signature = sig
	doc.Entries[1].Signature = sig
	doc.Revocations = []Revocation{{SignerFingerprint: pubFP, Reason: "publisher signing key compromised", At: 7}}

	f := newRegistryFixture(t, doc)
	c, _ := f.client(t, []string{f.indexFP})

	idx, err := c.Index(context.Background())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(idx.Entries) != 0 {
		t.Fatalf("entries = %+v, want none (every artifact by the revoked key is withdrawn)", idx.Entries)
	}
	for _, e := range doc.Entries {
		if _, err := c.FetchBlueprintBundle(context.Background(), e); !errors.Is(err, ErrRevoked) {
			t.Fatalf("fetch %s: err = %v, want ErrRevoked", e.ID, err)
		}
	}
}

// TestGate_CorruptedIndexNeverPartiallyLoads is AC5 at the client boundary:
// the client surfaces an error and zero entries, not a subset.
func TestGate_CorruptedIndexNeverPartiallyLoads(t *testing.T) {
	//nolint:govet // fieldalignment: test table; field order documents each case, not packing.
	tests := []struct {
		name    string
		tamper  tamperFunc
		wantErr error
	}{
		{
			name: "an entry edited after signing",
			tamper: func(raw []byte) []byte {
				return []byte(replaceOnce(string(raw), `"id":"bp-a"`, `"id":"bp-evil"`))
			},
			wantErr: ErrInvalidIndexSignature,
		},
		{
			name: "an entry appended after signing",
			tamper: func(raw []byte) []byte {
				return []byte(replaceOnce(string(raw), `"entries":[`, `"entries":[{"type":"plugin","id":"pl-evil","name":"E","version":"1","artifactUrl":"/artifacts/plugin/pl-evil/1.json"},`))
			},
			wantErr: ErrInvalidIndexSignature,
		},
		{
			name:    "truncated in transit",
			tamper:  func(raw []byte) []byte { return raw[:len(raw)/3] },
			wantErr: ErrInvalidIndex,
		},
		{
			name:    "signature stripped",
			tamper:  func(raw []byte) []byte { return []byte(replaceOnce(string(raw), `"signature"`, `"signatureX"`)) },
			wantErr: ErrInvalidIndex,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newRegistryFixture(t, testDoc())
			f.tamper = tc.tamper
			c, gate := f.client(t, []string{f.indexFP})

			idx, err := c.Index(context.Background())
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if len(idx.Entries) != 0 {
				t.Fatalf("a failed verification handed the client %d entries", len(idx.Entries))
			}
			if _, ok := gate.Document(); ok {
				t.Fatal("a failed verification cached a document")
			}
			// And with no verified index, nothing is fetchable at all.
			entry := hub.Entry{Type: hub.TypeBlueprint, ID: "bp-a", ArtifactURL: "/artifacts/blueprint/bp-a/1.0.0.json"}
			if _, ferr := c.FetchBlueprintBundle(context.Background(), entry); !errors.Is(ferr, ErrNoVerifiedIndex) {
				t.Fatalf("fetch err = %v, want ErrNoVerifiedIndex", ferr)
			}
		})
	}
}

// TestGate_UntrustedIndexSigner: a well-formed index signed by a key the
// operator never pinned yields nothing, through the client.
func TestGate_UntrustedIndexSigner(t *testing.T) {
	f := newRegistryFixture(t, testDoc())
	_, otherFP := testKey(t)
	c, _ := f.client(t, []string{otherFP})

	if _, err := c.Index(context.Background()); !errors.Is(err, ErrUntrustedIndexSigner) {
		t.Fatalf("err = %v, want ErrUntrustedIndexSigner", err)
	}
	// Empty trust set: nothing is accepted.
	c2, _ := f.client(t, nil)
	if _, err := c2.Index(context.Background()); !errors.Is(err, ErrUntrustedIndexSigner) {
		t.Fatalf("err = %v, want ErrUntrustedIndexSigner", err)
	}
}

// TestGate_UnlistedArtifactRefused: the gate is an allowlist — a URL the
// signed catalog does not name is never fetched, even on the registry's own
// host.
func TestGate_UnlistedArtifactRefused(t *testing.T) {
	f := newRegistryFixture(t, testDoc())
	f.artifacts["/artifacts/plugin/pl-sneaky/1.0.0.json"] = []byte(`{"manifest":{"id":"pl-sneaky"}}`)
	c, _ := f.client(t, []string{f.indexFP})
	if _, err := c.Index(context.Background()); err != nil {
		t.Fatalf("Index: %v", err)
	}
	before := f.doer.count()
	sneaky := hub.Entry{Type: hub.TypePlugin, ID: "pl-sneaky", Version: "1.0.0", ArtifactURL: "/artifacts/plugin/pl-sneaky/1.0.0.json"}
	if _, err := c.FetchPluginArtifact(context.Background(), sneaky); !errors.Is(err, ErrUnlistedArtifact) {
		t.Fatalf("err = %v, want ErrUnlistedArtifact", err)
	}
	if got := f.doer.count(); got != before {
		t.Fatalf("an unlisted artifact was fetched from the network (%d calls)", got-before)
	}
}

// TestGate_RegistryStatusPassesThrough: a 404/500 from the hosting is the
// client's own error, not swallowed into a verification failure.
func TestGate_RegistryStatusPassesThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	gate := NewGate(nil, []string{"deadbeef"})
	c, err := hub.NewClient(srv.URL, hub.WithHTTPClient(gate))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.Index(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrInvalidIndex) || errors.Is(err, ErrUnsignedIndex) {
		t.Fatalf("a 404 must surface as the registry's status, got %v", err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func replaceOnce(s, old, replacement string) string {
	return strings.Replace(s, old, replacement, 1)
}
