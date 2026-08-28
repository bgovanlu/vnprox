// SPDX-License-Identifier: Apache-2.0

package blueprint

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

func testBundleBlueprint() Blueprint {
	return Blueprint{
		BlueprintVersion: CurrentBlueprintVersion,
		ID:               "bp1",
		Name:             "test",
		NodeSelector:     NodeSelector{Mode: SelectAll},
		Entities: []EntityTemplate{
			{Kind: KindBridge, IDTemplate: "vmbr9", Fields: map[string]any{"vlanAware": true}},
		},
	}
}

func TestSignBundle_NilKeyProducesUnsigned(t *testing.T) {
	bundle, err := SignBundle(testBundleBlueprint(), nil)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	if bundle.Signature != nil {
		t.Fatal("expected no signature for a nil priv key")
	}
	verified, fp, err := VerifyBundle(bundle)
	if verified || fp != "" || err != nil {
		t.Fatalf("VerifyBundle on unsigned bundle = (%v, %q, %v), want (false, \"\", nil)", verified, fp, err)
	}
}

func TestSignBundle_RoundTripVerifies(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	bp := testBundleBlueprint()
	bundle, err := SignBundle(bp, priv)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	if bundle.Signature == nil {
		t.Fatal("expected a signature")
	}
	if bundle.Signature.Alg != SignatureAlgEd25519 {
		t.Errorf("alg = %s, want %s", bundle.Signature.Alg, SignatureAlgEd25519)
	}

	verified, fp, err := VerifyBundle(bundle)
	if err != nil {
		t.Fatalf("VerifyBundle: %v", err)
	}
	if !verified {
		t.Fatal("expected the signature to verify")
	}
	pub, _ := priv.Public().(ed25519.PublicKey)
	if want := Fingerprint(pub); fp != want {
		t.Errorf("fingerprint = %s, want %s", fp, want)
	}
}

func TestVerifyBundle_TamperedContentInvalidatesSignature(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	bundle, err := SignBundle(testBundleBlueprint(), priv)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}

	// Tamper with the blueprint after signing (e.g. renaming an entity id) —
	// the signature bytes are unchanged, but they no longer verify against
	// the (now different) canonical bytes.
	bundle.Blueprint.Entities[0].IDTemplate = "vmbr666"

	verified, fp, err := VerifyBundle(bundle)
	if verified {
		t.Fatal("expected tampering to invalidate the signature")
	}
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("err = %v, want ErrInvalidSignature", err)
	}
	if fp == "" {
		t.Error("expected a fingerprint to still be reported even for an invalid signature")
	}
}

func TestVerifyBundle_MismatchedFingerprintIsInvalid(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	bundle, err := SignBundle(testBundleBlueprint(), priv)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	bundle.Signature.PublicKeyFingerprint = "0000000000000000000000000000000000000000000000000000000000000000"

	verified, _, err := VerifyBundle(bundle)
	if verified || !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("VerifyBundle with mismatched fingerprint = (%v, %v), want (false, ErrInvalidSignature)", verified, err)
	}
}

func TestVerifyBundle_UnsupportedAlg(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	bundle, err := SignBundle(testBundleBlueprint(), priv)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	bundle.Signature.Alg = "rsa"

	verified, _, err := VerifyBundle(bundle)
	if verified || !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("VerifyBundle with unsupported alg = (%v, %v), want (false, ErrInvalidSignature)", verified, err)
	}
}

func TestCanonicalBytes_DeterministicAcrossCalls(t *testing.T) {
	bp := testBundleBlueprint()
	bp.Params = []ParamDef{{Name: "a", Type: ParamString}, {Name: "b", Type: ParamInt}}
	bp.Entities[0].Fields = map[string]any{"z": 1, "a": 2, "m": 3}

	b1, err := canonicalBlueprintBytes(bp)
	if err != nil {
		t.Fatalf("canonicalBlueprintBytes: %v", err)
	}
	b2, err := canonicalBlueprintBytes(bp)
	if err != nil {
		t.Fatalf("canonicalBlueprintBytes: %v", err)
	}
	if string(b1) != string(b2) {
		t.Errorf("canonicalBlueprintBytes not deterministic:\n%s\nvs\n%s", b1, b2)
	}
}
