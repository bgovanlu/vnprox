package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/push"
	"github.com/bgovanlu/vnprox/internal/store"
)

// pushVAPIDKeyFileName is T-2005's VAPID identity key's filename, placed
// alongside the daemon's OTHER at-rest keys rather than getting its own
// [push] config section: this is a deliberate scope decision (see this
// task's report) — the key's directory is derived from
// cfg.Blueprint.SigningKeyFile's own directory, the same one
// session.key/metrics.key/blueprint-signing.key already share
// (docs/security.md's Authentication section: "/etc/vnprox/keys/",
// already covered by the packaging ReadWritePaths a prior release added).
// This has one concrete, load-bearing benefit beyond avoiding a new config
// field: cmd/vnproxd/demo.go already routes
// Blueprint.SigningKeyFile into --demo's own sandboxed temp directory, so
// deriving from it means a demo run generates its VAPID key inside that
// SAME sandbox automatically, with no separate demo-config plumbing to
// keep in sync — a real risk this file's doc comment on
// pushVAPIDKeyFile below explains further.
const pushVAPIDKeyFileName = "push-vapid.key"

// pushVAPIDKeyFile derives T-2005's VAPID key path from
// cfg.Blueprint.SigningKeyFile's directory (this file's doc comment above
// explains why). Every code path that constructs a *config.Config already
// resolves Blueprint.SigningKeyFile to a non-empty default
// (internal/config's resolveBlueprintConfig), so this never falls back to
// a relative "." path in practice.
func pushVAPIDKeyFile(cfg *config.Config) string {
	dir := filepath.Dir(cfg.Blueprint.SigningKeyFile)
	return filepath.Join(dir, pushVAPIDKeyFileName)
}

// setupPushVAPIDKey loads — generating on first run if absent, the exact
// "generate if absent, 0600, belt-and-suspenders" convention
// setupBlueprintSigningKey/setupMetricsExporterToken already establish —
// this daemon's VAPID identity (docs/security.md's Authentication section
// documents the identical rule for every other at-rest key this codebase
// generates: "Ship no default key.").
func setupPushVAPIDKey(cfg *config.Config, logger *slog.Logger) (*ecdsa.PrivateKey, error) {
	path := pushVAPIDKeyFile(cfg)
	if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
		logger.Info("push: generating VAPID key", "path", path)
		if genErr := push.GenerateVAPIDKeyFile(path); genErr != nil {
			return nil, fmt.Errorf("generating push VAPID key: %w", genErr)
		}
	} else if statErr != nil {
		return nil, fmt.Errorf("checking push VAPID key file %s: %w", path, statErr)
	}

	priv, err := push.LoadVAPIDKeyFile(path)
	if err != nil {
		return nil, fmt.Errorf("loading push VAPID key: %w", err)
	}
	return priv, nil
}

// pushSecretCipher is the subset of *store.SessionCipher
// pushSubscriptionProviderAdapter needs — the same one-method-family seam
// webhookSecretCipher (automation.go) declares for the identical reason.
type pushSecretCipher interface {
	Decrypt(sealed []byte) ([]byte, error)
}

// pushSubscriptionStore is the subset of *store.PushSubscriptionRepo
// pushSubscriptionProviderAdapter needs.
type pushSubscriptionStore interface {
	ListAll(ctx context.Context) ([]store.PushSubscription, error)
	TouchLastUsed(ctx context.Context, id string, now int64) error
	Delete(ctx context.Context, id string) error
}

// pushSubscriptionProviderAdapter adapts *store.PushSubscriptionRepo (plus
// the session cipher) into push.Provider AND push.Tracker — the same
// decrypt-and-adapt composition-root conversion webhookProviderAdapter
// performs for T-1104's webhook Dispatcher (automation.go), so
// internal/push never imports internal/store directly. A subscription
// whose endpoint/keys fail to decrypt (a corrupt row, or a key rotated out
// from under an existing registration) is logged and skipped for this
// delivery rather than failing the whole fan-out — identical degraded
// handling to webhookProviderAdapter.Webhooks' own secret-decrypt loop.
type pushSubscriptionProviderAdapter struct {
	repo   pushSubscriptionStore
	cipher pushSecretCipher
	logger *slog.Logger
}

var _ push.Provider = pushSubscriptionProviderAdapter{}
var _ push.Tracker = pushSubscriptionProviderAdapter{}

func (a pushSubscriptionProviderAdapter) Subscriptions(ctx context.Context, category push.Category) ([]push.SubscriptionRecord, error) {
	rows, err := a.repo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("push: listing subscriptions: %w", err)
	}
	out := make([]push.SubscriptionRecord, 0, len(rows))
	for _, row := range rows {
		if !subscriptionWantsCategory(row.CategoriesJSON, category) {
			continue
		}
		endpoint, err := a.cipher.Decrypt(row.EndpointEnc)
		if err != nil {
			a.logger.Warn("push: decrypting subscription endpoint, skipping this delivery", "subscription_id", row.ID, "error", err)
			continue
		}
		p256dh, err := a.cipher.Decrypt(row.P256dhEnc)
		if err != nil {
			a.logger.Warn("push: decrypting subscription p256dh, skipping this delivery", "subscription_id", row.ID, "error", err)
			continue
		}
		auth, err := a.cipher.Decrypt(row.AuthEnc)
		if err != nil {
			a.logger.Warn("push: decrypting subscription auth, skipping this delivery", "subscription_id", row.ID, "error", err)
			continue
		}
		out = append(out, push.SubscriptionRecord{
			ID: row.ID,
			Subscription: push.Subscription{
				Endpoint: string(endpoint), P256dh: string(p256dh), Auth: string(auth),
			},
		})
	}
	return out, nil
}

func (a pushSubscriptionProviderAdapter) TouchLastUsed(ctx context.Context, id string, now int64) error {
	return a.repo.TouchLastUsed(ctx, id, now)
}

// Prune implements push.Tracker: a subscription the push service reports
// as permanently gone (push.ErrGone — HTTP 404/410) is deleted outright
// rather than merely marked, mirroring how a browser that revoked
// permission leaves nothing meaningful behind to keep.
func (a pushSubscriptionProviderAdapter) Prune(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, id)
}

func subscriptionWantsCategory(categoriesJSON string, category push.Category) bool {
	var cats []string
	if err := json.Unmarshal([]byte(categoriesJSON), &cats); err != nil {
		return false
	}
	for _, c := range cats {
		if push.Category(c) == category {
			return true
		}
	}
	return false
}

// vapidSubject is RFC 8292's required `sub` claim (a contact URI a push
// service may use about delivery problems) — this daemon has no operator
// email of its own to offer, so it uses a fixed, non-identifying
// "https://" URI naming the project rather than a mailto: nobody reads,
// matching every other "no operator identity to disclose" default this
// codebase picks (e.g. internal/telemetry's installId being a random
// ULID, never an email).
const pushVAPIDSubject = "https://github.com/bgovanlu/vnprox"

// setupPush builds T-2005's push.Dispatcher. It does NOT itself call
// ws.SetEventSink — internal/topology.Hub.SetEventSink stores a single func
// (hub.go's doc comment: "cmd/vnproxd wires this to internal/automation's
// webhook dispatcher"), and automation.go's setupAutomation already claims
// that slot for T-1104's webhook Dispatcher. server.go instead installs
// ONE combined closure, after both dispatchers exist, that calls the
// automation Dispatcher's Publish and then this one's — see server.go's
// construction-order comment right after both setup calls. Keeping the
// chaining at the call site (rather than a SetEventSink call buried in
// this function) means there is exactly one place in the whole daemon that
// decides "what is internal/topology.Hub's event sink", which is what
// automation.go's own doc comment already promises for T-1104's half of
// it.
func setupPush(subs *store.PushSubscriptionRepo, cipher pushSecretCipher, vapidPriv *ecdsa.PrivateKey, logger *slog.Logger) *push.Dispatcher {
	adapter := pushSubscriptionProviderAdapter{repo: subs, cipher: cipher, logger: logger}
	return push.NewDispatcher(push.DispatcherConfig{
		Provider: adapter, Tracker: adapter,
		VAPIDPrivateKey: vapidPriv, VAPIDSubject: pushVAPIDSubject,
		Logger: logger,
	})
}

// pushFindingsNotifier adapts push.Dispatcher into findings.Notifier
// (notify.go's interface), composed alongside PVENotifier/WebhookNotifier/
// FindingEventsNotifier in findings.go's multiNotifier — the same "one more
// wrapped Notifier" pattern every T-1005-era notifier already follows. It
// lives here rather than in internal/push so that package stays free of a
// dependency on internal/findings (mirroring internal/automation's
// identical independence: internal/automation.Dispatcher.Publish takes raw
// bytes, never a findings.Finding).
//
// Deliberately does NOT thread the findings.Finding argument into anything
// the dispatcher sends: T-2005's card requires the push payload carry no
// cluster-identifying content, and a Finding's ID/Detail/Nodes/Refs
// routinely do (this file's payload.go doc comment lists concrete
// examples). Firing is gated on severity == findings.SeverityError — this
// codebase's highest defined severity (info/warning/error, types.go) — and
// on the finding being NEW or ESCALATED into that severity, never
// RESOLVED: a resolved finding is good news, not something to page anyone
// about.
type pushFindingsNotifier struct {
	dispatcher *push.Dispatcher
}

var _ findings.Notifier = pushFindingsNotifier{}

func (n pushFindingsNotifier) Notify(_ context.Context, f findings.Finding, kind findings.TransitionKind) error {
	if n.dispatcher == nil {
		return nil
	}
	if f.Severity != findings.SeverityError {
		return nil
	}
	if kind != findings.TransitionNew && kind != findings.TransitionEscalated {
		return nil
	}
	n.dispatcher.PublishCriticalFinding()
	return nil
}
