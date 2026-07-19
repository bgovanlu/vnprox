package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// DefaultUnhealthyThreshold is how many *consecutive* delivery failures
// (an exhausted retry sequence, not individual attempts) raise the
// webhook_unhealthy finding (docs/api.md's Webhooks section: "N
// consecutive failures"); a subsequent successful delivery resets the
// counter to 0 (internal/store.WebhookRepo.RecordSuccess), which is what
// clears the finding on the next findings cycle (internal/findings'
// WebhookProvider seam recomputes it live from this column — see
// internal/findings/adapt_webhook.go's doc comment). Chosen to match this
// codebase's other "a few misses, not a single blip" health-check
// thresholds (e.g. internal/findings' 3-consecutive-poll-failure
// staleness rule) rather than firing on the very first failed delivery,
// which would be noisy for a target having one transient blip.
const DefaultUnhealthyThreshold = 3

// Default retry/backoff parameters — identical shape to
// internal/findings/webhook.go's DefaultMaxAttempts/DefaultBackoffBase/
// DefaultBackoffCap (5 bounded attempts, 1s doubling to a 30s cap), reused
// here for the same reason: there is nothing webhook-delivery-specific
// about alert-rule webhooks vs. automation-event webhooks that would
// justify different tuning.
const (
	DefaultMaxAttempts = 5
	DefaultBackoffBase = 1 * time.Second
	DefaultBackoffCap  = 30 * time.Second
)

// Webhook is one registered delivery target, decoupled from
// internal/store.Webhook the same way internal/findings' AlertRule is
// decoupled from internal/store.AlertRule — cmd/vnproxd's composition-root
// adapter decrypts store.Webhook.SecretEnc into Secret before handing this
// package a value. Events is the optional event-name allowlist
// (docs/api.md's Webhooks section: empty/nil means "every event",
// matching alert_rules' source/severity filter convention); Secret is the
// plaintext HMAC signing key, never logged or persisted by this package.
type Webhook struct {
	ID     string
	URL    string
	Secret string
	Events []string
}

// Provider supplies the current set of registered webhooks, ready to
// deliver against (secrets already decrypted). cmd/vnproxd adapts
// *store.WebhookRepo into this seam.
type Provider interface {
	Webhooks(ctx context.Context) ([]Webhook, error)
}

// FailureTracker persists delivery outcomes against a webhook's row
// (internal/store.WebhookRepo.RecordSuccess/RecordFailure satisfy this
// directly). Unlike internal/findings' DeliveryRecorder (one row per HTTP
// attempt), only the outcome of a whole retry *sequence* is recorded here
// — see webhooks.consecutive_failures' doc comment in the T-1104
// migration for why a full per-attempt log isn't part of this schema.
type FailureTracker interface {
	RecordSuccess(ctx context.Context, id string, now int64) error
	RecordFailure(ctx context.Context, id string, now int64, errMsg string) (count int, err error)
}

// envelopeEvent extracts just the "event" field common to every payload
// this package delivers (internal/topology.Hub's flat envelope
// convention), for the Events allowlist filter — nothing here needs to
// understand the rest of any specific event's shape.
type envelopeEvent struct {
	Event string `json:"event"`
}

func eventNameOf(payload []byte) string {
	var e envelopeEvent
	if err := json.Unmarshal(payload, &e); err != nil {
		return ""
	}
	return e.Event
}

func matchesEvents(events []string, name string) bool {
	if len(events) == 0 {
		return true
	}
	for _, e := range events {
		if e == name {
			return true
		}
	}
	return false
}

// DispatcherConfig configures a Dispatcher. Provider is required (a nil
// Provider makes Publish a no-op); everything else defaults sensibly.
type DispatcherConfig struct {
	Provider    Provider
	Tracker     FailureTracker
	Client      *http.Client
	Logger      *slog.Logger
	Now         func() time.Time
	Sleep       func(ctx context.Context, d time.Duration)
	MaxAttempts int
	BackoffBase time.Duration
	BackoffCap  time.Duration
}

// Dispatcher is internal/topology.Hub's SetEventSink target (T-1104): it
// fans the same envelope an "events"-subscribed WS client receives out to
// every registered, matching webhook target, each with its own bounded
// exponential-backoff retry sequence, signed with HeaderSignature.
type Dispatcher struct {
	provider    Provider
	tracker     FailureTracker
	client      *http.Client
	log         *slog.Logger
	now         func() time.Time
	sleep       func(ctx context.Context, d time.Duration)
	maxAttempts int
	backoffBase time.Duration
	backoffCap  time.Duration
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
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = realSleep
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	base := cfg.BackoffBase
	if base <= 0 {
		base = DefaultBackoffBase
	}
	backoffCapV := cfg.BackoffCap
	if backoffCapV <= 0 {
		backoffCapV = DefaultBackoffCap
	}
	return &Dispatcher{
		provider: cfg.Provider, tracker: cfg.Tracker, client: client, log: logger,
		now: now, sleep: sleep, maxAttempts: maxAttempts, backoffBase: base, backoffCap: backoffCapV,
	}
}

func realSleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func backoffDuration(base, backoffCapV time.Duration, attempt int) time.Duration {
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= backoffCapV {
			return backoffCapV
		}
	}
	return d
}

// Publish fans payload out to every registered webhook whose Events
// allowlist (if any) includes payload's "event" field, each delivered
// concurrently in its own goroutine — Publish itself returns immediately
// without waiting for any delivery or retry backoff to complete, since it
// is wired as internal/topology.Hub's SetEventSink callback, which must
// never block the broadcaster (see that method's doc comment). A nil
// Provider (no webhook wiring) makes this an immediate no-op.
func (d *Dispatcher) Publish(payload []byte) {
	if d.provider == nil {
		return
	}
	go d.publish(payload)
}

func (d *Dispatcher) publish(payload []byte) {
	ctx := context.Background()
	name := eventNameOf(payload)

	webhooks, err := d.provider.Webhooks(ctx)
	if err != nil {
		d.log.Warn("automation: listing webhooks", "error", err)
		return
	}

	var wg sync.WaitGroup
	for _, wh := range webhooks {
		if !matchesEvents(wh.Events, name) {
			continue
		}
		wg.Add(1)
		go func(wh Webhook) {
			defer wg.Done()
			d.deliverWithRetry(ctx, wh, payload)
		}(wh)
	}
	wg.Wait()
}

// Deliver sends one signed HTTP delivery attempt of payload to wh's URL,
// reporting success as a 2xx response, failure otherwise. It does not
// retry — deliverWithRetry wraps it with the bounded backoff loop.
// client defaults to http.DefaultClient if nil.
func Deliver(ctx context.Context, client *http.Client, wh Webhook, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wh.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("automation: building webhook request for %s: %w", wh.ID, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if wh.Secret != "" {
		req.Header.Set(HeaderSignature, Sign([]byte(wh.Secret), payload))
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("automation: delivering webhook %s: %w", wh.ID, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("automation: webhook target %s responded %d", wh.ID, resp.StatusCode)
	}
	return nil
}

// deliverWithRetry runs wh's bounded retry sequence, recording only the
// sequence's final outcome via d.tracker (RecordSuccess the moment an
// attempt succeeds, RecordFailure once every attempt has failed) — see
// FailureTracker's doc comment for why this differs from
// internal/findings/webhook.go's per-attempt recording.
func (d *Dispatcher) deliverWithRetry(ctx context.Context, wh Webhook, payload []byte) {
	var lastErr error
	for attempt := 1; attempt <= d.maxAttempts; attempt++ {
		if err := Deliver(ctx, d.client, wh, payload); err != nil {
			lastErr = err
			if attempt < d.maxAttempts {
				d.sleep(ctx, backoffDuration(d.backoffBase, d.backoffCap, attempt))
				if ctx.Err() != nil {
					return
				}
			}
			continue
		}
		d.recordSuccess(ctx, wh)
		return
	}
	d.recordFailure(ctx, wh, lastErr)
}

func (d *Dispatcher) recordSuccess(ctx context.Context, wh Webhook) {
	if d.tracker == nil {
		return
	}
	if err := d.tracker.RecordSuccess(ctx, wh.ID, d.now().Unix()); err != nil {
		d.log.Warn("automation: recording webhook delivery success", "webhook_id", wh.ID, "error", err)
	}
}

func (d *Dispatcher) recordFailure(ctx context.Context, wh Webhook, lastErr error) {
	errMsg := ""
	if lastErr != nil {
		errMsg = lastErr.Error()
	}
	d.log.Warn("automation: webhook delivery failed after retries", "webhook_id", wh.ID, "url", wh.URL, "error", lastErr)
	if d.tracker == nil {
		return
	}
	if _, err := d.tracker.RecordFailure(ctx, wh.ID, d.now().Unix(), errMsg); err != nil {
		d.log.Warn("automation: recording webhook delivery failure", "webhook_id", wh.ID, "error", err)
	}
}
