// SPDX-License-Identifier: Apache-2.0

package tcmirror

import (
	"reflect"
	"testing"
)

func TestRenderTC(t *testing.T) {
	cases := []struct {
		name    string
		session Session
		want    [][]string
		wantErr bool
	}{
		{
			name:    "basic",
			session: Session{ID: "span1", Node: "pvecube", SourceIface: "vmbr2", DestIface: "vmbr99"},
			want: [][]string{
				{"tc", "qdisc", "add", "dev", "vmbr2", "clsact"},
				{"tc", "filter", "add", "dev", "vmbr2", "ingress", "protocol", "all", "prio", "1", "matchall", "action", "mirred", "egress", "mirror", "dev", "vmbr99"},
				{"tc", "filter", "add", "dev", "vmbr2", "egress", "protocol", "all", "prio", "1", "matchall", "action", "mirred", "egress", "mirror", "dev", "vmbr99"},
			},
		},
		{
			name:    "missing source",
			session: Session{ID: "span2", DestIface: "vmbr99"},
			wantErr: true,
		},
		{
			name:    "missing dest",
			session: Session{ID: "span3", SourceIface: "vmbr2"},
			wantErr: true,
		},
		{
			name:    "source equals dest",
			session: Session{ID: "span4", SourceIface: "vmbr2", DestIface: "vmbr2"},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RenderTC(tc.session)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("RenderTC(%+v): want error, got nil", tc.session)
				}
				return
			}
			if err != nil {
				t.Fatalf("RenderTC(%+v): unexpected error: %v", tc.session, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("RenderTC(%+v) =\n%v\nwant\n%v", tc.session, got, tc.want)
			}
		})
	}
}

func TestRenderTCTeardown(t *testing.T) {
	s := Session{ID: "span1", SourceIface: "vmbr2", DestIface: "vmbr99"}
	want := [][]string{
		{"tc", "filter", "del", "dev", "vmbr2", "ingress"},
		{"tc", "filter", "del", "dev", "vmbr2", "egress"},
		{"tc", "qdisc", "del", "dev", "vmbr2", "clsact"},
	}
	got := RenderTCTeardown(s)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RenderTCTeardown(%+v) =\n%v\nwant\n%v", s, got, want)
	}
}

func TestRenderTCIdempotencyIsDeliberatelyAdd(t *testing.T) {
	// RenderTC always uses "add", never "replace" (session.go's doc
	// comment): a second call for the same source must fail at the tc
	// layer, not silently steal the qdisc from another session. This test
	// documents that contract at the argv level (no live tc invoked, per
	// this task's read-only constraint).
	s := Session{ID: "span1", SourceIface: "vmbr2", DestIface: "vmbr99"}
	lines, err := RenderTC(s)
	if err != nil {
		t.Fatalf("RenderTC: %v", err)
	}
	if lines[0][2] != "add" {
		t.Fatalf("qdisc line verb = %q, want %q", lines[0][2], "add")
	}
	for _, l := range lines[1:] {
		if l[2] != "add" {
			t.Fatalf("filter line verb = %q, want %q", l[2], "add")
		}
	}
}
