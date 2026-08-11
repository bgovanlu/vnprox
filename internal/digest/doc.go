// Package digest implements T-2807's scheduled digest reports: one periodic
// push that turns three pull surfaces — the posture score, capacity forecasts,
// and unresolved drift — plus the findings opened and closed in the period
// into a single message an operator receives instead of having to remember to
// go and look.
//
// WHAT THIS PACKAGE DELIBERATELY DOES NOT CONTAIN.
//
//   - No renderer. The digest is rendered by internal/docexport (digest.go
//     there), alongside the config documentation, the posture report and the
//     compliance report, so a digest's format is one that is already under
//     that package's golden tests.
//   - No delivery path. The digest is handed to T-2407's
//     *findings.WebhookNotifier as an ordinary Finding, which is what makes it
//     obey quiet hours, coalesce, retry with bounded backoff, and land in the
//     same alert_deliveries log every other alert lands in. A second delivery
//     path would be a second thing to configure, a second thing to get wrong
//     at 3am, and a second thing whose quiet hours nobody remembers to set.
//   - No recipient book. Recipients are alert_rules rows. The schedule carries
//     a list of rule ids that NARROWS the existing fan-out (see
//     RecipientFilter) and nothing more.
//
// THE ONE PROPERTY WORTH PROTECTING. A digest with nothing to report is one
// line. Everything about how Report is assembled — that a period with no
// findings, no drift, no forecast and no score movement produces empty slices
// and a zero delta rather than filler — exists so docexport's quiet form is
// reachable. A digest that arrives full every week regardless of what
// happened trains its recipients to delete it unread, and the week that
// matters is the week nobody opens it.
//
// THE SCHEDULE LIVES IN THE DATABASE, not in config.toml, and Tick re-reads it
// on every pass. That is T-2807 AC5 stated as a design constraint rather than
// discovered as a bug: a schedule that needs a daemon restart to change is a
// schedule an operator changes once and then works around.
package digest
