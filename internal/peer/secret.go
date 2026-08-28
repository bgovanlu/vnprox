// SPDX-License-Identifier: Apache-2.0

package peer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultSecretPath is where the cluster secret lives in production
// (docs/deployment.md: "Generates the cluster secret in /etc/pve/priv/vnprox/
// (first node only; pmxcfs replicates it)."; docs/architecture.md §5). It
// sits under pmxcfs's priv/ subtree specifically, not just anywhere under
// /etc/pve: hardware validation against a real PVE 9.2.4 node found pmxcfs
// silently coerces every file's creation-time mode to 0640 root:www-data
// (and rejects chmod() outright) everywhere except /etc/pve/priv/, which it
// alone auto-restricts to 0600 root-only — the same place PVE itself keeps
// shadow.cfg and authkey.key. Putting the secret anywhere else under
// /etc/pve would make it group-readable (by www-data, i.e. pveproxy)
// despite the code and docs assuming/requiring 0600.
const DefaultSecretPath = "/etc/pve/priv/vnprox/cluster.secret"

// secretLen is the cluster secret's length in raw bytes (docs/security.md:
// "cluster secret" is generated fresh; docs/architecture.md §5 doesn't pin
// a length, so this package uses a 256-bit secret — the same strength
// internal/auth uses for session ids).
const secretLen = 32

// SecretStore holds the currently loaded cluster secret and can reload it
// from disk, generating a fresh one on first use if absent. It is safe for
// concurrent use; Current() is called on every signed/verified request.
type SecretStore struct {
	modTime time.Time
	log     *slog.Logger
	path    string
	secret  []byte
	mu      sync.RWMutex
}

// LoadOrGenerateSecret loads the cluster secret at path, generating and
// persisting a fresh random one (0600, root-owned by virtue of the daemon
// running as root) if the file does not yet exist. Concurrent
// generation — two nodes (or, in this package's own tests, goroutines)
// racing to create the file for the first time via the same pmxcfs-backed
// path — is handled by generateSecretFile's write-then-publish scheme: the
// loser's reload always sees either no file or the winner's *complete*
// content, never a partially-written one, so every caller converges on the
// exact same secret regardless of who "wins".
// loadOrGenerateMu serializes LoadOrGenerateSecret across the whole process
// so concurrent in-process callers converge on one secret. Generation is a
// check-then-generate-then-publish sequence (os.Stat + os.Rename) with an
// inherent TOCTOU window: without this, two goroutines could each see no
// file, generate *different* secrets, and race their renames — last writer
// wins, and readers diverge. In production this is a once-per-process
// startup call so there is no contention; the lock just makes the
// concurrent case (and its test) deterministic without changing the
// filesystem semantics pmxcfs relies on (os.Rename, not a hardlink whose
// pmxcfs support is unverified). Cross-*node* convergence is unchanged — it
// still rides pmxcfs replication plus generateSecretFile's own "already
// published, nothing to do" check.
var loadOrGenerateMu sync.Mutex

func LoadOrGenerateSecret(path string, logger *slog.Logger) (*SecretStore, error) {
	loadOrGenerateMu.Lock()
	defer loadOrGenerateMu.Unlock()

	if logger == nil {
		logger = slog.Default()
	}
	s := &SecretStore{path: path, log: logger}

	if err := s.reload(); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if genErr := generateSecretFile(path); genErr != nil {
			return nil, genErr
		}
		if err := s.reload(); err != nil {
			return nil, fmt.Errorf("peer: loading cluster secret %s after generating it: %w", path, err)
		}
		logger.Info("peer: generated new cluster secret", "path", path)
	}
	return s, nil
}

// generateSecretFile writes secretLen random bytes, hex-encoded, to path
// with 0600 permissions, creating the parent directory if needed. It is a
// no-op (not an error) if another process/node already published a secret
// at path — see LoadOrGenerateSecret's doc comment.
//
// The content is written in full to a temporary file first and only then
// published at path via os.Rename, which is atomic on a given filesystem
// (no reader ever observes a partially-written file). This used to publish
// via os.Link (fails atomically with ErrExist if path already has an
// entry, so a concurrent loser could never clobber a winner's file) —
// hardware validation against a real PVE 9.2.4 node found pmxcfs (the
// /etc/pve FUSE filesystem DefaultSecretPath lives under) rejects link(2)
// outright with EPERM, which would make every secret-generation attempt
// fail on real hardware, not just race unsafely. Rename instead, guarded
// by a best-effort existence check immediately before it: this avoids
// clobbering another racer's already-published secret in the overwhelming
// common case. The narrow TOCTOU window this leaves (two nodes generating
// for the very first time within the same instant) is self-healing
// regardless — LoadOrGenerateSecret always reloads from disk after this
// returns, so every daemon converges on whichever write landed last within
// one Watch poll interval.
func generateSecretFile(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("peer: creating cluster secret directory %s: %w", dir, err)
	}

	if _, err := os.Stat(path); err == nil {
		return nil // another process/node already published one; nothing to do
	}

	buf := make([]byte, secretLen)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("peer: generating cluster secret: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".cluster-secret-*.tmp")
	if err != nil {
		return fmt.Errorf("peer: creating temporary cluster secret file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // best-effort cleanup; a no-op once Rename below has succeeded and removed the name's only reference

	if _, err := tmp.WriteString(hex.EncodeToString(buf) + "\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("peer: writing temporary cluster secret file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("peer: closing temporary cluster secret file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("peer: publishing cluster secret file %s: %w", path, err)
	}
	return nil
}

// reload re-reads the secret from disk, hex-decoding it. It returns the
// underlying os error (wrapped) unchanged on os.ErrNotExist so callers can
// errors.Is against it, and a distinct error for any other failure
// (unreadable, malformed, wrong length).
func (s *SecretStore) reload() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("peer: cluster secret %s: %w", s.path, err)
		}
		return fmt.Errorf("peer: reading cluster secret %s: %w", s.path, err)
	}

	decoded, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("peer: cluster secret %s is not valid hex: %w", s.path, err)
	}
	if len(decoded) < secretLen {
		return fmt.Errorf("peer: cluster secret %s is too short (%d bytes, want at least %d)", s.path, len(decoded), secretLen)
	}

	modTime := statModTime(s.path)

	s.mu.Lock()
	s.secret = decoded
	s.modTime = modTime
	s.mu.Unlock()
	return nil
}

// Current returns the currently loaded secret bytes. Callers must not
// mutate the returned slice.
func (s *SecretStore) Current() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.secret
}

func (s *SecretStore) changedOnDisk() bool {
	s.mu.RLock()
	modTime := s.modTime
	s.mu.RUnlock()
	return !statModTime(s.path).Equal(modTime)
}

func statModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// Watch polls the secret file for changes (pmxcfs propagating another
// node's rotation, or an operator-driven rotation) until ctx is cancelled,
// reloading whenever the mtime changes. Mirrors config.CertProvider.Watch's
// polling design and its rationale (secrets change on the order of a
// cluster's lifetime, not sub-second, so inotify's latency advantage buys
// nothing here).
func (s *SecretStore) Watch(ctx context.Context, pollInterval time.Duration) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if s.changedOnDisk() {
				if err := s.reload(); err != nil {
					s.log.Error("peer: reloading cluster secret failed", "error", err)
				} else {
					s.log.Info("peer: cluster secret reloaded", "path", s.path)
				}
			}
		}
	}
}
