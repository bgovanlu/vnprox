// SPDX-License-Identifier: Apache-2.0

package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/blueprint"
)

func fixtureIndex() Index {
	return Index{
		SchemaVersion: CurrentIndexSchema,
		Entries: []Entry{
			{Type: TypeBlueprint, ID: "bp-a", Name: "Blueprint A", Version: "1.0", ArtifactURL: "/artifacts/bp-a.json"},
			{Type: TypePlugin, ID: "pl-a", Name: "Plugin A", Version: "2.0", ArtifactURL: "/artifacts/pl-a.json", Capabilities: []string{"netRead"}, ExtensionPoints: []string{"dashboardTile"}, Transport: "grpc"},
		},
	}
}

// newFixtureRegistry stands up an httptest registry serving a fixed index and
// artifacts — a double for the registry service that lives in a separate repo.
func newFixtureRegistry(t *testing.T, idx Index, artifacts map[string]any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(idx)
	})
	for path, body := range artifacts {
		b := body
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(b)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_Index(t *testing.T) {
	srv := newFixtureRegistry(t, fixtureIndex(), nil)
	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	idx, err := c.Index(context.Background())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(idx.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(idx.Entries))
	}
}

func TestClient_Index_UnsupportedSchema(t *testing.T) {
	idx := fixtureIndex()
	idx.SchemaVersion = 999
	srv := newFixtureRegistry(t, idx, nil)
	c, _ := NewClient(srv.URL)
	if _, err := c.Index(context.Background()); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("err = %v, want ErrUnsupportedSchema", err)
	}
}

func TestClient_IndexCaching(t *testing.T) {
	var hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(fixtureIndex())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, _ := NewClient(srv.URL, WithCacheTTL(time.Minute))
	for range 3 {
		if _, err := c.Index(context.Background()); err != nil {
			t.Fatalf("Index: %v", err)
		}
	}
	if hits != 1 {
		t.Fatalf("registry hits = %d, want 1 (cached)", hits)
	}
}

func TestClient_FetchBlueprintBundle(t *testing.T) {
	bp := blueprint.Blueprint{BlueprintVersion: blueprint.CurrentBlueprintVersion, ID: "bp-a", Name: "Blueprint A"}
	bundle := blueprint.Bundle{BundleVersion: blueprint.CurrentBundleVersion, Blueprint: bp}
	srv := newFixtureRegistry(t, fixtureIndex(), map[string]any{"/artifacts/bp-a.json": bundle})
	c, _ := NewClient(srv.URL)

	idx, _ := c.Index(context.Background())
	got, err := c.FetchBlueprintBundle(context.Background(), idx.Entries[0])
	if err != nil {
		t.Fatalf("FetchBlueprintBundle: %v", err)
	}
	if got.Blueprint.Name != "Blueprint A" {
		t.Fatalf("blueprint name = %q, want %q", got.Blueprint.Name, "Blueprint A")
	}
}

// TestClient_ForeignArtifactRejected is the SSRF/off-origin guard: an artifact
// URL pointing at a different host than the registry is refused, so a fetched
// (unsigned-as-a-whole) index can never redirect an install off-origin.
func TestClient_ForeignArtifactRejected(t *testing.T) {
	srv := newFixtureRegistry(t, fixtureIndex(), nil)
	c, _ := NewClient(srv.URL)
	entry := Entry{Type: TypeBlueprint, ID: "evil", ArtifactURL: "https://evil.example.com/x.json"}
	if _, err := c.FetchBlueprintBundle(context.Background(), entry); !errors.Is(err, ErrForeignArtifact) {
		t.Fatalf("err = %v, want ErrForeignArtifact", err)
	}
}

func TestVettedSet(t *testing.T) {
	set := NewVettedSet([]string{"ABCDEF", " ", "123abc"})
	if !set.IsVetted("abcdef") {
		t.Error("expected abcdef vetted (case-insensitive)")
	}
	if !set.IsVetted("123ABC") {
		t.Error("expected 123ABC vetted")
	}
	if set.IsVetted("deadbeef") {
		t.Error("did not expect deadbeef vetted")
	}
	if set.IsVetted("") {
		t.Error("empty fingerprint (unsigned) must never be vetted")
	}
}

// TestCapabilityMismatch is T-2104 AC2's unit-level coverage for the
// pre-install "what was shown must equal what is granted" gate: an entry
// whose Capabilities/ExtensionPoints agree with the manifest (set-wise,
// order-independent) reports no mismatch (the positive leg); one that
// disagrees in either field does (the negative leg, both directions —
// narrower-shown and wider-shown are both a mismatch, since either means the
// catalog and the artifact disagree about what installing this grants).
func TestCapabilityMismatch(t *testing.T) {
	m := PluginManifest{ID: "p", Capabilities: []string{"netRead", "netWrite"}, ExtensionPoints: []string{"dashboardTile"}}

	t.Run("agrees exactly", func(t *testing.T) {
		e := Entry{Capabilities: []string{"netRead", "netWrite"}, ExtensionPoints: []string{"dashboardTile"}}
		if got := CapabilityMismatch(e, m); got != "" {
			t.Fatalf("CapabilityMismatch = %q, want no mismatch", got)
		}
	})

	t.Run("agrees out of order", func(t *testing.T) {
		e := Entry{Capabilities: []string{"netWrite", "netRead"}, ExtensionPoints: []string{"dashboardTile"}}
		if got := CapabilityMismatch(e, m); got != "" {
			t.Fatalf("CapabilityMismatch = %q, want no mismatch (order must not matter)", got)
		}
	})

	t.Run("catalog under-advertises capabilities", func(t *testing.T) {
		e := Entry{Capabilities: []string{"netRead"}, ExtensionPoints: []string{"dashboardTile"}}
		if got := CapabilityMismatch(e, m); got == "" {
			t.Fatal("expected a mismatch: the catalog showed less than the manifest grants")
		}
	})

	t.Run("catalog over-advertises capabilities", func(t *testing.T) {
		e := Entry{Capabilities: []string{"netRead", "netWrite", "fwWrite"}, ExtensionPoints: []string{"dashboardTile"}}
		if got := CapabilityMismatch(e, m); got == "" {
			t.Fatal("expected a mismatch: the catalog showed more than the manifest actually grants")
		}
	})

	t.Run("extensionPoints disagree independent of capabilities agreeing", func(t *testing.T) {
		e := Entry{Capabilities: []string{"netRead", "netWrite"}, ExtensionPoints: []string{"findingProducer"}}
		if got := CapabilityMismatch(e, m); got == "" {
			t.Fatal("expected a mismatch on extensionPoints")
		}
	})

	t.Run("both empty is not a mismatch", func(t *testing.T) {
		if got := CapabilityMismatch(Entry{}, PluginManifest{ID: "p"}); got != "" {
			t.Fatalf("CapabilityMismatch = %q, want no mismatch for two empty sets", got)
		}
	})
}

func TestCanonicalManifestBytes_Deterministic(t *testing.T) {
	m := PluginManifest{ID: "p", Name: "P", Version: "1", APIVersion: "v1", Transport: "grpc", ExtensionPoints: []string{"dashboardTile"}, Capabilities: []string{"netRead"}}
	a, err := CanonicalManifestBytes(m)
	if err != nil {
		t.Fatalf("CanonicalManifestBytes: %v", err)
	}
	b, _ := CanonicalManifestBytes(m)
	if string(a) != string(b) {
		t.Fatal("canonical bytes not deterministic")
	}
}
