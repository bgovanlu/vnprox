// push.go implements T-2005's web-push subscription routes:
//
//   - GET    /push/vapid-public-key      — this daemon's VAPID identity, the
//     `applicationServerKey` a browser's `PushManager.subscribe()` needs
//   - POST   /push/subscriptions          — register a subscription, tied to
//     the caller's own session and opted into a subset of internal/push's
//     closed category vocabulary (critical/awaitingConfirm/drift)
//   - GET    /push/subscriptions          — list the caller's own devices
//     (never the endpoint/keys — see toPushSubscriptionResponse)
//   - DELETE /push/subscriptions/{id}     — revoke one of the caller's own
//     devices
//
// Gated on nothing beyond being authenticated (SessionMiddleware, no
// RequireCap) — matching tokens.go's "no capability required to call POST
// /tokens" precedent: a push subscription is a personal notification
// preference about the caller's OWN device, not a privileged operation, and
// docs/roadmap-universal.md's Phase 17 exit demo ("the on-call human
// confirms it from their phone") does not presuppose netWrite. What a
// subscriber can actually DO once a notification deep-links them into the
// app is governed entirely by their session's own capabilities at that
// moment (T-2005's card: "confirming still requires an authenticated
// session with the capability — the notification is a deep link, never an
// action token"), enforced by the routes that already exist, not by
// anything here.
//
// Subscriptions are scoped to the caller's own username the same way
// tokens.go scopes GET/DELETE /tokens — returning or letting anyone revoke
// another user's registered devices would be a needless information-
// disclosure/DoS surface, per that file's identical reasoning.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/push"
	"github.com/bgovanlu/vnprox/internal/store"
)

// maxPushSubscriptionBodyBytes bounds a POST /push/subscriptions request
// body — an endpoint URL, two short base64 keys, and a handful of category
// strings, generous headroom against an abusive/buggy client, matching
// maxWebhookBodyBytes' reasoning.
const maxPushSubscriptionBodyBytes = 8 << 10 // 8 KiB

// maxDeviceLabelRunes caps the client-supplied, free-text, display-only
// device label (0046's migration doc comment: "display-only ... never
// parsed") well short of anything that could be mistaken for a real
// identifier field, without being so short a legitimate "iPhone 15 —
// Safari" description gets truncated.
const maxDeviceLabelRunes = 120

// PushSubscriptionStore is the subset of *store.PushSubscriptionRepo the
// router needs.
type PushSubscriptionStore interface {
	Create(ctx context.Context, s store.PushSubscription) error
	GetByEndpointHash(ctx context.Context, hash string) (store.PushSubscription, error)
	DeleteByEndpointHash(ctx context.Context, hash string) error
	Get(ctx context.Context, id string) (store.PushSubscription, error)
	ListByUsername(ctx context.Context, username string) ([]store.PushSubscription, error)
	Delete(ctx context.Context, id string) error
}

type pushSubscriptionResponse struct {
	LastUsedAt  *int64   `json:"lastUsedAt,omitempty"`
	ID          string   `json:"id"`
	DeviceLabel string   `json:"deviceLabel,omitempty"`
	Categories  []string `json:"categories"`
	CreatedAt   int64    `json:"createdAt"`
}

type pushSubscriptionsListResponse struct {
	Items []pushSubscriptionResponse `json:"items"`
}

type vapidPublicKeyResponse struct {
	Key string `json:"key"`
}

// pushSubscriptionRequest is POST /push/subscriptions' request body,
// mirroring the browser's `PushSubscription.toJSON()` shape (`endpoint` +
// a `keys` object) plus this app's own `categories`/`deviceLabel` fields.
type pushSubscriptionRequest struct {
	Keys struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	Endpoint    string   `json:"endpoint"`
	DeviceLabel string   `json:"deviceLabel,omitempty"`
	Categories  []string `json:"categories"`
}

func decodePushCategories(categoriesJSON string) []string {
	var cats []string
	if err := json.Unmarshal([]byte(categoriesJSON), &cats); err != nil {
		return []string{}
	}
	return cats
}

func toPushSubscriptionResponse(s store.PushSubscription) pushSubscriptionResponse {
	resp := pushSubscriptionResponse{
		ID: s.ID, DeviceLabel: s.DeviceLabel,
		Categories: decodePushCategories(s.CategoriesJSON),
		CreatedAt:  s.CreatedAt,
	}
	if s.LastUsedAt.Valid {
		v := s.LastUsedAt.Int64
		resp.LastUsedAt = &v
	}
	return resp
}

func truncateDeviceLabel(label string) string {
	if utf8.RuneCountInString(label) <= maxDeviceLabelRunes {
		return label
	}
	runes := []rune(label)
	return string(runes[:maxDeviceLabelRunes])
}

// mountPushRoutes registers the routes above. Nil-safe: any missing
// dependency (subscription store/cipher/audit/auth) skips mounting the
// whole family, matching every other optional Options field's degraded-
// mode convention. vapidPublicKey empty also skips mounting — an instance
// with no VAPID identity configured (cmd/vnproxd's setupPush degrades
// gracefully if key generation ever failed non-fatally, though in practice
// it returns an error and the daemon does not start) has nothing to offer
// a subscribing browser.
func mountPushRoutes(r chi.Router, subs PushSubscriptionStore, cipher SecretCipher, audit tokenAuditor, vapidPublicKey string, auth AuthService) {
	if subs == nil || cipher == nil || audit == nil || auth == nil || vapidPublicKey == "" {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}
	sessions, ok := auth.(SessionLookup)
	if !ok {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Get("/push/vapid-public-key", handleGetVAPIDPublicKey(vapidPublicKey))
		r.Get("/push/subscriptions", handleListPushSubscriptions(subs, lookup))
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Post("/push/subscriptions", handleCreatePushSubscription(subs, cipher, audit, lookup, sessions))
		r.Delete("/push/subscriptions/{id}", handleDeletePushSubscription(subs, audit, lookup))
	})
}

func handleGetVAPIDPublicKey(key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, vapidPublicKeyResponse{Key: key})
	}
}

func handleListPushSubscriptions(subs PushSubscriptionStore, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		list, err := subs.ListByUsername(r.Context(), username)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list push subscriptions")
			return
		}
		items := make([]pushSubscriptionResponse, 0, len(list))
		for _, s := range list {
			items = append(items, toPushSubscriptionResponse(s))
		}
		writeJSON(w, http.StatusOK, pushSubscriptionsListResponse{Items: items})
	}
}

func handleCreatePushSubscription(subs PushSubscriptionStore, cipher SecretCipher, audit tokenAuditor, lookup UsernameLookup, sessions SessionLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		sessionID, ok := sessions.SessionID(r.Context())
		if !ok || sessionID == "" {
			// A push subscription is meaningless without a session to die
			// with (0046's "subscriptions are per-session and die with it"
			// — the FK this repo relies on needs a real session row to
			// reference). A bearer-token caller (no session) has nothing
			// valid to attach one to.
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "push subscriptions require a browser session, not a bearer token")
			return
		}

		var req pushSubscriptionRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPushSubscriptionBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed push subscription body: "+err.Error())
			return
		}

		sub, err := push.ParseSubscription(req.Endpoint, req.Keys.P256dh, req.Keys.Auth)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
			return
		}
		categories, err := push.ParseCategories(req.Categories)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
			return
		}
		categoriesJSON, err := json.Marshal(categories)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not encode categories")
			return
		}

		endpointEnc, err := cipher.Encrypt([]byte(sub.Endpoint))
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not encrypt subscription")
			return
		}
		p256dhEnc, err := cipher.Encrypt([]byte(sub.P256dh))
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not encrypt subscription")
			return
		}
		authEnc, err := cipher.Encrypt([]byte(sub.Auth))
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not encrypt subscription")
			return
		}

		hash := push.EndpointHash(sub.Endpoint)
		// A browser resubscribing to the SAME push service endpoint (e.g.
		// after re-granting permission, or a fresh login on the same
		// device) replaces its prior registration rather than
		// accumulating duplicates — the endpoint identifies the device+
		// browser+origin triple, so a second row for it would just be
		// stale state nothing ever prunes.
		if _, getErr := subs.GetByEndpointHash(r.Context(), hash); getErr == nil {
			if delErr := subs.DeleteByEndpointHash(r.Context(), hash); delErr != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not replace existing subscription")
				return
			}
		} else if !errors.Is(getErr, store.ErrNotFound) {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not check for an existing subscription")
			return
		}

		row := store.PushSubscription{
			ID: store.NewULID(), SessionID: sessionID, Username: username,
			EndpointHash: hash, EndpointEnc: endpointEnc, P256dhEnc: p256dhEnc, AuthEnc: authEnc,
			CategoriesJSON: string(categoriesJSON), DeviceLabel: truncateDeviceLabel(req.DeviceLabel),
			CreatedAt: time.Now().Unix(),
		}
		if err := subs.Create(r.Context(), row); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save push subscription")
			return
		}

		auditTokenAction(r.Context(), audit, username, "push.subscribe", row.ID, map[string]any{
			"categories": categories, "deviceLabel": row.DeviceLabel,
		})
		writeJSON(w, http.StatusCreated, toPushSubscriptionResponse(row))
	}
}

func handleDeletePushSubscription(subs PushSubscriptionStore, audit tokenAuditor, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		existing, err := subs.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such push subscription")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not look up push subscription")
			return
		}
		if existing.Username != username {
			// Same "404, not 403" convention tokens.go's DELETE uses: never
			// confirm another user's subscription id exists.
			writeJSONError(w, http.StatusNotFound, "not_found", "no such push subscription")
			return
		}

		if err := subs.Delete(r.Context(), id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not revoke push subscription")
			return
		}
		auditTokenAction(r.Context(), audit, username, "push.unsubscribe", id, nil)
		w.WriteHeader(http.StatusNoContent)
	}
}
