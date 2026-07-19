package api

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
)

// testBlueprint returns a minimal, always-valid Blueprint (one bridge
// entity, no params) so bundle tests don't need to exercise the full
// EntityTemplate/param machinery blueprints_test.go's other tests already
// cover.
func testBlueprint(id string) blueprint.Blueprint {
	return blueprint.Blueprint{
		BlueprintVersion: blueprint.CurrentBlueprintVersion,
		ID:               id,
		Name:             "test bundle blueprint",
		NodeSelector:     blueprint.NodeSelector{Mode: blueprint.SelectAll},
		Entities: []blueprint.EntityTemplate{
			{Kind: blueprint.KindBridge, IDTemplate: "vmbr9", Fields: map[string]any{"vlanAware": true}},
		},
	}
}

func newBlueprintBundleTestRouter(t *testing.T, svc BlueprintService, signingKey ed25519.PrivateKey, trust BlueprintTrustStore, audit blueprintBundleAuditor, auth AuthService) http.Handler {
	t.Helper()
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Blueprints: svc,
		BlueprintSigningKey: signingKey, BlueprintTrust: trust, BlueprintSignersAudit: audit,
	})
}

func do(t *testing.T, r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshaling request body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestExportBundle_RoundTripVerifies is T-1107 AC1: a signed export's
// signature verifies against its own exported public key.
func TestExportBundle_RoundTripVerifies(t *testing.T) {
	svc := newBlueprintTestService(t, "pve1")
	bp, err := svc.Save(t.Context(), "root@pam", ptr(testBlueprint("")))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	trust := blueprint.NewTrustStore(t.TempDir())
	auth := blueprintTestAuth(map[string]bool{"netRead": true})

	r := newBlueprintBundleTestRouter(t, svc, priv, trust, nil, auth)
	rec := do(t, r, http.MethodGet, "/api/v1/blueprints/"+bp.ID+"/bundle?sign=true", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp bundleResponse
	if decodeErr := json.Unmarshal(rec.Body.Bytes(), &resp); decodeErr != nil {
		t.Fatalf("decoding response: %v", decodeErr)
	}
	if resp.Signature == nil {
		t.Fatal("expected a signature on a ?sign=true export")
	}

	bundle := blueprint.Bundle{BundleVersion: resp.BundleVersion, Blueprint: *resp.Blueprint, Signature: toBundleSignature(resp.Signature)}
	verified, fp, err := blueprint.VerifyBundle(bundle)
	if err != nil || !verified {
		t.Fatalf("VerifyBundle: verified=%v err=%v", verified, err)
	}
	pub, _ := priv.Public().(ed25519.PublicKey)
	if want := blueprint.Fingerprint(pub); fp != want {
		t.Errorf("fingerprint = %s, want %s", fp, want)
	}

	// Unsigned export (sign omitted) carries no signature at all.
	rec2 := do(t, r, http.MethodGet, "/api/v1/blueprints/"+bp.ID+"/bundle", nil)
	var resp2 bundleResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decoding unsigned response: %v", err)
	}
	if resp2.Signature != nil {
		t.Error("expected no signature on a plain export")
	}
}

func TestSigningKeyRoute(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	auth := blueprintTestAuth(map[string]bool{"netRead": true})
	r := newBlueprintBundleTestRouter(t, nil, priv, blueprint.NewTrustStore(t.TempDir()), nil, auth)

	rec := do(t, r, http.MethodGet, "/api/v1/blueprints/signing-key", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp signingKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	pub, _ := priv.Public().(ed25519.PublicKey)
	if resp.Fingerprint != blueprint.Fingerprint(pub) {
		t.Errorf("fingerprint = %s, want %s", resp.Fingerprint, blueprint.Fingerprint(pub))
	}
	if resp.Alg != blueprint.SignatureAlgEd25519 {
		t.Errorf("alg = %s, want %s", resp.Alg, blueprint.SignatureAlgEd25519)
	}
}

// TestImportBundle_UnsignedRejectedByDefault is AC4: an unsigned bundle is
// rejected by default, and succeeds only with trustUnsigned: true.
func TestImportBundle_UnsignedRejectedByDefault(t *testing.T) {
	svc := newBlueprintTestService(t, "pve1")
	trust := blueprint.NewTrustStore(t.TempDir())
	audit := &fakeAuditor{}
	auth := blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true})
	r := newBlueprintBundleTestRouter(t, svc, nil, trust, audit, auth)

	body := map[string]any{"bundleVersion": 1, "blueprint": testBlueprint("shared-bp")}
	rec := do(t, r, http.MethodPost, "/api/v1/blueprints/import", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp bundleImportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Status != bundleStatusUnsigned {
		t.Fatalf("status = %s, want %s", resp.Status, bundleStatusUnsigned)
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != "blueprint.import" || audit.entries[0].Result != "denied" {
		t.Fatalf("audit entries = %+v, want one denied blueprint.import", audit.entries)
	}

	// Retry with trustUnsigned: true -> imported.
	body["trustUnsigned"] = true
	rec2 := do(t, r, http.MethodPost, "/api/v1/blueprints/import", body)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec2.Code, rec2.Body.String())
	}
	var resp2 bundleImportResponse
	if decodeErr := json.Unmarshal(rec2.Body.Bytes(), &resp2); decodeErr != nil {
		t.Fatalf("decoding response: %v", decodeErr)
	}
	if resp2.Status != bundleStatusImported || resp2.Blueprint == nil {
		t.Fatalf("resp2 = %+v, want imported with a blueprint", resp2)
	}
	if resp2.Blueprint.ID == "shared-bp" {
		t.Error("imported blueprint should have minted a new id, not kept the shared bundle's id")
	}

	if len(audit.entries) != 2 || audit.entries[1].Result != "ok" {
		t.Fatalf("audit entries = %+v, want a second ok entry", audit.entries)
	}

	list, err := svc.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, bp := range list {
		if bp.Name == "test bundle blueprint" {
			found = true
		}
	}
	if !found {
		t.Error("imported blueprint not found in saved list")
	}
}

// TestImportBundle_TrustDecisions is AC2/AC3/AC5: already-trusted signer
// imports immediately; an unknown signer is rejected without trustNewKey
// and succeeds with it (or once separately pinned via the signer store);
// tampering invalidates the signature.
func TestImportBundle_TrustDecisions(t *testing.T) {
	svc := newBlueprintTestService(t, "pve1")
	trust := blueprint.NewTrustStore(t.TempDir())
	audit := &fakeAuditor{}
	auth := blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true})
	r := newBlueprintBundleTestRouter(t, svc, nil, trust, audit, auth)

	_, priv, _ := ed25519.GenerateKey(nil)
	bp := testBlueprint("shared-bp-2")
	signed, err := blueprint.SignBundle(bp, priv)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	sigResp := &bundleSignatureResponse{
		Alg: signed.Signature.Alg, PublicKeyFingerprint: signed.Signature.PublicKeyFingerprint,
		PublicKey: signed.Signature.PublicKey, Sig: signed.Signature.Sig,
	}

	// 1. Unknown signer, no trustNewKey -> untrustedSignature, not imported.
	body := map[string]any{"bundleVersion": 1, "blueprint": bp, "signature": sigResp}
	rec := do(t, r, http.MethodPost, "/api/v1/blueprints/import", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp bundleImportResponse
	if decodeErr := json.Unmarshal(rec.Body.Bytes(), &resp); decodeErr != nil {
		t.Fatalf("decoding response: %v", decodeErr)
	}
	if resp.Status != bundleStatusUntrustedSignature {
		t.Fatalf("status = %s, want %s", resp.Status, bundleStatusUntrustedSignature)
	}
	if resp.Signer == nil || resp.Signer.Fingerprint != signed.Signature.PublicKeyFingerprint {
		t.Fatalf("signer = %+v, want fingerprint %s", resp.Signer, signed.Signature.PublicKeyFingerprint)
	}

	// 2. Same import with trustNewKey: true -> imported, and the signer is
	// now pinned in the trust store.
	body["trustNewKey"] = true
	rec2 := do(t, r, http.MethodPost, "/api/v1/blueprints/import", body)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec2.Code, rec2.Body.String())
	}
	var resp2 bundleImportResponse
	if decodeErr := json.Unmarshal(rec2.Body.Bytes(), &resp2); decodeErr != nil {
		t.Fatalf("decoding response: %v", decodeErr)
	}
	if resp2.Status != bundleStatusImported {
		t.Fatalf("status = %s, want %s", resp2.Status, bundleStatusImported)
	}
	if _, ok, _ := trust.Get(signed.Signature.PublicKeyFingerprint); !ok {
		t.Error("expected signer to be pinned in the trust store after trustNewKey import")
	}

	// 3. A fresh bundle from the *same now-trusted* signer imports
	// immediately, no trust flag needed.
	bp3 := testBlueprint("shared-bp-3")
	signed3, err := blueprint.SignBundle(bp3, priv)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	body3 := map[string]any{"bundleVersion": 1, "blueprint": bp3, "signature": bundleSignatureResponse{
		Alg: signed3.Signature.Alg, PublicKeyFingerprint: signed3.Signature.PublicKeyFingerprint,
		PublicKey: signed3.Signature.PublicKey, Sig: signed3.Signature.Sig,
	}}
	rec3 := do(t, r, http.MethodPost, "/api/v1/blueprints/import", body3)
	if rec3.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec3.Code, rec3.Body.String())
	}
	var resp3 bundleImportResponse
	if err := json.Unmarshal(rec3.Body.Bytes(), &resp3); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp3.Status != bundleStatusImported {
		t.Fatalf("status = %s, want %s", resp3.Status, bundleStatusImported)
	}

	// 4. Tampering the blueprint content after signing invalidates the
	// signature (AC5) — distinct from "unsigned".
	tampered := bp
	tampered.Name = "tampered name"
	bodyTampered := map[string]any{"bundleVersion": 1, "blueprint": tampered, "signature": sigResp}
	recTampered := do(t, r, http.MethodPost, "/api/v1/blueprints/import", bodyTampered)
	if recTampered.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recTampered.Code, recTampered.Body.String())
	}
	var respTampered bundleImportResponse
	if err := json.Unmarshal(recTampered.Body.Bytes(), &respTampered); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if respTampered.Status != bundleStatusInvalidSignature {
		t.Fatalf("status = %s, want %s", respTampered.Status, bundleStatusInvalidSignature)
	}
}

// TestImportBundle_TrustedViaSeparateSignerPost is the second half of AC3:
// "or after the key is separately added via POST /blueprint-signers".
func TestImportBundle_TrustedViaSeparateSignerPost(t *testing.T) {
	svc := newBlueprintTestService(t, "pve1")
	trust := blueprint.NewTrustStore(t.TempDir())
	audit := &fakeAuditor{}
	auth := blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true})
	r := newBlueprintBundleTestRouter(t, svc, nil, trust, audit, auth)

	_, priv, _ := ed25519.GenerateKey(nil)
	pub, _ := priv.Public().(ed25519.PublicKey)
	bp := testBlueprint("shared-bp-4")
	signed, err := blueprint.SignBundle(bp, priv)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}

	// Pin the signer first via POST /blueprint-signers.
	addRec := do(t, r, http.MethodPost, "/api/v1/blueprint-signers", map[string]any{
		"publicKey": signed.Signature.PublicKey, "label": "ci",
	})
	if addRec.Code != http.StatusCreated {
		t.Fatalf("POST /blueprint-signers status = %d, want 201: %s", addRec.Code, addRec.Body.String())
	}

	// Now import without any trust flag — succeeds because the signer is
	// already trusted.
	body := map[string]any{"bundleVersion": 1, "blueprint": bp, "signature": bundleSignatureResponse{
		Alg: signed.Signature.Alg, PublicKeyFingerprint: signed.Signature.PublicKeyFingerprint,
		PublicKey: signed.Signature.PublicKey, Sig: signed.Signature.Sig,
	}}
	rec := do(t, r, http.MethodPost, "/api/v1/blueprints/import", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var resp bundleImportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Status != bundleStatusImported {
		t.Fatalf("status = %s, want %s", resp.Status, bundleStatusImported)
	}

	// GET /blueprint-signers lists it.
	listRec := do(t, r, http.MethodGet, "/api/v1/blueprint-signers", nil)
	var listResp struct {
		Items []bundleSignerResponse `json:"items"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decoding list response: %v", err)
	}
	if len(listResp.Items) != 1 || listResp.Items[0].Fingerprint != blueprint.Fingerprint(pub) {
		t.Fatalf("signer list = %+v, want one entry with fingerprint %s", listResp.Items, blueprint.Fingerprint(pub))
	}

	// DELETE un-pins it, and audits the action.
	delRec := do(t, r, http.MethodDelete, "/api/v1/blueprint-signers/"+blueprint.Fingerprint(pub), nil)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204: %s", delRec.Code, delRec.Body.String())
	}
	if _, ok, _ := trust.Get(blueprint.Fingerprint(pub)); ok {
		t.Error("expected signer to be removed after DELETE")
	}

	var sawAdd, sawDelete bool
	for _, e := range audit.entries {
		if e.Action == "blueprint.signer.add" {
			sawAdd = true
		}
		if e.Action == "blueprint.signer.delete" {
			sawDelete = true
		}
	}
	if !sawAdd || !sawDelete {
		t.Errorf("audit entries = %+v, want both blueprint.signer.add and blueprint.signer.delete", audit.entries)
	}
}

func ptr[T any](v T) *T { return &v }
