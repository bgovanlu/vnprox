// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/hub"
	"github.com/bgovanlu/vnprox/internal/hubreg"
)

// signedIndexServer serves one signed registry index, plus an artifact.
func signedIndexServer(t *testing.T, doc hubreg.Document, key ed25519.PrivateKey) *httptest.Server {
	t.Helper()
	signed, signErr := hubreg.Sign(doc, key)
	if signErr != nil {
		t.Fatalf("Sign: %v", signErr)
	}
	raw, signErr := json.Marshal(signed)
	if signErr != nil {
		t.Fatalf("marshal: %v", signErr)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(raw) })
	mux.HandleFunc("/artifacts/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"bundleVersion":1,"blueprint":{"id":"bp-a","name":"BP-A","blueprintVersion":1,"nodeSelector":{"mode":"all"},"params":null,"entities":null}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func hubWiringDoc() hubreg.Document {
	return hubreg.Document{
		SchemaVersion: hubreg.CurrentIndexSchema,
		Entries: []hub.Entry{
			{Type: hub.TypeBlueprint, ID: "bp-a", Name: "BP-A", Version: "1.0.0", ArtifactURL: "/artifacts/blueprint/bp-a/1.0.0.json"},
			{Type: hub.TypeBlueprint, ID: "bp-gone", Name: "Gone", Version: "1.0.0", ArtifactURL: "/artifacts/blueprint/bp-gone/1.0.0.json"},
		},
		Revocations: []hubreg.Revocation{
			{Type: hub.TypeBlueprint, ID: "bp-gone", Reason: "withdrawn by the publisher"},
		},
	}
}

func discardSlog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestNewHubClient_IndexSignersInstallTheGate is the daemon-side wiring
// assertion for T-2803: setting [hub] index_signers must actually put the
// registry gate on the client the daemon uses — verification and revocation
// enforced — and clearing it must leave the pre-T-2803 behaviour untouched.
func TestNewHubClient_IndexSignersInstallTheGate(t *testing.T) {
	// keyErr, not err: the file-writing blocks below use the idiomatic
	// `if err := ...` form, which govet's shadow check flags against an
	// outer err.
	pub, key, keyErr := ed25519.GenerateKey(nil)
	if keyErr != nil {
		t.Fatalf("GenerateKey: %v", keyErr)
	}
	fp := blueprint.Fingerprint(pub)
	srv := signedIndexServer(t, hubWiringDoc(), key)

	t.Run("configured: verified and revocation-enforcing", func(t *testing.T) {
		c := newHubClient(config.HubConfig{RegistryURL: srv.URL, IndexSigners: []string{fp}}, discardSlog())
		if c == nil {
			t.Fatal("hub client is nil with a registry URL configured")
		}
		idx, idxErr := c.Index(context.Background())
		if idxErr != nil {
			t.Fatalf("Index: %v", idxErr)
		}
		if len(idx.Entries) != 1 || idx.Entries[0].ID != "bp-a" {
			t.Fatalf("entries = %+v, want only the unrevoked bp-a", idx.Entries)
		}
		revoked := hub.Entry{Type: hub.TypeBlueprint, ID: "bp-gone", Version: "1.0.0", ArtifactURL: "/artifacts/blueprint/bp-gone/1.0.0.json"}
		if _, fetchErr := c.FetchBlueprintBundle(context.Background(), revoked); !errors.Is(fetchErr, hubreg.ErrRevoked) {
			t.Fatalf("fetch err = %v, want ErrRevoked", fetchErr)
		}
	})

	t.Run("configured with the wrong signer: no catalog at all", func(t *testing.T) {
		otherPub, _, keyErr := ed25519.GenerateKey(nil)
		if keyErr != nil {
			t.Fatalf("GenerateKey: %v", keyErr)
		}
		c := newHubClient(config.HubConfig{RegistryURL: srv.URL, IndexSigners: []string{blueprint.Fingerprint(otherPub)}}, discardSlog())
		if _, idxErr := c.Index(context.Background()); !errors.Is(idxErr, hubreg.ErrUntrustedIndexSigner) {
			t.Fatalf("err = %v, want ErrUntrustedIndexSigner", idxErr)
		}
	})

	t.Run("unconfigured: pre-T-2803 behaviour, no gate", func(t *testing.T) {
		c := newHubClient(config.HubConfig{RegistryURL: srv.URL}, discardSlog())
		idx, idxErr := c.Index(context.Background())
		if idxErr != nil {
			t.Fatalf("Index: %v", idxErr)
		}
		if len(idx.Entries) != 2 {
			t.Fatalf("entries = %d, want 2 (revocations are NOT enforced without index_signers)", len(idx.Entries))
		}
	})

	t.Run("off and malformed", func(t *testing.T) {
		if c := newHubClient(config.HubConfig{}, discardSlog()); c != nil {
			t.Fatal("the hub must be off with no registry URL")
		}
		if c := newHubClient(config.HubConfig{RegistryURL: "not-a-url"}, discardSlog()); c != nil {
			t.Fatal("a malformed registry URL must leave the hub off, not construct a client")
		}
	})
}

// TestNewHubClient_LocalMirror is T-4009's daemon-side wiring assertion: a
// "file://" registry_url with index_signers configured must read the
// mirrored files on disk (internal/hub.NewLocalDoer, via the Gate's inner
// doer) rather than trying to reach the network for every artifact fetch
// after a perfectly good local Index() — a daemon that verified an index
// offline and then dialed out for the artifact would defeat the entire
// point of an air-gapped mirror.
func TestNewHubClient_LocalMirror(t *testing.T) {
	// keyErr, not err: the file-writing blocks below use the idiomatic
	// `if err := ...; err != nil` form, which govet's shadow check flags
	// against an outer err in the same function.
	pub, key, keyErr := ed25519.GenerateKey(nil)
	if keyErr != nil {
		t.Fatalf("GenerateKey: %v", keyErr)
	}
	fp := blueprint.Fingerprint(pub)

	dir := t.TempDir()
	doc := hubWiringDoc()
	signed, signErr := hubreg.Sign(doc, key)
	if signErr != nil {
		t.Fatalf("Sign: %v", signErr)
	}
	raw, signErr := json.Marshal(signed)
	if signErr != nil {
		t.Fatalf("marshal: %v", signErr)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), raw, 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatalf("write index.json: %v", err)
	}
	artifactPath := filepath.Join(dir, "artifacts", "blueprint", "bp-a", "1.0.0.json")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bundleBody := []byte(`{"bundleVersion":1,"blueprint":{"id":"bp-a","name":"BP-A","blueprintVersion":1,"nodeSelector":{"mode":"all"},"params":null,"entities":null}}`)
	if err := os.WriteFile(artifactPath, bundleBody, 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatalf("write artifact: %v", err)
	}

	regURL, err := hub.LocalRegistryURL(dir)
	if err != nil {
		t.Fatalf("LocalRegistryURL: %v", err)
	}
	c := newHubClient(config.HubConfig{RegistryURL: regURL, IndexSigners: []string{fp}}, discardSlog())
	if c == nil {
		t.Fatal("hub client is nil for a valid file:// registry URL")
	}
	idx, idxErr := c.Index(context.Background())
	if idxErr != nil {
		t.Fatalf("Index: %v", idxErr)
	}
	if len(idx.Entries) != 1 || idx.Entries[0].ID != "bp-a" {
		t.Fatalf("entries = %+v, want only the unrevoked bp-a", idx.Entries)
	}
	bundle, fetchErr := c.FetchBlueprintBundle(context.Background(), idx.Entries[0])
	if fetchErr != nil {
		t.Fatalf("FetchBlueprintBundle: %v", fetchErr)
	}
	if bundle.Blueprint.ID != "bp-a" {
		t.Fatalf("bundle id = %q, want bp-a", bundle.Blueprint.ID)
	}
}
