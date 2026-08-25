package hubreg

import (
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/hub"
)

// TestAutomatedVetChecks_ResultAlwaysCarriesResidualNote is the badge-copy
// invariant this file exists to protect: the reproducible-build residual is
// present in Notes regardless of whether the other checks passed, so a
// caller inspecting a passing VetResult can never mistake it for a
// full reproducible-build attestation.
func TestAutomatedVetChecks_ResultAlwaysCarriesResidualNote(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)

	for _, tc := range []struct {
		name string
		sub  Submission
	}{
		{"good blueprint", buildTestBlueprintSubmission(t, priv)},
		{"good plugin", buildTestPluginSubmission(t, priv)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := AutomatedVetChecks(tc.sub)
			found := false
			for _, n := range res.Notes {
				if n == reproducibleBuildResidualNote {
					found = true
				}
			}
			if !found {
				t.Fatalf("Notes = %v, want the reproducible-build residual note always present", res.Notes)
			}
		})
	}
}

func TestAutomatedVetChecks_Blueprint(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)

	t.Run("well-formed passes", func(t *testing.T) {
		sub := buildTestBlueprintSubmission(t, priv)
		res := AutomatedVetChecks(sub)
		if !res.Passed() {
			t.Fatalf("res = %+v, want Passed()", res)
		}
		if !res.ManifestWellFormed || !res.CapabilityScopeAgrees {
			t.Fatalf("res = %+v, want the two N/A-for-blueprint checks vacuously true", res)
		}
	})

	t.Run("unknown field in the artifact fails ArtifactWellFormed", func(t *testing.T) {
		sub := buildTestBlueprintSubmission(t, priv)
		sub.Artifact = injectUnknownField(t, sub.Artifact)
		res := AutomatedVetChecks(sub)
		if res.ArtifactWellFormed {
			t.Fatal("an artifact with an extra, undeclared field must not be ArtifactWellFormed")
		}
		if res.Passed() {
			t.Fatal("Passed() must be false when ArtifactWellFormed is false")
		}
	})
}

func TestAutomatedVetChecks_Plugin(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)

	t.Run("well-formed, agreeing capabilities passes", func(t *testing.T) {
		sub := buildTestPluginSubmission(t, priv)
		res := AutomatedVetChecks(sub)
		if !res.Passed() {
			t.Fatalf("res = %+v, want Passed()", res)
		}
	})

	t.Run("undeclared capability mismatch fails CapabilityScopeAgrees", func(t *testing.T) {
		sub := buildTestPluginSubmission(t, priv)
		// Simulate a catalog entry that disagrees with the artifact's own
		// manifest — exactly hub.CapabilityMismatch's reason to exist,
		// reused here rather than reimplemented.
		sub.Entry.Capabilities = []string{"netRead", "netWrite"}
		res := AutomatedVetChecks(sub)
		if res.CapabilityScopeAgrees {
			t.Fatal("a mismatched catalog entry must not report CapabilityScopeAgrees")
		}
		if res.Passed() {
			t.Fatal("Passed() must be false on a capability mismatch")
		}
		joined := strings.Join(res.Notes, "\n")
		if !strings.Contains(joined, "disagree") {
			t.Fatalf("Notes = %v, want a note naming the disagreement", res.Notes)
		}
	})

	t.Run("apiVersion outside the frozen v1 surface fails ManifestWellFormed", func(t *testing.T) {
		sub := buildTestPluginSubmission(t, priv)
		sub.Artifact = withManifestField(t, sub.Artifact, "apiVersion", "v2")
		res := AutomatedVetChecks(sub)
		if res.ManifestWellFormed {
			t.Fatal("apiVersion v2 must not be ManifestWellFormed against the frozen v1 surface")
		}
	})

	t.Run("unrecognized transport fails ManifestWellFormed", func(t *testing.T) {
		sub := buildTestPluginSubmission(t, priv)
		sub.Artifact = withManifestField(t, sub.Artifact, "transport", "carrier-pigeon")
		res := AutomatedVetChecks(sub)
		if res.ManifestWellFormed {
			t.Fatal("an unrecognized transport must not be ManifestWellFormed")
		}
	})

	t.Run("extension point below its minimum capability fails ManifestWellFormed", func(t *testing.T) {
		// dashboardTile requires netRead (plugin.extensionPointMinCap); a
		// manifest declaring the point with no capabilities at all must
		// fail plugin.ValidateScope, reused unmodified here.
		sub := buildTestPluginSubmission(t, priv)
		sub.Entry.Capabilities = nil
		sub.Artifact = withManifestField(t, sub.Artifact, "capabilities", []string{})
		res := AutomatedVetChecks(sub)
		if res.ManifestWellFormed {
			t.Fatal("an extension point missing its minimum capability must not be ManifestWellFormed")
		}
	})

	t.Run("unknown field in the artifact fails ArtifactWellFormed", func(t *testing.T) {
		sub := buildTestPluginSubmission(t, priv)
		sub.Artifact = injectUnknownField(t, sub.Artifact)
		res := AutomatedVetChecks(sub)
		if res.ArtifactWellFormed {
			t.Fatal("an artifact with an extra, undeclared field must not be ArtifactWellFormed")
		}
	})
}

// --- test helpers -----------------------------------------------------

func buildTestBlueprintSubmission(t *testing.T, priv ed25519.PrivateKey) Submission {
	t.Helper()
	raw := testBundleJSON(t, "vet-bp", priv)
	sub, err := BuildSubmission(raw, SubmissionOptions{Type: hub.TypeBlueprint, Version: "1.0.0"})
	if err != nil {
		t.Fatalf("BuildSubmission: %v", err)
	}
	return sub
}

func buildTestPluginSubmission(t *testing.T, priv ed25519.PrivateKey) Submission {
	t.Helper()
	raw := testPluginJSON(t, "vet-pl", "1.0.0", priv)
	sub, err := BuildSubmission(raw, SubmissionOptions{Type: hub.TypePlugin, Version: "1.0.0"})
	if err != nil {
		t.Fatalf("BuildSubmission: %v", err)
	}
	return sub
}

// injectUnknownField adds a field no target struct declares, simulating a
// submission file hand-edited after `hub publish` wrote it — exactly what
// ArtifactWellFormed exists to catch.
func injectUnknownField(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	m["notAKnownField"] = "surprise"
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

// withManifestField overwrites one field of a plugin artifact's manifest
// object, simulating a hand-edited submission.
func withManifestField(t *testing.T, raw json.RawMessage, field string, value any) json.RawMessage {
	t.Helper()
	var art map[string]any
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	manifest, ok := art["manifest"].(map[string]any)
	if !ok {
		t.Fatal("artifact has no manifest object")
	}
	manifest[field] = value
	out, err := json.Marshal(art)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}
