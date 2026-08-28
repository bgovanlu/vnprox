// SPDX-License-Identifier: Apache-2.0

// signingkey.go implements the on-disk Ed25519 signing identity T-1107's
// bundle export uses (docs/features/blueprints.md §5): a keypair generated
// at first use and stored at /etc/vnprox/keys/blueprint-signing.key,
// root:root 0600 — the same "generate if absent, never silently overwrite"
// convention docs/security.md's session key (internal/store.GenerateKeyFile)
// and metrics scrape token (internal/store.GenerateHexTokenFile) already
// use. This package doesn't import internal/store for the ~20 lines that
// convention takes to implement — the same "duplicated rather than forced
// into a shared package for one seam" reasoning internal/store/token.go's
// own doc comment gives for its near-identical helper over cipher.go's.

package blueprint

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GenerateSigningKeyFile creates a new random Ed25519 keypair and writes its
// private seed (32 bytes, base64-standard-encoded, plus a trailing newline)
// to path with mode 0600, creating parent directories (mode 0700) as
// needed. It fails if a file already exists at path, so an installation's
// signing identity — and therefore every fingerprint a receiving admin has
// already pinned for it — is never silently orphaned by a second call.
func GenerateSigningKeyFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("blueprint: signing key file %s already exists, refusing to overwrite", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("blueprint: checking signing key file %s: %w", path, err)
	}

	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return fmt.Errorf("blueprint: generating signing key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("blueprint: creating signing key directory for %s: %w", path, err)
	}

	encoded := base64.StdEncoding.EncodeToString(priv.Seed())
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		return fmt.Errorf("blueprint: writing signing key file %s: %w", path, err)
	}
	return nil
}

// LoadSigningKeyFile reads and reconstructs the Ed25519 private key
// previously written by GenerateSigningKeyFile (or an equivalent, e.g. a
// packaging script).
func LoadSigningKeyFile(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("blueprint: reading signing key file %s: %w", path, err)
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("blueprint: signing key file %s is not valid base64: %w", path, err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("blueprint: signing key file %s decodes to %d bytes, want %d", path, len(seed), ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}
