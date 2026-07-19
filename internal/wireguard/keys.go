package wireguard

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// KeyLen is the byte length of a raw Curve25519 WireGuard key (public or
// private): 32 bytes, base64-encoded to a 44-character string in every
// on-the-wire and config-file form.
const KeyLen = 32

// GenerateKeypair generates a fresh WireGuard keypair on this node using the
// standard library's X25519 curve (crypto/ecdh — Go 1.20+, no third-party
// crypto dependency, per the T-1401 key-custody analysis). It returns the raw
// 32-byte private and derived public keys.
//
// The private key is the sensitive half: callers seal it with
// internal/store.SessionCipher immediately and never persist, log, or return
// it in plaintext (docs/security.md's WireGuard credential-storage note). The
// clamping WireGuard's reference implementation applies to a private key is
// performed by crypto/ecdh internally, so the derived public key matches
// `wg pubkey` for the same private key.
func GenerateKeypair() (private, public []byte, err error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("wireguard: generating X25519 keypair: %w", err)
	}
	return priv.Bytes(), priv.PublicKey().Bytes(), nil
}

// PublicKeyFor derives the public key for a raw 32-byte private key, so a
// tunnel's exportable public key can be recomputed from the (decrypted)
// stored private key without a second stored copy — though in practice the
// public key is stored alongside the sealed private key and read directly.
// It errors if private is not a valid X25519 scalar length.
func PublicKeyFor(private []byte) ([]byte, error) {
	key, err := ecdh.X25519().NewPrivateKey(private)
	if err != nil {
		return nil, fmt.Errorf("wireguard: deriving public key: %w", err)
	}
	return key.PublicKey().Bytes(), nil
}

// EncodeKey renders a raw 32-byte key in WireGuard's canonical base64 form.
func EncodeKey(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

// DecodeKey parses a WireGuard base64 key string back to its raw 32 bytes,
// rejecting anything that is not exactly KeyLen bytes once decoded.
func DecodeKey(s string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("wireguard: decoding base64 key: %w", err)
	}
	if len(raw) != KeyLen {
		return nil, fmt.Errorf("wireguard: key is %d bytes, want %d", len(raw), KeyLen)
	}
	return raw, nil
}
