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
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/hub"
	"github.com/bgovanlu/vnprox/internal/hubreg"
)

// signedIndexServer serves one signed registry index, plus an artifact.
func signedIndexServer(t *testing.T, doc hubreg.Document, key ed25519.PrivateKey) *httptest.Server {
	t.Helper()
	signed, err := hubreg.Sign(doc, key)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, err := json.Marshal(signed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
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
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
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
