// SPDX-License-Identifier: Apache-2.0

package ha

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"time"
)

// Mode selects the failover-announce mechanism ([ha] mode).
type Mode string

const (
	// ModeVIP triggers a pluggable external command that moves a virtual IP to
	// the newly-active node (e.g. an operator script running `ip addr add` +
	// gratuitous ARP). vnprox only triggers it — it never manages the VIP or
	// ARP itself, so no new daemon dependency is introduced.
	ModeVIP Mode = "vip"
	// ModeDNS triggers a pluggable webhook the operator points at their own DNS
	// automation to repoint the service record at the newly-active node.
	ModeDNS Mode = "dns"
)

// ValidMode reports whether m is a recognized [ha] mode.
func ValidMode(m string) bool { return Mode(m) == ModeVIP || Mode(m) == ModeDNS }

// NoopAnnouncer is the default when no VIP command or DNS webhook is
// configured: role transitions are logged (by the Manager) but no external
// mechanism is triggered. Useful for a manually-fronted deployment (an external
// load balancer already health-checks the pair) and for tests.
type NoopAnnouncer struct{}

// Announce does nothing.
func (NoopAnnouncer) Announce(context.Context, Role) error { return nil }

// CommandAnnouncer (ModeVIP) runs an operator-provided command on every role
// transition, passing the new role as the sole argument (e.g.
// `/etc/vnprox/ha-vip.sh active`). The command is entirely operator-owned —
// vnprox neither ships nor assumes a specific VIP mechanism.
type CommandAnnouncer struct {
	Path    string
	Timeout time.Duration
}

// Announce runs Path with the role name as its argument.
func (c CommandAnnouncer) Announce(ctx context.Context, role Role) error {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, c.Path, string(role)) //nolint:gosec // operator-configured path, root-owned config (docs/security.md)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ha: vip announce command %q for role %s failed: %w (output: %s)", c.Path, role, err, bytes.TrimSpace(out))
	}
	return nil
}

// WebhookAnnouncer (ModeDNS) POSTs a small JSON body to an operator-provided
// webhook on every role transition — the operator wires it to their own DNS
// automation.
type WebhookAnnouncer struct {
	Client  *http.Client
	URL     string
	Timeout time.Duration
}

// announcePayload is the webhook body.
type announcePayload struct {
	Role string `json:"role"`
	At   int64  `json:"at"`
}

// Announce POSTs {role, at} to the webhook URL.
func (w WebhookAnnouncer) Announce(ctx context.Context, role Role) error {
	timeout := w.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := json.Marshal(announcePayload{Role: string(role), At: time.Now().Unix()})
	if err != nil {
		return fmt.Errorf("ha: encoding dns announce payload: %w", err)
	}
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ha: building dns announce request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ha: posting dns announce webhook %q for role %s: %w", w.URL, role, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ha: dns announce webhook %q for role %s returned status %d", w.URL, role, resp.StatusCode)
	}
	return nil
}
