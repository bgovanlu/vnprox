// SPDX-License-Identifier: Apache-2.0

package pvecassette_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pvecassette"
)

// TestRequestKey_QueryMatching is T-2502 AC4: query-string matching is
// order-independent but value-sensitive, proven by a pair of cases.
//
// The pair is the point. "Order-independent" alone is satisfied by a
// matcher that ignores the query entirely — which would make
// `/cluster/sdn/zones` and `/cluster/sdn/zones?running=1` the same
// cassette, and those two return *different data* (configured vs running
// SDN state, the exact distinction internal/sdn's pending-changes logic is
// built on). So every "these must match" row below is paired with a "these
// must not" row that differs only in a value.
func TestRequestKey_QueryMatching(t *testing.T) {
	cases := []struct {
		name      string
		a, b      string // full request lines: "GET /path?query"
		wantMatch bool
	}{
		{
			name:      "key order does not matter",
			a:         "GET /api2/json/nodes/pve1/network?type=bridge&node=pve1",
			b:         "GET /api2/json/nodes/pve1/network?node=pve1&type=bridge",
			wantMatch: true,
		},
		{
			name:      "a different value is a different request",
			a:         "GET /api2/json/nodes/pve1/network?type=bridge",
			b:         "GET /api2/json/nodes/pve1/network?type=bond",
			wantMatch: false,
		},
		{
			name:      "repeated parameters match in any order",
			a:         "PUT /api2/json/cluster/firewall/rules/0?delete=comment&delete=dport",
			b:         "PUT /api2/json/cluster/firewall/rules/0?delete=dport&delete=comment",
			wantMatch: true,
		},
		{
			name:      "one repeated value changed is a different request",
			a:         "PUT /api2/json/cluster/firewall/rules/0?delete=comment&delete=dport",
			b:         "PUT /api2/json/cluster/firewall/rules/0?delete=comment&delete=sport",
			wantMatch: false,
		},
		{
			name:      "an absent query and an empty one are the same request",
			a:         "GET /api2/json/cluster/status",
			b:         "GET /api2/json/cluster/status?",
			wantMatch: true,
		},
		{
			name:      "a present-but-empty value is not the same as an absent parameter",
			a:         "GET /api2/json/cluster/sdn/zones",
			b:         "GET /api2/json/cluster/sdn/zones?running=",
			wantMatch: false,
		},
		{
			name:      "the method is part of the identity",
			a:         "GET /api2/json/nodes/pve1/network",
			b:         "PUT /api2/json/nodes/pve1/network",
			wantMatch: false,
		},
		{
			name:      "the path is part of the identity",
			a:         "GET /api2/json/nodes/pve1/network",
			b:         "GET /api2/json/nodes/pve2/network",
			wantMatch: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ka, kb := parseKey(t, tc.a), parseKey(t, tc.b)
			if match := ka == kb; match != tc.wantMatch {
				t.Errorf("match = %v, want %v\n  %s -> %q\n  %s -> %q", match, tc.wantMatch, tc.a, ka, tc.b, kb)
			}
		})
	}
}

func parseKey(t *testing.T, requestLine string) string {
	t.Helper()
	method, target, ok := strings.Cut(requestLine, " ")
	if !ok {
		t.Fatalf("malformed request line %q", requestLine)
	}
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parsing %q: %v", target, err)
	}
	return pvecassette.RequestKey(method, u.Path, u.Query())
}

// TestFileName_IsDerivedFromTheKey: re-recording the same request must
// overwrite its cassette rather than accumulate near-duplicates, and two
// requests that differ only in their query must not collide.
func TestFileName_IsDerivedFromTheKey(t *testing.T) {
	base := pvecassette.Cassette{PVEVersion: "8.3.5", Method: "GET", Path: "/api2/json/cluster/sdn/zones", Status: 200}
	running := base
	running.Query = map[string][]string{"running": {"1"}}

	if base.FileName() == running.FileName() {
		t.Errorf("two different requests share a file name: %s", base.FileName())
	}
	again := base
	again.Body = "a later recording of the same request"
	if base.FileName() != again.FileName() {
		t.Errorf("the same request produced two file names: %s vs %s", base.FileName(), again.FileName())
	}
	if !strings.HasPrefix(base.FileName(), "GET_cluster_sdn_zones_") {
		t.Errorf("file name is not readable at a glance: %s", base.FileName())
	}
}
