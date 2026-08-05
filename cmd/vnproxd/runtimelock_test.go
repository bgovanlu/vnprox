package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/backup"
	"github.com/bgovanlu/vnprox/internal/store"
)

// TestRunDaemon_HoldsTheStoreRuntimeLock is T-1901's daemon-level wiring
// check, and it is the one that actually matters for AC4: internal/store's
// runtime lock is only useful if the REAL daemon takes it, on the real
// startup path, for its whole lifetime.
//
// Rather than assert "cmd/vnproxd calls AcquireRuntimeLock" (which would
// only restate the source), this drives a real runDaemon against the
// checked-in dev config and asserts the property from the outside, in the
// order an operator would experience it:
//
//  1. before the daemon starts, no lock is held and a restore is permitted;
//  2. while it is serving, the lock IS held and a restore is refused;
//  3. after it shuts down, the lock is released and a restore is permitted
//     again — so a stopped daemon never permanently blocks recovery.
//
// Step 1 is what stops step 2 from passing vacuously (e.g. because the
// restore was refused for some unrelated reason), and step 3 is what stops
// the lock from being a foot-gun.
//
// The port is reserved with 127.0.0.1:0 and reused, so this test claims no
// fixed port — see planning/reports/T-1807.md and T-1807-bug-01 on why that
// matters in this repo.
func TestRunDaemon_HoldsTheStoreRuntimeLock(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving ephemeral port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	dir := t.TempDir()
	cfgPath := rewriteDevConfig(t, repoRoot, dir, port)
	dbPath := filepath.Join(dir, "vnprox.db")
	listen := fmt.Sprintf("127.0.0.1:%d", port)

	// --- 1. before the daemon runs: nothing holds the store -------------
	liveness := backup.DaemonLiveness(dbPath, listen)
	if liveErr := liveness(); liveErr != nil {
		t.Fatalf("precondition: the liveness check reports a daemon before one has started: %v", liveErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	daemonDone := make(chan error, 1)
	go func() { daemonDone <- runDaemon(ctx, cfgPath, testLogger()) }()

	// vnprox.service is Type=simple and this is the same shape: the process
	// exists long before the listener is bound (T-1807 found exactly this).
	// Poll for the listener, never for "it started".
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only client for the throwaway dev cert
		},
	}
	healthURL := fmt.Sprintf("https://%s/api/v1/health", listen)
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case daemonErr := <-daemonDone:
			t.Fatalf("daemon exited before serving: %v", daemonErr)
		default:
		}
		req, _ := http.NewRequest(http.MethodGet, healthURL, nil)
		resp, reqErr := client.Do(req)
		if reqErr == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon never served %s: %v", healthURL, reqErr)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// --- 2. while serving: the lock is held and a restore is refused ----
	held, err := store.RuntimeLockHeld(dbPath)
	if err != nil {
		t.Fatalf("RuntimeLockHeld: %v", err)
	}
	if !held {
		t.Error("the running daemon does not hold the store runtime lock — " +
			"`vnproxctl restore` would fall back to the listen-address probe alone")
	}
	if liveErr := liveness(); !errors.Is(liveErr, backup.ErrDaemonRunning) {
		t.Errorf("liveness check while the daemon is serving = %v, want ErrDaemonRunning", liveErr)
	}

	// A restore attempt through the real entry point is refused, and the
	// store file is untouched. (The archive path does not exist: the
	// refusal must come first, before anything is read, or an operator
	// could be told "no such file" when the real problem is "stop the
	// daemon".)
	if _, restoreErr := backup.Restore(context.Background(), backup.RestoreOptions{
		ArchivePath: filepath.Join(dir, "does-not-exist.tar.gz"),
		DBPath:      dbPath, Listen: listen,
	}); !errors.Is(restoreErr, backup.ErrDaemonRunning) {
		t.Errorf("Restore against the running daemon = %v, want ErrDaemonRunning", restoreErr)
	}

	// --- 3. after shutdown: released -------------------------------------
	cancel()
	select {
	case shutdownErr := <-daemonDone:
		if shutdownErr != nil {
			t.Fatalf("runDaemon returned error on shutdown: %v", shutdownErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runDaemon did not return within 10s of context cancellation")
	}

	held, err = store.RuntimeLockHeld(dbPath)
	if err != nil {
		t.Fatalf("RuntimeLockHeld after shutdown: %v", err)
	}
	if held {
		t.Error("the store runtime lock is still held after the daemon shut down — " +
			"a stopped daemon would permanently block `vnproxctl restore`")
	}
	if liveErr := liveness(); liveErr != nil {
		t.Errorf("liveness check after shutdown = %v, want nil (recovery must be possible again)", liveErr)
	}
}
