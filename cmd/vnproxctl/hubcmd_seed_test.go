// hubcmd_seed_test.go is T-2104 AC4: the submission and review process
// documented in docs/hub-registry.md, walked once end to end with real
// bundles — the T-2104 seeded blueprint library (internal/hub/seed), not the
// content-light placeholder TestHubCLI_PublishReviewIndexFlow uses to pin
// down the CLI's own mechanics. Every seed is signed, submitted, reviewed
// and indexed through the exact `vnproxctl hub` commands a real publisher
// and registry maintainer would run, and the resulting published artifact is
// read back and confirmed to still verify and to still carry the seed's own
// entities — proof that real, multi-entity blueprint content survives the
// whole pipeline, not just the two-field fixture the mechanics test uses.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/hub/seed"
	"github.com/bgovanlu/vnprox/internal/hubreg"
)

// writeSeedBundle writes an unsigned bundle file for one seed blueprint, for
// the publisher to sign via `hub publish --key`.
func writeSeedBundle(t *testing.T, dir string, bp *blueprint.Blueprint) string {
	t.Helper()
	bundle := blueprint.Bundle{BundleVersion: blueprint.CurrentBundleVersion, Blueprint: *bp}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(dir, bp.ID+".bundle.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// TestHubCLI_SeedBlueprintsPublishReviewIndex walks docs/hub-registry.md's
// submission/review process for every T-2104 seed blueprint: publisher signs
// and submits, reviewer indexes, anyone verifies — then the published
// artifact is fetched back and confirmed to (a) still verify against the
// publisher's key and (b) still be the same real topology the seed package
// built, not a placeholder.
func TestHubCLI_SeedBlueprintsPublishReviewIndex(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "registry")
	pubKey, pubFP := keygen(t, dir, "publisher.key")
	idxKey, idxFP := keygen(t, dir, "index.key")

	for _, bp := range seed.Seeds() {
		bp := bp
		t.Run(bp.ID, func(t *testing.T) {
			bundlePath := writeSeedBundle(t, dir, bp)
			submission := filepath.Join(dir, bp.ID+".submission.json")

			// 1. Publisher side: sign the real seed content and submit it.
			code, _, errOut := hubRun(t, "hub", "publish",
				"--artifact", bundlePath, "--type", "blueprint", "--version", "1.0.0",
				"--key", pubKey, "--publisher", "vnprox-project", "--description", bp.Description,
				"--out", submission)
			if code != ExitSuccess {
				t.Fatalf("publish: code=%d stderr=%s", code, errOut)
			}
			subRaw, err := os.ReadFile(submission) //nolint:gosec // test-local path
			if err != nil {
				t.Fatalf("read submission: %v", err)
			}
			sub, err := hubreg.ParseSubmission(subRaw)
			if err != nil {
				t.Fatalf("the emitted submission must be valid: %v", err)
			}
			if sub.Entry.SignerFingerprint() != pubFP {
				t.Fatalf("submission signer = %q, want %q", sub.Entry.SignerFingerprint(), pubFP)
			}
			if sub.Entry.ID != bp.ID {
				t.Fatalf("submission entry id = %q, want %q", sub.Entry.ID, bp.ID)
			}

			// 2. Reviewer side: index it (this is what vnproxctl hub index
			// re-checks mechanically per docs/hub-registry.md's review table:
			// identity agreement, the derived artifact URL, and that the
			// signature verifies over the exact bytes being published).
			code, _, errOut = hubRun(t, "hub", "index", "--root", root, "--submission", submission, "--key", idxKey)
			if code != ExitSuccess {
				t.Fatalf("index: code=%d stderr=%s", code, errOut)
			}

			// 3. Anyone: verify the published index against the pinned
			// registry signer, and that this seed is offered.
			indexPath := filepath.Join(root, "index.json")
			code, out, errOut := hubRun(t, "hub", "verify", "--index", indexPath, "--signers", idxFP)
			if code != ExitSuccess {
				t.Fatalf("verify: code=%d stderr=%s", code, errOut)
			}
			wantLine := bp.ID + "@1.0.0"
			if !strings.Contains(out, wantLine) {
				t.Fatalf("verify output = %q, want it to list %q", out, wantLine)
			}

			// 4. Read the published artifact straight off disk (what a
			// static host would serve at the entry's artifactUrl) and
			// confirm it is still real content: it verifies against the
			// publisher's own key, and it still carries the seed's exact
			// entity count and every param this seed declares — not a
			// placeholder, and not silently truncated by the pipeline.
			artifactPath := filepath.Join(root, filepath.FromSlash(sub.Entry.ArtifactURL))
			artifactRaw, err := os.ReadFile(artifactPath) //nolint:gosec // test-local path
			if err != nil {
				t.Fatalf("read published artifact: %v", err)
			}
			var published blueprint.Bundle
			if unmarshalErr := json.Unmarshal(artifactRaw, &published); unmarshalErr != nil {
				t.Fatalf("parse published artifact: %v", unmarshalErr)
			}
			verified, fp, err := blueprint.VerifyBundle(published)
			if err != nil || !verified {
				t.Fatalf("published artifact does not verify: verified=%v err=%v", verified, err)
			}
			if fp != pubFP {
				t.Fatalf("published artifact signer = %q, want %q", fp, pubFP)
			}
			if len(published.Blueprint.Entities) != len(bp.Entities) {
				t.Fatalf("published entities = %d, want %d (the seed's own count)", len(published.Blueprint.Entities), len(bp.Entities))
			}
			if len(published.Blueprint.Params) != len(bp.Params) {
				t.Fatalf("published params = %d, want %d", len(published.Blueprint.Params), len(bp.Params))
			}
		})
	}

	// AC1, at this CLI layer: an unsigned/tampered artifact never reaches
	// this far. publish without --key and without --allow-unsigned refuses
	// outright, so an unsigned seed can never be silently indexed.
	t.Run("unsigned publish refused without --allow-unsigned", func(t *testing.T) {
		bp := seed.Seeds()[0]
		bundlePath := writeSeedBundle(t, dir, bp)
		code, _, errOut := hubRun(t, "hub", "publish", "--artifact", bundlePath, "--type", "blueprint", "--version", "9.9.9")
		if code == ExitSuccess {
			t.Fatal("publish succeeded for an unsigned artifact with no --allow-unsigned")
		}
		if errOut == "" {
			t.Fatal("expected a refusal message")
		}
	})
}
