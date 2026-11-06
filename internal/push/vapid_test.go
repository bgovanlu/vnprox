// SPDX-License-Identifier: Apache-2.0

package push

import (
	"bytes"
	"crypto/ecdh"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateAndLoadVAPIDKeyFile_RoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "push-vapid.key")

	if err := GenerateVAPIDKeyFile(path); err != nil {
		t.Fatalf("GenerateVAPIDKeyFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat generated key file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %o, want 0600", perm)
	}

	priv, err := LoadVAPIDKeyFile(path)
	if err != nil {
		t.Fatalf("LoadVAPIDKeyFile: %v", err)
	}
	// ecdh.P256().NewPublicKey performs the on-curve validation itself
	// (crypto/ecdh's documented replacement for the deprecated
	// elliptic.Curve.IsOnCurve) — an error here means UncompressedPublicKey
	// produced a point that isn't valid.
	point, err := UncompressedPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("UncompressedPublicKey: %v", err)
	}
	if _, pubErr := ecdh.P256().NewPublicKey(point); pubErr != nil {
		t.Errorf("loaded VAPID key's public point is not a valid P-256 point: %v", pubErr)
	}

	// Loading the same file twice must yield the identical key, proving
	// LoadVAPIDKeyFile is deterministic over what was actually persisted
	// rather than generating anything itself.
	priv2, err := LoadVAPIDKeyFile(path)
	if err != nil {
		t.Fatalf("second LoadVAPIDKeyFile: %v", err)
	}
	if !priv.Equal(priv2) {
		t.Error("two loads of the same key file produced different private keys")
	}
}

func TestGenerateVAPIDKeyFile_RefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push-vapid.key")
	if err := GenerateVAPIDKeyFile(path); err != nil {
		t.Fatalf("first GenerateVAPIDKeyFile: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading key file: %v", err)
	}

	if overwriteErr := GenerateVAPIDKeyFile(path); overwriteErr == nil {
		t.Fatal("second GenerateVAPIDKeyFile over an existing file returned nil error, want a refusal")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading key file after refused overwrite: %v", err)
	}
	if string(before) != string(after) {
		t.Error("key file content changed despite GenerateVAPIDKeyFile refusing to overwrite")
	}
}

func TestPublicKeyBase64URL_DecodesToUncompressedPoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push-vapid.key")
	if err := GenerateVAPIDKeyFile(path); err != nil {
		t.Fatalf("GenerateVAPIDKeyFile: %v", err)
	}
	priv, err := LoadVAPIDKeyFile(path)
	if err != nil {
		t.Fatalf("LoadVAPIDKeyFile: %v", err)
	}

	encoded, err := PublicKeyBase64URL(&priv.PublicKey)
	if err != nil {
		t.Fatalf("PublicKeyBase64URL: %v", err)
	}
	decoded, err := decodeBase64URL(encoded)
	if err != nil {
		t.Fatalf("decoding PublicKeyBase64URL output: %v", err)
	}
	if len(decoded) != uncompressedPointLen {
		t.Fatalf("decoded public key length = %d, want %d", len(decoded), uncompressedPointLen)
	}
	if decoded[0] != 0x04 {
		t.Errorf("decoded public key first byte = %#x, want 0x04 (uncompressed point marker)", decoded[0])
	}

	// The encoded string must actually describe THIS key's point, not just
	// any well-formed point. crypto/ecdh is an independent route to it.
	ecdhPriv, err := priv.ECDH()
	if err != nil {
		t.Fatalf("converting the key to crypto/ecdh: %v", err)
	}
	if !bytes.Equal(decoded, ecdhPriv.PublicKey().Bytes()) {
		t.Error("PublicKeyBase64URL's decoded point does not match the key's own public point")
	}
}

// Scalar 1's public key is the P-256 base point published in FIPS 186-4, so the
// expected value owes nothing to the code under test. Its 31 leading zero bytes
// also pin the fixed-width encoding the key file format depends on.
func TestVAPIDKeyEncoding_MatchesTheP256BasePoint(t *testing.T) {
	const basePoint = "04" +
		"6b17d1f2e12c4247f8bce6e563a440f277037d812deb33a0f4a13945d898c296" +
		"4fe342e2fe1a7f9b8ee7eb4a7c0f9e162bce33576b315ececbb6406837bf51f5"

	scalar := make([]byte, 32)
	scalar[31] = 1
	priv, err := privateKeyFromScalar(scalar)
	if err != nil {
		t.Fatalf("privateKeyFromScalar: %v", err)
	}

	gotScalar, err := priv.Bytes()
	if err != nil {
		t.Fatalf("priv.Bytes: %v", err)
	}
	if !bytes.Equal(gotScalar, scalar) {
		t.Errorf("scalar round trip = %x, want %x", gotScalar, scalar)
	}

	point, err := UncompressedPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("UncompressedPublicKey: %v", err)
	}
	if got := hex.EncodeToString(point); got != basePoint {
		t.Errorf("public point = %s, want the P-256 base point %s", got, basePoint)
	}
}

func TestPrivateKeyFromScalar_RejectsZeroScalar(t *testing.T) {
	if _, err := privateKeyFromScalar(make([]byte, 32)); err == nil {
		t.Fatal("privateKeyFromScalar(zero) returned nil error, want a rejection")
	}
}
