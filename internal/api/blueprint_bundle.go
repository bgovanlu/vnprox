// blueprint_bundle.go implements T-1107's blueprint sharing bundles
// (docs/features/blueprints.md §5, docs/api.md's Blueprint bundles
// section):
//
//   - GET    /blueprints/{id}/bundle?sign=   — export a (optionally signed) Bundle
//   - GET    /blueprints/signing-key         — this install's own signing public key
//   - POST   /blueprints/import              — verify + trust-gate + save a Bundle
//   - GET    /blueprint-signers              — list pinned (trusted) signers
//   - POST   /blueprint-signers              — pin a new trusted signer
//   - DELETE /blueprint-signers/{fingerprint} — un-pin a trusted signer
//
// Signature verification itself (internal/blueprint.VerifyBundle) is pure
// cryptography with no notion of trust; this file is what layers a
// TrustStore lookup and the documented explicit-trust-step gate on top of
// it — an unsigned bundle, or one signed by a key not in the trust store,
// is never imported unless the request explicitly sets trustUnsigned or
// trustNewKey, and both of those paths (plus the plain already-trusted
// path) are audited as `blueprint.import` with the trust decision recorded
// (docs/security.md's Audit section).

package api

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/store"
)

// maxBundleBodyBytes bounds a POST /blueprints/import request body — a
// bundle is a single blueprint plus a small signature block, so this
// ceiling (matching maxBlueprintBodyBytes) is generous headroom, not a
// realistic limit.
const maxBundleBodyBytes = 4 << 20 // 4 MiB

// BlueprintTrustStore is the subset of *blueprint.TrustStore the router
// needs — declared as an interface (this package's standard seam pattern)
// so tests can substitute an in-memory double without touching a real
// filesystem trust-store directory.
type BlueprintTrustStore interface {
	List() ([]blueprint.TrustedSigner, error)
	Get(fingerprint string) (blueprint.TrustedSigner, bool, error)
	Add(s blueprint.TrustedSigner) error
	Delete(fingerprint string) error
}

// blueprintBundleAuditor is the minimal audit-log seam this file needs —
// *store.AuditRepo satisfies it directly, the same narrow-seam pattern
// lldpInstallAuditor (lldpinstall.go) already establishes for a
// write-mostly route family.
type blueprintBundleAuditor interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

// bundleSignatureResponse mirrors blueprint.BundleSignature for the wire
// (identical shape; a distinct type only so this file doesn't leak
// internal/blueprint's struct tags as this package's own public contract
// by accident, matching every other toXResponse convention in this
// package).
type bundleSignatureResponse struct {
	Alg                  string `json:"alg"`
	PublicKeyFingerprint string `json:"publicKeyFingerprint"`
	PublicKey            string `json:"publicKey"`
	Sig                  string `json:"sig"`
}

//nolint:govet // fieldalignment: response DTO; field order is the JSON shape, not packing.
type bundleResponse struct {
	Blueprint     *blueprint.Blueprint     `json:"blueprint"`
	Signature     *bundleSignatureResponse `json:"signature,omitempty"`
	BundleVersion int                      `json:"bundleVersion"`
}

func toBundleResponse(b blueprint.Bundle) bundleResponse {
	resp := bundleResponse{BundleVersion: b.BundleVersion, Blueprint: &b.Blueprint}
	if b.Signature != nil {
		resp.Signature = &bundleSignatureResponse{
			Alg: b.Signature.Alg, PublicKeyFingerprint: b.Signature.PublicKeyFingerprint,
			PublicKey: b.Signature.PublicKey, Sig: b.Signature.Sig,
		}
	}
	return resp
}

func toBundleSignature(r *bundleSignatureResponse) *blueprint.BundleSignature {
	if r == nil {
		return nil
	}
	return &blueprint.BundleSignature{
		Alg: r.Alg, PublicKeyFingerprint: r.PublicKeyFingerprint, PublicKey: r.PublicKey, Sig: r.Sig,
	}
}

// bundleRequest is the wire shape POST /blueprints/import decodes: a Bundle
// (bundleVersion/blueprint/signature) plus the two explicit-trust flags
// (docs/api.md's Blueprint bundles section).
//
//nolint:govet // fieldalignment: request DTO; field order is the JSON shape, not packing.
type bundleRequest struct {
	Signature     *bundleSignatureResponse `json:"signature,omitempty"`
	Blueprint     blueprint.Blueprint      `json:"blueprint"`
	BundleVersion int                      `json:"bundleVersion"`
	TrustUnsigned bool                     `json:"trustUnsigned,omitempty"`
	TrustNewKey   bool                     `json:"trustNewKey,omitempty"`
}

// Import status values (docs/api.md's Blueprint bundles section): the four
// distinct outcomes AC1-5 (T-1107's task card) each test for.
const (
	bundleStatusImported           = "imported"
	bundleStatusUnsigned           = "unsigned"
	bundleStatusUntrustedSignature = "untrustedSignature"
	bundleStatusInvalidSignature   = "invalidSignature"
)

type bundleSignerResponse struct {
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"publicKey"`
	Label       string `json:"label,omitempty"`
	AddedBy     string `json:"addedBy,omitempty"`
	AddedAt     int64  `json:"addedAt"`
}

func toBundleSignerResponse(s blueprint.TrustedSigner) bundleSignerResponse {
	return bundleSignerResponse{
		Fingerprint: s.Fingerprint, PublicKey: s.PublicKey,
		Label: s.Label, AddedBy: s.AddedBy, AddedAt: s.AddedAt,
	}
}

//nolint:govet // fieldalignment: response DTO; field order is the JSON shape, not packing.
type bundleImportResponse struct {
	Blueprint *blueprint.Blueprint  `json:"blueprint,omitempty"`
	Signer    *bundleSignerResponse `json:"signer,omitempty"`
	Status    string                `json:"status"`
}

type signingKeyResponse struct {
	Alg         string `json:"alg"`
	PublicKey   string `json:"publicKey"`
	Fingerprint string `json:"fingerprint"`
}

// mountBlueprintBundleRoutes registers the routes above. Reads (bundle
// export, signing-key export, signer list) are netRead-gated like every
// other read route in this package; writes (import, pin/un-pin a signer)
// are netWrite + CSRF, matching blueprints.go's own gate. Every dependency
// is nil-safe: a missing one simply skips mounting the routes that need it,
// the same degraded-mode convention every other mountXRoutes function in
// this package follows.
func mountBlueprintBundleRoutes(r chi.Router, svc BlueprintService, signingKey ed25519.PrivateKey, trust BlueprintTrustStore, audit blueprintBundleAuditor, auth AuthService) {
	if auth == nil {
		return
	}

	if svc != nil {
		r.Group(func(r chi.Router) {
			r.Use(auth.SessionMiddleware)
			r.Use(auth.RequireCap(capNetRead))
			r.Get("/blueprints/{id}/bundle", handleExportBundle(svc, signingKey))
		})
	}

	if len(signingKey) > 0 {
		r.Group(func(r chi.Router) {
			r.Use(auth.SessionMiddleware)
			r.Use(auth.RequireCap(capNetRead))
			r.Get("/blueprints/signing-key", handleSigningKey(signingKey))
		})
	}

	if trust != nil {
		r.Group(func(r chi.Router) {
			r.Use(auth.SessionMiddleware)
			r.Use(auth.RequireCap(capNetRead))
			r.Get("/blueprint-signers", handleListBlueprintSigners(trust))
		})
	}

	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}

	if svc != nil && trust != nil {
		r.Group(func(r chi.Router) {
			r.Use(auth.SessionMiddleware)
			if csrf, ok := auth.(CSRFEnforcer); ok {
				r.Use(csrf.CSRFMiddleware)
			}
			r.Use(auth.RequireCap(capNetWrite))
			r.Post("/blueprints/import", handleImportBundle(svc, trust, audit, lookup))
		})
	}

	if trust != nil {
		r.Group(func(r chi.Router) {
			r.Use(auth.SessionMiddleware)
			if csrf, ok := auth.(CSRFEnforcer); ok {
				r.Use(csrf.CSRFMiddleware)
			}
			r.Use(auth.RequireCap(capNetWrite))
			r.Post("/blueprint-signers", handleAddBlueprintSigner(trust, audit, lookup))
			r.Delete("/blueprint-signers/{fingerprint}", handleDeleteBlueprintSigner(trust, audit, lookup))
		})
	}
}

// parseSignQuery reports whether ?sign= asks for a signed bundle — any
// value other than absent/""/"0"/"false" is treated as true, matching the
// permissive boolean-query-param convention docs/api.md's other ?flag=
// parameters (e.g. GET /snapshots's own filters) already use.
func parseSignQuery(r *http.Request) bool {
	switch r.URL.Query().Get("sign") {
	case "", "0", "false":
		return false
	default:
		return true
	}
}

func handleExportBundle(svc BlueprintService, signingKey ed25519.PrivateKey) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bp, err := svc.Get(r.Context(), id)
		if err != nil {
			writeBlueprintError(w, err)
			return
		}
		var priv ed25519.PrivateKey
		if parseSignQuery(r) {
			priv = signingKey
		}
		bundle, err := blueprint.SignBundle(*bp, priv)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not sign bundle")
			return
		}
		writeJSON(w, http.StatusOK, toBundleResponse(bundle))
	}
}

func handleSigningKey(signingKey ed25519.PrivateKey) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pub, ok := signingKey.Public().(ed25519.PublicKey)
		if !ok {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "signing key has no Ed25519 public half")
			return
		}
		writeJSON(w, http.StatusOK, signingKeyResponse{
			Alg:         blueprint.SignatureAlgEd25519,
			PublicKey:   base64.StdEncoding.EncodeToString(pub),
			Fingerprint: blueprint.Fingerprint(pub),
		})
	}
}

func handleListBlueprintSigners(trust BlueprintTrustStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := trust.List()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list trusted signers")
			return
		}
		items := make([]bundleSignerResponse, 0, len(list))
		for _, s := range list {
			items = append(items, toBundleSignerResponse(s))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

type addSignerRequest struct {
	PublicKey string `json:"publicKey"`
	Label     string `json:"label,omitempty"`
}

func handleAddBlueprintSigner(trust BlueprintTrustStore, audit blueprintBundleAuditor, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var req addSignerRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBundleBodyBytes))
		if err := dec.Decode(&req); err != nil || req.PublicKey == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", `request body must be {"publicKey": "<base64>", "label"?: "<string>"}`)
			return
		}
		pubBytes, err := base64.StdEncoding.DecodeString(req.PublicKey)
		if err != nil || len(pubBytes) != ed25519.PublicKeySize {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "publicKey must be a base64-encoded 32-byte Ed25519 public key")
			return
		}
		signer := blueprint.TrustedSigner{
			Fingerprint: blueprint.Fingerprint(pubBytes),
			PublicKey:   req.PublicKey,
			Label:       req.Label,
			AddedBy:     username,
			AddedAt:     time.Now().Unix(),
		}
		if err := trust.Add(signer); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save trusted signer")
			return
		}
		auditBlueprintSigner(r.Context(), audit, username, "blueprint.signer.add", signer.Fingerprint, "ok", "")
		writeJSON(w, http.StatusCreated, toBundleSignerResponse(signer))
	}
}

func handleDeleteBlueprintSigner(trust BlueprintTrustStore, audit blueprintBundleAuditor, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		fingerprint := chi.URLParam(r, "fingerprint")
		err := trust.Delete(fingerprint)
		if err != nil {
			result := "error"
			if errors.Is(err, blueprint.ErrNotFound) {
				auditBlueprintSigner(r.Context(), audit, username, "blueprint.signer.delete", fingerprint, result, err.Error())
				writeJSONError(w, http.StatusNotFound, "not_found", "no such trusted signer")
				return
			}
			auditBlueprintSigner(r.Context(), audit, username, "blueprint.signer.delete", fingerprint, result, err.Error())
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not delete trusted signer")
			return
		}
		auditBlueprintSigner(r.Context(), audit, username, "blueprint.signer.delete", fingerprint, "ok", "")
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleImportBundle implements POST /blueprints/import's documented
// trust-decision logic (docs/api.md's Blueprint bundles section):
//
//  1. No signature at all -> imported only if trustUnsigned; otherwise
//     status "unsigned".
//  2. A signature present but which doesn't cryptographically verify
//     (malformed, or the blueprint content was tampered with after
//     signing) -> status "invalidSignature", never imported regardless of
//     any trust flag.
//  3. A signature that verifies, signed by a fingerprint already in the
//     trust store -> imported immediately, no trust flag needed.
//  4. A signature that verifies, signed by a fingerprint *not* in the
//     trust store -> imported only if trustNewKey (which also pins the key
//     for future imports); otherwise status "untrustedSignature" (the
//     response's signer field carries the fingerprint+publicKey so a UI
//     can offer "trust this signer" without a second round trip to fetch
//     it).
func handleImportBundle(svc BlueprintService, trust BlueprintTrustStore, audit blueprintBundleAuditor, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		var req bundleRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBundleBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed bundle body: "+err.Error())
			return
		}
		if req.BundleVersion != blueprint.CurrentBundleVersion {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "bundleVersion must be 1")
			return
		}

		bundle := blueprint.Bundle{
			BundleVersion: req.BundleVersion,
			Blueprint:     req.Blueprint,
			Signature:     toBundleSignature(req.Signature),
		}

		verified, fingerprint, verr := blueprint.VerifyBundle(bundle)

		switch {
		case bundle.Signature == nil:
			if !req.TrustUnsigned {
				auditBlueprintImport(r.Context(), audit, username, bundleStatusUnsigned, false, "", "")
				writeJSON(w, http.StatusOK, bundleImportResponse{Status: bundleStatusUnsigned})
				return
			}
			saved, err := saveImportedBlueprint(r.Context(), svc, username, req.Blueprint)
			if err != nil {
				writeBlueprintError(w, err)
				return
			}
			auditBlueprintImport(r.Context(), audit, username, bundleStatusImported, false, "trustUnsigned", "")
			writeJSON(w, http.StatusCreated, bundleImportResponse{Status: bundleStatusImported, Blueprint: saved})
			return

		case verr != nil || !verified:
			auditBlueprintImport(r.Context(), audit, username, bundleStatusInvalidSignature, false, "", fingerprint)
			writeJSON(w, http.StatusOK, bundleImportResponse{Status: bundleStatusInvalidSignature})
			return
		}

		// Signature verifies against its own embedded key; decide trust.
		_, alreadyTrusted, getErr := trust.Get(fingerprint)
		if getErr != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not check trust store")
			return
		}

		if alreadyTrusted {
			saved, err := saveImportedBlueprint(r.Context(), svc, username, req.Blueprint)
			if err != nil {
				writeBlueprintError(w, err)
				return
			}
			auditBlueprintImport(r.Context(), audit, username, bundleStatusImported, true, "alreadyTrusted", fingerprint)
			writeJSON(w, http.StatusCreated, bundleImportResponse{Status: bundleStatusImported, Blueprint: saved})
			return
		}

		if req.TrustNewKey {
			signer := blueprint.TrustedSigner{
				Fingerprint: fingerprint, PublicKey: bundle.Signature.PublicKey,
				AddedBy: username, AddedAt: time.Now().Unix(),
			}
			if err := trust.Add(signer); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not pin new signer")
				return
			}
			saved, err := saveImportedBlueprint(r.Context(), svc, username, req.Blueprint)
			if err != nil {
				writeBlueprintError(w, err)
				return
			}
			auditBlueprintImport(r.Context(), audit, username, bundleStatusImported, true, "trustNewKey", fingerprint)
			writeJSON(w, http.StatusCreated, bundleImportResponse{Status: bundleStatusImported, Blueprint: saved})
			return
		}

		auditBlueprintImport(r.Context(), audit, username, bundleStatusUntrustedSignature, false, "", fingerprint)
		writeJSON(w, http.StatusOK, bundleImportResponse{
			Status: bundleStatusUntrustedSignature,
			Signer: &bundleSignerResponse{Fingerprint: fingerprint, PublicKey: bundle.Signature.PublicKey},
		})
	}
}

// saveImportedBlueprint always mints a new saved blueprint (clears id and
// readOnly) rather than overwriting an existing one — a shared bundle's
// author-assigned id has no relationship to this installation's own saved
// blueprints, so silently overwriting whatever local blueprint happens to
// share that id would be a surprising, un-audited mutation of unrelated
// data. This mirrors docs/api.md's pre-existing file-level import
// convention ("id cleared so a new blueprint is created").
func saveImportedBlueprint(ctx context.Context, svc BlueprintService, author string, bp blueprint.Blueprint) (*blueprint.Blueprint, error) {
	bp.ID = ""
	bp.ReadOnly = false
	bp.CreatedBy = ""
	return svc.Save(ctx, author, &bp)
}

func auditBlueprintImport(ctx context.Context, audit blueprintBundleAuditor, username, status string, trusted bool, trustDecision, signerFingerprint string) {
	if audit == nil {
		return
	}
	detail := map[string]any{"status": status, "trusted": trusted}
	if trustDecision != "" {
		detail["trustDecision"] = trustDecision
	}
	if signerFingerprint != "" {
		detail["signerFingerprint"] = signerFingerprint
	}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		return
	}
	result := "ok"
	if status != bundleStatusImported {
		result = "denied"
	}
	entry := store.AuditEntry{At: time.Now().Unix(), Username: username, Action: "blueprint.import", Result: result}
	entry.DetailJSON.String, entry.DetailJSON.Valid = string(detailJSON), true
	_, _ = audit.Append(ctx, entry)
}

func auditBlueprintSigner(ctx context.Context, audit blueprintBundleAuditor, username, action, fingerprint, result, errMsg string) {
	if audit == nil {
		return
	}
	detail := map[string]string{"fingerprint": fingerprint}
	if errMsg != "" {
		detail["detail"] = errMsg
	}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		return
	}
	entry := store.AuditEntry{At: time.Now().Unix(), Username: username, Action: action, Result: result}
	entry.Target.String, entry.Target.Valid = fingerprint, true
	entry.DetailJSON.String, entry.DetailJSON.Valid = string(detailJSON), true
	_, _ = audit.Append(ctx, entry)
}
