// SPDX-License-Identifier: Apache-2.0

package verify

// sign.go turns a Report into the on-disk artifact: a signed, timestamped
// JSON document that a reader who was not present at the run can check.
//
// The signature is Ed25519 over the report's exact serialized bytes, using
// internal/blueprint's verification path rather than a second copy of the
// crypto — blueprint's own doc comment asks any other signed artifact in this
// repository to verify through VerifySignature, and a validation report is
// exactly that.
//
// What the signature does and does not mean is worth stating plainly, because
// the failure mode of a signed artifact is a reader assuming more than it
// says. The key material is carried *in* the document, so a verified
// signature means "these bytes are the bytes that were signed" — integrity —
// and says nothing at all about who signed them unless the reader
// independently trusts the fingerprint. That is the same contract
// blueprint bundles ship with, and it is the right one here: the threat this
// artifact needs to resist is a report that was edited after the run (by
// accident far more often than by malice), not a forged provenance claim.
//
// AC5 is "a byte flipped anywhere in it fails verification", and the word
// *anywhere* is why this file does more than sign the report. A signature
// over the semantic content would happily verify a document whose whitespace
// had been rewritten, whose fields had been reordered, or which had grown a
// key the parser ignores. So the artifact is written in exactly one canonical
// form, and ParseSignedReport rejects any document that is not byte-identical
// to the canonical encoding of what it parsed. Between the two rules, every
// byte in the file is load-bearing.

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/blueprint"
)

// ArtifactVersion is the envelope's schema version.
const ArtifactVersion = 1

// ErrTampered is returned when a report artifact does not verify — because
// its signature does not match, or because its bytes are not the canonical
// encoding of its own content.
var ErrTampered = errors.New("verify: report artifact does not verify")

// signedArtifact is the on-disk envelope.
//
// Report is a json.RawMessage rather than a Report so that verification runs
// over the bytes *as they appear in the file*, not over a re-encoding of the
// decoded value. Decoding and re-encoding would normalise away exactly the
// differences AC5 requires to be fatal.
//
//nolint:govet // fieldalignment: on-disk field order.
type signedArtifact struct {
	ArtifactVersion int                       `json:"artifactVersion"`
	Signature       blueprint.BundleSignature `json:"signature"`
	Report          json.RawMessage           `json:"report"`
}

// SignReport encodes rep and signs it with priv, returning the artifact
// bytes.
//
// The output is compact single-line JSON. That is not an oversight: it is the
// canonical form ParseSignedReport requires, and a canonical form with
// optional indentation is not canonical. `jq .` renders it for a human, and
// the CLI's own -o json output is the pretty one.
func SignReport(rep Report, priv ed25519.PrivateKey) ([]byte, error) {
	if priv == nil {
		return nil, fmt.Errorf("verify: signing a report needs a private key")
	}
	if err := rep.Validate(); err != nil {
		// Signing a malformed report would produce a document that verifies
		// perfectly and means nothing.
		return nil, fmt.Errorf("verify: refusing to sign a malformed report: %w", err)
	}
	payload, err := json.Marshal(rep)
	if err != nil {
		return nil, fmt.Errorf("verify: encoding report for signature: %w", err)
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("verify: signing key has no Ed25519 public half")
	}
	art := signedArtifact{
		ArtifactVersion: ArtifactVersion,
		Signature: blueprint.BundleSignature{
			Alg:                  blueprint.SignatureAlgEd25519,
			PublicKeyFingerprint: blueprint.Fingerprint(pub),
			PublicKey:            base64.StdEncoding.EncodeToString(pub),
			Sig:                  base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload)),
		},
		Report: payload,
	}
	out, err := json.Marshal(art)
	if err != nil {
		return nil, fmt.Errorf("verify: encoding signed report artifact: %w", err)
	}
	return out, nil
}

// ParseSignedReport parses and verifies an artifact written by SignReport.
//
// It fails, with ErrTampered, on any of:
//
//   - a document that is not valid JSON, or carries a member the envelope
//     does not define;
//   - a document whose bytes are not the canonical encoding of its own
//     content (a re-indented file, a reordered member, an added space);
//   - a signature that does not verify over the report bytes;
//   - a report that does not satisfy Report.Validate.
//
// The canonical-form rule is what makes AC5's "anywhere" true. A signature
// alone leaves the whitespace between members unprotected, and a report whose
// formatting can be changed without detection is one whose formatting can be
// used to hide a change.
func ParseSignedReport(data []byte) (Report, string, error) {
	var art signedArtifact
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&art); err != nil {
		return Report{}, "", fmt.Errorf("%w: parsing artifact: %w", ErrTampered, err)
	}
	if dec.More() {
		return Report{}, "", fmt.Errorf("%w: artifact carries trailing content after the envelope", ErrTampered)
	}
	if art.ArtifactVersion != ArtifactVersion {
		return Report{}, "", fmt.Errorf("%w: artifactVersion %d, this build reads %d", ErrTampered, art.ArtifactVersion, ArtifactVersion)
	}

	// Canonical-form check. json.Marshal compacts the embedded RawMessage, so
	// re-encoding what we just decoded reproduces the file byte for byte iff
	// the file was in canonical form to begin with.
	recoded, err := json.Marshal(art)
	if err != nil {
		return Report{}, "", fmt.Errorf("%w: re-encoding artifact: %w", ErrTampered, err)
	}
	if string(recoded) != string(data) {
		return Report{}, "", fmt.Errorf("%w: artifact is not in canonical form (%d bytes on disk, %d canonical) — it has been reformatted or edited since it was signed",
			ErrTampered, len(data), len(recoded))
	}

	sig := art.Signature
	verified, fingerprint, err := blueprint.VerifySignature(&sig, art.Report)
	if err != nil || !verified {
		return Report{}, fingerprint, fmt.Errorf("%w: signature over the report does not verify: %v", ErrTampered, err)
	}

	var rep Report
	if err := json.Unmarshal(art.Report, &rep); err != nil {
		return Report{}, fingerprint, fmt.Errorf("%w: decoding the verified report: %w", ErrTampered, err)
	}
	if err := rep.Validate(); err != nil {
		// A signed report that does not satisfy its own invariants is a
		// correctly-signed lie; it is rejected here rather than handed on.
		return Report{}, fingerprint, fmt.Errorf("verify: artifact signature verifies but the report inside it is malformed: %w", err)
	}
	return rep, fingerprint, nil
}

// EphemeralSigningKey generates a throwaway keypair.
//
// It is what the CLI uses when no --sign-key is given, and the resulting
// artifact is honest about what it is worth: the signature still detects any
// later edit (which is the property a report needs while it is being mailed
// around), and it still carries no provenance claim, because the key that
// made it did not outlive the process. An operator who wants an attributable
// report points --sign-key at a key whose fingerprint the reader already
// knows.
func EphemeralSigningKey() (ed25519.PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, fmt.Errorf("verify: generating an ephemeral signing key: %w", err)
	}
	return priv, nil
}
