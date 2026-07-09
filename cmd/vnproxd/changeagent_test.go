package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func newTestAgent(t *testing.T, reload func(context.Context) error) *hostNodeAgent {
	t.Helper()
	dir := t.TempDir()
	ifPath := filepath.Join(dir, "interfaces")
	if err := os.WriteFile(ifPath, []byte("original\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return &hostNodeAgent{
		interfacesPath: ifPath,
		pendingPath:    filepath.Join(dir, "interfaces.new"),
		reload:         reload,
		log:            slog.Default(),
	}
}

func TestHostNodeAgent_StageReloadCommits(t *testing.T) {
	a := newTestAgent(t, func(context.Context) error { return nil })
	ctx := context.Background()

	if err := a.StageInterfaces(ctx, "pve1", "new content\n"); err != nil {
		t.Fatalf("stage: %v", err)
	}
	// Not committed until reload.
	if got, _ := a.ReadInterfaces(ctx, "pve1"); got != "original\n" {
		t.Fatalf("committed changed before reload: %q", got)
	}
	if err := a.ReloadInterfaces(ctx, "pve1"); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, _ := a.ReadInterfaces(ctx, "pve1"); got != "new content\n" {
		t.Fatalf("committed = %q, want new content", got)
	}
	// Staged file is cleaned up.
	if _, err := os.Stat(a.pendingPath); !os.IsNotExist(err) {
		t.Fatal("staged file not removed after reload")
	}
}

func TestHostNodeAgent_ReloadFailureRestores(t *testing.T) {
	reloadErr := errors.New("ifreload boom")
	calls := 0
	a := newTestAgent(t, func(context.Context) error {
		calls++
		// First call (the real apply) fails; the restore re-reload succeeds.
		if calls == 1 {
			return reloadErr
		}
		return nil
	})
	ctx := context.Background()

	if err := a.StageInterfaces(ctx, "pve1", "broken content\n"); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if err := a.ReloadInterfaces(ctx, "pve1"); err == nil {
		t.Fatal("expected reload to fail")
	}
	// Committed file must be byte-identical to the pre-reload original.
	if got, _ := a.ReadInterfaces(ctx, "pve1"); got != "original\n" {
		t.Fatalf("committed = %q, want restored original", got)
	}
}

func TestHostNodeAgent_Discard(t *testing.T) {
	a := newTestAgent(t, func(context.Context) error { return nil })
	ctx := context.Background()
	if err := a.StageInterfaces(ctx, "pve1", "x\n"); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if err := a.DiscardStaged(ctx, "pve1"); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if _, err := os.Stat(a.pendingPath); !os.IsNotExist(err) {
		t.Fatal("staged file still present after discard")
	}
	// Discarding again (nothing staged) is a no-op, not an error.
	if err := a.DiscardStaged(ctx, "pve1"); err != nil {
		t.Fatalf("discard (idempotent): %v", err)
	}
}

func TestUPIDNode(t *testing.T) {
	if got := upidNode("UPID:pve1:00001:00002:0003:sdnapply:sdn:root@pam:"); got != "pve1" {
		t.Fatalf("upidNode = %q, want pve1", got)
	}
	if got := upidNode("garbage"); got != "" {
		t.Fatalf("upidNode(garbage) = %q, want empty", got)
	}
}
