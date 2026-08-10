package gitsync_test

// Signature-verification tests (T-2701 AC5).
//
// The fixtures under testdata/ are REAL: `signed-commit.payload` is a git
// commit object, and each `.sig` beside it was produced by OpenSSH's own
// `ssh-keygen -Y sign -n git`, which is exactly what `git commit -S` runs
// when `gpg.format = ssh`. `testdata/allowed_signers` is an ordinary OpenSSH
// allowed-signers file, and `ssh-keygen -Y verify -f allowed_signers -n git`
// accepts the ed25519 fixture against it — so these bytes are not this
// package's own idea of the format agreeing with itself.
//
// The private keys were deliberately NOT checked in: verification needs only
// the public halves, and a test fixture is not a place to keep signing keys.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/gitsync"
)

func loadSigners(t *testing.T) []gitsync.AllowedSigner {
	t.Helper()
	signers, err := gitsync.LoadAllowedSigners("testdata/allowed_signers")
	if err != nil {
		t.Fatalf("LoadAllowedSigners: %v", err)
	}
	if len(signers) != 2 {
		t.Fatalf("allowed_signers yielded %d signer(s), want 2 (ed25519 + rsa)", len(signers))
	}
	return signers
}

// TestVerifyCommit is the unit-level table over the real fixtures: every
// refusal reason gets a case, so no single change can quietly turn the gate
// into a pass-through.
func TestVerifyCommit(t *testing.T) {
	signers := loadSigners(t)
	payload := readFixture(t, "signed-commit.payload")

	//nolint:govet // fieldalignment: test table; field order documents each case, not packing.
	tests := []struct {
		name          string
		payload       []byte
		sig           string
		wantPrincipal string
		wantErr       bool
		wantErrText   string
	}{
		{
			name:          "ed25519 signature from a listed signer verifies",
			payload:       payload,
			sig:           string(readFixture(t, "signed-commit.ed25519.sig")),
			wantPrincipal: "fixture@vnprox.test",
		},
		{
			name:          "rsa-sha2 signature from a listed signer verifies",
			payload:       payload,
			sig:           string(readFixture(t, "signed-commit.rsa.sig")),
			wantPrincipal: "rsa-fixture@vnprox.test",
		},
		{
			name:        "a signature from a key not in allowed_signers is refused",
			payload:     payload,
			sig:         string(readFixture(t, "signed-commit.untrusted.sig")),
			wantErr:     true,
			wantErrText: "not in the allowed-signers file",
		},
		{
			name:        "a signature made under a different namespace cannot authenticate a commit",
			payload:     payload,
			sig:         string(readFixture(t, "signed-commit.wrongnamespace.sig")),
			wantErr:     true,
			wantErrText: "namespace",
		},
		{
			name:        "a tampered payload does not verify",
			payload:     append(append([]byte(nil), payload...), '\n'),
			sig:         string(readFixture(t, "signed-commit.ed25519.sig")),
			wantErr:     true,
			wantErrText: "does not verify",
		},
		{
			name:        "an OpenPGP signature is unverifiable, never assumed good",
			payload:     payload,
			sig:         "-----BEGIN PGP SIGNATURE-----\niQIzBAABCgAdFiEE\n-----END PGP SIGNATURE-----",
			wantErr:     true,
			wantErrText: "armor header",
		},
		{
			name:        "a truncated armor block is refused",
			payload:     payload,
			sig:         "-----BEGIN SSH SIGNATURE-----\nU1NIU0lH\n-----END SSH SIGNATURE-----",
			wantErr:     true,
			wantErrText: "",
		},
		{
			name:        "an empty signature is refused",
			payload:     payload,
			sig:         "",
			wantErr:     true,
			wantErrText: "armor header",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			principal, err := gitsync.VerifyCommit(tc.payload, tc.sig, signers)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("VerifyCommit accepted %s (principal %q)", tc.name, principal)
				}
				if !errors.Is(err, gitsync.ErrUnverifiableSignature) {
					t.Errorf("error %v does not wrap ErrUnverifiableSignature", err)
				}
				if tc.wantErrText != "" && !strings.Contains(err.Error(), tc.wantErrText) {
					t.Errorf("error %q does not mention %q", err, tc.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("VerifyCommit: %v", err)
			}
			if principal != tc.wantPrincipal {
				t.Errorf("principal = %q, want %q", principal, tc.wantPrincipal)
			}
		})
	}
}

// TestLoadAllowedSigners covers the trust-anchor file's own failure modes.
// An empty or unusable file must be an error, never an empty trust set that
// silently refuses everything for a reason nobody can see.
func TestLoadAllowedSigners(t *testing.T) {
	if _, err := gitsync.LoadAllowedSigners("testdata/does-not-exist"); err == nil {
		t.Error("a missing allowed-signers file loaded without error")
	}
	if _, err := gitsync.LoadAllowedSigners("testdata/signed-commit.payload"); err == nil {
		t.Error("a file containing no public key loaded without error")
	}
	// An authorized_keys-shaped file (no principal column) is accepted too:
	// it is the file most operators already have.
	signers, err := gitsync.LoadAllowedSigners("testdata/signer_ed25519.pub")
	if err != nil {
		t.Fatalf("LoadAllowedSigners on an authorized_keys-shaped file: %v", err)
	}
	if len(signers) != 1 || signers[0].KeyType != "ssh-ed25519" {
		t.Fatalf("parsed %+v, want one ssh-ed25519 key", signers)
	}
}

// TestAC5_SignatureGateBothDirections is acceptance criterion 5 at the
// service level: with require_signed_commits set, a correctly signed commit
// proceeds to a draft and an unsigned or unverifiable one is refused with a
// finding and nothing staged.
func TestAC5_SignatureGateBothDirections(t *testing.T) {
	g := buildFixtureGraph(t, fixtureThreeNode)
	doc := divergentSpec(t, g, 1400)
	payload := readFixture(t, "signed-commit.payload")
	signers := loadSigners(t)

	//nolint:govet // fieldalignment: test table; field order documents each case, not packing.
	tests := []struct {
		name        string
		sig         *gitsync.CommitSignature
		wantDraft   bool
		wantErr     error
		wantCheck   string
		wantDetails string
	}{
		{
			name:      "a correctly signed commit proceeds",
			sig:       &gitsync.CommitSignature{Payload: payload, Armored: string(readFixture(t, "signed-commit.ed25519.sig"))},
			wantDraft: true,
		},
		{
			name:        "an unsigned commit is refused",
			sig:         nil,
			wantErr:     gitsync.ErrUnsigned,
			wantCheck:   gitsync.CheckCommitUnsigned,
			wantDetails: "carries no signature",
		},
		{
			name:        "a commit signed by an unlisted key is refused",
			sig:         &gitsync.CommitSignature{Payload: payload, Armored: string(readFixture(t, "signed-commit.untrusted.sig"))},
			wantErr:     gitsync.ErrUnverifiableSignature,
			wantCheck:   gitsync.CheckSignatureUnverifiable,
			wantDetails: "could not verify",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := &fakeSource{}
			src.setSigned("feedfacefeedfacefeedfacefeedfacefeedface", doc, tc.sig)
			stager := newFakeStager()
			svc := newTestService(t, src, stager, g, func(c *gitsync.Config) {
				c.RequireSignedCommits = true
				c.AllowedSigners = signers
			})

			res, err := svc.Sync(context.Background())
			if tc.wantDraft {
				if err != nil {
					t.Fatalf("Sync: %v", err)
				}
				if !res.Created {
					t.Fatalf("a correctly signed commit did not open a draft: %+v", res)
				}
				if st := svc.Status(); st.LastSigner != "fixture@vnprox.test" {
					t.Errorf("Status.LastSigner = %q, want the verified principal", st.LastSigner)
				}
				return
			}

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Sync error = %v, want one wrapping %v", err, tc.wantErr)
			}
			if create, update, _, apply := stager.counts(); create != 0 || update != 0 || apply != 0 {
				t.Errorf("a refused commit touched the change engine: create=%d update=%d apply=%d", create, update, apply)
			}
			var got *gitsync.Issue
			for _, iss := range svc.Issues() {
				if iss.Check == tc.wantCheck {
					iss := iss
					got = &iss
				}
			}
			if got == nil {
				t.Fatalf("no %s finding was raised; issues = %+v", tc.wantCheck, svc.Issues())
			}
			if !strings.Contains(got.Detail, tc.wantDetails) {
				t.Errorf("finding detail %q does not contain %q", got.Detail, tc.wantDetails)
			}
			if !strings.Contains(got.Detail, "nothing was staged") {
				t.Errorf("finding detail %q does not say nothing was staged", got.Detail)
			}
		})
	}
}

// TestAC5_GateOffAcceptsAnUnsignedCommit is the guard's own control: with
// require_signed_commits unset, the same unsigned commit that was refused
// above proceeds. Without this, a gate that refused everything (or a fixture
// that was unsigned for the wrong reason) would look identical.
func TestAC5_GateOffAcceptsAnUnsignedCommit(t *testing.T) {
	g := buildFixtureGraph(t, fixtureThreeNode)
	src := &fakeSource{}
	src.setSigned("feedfacefeedfacefeedfacefeedfacefeedface", divergentSpec(t, g, 1400), nil)
	stager := newFakeStager()
	svc := newTestService(t, src, stager, g, nil) // RequireSignedCommits defaults to false

	res, err := svc.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync with the signature gate off: %v", err)
	}
	if !res.Created {
		t.Fatalf("an unsigned commit did not open a draft with the gate off: %+v", res)
	}
}

// TestAC5_AProviderThatCannotSupplySignaturesFailsClosed asserts the stated
// consequence of the git-access decision: a source that cannot hand over the
// signed commit object (GitLab, a plain raw host) is treated as unsigned
// under require_signed_commits, never as "the host says it's fine".
func TestAC5_AProviderThatCannotSupplySignaturesFailsClosed(t *testing.T) {
	g := buildFixtureGraph(t, fixtureSingleNode)
	src := &fakeSource{}
	// A revision with content but no signature material at all — exactly
	// what fetchGitLab and fetchRaw return.
	src.setSigned("sha256:deadbeef", divergentSpec(t, g, 1400), nil)
	stager := newFakeStager()
	svc := newTestService(t, src, stager, g, func(c *gitsync.Config) {
		c.RequireSignedCommits = true
		c.AllowedSigners = loadSigners(t)
	})

	if _, err := svc.Sync(context.Background()); !errors.Is(err, gitsync.ErrUnsigned) {
		t.Fatalf("Sync = %v, want ErrUnsigned — a source that cannot prove a signature must fail closed", err)
	}
	if create, _, _, _ := stager.counts(); create != 0 {
		t.Errorf("a source that cannot prove a signature still staged %d changeset(s)", create)
	}
}
