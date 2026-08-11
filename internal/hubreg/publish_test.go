package hubreg

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/hub"
)

func testBundleJSON(t *testing.T, id string, priv ed25519.PrivateKey) []byte {
	t.Helper()
	bp := blueprint.Blueprint{
		BlueprintVersion: blueprint.CurrentBlueprintVersion,
		ID:               id,
		Name:             "Blueprint " + id,
		Description:      "a test blueprint",
	}
	bundle, err := blueprint.SignBundle(bp, priv)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	return raw
}

func testPluginJSON(t *testing.T, id, version string, priv ed25519.PrivateKey) []byte {
	t.Helper()
	m := hub.PluginManifest{
		ID: id, Name: "Plugin " + id, Version: version, APIVersion: "v1",
		Transport: "grpc", Endpoint: "/usr/libexec/" + id,
		ExtensionPoints: []string{"dashboardTile"}, Capabilities: []string{"netRead"},
	}
	art := hub.PluginArtifact{Manifest: m}
	if priv != nil {
		msg, err := hub.CanonicalManifestBytes(m)
		if err != nil {
			t.Fatalf("CanonicalManifestBytes: %v", err)
		}
		pub, _ := priv.Public().(ed25519.PublicKey)
		art.Signature = &blueprint.BundleSignature{
			Alg:                  blueprint.SignatureAlgEd25519,
			PublicKeyFingerprint: blueprint.Fingerprint(pub),
			PublicKey:            b64(pub),
			Sig:                  b64(ed25519.Sign(priv, msg)),
		}
	}
	raw, err := json.Marshal(art)
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}
	return raw
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// TestPublishIsIdempotent is AC4: the same artifact published twice yields one
// index entry, and the second publish reports no change.
func TestPublishIsIdempotent(t *testing.T) {
	pubKey, _ := testKey(t)
	raw := testBundleJSON(t, "bp-a", pubKey)

	sub1, err := BuildSubmission(raw, SubmissionOptions{Type: hub.TypeBlueprint, Version: "1.0.0", SubmittedAt: 100})
	if err != nil {
		t.Fatalf("BuildSubmission: %v", err)
	}
	// A second, independent publish of the same artifact — different
	// submission timestamp, and the input bytes re-formatted, to prove
	// idempotency is about the artifact and not about byte-identical files.
	var pretty bytes.Buffer
	if indentErr := json.Indent(&pretty, raw, "", "  "); indentErr != nil {
		t.Fatalf("indent: %v", indentErr)
	}
	sub2, err := BuildSubmission(pretty.Bytes(), SubmissionOptions{Type: hub.TypeBlueprint, Version: "1.0.0", SubmittedAt: 200})
	if err != nil {
		t.Fatalf("BuildSubmission (second): %v", err)
	}

	doc := Document{SchemaVersion: CurrentIndexSchema}
	doc, changed, err := AddEntry(doc, sub1)
	if err != nil || !changed {
		t.Fatalf("first AddEntry: changed=%v err=%v", changed, err)
	}
	doc, changed, err = AddEntry(doc, sub2)
	if err != nil {
		t.Fatalf("second AddEntry: %v", err)
	}
	if changed {
		t.Fatal("republishing the same artifact reported a change")
	}
	if len(doc.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(doc.Entries))
	}

	// And the published index file is byte-identical across the two runs.
	idxKey, _ := testKey(t)
	first := mustSign(t, mustAdd(t, Document{SchemaVersion: CurrentIndexSchema}, sub1), idxKey)
	second := mustSign(t, mustAdd(t, mustAdd(t, Document{SchemaVersion: CurrentIndexSchema}, sub1), sub2), idxKey)
	if !bytes.Equal(first, second) {
		t.Fatalf("index file changed on republish:\n%s\n%s", first, second)
	}
}

func mustAdd(t *testing.T, d Document, s Submission) Document {
	t.Helper()
	out, _, err := AddEntry(d, s)
	if err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	return out
}

// TestPublishConflict: a published version is immutable — different content
// under the same (type,id,version) is refused, not silently swapped.
func TestPublishConflict(t *testing.T) {
	keyA, _ := testKey(t)
	keyB, _ := testKey(t)
	subA, err := BuildSubmission(testPluginJSON(t, "pl-a", "2.0.0", keyA), SubmissionOptions{Type: hub.TypePlugin})
	if err != nil {
		t.Fatalf("BuildSubmission: %v", err)
	}
	subB, err := BuildSubmission(testPluginJSON(t, "pl-a", "2.0.0", keyB), SubmissionOptions{Type: hub.TypePlugin})
	if err != nil {
		t.Fatalf("BuildSubmission: %v", err)
	}
	doc := mustAdd(t, Document{SchemaVersion: CurrentIndexSchema}, subA)
	if _, _, addErr := AddEntry(doc, subB); !errors.Is(addErr, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", addErr)
	}
	// A new version is fine.
	subC, err := BuildSubmission(testPluginJSON(t, "pl-a", "2.1.0", keyB), SubmissionOptions{Type: hub.TypePlugin})
	if err != nil {
		t.Fatalf("BuildSubmission: %v", err)
	}
	doc = mustAdd(t, doc, subC)
	if len(doc.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(doc.Entries))
	}
}

func TestBuildSubmission_DerivesEntryFromArtifact(t *testing.T) {
	key, fp := testKey(t)
	sub, err := BuildSubmission(testPluginJSON(t, "pl-a", "2.0.0", key), SubmissionOptions{
		Type: hub.TypePlugin, Publisher: "acme",
	})
	if err != nil {
		t.Fatalf("BuildSubmission: %v", err)
	}
	e := sub.Entry
	if e.ID != "pl-a" || e.Version != "2.0.0" || e.Name != "Plugin pl-a" {
		t.Fatalf("entry identity = %+v", e)
	}
	if e.ArtifactURL != "/artifacts/plugin/pl-a/2.0.0.json" {
		t.Fatalf("artifactUrl = %q", e.ArtifactURL)
	}
	if e.SignerFingerprint() != fp {
		t.Fatalf("signer = %q, want %q", e.SignerFingerprint(), fp)
	}
	// The capability scope a browse UI shows before install comes from the
	// signed manifest, not from publisher-supplied metadata.
	if len(e.Capabilities) != 1 || e.Capabilities[0] != "netRead" {
		t.Fatalf("capabilities = %v", e.Capabilities)
	}
	if len(e.ExtensionPoints) != 1 || e.ExtensionPoints[0] != "dashboardTile" {
		t.Fatalf("extensionPoints = %v", e.ExtensionPoints)
	}
}

func TestBuildSubmission_SignsAnUnsignedArtifact(t *testing.T) {
	key, fp := testKey(t)
	unsigned := testPluginJSON(t, "pl-a", "2.0.0", nil)

	if _, err := BuildSubmission(unsigned, SubmissionOptions{Type: hub.TypePlugin}); !errors.Is(err, ErrInvalidSubmission) {
		t.Fatalf("an unsigned artifact with no key must be refused, got %v", err)
	}
	sub, err := BuildSubmission(unsigned, SubmissionOptions{Type: hub.TypePlugin, SigningKey: key})
	if err != nil {
		t.Fatalf("BuildSubmission: %v", err)
	}
	if sub.Entry.SignerFingerprint() != fp {
		t.Fatalf("signer = %q, want %q", sub.Entry.SignerFingerprint(), fp)
	}
	// Publishing unsigned is possible, but only deliberately.
	sub, err = BuildSubmission(unsigned, SubmissionOptions{Type: hub.TypePlugin, AllowUnsigned: true})
	if err != nil {
		t.Fatalf("BuildSubmission(allow unsigned): %v", err)
	}
	if sub.Entry.Signature != nil {
		t.Fatal("expected an unsigned entry")
	}
}

func TestBuildSubmission_Rejections(t *testing.T) {
	key, _ := testKey(t)
	tests := []struct {
		name string
		raw  []byte
		opts SubmissionOptions
	}{
		{"unknown type", testPluginJSON(t, "p", "1", key), SubmissionOptions{Type: "widget"}},
		{"blueprint without a catalog version", testBundleJSON(t, "bp", key), SubmissionOptions{Type: hub.TypeBlueprint}},
		{"version disagrees with the manifest", testPluginJSON(t, "p", "1", key), SubmissionOptions{Type: hub.TypePlugin, Version: "2"}},
		{"id that would escape the artifact tree", testPluginJSON(t, "..", "1", key), SubmissionOptions{Type: hub.TypePlugin}},
		{"not a bundle at all", []byte(`{"nope":true}`), SubmissionOptions{Type: hub.TypeBlueprint, Version: "1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildSubmission(tc.raw, tc.opts); !errors.Is(err, ErrInvalidSubmission) {
				t.Fatalf("err = %v, want ErrInvalidSubmission", err)
			}
		})
	}
}

// TestVerifySubmission_CatchesTamperedSubmission is the reviewer-side gate: a
// submission whose artifact was edited after signing, or whose entry
// advertises a signer the artifact does not carry, never reaches the index.
func TestVerifySubmission_CatchesTamperedSubmission(t *testing.T) {
	key, _ := testKey(t)
	sub, err := BuildSubmission(testPluginJSON(t, "pl-a", "2.0.0", key), SubmissionOptions{Type: hub.TypePlugin})
	if err != nil {
		t.Fatalf("BuildSubmission: %v", err)
	}

	t.Run("artifact edited after signing", func(t *testing.T) {
		tampered := sub
		var art hub.PluginArtifact
		if err := json.Unmarshal(sub.Artifact, &art); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		art.Manifest.Capabilities = append(art.Manifest.Capabilities, "netWrite") // scope escalation
		body, err := json.Marshal(art)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		tampered.Artifact = body
		if err := VerifySubmission(tampered); !errors.Is(err, ErrInvalidSubmission) {
			t.Fatalf("err = %v, want ErrInvalidSubmission", err)
		}
		if _, _, err := AddEntry(Document{SchemaVersion: CurrentIndexSchema}, tampered); !errors.Is(err, ErrInvalidSubmission) {
			t.Fatalf("AddEntry err = %v, want ErrInvalidSubmission", err)
		}
	})

	t.Run("entry advertises a different signer", func(t *testing.T) {
		lying := sub
		otherKey, _ := testKey(t)
		otherSub, err := BuildSubmission(testPluginJSON(t, "pl-a", "2.0.0", otherKey), SubmissionOptions{Type: hub.TypePlugin})
		if err != nil {
			t.Fatalf("BuildSubmission: %v", err)
		}
		lying.Entry.Signature = otherSub.Entry.Signature
		if err := VerifySubmission(lying); !errors.Is(err, ErrInvalidSubmission) {
			t.Fatalf("err = %v, want ErrInvalidSubmission", err)
		}
	})

	t.Run("artifact url repointed", func(t *testing.T) {
		moved := sub
		moved.Entry.ArtifactURL = "/artifacts/plugin/pl-a/9.9.9.json"
		if err := VerifySubmission(moved); !errors.Is(err, ErrInvalidSubmission) {
			t.Fatalf("err = %v, want ErrInvalidSubmission", err)
		}
	})
}

func TestParseSubmission_RoundTrip(t *testing.T) {
	key, _ := testKey(t)
	sub, err := BuildSubmission(testBundleJSON(t, "bp-a", key), SubmissionOptions{Type: hub.TypeBlueprint, Version: "1.0.0"})
	if err != nil {
		t.Fatalf("BuildSubmission: %v", err)
	}
	raw, err := json.Marshal(sub)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := ParseSubmission(raw)
	if err != nil {
		t.Fatalf("ParseSubmission: %v", err)
	}
	if got.Entry.ID != "bp-a" || got.Entry.Version != "1.0.0" {
		t.Fatalf("entry = %+v", got.Entry)
	}
	if _, err := ParseSubmission([]byte(`{"schemaVersion":99}`)); !errors.Is(err, ErrInvalidSubmission) {
		t.Fatalf("a future submission schema must be refused, got %v", err)
	}
}

// TestWriteArtifact_Idempotent is AC4's file-tree half: publishing twice
// writes one file and does not rewrite it; different bytes under a published
// path are a conflict, never an overwrite.
func TestWriteArtifact_Idempotent(t *testing.T) {
	root := t.TempDir()
	key, _ := testKey(t)
	sub, err := BuildSubmission(testPluginJSON(t, "pl-a", "2.0.0", key), SubmissionOptions{Type: hub.TypePlugin})
	if err != nil {
		t.Fatalf("BuildSubmission: %v", err)
	}
	path, changed, err := WriteArtifact(root, sub)
	if err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	if want := filepath.Join(root, "artifacts", "plugin", "pl-a", "2.0.0.json"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	before, err := os.ReadFile(path) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, changed, err = WriteArtifact(root, sub); err != nil || changed {
		t.Fatalf("second write: changed=%v err=%v", changed, err)
	}
	after, err := os.ReadFile(path) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("republish rewrote the artifact file")
	}

	otherKey, _ := testKey(t)
	conflicting, err := BuildSubmission(testPluginJSON(t, "pl-a", "2.0.0", otherKey), SubmissionOptions{Type: hub.TypePlugin})
	if err != nil {
		t.Fatalf("BuildSubmission: %v", err)
	}
	if _, _, err := WriteArtifact(root, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

// TestAddRevocation_Idempotent: revoking twice yields one record.
func TestAddRevocation_Idempotent(t *testing.T) {
	doc := Document{SchemaVersion: CurrentIndexSchema}
	rev := Revocation{Type: hub.TypePlugin, ID: "pl-a", Version: "2.0.0", Reason: "malicious update", At: 5}
	doc, changed, err := AddRevocation(doc, rev)
	if err != nil || !changed {
		t.Fatalf("first: changed=%v err=%v", changed, err)
	}
	doc, changed, err = AddRevocation(doc, rev)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if changed || len(doc.Revocations) != 1 {
		t.Fatalf("changed=%v revocations=%d, want false/1", changed, len(doc.Revocations))
	}
	if _, _, err := AddRevocation(doc, Revocation{Reason: "no target"}); !errors.Is(err, ErrInvalidIndex) {
		t.Fatalf("err = %v, want ErrInvalidIndex", err)
	}
}
