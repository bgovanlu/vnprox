// SPDX-License-Identifier: Apache-2.0

// dispatcher.go is this package's fan-out seam: internal/topology.Hub's
// SetEventSink target (T-1104's mechanism — see internal/automation's
// Dispatcher for the sibling webhook consumer of the exact same seam) plus
// a direct entry point for the findings-transition-driven "critical"
// category, which never flows over the generic event-sink payload (see
// payload.go's BuildFromEvent doc comment for why: findings.changed only
// carries a count, not severity).
//
// Unlike internal/automation's Dispatcher, delivery here is single-attempt,
// no retry/backoff: RFC 8030's push-service TTL mechanism (webpush.go's
// DefaultTTL) is what already covers "the device was briefly offline" —
// the push SERVICE (FCM/autopush/APNs-web-push) holds and retries delivery
// to the device on vnprox's behalf once accepted, so an application-level
// retry loop here would just be retrying against a service that already
// accepted the message.

package push

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// SubscriptionRecord is one push.Send-ready subscription, decrypted and
// adapted from storage by cmd/vnproxd's composition-root Provider
// implementation (mirrors internal/automation.Webhook's identical
// decrypt-at-the-boundary convention — see that type's doc comment).
type SubscriptionRecord struct {
	ID           string
	Subscription Subscription
}

// Provider supplies the subscriptions currently opted into category, ready
// to deliver against (secrets already decrypted). cmd/vnproxd adapts
// *store.PushSubscriptionRepo (plus the session cipher) into this seam.
type Provider interface {
	Subscriptions(ctx context.Context, category Category) ([]SubscriptionRecord, error)
}

// Tracker records delivery outcomes against a subscription's row.
// *store.PushSubscriptionRepo satisfies TouchLastUsed directly; Prune is
// typically DeleteByEndpointHash's id-keyed sibling (internal/api's
// adapter deletes by id).
type Tracker interface {
	TouchLastUsed(ctx context.Context, id string, now int64) error
	Prune(ctx context.Context, id string) error
}

// DispatcherConfig configures a Dispatcher. Provider and VAPIDPrivateKey
// are required (a nil Provider makes Publish/PublishCriticalFinding
// no-ops, matching automation.Dispatcher's identical "no Provider wired"
// convention); everything else defaults sensibly.
type DispatcherConfig struct {
	Provider        Provider
	Tracker         Tracker
	VAPIDPrivateKey *ecdsa.PrivateKey
	Client          *http.Client
	Logger          *slog.Logger
	Now             func() time.Time
	VAPIDSubject    string
}

// Dispatcher is internal/topology.Hub's SetEventSink target for the
// awaitingConfirm/drift categories (via Publish), and
// internal/findings.Notifier's (adapted in cmd/vnproxd) delivery target for
// the critical category (via PublishCriticalFinding).
type Dispatcher struct {
	provider     Provider
	tracker      Tracker
	vapidPriv    *ecdsa.PrivateKey
	client       *http.Client
	log          *slog.Logger
	now          func() time.Time
	vapidSubject string
}

// NewDispatcher builds a Dispatcher from cfg, applying documented defaults
// for every unset field.
func NewDispatcher(cfg DispatcherConfig) *Dispatcher {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Dispatcher{
		provider: cfg.Provider, tracker: cfg.Tracker,
		vapidPriv: cfg.VAPIDPrivateKey, vapidSubject: cfg.VAPIDSubject,
		client: client, log: logger, now: now,
	}
}

// Publish fans payload out to every subscription opted into the category
// BuildFromEvent derives from it, if any. It is wired as
// internal/topology.Hub's SetEventSink callback (via cmd/vnproxd, chained
// alongside internal/automation.Dispatcher.Publish so both consumers see
// every event — see that package's doc comment on why SetEventSink only
// ever holds one func), which must never block the broadcaster — this
// method returns immediately, the same "never block" contract
// automation.Dispatcher.Publish documents.
func (d *Dispatcher) Publish(payload []byte) {
	if d.provider == nil {
		return
	}
	n, ok := BuildFromEvent(payload)
	if !ok {
		return
	}
	go d.deliver(n)
}

// PublishCriticalFinding fans CriticalFindingNotification() out to every
// subscription opted into the critical category. Called by cmd/vnproxd's
// findings.Notifier adapter on a qualifying (new-or-escalated,
// error-severity) finding transition — see that adapter's doc comment for
// why the findings.Finding itself never reaches this package.
func (d *Dispatcher) PublishCriticalFinding() {
	if d.provider == nil {
		return
	}
	go d.deliver(CriticalFindingNotification())
}

func (d *Dispatcher) deliver(n Notification) {
	ctx := context.Background()
	subs, err := d.provider.Subscriptions(ctx, n.Category)
	if err != nil {
		d.log.Warn("push: listing subscriptions", "category", string(n.Category), "error", err)
		return
	}
	if len(subs) == 0 {
		return
	}
	payload, err := n.Marshal()
	if err != nil {
		d.log.Error("push: marshaling notification", "category", string(n.Category), "error", err)
		return
	}
	for _, s := range subs {
		go d.deliverOne(ctx, s, payload)
	}
}

func (d *Dispatcher) deliverOne(ctx context.Context, s SubscriptionRecord, payload []byte) {
	err := Send(ctx, s.Subscription, payload, SendConfig{
		VAPIDPrivateKey: d.vapidPriv, VAPIDSubject: d.vapidSubject, Client: d.client, Now: d.now,
	})
	switch {
	case err == nil:
		if d.tracker != nil {
			if terr := d.tracker.TouchLastUsed(ctx, s.ID, d.now().Unix()); terr != nil {
				d.log.Warn("push: recording delivery", "subscription_id", s.ID, "error", terr)
			}
		}
	case errors.Is(err, ErrGone):
		d.log.Info("push: subscription no longer valid, pruning", "subscription_id", s.ID)
		if d.tracker != nil {
			if terr := d.tracker.Prune(ctx, s.ID); terr != nil {
				d.log.Warn("push: pruning dead subscription", "subscription_id", s.ID, "error", terr)
			}
		}
	default:
		d.log.Warn("push: delivery failed", "subscription_id", s.ID, "error", err)
	}
}
