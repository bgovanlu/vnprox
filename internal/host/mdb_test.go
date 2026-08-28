// SPDX-License-Identifier: Apache-2.0

package host

import "testing"

// pvecubeMDBFixture is the exact `bridge -d -j mdb show` output captured
// from pvecube (PVE 9.2.4) on 2026-08-27 — see
// planning/reports/evidence/pve-9.2.4-bridge-mdb-2026-08-27.txt §4. Used
// verbatim (not reformatted) so this test exercises the real wire shape,
// not an idealized one.
const pvecubeMDBFixture = `[{"mdb":[{"index":6,"dev":"vmbr0","port":"enp1s0","grp":"ff02::fb","state":"temp","protocol":"kernel","flags":[]},{"index":8,"dev":"vmbr2","port":"enp3s0","grp":"ff02::fb","state":"temp","protocol":"kernel","flags":[]},{"index":12,"dev":"fwbr103i0","port":"fwln103i0","grp":"ff02::fb","state":"temp","protocol":"kernel","flags":[]},{"index":13,"dev":"fwbr104i0","port":"fwln104i0","grp":"ff02::fb","state":"temp","protocol":"kernel","flags":[]}],"router":{}}]`

func TestParseMDB(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []MDBRow
		wantErr bool
	}{
		{
			name: "empty input is not an error (T-3902: the common case observed on most pvecube bridges)",
			raw:  "",
			want: nil,
		},
		{
			name: "whitespace-only input is not an error",
			raw:  "   \n\t  ",
			want: nil,
		},
		{
			name: "real pvecube output: four temp/kernel IPv6 mDNS entries, sorted by bridge",
			raw:  pvecubeMDBFixture,
			want: []MDBRow{
				{Bridge: "fwbr103i0", Group: "ff02::fb", Port: "fwln103i0", State: "temp", Protocol: "kernel"},
				{Bridge: "fwbr104i0", Group: "ff02::fb", Port: "fwln104i0", State: "temp", Protocol: "kernel"},
				{Bridge: "vmbr0", Group: "ff02::fb", Port: "enp1s0", State: "temp", Protocol: "kernel"},
				{Bridge: "vmbr2", Group: "ff02::fb", Port: "enp3s0", State: "temp", Protocol: "kernel"},
			},
		},
		{
			name: "empty mdb table on a real bridge (e.g. vmbr1/vmbr3/vmbr99 on pvecube) renders no rows",
			raw:  `[{"mdb":[],"router":{}}]`,
			want: nil,
		},
		{
			name: "non-detail bridge -j mdb show (no protocol field) still parses",
			raw:  `[{"mdb":[{"index":6,"dev":"vmbr0","port":"enp1s0","grp":"ff02::fb","state":"temp","flags":[]}],"router":{}}]`,
			want: []MDBRow{{Bridge: "vmbr0", Group: "ff02::fb", Port: "enp1s0", State: "temp"}},
		},
		{
			name: "VLAN tag under the unverified 'vid' key is tolerated",
			raw:  `[{"mdb":[{"dev":"vmbr0","grp":"224.0.0.251","vid":100}],"router":{}}]`,
			want: []MDBRow{{Bridge: "vmbr0", Group: "224.0.0.251", Vlan: 100}},
		},
		{
			name: "VLAN tag under the unverified 'vlan' key is also tolerated",
			raw:  `[{"mdb":[{"dev":"vmbr0","grp":"224.0.0.251","vlan":200}],"router":{}}]`,
			want: []MDBRow{{Bridge: "vmbr0", Group: "224.0.0.251", Vlan: 200}},
		},
		{
			name: "a permanent entry (never observed on pvecube, but bridge's own documented state) round-trips untouched",
			raw:  `[{"mdb":[{"dev":"vmbr0","grp":"239.1.1.1","state":"permanent","protocol":"static"}],"router":{}}]`,
			want: []MDBRow{{Bridge: "vmbr0", Group: "239.1.1.1", State: "permanent", Protocol: "static"}},
		},
		{
			name:    "malformed JSON is a parse error, not a panic",
			raw:     `not json at all`,
			wantErr: true,
		},
		{
			name:    "a bare object instead of the expected array is a parse error",
			raw:     `{"mdb":[]}`,
			wantErr: true,
		},
		{
			name: "an entry with neither dev nor grp is skipped defensively",
			raw:  `[{"mdb":[{"port":"eth0"}],"router":{}}]`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMDB([]byte(tt.raw))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseMDB(%q) = nil error, want an error", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMDB(%q): unexpected error: %v", tt.raw, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ParseMDB(%q) = %d rows, want %d: %+v", tt.raw, len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("row %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestParseMDB_NeverPanics is a lightweight fuzz-adjacent smoke test:
// several adversarial/truncated inputs must never panic, mirroring
// ParseBGPSummary/ParseEVPNVNI's own panic-recovery convention (frr.go).
func TestParseMDB_NeverPanics(t *testing.T) {
	inputs := []string{
		"[",
		"[{}]",
		"[{\"mdb\":null}]",
		"[{\"mdb\":[null]}]",
		"[null]",
		"[[]]",
		"[{\"mdb\":[{\"dev\":null}]}]",
		"[{\"mdb\":[{\"vid\":\"not-a-number\"}]}]",
	}
	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("ParseMDB(%q) panicked: %v", in, r)
				}
			}()
			_, _ = ParseMDB([]byte(in))
		}()
	}
}
