// SPDX-License-Identifier: Apache-2.0

package findings

// WebhookProvider is the seam T-1104's webhook-health producer satisfies
// (via cmd/vnproxd's webhookHealthAdapter, which reads consecutive-failure
// counts straight off the webhooks table and converts them into the
// unified Finding shape — the same composition-root-does-the-conversion
// pattern IPAMProvider/adapt_ipam.go already uses, so internal/store need
// not import this package). Findings are recomputed live from
// webhooks.consecutive_failures each cycle (no separate persisted
// "unhealthy" flag): a webhook whose next delivery succeeds simply stops
// appearing the moment its counter resets to 0, which is what "recovery
// clears it" means in practice — the same style probe/ipam findings use,
// just without probe's persistence (there is no user-triggered action
// here to remember between polls, only a column already living in
// storage).
type WebhookProvider interface {
	Findings() []Finding
}

// webhookFindings returns p's current findings, or nil when p is nil (no
// webhook registrations wired — the same degraded-mode convention every
// other optional producer in this file follows).
func webhookFindings(p WebhookProvider) []Finding {
	if p == nil {
		return nil
	}
	return p.Findings()
}
