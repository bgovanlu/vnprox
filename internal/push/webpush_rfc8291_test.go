// SPDX-License-Identifier: Apache-2.0

package push

import (
	"bytes"
	"crypto/ecdh"
	"testing"
)

// TestEncryptAES128GCM_RFC8291AppendixA proves encryptAES128GCM implements
// RFC 8291 correctly by reproducing the RFC's own worked example (Appendix
// A, "Encryption Summary") byte-for-byte: every input is the RFC's fixed
// test value, and the output is compared against the RFC's own final
// encrypted message. A crypto primitive whose only test is "it round-trips
// against itself" is exactly the kind of test that passes on a broken
// implementation two independent people could disagree about; this pins
// interop with the actual RFC instead.
func TestEncryptAES128GCM_RFC8291AppendixA(t *testing.T) {
	uaPublic := mustDecodeB64URL(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcx"+
		"aOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	authSecret := mustDecodeB64URL(t, "BTBZMqHH6r4Tts7J_aSIgg")
	salt := mustDecodeB64URL(t, "DGv6ra1nlYgDCS1FRnbzlw")
	asPrivScalar := mustDecodeB64URL(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw")
	plaintext := mustDecodeB64URL(t, "V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24")
	wantMessage := mustDecodeB64URL(t, "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml"+
		"mlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPT"+
		"pK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN")

	asPriv, err := ecdh.P256().NewPrivateKey(asPrivScalar)
	if err != nil {
		t.Fatalf("constructing ephemeral private key from RFC vector: %v", err)
	}

	// Sanity check on the fixture itself: the RFC's own ephemeral public
	// key line should equal what deriving it from the private scalar
	// produces, or every input below is already wrong.
	wantAsPublic := mustDecodeB64URL(t, "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIg"+
		"Dll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8")
	if !bytes.Equal(asPriv.PublicKey().Bytes(), wantAsPublic) {
		t.Fatalf("ephemeral public key derived from RFC's private scalar does not match RFC's own public key line — fixture is wrong")
	}

	got, err := encryptAES128GCM(plaintext, uaPublic, authSecret, salt, asPriv)
	if err != nil {
		t.Fatalf("encryptAES128GCM: %v", err)
	}
	if !bytes.Equal(got, wantMessage) {
		t.Errorf("encryptAES128GCM output does not match RFC 8291 Appendix A:\n got  = %x\n want = %x", got, wantMessage)
	}
}

// TestEncryptAES128GCM_WrongAuthSecretProducesDifferentCiphertext is the
// mutation-style negative leg for the positive RFC-vector test above:
// changing ONE input (the auth secret) that the RFC vector says should feed
// into the derivation must change the output. This is what rules out a
// vacuously-passing implementation that ignores authSecret entirely (e.g. a
// bug that derives keys from uaPublic and the ephemeral key alone) — that
// bug would still pass the exact-vector test above by coincidence only if
// it also happened to hardcode the RFC's values, which the code plainly
// does not.
func TestEncryptAES128GCM_WrongAuthSecretProducesDifferentCiphertext(t *testing.T) {
	uaPublic := mustDecodeB64URL(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcx"+
		"aOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	salt := mustDecodeB64URL(t, "DGv6ra1nlYgDCS1FRnbzlw")
	asPrivScalar := mustDecodeB64URL(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw")
	plaintext := mustDecodeB64URL(t, "V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24")
	asPriv, err := ecdh.P256().NewPrivateKey(asPrivScalar)
	if err != nil {
		t.Fatalf("constructing ephemeral private key: %v", err)
	}

	rightAuth := mustDecodeB64URL(t, "BTBZMqHH6r4Tts7J_aSIgg")
	wrongAuth := make([]byte, len(rightAuth))
	copy(wrongAuth, rightAuth)
	wrongAuth[0] ^= 0xff

	got1, err := encryptAES128GCM(plaintext, uaPublic, rightAuth, salt, asPriv)
	if err != nil {
		t.Fatalf("encryptAES128GCM(rightAuth): %v", err)
	}
	got2, err := encryptAES128GCM(plaintext, uaPublic, wrongAuth, salt, asPriv)
	if err != nil {
		t.Fatalf("encryptAES128GCM(wrongAuth): %v", err)
	}
	if bytes.Equal(got1, got2) {
		t.Error("encryptAES128GCM output is identical for two different auth secrets — the auth secret is not actually being mixed into key derivation")
	}
}

func mustDecodeB64URL(t *testing.T, s string) []byte {
	t.Helper()
	b, err := decodeBase64URL(s)
	if err != nil {
		t.Fatalf("decoding fixture %q: %v", s, err)
	}
	return b
}
