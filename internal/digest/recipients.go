// SPDX-License-Identifier: Apache-2.0

package digest

import (
	"context"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/findings"
)

// RecipientFilter narrows T-2407's alert-rule fan-out to the schedule's
// recipient list.
//
// It is a findings.AlertRuleProvider, which means the digest's own
// *findings.WebhookNotifier is constructed from exactly the same code as the
// alerting one — same retry, same quiet hours, same delivery log — and differs
// only in WHICH rules it can see. That is the cheapest possible way to have
// configurable recipients without a second address book that could disagree
// with the alert targets, and without a second delivery path.
//
// An EMPTY recipient list means every rule, which is the ordinary fan-out and
// the same "empty filter matches everything" contract every other filter in
// this codebase follows.
type RecipientFilter struct {
	Rules findings.AlertRuleProvider
	Store Store
}

var _ findings.AlertRuleProvider = RecipientFilter{}

// AlertRules returns the rules the digest may be delivered to.
//
// A schedule that cannot be read is an ERROR, not a fallback to "every rule".
// Widening the recipient list because a query failed would deliver a digest to
// targets the operator explicitly excluded, and the operator would have no way
// to know it happened.
func (f RecipientFilter) AlertRules(ctx context.Context) ([]findings.AlertRule, error) {
	if f.Rules == nil {
		return nil, nil
	}
	rules, err := f.Rules.AlertRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("digest: listing alert rules for the digest recipients: %w", err)
	}
	if f.Store == nil {
		return rules, nil
	}

	sched, ok, err := f.Store.Schedule(ctx)
	if err != nil {
		return nil, fmt.Errorf("digest: reading the digest schedule's recipients: %w", err)
	}
	if !ok || len(sched.RuleIDs) == 0 {
		return rules, nil
	}

	allow := make(map[string]bool, len(sched.RuleIDs))
	for _, id := range sched.RuleIDs {
		allow[id] = true
	}
	out := make([]findings.AlertRule, 0, len(rules))
	for _, rule := range rules {
		if allow[rule.ID] {
			out = append(out, rule)
		}
	}
	return out, nil
}
