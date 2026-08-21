//go:build linux

package host

import "testing"

// The regression this fixes: `systemctl is-active` prints "inactive" for a
// unit that does not exist, byte-identical to an installed-but-stopped one.
// vnprox therefore reported "dnsmasq is not running" on nodes where dnsmasq
// had never been installed — and, once T-3604 gave that finding a button,
// offered to start a unit that cannot exist.
//
// Every fixture below is real output from pvecube (systemd 257), captured in
// planning/reports/evidence/pve-9.2.4-systemctl-is-active.txt. The previous
// implementation's doc comment described behaviour this systemd does not
// have, which is precisely why it survived review: the code read correctly
// against a description nobody had checked.
func TestParseUnitShow(t *testing.T) {
	// Field order is fieldalignment's, not the reading order.
	tests := []struct {
		name       string
		out        string
		wantActive bool
		wantKnown  bool
	}{
		{
			name:      "dnsmasq on pvecube: package never installed",
			out:       "LoadState=not-found\nActiveState=inactive\n",
			wantKnown: false,
		},
		{
			name:      "frr on pvecube: installed, stopped",
			out:       "LoadState=loaded\nActiveState=inactive\n",
			wantKnown: true,
		},
		{
			name:       "pveproxy on pvecube: installed, running",
			out:        "LoadState=loaded\nActiveState=active\n",
			wantActive: true,
			wantKnown:  true,
		},
		{
			// A masked unit exists and an operator whose SDN DHCP is masked
			// wants to know. Reported as known-and-inactive; the start
			// attempt then surfaces systemd's own "Unit is masked."
			// rather than this code guessing at intent.
			name:      "masked unit is reported, not hidden",
			out:       "LoadState=masked\nActiveState=inactive\n",
			wantKnown: true,
		},
		{
			// Property order is not guaranteed; a positional read would
			// invert these two.
			name:       "properties in the opposite order",
			out:        "ActiveState=active\nLoadState=loaded\n",
			wantActive: true,
			wantKnown:  true,
		},
		{
			name:      "no output at all: no systemd, or systemctl absent",
			out:       "",
			wantKnown: false,
		},
		{
			name:      "a bad unit file is not a running service",
			out:       "LoadState=bad-setting\nActiveState=inactive\n",
			wantKnown: false,
		},
		{
			// The old implementation's whole input space. If someone
			// reverts to parsing `is-active` stdout, this is the fixture
			// that no longer distinguishes anything.
			name:      "bare is-active output carries no LoadState and is untrustworthy",
			out:       "inactive\n",
			wantKnown: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			active, known := parseUnitShow(tt.out)
			if known != tt.wantKnown {
				t.Errorf("known = %v, want %v", known, tt.wantKnown)
			}
			if known && active != tt.wantActive {
				t.Errorf("active = %v, want %v", active, tt.wantActive)
			}
		})
	}
}
