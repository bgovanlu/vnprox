package push

import (
	"crypto/ecdh"
	"math/big"
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
	if _, pubErr := ecdh.P256().NewPublicKey(UncompressedPublicKey(&priv.PublicKey)); pubErr != nil {
		t.Errorf("loaded VAPID key's public point is not a valid P-256 point: %v", pubErr)
	}

	// Loading the same file twice must yield the identical key, proving
	// LoadVAPIDKeyFile is deterministic over what was actually persisted
	// rather than generating anything itself.
	priv2, err := LoadVAPIDKeyFile(path)
	if err != nil {
		t.Fatalf("second LoadVAPIDKeyFile: %v", err)
	}
	if priv.D.Cmp(priv2.D) != 0 {
		t.Error("two loads of the same key file produced different private scalars")
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

	encoded := PublicKeyBase64URL(&priv.PublicKey)
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
	// any well-formed point — reconstruct and compare X/Y directly.
	x := new(big.Int).SetBytes(decoded[1:33])
	y := new(big.Int).SetBytes(decoded[33:65])
	if x.Cmp(priv.X) != 0 || y.Cmp(priv.Y) != 0 {
		t.Error("PublicKeyBase64URL's decoded X/Y do not match the key's own PublicKey.X/Y")
	}
}

func TestPrivateKeyFromScalar_RejectsZeroScalar(t *testing.T) {
	if _, err := privateKeyFromScalar(make([]byte, 32)); err == nil {
		t.Fatal("privateKeyFromScalar(zero) returned nil error, want a rejection")
	}
}
