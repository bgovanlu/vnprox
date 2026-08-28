// SPDX-License-Identifier: Apache-2.0

package qos

import (
	"reflect"
	"testing"
)

func intPtr(n int) *int { return &n }

func TestRenderTC(t *testing.T) {
	cases := []struct {
		name    string
		shape   Shape
		wantErr bool
	}{
		{
			name:  "whole-bridge shape (no match) becomes the root default",
			shape: Shape{ID: "s1", Node: "pve1", Bridge: "vmbr0", RateMbit: 10},
		},
		{
			name:  "matched-CIDR shape gets a filter, default stays class 1",
			shape: Shape{ID: "s2", Node: "pve1", Bridge: "vmbr0", MatchCIDR: "10.10.0.0/24", RateMbit: 5, CeilMbit: intPtr(20), Priority: intPtr(1)},
		},
		{
			name:  "matched-VLAN shape",
			shape: Shape{ID: "s3", Node: "pve1", Bridge: "vmbr0", MatchVlan: intPtr(100), RateMbit: 100},
		},
		{
			name:  "matched CIDR+VLAN shape chains both matches in one filter",
			shape: Shape{ID: "s4", Node: "pve1", Bridge: "vmbr0", MatchCIDR: "10.20.0.0/24", MatchVlan: intPtr(200), RateMbit: 50, CeilMbit: intPtr(100)},
		},
		{
			name:    "missing bridge",
			shape:   Shape{ID: "s5", RateMbit: 10},
			wantErr: true,
		},
		{
			name:    "non-positive rate",
			shape:   Shape{ID: "s6", Bridge: "vmbr0", RateMbit: 0},
			wantErr: true,
		},
		{
			name:    "ceil below rate",
			shape:   Shape{ID: "s7", Bridge: "vmbr0", RateMbit: 20, CeilMbit: intPtr(10)},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines, err := RenderTC(tc.shape)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("RenderTC(%+v): want error, got none", tc.shape)
				}
				return
			}
			if err != nil {
				t.Fatalf("RenderTC(%+v): unexpected error: %v", tc.shape, err)
			}
			for _, line := range lines {
				if len(line) == 0 || line[0] != "tc" {
					t.Fatalf("line %v does not start with argv[0]==\"tc\"", line)
				}
			}
		})
	}
}

// TestRenderTC_Golden pins the exact rendered argv for each shape shape so a
// future refactor cannot silently change the on-node tc invocation without
// a test failure calling it out.
func TestRenderTC_Golden(t *testing.T) {
	unmatched := Shape{ID: "s1", Node: "pve1", Bridge: "vmbr0", RateMbit: 10}
	got, err := RenderTC(unmatched)
	if err != nil {
		t.Fatalf("RenderTC: %v", err)
	}
	want := [][]string{
		{"tc", "qdisc", "replace", "dev", "vmbr0", "root", "handle", "1:", "htb", "default", classIDMinor(classID("s1"))},
		{"tc", "class", "replace", "dev", "vmbr0", "parent", "1:", "classid", classID("s1"), "htb", "rate", "10mbit"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RenderTC(unmatched) =\n%v\nwant\n%v", got, want)
	}

	matched := Shape{ID: "s2", Node: "pve1", Bridge: "vmbr0", MatchCIDR: "10.10.0.0/24", RateMbit: 5, CeilMbit: intPtr(20), Priority: intPtr(1)}
	got, err = RenderTC(matched)
	if err != nil {
		t.Fatalf("RenderTC: %v", err)
	}
	cid := classID("s2")
	want = [][]string{
		{"tc", "qdisc", "replace", "dev", "vmbr0", "root", "handle", "1:", "htb", "default", "1"},
		{"tc", "class", "replace", "dev", "vmbr0", "parent", "1:", "classid", cid, "htb", "rate", "5mbit", "ceil", "20mbit", "prio", "1"},
		{"tc", "filter", "replace", "dev", "vmbr0", "parent", "1:", "protocol", "802.1Q", "prio", "1", "u32", "match", "ip", "dst", "10.10.0.0/24", "flowid", cid},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RenderTC(matched) =\n%v\nwant\n%v", got, want)
	}
}

func TestRenderTC_Idempotent(t *testing.T) {
	s := Shape{ID: "s1", Node: "pve1", Bridge: "vmbr0", RateMbit: 10}
	a, err := RenderTC(s)
	if err != nil {
		t.Fatalf("RenderTC: %v", err)
	}
	b, err := RenderTC(s)
	if err != nil {
		t.Fatalf("RenderTC: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("RenderTC is not deterministic: %v != %v", a, b)
	}
}

func TestRenderTCTeardown(t *testing.T) {
	matched := Shape{ID: "s2", Bridge: "vmbr0", MatchCIDR: "10.10.0.0/24", RateMbit: 5}
	lines := RenderTCTeardown(matched)
	if len(lines) != 2 {
		t.Fatalf("RenderTCTeardown(matched): want 2 lines (filter del + class del), got %d: %v", len(lines), lines)
	}
	if lines[0][1] != "filter" || lines[1][1] != "class" {
		t.Fatalf("RenderTCTeardown(matched): want filter-del then class-del, got %v", lines)
	}

	unmatched := Shape{ID: "s1", Bridge: "vmbr0", RateMbit: 10}
	lines = RenderTCTeardown(unmatched)
	if len(lines) != 1 || lines[0][1] != "class" {
		t.Fatalf("RenderTCTeardown(unmatched): want a single class-del line, got %v", lines)
	}
}

func TestClassID_StableAndAvoidsReservedMinors(t *testing.T) {
	seen := map[string]bool{}
	for _, id := range []string{"a", "b", "c", "shape-1", "shape-2", ""} {
		cid := classID(id)
		if cid == unclassifiedClassID {
			t.Fatalf("classID(%q) collided with the reserved unclassified class %q", id, unclassifiedClassID)
		}
		if classID(id) != cid {
			t.Fatalf("classID(%q) is not deterministic", id)
		}
		seen[cid] = true
	}
}
