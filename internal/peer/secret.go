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
// (docs/deployment.md: "Generates the cluster secret in /etc/pve/vnprox/
// (first node only; pmxcfs replicates it)."; docs/architecture.md §5). It
// sits under pmxcfs, which is already cluster-replicated and root-only, so
// every node's daemon converges on the same secret without vnprox needing
// its own distribution mechanism.
const DefaultSecretPath = "/etc/pve/vnprox/cluster.secret"

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
func LoadOrGenerateSecret(path string, logger *slog.Logger) (*SecretStore, error) {
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
// no-op (not an error) if another process/node won a concurrent generation
// race — see LoadOrGenerateSecret's doc comment.
//
// The content is written in full to a temporary file first and only then
// published at path via os.Link, which atomically fails with ErrExist if
// path already has an entry. This — rather than an O_CREATE|O_EXCL open
// directly on path — is what closes the race a naive "reserve the name,
// then write into it" approach leaves open: with O_EXCL, a concurrent
// loser can observe the winner's reserved-but-not-yet-written file and
// read it as empty/truncated. Here, nothing is ever visible at path until
// its content already exists in full under the temp name, so any reader
// that sees path exist always sees complete, valid content.
func generateSecretFile(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("peer: creating cluster secret directory %s: %w", dir, err)
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
	defer func() { _ = os.Remove(tmpPath) }() // best-effort cleanup; a no-op once Link below has succeeded and removed the name's only other reference

	if _, err := tmp.WriteString(hex.EncodeToString(buf) + "\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("peer: writing temporary cluster secret file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("peer: closing temporary cluster secret file: %w", err)
	}

	if err := os.Link(tmpPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil // lost the race; the existing file is authoritative
		}
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
