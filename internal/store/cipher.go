// SPDX-License-Identifier: Apache-2.0

// cipher.go implements the session-secret encryption-at-rest helpers
// described in docs/security.md "Authentication": sessions.pve_ticket_enc
// and sessions.csrf_token_enc are AES-256-GCM ciphertext, keyed from a
// 256-bit key conventionally stored at /etc/vnprox/keys/session.key
// (root:root 0600, generated at install).
//
// This package never hardcodes that path: SessionCipher is constructed from
// raw key bytes, and LoadKeyFile/GenerateKeyFile are separate, injectable
// helpers so tests (and the real daemon/installer) can supply the key
// however is appropriate for their environment.

package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// KeySize is the required length, in bytes, of a session-secret encryption
// key (AES-256).
const KeySize = 32

// SessionCipher encrypts/decrypts sessions.pve_ticket_enc and
// sessions.csrf_token_enc with AES-256-GCM. The nonce is generated per call
// to Encrypt and stored as a prefix of the returned ciphertext.
type SessionCipher struct {
	aead cipher.AEAD
}

// NewSessionCipher builds a SessionCipher from a raw 256-bit key. It returns
// ErrInvalidKey if key is not exactly KeySize bytes.
func NewSessionCipher(key []byte) (*SessionCipher, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w: want %d bytes, got %d", ErrInvalidKey, KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("store: constructing AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("store: constructing GCM mode: %w", err)
	}
	return &SessionCipher{aead: aead}, nil
}

// Encrypt seals plaintext, returning nonce||ciphertext||tag suitable for
// storing directly in a BLOB column.
func (c *SessionCipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("store: generating nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt. It returns an error if sealed is truncated or
// the tag doesn't verify (wrong key or tampered data).
func (c *SessionCipher) Decrypt(sealed []byte) ([]byte, error) {
	nonceSize := c.aead.NonceSize()
	if len(sealed) < nonceSize {
		return nil, fmt.Errorf("store: ciphertext shorter than nonce (got %d bytes)", len(sealed))
	}
	nonce, ciphertext := sealed[:nonceSize], sealed[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("store: decrypting: %w", err)
	}
	return plaintext, nil
}

// GenerateKeyFile creates a new random 256-bit key and writes it to path
// with mode 0600, creating parent directories as needed. It fails if a file
// already exists at path, to avoid silently overwriting (and thereby
// orphaning) an existing key that live sessions were encrypted under.
//
// This is the helper the packaging postinst script (docs/security.md:
// "generated at install") and ad-hoc test setup are expected to use; it is
// not called automatically by Open/NewSessionCipher.
func GenerateKeyFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("store: key file %s already exists, refusing to overwrite", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("store: checking key file %s: %w", path, err)
	}

	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return fmt.Errorf("store: generating key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("store: creating key directory for %s: %w", path, err)
	}

	if err := os.WriteFile(path, key, 0o600); err != nil {
		return fmt.Errorf("store: writing key file %s: %w", path, err)
	}
	return nil
}

// LoadKeyFile reads and validates a session-secret key previously written by
// GenerateKeyFile (or an equivalent, e.g. the packaging postinst script).
func LoadKeyFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("store: reading key file %s: %w", path, err)
	}
	if len(data) != KeySize {
		return nil, fmt.Errorf("%w: key file %s is %d bytes, want %d", ErrInvalidKey, path, len(data), KeySize)
	}
	return data, nil
}
