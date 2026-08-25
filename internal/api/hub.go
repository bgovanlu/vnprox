// hub.go implements T-1705's Blueprint & plugin hub — a browse/install
// surface over T-1107's signed blueprint bundles and T-1702's capability-scoped
// plugins (docs/features/blueprints.md's Hub subsection, docs/api.md's Hub
// section):
//
//   - GET  /hub/index?type=blueprint|plugin — proxy/cache the registry index,
//     annotate each entry with the informational "vetted" badge (netRead).
//   - POST /hub/install {type, id, version, trustUnsigned?, trustNewKey?} —
//     download the named artifact and install it (netWrite + CSRF).
//
// The hub is a catalog/install-orchestration layer, NOT a new trust boundary.
// It inherits every security gate wholesale:
//
//   - A blueprint install runs the downloaded bundle through T-1107's *exact*
//     import path — importBundleCore (blueprint_bundle.go), the same
//     verify -> trust-decision -> save+audit logic POST /blueprints/import uses,
//     reused not duplicated (T-1705 AC2).
//   - A plugin install verifies the downloaded manifest's Ed25519 signature
//     (blueprint.VerifySignature — the same primitive) against the same
//     TrustStore, applying the identical unsigned/untrusted/invalid gate, then
//     installs through T-1702's plugin.Registry (via PluginInstaller), which
//     independently re-validates the capability scope. The hub never reaches an
//     install path that skips the signature gate or the capability check.
//   - The "vetted" badge (VettedSet) is purely informational and never bypasses
//     the per-installation trust decision (T-1705 AC5).

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/hub"
	"github.com/bgovanlu/vnprox/internal/plugin"
	"github.com/bgovanlu/vnprox/internal/store"
)

// maxHubInstallBodyBytes bounds a POST /hub/install request body — a small
// {type,id,version,flags} object.
const maxHubInstallBodyBytes = 64 << 10

// HubClient is the router's seam onto internal/hub's registry client: fetch the
// index and download a named entry's artifact. Declared as an interface (the
// standard seam pattern) so tests substitute an in-memory registry double with
// no network.
type HubClient interface {
	Index(ctx context.Context) (hub.Index, error)
	FetchBlueprintBundle(ctx context.Context, entry hub.Entry) (blueprint.Bundle, error)
	FetchPluginArtifact(ctx context.Context, entry hub.Entry) (hub.PluginArtifact, error)
}

// HubVetting reports whether a signer fingerprint is in the hub's own
// recognized-signer allowlist ([hub] vetted_signers) — satisfied by
// hub.VettedSet. Purely informational.
type HubVetting interface {
	IsVetted(fingerprint string) bool
}

// PluginInstaller is the write seam onto T-1702's capability-scoped plugin
// registry. It takes a verified manifest and installs it through
// plugin.Registry.Install, which validates the capability scope and (for an
// out-of-process plugin) spawns and supervises the subprocess. The concrete
// implementation lives in cmd/vnproxd (the registration/subprocess wiring); the
// hub depends only on this manifest-in seam so it never handles a raw
// Registration or bypasses the registry's scope enforcement.
type PluginInstaller interface {
	Install(ctx context.Context, actor string, m plugin.Manifest) error
}

// hubEntryResponse is one catalog entry on the wire. Signed/SignerFingerprint
// expose the publisher signer identity; Vetted is the informational badge.
// Capabilities/ExtensionPoints/Transport are populated only for plugin entries
// so a browse UI can surface a plugin's declared capability scope for review
// before an install is confirmed (T-1705 AC4).
type hubEntryResponse struct {
	Type              string   `json:"type"`
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Version           string   `json:"version"`
	Publisher         string   `json:"publisher,omitempty"`
	Description       string   `json:"description,omitempty"`
	ArtifactURL       string   `json:"artifactUrl"`
	SignerFingerprint string   `json:"signerFingerprint,omitempty"`
	Transport         string   `json:"transport,omitempty"`
	Capabilities      []string `json:"capabilities,omitempty"`
	ExtensionPoints   []string `json:"extensionPoints,omitempty"`
	Signed            bool     `json:"signed"`
	Vetted            bool     `json:"vetted"`
}

type hubIndexResponse struct {
	Items []hubEntryResponse `json:"items"`
}

// hubInstallRequest is POST /hub/install's body. TrustUnsigned/TrustNewKey are
// the identical explicit-trust flags POST /blueprints/import uses — a hub
// install of an unsigned or untrusted-signer artifact requires the same
// explicit step, never an implicit trust (T-1705 AC3/AC5).
type hubInstallRequest struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	Version       string `json:"version"`
	TrustUnsigned bool   `json:"trustUnsigned,omitempty"`
	TrustNewKey   bool   `json:"trustNewKey,omitempty"`
}

// hubPluginInstalled echoes the installed plugin's identity and (authoritative,
// signed) capability scope back on success, so the UI confirms exactly what was
// installed.
type hubPluginInstalled struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	Capabilities    []string `json:"capabilities"`
	ExtensionPoints []string `json:"extensionPoints"`
}

// hubInstallResponse unifies both install outcomes. Status reuses the blueprint
// import vocabulary for the shared signature gate (unsigned / untrustedSignature
// / invalidSignature), plus "imported" (blueprint success) / "installed"
// (plugin success) — so a UI can reuse T-1107's trust-status dialog for the
// gate states across both artifact kinds.
type hubInstallResponse struct {
	Blueprint *blueprint.Blueprint  `json:"blueprint,omitempty"`
	Signer    *bundleSignerResponse `json:"signer,omitempty"`
	Plugin    *hubPluginInstalled   `json:"plugin,omitempty"`
	Type      string                `json:"type"`
	Status    string                `json:"status"`
}

// hubStatusInstalled is the plugin-install success status (the blueprint path
// reuses bundleStatusImported).
const hubStatusInstalled = "installed"

// hubStatusCapabilityMismatch (T-2104 AC2) is returned when a plugin
// artifact's manifest declares a capability scope or extension-point set
// different from what the catalog entry (GET /hub/index) advertised for it —
// the install is refused unconditionally, independent of signature/trust,
// because an operator can only consent to what they were shown.
const hubStatusCapabilityMismatch = "capabilityMismatch"

// hubStatusTrustUnsignedForbidden (T-2904) is the audit-detail status recorded
// when a request asked to trust an unsigned artifact but the server config
// forbids it. Unlike the gate statuses above it is never a 200 response body:
// the request is refused with errHubTrustUnsignedForbidden (a 403 on the
// wire), because the caller asked for something this server is configured to
// never do.
const hubStatusTrustUnsignedForbidden = "trustUnsignedForbidden"

// errHubTrustUnsignedForbidden is T-2904's config gate on the request-body
// trustUnsigned flag: honoring it for an unsigned artifact requires
// [hub] trust_unsigned = true in the server config. The request field stays
// schema-valid — it is refused out loud (never silently ignored), and the
// error names the config key so an operator knows exactly which knob the
// request needed. Signature verification for signed artifacts is a separate,
// never-optional gate this error has nothing to do with.
var errHubTrustUnsignedForbidden = errors.New("the server config forbids trusting unsigned hub artifacts: honoring trustUnsigned requires [hub] trust_unsigned = true")

// mountHubRoutes registers the hub routes. Every dependency is nil-safe: a
// missing hub client skips the whole family; within it, blueprint installs need
// svc+trust and plugin installs need installer+trust, and a type whose backing
// dependency is absent returns a clean 501 rather than a panic (the standard
// degraded-mode convention). trustUnsigned is [hub] trust_unsigned (T-2904):
// whether a request's trustUnsigned flag may be honored at all.
func mountHubRoutes(r chi.Router, client HubClient, vetting HubVetting, svc BlueprintService, trust BlueprintTrustStore, installer PluginInstaller, audit blueprintBundleAuditor, auth AuthService, trustUnsigned bool) {
	if client == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/hub/index", handleHubIndex(client, vetting))
	})

	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/hub/install", handleHubInstall(client, svc, trust, installer, audit, lookup, trustUnsigned))
	})
}

func handleHubIndex(client HubClient, vetting HubVetting) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter := hub.EntryType(r.URL.Query().Get("type"))
		if filter != "" && !hub.ValidType(filter) {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", `type must be "blueprint" or "plugin"`)
			return
		}
		idx, err := client.Index(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "registry_unavailable", "could not fetch registry index")
			return
		}
		items := make([]hubEntryResponse, 0, len(idx.Entries))
		for _, e := range idx.Entries {
			if filter != "" && e.Type != filter {
				continue
			}
			items = append(items, toHubEntryResponse(e, vetting))
		}
		writeJSON(w, http.StatusOK, hubIndexResponse{Items: items})
	}
}

// toHubEntryResponse computes the "vetted" badge from TWO signals, and both
// are required (T-3709): the operator's own [hub] vetted_signers allowlist
// opts a signer INTO consideration, and Entry.AutomatedChecksPassed is the
// mechanical hygiene verdict internal/hubreg's AutomatedVetChecks recorded
// at publish time, inside the signed index. Neither alone is sufficient —
// an allowlisted signer whose artifact failed hygiene is not vetted, and a
// hygienic artifact from a signer the operator never opted in is not vetted
// either. This is deliberately never a claim that a human reviewed or
// endorsed the artifact; see docs/hub-registry.md's "Automated vetting"
// section for the exact wording and what is and is not checked.
func toHubEntryResponse(e hub.Entry, vetting HubVetting) hubEntryResponse {
	fp := e.SignerFingerprint()
	vetted := false
	if vetting != nil {
		vetted = vetting.IsVetted(fp) && e.AutomatedChecksPassed
	}
	return hubEntryResponse{
		Type:              string(e.Type),
		ID:                e.ID,
		Name:              e.Name,
		Version:           e.Version,
		Publisher:         e.Publisher,
		Description:       e.Description,
		ArtifactURL:       e.ArtifactURL,
		SignerFingerprint: fp,
		Transport:         e.Transport,
		Capabilities:      nonNilStrings(e.Capabilities),
		ExtensionPoints:   nonNilStrings(e.ExtensionPoints),
		Signed:            e.Signature != nil,
		Vetted:            vetted,
	}
}

func handleHubInstall(client HubClient, svc BlueprintService, trust BlueprintTrustStore, installer PluginInstaller, audit blueprintBundleAuditor, lookup UsernameLookup, trustUnsignedAllowed bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var req hubInstallRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHubInstallBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body: "+err.Error())
			return
		}
		entryType := hub.EntryType(req.Type)
		if !hub.ValidType(entryType) {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", `type must be "blueprint" or "plugin"`)
			return
		}
		if req.ID == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "id is required")
			return
		}

		entry, found := findHubEntry(r.Context(), client, entryType, req.ID, req.Version)
		if !found {
			writeJSONError(w, http.StatusNotFound, "not_found", "no such registry entry")
			return
		}

		switch entryType {
		case hub.TypeBlueprint:
			installHubBlueprint(w, r, client, svc, trust, audit, username, entry, req, trustUnsignedAllowed)
		case hub.TypePlugin:
			installHubPlugin(w, r, client, trust, installer, audit, username, entry, req, trustUnsignedAllowed)
		}
	}
}

// findHubEntry fetches the index and returns the entry matching type+id (and
// version, when a version is given). A registry-fetch failure is reported as
// "not found" to the caller only after logging — but here it simply yields
// found=false, and the caller returns 404 (a fetch failure and a missing entry
// are indistinguishable to an installer and both mean "cannot proceed").
func findHubEntry(ctx context.Context, client HubClient, t hub.EntryType, id, version string) (hub.Entry, bool) {
	idx, err := client.Index(ctx)
	if err != nil {
		return hub.Entry{}, false
	}
	for _, e := range idx.Entries {
		if e.Type == t && e.ID == id && (version == "" || e.Version == version) {
			return e, true
		}
	}
	return hub.Entry{}, false
}

func installHubBlueprint(w http.ResponseWriter, r *http.Request, client HubClient, svc BlueprintService, trust BlueprintTrustStore, audit blueprintBundleAuditor, username string, entry hub.Entry, req hubInstallRequest, trustUnsignedAllowed bool) {
	if svc == nil || trust == nil {
		writeJSONError(w, http.StatusNotImplemented, "not_available", "blueprint hub installs are not configured on this node")
		return
	}
	bundle, err := client.FetchBlueprintBundle(r.Context(), entry)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "registry_unavailable", "could not download blueprint bundle")
		return
	}
	// T-2904: honoring the request's trustUnsigned flag for an unsigned
	// bundle needs [hub] trust_unsigned = true — refused out loud with the
	// config key named, never silently downgraded to an "unsigned" verdict.
	// Without the flag the request asked for nothing the config forbids, and
	// the ordinary unsigned rejection below applies unchanged.
	if bundle.Signature == nil && req.TrustUnsigned && !trustUnsignedAllowed {
		auditHubInstallDetail(r.Context(), audit, username, "blueprint", entry.ID, hubStatusTrustUnsignedForbidden, "", "", errHubTrustUnsignedForbidden.Error())
		writeJSONError(w, http.StatusForbidden, "trust_unsigned_forbidden", errHubTrustUnsignedForbidden.Error())
		return
	}
	// The one and only blueprint verify/trust/save path (T-1107's import
	// logic) — reused, never duplicated.
	resp, status, err := importBundleCore(r.Context(), svc, trust, audit, username, bundle, req.TrustUnsigned, req.TrustNewKey)
	if err != nil {
		writeBlueprintError(w, err)
		return
	}
	writeJSON(w, status, hubInstallResponse{
		Type:      string(hub.TypeBlueprint),
		Status:    resp.Status,
		Blueprint: resp.Blueprint,
		Signer:    resp.Signer,
	})
}

func installHubPlugin(w http.ResponseWriter, r *http.Request, client HubClient, trust BlueprintTrustStore, installer PluginInstaller, audit blueprintBundleAuditor, username string, entry hub.Entry, req hubInstallRequest, trustUnsignedAllowed bool) {
	if installer == nil || trust == nil {
		writeJSONError(w, http.StatusNotImplemented, "not_available", "plugin hub installs are not configured on this node")
		return
	}
	art, err := client.FetchPluginArtifact(r.Context(), entry)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "registry_unavailable", "could not download plugin artifact")
		return
	}
	resp, status, err := installPluginCore(r.Context(), trust, installer, audit, username, entry, art, req.TrustUnsigned, req.TrustNewKey, trustUnsignedAllowed)
	if errors.Is(err, errHubTrustUnsignedForbidden) {
		writeJSONError(w, http.StatusForbidden, "trust_unsigned_forbidden", errHubTrustUnsignedForbidden.Error())
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "plugin install failed")
		return
	}
	writeJSON(w, status, resp)
}

// installPluginCore applies the identical unsigned/untrusted/invalid signature
// gate importBundleCore applies to blueprints — same blueprint.VerifySignature
// primitive, same TrustStore, same explicit-trust flags — then, only once the
// trust decision permits, installs the manifest through T-1702's registry
// (installer.Install), which re-validates the capability scope. It returns the
// wire response plus HTTP status; a non-nil error is an install (registry)
// failure. Every outcome is audited under "hub.install" so a denied or
// completed plugin install is always visible in GET /audit.
//
// T-2104 AC2: before any trust decision is even considered, the capability
// scope and extension points GET /hub/index showed the operator (entry) must
// agree with the artifact actually being installed (art.Manifest) — the
// catalog's own display data is never installed from, but an operator's
// consent was given to what the catalog showed, and installing something
// else would be routing around the very capability-gate review this hub
// exists to preserve. A disagreement refuses the install outright, signed or
// not, trusted or not: hub.CapabilityMismatch.
func installPluginCore(ctx context.Context, trust BlueprintTrustStore, installer PluginInstaller, audit blueprintBundleAuditor, username string, entry hub.Entry, art hub.PluginArtifact, trustUnsigned, trustNewKey, trustUnsignedAllowed bool) (hubInstallResponse, int, error) {
	if mismatch := hub.CapabilityMismatch(entry, art.Manifest); mismatch != "" {
		auditHubInstallDetail(ctx, audit, username, "plugin", art.Manifest.ID, hubStatusCapabilityMismatch, "", "", mismatch)
		return hubInstallResponse{Type: string(hub.TypePlugin), Status: hubStatusCapabilityMismatch}, http.StatusOK, nil
	}
	msg, err := hub.CanonicalManifestBytes(art.Manifest)
	if err != nil {
		return hubInstallResponse{}, 0, err
	}

	// Unsigned artifact: same default-reject-unless-trustUnsigned gate as an
	// unsigned blueprint bundle — and (T-2904) the request flag alone is not
	// sufficient: the server config must also allow unsigned trust
	// ([hub] trust_unsigned = true), or the request is refused with the
	// config key named. Signed artifacts never reach this branch and are
	// never affected by either flag.
	if art.Signature == nil {
		if !trustUnsigned {
			auditHubInstall(ctx, audit, username, "plugin", art.Manifest.ID, bundleStatusUnsigned, "", "")
			return hubInstallResponse{Type: string(hub.TypePlugin), Status: bundleStatusUnsigned}, http.StatusOK, nil
		}
		if !trustUnsignedAllowed {
			auditHubInstallDetail(ctx, audit, username, "plugin", art.Manifest.ID, hubStatusTrustUnsignedForbidden, "", "", errHubTrustUnsignedForbidden.Error())
			return hubInstallResponse{}, 0, errHubTrustUnsignedForbidden
		}
		return finishPluginInstall(ctx, installer, audit, username, art, "trustUnsigned", "")
	}

	verified, fingerprint, verr := blueprint.VerifySignature(art.Signature, msg)
	if verr != nil || !verified {
		auditHubInstall(ctx, audit, username, "plugin", art.Manifest.ID, bundleStatusInvalidSignature, "", fingerprint)
		return hubInstallResponse{Type: string(hub.TypePlugin), Status: bundleStatusInvalidSignature}, http.StatusOK, nil
	}
	_, alreadyTrusted, getErr := trust.Get(fingerprint)
	if getErr != nil {
		return hubInstallResponse{}, 0, getErr
	}
	if alreadyTrusted {
		return finishPluginInstall(ctx, installer, audit, username, art, "alreadyTrusted", fingerprint)
	}
	if trustNewKey {
		signer := blueprint.TrustedSigner{
			Fingerprint: fingerprint,
			PublicKey:   art.Signature.PublicKey,
			AddedBy:     username,
			AddedAt:     time.Now().Unix(),
		}
		if err := trust.Add(signer); err != nil {
			return hubInstallResponse{}, 0, err
		}
		return finishPluginInstall(ctx, installer, audit, username, art, "trustNewKey", fingerprint)
	}
	auditHubInstall(ctx, audit, username, "plugin", art.Manifest.ID, bundleStatusUntrustedSignature, "", fingerprint)
	return hubInstallResponse{
		Type:   string(hub.TypePlugin),
		Status: bundleStatusUntrustedSignature,
		Signer: &bundleSignerResponse{Fingerprint: fingerprint, PublicKey: art.Signature.PublicKey},
	}, http.StatusOK, nil
}

// finishPluginInstall converts the verified manifest and installs it through
// T-1702's registry (which enforces the capability scope), then audits and
// returns the success response.
func finishPluginInstall(ctx context.Context, installer PluginInstaller, audit blueprintBundleAuditor, username string, art hub.PluginArtifact, trustDecision, fingerprint string) (hubInstallResponse, int, error) {
	m := toPluginManifest(art.Manifest)
	if err := installer.Install(ctx, username, m); err != nil {
		return hubInstallResponse{}, 0, err
	}
	auditHubInstall(ctx, audit, username, "plugin", m.ID, hubStatusInstalled, trustDecision, fingerprint)
	return hubInstallResponse{
		Type:   string(hub.TypePlugin),
		Status: hubStatusInstalled,
		Plugin: &hubPluginInstalled{
			ID:              m.ID,
			Name:            m.Name,
			Version:         m.Version,
			Capabilities:    nonNilStrings(art.Manifest.Capabilities),
			ExtensionPoints: nonNilStrings(art.Manifest.ExtensionPoints),
		},
	}, http.StatusCreated, nil
}

// toPluginManifest converts a hub wire manifest into an internal/plugin.Manifest
// for installation. No validation happens here — plugin.Registry.Install
// validates identity, api version, transport, extension points, and the
// capability scope, refusing anything malformed.
func toPluginManifest(m hub.PluginManifest) plugin.Manifest {
	points := make([]plugin.ExtensionPoint, 0, len(m.ExtensionPoints))
	for _, ep := range m.ExtensionPoints {
		points = append(points, plugin.ExtensionPoint(ep))
	}
	return plugin.Manifest{
		ID:              m.ID,
		Name:            m.Name,
		Version:         m.Version,
		APIVersion:      m.APIVersion,
		Transport:       plugin.Transport(m.Transport),
		Endpoint:        m.Endpoint,
		ExtensionPoints: points,
		Capabilities:    m.Capabilities,
	}
}

// auditHubInstall records one hub-install decision. A nil audit sink is a silent
// no-op (narrow unit tests). Result is "success" for a completed install,
// "denied" for any gate rejection.
func auditHubInstall(ctx context.Context, audit blueprintBundleAuditor, username, itemType, id, status, trustDecision, signerFingerprint string) {
	auditHubInstallDetail(ctx, audit, username, itemType, id, status, trustDecision, signerFingerprint, "")
}

// auditHubInstallDetail is auditHubInstall plus an optional free-text reason
// (e.g. hub.CapabilityMismatch's description) recorded in the entry's detail
// so a human reviewing GET /audit can see *why* a gate refused, not just that
// it did.
func auditHubInstallDetail(ctx context.Context, audit blueprintBundleAuditor, username, itemType, id, status, trustDecision, signerFingerprint, reason string) {
	if audit == nil {
		return
	}
	detail := map[string]any{"type": itemType, "id": id, "status": status}
	if trustDecision != "" {
		detail["trustDecision"] = trustDecision
	}
	if signerFingerprint != "" {
		detail["signerFingerprint"] = signerFingerprint
	}
	if reason != "" {
		detail["reason"] = reason
	}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		return
	}
	result := "success"
	if status != hubStatusInstalled && status != bundleStatusImported {
		result = "denied"
	}
	entry := store.AuditEntry{At: time.Now().Unix(), Username: username, Action: "hub.install", Result: result}
	entry.Target.String, entry.Target.Valid = id, true
	entry.DetailJSON.String, entry.DetailJSON.Valid = string(detailJSON), true
	_, _ = audit.Append(ctx, entry)
}
