// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/findings"
)

// tenantApprovalNotifier bridges T-1703's request-changeset approval routing to
// T-1005's alert plumbing (findings.Notifier): a pending request-changeset is
// surfaced as an ordinary routed finding (source "tenant"), so it flows through
// the same alert-rule matching, delivery, and retry machinery every other
// notification uses — no second delivery path. Best-effort by construction; a
// delivery failure never blocks the request-changeset's creation.
type tenantApprovalNotifier struct {
	notifier findings.Notifier
	logger   *slog.Logger
}

// NotifyApprovalPending implements api.ApprovalNotifier.
func (n tenantApprovalNotifier) NotifyApprovalPending(ctx context.Context, notice api.ApprovalNotice) error {
	if n.notifier == nil {
		return nil
	}
	f := findings.Finding{
		ID:       "tenant:approval:" + notice.ChangesetID,
		Source:   findings.Source("tenant"),
		Check:    "approval_pending",
		Severity: "info",
		Detail: fmt.Sprintf("tenant %q: %s requested changeset %s (%q) — awaiting approval by %s",
			notice.TenantName, notice.RequestedBy, notice.ChangesetID, notice.Title, strings.Join(notice.Approvers, ", ")),
		Nodes: notice.Approvers,
		Refs:  []string{notice.ChangesetID},
	}
	if err := n.notifier.Notify(ctx, f, findings.TransitionNew); err != nil {
		if n.logger != nil {
			n.logger.Warn("tenant: routing approval notification", "changeset", notice.ChangesetID, "error", err)
		}
		return err
	}
	return nil
}
