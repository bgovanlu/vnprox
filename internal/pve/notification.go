// SPDX-License-Identifier: Apache-2.0

package pve

import (
	"context"
	"fmt"
)

// NotificationTarget is one configured PVE notification target (a webhook,
// sendmail, or gotify endpoint), as reported by GET
// /cluster/notifications/targets — PVE's unified read view over its three
// endpoint types plus any matchers routing to them. vnprox only needs the
// identity/enablement fields to decide who to notify; the endpoint-specific
// configuration (webhook URL, SMTP recipients, gotify server) stays inside
// PVE and is never read back through this client (see Notify's doc comment
// on why delivery goes through PVE's own test-trigger route instead of
// vnprox reproducing it).
type NotificationTarget struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Comment string `json:"comment,omitempty"`
	Origin  string `json:"origin,omitempty"`
	Disable bool   `json:"disable,omitempty"`
}

// ListNotificationTargets returns every notification target configured
// cluster-wide (GET /cluster/notifications/targets).
func (c *Client) ListNotificationTargets(ctx context.Context) ([]NotificationTarget, error) {
	var out []NotificationTarget
	if err := c.do(ctx, "GET", "/cluster/notifications/targets", requestParams{}, &out); err != nil {
		return nil, fmt.Errorf("pve: listing notification targets: %w", err)
	}
	return out, nil
}

// TestNotificationTarget triggers PVE's own test-notification delivery for
// target (POST /cluster/notifications/targets/{name}/test).
//
// This is deliberately the only send primitive this client implements. PVE
// does not expose a public API for an external caller to push arbitrary
// message content through a configured target — sending is an internal
// operation performed by pvestatd/the notification matcher system itself,
// which holds whatever secret (webhook auth header, SMTP credentials) the
// target's configuration carries; that secret is never handed back out
// through the read API (GET /cluster/notifications/endpoints/webhook
// redacts it). The test-trigger route is the one documented, generally
// available way for a third-party tool to make PVE actually deliver
// *something* through a target without needing that secret itself.
//
// Consequence: vnprox's finding content (severity, explanation, affected
// refs) is not carried in the delivered message today — the operator sees
// PVE's own generic test-notification text, not vnprox's finding detail.
// This is a real, currently-unresolved gap between "notification hooks
// fire" (which this implements and T-602's tests verify) and "the operator
// reads the actual finding in their inbox/webhook payload" (which would
// need either a richer PVE API than the one available at the time this was
// written, or vnprox delivering directly to a webhook target's URL — which
// would require the target's secret, which PVE does not expose). Flagged in
// the T-602 completion report as needing verification against a live
// cluster / a newer PVE release before this is a complete P1.
func (c *Client) TestNotificationTarget(ctx context.Context, name string) error {
	path := fmt.Sprintf("/cluster/notifications/targets/%s/test", name)
	if err := c.do(ctx, "POST", path, requestParams{}, nil); err != nil {
		return fmt.Errorf("pve: testing notification target %s: %w", name, err)
	}
	return nil
}
