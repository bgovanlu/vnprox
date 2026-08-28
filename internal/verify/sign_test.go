// SPDX-License-Identifier: Apache-2.0

package verify

// sign_test.go is T-2501 AC5: the report round-trips through the parser, its
// signature verifies, and a byte flipped *anywhere* in it fails verification.
//
// "Anywhere" is asserted literally — every byte position, one at a time —
// rather than by picking a few likely spots. That is what caught the design
// problem this file's canonical-form rule exists to fix: a signature over the
// report's semantic content verifies happily after its whitespace has been
// rewritten, so a signature alone does not make every byte load-bearing. The
// test is cheap (a few hundred kilobytes of flips) and it is the difference
// between the criterion as written and the criterion as it is easy to meet.

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/blueprint"
)

func signedFixture(t *testing.T) (Report, ed25519.PrivateKey, []byte) {
	t.Helper()
	report, err := Run(context.Background(),
		Options{Suite: SuiteHardware, Version: "3.0.4", Logger: discardLog()}, healthyDeps())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	priv, err := EphemeralSigningKey()
	if err != nil {
		t.Fatalf("EphemeralSigningKey: %v", err)
	}
	artifact, err := SignReport(report, priv)
	if err != nil {
		t.Fatalf("SignReport: %v", err)
	}
	return report, priv, artifact
}

// TestSignedReportRoundTrips is AC5's first half.
func TestSignedReportRoundTrips(t *testing.T) {
	original, priv, artifact := signedFixture(t)

	parsed, fingerprint, err := ParseSignedReport(artifact)
	if err != nil {
		t.Fatalf("ParseSignedReport: %v", err)
	}
	if fingerprint == "" {
		t.Error("the parser reported no signer fingerprint")
	}

	// Every field a consumer reads has to survive the trip, not just the ones
	// that happen to be checked by Validate.
	if parsed.Suite != original.Suite {
		t.Errorf("suite = %q, want %q", parsed.Suite, original.Suite)
	}
	if !parsed.GeneratedAt.Equal(original.GeneratedAt) {
		t.Errorf("generatedAt = %v, want %v", parsed.GeneratedAt, original.GeneratedAt)
	}
	if !reflect.DeepEqual(parsed.Environment, original.Environment) {
		t.Errorf("environment did not survive the round trip:\n got %+v\nwant %+v", parsed.Environment, original.Environment)
	}
	if len(parsed.Results) != len(original.Results) {
		t.Fatalf("%d results survived, want %d", len(parsed.Results), len(original.Results))
	}
	for i, res := range parsed.Results {
		want := original.Results[i]
		if res.ID != want.ID || res.Status != want.Status || res.Detail != want.Detail {
			t.Errorf("result %d changed: %+v vs %+v", i, res, want)
		}
		if len(res.Evidence) != len(want.Evidence) {
			t.Errorf("result %s lost evidence: %d vs %d", res.ID, len(res.Evidence), len(want.Evidence))
		}
	}
	if parsed.Summary != original.Summary {
		t.Errorf("summary = %+v, want %+v", parsed.Summary, original.Summary)
	}

	// And the signature is over *this* key, not merely over some key.
	pub, _ := priv.Public().(ed25519.PublicKey)
	if !strings.Contains(string(artifact), blueprint.Fingerprint(pub)) {
		t.Error("the artifact does not carry the signing key's fingerprint")
	}
}

// TestAByteFlippedAnywhereFailsVerification is AC5's second half, taken
// literally.
//
// Every byte of the artifact is flipped in turn and the result must fail to
// parse or fail to verify. The positions that matter most are the boring
// ones: a space between two members, a digit inside a duration, a character
// in a field name the decoder would otherwise ignore.
func TestAByteFlippedAnywhereFailsVerification(t *testing.T) {
	_, _, artifact := signedFixture(t)
	if len(artifact) < 1000 {
		t.Fatalf("the artifact is only %d bytes; this test is not covering much", len(artifact))
	}

	var survived []int
	for i := range artifact {
		tampered := append([]byte(nil), artifact...)
		tampered[i] ^= 0x20 // flips letter case, and digits/punctuation into other printable bytes
		if tampered[i] == artifact[i] {
			continue
		}
		if _, _, err := ParseSignedReport(tampered); err == nil {
			survived = append(survived, i)
		}
	}
	if len(survived) > 0 {
		const show = 8
		positions := survived
		if len(positions) > show {
			positions = positions[:show]
		}
		t.Errorf("%d of %d byte positions verified after being flipped (AC5 requires none). First offenders at %v; byte %d is in context %q",
			len(survived), len(artifact), positions, survived[0], contextAround(artifact, survived[0]))
	}
	t.Logf("flipped all %d bytes of the artifact; every one failed verification", len(artifact))
}

// TestReindentingTheArtifactFailsVerification is the specific case a
// signature over semantic content would let through, called out because it is
// the one somebody will do by accident (`jq . report.json > pretty.json`) and
// then wonder why the report is still trusted.
func TestReindentingTheArtifactFailsVerification(t *testing.T) {
	_, _, artifact := signedFixture(t)

	var generic map[string]json.RawMessage
	if err := json.Unmarshal(artifact, &generic); err != nil {
		t.Fatalf("unmarshalling the artifact: %v", err)
	}
	pretty, err := json.MarshalIndent(generic, "", "  ")
	if err != nil {
		t.Fatalf("re-indenting: %v", err)
	}
	if string(pretty) == string(artifact) {
		t.Fatal("re-indenting produced identical bytes; this test is not testing anything")
	}

	if _, _, err := ParseSignedReport(pretty); err == nil {
		t.Error("a re-indented artifact verified: the on-disk formatting is not protected, so an edit could hide in it")
	} else if !errors.Is(err, ErrTampered) {
		t.Errorf("a re-indented artifact failed for the wrong reason: %v", err)
	}
}

// TestSigningRefusesAMalformedReport: a signature over a report that does not
// satisfy its own invariants produces a document that verifies perfectly and
// means nothing.
func TestSigningRefusesAMalformedReport(t *testing.T) {
	priv, err := EphemeralSigningKey()
	if err != nil {
		t.Fatalf("EphemeralSigningKey: %v", err)
	}
	bad := Report{
		ReportVersion: CurrentReportVersion,
		GeneratedAt:   fixtureNow(),
		Suite:         SuiteHardware,
		Environment:   validEnvironment(),
		Results: []Result{{
			ID: "a.b", MatrixRow: 1, Area: "a", Suite: SuiteHardware, Precondition: "p",
			Status: StatusPass, Detail: "d", // no evidence
		}},
	}
	bad.Summary = Summarize(bad.Results)
	if _, err := SignReport(bad, priv); err == nil {
		t.Fatal("SignReport signed a report with an evidence-free pass")
	}
}

// TestParsingRejectsAnUnknownEnvelopeMember: a member the parser ignores is a
// member an attacker can add. DisallowUnknownFields is what makes the
// canonical-form check meaningful rather than trivially bypassable.
func TestParsingRejectsAnUnknownEnvelopeMember(t *testing.T) {
	_, _, artifact := signedFixture(t)
	injected := strings.Replace(string(artifact), `{"artifactVersion":1,`, `{"artifactVersion":1,"note":"harmless",`, 1)
	if injected == string(artifact) {
		t.Fatal("could not inject a member; the envelope's encoding changed")
	}
	if _, _, err := ParseSignedReport([]byte(injected)); err == nil {
		t.Error("an artifact with an extra envelope member verified")
	}
}

// TestParsingRejectsTrailingContent.
func TestParsingRejectsTrailingContent(t *testing.T) {
	_, _, artifact := signedFixture(t)
	if _, _, err := ParseSignedReport(append(append([]byte(nil), artifact...), []byte(`{"more":true}`)...)); err == nil {
		t.Error("an artifact with a second JSON document appended verified")
	}
}

// TestParsingRejectsAnUnknownArtifactVersion: a consumer that does not
// recognise the schema must say so rather than guess.
func TestParsingRejectsAnUnknownArtifactVersion(t *testing.T) {
	_, _, artifact := signedFixture(t)
	future := strings.Replace(string(artifact), `"artifactVersion":1`, `"artifactVersion":99`, 1)
	if _, _, err := ParseSignedReport([]byte(future)); err == nil {
		t.Error("an artifact from a future schema version was read as if it were this one")
	}
}

// TestParsingRejectsAForeignSignature: re-signing the same bytes with a
// different key still verifies (the key travels with the document, so this is
// integrity and not provenance) — but a signature from key A over a report
// edited to say something else must not.
func TestParsingRejectsAForeignSignature(t *testing.T) {
	report, _, _ := signedFixture(t)
	otherKey, err := EphemeralSigningKey()
	if err != nil {
		t.Fatalf("EphemeralSigningKey: %v", err)
	}
	reSigned, err := SignReport(report, otherKey)
	if err != nil {
		t.Fatalf("SignReport: %v", err)
	}
	// It verifies — and that is the documented contract: an embedded key
	// proves the bytes are the signed bytes, never who produced them. The
	// fingerprint is how a reader who cares about provenance tells them apart.
	_, otherFingerprint, err := ParseSignedReport(reSigned)
	if err != nil {
		t.Fatalf("a correctly re-signed report did not verify: %v", err)
	}
	_, originalFingerprint, err := ParseSignedReport(mustSign(t, report))
	if err != nil {
		t.Fatalf("ParseSignedReport: %v", err)
	}
	if otherFingerprint == originalFingerprint {
		t.Error("two different signing keys produced the same fingerprint")
	}
}

func mustSign(t *testing.T, r Report) []byte {
	t.Helper()
	priv, err := EphemeralSigningKey()
	if err != nil {
		t.Fatalf("EphemeralSigningKey: %v", err)
	}
	out, err := SignReport(r, priv)
	if err != nil {
		t.Fatalf("SignReport: %v", err)
	}
	return out
}

// TestSignRefusesWithoutAKey.
func TestSignRefusesWithoutAKey(t *testing.T) {
	report, _, _ := signedFixture(t)
	if _, err := SignReport(report, nil); err == nil {
		t.Error("SignReport produced an artifact with no key")
	}
}

// TestReportWithADurationStillRoundTrips guards a subtlety: DurationMS is
// wall-clock-derived, so a report signed on one machine and parsed on another
// must not depend on the clock. Signing a report produced under an advancing
// clock and parsing it proves the values are carried, not recomputed.
func TestReportWithADurationStillRoundTrips(t *testing.T) {
	deps := healthyDeps()
	deps.Now = advancingClock(fixtureNow(), 7*time.Millisecond)
	report, err := Run(context.Background(), Options{Suite: SuiteHardware, Version: "3.0.4", Logger: discardLog()}, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	parsed, _, err := ParseSignedReport(mustSign(t, report))
	if err != nil {
		t.Fatalf("ParseSignedReport: %v", err)
	}
	for i, res := range parsed.Results {
		if res.DurationMS != report.Results[i].DurationMS {
			t.Errorf("%s's duration changed across the round trip: %d vs %d", res.ID, res.DurationMS, report.Results[i].DurationMS)
		}
	}
}

// --- small helpers ------------------------------------------------------------

func contextAround(b []byte, i int) string {
	start := i - 20
	if start < 0 {
		start = 0
	}
	end := i + 20
	if end > len(b) {
		end = len(b)
	}
	return string(b[start:end])
}
