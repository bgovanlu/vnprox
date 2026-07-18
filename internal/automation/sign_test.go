package automation

import "testing"

func TestSignAndVerifySignature_RoundTrip(t *testing.T) {
	secret := []byte("s3cret")
	body := []byte(`{"event":"changeset.status","id":"cs1"}`)

	sig := Sign(secret, body)
	if sig == "" {
		t.Fatal("Sign returned empty signature")
	}
	if !VerifySignature(secret, body, sig) {
		t.Error("VerifySignature(correct sig) = false, want true")
	}
}

func TestVerifySignature_TamperedBodyFails(t *testing.T) {
	secret := []byte("s3cret")
	body := []byte(`{"event":"changeset.status","id":"cs1"}`)
	sig := Sign(secret, body)

	tampered := []byte(`{"event":"changeset.status","id":"cs2"}`)
	if VerifySignature(secret, tampered, sig) {
		t.Error("VerifySignature(tampered body) = true, want false")
	}
}

func TestVerifySignature_WrongSecretFails(t *testing.T) {
	body := []byte(`{"event":"drift.changed"}`)
	sig := Sign([]byte("secret-a"), body)
	if VerifySignature([]byte("secret-b"), body, sig) {
		t.Error("VerifySignature(wrong secret) = true, want false")
	}
}

func TestVerifySignature_MalformedSignatureFailsWithoutPanic(t *testing.T) {
	if VerifySignature([]byte("s"), []byte("body"), "not-hex-!!") {
		t.Error("VerifySignature(malformed hex) = true, want false")
	}
	if VerifySignature([]byte("s"), []byte("body"), "") {
		t.Error("VerifySignature(empty) = true, want false")
	}
	if VerifySignature(nil, []byte("body"), Sign([]byte("s"), []byte("body"))) {
		t.Error("VerifySignature(empty secret) = true, want false")
	}
}
