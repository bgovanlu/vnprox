// SPDX-License-Identifier: Apache-2.0

package route

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// policyRuleJSON is the wire shape of one entry in `ip -j rule show` (and
// `-6`'s) top-level JSON array — see
// planning/reports/evidence/pve-9.2.4-routing-2026-08-28.txt. Only the
// three fields every observed rule carried (priority/src/table) are kept;
// a real VRF-lite/policy-routing configuration can add fwmark/iif/oif/
// suppress_* fields this task's fixture cluster never exercises —
// encoding/json silently drops any field this struct doesn't name, which
// is the correct behavior here (an unrecognized selector on a rule this
// tool doesn't evaluate should not fail the whole parse; Lookup's own doc
// comment names which rule shapes it does and doesn't evaluate).
type policyRuleJSON struct {
	Src      string `json:"src"`
	Table    string `json:"table"`
	Priority int    `json:"priority"`
}

// ParsePolicyRules parses `ip -j rule show` (afi == AFIv4) or
// `ip -j -6 rule show` (afi == AFIv6) output into PolicyRule values. Same
// panic-recovery/whole-document-fails convention as ParseFIBRoutes (see
// its doc comment) — a policy-routing rule list is short and
// safety-relevant, not a place to silently drop an entry. Empty input
// (a family with no rules — the evidence transcript's `ip -j -6 rule show`
// output has only two, not the three v4 has) returns nil, no error.
func ParsePolicyRules(raw []byte, afi AFI) (rules []PolicyRule, err error) {
	defer func() {
		if r := recover(); r != nil {
			rules, err = nil, fmt.Errorf("route: rules: parser panic recovered: %v", r)
		}
	}()

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}

	var entries []policyRuleJSON
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return nil, fmt.Errorf("route: rules: parsing %s policy rules: %w", afi, err)
	}

	out := make([]PolicyRule, 0, len(entries))
	for _, e := range entries {
		out = append(out, PolicyRule{
			AFI:      afi,
			Priority: e.Priority,
			Src:      e.Src,
			Table:    e.Table,
		})
	}
	return out, nil
}
