// index.go is the signed registry-index document: its wire shape, its
// canonical signing bytes, and the whole-document verification that either
// yields a complete index or nothing (T-2803 AC5).
//
// The document is a strict *superset* of hub.Index, which is what makes
// T-2803 AC1 structural rather than aspirational: the served JSON is this
// document, encoding/json ignores the additive `generatedAt`/`revocations`/
// `signature` keys when the existing client decodes it into hub.Index, and
// the entries themselves are literally []hub.Entry — the client's own type.
// A change to hub.Entry that this generator did not follow is a compile
// error here, not a silent format drift.

package hubreg

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/hub"
)

// CurrentIndexSchema is the schema version this package produces and accepts.
// It is deliberately *hub.CurrentIndexSchema* rather than an independent
// constant: the client refuses any index whose schemaVersion is not its own,
// so a generator with a version of its own could emit an index no client
// would load. Bumping the client's constant therefore forces a decision here.
const CurrentIndexSchema = hub.CurrentIndexSchema

// MaxIndexBytes bounds an index document Gate will read from the network,
// matching internal/hub's own 4 MiB index ceiling.
const MaxIndexBytes = 4 << 20

// DefaultArtifactBase is the path prefix under the registry root that
// published artifacts are laid out below. An entry's artifact URL is derived
// from it (ArtifactPath), never hand-written, so the index and the static
// file tree cannot disagree.
const DefaultArtifactBase = "/artifacts"

// Document is the registry index as published: `<registry>/index.json`.
//
// Field order is the JSON shape and is also the canonical signing order (see
// CanonicalBytes) — do not reorder.
//
//nolint:govet // fieldalignment: wire envelope; field order is the JSON shape and the signing order, not packing.
type Document struct {
	SchemaVersion int `json:"schemaVersion"`
	// GeneratedAt is the unix timestamp the index was signed at. It is inside
	// the signature, so it cannot be forged to make a stale catalog look
	// fresh; it is informational only — this client does not expire an index.
	GeneratedAt int64 `json:"generatedAt,omitempty"`
	// Entries is exactly the client's own catalog type. Revoked entries are
	// *kept* here (with a matching Revocations record) rather than deleted, so
	// the fact of a revocation is itself published and auditable; HubIndex
	// filters them out of what the client is shown.
	Entries []hub.Entry `json:"entries"`
	// Revocations withdraw already-published artifacts, or every artifact by a
	// compromised signer. They ride inside the signed document precisely so a
	// consumer can honour them with no further network access (AC3).
	Revocations []Revocation `json:"revocations,omitempty"`
	// Signature is an Ed25519 signature by the registry's index key over
	// CanonicalBytes(document-with-Signature-nil). It reuses T-1107's envelope
	// and primitive (blueprint.BundleSignature / VerifySignature) rather than
	// inventing a second signature format.
	Signature *blueprint.BundleSignature `json:"signature,omitempty"`
}

// Revocation withdraws artifacts from the catalog. Exactly one of the two
// addressing modes must be used:
//
//   - by artifact: Type+ID (+Version to scope it to one version; empty Version
//     revokes every version of that id).
//   - by signer: SignerFingerprint alone, which revokes every entry signed by
//     that key — the key-compromise case, where enumerating the affected
//     artifacts by hand is exactly what goes wrong under pressure.
//
//nolint:govet // fieldalignment: wire envelope; field order is the JSON shape and the signing order, not packing.
type Revocation struct {
	Type              hub.EntryType `json:"type,omitempty"`
	ID                string        `json:"id,omitempty"`
	Version           string        `json:"version,omitempty"`
	SignerFingerprint string        `json:"signerFingerprint,omitempty"`
	Reason            string        `json:"reason"`
	At                int64         `json:"at,omitempty"`
}

// Matches reports whether r revokes e.
func (r Revocation) Matches(e hub.Entry) bool {
	if r.SignerFingerprint != "" {
		fp := e.SignerFingerprint()
		if fp == "" || !strings.EqualFold(r.SignerFingerprint, fp) {
			return false
		}
		if r.ID == "" {
			return true
		}
	}
	if r.ID == "" || r.ID != e.ID {
		return false
	}
	if r.Type != "" && r.Type != e.Type {
		return false
	}
	if r.Version != "" && r.Version != e.Version {
		return false
	}
	return true
}

// key is a revocation's identity for idempotent insertion.
func (r Revocation) key() string {
	return strings.Join([]string{string(r.Type), r.ID, r.Version, strings.ToLower(r.SignerFingerprint)}, "\x00")
}

// IsRevoked reports whether any revocation in d withdraws e, and which one.
func (d Document) IsRevoked(e hub.Entry) (Revocation, bool) {
	for _, r := range d.Revocations {
		if r.Matches(e) {
			return r, true
		}
	}
	return Revocation{}, false
}

// HubIndex projects the document onto the exact shape internal/hub decodes,
// with revoked entries removed — a revoked artifact is not offered for
// install in the first place, and (see gate.go) is refused again if something
// reaches for it anyway.
func (d Document) HubIndex() hub.Index {
	idx := hub.Index{SchemaVersion: d.SchemaVersion, Entries: make([]hub.Entry, 0, len(d.Entries))}
	for _, e := range d.Entries {
		if _, revoked := d.IsRevoked(e); revoked {
			continue
		}
		idx.Entries = append(idx.Entries, e)
	}
	return idx
}

// CanonicalBytes returns the exact byte sequence an index signature is
// computed over: encoding/json's deterministic marshaling of the document
// with Signature cleared. Struct field order is fixed by Document's
// definition and Entries/Revocations are sorted by Sign, so two calls with
// equal content always produce identical bytes — the property verification
// depends on (the same convention blueprint.canonicalBlueprintBytes follows).
func CanonicalBytes(d Document) ([]byte, error) {
	d.Signature = nil
	b, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("hubreg: encoding index for signature: %w", err)
	}
	return b, nil
}

// Sign validates d, normalizes its ordering, and signs it with priv. The
// returned document is what gets written to index.json.
func Sign(d Document, priv ed25519.PrivateKey) (Document, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return Document{}, fmt.Errorf("hubreg: signing index: no Ed25519 private key")
	}
	if d.SchemaVersion == 0 {
		d.SchemaVersion = CurrentIndexSchema
	}
	d = normalize(d)
	if err := Validate(d); err != nil {
		return Document{}, err
	}
	msg, err := CanonicalBytes(d)
	if err != nil {
		return Document{}, err
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return Document{}, fmt.Errorf("hubreg: signing index: key has no Ed25519 public half")
	}
	d.Signature = &blueprint.BundleSignature{
		Alg:                  blueprint.SignatureAlgEd25519,
		PublicKeyFingerprint: blueprint.Fingerprint(pub),
		PublicKey:            base64.StdEncoding.EncodeToString(pub),
		Sig:                  base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg)),
	}
	return d, nil
}

// Verify parses and fully verifies raw index bytes against the operator's
// trusted index-signer fingerprints, returning the document only if every
// check passes. There is no partial result and no "verified except" state:
// any failure returns the zero Document (AC5).
//
// The checks, in order:
//
//  1. strict parse — unknown fields, trailing data and oversized bodies are
//     rejected, so nothing can ride along outside the signature's coverage;
//  2. a signature must be present (an unsigned index is refused, not
//     downgraded);
//  3. the signature must verify over the document's own canonical bytes;
//  4. the signing key must be one the operator configured;
//  5. the schema version must be this one;
//  6. the content must be structurally valid.
func Verify(raw []byte, trustedFingerprints []string) (Document, error) {
	if len(raw) > MaxIndexBytes {
		return Document{}, fmt.Errorf("%w: index is larger than %d bytes", ErrInvalidIndex, MaxIndexBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var d Document
	if err := dec.Decode(&d); err != nil {
		return Document{}, fmt.Errorf("%w: %w", ErrInvalidIndex, err)
	}
	if dec.More() {
		return Document{}, fmt.Errorf("%w: trailing data after the index document", ErrInvalidIndex)
	}
	if d.Signature == nil {
		return Document{}, ErrUnsignedIndex
	}
	msg, err := CanonicalBytes(d)
	if err != nil {
		return Document{}, err
	}
	verified, fingerprint, verr := blueprint.VerifySignature(d.Signature, msg)
	if verr != nil || !verified {
		return Document{}, fmt.Errorf("%w (signer %s)", ErrInvalidIndexSignature, d.Signature.PublicKeyFingerprint)
	}
	if !trustedSigner(fingerprint, trustedFingerprints) {
		return Document{}, fmt.Errorf("%w: %s", ErrUntrustedIndexSigner, fingerprint)
	}
	if d.SchemaVersion != CurrentIndexSchema {
		return Document{}, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedSchema, d.SchemaVersion, CurrentIndexSchema)
	}
	if err := Validate(d); err != nil {
		return Document{}, err
	}
	return d, nil
}

// trustedSigner reports whether fingerprint is in trusted (case-insensitive
// hex, matching blueprint.Fingerprint's lowercase output). An empty trusted
// set trusts nothing.
func trustedSigner(fingerprint string, trusted []string) bool {
	if fingerprint == "" {
		return false
	}
	for _, t := range trusted {
		if strings.EqualFold(strings.TrimSpace(t), fingerprint) {
			return true
		}
	}
	return false
}

// Validate checks a document's structure independently of its signature: a
// signed-but-nonsensical index is still refused.
func Validate(d Document) error {
	seen := make(map[string]struct{}, len(d.Entries))
	for _, e := range d.Entries {
		if err := validateEntry(e); err != nil {
			return err
		}
		k := entryKey(e)
		if _, dup := seen[k]; dup {
			return fmt.Errorf("%w: duplicate entry %s %s@%s", ErrInvalidIndex, e.Type, e.ID, e.Version)
		}
		seen[k] = struct{}{}
	}
	for _, r := range d.Revocations {
		if err := validateRevocation(r); err != nil {
			return err
		}
	}
	return nil
}

func validateEntry(e hub.Entry) error {
	if !hub.ValidType(e.Type) {
		return fmt.Errorf("%w: entry %q has unknown type %q", ErrInvalidIndex, e.ID, e.Type)
	}
	if err := validSlug("id", e.ID); err != nil {
		return err
	}
	if err := validSlug("version", e.Version); err != nil {
		return err
	}
	if err := validArtifactURL(e.ArtifactURL); err != nil {
		return fmt.Errorf("%w (entry %s %s@%s)", err, e.Type, e.ID, e.Version)
	}
	return nil
}

func validateRevocation(r Revocation) error {
	if r.ID == "" && r.SignerFingerprint == "" {
		return fmt.Errorf("%w: a revocation must name an artifact id or a signer fingerprint", ErrInvalidIndex)
	}
	if r.ID != "" {
		if err := validSlug("id", r.ID); err != nil {
			return err
		}
		if !hub.ValidType(r.Type) {
			return fmt.Errorf("%w: revocation of %q has unknown type %q", ErrInvalidIndex, r.ID, r.Type)
		}
		if r.Version != "" {
			if err := validSlug("version", r.Version); err != nil {
				return err
			}
		}
	}
	if strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("%w: revocation of %q has no reason", ErrInvalidIndex, r.ID+r.SignerFingerprint)
	}
	return nil
}

// validArtifactURL requires an artifact URL to be either an absolute path
// ("/artifacts/...") or an absolute http(s) URL. A *relative* reference is
// refused because resolution would then depend on which base a given consumer
// happened to use — and Gate's revocation matching resolves entry URLs to
// decide what a fetch is for. (The client separately refuses any URL that
// resolves off the registry host.)
func validArtifactURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("%w: entry has no artifactUrl", ErrInvalidIndex)
	}
	if strings.HasPrefix(raw, "//") {
		return fmt.Errorf("%w: artifactUrl %q is protocol-relative", ErrInvalidIndex, raw)
	}
	if strings.HasPrefix(raw, "/") {
		if strings.Contains(raw, "/../") || strings.HasSuffix(raw, "/..") {
			return fmt.Errorf("%w: artifactUrl %q traverses out of the registry root", ErrInvalidIndex, raw)
		}
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: unparsable artifactUrl %q", ErrInvalidIndex, raw)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: artifactUrl %q must be an absolute path or an absolute http(s) URL", ErrInvalidIndex, raw)
	}
	return nil
}

// validSlug bounds the identifier characters that reach a static file path.
// Artifact paths are derived from id and version (ArtifactPath), so anything
// that could escape a directory, or that a static host would treat specially,
// is refused at publish time rather than being sanitized away later.
func validSlug(field, s string) error {
	if s == "" {
		return fmt.Errorf("%w: entry has no %s", ErrInvalidIndex, field)
	}
	if len(s) > 128 {
		return fmt.Errorf("%w: %s %q is longer than 128 characters", ErrInvalidIndex, field, s)
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_' || r == '+':
		default:
			return fmt.Errorf("%w: %s %q contains %q (allowed: letters, digits, . - _ +)", ErrInvalidIndex, field, s, r)
		}
	}
	if s == "." || s == ".." {
		return fmt.Errorf("%w: %s %q is a path traversal", ErrInvalidIndex, field, s)
	}
	return nil
}

// ArtifactPath is the one place an entry's artifact URL comes from: a
// deterministic path under base derived from the artifact's own identity. No
// publisher supplies a URL by hand, so the index and the published file tree
// cannot disagree, and republishing the same artifact yields the same path
// (part of AC4's idempotency).
func ArtifactPath(base string, t hub.EntryType, id, version string) string {
	if base == "" {
		base = DefaultArtifactBase
	}
	if !strings.HasPrefix(base, "/") {
		base = "/" + base
	}
	return path.Join(base, string(t), id, version+".json")
}

func entryKey(e hub.Entry) string {
	return strings.Join([]string{string(e.Type), e.ID, e.Version}, "\x00")
}

// normalize sorts entries and revocations into a deterministic order so that
// two runs that published the same set produce byte-identical index files
// (idempotency is visible in the published artifact, not just in memory).
func normalize(d Document) Document {
	entries := append([]hub.Entry(nil), d.Entries...)
	sort.SliceStable(entries, func(i, j int) bool { return entryKey(entries[i]) < entryKey(entries[j]) })
	d.Entries = entries
	if len(d.Revocations) > 0 {
		revs := append([]Revocation(nil), d.Revocations...)
		sort.SliceStable(revs, func(i, j int) bool { return revs[i].key() < revs[j].key() })
		d.Revocations = revs
	}
	return d
}
