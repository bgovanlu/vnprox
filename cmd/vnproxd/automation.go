package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/automation"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/store"
)

// webhookSecretCipher is the subset of *store.SessionCipher
// webhookProviderAdapter needs — the same "declare a one-method seam so
// tests can substitute a fake cipher" pattern alertSecretCipher above
// (findings.go) uses.
type webhookSecretCipher interface {
	Decrypt(sealed []byte) ([]byte, error)
}

// webhookStore is the subset of *store.WebhookRepo webhookProviderAdapter
// needs.
type webhookStore interface {
	List(ctx context.Context) ([]store.Webhook, error)
}

// webhookProviderAdapter adapts *store.WebhookRepo (plus the session
// cipher) into automation.Provider — the same decrypt-and-adapt
// composition-root conversion alertRuleProviderAdapter performs for
// T-1005's webhook notifier, so internal/automation never imports
// internal/store directly. A webhook whose secret fails to decrypt (a
// corrupt row, or a key rotated out from under an existing registration)
// is logged and skipped for this delivery rather than failing the whole
// fan-out.
type webhookProviderAdapter struct {
	repo   webhookStore
	cipher webhookSecretCipher
	logger *slog.Logger
}

func (a webhookProviderAdapter) Webhooks(ctx context.Context) ([]automation.Webhook, error) {
	rows, err := a.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("automation: listing webhooks: %w", err)
	}
	out := make([]automation.Webhook, 0, len(rows))
	for _, row := range rows {
		secret := ""
		if len(row.SecretEnc) > 0 {
			plaintext, decErr := a.cipher.Decrypt(row.SecretEnc)
			if decErr != nil {
				a.logger.Warn("automation: decrypting webhook secret, skipping this cycle", "webhook_id", row.ID, "error", decErr)
				continue
			}
			secret = string(plaintext)
		}
		out = append(out, automation.Webhook{ID: row.ID, URL: row.URL, Secret: secret, Events: decodeWebhookRowEvents(row)})
	}
	return out, nil
}

// webhookHealthStore is the subset of *store.WebhookRepo
// webhookHealthAdapter needs.
type webhookHealthStore interface {
	List(ctx context.Context) ([]store.Webhook, error)
}

// webhookHealthAdapter adapts *store.WebhookRepo's live
// consecutive_failures column into the unified findings stream's
// webhook_unhealthy check (findings.WebhookProvider, T-1104) — computed
// fresh each findings cycle straight from storage, the same "no second
// persisted flag" pattern ipamFindingsAdapter uses for IPAM conflicts.
type webhookHealthAdapter struct {
	repo   webhookHealthStore
	logger *slog.Logger
}

const webhookUnhealthyDocsLink = "docs/api.md#webhooks"

func (a webhookHealthAdapter) Findings() []findings.Finding {
	rows, err := a.repo.List(context.Background())
	if err != nil {
		a.logger.Warn("findings: listing webhooks for webhook_unhealthy check", "error", err)
		return nil
	}
	var out []findings.Finding
	for _, row := range rows {
		if row.ConsecutiveFailures < automation.DefaultUnhealthyThreshold {
			continue
		}
		detail := fmt.Sprintf("webhook %s has failed %d consecutive deliveries", row.URL, row.ConsecutiveFailures)
		if row.LastError.Valid && row.LastError.String != "" {
			detail += ": " + row.LastError.String
		}
		out = append(out, findings.Finding{
			ID:       "health:webhook_unhealthy|" + row.ID,
			Source:   findings.SourceHealth,
			Check:    "webhook_unhealthy",
			Severity: findings.SeverityWarning,
			Detail:   detail,
			Nodes:    []string{},
			DocsLink: webhookUnhealthyDocsLink,
		})
	}
	return out
}

func decodeWebhookRowEvents(row store.Webhook) []string {
	if !row.EventsJSON.Valid || row.EventsJSON.String == "" {
		return nil
	}
	var events []string
	if err := json.Unmarshal([]byte(row.EventsJSON.String), &events); err != nil {
		return nil
	}
	return events
}

// setupAutomation builds T-1104's webhook Dispatcher and wires it as ws's
// event sink (internal/topology.Hub.SetEventSink, via topoSvc's own
// forwarding method) — every payload that lands on the WS "events" topic
// (reused changeset.status/drift.changed/findings.changed producers plus
// the direct audit.appended push, see hub.go's eventsSourceTopics doc
// comment) is handed to the exact same webhook fan-out an "events"
// subscriber would see over WS.
func setupAutomation(webhooks *store.WebhookRepo, cipher webhookSecretCipher, ws eventSinkSetter, logger *slog.Logger) *automation.Dispatcher {
	dispatcher := automation.NewDispatcher(automation.DispatcherConfig{
		Provider: webhookProviderAdapter{repo: webhooks, cipher: cipher, logger: logger},
		Tracker:  webhooks,
		Logger:   logger,
	})
	ws.SetEventSink(dispatcher.Publish)
	return dispatcher
}

// eventSinkSetter is the subset of *topology.Service setupAutomation
// needs.
type eventSinkSetter interface {
	SetEventSink(fn func([]byte))
}
