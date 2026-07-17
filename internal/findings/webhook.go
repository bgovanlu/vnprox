// webhook.go implements T-1005's alert routing: a Notifier (see notify.go's
// interface) that delivers finding/drift transitions to arbitrary webhook
// targets, independent of PVE's own notification-target system
// (docs/features/monitoring.md §5's documented gap in pvenotify.go — PVE's
// API has no way to carry vnprox's own finding text, only a generic
// test-notification). This file adds a second, parallel delivery path; it
// does not touch Engine's notification-firing/once-per-transition logic in
// engine.go/notify.go at all — WebhookNotifier is just another
// implementation of the existing Notifier interface, composed alongside
// PVENotifier at the cmd/vnproxd composition root (see that package's
// multiNotifier).
//
// Routing rules and their decrypted secrets are supplied through
// AlertRuleProvider — a small seam, not internal/store's concrete
// *store.AlertRuleRepo — so this package doesn't need to import
// internal/store (the same decoupling ipamFindingsAdapter/
// probeFindingsAdapter give internal/ipam/internal/store in
// cmd/vnproxd/findings.go). cmd/vnproxd's adapter is what decrypts
// AlertRule.TargetSecretEnc (docs/security.md's AES-256-GCM pattern, the
// same cipher session PVE tickets use — internal/store.SessionCipher) into
// the plaintext AlertRule.TargetSecret this package works with; a
// decrypted secret never touches disk or a log line on this side.

package findings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Alert target kinds (AlertRule.TargetKind vocabulary, docs/api.md's Alert
// Rules section).
const (
	TargetGeneric = "generic"
	TargetGotify  = "gotify"
	TargetNtfy    = "ntfy"
	TargetSlack   = "slack"
)

// Delivery status vocabulary (AlertDelivery.Status), matching
// internal/store/migrations/0008_alert_rules.sql's alert_deliveries.status
// doc comment exactly: "retrying" (this attempt failed, another is
// scheduled), "delivered" (terminal success), "failed" (terminal failure —
// the last attempt was exhausted).
const (
	StatusRetrying  = "retrying"
	StatusDelivered = "delivered"
	StatusFailed    = "failed"
)

// AlertRule is webhook.go's own view of one routing rule — decoupled from
// internal/store.AlertRule (see this file's package doc comment).
// SourceFilter/SeverityFilter empty/nil means "no filter on that dimension"
// (matches every value), the same optional/ANDed filter contract every
// other filter in this codebase follows. TargetSecret is already decrypted
// plaintext by the time it reaches here; empty means the target needs no
// secret (a generic webhook with no auth, or a Slack incoming-webhook URL,
// whose token already lives in the URL itself).
type AlertRule struct {
	ID             string
	Name           string
	TargetKind     string
	TargetURL      string
	TargetSecret   string
	SourceFilter   []string
	SeverityFilter []string
	Enabled        bool
}

// AlertRuleProvider supplies the current set of routing rules, decrypted
// and ready to deliver against. cmd/vnproxd adapts *store.AlertRuleRepo
// (decrypting TargetSecretEnc via the session-secret cipher) into this
// seam.
type AlertRuleProvider interface {
	AlertRules(ctx context.Context) ([]AlertRule, error)
}

// AlertDelivery is one webhook delivery *attempt* — see this file's
// StatusRetrying/StatusDelivered/StatusFailed doc comments for the status
// vocabulary. Unlike internal/store.AlertDelivery, it carries no ID (the
// DeliveryRecorder implementation assigns one on persist, the same
// "leaf package doesn't generate storage ids" split annotationCreateRequest
// vs. store.Annotation already uses elsewhere in this codebase).
type AlertDelivery struct {
	At        time.Time
	RuleID    string
	FindingID string
	Status    string
	Error     string
	Attempt   int
}

// DeliveryRecorder persists one delivery attempt for the Settings UI's
// delivery log (GET /alert-deliveries). cmd/vnproxd adapts
// *store.AlertDeliveryRepo into this seam. A recorder failure is logged,
// never fatal to delivery itself (mirrors every other "log, don't fail the
// cycle" convention in this package).
type DeliveryRecorder interface {
	RecordDelivery(ctx context.Context, d AlertDelivery) error
}

// gotifyMessage is Gotify's push-message API request body
// (POST /message: https://gotify.net/api-docs#/message).
type gotifyMessage struct {
	Title    string `json:"title"`
	Message  string `json:"message"`
	Priority int    `json:"priority"`
}

// slackMessage is Slack's incoming-webhook request body — the minimal
// {"text": "..."} shape every Slack incoming webhook accepts.
type slackMessage struct {
	Text string `json:"text"`
}

// transitionLabel renders a TransitionKind for human-readable message text.
func transitionLabel(kind TransitionKind) string {
	switch kind {
	case TransitionNew:
		return "New"
	case TransitionEscalated:
		return "Escalated"
	case TransitionResolved:
		return "Resolved"
	default:
		return string(kind)
	}
}

// severityPriority maps a Finding.Severity to Gotify's 0-10 priority scale
// (>=4 is Gotify's own "high priority" client-side threshold) — error gets
// a value comfortably above that threshold, info comfortably below it.
func severityPriority(severity string) int {
	switch severity {
	case SeverityError:
		return 8
	case SeverityWarning:
		return 5
	default:
		return 2
	}
}

// ntfyTag maps severity to one ntfy emoji-shortcode tag
// (https://docs.ntfy.sh/publish/#tags-emojis) for at-a-glance triage in the
// ntfy client.
func ntfyTag(severity string) string {
	switch severity {
	case SeverityError:
		return "rotating_light"
	case SeverityWarning:
		return "warning"
	default:
		return "information_source"
	}
}

func alertTitle(f Finding) string {
	return fmt.Sprintf("vnprox %s: %s", strings.ToUpper(f.Severity), f.Check)
}

func alertMessage(f Finding, kind TransitionKind) string {
	msg := fmt.Sprintf("[%s] %s", transitionLabel(kind), f.Detail)
	if len(f.Nodes) > 0 {
		msg += " (nodes: " + strings.Join(f.Nodes, ", ") + ")"
	}
	return msg
}

// PayloadFor builds the HTTP request body/content-type/extra-headers for
// delivering f (at transition kind) to rule's target, shaped per
// rule.TargetKind:
//
//   - generic: the Finding shape verbatim (json.Marshal(f), no wrapper) —
//     the documented contract for callers that want vnprox's own finding
//     shape unmodified. A rule secret, if set, authenticates as
//     `Authorization: Bearer <secret>`.
//   - gotify: Gotify's push-message JSON {title, message, priority}
//     (severityPriority maps severity onto Gotify's 0-10 scale); a rule
//     secret authenticates as `X-Gotify-Key` (Gotify's app-token header).
//   - ntfy: ntfy's plain-text publish convention
//     (https://docs.ntfy.sh/publish/): the message as the plain-text body,
//     `Title`/`Priority`/`Tags` headers carrying the rest. A rule secret
//     authenticates as `Authorization: Bearer <secret>` (ntfy access token
//     auth).
//   - slack: Slack's incoming-webhook JSON {"text": "..."} — the token
//     already lives in the webhook URL itself, so a rule secret (if set)
//     is not used for this kind.
//
// Every kind also gets an `X-Vnprox-Transition` header naming kind
// ("new"|"escalated"|"resolved") — additive, so it never breaks the
// generic body's "Finding shape verbatim" contract.
func PayloadFor(rule AlertRule, f Finding, kind TransitionKind) (body []byte, contentType string, headers map[string]string, err error) {
	headers = map[string]string{"X-Vnprox-Transition": string(kind)}

	switch rule.TargetKind {
	case TargetGeneric:
		body, err = json.Marshal(f)
		contentType = "application/json"
		if rule.TargetSecret != "" {
			headers["Authorization"] = "Bearer " + rule.TargetSecret
		}
	case TargetGotify:
		body, err = json.Marshal(gotifyMessage{
			Title:    alertTitle(f),
			Message:  alertMessage(f, kind),
			Priority: severityPriority(f.Severity),
		})
		contentType = "application/json"
		if rule.TargetSecret != "" {
			headers["X-Gotify-Key"] = rule.TargetSecret
		}
	case TargetNtfy:
		body = []byte(alertMessage(f, kind))
		contentType = "text/plain; charset=utf-8"
		headers["Title"] = alertTitle(f)
		headers["Priority"] = fmt.Sprintf("%d", ntfyPriority(f.Severity))
		headers["Tags"] = ntfyTag(f.Severity)
		if rule.TargetSecret != "" {
			headers["Authorization"] = "Bearer " + rule.TargetSecret
		}
	case TargetSlack:
		body, err = json.Marshal(slackMessage{Text: alertTitle(f) + "\n" + alertMessage(f, kind)})
		contentType = "application/json"
	default:
		return nil, "", nil, fmt.Errorf("findings: unknown alert target kind %q", rule.TargetKind)
	}
	if err != nil {
		return nil, "", nil, fmt.Errorf("findings: building %s webhook payload: %w", rule.TargetKind, err)
	}
	return body, contentType, headers, nil
}

// ntfyPriority maps severity onto ntfy's 1(min)-5(max) priority scale
// (https://docs.ntfy.sh/publish/#message-priority) — distinct from
// Gotify's 0-10 scale, so kept as its own mapping rather than reusing
// severityPriority.
func ntfyPriority(severity string) int {
	switch severity {
	case SeverityError:
		return 5
	case SeverityWarning:
		return 4
	default:
		return 3
	}
}

// Deliver sends one HTTP delivery attempt of f (at transition kind) to
// rule's target and reports success as a 2xx response, failure otherwise
// (including the response status code in the returned error). It does not
// retry — WebhookNotifier.Notify wraps it with the retry/backoff loop;
// callers that want a single-shot delivery (POST /alert-rules/{id}/test)
// call this directly. client defaults to http.DefaultClient if nil.
func Deliver(ctx context.Context, client *http.Client, rule AlertRule, f Finding, kind TransitionKind) error {
	body, contentType, headers, err := PayloadFor(rule, f, kind)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rule.TargetURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("findings: building webhook request for rule %s: %w", rule.ID, err)
	}
	req.Header.Set("Content-Type", contentType)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("findings: delivering %s webhook (rule %s): %w", rule.TargetKind, rule.ID, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("findings: webhook target for rule %s responded %d", rule.ID, resp.StatusCode)
	}
	return nil
}

// Default retry/backoff parameters (WebhookNotifierConfig's zero-value
// fallbacks): 5 bounded attempts, exponential backoff from 1s doubling up
// to a 30s cap — mirrors internal/collect/loop.go's backoffFor shape
// (base, doubling, capped), applied here to webhook delivery instead of
// poll retries. A delivery that still fails after DefaultMaxAttempts is
// logged StatusFailed and never retried again (the migration's documented
// bound — no indefinite retry, no second unbounded queue).
const (
	DefaultMaxAttempts = 5
	DefaultBackoffBase = 1 * time.Second
	DefaultBackoffCap  = 30 * time.Second
)

// WebhookNotifierConfig configures a WebhookNotifier. Rules is required;
// Recorder is optional (nil silently disables delivery logging, matching
// this package's every other "nil dependency -> feature quietly absent"
// convention) — delivery itself still happens either way. The Sleep/Now
// fields are testing seams (defaulted to real time.Sleep-equivalent/
// time.Now in production) so webhook_test.go's retry/backoff tests run
// without actually waiting seconds between attempts.
type WebhookNotifierConfig struct {
	Rules       AlertRuleProvider
	Recorder    DeliveryRecorder
	Client      *http.Client
	Logger      *slog.Logger
	Now         func() time.Time
	Sleep       func(ctx context.Context, d time.Duration)
	MaxAttempts int
	BackoffBase time.Duration
	BackoffCap  time.Duration
}

// WebhookNotifier is a Notifier (notify.go's interface) that fans a finding
// transition out to every enabled, matching alert_rules target
// concurrently, each with its own bounded exponential-backoff retry
// sequence. See this file's package doc comment: it never touches Engine's
// notification-firing logic, it's just another Notifier implementation.
type WebhookNotifier struct {
	rules       AlertRuleProvider
	recorder    DeliveryRecorder
	client      *http.Client
	log         *slog.Logger
	now         func() time.Time
	sleep       func(ctx context.Context, d time.Duration)
	maxAttempts int
	backoffBase time.Duration
	backoffCap  time.Duration
}

// NewWebhookNotifier builds a WebhookNotifier from cfg, applying the
// documented defaults for every unset field.
func NewWebhookNotifier(cfg WebhookNotifierConfig) *WebhookNotifier {
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
	backoffCap := cfg.BackoffCap
	if backoffCap <= 0 {
		backoffCap = DefaultBackoffCap
	}
	return &WebhookNotifier{
		rules:       cfg.Rules,
		recorder:    cfg.Recorder,
		client:      client,
		log:         logger,
		now:         now,
		sleep:       sleep,
		maxAttempts: maxAttempts,
		backoffBase: base,
		backoffCap:  backoffCap,
	}
}

var _ Notifier = (*WebhookNotifier)(nil)

// realSleep is the production Sleep implementation: waits d or returns
// early if ctx is cancelled (so a daemon shutdown during a retry backoff
// doesn't hang RunLoop's own ctx-cancellation contract).
func realSleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// backoffDuration computes the wait before retry attempt (the attempt
// number that just failed, 1-based), doubling from base and capped at
// backoffCap — the same shape internal/collect/loop.go's backoffFor uses,
// without the jitter (a fixed number of bounded retries doesn't need
// anti-thundering-herd jitter the way a continuous poll loop does).
func backoffDuration(base, backoffCap time.Duration, attempt int) time.Duration {
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= backoffCap {
			return backoffCap
		}
	}
	return d
}

// ruleMatches reports whether f qualifies for rule's optional/ANDed
// source+severity filters (empty means "match everything on that
// dimension") — the same filter contract every other filter in this
// codebase follows.
func ruleMatches(rule AlertRule, f Finding) bool {
	if len(rule.SourceFilter) > 0 && !containsString(rule.SourceFilter, string(f.Source)) {
		return false
	}
	if len(rule.SeverityFilter) > 0 && !containsString(rule.SeverityFilter, f.Severity) {
		return false
	}
	return true
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// Notify implements Notifier: it fans transition f/kind out to every
// enabled alert_rules row whose filters match f, delivering to each
// concurrently (so one slow/retrying target never delays another). Each
// rule's own delivery is deliverWithRetry's bounded retry sequence. Every
// attempt (success or failure) is recorded via Recorder before Notify
// returns for that rule — the delivery log is always consistent with
// what Notify actually attempted, never eventually-consistent. The first
// rule's terminal error (if any) is returned so Engine.fireNotification's
// own log line still records that something went wrong overall; every
// rule's own failure is independently logged here too.
func (w *WebhookNotifier) Notify(ctx context.Context, f Finding, kind TransitionKind) error {
	if w.rules == nil {
		return nil
	}
	rules, err := w.rules.AlertRules(ctx)
	if err != nil {
		return fmt.Errorf("findings: listing alert rules: %w", err)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, rule := range rules {
		if !rule.Enabled || !ruleMatches(rule, f) {
			continue
		}
		wg.Add(1)
		go func(rule AlertRule) {
			defer wg.Done()
			if err := w.deliverWithRetry(ctx, rule, f, kind); err != nil {
				w.log.Warn("findings: webhook delivery failed after retries",
					"rule_id", rule.ID, "rule_name", rule.Name, "finding_id", f.ID,
					"transition", string(kind), "error", err)
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(rule)
	}
	wg.Wait()
	return firstErr
}

// deliverWithRetry runs rule's bounded retry sequence for f/kind, recording
// every attempt via w.recorder. Returns nil the moment an attempt
// succeeds; returns the last attempt's error once w.maxAttempts is
// exhausted (StatusFailed, never retried again after that).
func (w *WebhookNotifier) deliverWithRetry(ctx context.Context, rule AlertRule, f Finding, kind TransitionKind) error {
	var lastErr error
	for attempt := 1; attempt <= w.maxAttempts; attempt++ {
		deliverErr := Deliver(ctx, w.client, rule, f, kind)

		status := StatusDelivered
		errMsg := ""
		if deliverErr != nil {
			lastErr = deliverErr
			errMsg = deliverErr.Error()
			if attempt < w.maxAttempts {
				status = StatusRetrying
			} else {
				status = StatusFailed
			}
		}
		w.record(ctx, rule.ID, f.ID, attempt, status, errMsg)

		if deliverErr == nil {
			return nil
		}
		if attempt < w.maxAttempts {
			w.sleep(ctx, backoffDuration(w.backoffBase, w.backoffCap, attempt))
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	return lastErr
}

func (w *WebhookNotifier) record(ctx context.Context, ruleID, findingID string, attempt int, status, errMsg string) {
	if w.recorder == nil {
		return
	}
	d := AlertDelivery{
		RuleID: ruleID, FindingID: findingID, At: w.now(),
		Attempt: attempt, Status: status, Error: errMsg,
	}
	if err := w.recorder.RecordDelivery(ctx, d); err != nil {
		w.log.Warn("findings: recording alert delivery failed", "rule_id", ruleID, "finding_id", findingID, "error", err)
	}
}
