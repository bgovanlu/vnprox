package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/store"
)

// TestMCPPathConstantsAgree pins config.DefaultMCPPath equal to
// api.DefaultMCPPath, so the config docs and the router mount can never drift
// (the two packages don't import each other).
func TestMCPPathConstantsAgree(t *testing.T) {
	if config.DefaultMCPPath != api.DefaultMCPPath {
		t.Fatalf("MCP path constants disagree: config=%q api=%q", config.DefaultMCPPath, api.DefaultMCPPath)
	}
}

// TestSetupMCPBuildsServer is a wiring smoke test: setupMCP constructs a live
// MCP server from the daemon's real change engine + token/audit repos, and its
// HTTP handler is non-nil. Read seams are left nil here (they degrade to
// "not available" per-tool) — the point is that the security-critical staging +
// auth path wires cleanly.
func TestSetupMCPBuildsServer(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	changeSvc, err := change.NewService(change.Config{
		Changesets: store.NewChangesetRepo(db),
		Audit:      store.NewAuditRepo(db),
		Now:        func() time.Time { return time.Unix(1, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}

	srv, err := setupMCP(api.Options{}, changeSvc, store.NewAPITokenRepo(db), store.NewAuditRepo(db),
		nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("setupMCP: %v", err)
	}
	if srv.HTTPHandler() == nil {
		t.Fatalf("setupMCP returned a server with a nil HTTP handler")
	}
}
