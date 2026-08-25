// vetting.go implements T-3709's automated "vetted" checks — the owner's
// decision that the hub's "vetted" badge means mechanically-checked hygiene,
// never a human's vouching for a signer (docs/hub-registry.md's "Automated
// vetting" section; internal/hub's package doc comment). AutomatedVetChecks
// runs at `vnproxctl hub index` time, when the registry maintainer's own
// tooling holds the full, reviewed artifact bytes — the one point in the
// pipeline where running these checks is both cheap (once per publish, not
// once per catalog browse — GET /hub/index never re-downloads every
// artifact just to render a badge) and trustworthy (the verdict is folded
// into the entry BEFORE the index is (re-)signed, so it rides inside the
// same signature everything else an operator's daemon verifies — an
// attacker who cannot forge the index signature cannot forge a vetted
// verdict either).

package hubreg

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/hub"
	"github.com/bgovanlu/vnprox/internal/plugin"
)

// reproducibleBuildResidualNote is ALWAYS appended to a VetResult's Notes,
// pass or fail, so the one named check this package does not attempt is
// never silently absent from anything that inspects a VetResult.
const reproducibleBuildResidualNote = "reproducible-build check: NOT performed. vnprox has no source-to-artifact build pipeline; for a plugin, the registry never receives the executable the manifest's endpoint names at all (only a {manifest, signature} artifact — see cmd/vnproxd/hubinstall.go), so there is nothing here to rebuild and compare. See docs/hub-registry.md's 'Automated vetting' section."

// VetResult is AutomatedVetChecks' outcome: which checks passed, and a
// human-readable reason for any that did not (plus the reproducible-build
// residual note, always).
//
//nolint:govet // fieldalignment: small result value returned by copy, not stored at scale; field order groups the slice before the bools for readability, not packing.
type VetResult struct {
	Notes                 []string
	ManifestWellFormed    bool
	CapabilityScopeAgrees bool
	ArtifactWellFormed    bool
}

// Passed reports whether every automated check this package can honestly
// perform succeeded. It is deliberately NOT "every check the decision
// named" — there is no reproducible-build field in this result at all.
// Inventing a placeholder that always reports true would let the "vetted"
// badge claim something unchecked, which is exactly the failure T-3709
// exists to close off.
func (r VetResult) Passed() bool {
	return r.ManifestWellFormed && r.CapabilityScopeAgrees && r.ArtifactWellFormed
}

// AutomatedVetChecks runs T-3709's automated hygiene checks over a
// submission's own artifact:
//
//   - ManifestWellFormed: for a plugin, the manifest declares a real
//     identity (id/name/version), targets the frozen v1 plugin SDK surface
//     (plugin.APIVersion), names a recognized transport, and its declared
//     extension points/capabilities are internally consistent — reusing
//     plugin.NewScope + plugin.ValidateScope, the exact vocabulary
//     plugin.Registry.Install enforces at install time, not reimplemented
//     here. A blueprint has no capability manifest of its own
//     (docs/hub-registry.md), so this is vacuously true for one.
//   - CapabilityScopeAgrees: the catalog entry's advertised
//     capabilities/extensionPoints agree with the artifact's own manifest —
//     hub.CapabilityMismatch, the T-2104 gate, reused rather than
//     reimplemented (per this card's own instruction). Vacuously true for a
//     blueprint: there is no capability dimension to disagree about.
//   - ArtifactWellFormed: the artifact decodes strictly into its declared
//     type with no unrecognized fields riding along (encoding/json's
//     DisallowUnknownFields). A signature is verified against the
//     CANONICALIZED form of whatever a client's json.Unmarshal accepts, so
//     an extra field smuggled into an otherwise validly-signed artifact
//     would decode today with nothing to say so; this check refuses to call
//     that artifact vetted.
//
// What this does NOT check, on purpose: reproducible builds. See
// reproducibleBuildResidualNote, always present in the result.
func AutomatedVetChecks(s Submission) VetResult {
	res := VetResult{Notes: []string{reproducibleBuildResidualNote}}
	switch s.Entry.Type {
	case hub.TypePlugin:
		vetPluginArtifact(s, &res)
	case hub.TypeBlueprint:
		vetBlueprintArtifact(s, &res)
	default:
		res.Notes = append(res.Notes, fmt.Sprintf("unknown artifact type %q", s.Entry.Type))
	}
	return res
}

func vetPluginArtifact(s Submission, res *VetResult) {
	var art hub.PluginArtifact
	dec := json.NewDecoder(bytes.NewReader(s.Artifact))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&art); err != nil {
		res.Notes = append(res.Notes, fmt.Sprintf("artifact does not decode strictly into a plugin artifact: %v", err))
		return
	}
	res.ArtifactWellFormed = true

	if ok, note := pluginManifestWellFormed(art.Manifest); ok {
		res.ManifestWellFormed = true
	} else {
		res.Notes = append(res.Notes, note)
	}

	if mismatch := hub.CapabilityMismatch(s.Entry, art.Manifest); mismatch == "" {
		res.CapabilityScopeAgrees = true
	} else {
		res.Notes = append(res.Notes, mismatch)
	}
}

func vetBlueprintArtifact(s Submission, res *VetResult) {
	// Blueprints declare no capability manifest or privilege scope of their
	// own (docs/hub-registry.md) — there is nothing for the first two
	// checks to assert, so they are vacuously satisfied; only the
	// artifact's own strictness is meaningful to check.
	res.ManifestWellFormed = true
	res.CapabilityScopeAgrees = true

	var bundle blueprint.Bundle
	dec := json.NewDecoder(bytes.NewReader(s.Artifact))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&bundle); err != nil {
		res.Notes = append(res.Notes, fmt.Sprintf("artifact does not decode strictly into a blueprint bundle: %v", err))
		return
	}
	res.ArtifactWellFormed = true
}

// pluginManifestWellFormed checks manifest hygiene using the same
// vocabulary plugin.Registry.Install enforces (plugin.APIVersion, the two
// plugin.Transport constants, plugin.NewScope + plugin.ValidateScope) —
// reused, not reimplemented.
func pluginManifestWellFormed(m hub.PluginManifest) (bool, string) {
	switch {
	case m.ID == "":
		return false, "manifest has no id"
	case m.Name == "":
		return false, "manifest has no name"
	case m.Version == "":
		return false, "manifest has no version"
	case m.APIVersion != plugin.APIVersion:
		return false, fmt.Sprintf("manifest apiVersion %q is not the frozen %q plugin SDK surface (docs/architecture.md §13.2)", m.APIVersion, plugin.APIVersion)
	}
	t := plugin.Transport(m.Transport)
	if t != plugin.TransportInProcess && t != plugin.TransportGRPC {
		return false, fmt.Sprintf("manifest transport %q is not a recognized plugin transport", m.Transport)
	}
	points := make([]plugin.ExtensionPoint, 0, len(m.ExtensionPoints))
	for _, ep := range m.ExtensionPoints {
		points = append(points, plugin.ExtensionPoint(ep))
	}
	scope, err := plugin.NewScope(m.Capabilities)
	if err != nil {
		return false, err.Error()
	}
	if err := plugin.ValidateScope(points, scope); err != nil {
		return false, err.Error()
	}
	return true, ""
}
