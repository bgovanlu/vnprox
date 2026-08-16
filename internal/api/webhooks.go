// webhooks.go implements T-1104's webhook registration routes (docs/api.md's
// Webhooks section):
//
//   - POST   /webhooks       — register a delivery target {url, events?, secret}
//   - GET    /webhooks       — list registrations (secret never echoed back)
//   - DELETE /webhooks/{id}  — remove a registration
//
// Gated on the "automation" capability (internal/auth.CapAutomation),
// per caps.go's doc comment: "gates the WS 'events' topic and the webhook
// registration routes below" — since Automation is never derived from a
// PVE privilege (only ever present on a bearer token's own minted scopes,
// see Capabilities.Automation), these routes are automation-token-only in
// practice, matching this task's "no frontend UI deliverable" scope note
// in tokens.go's doc comment. CSRF middleware is still wired for
// defense-in-depth/consistency with every other mutating route in this
// package, even though a bearer-authenticated request (the only kind that
// can ever carry the automation capability) skips the check anyway.
//
// Delivery itself (signing, retry/backoff, consecutive-failure tracking)
// is internal/automation's job — this file only owns the CRUD surface and
// the store.Webhook <-> automation.Webhook secret-decryption seam,
// mirroring alertrules.go's own AlertRule <-> findings.AlertRule split.

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/store"
)

// capAutomation is docs/api.md's documented automation-scope capability
// flag name (internal/auth.CapAutomation's underlying string), spelled out
// as a plain string for the same reason capNetRead/capNetWrite are.
const capAutomation = "automation"

// maxWebhookBodyBytes bounds a POST /webhooks request body, matching
// maxAlertRuleBodyBytes' reasoning.
const maxWebhookBodyBytes = 16 << 10 // 16 KiB

// WebhookStore is the subset of *store.WebhookRepo the router needs.
type WebhookStore interface {
	Create(ctx context.Context, w store.Webhook) error
	Get(ctx context.Context, id string) (store.Webhook, error)
	List(ctx context.Context) ([]store.Webhook, error)
	Delete(ctx context.Context, id string) error
}

type webhookResponse struct {
	LastAttemptAt       *int64   `json:"lastAttemptAt,omitempty"`
	LastSuccessAt       *int64   `json:"lastSuccessAt,omitempty"`
	LastError           string   `json:"lastError,omitempty"`
	ID                  string   `json:"id"`
	URL                 string   `json:"url"`
	CreatedBy           string   `json:"createdBy"`
	Events              []string `json:"events,omitempty"`
	CreatedAt           int64    `json:"createdAt"`
	ConsecutiveFailures int      `json:"consecutiveFailures"`
}

type webhooksListResponse struct {
	Items []webhookResponse `json:"items"`
}

// webhookRequest is POST /webhooks' request body. Secret is required (it
// is the HMAC signing key every delivery is authenticated with — there is
// no "leave unchanged"/PUT update route in this task, unlike alert
// rules' three-way TargetSecret contract).
type webhookRequest struct {
	URL    string   `json:"url"`
	Secret string   `json:"secret"`
	Events []string `json:"events,omitempty"`
}

func decodeWebhookEvents(raw store.Webhook) []string {
	if !raw.EventsJSON.Valid || raw.EventsJSON.String == "" {
		return nil
	}
	var events []string
	if err := json.Unmarshal([]byte(raw.EventsJSON.String), &events); err != nil {
		return nil
	}
	return events
}

// eventsJSONColumn encodes events (POST /webhooks' optional `events`
// allowlist) into the nullable webhooks.events_json column shape: empty/nil
// stores SQL NULL ("every event", the same optional-filter convention
// alert_rules' source/severity filters use), a non-empty list stores its
// JSON encoding.
func eventsJSONColumn(events []string) sql.NullString {
	if len(events) == 0 {
		return sql.NullString{}
	}
	b, err := json.Marshal(events)
	if err != nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(b), Valid: true}
}

func toWebhookResponse(w store.Webhook) webhookResponse {
	resp := webhookResponse{
		ID: w.ID, URL: w.URL, Events: decodeWebhookEvents(w),
		CreatedBy: w.CreatedBy, CreatedAt: w.CreatedAt,
		ConsecutiveFailures: w.ConsecutiveFailures,
	}
	if w.LastAttemptAt.Valid {
		v := w.LastAttemptAt.Int64
		resp.LastAttemptAt = &v
	}
	if w.LastSuccessAt.Valid {
		v := w.LastSuccessAt.Int64
		resp.LastSuccessAt = &v
	}
	if w.LastError.Valid {
		resp.LastError = w.LastError.String
	}
	return resp
}

func validateWebhookRequest(req webhookRequest, targetCheck func(string) error) string {
	u, err := url.ParseRequestURI(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "url must be an absolute http(s) URL"
	}
	// T-2905: the destination policy (public-https-only by default, with
	// loud config escape hatches) — cmd/vnproxd wires
	// automation.TargetPolicy.ValidateURL here, and the dispatcher re-checks
	// the resolved address at every dial. Nil (tests without a policy)
	// preserves the pre-T-2905 shape.
	if targetCheck != nil {
		if err := targetCheck(req.URL); err != nil {
			return err.Error()
		}
	}
	if req.Secret == "" {
		return "secret is required"
	}
	return ""
}

// mountWebhookRoutes registers the routes above. Nil-safe: any missing
// dependency (webhooks store/cipher/auth) skips mounting the whole
// family, matching every other optional Options field's degraded-mode
// convention. If auth doesn't also implement UsernameLookup, the routes
// are likewise not mounted (same precedent as mountLayoutsRoutes).
func mountWebhookRoutes(r chi.Router, webhooks WebhookStore, cipher SecretCipher, audit tokenAuditor, auth AuthService, targetCheck func(string) error) {
	if webhooks == nil || cipher == nil || audit == nil || auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capAutomation))
		r.Get("/webhooks", handleListWebhooks(webhooks))
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capAutomation))
		r.Post("/webhooks", handleCreateWebhook(webhooks, cipher, audit, lookup, targetCheck))
		r.Delete("/webhooks/{id}", handleDeleteWebhook(webhooks, audit, lookup))
	})
}

func handleListWebhooks(webhooks WebhookStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := webhooks.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list webhooks")
			return
		}
		items := make([]webhookResponse, 0, len(list))
		for _, wh := range list {
			items = append(items, toWebhookResponse(wh))
		}
		writeJSON(w, http.StatusOK, webhooksListResponse{Items: items})
	}
}

func handleCreateWebhook(webhooks WebhookStore, cipher SecretCipher, audit tokenAuditor, lookup UsernameLookup, targetCheck func(string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		var req webhookRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed webhook body: "+err.Error())
			return
		}
		if msg := validateWebhookRequest(req, targetCheck); msg != "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", msg)
			return
		}

		secretEnc, err := cipher.Encrypt([]byte(req.Secret))
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not encrypt webhook secret")
			return
		}

		wh := store.Webhook{
			ID: store.NewULID(), URL: req.URL, SecretEnc: secretEnc,
			EventsJSON: eventsJSONColumn(req.Events),
			CreatedBy:  username,
			CreatedAt:  time.Now().Unix(),
		}
		if err := webhooks.Create(r.Context(), wh); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save webhook")
			return
		}

		auditTokenAction(r.Context(), audit, username, "webhook.create", wh.ID, map[string]any{"url": wh.URL, "events": req.Events})
		writeJSON(w, http.StatusCreated, toWebhookResponse(wh))
	}
}

func handleDeleteWebhook(webhooks WebhookStore, audit tokenAuditor, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		if _, err := webhooks.Get(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such webhook")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not look up webhook")
			return
		}
		if err := webhooks.Delete(r.Context(), id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not delete webhook")
			return
		}
		auditTokenAction(r.Context(), audit, username, "webhook.delete", id, nil)
		w.WriteHeader(http.StatusNoContent)
	}
}
