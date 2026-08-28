// SPDX-License-Identifier: Apache-2.0

package store

// token.go implements generic hex-encoded random secret token
// generation/loading for vnprox's on-disk secrets that — unlike the session
// AES key in cipher.go, which is only ever consumed as raw key material
// inside this daemon — must also be directly usable as HTTP header text by
// an external caller. T-1001's Prometheus scrape token
// (/etc/vnprox/keys/metrics.key) is the first consumer: it is presented
// verbatim as an `Authorization: Bearer <token>` header value by a scraper,
// or pasted straight into a Prometheus `bearer_token_file`. Hex encoding
// keeps the on-disk content restricted to a safe, header-value-printable
// character set regardless of which random bytes were drawn, matching the
// on-disk format internal/peer's cluster secret (secret.go) and
// packaging/bin/vnprox-setup's PVE token secret already use for the
// identical reason — this package doesn't import internal/peer (or vice
// versa) just to share ~20 lines of "hex-encode 32 random bytes, write
// 0600, don't clobber an existing file" logic, so the pattern is
// duplicated here rather than factored into a new shared package.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// TokenSize is a generated hex token's length in raw bytes (256 bits)
// before hex encoding doubles it on disk.
const TokenSize = 32

// GenerateHexTokenFile creates a new random 256-bit token, hex-encodes it,
// and writes it (plus a trailing newline) to path with mode 0600, creating
// parent directories as needed. It fails if a file already exists at path —
// the same "never silently overwrite (and thereby orphan whatever already
// trusts it)" contract GenerateKeyFile above enforces for the session key.
func GenerateHexTokenFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("store: token file %s already exists, refusing to overwrite", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("store: checking token file %s: %w", path, err)
	}

	raw := make([]byte, TokenSize)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return fmt.Errorf("store: generating token: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("store: creating token directory for %s: %w", path, err)
	}

	if err := os.WriteFile(path, []byte(hex.EncodeToString(raw)+"\n"), 0o600); err != nil {
		return fmt.Errorf("store: writing token file %s: %w", path, err)
	}
	return nil
}

// LoadHexTokenFile reads and validates a hex-encoded token previously
// written by GenerateHexTokenFile (or an equivalent, e.g. a packaging
// script), returning the trimmed hex string itself — the exact text a
// caller presents as the Bearer token — after confirming it decodes to
// TokenSize bytes.
func LoadHexTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("store: reading token file %s: %w", path, err)
	}
	token := strings.TrimSpace(string(data))
	decoded, err := hex.DecodeString(token)
	if err != nil {
		return "", fmt.Errorf("store: token file %s is not valid hex: %w", path, err)
	}
	if len(decoded) != TokenSize {
		return "", fmt.Errorf("%w: token file %s decodes to %d bytes, want %d", ErrInvalidKey, path, len(decoded), TokenSize)
	}
	return token, nil
}
