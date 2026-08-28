// SPDX-License-Identifier: Apache-2.0

package peer

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLoadOrGenerateSecret_GeneratesWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vnprox", "cluster.secret")

	store, err := LoadOrGenerateSecret(path, discardLogger())
	if err != nil {
		t.Fatalf("LoadOrGenerateSecret: %v", err)
	}

	secret := store.Current()
	if len(secret) != secretLen {
		t.Fatalf("generated secret length = %d, want %d", len(secret), secretLen)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat generated secret file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("secret file mode = %o, want 0600", perm)
	}
}

func TestLoadOrGenerateSecret_LoadsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cluster.secret")
	want := hex.EncodeToString(testSecret)
	if err := os.WriteFile(path, []byte(want+"\n"), 0o600); err != nil {
		t.Fatalf("seeding secret file: %v", err)
	}

	store, err := LoadOrGenerateSecret(path, discardLogger())
	if err != nil {
		t.Fatalf("LoadOrGenerateSecret: %v", err)
	}
	if hex.EncodeToString(store.Current()) != want {
		t.Errorf("loaded secret = %x, want %s", store.Current(), want)
	}
}

func TestLoadOrGenerateSecret_RejectsMalformedContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cluster.secret")
	if err := os.WriteFile(path, []byte("not hex at all"), 0o600); err != nil {
		t.Fatalf("seeding secret file: %v", err)
	}

	if _, err := LoadOrGenerateSecret(path, discardLogger()); err == nil {
		t.Fatal("expected an error loading a non-hex secret file")
	}
}

func TestLoadOrGenerateSecret_ConcurrentGenerationConverges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vnprox", "cluster.secret")

	const n = 8
	stores := make([]*SecretStore, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stores[i], errs[i] = LoadOrGenerateSecret(path, discardLogger())
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: LoadOrGenerateSecret: %v", i, err)
		}
	}
	want := hex.EncodeToString(stores[0].Current())
	for i, s := range stores {
		if got := hex.EncodeToString(s.Current()); got != want {
			t.Errorf("goroutine %d loaded a different secret (%s) than goroutine 0 (%s) — concurrent generation did not converge", i, got, want)
		}
	}
}

func TestSecretStore_WatchPicksUpRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cluster.secret")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(testSecret)), 0o600); err != nil {
		t.Fatalf("seeding secret file: %v", err)
	}

	store, err := LoadOrGenerateSecret(path, discardLogger())
	if err != nil {
		t.Fatalf("LoadOrGenerateSecret: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- store.Watch(ctx, 5*time.Millisecond) }()

	rotated := bytes.Repeat([]byte{0x7a}, secretLen)
	// Ensure the mtime actually changes even on filesystems with coarse
	// mtime granularity.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte(hex.EncodeToString(rotated)), 0o600); err != nil {
		t.Fatalf("rotating secret file: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hex.EncodeToString(store.Current()) == hex.EncodeToString(rotated) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if hex.EncodeToString(store.Current()) != hex.EncodeToString(rotated) {
		t.Fatalf("Watch did not pick up the rotated secret in time; current = %x", store.Current())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Watch returned an error after cancellation: %v", err)
	}
}
