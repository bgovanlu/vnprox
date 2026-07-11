package findings

import (
	"context"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// NotificationClient is the subset of *pve.Client PVENotifier needs.
// Declared as an interface so this package's dependency on internal/pve
// stays a two-method seam (the same pattern every other cross-package
// dependency in this codebase uses), and so tests can substitute a fake
// client without a mock PVE server.
type NotificationClient interface {
	ListNotificationTargets(ctx context.Context) ([]pve.NotificationTarget, error)
	TestNotificationTarget(ctx context.Context, name string) error
}

// PVENotifier is the production Notifier: on every qualifying transition it
// triggers PVE's own notification-target test-delivery route for every
// enabled target (docs/features/monitoring.md §5: "webhook/email via PVE
// notification system"). See internal/pve.Client.TestNotificationTarget's
// doc comment for the real, currently-unresolved gap this implementation
// has: PVE's public API has no documented way for vnprox to push its own
// finding text through a target, so what the operator sees delivered is
// PVE's generic test-notification content, not the finding's own detail —
// flagged in the T-602 completion report as needing verification against a
// live cluster / a newer PVE API surface.
type PVENotifier struct {
	client NotificationClient
	log    *slog.Logger
}

// NewPVENotifier builds a PVENotifier over client. log defaults to
// slog.Default() when nil.
func NewPVENotifier(client NotificationClient, log *slog.Logger) *PVENotifier {
	if log == nil {
		log = slog.Default()
	}
	return &PVENotifier{client: client, log: log}
}

var _ Notifier = (*PVENotifier)(nil)

// Notify implements Notifier: it fans the transition out to every enabled
// notification target currently configured cluster-wide. A single target
// failing to trigger does not stop the others from being tried; every
// per-target error is logged and the first one is returned (so
// Engine.fireNotification's own log line still records that something
// went wrong overall).
func (n *PVENotifier) Notify(ctx context.Context, f Finding, kind TransitionKind) error {
	targets, err := n.client.ListNotificationTargets(ctx)
	if err != nil {
		return err
	}

	var firstErr error
	for _, t := range targets {
		if t.Disable {
			continue
		}
		if err := n.client.TestNotificationTarget(ctx, t.Name); err != nil {
			n.log.Warn("findings: pve notification target delivery failed",
				"target", t.Name, "finding_id", f.ID, "transition", string(kind), "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		n.log.Info("findings: notification delivered",
			"target", t.Name, "finding_id", f.ID, "severity", f.Severity, "transition", string(kind))
	}
	return firstErr
}
