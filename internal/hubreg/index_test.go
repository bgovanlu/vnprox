package hubreg

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/hub"
)

// testKey returns a deterministic-per-call Ed25519 keypair plus its
// fingerprint, the identifier [hub] index_signers pins.
func testKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return priv, blueprint.Fingerprint(pub)
}

func testEntry(t hub.EntryType, id, version string) hub.Entry {
	return hub.Entry{
		Type:        t,
		ID:          id,
		Name:        strings.ToUpper(id),
		Version:     version,
		ArtifactURL: ArtifactPath("", t, id, version),
	}
}

func testDoc() Document {
	return Document{
		SchemaVersion: CurrentIndexSchema,
		GeneratedAt:   1700000000,
		Entries: []hub.Entry{
			testEntry(hub.TypeBlueprint, "bp-a", "1.0.0"),
			testEntry(hub.TypePlugin, "pl-a", "2.1.0"),
		},
	}
}

func mustSign(t *testing.T, d Document, priv ed25519.PrivateKey) []byte {
	t.Helper()
	signed, err := Sign(d, priv)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, err := json.Marshal(signed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestSignVerify_RoundTrip(t *testing.T) {
	priv, fp := testKey(t)
	raw := mustSign(t, testDoc(), priv)

	doc, err := Verify(raw, []string{strings.ToUpper(fp)}) // case-insensitive fingerprint
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(doc.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(doc.Entries))
	}
	if doc.Signature == nil || doc.Signature.PublicKeyFingerprint != fp {
		t.Fatalf("signer = %+v, want %s", doc.Signature, fp)
	}
}

// TestVerify_UntrustedSigner is the AC2 gate at the index layer: a correctly
// signed index by a key the operator never pinned is refused, and an empty
// trusted set trusts nothing (fail closed).
func TestVerify_UntrustedSigner(t *testing.T) {
	priv, fp := testKey(t)
	otherPriv, otherFP := testKey(t)
	raw := mustSign(t, testDoc(), priv)

	if _, err := Verify(raw, []string{otherFP}); !errors.Is(err, ErrUntrustedIndexSigner) {
		t.Fatalf("err = %v, want ErrUntrustedIndexSigner", err)
	}
	if _, err := Verify(raw, nil); !errors.Is(err, ErrUntrustedIndexSigner) {
		t.Fatalf("empty trust set err = %v, want ErrUntrustedIndexSigner", err)
	}
	// The other key's own index is refused against the first fingerprint too
	// (i.e. the check is on the signer, not merely on "some key signed it").
	otherRaw := mustSign(t, testDoc(), otherPriv)
	if _, err := Verify(otherRaw, []string{fp}); !errors.Is(err, ErrUntrustedIndexSigner) {
		t.Fatalf("err = %v, want ErrUntrustedIndexSigner", err)
	}
	if _, err := Verify(otherRaw, []string{otherFP}); err != nil {
		t.Fatalf("the other key's own index should verify against its own fingerprint: %v", err)
	}
}

func TestVerify_UnsignedIndexRefused(t *testing.T) {
	raw, err := json.Marshal(testDoc()) // no Signature field at all
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, verr := Verify(raw, []string{"deadbeef"}); !errors.Is(verr, ErrUnsignedIndex) {
		t.Fatalf("err = %v, want ErrUnsignedIndex", verr)
	}
}

// TestVerify_CorruptedIndex is AC5: a corrupted index fails verification
// rather than partially loading. Corruption is applied at several distinct
// offsets — inside an entry id, inside an artifact URL, inside the schema
// version, inside a revocation's reason, inside the signature itself, and by
// truncation — because a single flipped byte in one field proves only that
// one field is covered.
func TestVerify_CorruptedIndex(t *testing.T) {
	priv, fp := testKey(t)
	d := testDoc()
	d.Revocations = []Revocation{{Type: hub.TypePlugin, ID: "pl-old", Reason: "withdrawn by publisher", At: 1700000001}}
	raw := mustSign(t, d, priv)

	// Sanity: the pristine document verifies, so every failure below is
	// caused by the corruption and not by the fixture.
	if _, err := Verify(raw, []string{fp}); err != nil {
		t.Fatalf("pristine index must verify: %v", err)
	}

	corrupt := func(t *testing.T, near string, replacement string) []byte {
		t.Helper()
		i := bytes.Index(raw, []byte(near))
		if i < 0 {
			t.Fatalf("fixture does not contain %q", near)
		}
		out := make([]byte, 0, len(raw))
		out = append(out, raw[:i]...)
		out = append(out, []byte(replacement)...)
		out = append(out, raw[i+len(near):]...)
		return out
	}

	//nolint:govet // fieldalignment: test table; field order documents each case, not packing.
	tests := []struct {
		name    string
		mutate  func(t *testing.T) []byte
		wantErr error
	}{
		{
			name:    "entry id",
			mutate:  func(t *testing.T) []byte { return corrupt(t, `"id":"bp-a"`, `"id":"bp-b"`) },
			wantErr: ErrInvalidIndexSignature,
		},
		{
			name: "artifact url repointed",
			mutate: func(t *testing.T) []byte {
				return corrupt(t, `/artifacts/blueprint/bp-a/1.0.0.json`, `/artifacts/blueprint/bp-a/9.9.9.json`)
			},
			wantErr: ErrInvalidIndexSignature,
		},
		{
			name:    "schema version",
			mutate:  func(t *testing.T) []byte { return corrupt(t, `"schemaVersion":1`, `"schemaVersion":2`) },
			wantErr: ErrInvalidIndexSignature,
		},
		{
			name:    "revocation reason",
			mutate:  func(t *testing.T) []byte { return corrupt(t, `withdrawn by publisher`, `withdrawn by publishes`) },
			wantErr: ErrInvalidIndexSignature,
		},
		{
			// The attacker's most valuable edit: strip the revocation and
			// leave a document that is still perfectly well-formed JSON.
			name: "revocation stripped",
			mutate: func(t *testing.T) []byte {
				t.Helper()
				revs, err := json.Marshal(d.Revocations)
				if err != nil {
					t.Fatalf("marshal revocations: %v", err)
				}
				return corrupt(t, `"revocations":`+string(revs)+`,`, ``)
			},
			wantErr: ErrInvalidIndexSignature,
		},
		{
			name: "field injected outside the signature's coverage",
			mutate: func(t *testing.T) []byte {
				return corrupt(t, `"revocations":[`, `"mirror":"http://evil.example","revocations":[`)
			},
			wantErr: ErrInvalidIndex,
		},
		{
			name:    "signature bytes",
			mutate:  func(t *testing.T) []byte { return flipSigByte(t, raw) },
			wantErr: ErrInvalidIndexSignature,
		},
		{
			name:    "truncated",
			mutate:  func(_ *testing.T) []byte { return raw[:len(raw)/2] },
			wantErr: ErrInvalidIndex,
		},
		{
			name:    "trailing data",
			mutate:  func(_ *testing.T) []byte { return append(append([]byte(nil), raw...), []byte(`{"entries":[]}`)...) },
			wantErr: ErrInvalidIndex,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Verify(tc.mutate(t), []string{fp})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(got, Document{}) {
				t.Fatalf("a failed verification must yield no document, got %+v", got)
			}
		})
	}
}

// flipSigByte mutates one base64 character of the signature itself.
func flipSigByte(t *testing.T, raw []byte) []byte {
	t.Helper()
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Signature == nil {
		t.Fatal("fixture is unsigned")
	}
	sig := []byte(doc.Signature.Sig)
	if sig[0] == 'A' {
		sig[0] = 'B'
	} else {
		sig[0] = 'A'
	}
	doc.Signature.Sig = string(sig)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

func TestVerify_UnsupportedSchema(t *testing.T) {
	priv, fp := testKey(t)
	d := testDoc()
	d.SchemaVersion = CurrentIndexSchema + 1
	raw := mustSign(t, d, priv)
	if _, err := Verify(raw, []string{fp}); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("err = %v, want ErrUnsupportedSchema", err)
	}
}

func TestVerify_UnknownFieldRefused(t *testing.T) {
	priv, fp := testKey(t)
	raw := mustSign(t, testDoc(), priv)
	injected := bytes.Replace(raw, []byte(`{"schemaVersion"`), []byte(`{"mirror":"http://evil.example","schemaVersion"`), 1)
	if _, err := Verify(injected, []string{fp}); !errors.Is(err, ErrInvalidIndex) {
		t.Fatalf("err = %v, want ErrInvalidIndex", err)
	}
}

// TestValidate_StructuralRules covers what a *validly signed* index still may
// not contain — a registry that signs nonsense does not get to publish it.
func TestValidate_StructuralRules(t *testing.T) {
	//nolint:govet // fieldalignment: test table; field order documents each case, not packing.
	tests := []struct {
		name string
		doc  Document
	}{
		{"unknown type", Document{SchemaVersion: 1, Entries: []hub.Entry{{Type: "widget", ID: "x", Version: "1", ArtifactURL: "/a.json"}}}},
		{"no id", Document{SchemaVersion: 1, Entries: []hub.Entry{{Type: hub.TypeBlueprint, Version: "1", ArtifactURL: "/a.json"}}}},
		{"no version", Document{SchemaVersion: 1, Entries: []hub.Entry{{Type: hub.TypeBlueprint, ID: "x", ArtifactURL: "/a.json"}}}},
		{"no artifact url", Document{SchemaVersion: 1, Entries: []hub.Entry{{Type: hub.TypeBlueprint, ID: "x", Version: "1"}}}},
		{"relative artifact url", Document{SchemaVersion: 1, Entries: []hub.Entry{{Type: hub.TypeBlueprint, ID: "x", Version: "1", ArtifactURL: "a.json"}}}},
		{"protocol relative artifact url", Document{SchemaVersion: 1, Entries: []hub.Entry{{Type: hub.TypeBlueprint, ID: "x", Version: "1", ArtifactURL: "//evil.example/a.json"}}}},
		{"traversing artifact url", Document{SchemaVersion: 1, Entries: []hub.Entry{{Type: hub.TypeBlueprint, ID: "x", Version: "1", ArtifactURL: "/artifacts/../../etc/passwd"}}}},
		{"id with a slash", Document{SchemaVersion: 1, Entries: []hub.Entry{{Type: hub.TypeBlueprint, ID: "a/b", Version: "1", ArtifactURL: "/a.json"}}}},
		{"duplicate entry", Document{SchemaVersion: 1, Entries: []hub.Entry{testEntry(hub.TypeBlueprint, "x", "1"), testEntry(hub.TypeBlueprint, "x", "1")}}},
		{"revocation naming nothing", Document{SchemaVersion: 1, Revocations: []Revocation{{Reason: "because"}}}},
		{"revocation without a reason", Document{SchemaVersion: 1, Revocations: []Revocation{{Type: hub.TypeBlueprint, ID: "x"}}}},
		{"revocation with an unknown type", Document{SchemaVersion: 1, Revocations: []Revocation{{Type: "widget", ID: "x", Reason: "because"}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(tc.doc); !errors.Is(err, ErrInvalidIndex) {
				t.Fatalf("Validate err = %v, want ErrInvalidIndex", err)
			}
			// And it can never be signed into existence either.
			priv, _ := testKey(t)
			if _, err := Sign(tc.doc, priv); err == nil {
				t.Fatal("Sign accepted a structurally invalid document")
			}
		})
	}
}

func TestRevocation_Matches(t *testing.T) {
	signed := testEntry(hub.TypePlugin, "pl-a", "2.1.0")
	signed.Signature = &blueprint.BundleSignature{PublicKeyFingerprint: "AABB"}
	unsigned := testEntry(hub.TypePlugin, "pl-b", "1.0.0")

	tests := []struct {
		name string
		rev  Revocation
		e    hub.Entry
		want bool
	}{
		{"exact version", Revocation{Type: hub.TypePlugin, ID: "pl-a", Version: "2.1.0", Reason: "r"}, signed, true},
		{"other version", Revocation{Type: hub.TypePlugin, ID: "pl-a", Version: "2.0.0", Reason: "r"}, signed, false},
		{"all versions of an id", Revocation{Type: hub.TypePlugin, ID: "pl-a", Reason: "r"}, signed, true},
		{"other type same id", Revocation{Type: hub.TypeBlueprint, ID: "pl-a", Reason: "r"}, signed, false},
		{"by signer", Revocation{SignerFingerprint: "aabb", Reason: "key compromised"}, signed, true},
		{"by signer, unsigned entry", Revocation{SignerFingerprint: "aabb", Reason: "key compromised"}, unsigned, false},
		{"by signer, wrong key", Revocation{SignerFingerprint: "ccdd", Reason: "key compromised"}, signed, false},
		{"signer plus id narrows", Revocation{SignerFingerprint: "aabb", Type: hub.TypePlugin, ID: "pl-a", Reason: "r"}, signed, true},
		{"signer plus other id", Revocation{SignerFingerprint: "aabb", Type: hub.TypePlugin, ID: "pl-z", Reason: "r"}, signed, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rev.Matches(tc.e); got != tc.want {
				t.Fatalf("Matches = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestHubIndex_HidesRevoked proves the projection the client is handed omits
// revoked entries while the signed document still records them (so the fact
// of the revocation stays published and auditable).
func TestHubIndex_HidesRevoked(t *testing.T) {
	d := testDoc()
	d.Revocations = []Revocation{{Type: hub.TypePlugin, ID: "pl-a", Reason: "malicious update"}}
	idx := d.HubIndex()
	if len(idx.Entries) != 1 || idx.Entries[0].ID != "bp-a" {
		t.Fatalf("client index = %+v, want only bp-a", idx.Entries)
	}
	if len(d.Entries) != 2 {
		t.Fatalf("the signed document must keep the revoked entry, got %d entries", len(d.Entries))
	}
	if idx.SchemaVersion != CurrentIndexSchema {
		t.Fatalf("schemaVersion = %d, want %d", idx.SchemaVersion, CurrentIndexSchema)
	}
}

// TestCanonicalBytes_IgnoresSignature proves the signature is not part of its
// own signed message (otherwise verification could never reproduce it).
func TestCanonicalBytes_IgnoresSignature(t *testing.T) {
	priv, _ := testKey(t)
	signed, err := Sign(testDoc(), priv)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	withSig, err := CanonicalBytes(signed)
	if err != nil {
		t.Fatalf("CanonicalBytes: %v", err)
	}
	stripped := signed
	stripped.Signature = nil
	withoutSig, err := CanonicalBytes(stripped)
	if err != nil {
		t.Fatalf("CanonicalBytes: %v", err)
	}
	if !bytes.Equal(withSig, withoutSig) {
		t.Fatal("canonical bytes must not depend on the signature field")
	}
}

// TestSign_NormalizesOrder proves two registries that published the same set
// in a different order produce byte-identical index files.
func TestSign_NormalizesOrder(t *testing.T) {
	priv, _ := testKey(t)
	a := testDoc()
	b := testDoc()
	b.Entries[0], b.Entries[1] = b.Entries[1], b.Entries[0]

	rawA := mustSign(t, a, priv)
	rawB := mustSign(t, b, priv)
	if !bytes.Equal(rawA, rawB) {
		t.Fatalf("index files differ by input order:\n%s\n%s", rawA, rawB)
	}
}
