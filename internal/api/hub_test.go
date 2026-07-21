package api

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/hub"
	"github.com/bgovanlu/vnprox/internal/plugin"
)

// fakeHubClient is an in-memory registry double: no network, no signature
// checking — it only returns the index and the artifacts the test wired.
type fakeHubClient struct {
	bundles  map[string]blueprint.Bundle
	plugins  map[string]hub.PluginArtifact
	indexErr error
	index    hub.Index
}

func (f *fakeHubClient) Index(context.Context) (hub.Index, error) { return f.index, f.indexErr }

func (f *fakeHubClient) FetchBlueprintBundle(_ context.Context, e hub.Entry) (blueprint.Bundle, error) {
	return f.bundles[e.ID], nil
}

func (f *fakeHubClient) FetchPluginArtifact(_ context.Context, e hub.Entry) (hub.PluginArtifact, error) {
	return f.plugins[e.ID], nil
}

// fakeInstaller records the manifests handed to it — standing in for T-1702's
// registry, which the real cmd/vnproxd adapter forwards to (and which
// re-validates the capability scope).
type fakeInstaller struct {
	installed []plugin.Manifest
}

func (f *fakeInstaller) Install(_ context.Context, _ string, m plugin.Manifest) error {
	f.installed = append(f.installed, m)
	return nil
}

func newHubTestRouter(t *testing.T, client HubClient, vetting HubVetting, svc BlueprintService, trust BlueprintTrustStore, installer PluginInstaller, audit blueprintBundleAuditor, auth AuthService) http.Handler {
	t.Helper()
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, HubClient: client, HubVetting: vetting,
		Blueprints: svc, BlueprintTrust: trust, PluginInstaller: installer, BlueprintSignersAudit: audit,
	})
}

func signPluginArtifact(t *testing.T, m hub.PluginManifest, priv ed25519.PrivateKey) hub.PluginArtifact {
	t.Helper()
	msg, err := hub.CanonicalManifestBytes(m)
	if err != nil {
		t.Fatalf("CanonicalManifestBytes: %v", err)
	}
	pub, _ := priv.Public().(ed25519.PublicKey)
	sig := &blueprint.BundleSignature{
		Alg:                  blueprint.SignatureAlgEd25519,
		PublicKeyFingerprint: blueprint.Fingerprint(pub),
		PublicKey:            base64Std(pub),
		Sig:                  base64Std(ed25519.Sign(priv, msg)),
	}
	return hub.PluginArtifact{Manifest: m, Signature: sig}
}

func base64Std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// TestHubIndex_TypedAndFilterable is T-1705 AC1.
func TestHubIndex_TypedAndFilterable(t *testing.T) {
	client := &fakeHubClient{index: hub.Index{
		SchemaVersion: hub.CurrentIndexSchema,
		Entries: []hub.Entry{
			{Type: hub.TypeBlueprint, ID: "bp-a", Name: "BP A", Version: "1.0"},
			{Type: hub.TypePlugin, ID: "pl-a", Name: "PL A", Version: "2.0", Capabilities: []string{"netRead"}, ExtensionPoints: []string{"dashboardTile"}},
		},
	}}
	auth := blueprintTestAuth(map[string]bool{"netRead": true})
	r := newHubTestRouter(t, client, nil, nil, nil, nil, nil, auth)

	rec := do(t, r, http.MethodGet, "/api/v1/hub/index?type=plugin", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp hubIndexResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != "pl-a" {
		t.Fatalf("items = %+v, want only pl-a", resp.Items)
	}
	if len(resp.Items[0].Capabilities) != 1 || resp.Items[0].Capabilities[0] != "netRead" {
		t.Fatalf("capabilities = %v, want [netRead] surfaced for review", resp.Items[0].Capabilities)
	}

	// An invalid type is a 400.
	if bad := do(t, r, http.MethodGet, "/api/v1/hub/index?type=widget", nil); bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid type status = %d, want 400", bad.Code)
	}
}

// TestHubInstall_SignedTrustedBlueprint is T-1705 AC2: a signed blueprint from
// an already-trusted signer installs via T-1107's exact import path.
func TestHubInstall_SignedTrustedBlueprint(t *testing.T) {
	svc := newBlueprintTestService(t, "pve1")
	trust := blueprint.NewTrustStore(t.TempDir())
	audit := &fakeAuditor{}

	_, priv, _ := ed25519.GenerateKey(nil)
	bp := testBlueprint("hub-bp")
	signed, err := blueprint.SignBundle(bp, priv)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	// Pre-trust the signer.
	if addErr := trust.Add(blueprint.TrustedSigner{Fingerprint: signed.Signature.PublicKeyFingerprint, PublicKey: signed.Signature.PublicKey}); addErr != nil {
		t.Fatalf("trust.Add: %v", addErr)
	}

	client := &fakeHubClient{
		index:   hub.Index{SchemaVersion: hub.CurrentIndexSchema, Entries: []hub.Entry{{Type: hub.TypeBlueprint, ID: "hub-bp", Version: "1.0"}}},
		bundles: map[string]blueprint.Bundle{"hub-bp": signed},
	}
	auth := blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true})
	r := newHubTestRouter(t, client, nil, svc, trust, nil, audit, auth)

	rec := do(t, r, http.MethodPost, "/api/v1/hub/install", map[string]any{"type": "blueprint", "id": "hub-bp", "version": "1.0"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var resp hubInstallResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != bundleStatusImported || resp.Blueprint == nil {
		t.Fatalf("resp = %+v, want imported with a blueprint", resp)
	}
	// The reused import path audited a blueprint.import (identical shape).
	if len(audit.entries) != 1 || audit.entries[0].Action != "blueprint.import" || audit.entries[0].Result != "ok" {
		t.Fatalf("audit = %+v, want one ok blueprint.import", audit.entries)
	}
}

// TestHubInstall_UnsignedBlueprintRejected is T-1705 AC3: an unsigned bundle is
// rejected without trustUnsigned, with the identical status + audit shape as a
// direct import.
func TestHubInstall_UnsignedBlueprintRejected(t *testing.T) {
	svc := newBlueprintTestService(t, "pve1")
	trust := blueprint.NewTrustStore(t.TempDir())
	audit := &fakeAuditor{}
	unsigned := blueprint.Bundle{BundleVersion: blueprint.CurrentBundleVersion, Blueprint: testBlueprint("u")}
	client := &fakeHubClient{
		index:   hub.Index{SchemaVersion: hub.CurrentIndexSchema, Entries: []hub.Entry{{Type: hub.TypeBlueprint, ID: "u"}}},
		bundles: map[string]blueprint.Bundle{"u": unsigned},
	}
	auth := blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true})
	r := newHubTestRouter(t, client, nil, svc, trust, nil, audit, auth)

	rec := do(t, r, http.MethodPost, "/api/v1/hub/install", map[string]any{"type": "blueprint", "id": "u"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp hubInstallResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != bundleStatusUnsigned {
		t.Fatalf("status = %s, want %s", resp.Status, bundleStatusUnsigned)
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != "blueprint.import" || audit.entries[0].Result != "denied" {
		t.Fatalf("audit = %+v, want one denied blueprint.import (identical to T-1107)", audit.entries)
	}

	// With trustUnsigned it imports.
	rec2 := do(t, r, http.MethodPost, "/api/v1/hub/install", map[string]any{"type": "blueprint", "id": "u", "trustUnsigned": true})
	if rec2.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec2.Code, rec2.Body.String())
	}
}

// TestHubInstall_PluginRegistersWithCapabilities is T-1705 AC4: installing a
// plugin registers it through the plugin-registry seam with its declared
// capability set surfaced.
func TestHubInstall_PluginRegistersWithCapabilities(t *testing.T) {
	trust := blueprint.NewTrustStore(t.TempDir())
	audit := &fakeAuditor{}
	installer := &fakeInstaller{}

	_, priv, _ := ed25519.GenerateKey(nil)
	pub, _ := priv.Public().(ed25519.PublicKey)
	// Pre-trust the plugin publisher.
	if err := trust.Add(blueprint.TrustedSigner{Fingerprint: blueprint.Fingerprint(pub), PublicKey: base64Std(pub)}); err != nil {
		t.Fatalf("trust.Add: %v", err)
	}
	m := hub.PluginManifest{ID: "acme-tiles", Name: "Acme Tiles", Version: "1.0", APIVersion: "v1", Transport: "grpc", Endpoint: "/opt/acme", ExtensionPoints: []string{"dashboardTile"}, Capabilities: []string{"netRead"}}
	art := signPluginArtifact(t, m, priv)

	client := &fakeHubClient{
		index:   hub.Index{SchemaVersion: hub.CurrentIndexSchema, Entries: []hub.Entry{{Type: hub.TypePlugin, ID: "acme-tiles", Version: "1.0", Capabilities: []string{"netRead"}}}},
		plugins: map[string]hub.PluginArtifact{"acme-tiles": art},
	}
	auth := blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true})
	r := newHubTestRouter(t, client, nil, nil, trust, installer, audit, auth)

	rec := do(t, r, http.MethodPost, "/api/v1/hub/install", map[string]any{"type": "plugin", "id": "acme-tiles", "version": "1.0"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var resp hubInstallResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != hubStatusInstalled || resp.Plugin == nil {
		t.Fatalf("resp = %+v, want installed with plugin detail", resp)
	}
	if len(resp.Plugin.Capabilities) != 1 || resp.Plugin.Capabilities[0] != "netRead" {
		t.Fatalf("plugin capabilities = %v, want [netRead]", resp.Plugin.Capabilities)
	}
	if len(installer.installed) != 1 || installer.installed[0].ID != "acme-tiles" {
		t.Fatalf("installer.installed = %+v, want acme-tiles registered", installer.installed)
	}
	if got := installer.installed[0].Capabilities; len(got) != 1 || got[0] != "netRead" {
		t.Fatalf("registered capabilities = %v, want [netRead]", got)
	}
}

// TestHubInstall_UnsignedPluginRejected: a plugin whose artifact is unsigned is
// never handed to the registry without trustUnsigned.
func TestHubInstall_UnsignedPluginRejected(t *testing.T) {
	trust := blueprint.NewTrustStore(t.TempDir())
	installer := &fakeInstaller{}
	m := hub.PluginManifest{ID: "unsigned-pl", Name: "U", Version: "1", APIVersion: "v1", Transport: "grpc", ExtensionPoints: []string{"dashboardTile"}, Capabilities: []string{"netRead"}}
	client := &fakeHubClient{
		index:   hub.Index{SchemaVersion: hub.CurrentIndexSchema, Entries: []hub.Entry{{Type: hub.TypePlugin, ID: "unsigned-pl"}}},
		plugins: map[string]hub.PluginArtifact{"unsigned-pl": {Manifest: m}},
	}
	auth := blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true})
	r := newHubTestRouter(t, client, nil, nil, trust, installer, &fakeAuditor{}, auth)

	rec := do(t, r, http.MethodPost, "/api/v1/hub/install", map[string]any{"type": "plugin", "id": "unsigned-pl"})
	var resp hubInstallResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != bundleStatusUnsigned {
		t.Fatalf("status = %s, want %s", resp.Status, bundleStatusUnsigned)
	}
	if len(installer.installed) != 0 {
		t.Fatalf("installer must not be reached for an unsigned plugin, got %+v", installer.installed)
	}
}

// TestHubInstall_VettedButUntrusted is T-1705 AC5: a "vetted" signer (in the
// hub's allowlist) that this installation hasn't trusted still requires an
// explicit trust step — the vetted badge never bypasses the trust decision.
func TestHubInstall_VettedButUntrusted(t *testing.T) {
	svc := newBlueprintTestService(t, "pve1")
	trust := blueprint.NewTrustStore(t.TempDir()) // empty: signer NOT trusted here
	audit := &fakeAuditor{}

	_, priv, _ := ed25519.GenerateKey(nil)
	bp := testBlueprint("vetted-bp")
	signed, _ := blueprint.SignBundle(bp, priv)
	fp := signed.Signature.PublicKeyFingerprint

	// The signer IS in the hub's vetted allowlist...
	vetting := hub.NewVettedSet([]string{fp})
	client := &fakeHubClient{
		index: hub.Index{SchemaVersion: hub.CurrentIndexSchema, Entries: []hub.Entry{{
			Type: hub.TypeBlueprint, ID: "vetted-bp", Version: "1.0",
			Signature: &blueprint.BundleSignature{Alg: signed.Signature.Alg, PublicKeyFingerprint: fp, PublicKey: signed.Signature.PublicKey, Sig: signed.Signature.Sig},
		}}},
		bundles: map[string]blueprint.Bundle{"vetted-bp": signed},
	}
	auth := blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true})
	r := newHubTestRouter(t, client, vetting, svc, trust, nil, audit, auth)

	// The index shows it vetted...
	idxRec := do(t, r, http.MethodGet, "/api/v1/hub/index?type=blueprint", nil)
	var idxResp hubIndexResponse
	_ = json.Unmarshal(idxRec.Body.Bytes(), &idxResp)
	if len(idxResp.Items) != 1 || !idxResp.Items[0].Vetted {
		t.Fatalf("entry.Vetted = %+v, want vetted badge present", idxResp.Items)
	}

	// ...but installing it still hits the untrusted-signature gate.
	rec := do(t, r, http.MethodPost, "/api/v1/hub/install", map[string]any{"type": "blueprint", "id": "vetted-bp"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp hubInstallResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != bundleStatusUntrustedSignature {
		t.Fatalf("status = %s, want %s (vetted must NOT bypass trust)", resp.Status, bundleStatusUntrustedSignature)
	}

	// A non-vetted entry gets no badge.
	client.index.Entries[0].Signature.PublicKeyFingerprint = "deadbeef"
	idxRec2 := do(t, r, http.MethodGet, "/api/v1/hub/index?type=blueprint", nil)
	var idxResp2 hubIndexResponse
	_ = json.Unmarshal(idxRec2.Body.Bytes(), &idxResp2)
	if idxResp2.Items[0].Vetted {
		t.Fatal("a non-allowlisted fingerprint must not be vetted")
	}
}

// TestHubRoutes_NotMountedWithoutClient: no hub client -> routes absent.
func TestHubRoutes_NotMountedWithoutClient(t *testing.T) {
	auth := blueprintTestAuth(map[string]bool{"netRead": true})
	r := NewRouter(Options{Version: "test", DistFS: testDistFS(), Logger: testLogger(), Auth: auth})
	if rec := do(t, r, http.MethodGet, "/api/v1/hub/index", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route not mounted)", rec.Code)
	}
}
