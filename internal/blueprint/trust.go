// SPDX-License-Identifier: Apache-2.0

// trust.go implements T-1107's admin-managed trust store of signers whose
// bundle signatures this installation accepts without an explicit
// per-import trust step (docs/features/blueprints.md §5): a directory of
// small JSON files under /etc/vnprox/keys/trusted-signers/, one per pinned
// public key, keyed by that key's Fingerprint (bundle.go). This is
// filesystem state, not a SQLite table — CLAUDE.md's "Proxmox is the source
// of truth ... never persist a shadow copy" rule is about network config,
// not this, but the same "app-owned data lives somewhere durable and
// simple" instinct applies: a signer either is or isn't in this directory,
// there is no diffing/versioning need that would justify a store migration
// (see the T-1107 report for the explicit "no migration needed" note).

package blueprint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TrustedSigner is one pinned public key: the wire/storage shape for both
// GET/POST/DELETE /blueprint-signers and the on-disk JSON file.
type TrustedSigner struct {
	Fingerprint string `json:"fingerprint"`
	// PublicKey is base64-standard-encoded, the raw 32-byte Ed25519 public
	// key this fingerprint names.
	PublicKey string `json:"publicKey"`
	Label     string `json:"label,omitempty"`
	AddedBy   string `json:"addedBy,omitempty"`
	AddedAt   int64  `json:"addedAt"`
}

// TrustStore reads/writes TrustedSigner rows under a directory, one JSON
// file per signer named "<fingerprint>.json". Fingerprint is a lowercase
// hex SHA-256 digest ([0-9a-f]{64}) — already a safe filename component, so
// no further escaping is needed, but validateFingerprint below still
// rejects anything else defensively (a path-traversal attempt via a
// malformed fingerprint string must never reach os.ReadFile/WriteFile/
// Remove).
type TrustStore struct {
	dir string
}

// NewTrustStore constructs a TrustStore rooted at dir (conventionally
// /etc/vnprox/keys/trusted-signers/, docs/features/blueprints.md §5).
func NewTrustStore(dir string) *TrustStore {
	return &TrustStore{dir: dir}
}

// validateFingerprint rejects anything that isn't a plausible hex SHA-256
// digest, so a caller-supplied fingerprint (from a URL path segment or a
// bundle's claimed publicKeyFingerprint) can never be used to construct a
// path outside dir.
func validateFingerprint(fp string) error {
	if len(fp) != 64 {
		return fmt.Errorf("%w: fingerprint must be a 64-character hex string", ErrInvalid)
	}
	for _, c := range fp {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("%w: fingerprint must be lowercase hex", ErrInvalid)
		}
	}
	return nil
}

func (t *TrustStore) path(fingerprint string) string {
	return filepath.Join(t.dir, fingerprint+".json")
}

// List returns every trusted signer, ordered by fingerprint for stable
// output. An empty/missing trust-store directory returns an empty slice,
// not an error — a fresh installation with no signers pinned yet is the
// expected default state, not a failure.
func (t *TrustStore) List() ([]TrustedSigner, error) {
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []TrustedSigner{}, nil
		}
		return nil, fmt.Errorf("blueprint: listing trusted signers in %s: %w", t.dir, err)
	}
	out := make([]TrustedSigner, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(t.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("blueprint: reading trusted signer file %s: %w", e.Name(), err)
		}
		var s TrustedSigner
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, fmt.Errorf("blueprint: decoding trusted signer file %s: %w", e.Name(), err)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Fingerprint < out[j].Fingerprint })
	return out, nil
}

// Get returns the trusted signer named by fingerprint, and ok=false (no
// error) if it isn't pinned.
func (t *TrustStore) Get(fingerprint string) (TrustedSigner, bool, error) {
	if err := validateFingerprint(fingerprint); err != nil {
		return TrustedSigner{}, false, err
	}
	data, err := os.ReadFile(t.path(fingerprint))
	if err != nil {
		if os.IsNotExist(err) {
			return TrustedSigner{}, false, nil
		}
		return TrustedSigner{}, false, fmt.Errorf("blueprint: reading trusted signer %s: %w", fingerprint, err)
	}
	var s TrustedSigner
	if err := json.Unmarshal(data, &s); err != nil {
		return TrustedSigner{}, false, fmt.Errorf("blueprint: decoding trusted signer %s: %w", fingerprint, err)
	}
	return s, true, nil
}

// Add pins s (keyed by s.Fingerprint), overwriting any existing entry for
// the same fingerprint — re-adding an already-trusted signer (e.g. to
// change its label) is not an error, unlike GenerateSigningKeyFile's
// refuse-to-overwrite contract: a trust-store entry has no live state
// (sessions, signed bundles already in flight) that a silent overwrite
// could orphan the way overwriting a signing key would.
func (t *TrustStore) Add(s TrustedSigner) error {
	if err := validateFingerprint(s.Fingerprint); err != nil {
		return err
	}
	if err := os.MkdirAll(t.dir, 0o700); err != nil {
		return fmt.Errorf("blueprint: creating trust store directory %s: %w", t.dir, err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("blueprint: encoding trusted signer %s: %w", s.Fingerprint, err)
	}
	if err := os.WriteFile(t.path(s.Fingerprint), data, 0o600); err != nil {
		return fmt.Errorf("blueprint: writing trusted signer %s: %w", s.Fingerprint, err)
	}
	return nil
}

// Delete removes the trusted signer named by fingerprint. Deleting a
// fingerprint that isn't pinned returns ErrNotFound.
func (t *TrustStore) Delete(fingerprint string) error {
	if err := validateFingerprint(fingerprint); err != nil {
		return err
	}
	if err := os.Remove(t.path(fingerprint)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNotFound, fingerprint)
		}
		return fmt.Errorf("blueprint: deleting trusted signer %s: %w", fingerprint, err)
	}
	return nil
}
