// SPDX-License-Identifier: Apache-2.0

package change

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

func TestDetectProtected_ManagementBridge(t *testing.T) {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "pve1"}, []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, ID: "pve1"}, Name: "pve1", IP: "192.168.1.10"},
		&inventory.Bridge{
			Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"},
			Name: "vmbr0", Addresses: []string{"192.168.1.10/24"},
		},
		&inventory.Bridge{
			Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr1"},
			Name: "vmbr1", Addresses: []string{"10.0.0.1/24"},
		},
	})
	set := DetectProtected(g.Snapshot(), nil)
	refs := set["pve1"]
	if len(refs) != 1 || refs[0].ID != "vmbr0" {
		t.Fatalf("DetectProtected = %+v, want just vmbr0", refs)
	}
	// ToConfig round-trips the set into the on-disk string encoding.
	cfg := set.ToConfig()
	if len(cfg["pve1"]) != 1 {
		t.Fatalf("ToConfig = %+v", cfg)
	}
}

func TestLoadProtectedConfig_MalformedAndMissing(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "nope.json")
	cfg, err := LoadProtectedConfig(missing)
	if err != nil || len(cfg.Nodes) != 0 {
		t.Fatalf("missing file: cfg=%+v err=%v", cfg, err)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadProtectedConfig(bad); err == nil {
		t.Fatal("expected error loading malformed protected.json")
	}
}

func TestSaveLoadProtectedConfig_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "protected.json") // parent dir created by Save
	in := ProtectedConfig{Version: 1, Nodes: map[string][]string{
		"pve1": {"bridge:pve1:vmbr0"},
	}}
	if err := SaveProtectedConfig(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := LoadProtectedConfig(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(out.Nodes["pve1"]) != 1 {
		t.Fatalf("round-trip lost data: %+v", out)
	}
}

func TestProtectedConfig_ResolveBadRefs(t *testing.T) {
	cfg := ProtectedConfig{Nodes: map[string][]string{
		"pve1": {"bridge:pve1:vmbr0", "totally-bad"},
	}}
	set, bad := cfg.Resolve()
	if len(bad) != 1 || len(set["pve1"]) != 1 {
		t.Fatalf("Resolve set=%+v bad=%+v", set, bad)
	}
}

// safetyOptions degrades gracefully when protected.json has an unparsable ref
// (logs a warning, keeps the good refs).
func TestSafetyOptions_BadRefDegrades(t *testing.T) {
	db := openTestDBInternal(t)
	path := filepath.Join(t.TempDir(), "protected.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"nodes":{"pve1":["bridge:pve1:vmbr0","garbage"]}}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	svc, err := NewService(Config{
		Changesets: store.NewChangesetRepo(db), Audit: store.NewAuditRepo(db),
		ProtectedPath: path, Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	opts := svc.safetyOptions()
	if len(opts.Protected["pve1"]) != 1 {
		t.Fatalf("safetyOptions kept %d protected refs, want 1 (good one)", len(opts.Protected["pve1"]))
	}
}

func TestErrInvalidProtectedRef_Error(t *testing.T) {
	e := &ErrInvalidProtectedRef{Refs: []string{"a", "b"}}
	if e.Error() == "" {
		t.Fatal("empty error string")
	}
}

func openTestDBInternal(t *testing.T) *store.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
