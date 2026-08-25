// publish.go is the publisher/reviewer half of the registry: turning an
// artifact file into a reviewable submission, and folding a reviewed
// submission into the signed index idempotently (T-2803 AC4).
//
// The two halves are separate on purpose. `vnproxctl hub publish` runs on the
// *publisher's* machine with the publisher's own signing key and produces a
// submission file; `vnproxctl hub index` runs on the *registry's* side, after
// a human review, with the registry's index key. Nothing here submits over a
// network: a submission is a file, and "submitting" it is opening a pull
// request against the registry repository (docs/hub-registry.md). That is
// what keeps "reviewed before indexing" a property of the process rather than
// a promise in a comment.

package hubreg

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/hub"
)

// CurrentSubmissionSchema is the submission-file schema version.
const CurrentSubmissionSchema = 1

// Submission is one reviewable proposal: the catalog entry the registry would
// publish, plus the exact artifact bytes it points at. Artifact is stored
// verbatim so that what a reviewer reads, what gets hashed into the entry's
// signature, and what gets written into the published tree are the same
// bytes.
//
//nolint:govet // fieldalignment: wire envelope; field order is the JSON shape, not packing.
type Submission struct {
	SchemaVersion int             `json:"schemaVersion"`
	SubmittedAt   int64           `json:"submittedAt,omitempty"`
	Entry         hub.Entry       `json:"entry"`
	Artifact      json.RawMessage `json:"artifact"`
}

// SubmissionOptions are the publisher-supplied parts of a submission: the
// things that cannot be derived from the artifact itself.
type SubmissionOptions struct {
	// Type is the artifact kind being published (required).
	Type hub.EntryType
	// Version is the catalog version. Required for a blueprint (a Blueprint
	// carries no user-facing version of its own); for a plugin it defaults to
	// the manifest's version and must match it if given.
	Version string
	// Publisher and Description are display metadata for the catalog.
	Publisher   string
	Description string
	// ArtifactBase is the path prefix artifact URLs are derived under
	// (defaults to DefaultArtifactBase).
	ArtifactBase string
	// SigningKey signs the artifact. When nil the artifact must already carry
	// a valid signature, or AllowUnsigned must be set.
	SigningKey ed25519.PrivateKey
	// AllowUnsigned permits publishing an artifact with no signature at all.
	// Off by default: an unsigned artifact is installable only behind the
	// operator's explicit trustUnsigned step, so publishing one is a
	// deliberate act, not an accident of forgetting --key.
	AllowUnsigned bool
	// SubmittedAt is the recorded submission time (0 omits it).
	SubmittedAt int64
}

// BuildSubmission parses an artifact file, signs it if asked to, derives the
// catalog entry from the artifact's own identity, and returns the submission.
// The artifact is re-encoded canonically, so two submissions of the same
// artifact are byte-identical regardless of how the input file was formatted
// — the property AC4's idempotency rests on.
func BuildSubmission(raw []byte, opts SubmissionOptions) (Submission, error) {
	switch opts.Type {
	case hub.TypeBlueprint:
		return buildBlueprintSubmission(raw, opts)
	case hub.TypePlugin:
		return buildPluginSubmission(raw, opts)
	default:
		return Submission{}, fmt.Errorf("%w: unknown artifact type %q", ErrInvalidSubmission, opts.Type)
	}
}

func buildBlueprintSubmission(raw []byte, opts SubmissionOptions) (Submission, error) {
	var bundle blueprint.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return Submission{}, fmt.Errorf("%w: parsing blueprint bundle: %w", ErrInvalidSubmission, err)
	}
	if bundle.BundleVersion != blueprint.CurrentBundleVersion {
		return Submission{}, fmt.Errorf("%w: bundle version %d, want %d", ErrInvalidSubmission, bundle.BundleVersion, blueprint.CurrentBundleVersion)
	}
	if opts.SigningKey != nil {
		signed, err := blueprint.SignBundle(bundle.Blueprint, opts.SigningKey)
		if err != nil {
			return Submission{}, fmt.Errorf("hubreg: signing blueprint bundle: %w", err)
		}
		bundle = signed
	}
	if err := checkArtifactSignature(bundle.Signature, opts.AllowUnsigned); err != nil {
		return Submission{}, err
	}
	if bundle.Signature != nil {
		verified, _, err := blueprint.VerifyBundle(bundle)
		if err != nil || !verified {
			return Submission{}, fmt.Errorf("%w: the bundle's own signature does not verify", ErrInvalidSubmission)
		}
	}
	if opts.Version == "" {
		return Submission{}, fmt.Errorf("%w: a blueprint submission needs an explicit catalog version", ErrInvalidSubmission)
	}
	entry := hub.Entry{
		Signature:   bundle.Signature,
		Type:        hub.TypeBlueprint,
		ID:          bundle.Blueprint.ID,
		Name:        bundle.Blueprint.Name,
		Version:     opts.Version,
		Publisher:   opts.Publisher,
		Description: firstNonEmpty(opts.Description, bundle.Blueprint.Description),
	}
	return finishSubmission(entry, bundle, opts)
}

func buildPluginSubmission(raw []byte, opts SubmissionOptions) (Submission, error) {
	var art hub.PluginArtifact
	if err := json.Unmarshal(raw, &art); err != nil {
		return Submission{}, fmt.Errorf("%w: parsing plugin artifact: %w", ErrInvalidSubmission, err)
	}
	if art.Manifest.ID == "" {
		return Submission{}, fmt.Errorf("%w: plugin artifact has no manifest id", ErrInvalidSubmission)
	}
	msg, err := hub.CanonicalManifestBytes(art.Manifest)
	if err != nil {
		return Submission{}, fmt.Errorf("hubreg: canonicalizing plugin manifest: %w", err)
	}
	if opts.SigningKey != nil {
		pub, ok := opts.SigningKey.Public().(ed25519.PublicKey)
		if !ok {
			return Submission{}, fmt.Errorf("hubreg: signing key has no Ed25519 public half")
		}
		art.Signature = &blueprint.BundleSignature{
			Alg:                  blueprint.SignatureAlgEd25519,
			PublicKeyFingerprint: blueprint.Fingerprint(pub),
			PublicKey:            base64.StdEncoding.EncodeToString(pub),
			Sig:                  base64.StdEncoding.EncodeToString(ed25519.Sign(opts.SigningKey, msg)),
		}
	}
	if err := checkArtifactSignature(art.Signature, opts.AllowUnsigned); err != nil {
		return Submission{}, err
	}
	if art.Signature != nil {
		verified, _, verr := blueprint.VerifySignature(art.Signature, msg)
		if verr != nil || !verified {
			return Submission{}, fmt.Errorf("%w: the plugin artifact's own signature does not verify", ErrInvalidSubmission)
		}
	}
	version := opts.Version
	switch {
	case version == "":
		version = art.Manifest.Version
	case art.Manifest.Version != "" && version != art.Manifest.Version:
		return Submission{}, fmt.Errorf("%w: --version %q disagrees with the manifest's %q", ErrInvalidSubmission, version, art.Manifest.Version)
	}
	entry := hub.Entry{
		Signature:       art.Signature,
		Type:            hub.TypePlugin,
		ID:              art.Manifest.ID,
		Name:            art.Manifest.Name,
		Version:         version,
		Publisher:       opts.Publisher,
		Description:     opts.Description,
		Transport:       art.Manifest.Transport,
		Capabilities:    append([]string(nil), art.Manifest.Capabilities...),
		ExtensionPoints: append([]string(nil), art.Manifest.ExtensionPoints...),
	}
	return finishSubmission(entry, art, opts)
}

// checkArtifactSignature enforces the "signed unless explicitly allowed"
// publisher rule.
func checkArtifactSignature(sig *blueprint.BundleSignature, allowUnsigned bool) error {
	if sig == nil && !allowUnsigned {
		return fmt.Errorf("%w: artifact is unsigned (sign it, or publish it deliberately as unsigned)", ErrInvalidSubmission)
	}
	return nil
}

// finishSubmission derives the artifact URL, re-encodes the artifact
// canonically and validates the resulting entry.
func finishSubmission(entry hub.Entry, artifact any, opts SubmissionOptions) (Submission, error) {
	if err := validSlug("id", entry.ID); err != nil {
		return Submission{}, fmt.Errorf("%w: %w", ErrInvalidSubmission, err)
	}
	if err := validSlug("version", entry.Version); err != nil {
		return Submission{}, fmt.Errorf("%w: %w", ErrInvalidSubmission, err)
	}
	entry.ArtifactURL = ArtifactPath(opts.ArtifactBase, entry.Type, entry.ID, entry.Version)
	if err := validateEntry(entry); err != nil {
		return Submission{}, fmt.Errorf("%w: %w", ErrInvalidSubmission, err)
	}
	body, err := json.Marshal(artifact)
	if err != nil {
		return Submission{}, fmt.Errorf("hubreg: encoding artifact: %w", err)
	}
	return Submission{
		SchemaVersion: CurrentSubmissionSchema,
		SubmittedAt:   opts.SubmittedAt,
		Entry:         entry,
		Artifact:      body,
	}, nil
}

// ParseSubmission decodes and checks a submission file.
func ParseSubmission(raw []byte) (Submission, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s Submission
	if err := dec.Decode(&s); err != nil {
		return Submission{}, fmt.Errorf("%w: %w", ErrInvalidSubmission, err)
	}
	if s.SchemaVersion != CurrentSubmissionSchema {
		return Submission{}, fmt.Errorf("%w: schema version %d, want %d", ErrInvalidSubmission, s.SchemaVersion, CurrentSubmissionSchema)
	}
	if err := VerifySubmission(s); err != nil {
		return Submission{}, err
	}
	return s, nil
}

// VerifySubmission re-checks, on the registry's side, everything the
// publisher's tooling asserted: that the entry is well-formed, that its
// artifact URL is the derived one, that the artifact's identity agrees with
// the entry, and that the artifact's own signature verifies and is the one
// the entry advertises. A reviewer reads the diff; this catches what reading
// a diff does not.
func VerifySubmission(s Submission) error {
	if err := validateEntry(s.Entry); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidSubmission, err)
	}
	base := artifactBaseOf(s.Entry.ArtifactURL, s.Entry.Type, s.Entry.ID, s.Entry.Version)
	if want := ArtifactPath(base, s.Entry.Type, s.Entry.ID, s.Entry.Version); want != s.Entry.ArtifactURL {
		return fmt.Errorf("%w: artifactUrl %q is not the derived path %q", ErrInvalidSubmission, s.Entry.ArtifactURL, want)
	}
	switch s.Entry.Type {
	case hub.TypeBlueprint:
		var bundle blueprint.Bundle
		if err := json.Unmarshal(s.Artifact, &bundle); err != nil {
			return fmt.Errorf("%w: parsing blueprint bundle: %w", ErrInvalidSubmission, err)
		}
		if bundle.Blueprint.ID != s.Entry.ID {
			return fmt.Errorf("%w: bundle id %q disagrees with entry id %q", ErrInvalidSubmission, bundle.Blueprint.ID, s.Entry.ID)
		}
		if err := sameSignature(bundle.Signature, s.Entry.Signature); err != nil {
			return err
		}
		if bundle.Signature != nil {
			verified, _, err := blueprint.VerifyBundle(bundle)
			if err != nil || !verified {
				return fmt.Errorf("%w: the bundle's signature does not verify", ErrInvalidSubmission)
			}
		}
	case hub.TypePlugin:
		var art hub.PluginArtifact
		if err := json.Unmarshal(s.Artifact, &art); err != nil {
			return fmt.Errorf("%w: parsing plugin artifact: %w", ErrInvalidSubmission, err)
		}
		if art.Manifest.ID != s.Entry.ID {
			return fmt.Errorf("%w: manifest id %q disagrees with entry id %q", ErrInvalidSubmission, art.Manifest.ID, s.Entry.ID)
		}
		if err := sameSignature(art.Signature, s.Entry.Signature); err != nil {
			return err
		}
		if art.Signature != nil {
			msg, err := hub.CanonicalManifestBytes(art.Manifest)
			if err != nil {
				return fmt.Errorf("hubreg: canonicalizing plugin manifest: %w", err)
			}
			verified, _, verr := blueprint.VerifySignature(art.Signature, msg)
			if verr != nil || !verified {
				return fmt.Errorf("%w: the plugin artifact's signature does not verify", ErrInvalidSubmission)
			}
		}
	default:
		return fmt.Errorf("%w: unknown artifact type %q", ErrInvalidSubmission, s.Entry.Type)
	}
	return nil
}

// artifactBaseOf recovers the base prefix an artifact URL was derived under,
// so VerifySubmission can re-derive the path without hard-coding one base.
func artifactBaseOf(artifactURL string, t hub.EntryType, id, version string) string {
	suffix := ArtifactPath("/", t, id, version)
	if !strings.HasSuffix(artifactURL, suffix) {
		return ""
	}
	base := strings.TrimSuffix(artifactURL, suffix)
	if base == "" {
		return "/"
	}
	return base
}

// sameSignature requires an entry's advertised signature to be exactly the
// artifact's own — a catalog that displays one signer while shipping another
// artifact is how a "vetted" badge gets pointed at the wrong bytes.
func sameSignature(artifact, entry *blueprint.BundleSignature) error {
	switch {
	case artifact == nil && entry == nil:
		return nil
	case artifact == nil || entry == nil:
		return fmt.Errorf("%w: entry and artifact disagree about whether the artifact is signed", ErrInvalidSubmission)
	case *artifact != *entry:
		return fmt.Errorf("%w: the entry's signature is not the artifact's own", ErrInvalidSubmission)
	}
	return nil
}

// AddEntry folds a reviewed submission into d, idempotently: publishing the
// same artifact twice yields one entry and reports changed=false the second
// time (AC4). Publishing *different* content under an already-published
// (type,id,version) is ErrConflict — a published version is immutable, so a
// change means a new version, never a silent swap under installations that
// already fetched the old one.
func AddEntry(d Document, s Submission) (Document, bool, error) {
	if err := VerifySubmission(s); err != nil {
		return Document{}, false, err
	}
	for _, e := range d.Entries {
		if entryKey(e) != entryKey(s.Entry) {
			continue
		}
		if !sameEntry(e, s.Entry) {
			return Document{}, false, fmt.Errorf("%w: %s %s@%s", ErrConflict, s.Entry.Type, s.Entry.ID, s.Entry.Version)
		}
		// Identical: keep the document exactly as it was.
		return normalize(d), false, nil
	}
	out := normalize(Document{
		SchemaVersion: d.SchemaVersion,
		GeneratedAt:   d.GeneratedAt,
		Entries:       append(append([]hub.Entry(nil), d.Entries...), s.Entry),
		Revocations:   append([]Revocation(nil), d.Revocations...),
	})
	if err := Validate(out); err != nil {
		return Document{}, false, err
	}
	return out, true, nil
}

// AddRevocation folds a revocation into d, idempotently: revoking the same
// thing twice yields one record.
func AddRevocation(d Document, r Revocation) (Document, bool, error) {
	if err := validateRevocation(r); err != nil {
		return Document{}, false, err
	}
	for _, existing := range d.Revocations {
		if existing.key() == r.key() {
			return normalize(d), false, nil
		}
	}
	out := normalize(Document{
		SchemaVersion: d.SchemaVersion,
		GeneratedAt:   d.GeneratedAt,
		Entries:       append([]hub.Entry(nil), d.Entries...),
		Revocations:   append(append([]Revocation(nil), d.Revocations...), r),
	})
	return out, true, nil
}

// sameEntry compares two catalog entries by value (including the signature
// they advertise).
func sameEntry(a, b hub.Entry) bool {
	if (a.Signature == nil) != (b.Signature == nil) {
		return false
	}
	if a.Signature != nil && *a.Signature != *b.Signature {
		return false
	}
	if !equalStrings(a.Capabilities, b.Capabilities) || !equalStrings(a.ExtensionPoints, b.ExtensionPoints) {
		return false
	}
	return a.Type == b.Type &&
		a.ID == b.ID &&
		a.Name == b.Name &&
		a.Version == b.Version &&
		a.Publisher == b.Publisher &&
		a.Description == b.Description &&
		a.ArtifactURL == b.ArtifactURL &&
		a.Transport == b.Transport &&
		a.AutomatedChecksPassed == b.AutomatedChecksPassed
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// WriteArtifact writes a submission's artifact bytes into the published file
// tree rooted at root, at the path the entry's artifact URL names. It is
// idempotent in the same sense AddEntry is: identical bytes already present
// are left alone (changed=false); different bytes under the same path are
// ErrConflict rather than an overwrite.
func WriteArtifact(root string, s Submission) (string, bool, error) {
	rel := strings.TrimPrefix(s.Entry.ArtifactURL, "/")
	if strings.Contains(s.Entry.ArtifactURL, "://") {
		return "", false, fmt.Errorf("%w: entry names an off-tree absolute artifact URL %q", ErrInvalidSubmission, s.Entry.ArtifactURL)
	}
	dest := filepath.Join(root, filepath.FromSlash(rel))
	cleanRoot := filepath.Clean(root)
	if !strings.HasPrefix(dest, cleanRoot+string(filepath.Separator)) {
		return "", false, fmt.Errorf("%w: artifact path %q escapes the registry root", ErrInvalidSubmission, dest)
	}
	existing, err := os.ReadFile(dest) //nolint:gosec // dest is derived from a validated slug under an operator-supplied root.
	switch {
	case err == nil && bytes.Equal(existing, s.Artifact):
		return dest, false, nil
	case err == nil:
		return "", false, fmt.Errorf("%w: %s already holds different artifact bytes", ErrConflict, dest)
	case !errors.Is(err, os.ErrNotExist):
		return "", false, fmt.Errorf("hubreg: reading %s: %w", dest, err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", false, fmt.Errorf("hubreg: creating artifact directory for %s: %w", dest, err)
	}
	if err := os.WriteFile(dest, s.Artifact, 0o644); err != nil { //nolint:gosec // a published artifact is world-readable static hosting content.
		return "", false, fmt.Errorf("hubreg: writing %s: %w", dest, err)
	}
	return dest, true, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
