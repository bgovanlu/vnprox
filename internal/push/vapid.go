// Package push implements T-2005's web-push delivery: RFC 8291 message
// encryption, RFC 8292 VAPID request authentication, and the
// category-filtered dispatcher that turns internal/topology.Hub's event
// fan-in (T-1104's SetEventSink seam) and internal/findings' Notifier hook
// into safe, enumerated push payloads.
//
// vapid.go implements this daemon's own VAPID identity: an ECDSA P-256
// keypair generated at first use and persisted to disk, following the exact
// "generate if absent, 0600, belt-and-suspenders" convention
// cmd/vnproxd/blueprintbundle.go's setupBlueprintSigningKey (Ed25519) and
// cmd/vnproxd/metricsexporter.go's setupMetricsExporterToken already
// establish for this codebase's other at-rest identities — see those files'
// doc comments. Ship no default key: every install generates its own on
// first daemon start (docs/security.md's Authentication section documents
// the identical rule for the session-secret key).
package push

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
)

// vapidCurve is the curve RFC 8292 requires for VAPID keys (and the one
// RFC 8291 requires for the per-message ECDH keys in webpush.go) —
// P-256/prime256v1/secp256r1, the only curve every major push service
// (Chrome/FCM, Firefox/autopush, Safari) accepts.
var vapidCurve = elliptic.P256()

// uncompressedPointLen is a P-256 uncompressed SEC1 point's fixed length:
// 0x04 || X (32 bytes) || Y (32 bytes).
const uncompressedPointLen = 1 + 32 + 32

// GenerateVAPIDKeyFile creates a new ECDSA P-256 private key and writes it
// to path base64-encoded (the raw 32-byte scalar, matching
// blueprint.GenerateSigningKeyFile's "just the seed/scalar, one line"
// convention rather than a PEM envelope this package never needs to
// interoperate with anything else on) with mode 0600, creating parent
// directories as needed. It fails if a file already exists at path, so a
// second daemon start never silently overwrites (and thereby orphans)
// every already-issued browser subscription's applicationServerKey — VAPID
// key rotation is an explicit operator action (delete the file, every live
// subscription then needs to re-subscribe), not an accidental side effect
// of a restart.
func GenerateVAPIDKeyFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("push: VAPID key file %s already exists, refusing to overwrite", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("push: checking VAPID key file %s: %w", path, err)
	}

	priv, err := ecdsa.GenerateKey(vapidCurve, rand.Reader)
	if err != nil {
		return fmt.Errorf("push: generating VAPID key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("push: creating VAPID key directory for %s: %w", path, err)
	}

	// D (the private scalar) is the only secret material; it is always
	// exactly 32 bytes for P-256 (FillBytes zero-pads a scalar that
	// happens to have leading zero bytes, which crypto/ecdsa's own
	// D.Bytes() would silently drop, producing a shorter-than-32-byte
	// encoding on a small fraction of keys).
	var scalar [32]byte
	priv.D.FillBytes(scalar[:])
	encoded := base64.StdEncoding.EncodeToString(scalar[:])
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		return fmt.Errorf("push: writing VAPID key file %s: %w", path, err)
	}
	return nil
}

// LoadVAPIDKeyFile reads and reconstructs the ECDSA P-256 private key
// previously written by GenerateVAPIDKeyFile.
func LoadVAPIDKeyFile(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("push: reading VAPID key file %s: %w", path, err)
	}
	scalar, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("push: VAPID key file %s is not valid base64: %w", path, err)
	}
	if len(scalar) != 32 {
		return nil, fmt.Errorf("push: VAPID key file %s decodes to %d bytes, want 32", path, len(scalar))
	}
	priv, err := privateKeyFromScalar(scalar)
	if err != nil {
		return nil, fmt.Errorf("push: VAPID key file %s: %w", path, err)
	}
	return priv, nil
}

// privateKeyFromScalar reconstructs a P-256 ecdsa.PrivateKey from its raw
// 32-byte scalar, deriving the public key via crypto/ecdh — the standard
// "private key is just the scalar, public key is recomputed" reconstruction,
// since GenerateVAPIDKeyFile only persists D. Routed through crypto/ecdh
// rather than the lower-level elliptic.Curve.ScalarBaseMult (deprecated
// since Go 1.21 in favor of exactly this) both to follow that guidance and
// because ecdh.P256().NewPrivateKey validates the scalar is in the curve's
// valid private-key range, a check raw ScalarBaseMult does not perform.
func privateKeyFromScalar(scalar []byte) (*ecdsa.PrivateKey, error) {
	ecdhPriv, err := ecdh.P256().NewPrivateKey(scalar)
	if err != nil {
		return nil, fmt.Errorf("invalid private key scalar: %w", err)
	}
	pubBytes := ecdhPriv.PublicKey().Bytes() // 0x04 || X(32) || Y(32)
	if len(pubBytes) != uncompressedPointLen {
		return nil, errors.New("derived public key has unexpected length")
	}
	return &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: vapidCurve,
			X:     new(big.Int).SetBytes(pubBytes[1:33]),
			Y:     new(big.Int).SetBytes(pubBytes[33:65]),
		},
		D: new(big.Int).SetBytes(scalar),
	}, nil
}

// UncompressedPublicKey returns pub's uncompressed SEC1 point encoding
// (0x04 || X(32) || Y(32), 65 bytes for P-256) — the wire form both RFC
// 8291's per-message ephemeral keys and RFC 8292's VAPID identity key use.
// Built by hand (rather than the deprecated elliptic.Marshal) so X/Y are
// always fixed-width, zero-padded 32-byte fields even for a coordinate that
// happens to have leading zero bytes.
func UncompressedPublicKey(pub *ecdsa.PublicKey) []byte {
	out := make([]byte, uncompressedPointLen)
	out[0] = 0x04
	pub.X.FillBytes(out[1:33])
	pub.Y.FillBytes(out[33:65])
	return out
}

// PublicKeyBase64URL returns pub's uncompressed point, base64url-encoded
// without padding — the exact string shape both the browser's
// `PushManager.subscribe({applicationServerKey})` call and RFC 8292's
// Authorization header `k=` parameter need.
func PublicKeyBase64URL(pub *ecdsa.PublicKey) string {
	return base64.RawURLEncoding.EncodeToString(UncompressedPublicKey(pub))
}
