// SPDX-License-Identifier: Apache-2.0

package change

import "testing"

// TestSDNPrefixListsAndRouteMapsHaveNoChangesetOp pins T-3101's explicit
// scoping decision from the Go op-vocabulary side (the HTTP-route side —
// no POST/PUT/DELETE handler exists for either path — is pinned by
// internal/pvemock's TestSDNPrefixListsAndRouteMapsAreReadOnly): prefix-
// lists and route-maps are read-only in vnprox, so no op type in this
// package's vocabulary may exist for either family. Every known OpType is
// asserted against by prefix so a future op named e.g.
// "sdn.prefix-list.create" fails here rather than only in code review.
func TestSDNPrefixListsAndRouteMapsHaveNoChangesetOp(t *testing.T) {
	for _, ot := range allOpTypeConstants {
		s := string(ot)
		if hasPrefix(s, "sdn.prefix-list.") || hasPrefix(s, "sdn.route-map.") {
			t.Errorf("op type %q exists for a family T-3101 scoped read-only (prefix-lists/route-maps); "+
				"CRUD for these is explicitly out of scope — see planning/tasks/phase-31.md's T-3101 section", s)
		}
	}
	// Symmetric check against the actual decode-time registry, not just the
	// hand-maintained constant list above — the two are asserted equal
	// elsewhere (TestKnownOpTypes_MatchesVocabulary), but this test's whole
	// point is "assert something concrete", so it checks both directly.
	for ot := range paramFactories {
		s := string(ot)
		if hasPrefix(s, "sdn.prefix-list.") || hasPrefix(s, "sdn.route-map.") {
			t.Errorf("paramFactories has an entry for %q; prefix-lists/route-maps are read-only (T-3101)", s)
		}
	}
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
