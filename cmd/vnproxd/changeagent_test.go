package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/config"
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

// TestNewDevNodeAgent covers the [safety] dev_interfaces_dir sandbox
// (audit-phase-2 F-22): the agent operates only under the sandbox dir,
// seeds a fixture on first use, keeps an existing file on reuse, and its
// reload never execs a real ifreload.
func TestNewDevNodeAgent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dev-host")
	a, err := newDevNodeAgent(dir, testLogger())
	if err != nil {
		t.Fatalf("newDevNodeAgent: %v", err)
	}

	if a.interfacesPath != filepath.Join(dir, "interfaces") || a.pendingPath != filepath.Join(dir, "interfaces.new") {
		t.Fatalf("paths = %q/%q, want them under the sandbox dir %q", a.interfacesPath, a.pendingPath, dir)
	}

	ctx := context.Background()
	got, err := a.ReadInterfaces(ctx, "pve1")
	if err != nil {
		t.Fatalf("ReadInterfaces: %v", err)
	}
	if !strings.Contains(got, "vmbr0") {
		t.Errorf("seeded sandbox file does not contain the fixture bridge: %q", got)
	}

	// Full stage -> reload cycle must succeed with no real ifreload binary
	// involved and commit the staged content to the sandboxed file.
	want := got + "\n# staged by test\n"
	if stageErr := a.StageInterfaces(ctx, "pve1", want); stageErr != nil {
		t.Fatalf("StageInterfaces: %v", stageErr)
	}
	if reloadErr := a.ReloadInterfaces(ctx, "pve1"); reloadErr != nil {
		t.Fatalf("ReloadInterfaces (no-op reload) returned error: %v", reloadErr)
	}
	after, err := a.ReadInterfaces(ctx, "pve1")
	if err != nil {
		t.Fatalf("ReadInterfaces after reload: %v", err)
	}
	if after != want {
		t.Errorf("committed content = %q, want the staged content", after)
	}

	// Re-opening the sandbox must keep the existing (edited) file, not
	// re-seed over it.
	b, err := newDevNodeAgent(dir, testLogger())
	if err != nil {
		t.Fatalf("newDevNodeAgent (reuse): %v", err)
	}
	again, err := b.ReadInterfaces(ctx, "pve1")
	if err != nil {
		t.Fatalf("ReadInterfaces (reuse): %v", err)
	}
	if again != want {
		t.Errorf("re-seeded over an existing sandbox file: got %q", again)
	}
}

// TestDevConfig_SandboxesHostWriter pins the F-22 remediation at the config
// level: the checked-in dev config must never wire the production host
// agent against the real /etc/network/interfaces.
func TestDevConfig_SandboxesHostWriter(t *testing.T) {
	// config.Load validates relative TLS paths against the cwd, and
	// dev.toml's paths are repo-root-relative (matching `make dev`).
	t.Chdir(filepath.Join("..", ".."))
	cfg, err := config.Load(filepath.Join("testdata", "dev.toml"), testLogger())
	if err != nil {
		t.Fatalf("loading testdata/dev.toml: %v", err)
	}
	if cfg.Safety.DevInterfacesDir == "" {
		t.Fatal("testdata/dev.toml must set [safety] dev_interfaces_dir — a dev daemon must not operate on the real /etc/network/interfaces (audit-phase-2 F-22)")
	}
}
