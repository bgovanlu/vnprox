// SPDX-License-Identifier: Apache-2.0

// alertrules.go implements T-1005's alert routing CRUD + delivery log
// (docs/api.md's Alert Rules section):
//
//   - GET    /alert-rules            — list every routing rule (secrets never echoed back)
//   - POST   /alert-rules            — create a rule
//   - GET    /alert-rules/{id}       — single rule detail
//   - PUT    /alert-rules/{id}       — update a rule
//   - DELETE /alert-rules/{id}       — remove a rule
//   - POST   /alert-rules/{id}/test  — deliver a synthetic finding through the rule's target now
//   - GET    /alert-deliveries       — delivery log, ?ruleId=&status= both optional/ANDed
//
// Reads are gated by netRead (same as every other read route in this
// package); writes (create/update/delete/test — test performs a real
// outbound HTTP call and writes a delivery-log row, so it is treated as a
// write, not a read) are gated by netWrite + CSRF, matching blueprints.go's
// own gate.
//
// Secrets (Gotify/ntfy tokens) are encrypted at rest via SecretCipher — in
// production *store.SessionCipher, the same AES-256-GCM cipher session PVE
// tickets use (docs/security.md) — and this package never returns
// plaintext or ciphertext back to the client: GET responses carry only
// `hasSecret bool`, matching GET /config's "deliberately excludes every
// secret" contract. A PUT's `targetSecret` field is a *string so the three
// states are distinguishable: absent/null (leave the existing secret
// untouched), `""` (clear it), non-empty (replace it).

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/store"
)

// maxAlertRuleBodyBytes bounds a create/update request body — generous
// headroom for a routing rule, mirroring maxBlueprintBodyBytes/
// maxAnnotationBodyBytes' reasoning.
const maxAlertRuleBodyBytes = 16 << 10 // 16 KiB

// alertTestClientTimeout bounds POST /alert-rules/{id}/test's outbound
// call: this handler blocks the HTTP response on a real delivery to an
// operator-supplied URL, so it must never hang indefinitely on an
// unreachable target.
const alertTestClientTimeout = 10 * time.Second

var alertTestClient = &http.Client{Timeout: alertTestClientTimeout}

// validAlertTargetKinds is docs/api.md's Alert Rules targetKind vocabulary.
var validAlertTargetKinds = map[string]bool{
	findings.TargetGeneric: true,
	findings.TargetGotify:  true,
	findings.TargetNtfy:    true,
	findings.TargetSlack:   true,
}

// AlertRuleStore is the subset of *store.AlertRuleRepo the router needs.
type AlertRuleStore interface {
	List(ctx context.Context) ([]store.AlertRule, error)
	Get(ctx context.Context, id string) (store.AlertRule, error)
	Insert(ctx context.Context, a store.AlertRule) error
	Update(ctx context.Context, a store.AlertRule) error
	Delete(ctx context.Context, id string) error
}

// AlertDeliveryStore is the subset of *store.AlertDeliveryRepo the router
// needs.
type AlertDeliveryStore interface {
	List(ctx context.Context, ruleID, status string) ([]store.AlertDelivery, error)
	Insert(ctx context.Context, d store.AlertDelivery) error
}

// SecretCipher is the subset of *store.SessionCipher the router needs to
// encrypt/decrypt AlertRule.TargetSecretEnc — declared as an interface (the
// same seam pattern every other cross-package dependency in this file
// uses) so tests can substitute a fake cipher without real AES-GCM key
// material.
type SecretCipher interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(sealed []byte) ([]byte, error)
}

type alertRuleResponse struct {
	QuietStart              string   `json:"quietStart,omitempty"`
	ID                      string   `json:"id"`
	TargetKind              string   `json:"targetKind"`
	TargetURL               string   `json:"targetUrl"`
	QuietTZ                 string   `json:"quietTz,omitempty"`
	QuietEnd                string   `json:"quietEnd,omitempty"`
	Name                    string   `json:"name"`
	SourceFilter            []string `json:"sourceFilter,omitempty"`
	SeverityFilter          []string `json:"severityFilter,omitempty"`
	DigestWindowSec         int64    `json:"digestWindowSec"`
	CreatedAt               int64    `json:"createdAt"`
	UpdatedAt               int64    `json:"updatedAt"`
	BypassQuietHoursOnError bool     `json:"bypassQuietHoursOnError"`
	Enabled                 bool     `json:"enabled"`
	HasSecret               bool     `json:"hasSecret"`
}

type alertRulesListResponse struct {
	Items []alertRuleResponse `json:"items"`
}

// alertRuleRequest is both POST /alert-rules' and PUT /alert-rules/{id}'s
// request body. TargetSecret is a *string so PUT can distinguish "leave
// unchanged" (nil) from "clear" ("") from "replace" (non-empty) — see this
// file's doc comment.
type alertRuleRequest struct {
	TargetSecret            *string  `json:"targetSecret,omitempty"`
	BypassQuietHoursOnError *bool    `json:"bypassQuietHoursOnError,omitempty"`
	Name                    string   `json:"name"`
	TargetKind              string   `json:"targetKind"`
	TargetURL               string   `json:"targetUrl"`
	QuietStart              string   `json:"quietStart,omitempty"`
	QuietEnd                string   `json:"quietEnd,omitempty"`
	QuietTZ                 string   `json:"quietTz,omitempty"`
	SourceFilter            []string `json:"sourceFilter,omitempty"`
	SeverityFilter          []string `json:"severityFilter,omitempty"`
	DigestWindowSec         int64    `json:"digestWindowSec,omitempty"`
	Enabled                 bool     `json:"enabled"`
}

type alertDeliveryResponse struct {
	ID        string `json:"id"`
	RuleID    string `json:"ruleId"`
	FindingID string `json:"findingId"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	// Detail (T-2407) says why a delivery was deferred, or what a coalesced
	// one contained. Distinct from error: a deferral is not a failure.
	Detail  string `json:"detail,omitempty"`
	At      int64  `json:"at"`
	Attempt int    `json:"attempt"`
}

type alertDeliveriesListResponse struct {
	Items []alertDeliveryResponse `json:"items"`
}

type alertRuleTestResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func toAlertRuleResponse(a store.AlertRule) alertRuleResponse {
	return alertRuleResponse{
		ID: a.ID, Name: a.Name, Enabled: a.Enabled,
		SourceFilter: a.SourceFilter, SeverityFilter: a.SeverityFilter,
		TargetKind: a.TargetKind, TargetURL: a.TargetURL,
		HasSecret: len(a.TargetSecretEnc) > 0,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
		QuietStart: a.QuietStart, QuietEnd: a.QuietEnd, QuietTZ: a.QuietTZ,
		DigestWindowSec:         a.DigestWindowSec,
		BypassQuietHoursOnError: a.QuietBypassError,
	}
}

func toAlertDeliveryResponse(d store.AlertDelivery) alertDeliveryResponse {
	return alertDeliveryResponse{
		ID: d.ID, RuleID: d.RuleID, FindingID: d.FindingID,
		At: d.At, Attempt: d.Attempt, Status: d.Status, Error: d.Error, Detail: d.Detail,
	}
}

// toFindingsAlertRule adapts a store row (plus its already-decrypted
// secret) into the findings.AlertRule shape internal/findings/webhook.go's
// Deliver/PayloadFor expect — this is the decoupling seam T-1005's task
// card describes (internal/findings never imports internal/store).
func toFindingsAlertRule(a store.AlertRule, secret string) findings.AlertRule {
	return findings.AlertRule{
		ID: a.ID, Name: a.Name, Enabled: a.Enabled,
		SourceFilter: a.SourceFilter, SeverityFilter: a.SeverityFilter,
		TargetKind: a.TargetKind, TargetURL: a.TargetURL, TargetSecret: secret,
		QuietHours: findings.QuietHours{
			Start: a.QuietStart, End: a.QuietEnd, Zone: a.QuietTZ,
		},
		BypassQuietHoursOnError: a.QuietBypassError,
		DigestWindow:            time.Duration(a.DigestWindowSec) * time.Second,
	}
}

func decryptAlertSecret(cipher SecretCipher, enc []byte) (string, error) {
	if len(enc) == 0 {
		return "", nil
	}
	plaintext, err := cipher.Decrypt(enc)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// validateAlertRuleRequest checks the shared shape both create and update
// use. targetUrl must be an absolute http(s) URL — every documented target
// kind (generic/Gotify/ntfy/Slack incoming-webhook) is delivered to over
// plain HTTP(S), never any other scheme.
func validateAlertRuleRequest(req alertRuleRequest) string {
	if req.Name == "" {
		return "name is required"
	}
	if !validAlertTargetKinds[req.TargetKind] {
		return "targetKind must be one of generic, gotify, ntfy, slack"
	}
	u, err := url.ParseRequestURI(req.TargetURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "targetUrl must be an absolute http(s) URL"
	}
	// T-2407. Validated here, at the boundary, rather than at delivery time:
	// a window that only turns out to be unparseable at 22:00 has already
	// cost the operator the night it was meant to protect.
	if err := (findings.QuietHours{Start: req.QuietStart, End: req.QuietEnd, Zone: req.QuietTZ}).Validate(); err != nil {
		return err.Error()
	}
	if req.DigestWindowSec < 0 {
		return "digestWindowSec must not be negative"
	}
	if req.DigestWindowSec > maxDigestWindowSec {
		return fmt.Sprintf("digestWindowSec must be at most %d (24h); a longer window is indistinguishable from silence", maxDigestWindowSec)
	}
	return ""
}

// maxDigestWindowSec caps the coalescing window at 24 hours. Beyond that a
// "digest" is not a digest, and an operator who typed an extra zero should
// find out from a 400 rather than from a week of unsent alerts.
const maxDigestWindowSec = 24 * 60 * 60

// bypassOrDefault resolves the optional bypassQuietHoursOnError field.
func bypassOrDefault(v *bool) bool {
	if v == nil {
		return store.DefaultQuietBypassError
	}
	return *v
}

// mountAlertRulesRoutes registers the routes above. Nil-safe: any missing
// dependency (rules/deliveries/cipher/auth) simply skips mounting every
// route in this file, matching every other optional Options field's
// degraded-mode convention.
func mountAlertRulesRoutes(r chi.Router, rules AlertRuleStore, deliveries AlertDeliveryStore, cipher SecretCipher, auth AuthService) {
	if rules == nil || deliveries == nil || cipher == nil || auth == nil {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/alert-rules", handleListAlertRules(rules))
		r.Get("/alert-rules/{id}", handleGetAlertRule(rules))
		r.Get("/alert-deliveries", handleListAlertDeliveries(deliveries))
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/alert-rules", handleCreateAlertRule(rules, cipher))
		r.Put("/alert-rules/{id}", handleUpdateAlertRule(rules, cipher))
		r.Delete("/alert-rules/{id}", handleDeleteAlertRule(rules))
		r.Post("/alert-rules/{id}/test", handleTestAlertRule(rules, deliveries, cipher))
	})
}

func handleListAlertRules(rules AlertRuleStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := rules.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list alert rules")
			return
		}
		items := make([]alertRuleResponse, 0, len(list))
		for _, a := range list {
			items = append(items, toAlertRuleResponse(a))
		}
		writeJSON(w, http.StatusOK, alertRulesListResponse{Items: items})
	}
}

func handleGetAlertRule(rules AlertRuleStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		a, err := rules.Get(r.Context(), id)
		if err != nil {
			writeAlertRuleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toAlertRuleResponse(a))
	}
}

func handleCreateAlertRule(rules AlertRuleStore, cipher SecretCipher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req alertRuleRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAlertRuleBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed alert rule body: "+err.Error())
			return
		}
		if msg := validateAlertRuleRequest(req); msg != "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", msg)
			return
		}

		var secretEnc []byte
		if req.TargetSecret != nil && *req.TargetSecret != "" {
			enc, err := cipher.Encrypt([]byte(*req.TargetSecret))
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not encrypt target secret")
				return
			}
			secretEnc = enc
		}

		now := time.Now().Unix()
		a := store.AlertRule{
			ID: store.NewULID(), Name: req.Name, Enabled: req.Enabled,
			SourceFilter: req.SourceFilter, SeverityFilter: req.SeverityFilter,
			TargetKind: req.TargetKind, TargetURL: req.TargetURL, TargetSecretEnc: secretEnc,
			CreatedAt: now, UpdatedAt: now,
			QuietStart: req.QuietStart, QuietEnd: req.QuietEnd, QuietTZ: req.QuietTZ,
			DigestWindowSec:  req.DigestWindowSec,
			QuietBypassError: bypassOrDefault(req.BypassQuietHoursOnError),
		}
		if err := rules.Insert(r.Context(), a); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save alert rule")
			return
		}
		writeJSON(w, http.StatusCreated, toAlertRuleResponse(a))
	}
}

func handleUpdateAlertRule(rules AlertRuleStore, cipher SecretCipher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		existing, err := rules.Get(r.Context(), id)
		if err != nil {
			writeAlertRuleError(w, err)
			return
		}

		var req alertRuleRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAlertRuleBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed alert rule body: "+err.Error())
			return
		}
		if msg := validateAlertRuleRequest(req); msg != "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", msg)
			return
		}

		secretEnc := existing.TargetSecretEnc
		if req.TargetSecret != nil {
			if *req.TargetSecret == "" {
				secretEnc = nil
			} else {
				enc, err := cipher.Encrypt([]byte(*req.TargetSecret))
				if err != nil {
					writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not encrypt target secret")
					return
				}
				secretEnc = enc
			}
		}

		updated := store.AlertRule{
			ID: existing.ID, Name: req.Name, Enabled: req.Enabled,
			SourceFilter: req.SourceFilter, SeverityFilter: req.SeverityFilter,
			TargetKind: req.TargetKind, TargetURL: req.TargetURL, TargetSecretEnc: secretEnc,
			CreatedAt: existing.CreatedAt, UpdatedAt: time.Now().Unix(),
			QuietStart: req.QuietStart, QuietEnd: req.QuietEnd, QuietTZ: req.QuietTZ,
			DigestWindowSec:  req.DigestWindowSec,
			QuietBypassError: bypassOrDefault(req.BypassQuietHoursOnError),
		}
		if err := rules.Update(r.Context(), updated); err != nil {
			writeAlertRuleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toAlertRuleResponse(updated))
	}
}

func handleDeleteAlertRule(rules AlertRuleStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := rules.Delete(r.Context(), id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not delete alert rule")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// alertRuleTestFindingID is the synthetic finding id POST
// /alert-rules/{id}/test delivers — content-derived from the rule id (not
// random), so repeated test-sends against the same rule are visibly
// correlated in the delivery log.
func alertRuleTestFindingID(ruleID string) string { return "test:" + ruleID }

func handleTestAlertRule(rules AlertRuleStore, deliveries AlertDeliveryStore, cipher SecretCipher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		rule, err := rules.Get(r.Context(), id)
		if err != nil {
			writeAlertRuleError(w, err)
			return
		}
		secret, err := decryptAlertSecret(cipher, rule.TargetSecretEnc)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not decrypt target secret")
			return
		}

		findingID := alertRuleTestFindingID(rule.ID)
		synthetic := findings.Finding{
			ID: findingID, Source: findings.SourceHealth, Check: "alert_rule_test",
			Severity: findings.SeverityInfo,
			Detail:   "This is a test alert from vnprox. Your alert rule \"" + rule.Name + "\" is configured correctly if you received this.",
			Nodes:    []string{},
		}

		deliverErr := findings.Deliver(r.Context(), alertTestClient, toFindingsAlertRule(rule, secret), synthetic, findings.TransitionNew)
		resp := alertRuleTestResponse{Status: "delivered"}
		errMsg := ""
		if deliverErr != nil {
			resp.Status = "failed"
			resp.Error = deliverErr.Error()
			errMsg = deliverErr.Error()
		}

		d := store.AlertDelivery{
			ID: store.NewULID(), RuleID: rule.ID, FindingID: findingID,
			At: time.Now().Unix(), Attempt: 1, Status: resp.Status, Error: errMsg,
		}
		if err := deliveries.Insert(r.Context(), d); err != nil {
			// Delivery itself already happened; a logging failure must not
			// mask that outcome from the caller (mirrors WebhookNotifier's
			// own "recorder failure is logged, not fatal" convention) —
			// there's no logger threaded into this handler, so this is the
			// one place in this file that swallows an error silently on
			// purpose rather than surfacing a 500 for an already-completed
			// delivery.
			_ = err
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

func handleListAlertDeliveries(deliveries AlertDeliveryStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ruleID := r.URL.Query().Get("ruleId")
		status := r.URL.Query().Get("status")
		list, err := deliveries.List(r.Context(), ruleID, status)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list alert deliveries")
			return
		}
		items := make([]alertDeliveryResponse, 0, len(list))
		for _, d := range list {
			items = append(items, toAlertDeliveryResponse(d))
		}
		writeJSON(w, http.StatusOK, alertDeliveriesListResponse{Items: items})
	}
}

func writeAlertRuleError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "no such alert rule")
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "internal error")
}
