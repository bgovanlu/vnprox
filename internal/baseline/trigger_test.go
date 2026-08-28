// SPDX-License-Identifier: Apache-2.0

package baseline

import "testing"

func TestAnomaly_CaptureFilter(t *testing.T) {
	// Field order is densest-pointer-first: both strings precede the struct,
	// since govet's fieldalignment counts bytes up to the final pointer.
	cases := []struct {
		name string
		want string
		a    Anomaly
	}{
		{
			name: "new_port",
			a:    Anomaly{Class: ClassNewPort, Subject: "tcp/6667"},
			want: "port 6667",
		},
		{
			name: "new_port unnamed proto",
			a:    Anomaly{Class: ClassNewPort, Subject: "17/500"},
			want: "port 500",
		},
		{
			name: "new_port malformed subject",
			a:    Anomaly{Class: ClassNewPort, Subject: "not-a-port"},
			want: "",
		},
		{
			name: "new_subnet v4",
			a:    Anomaly{Class: ClassNewSubnet, Subject: "10.9.0.0/24"},
			want: "net 10.9.0.0/24",
		},
		{
			name: "new_subnet v6",
			a:    Anomaly{Class: ClassNewSubnet, Subject: "fd00:9::/64"},
			want: "net fd00:9::/64",
		},
		{
			name: "new_subnet malformed subject",
			a:    Anomaly{Class: ClassNewSubnet, Subject: "not-a-cidr"},
			want: "",
		},
		{
			name: "volume_spike is not filterable",
			a:    Anomaly{Class: ClassVolumeSpike, Subject: "2024-01-15T14:00Z"},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.CaptureFilter(); got != tc.want {
				t.Errorf("CaptureFilter() = %q, want %q", got, tc.want)
			}
		})
	}
}
