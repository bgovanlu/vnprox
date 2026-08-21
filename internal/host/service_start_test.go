package host

import "testing"

// T-3604. IsWatchedService is an authorisation decision, not a display
// detail: it is what stands between "start the SDN DHCP daemon" and "start
// anything on this host as root".
func TestIsWatchedService(t *testing.T) {
	for _, unit := range WatchedServices {
		if !IsWatchedService(unit) {
			t.Errorf("WatchedServices contains %q but IsWatchedService rejects it — the two have drifted", unit)
		}
	}
	// The exported var and the function are deliberately separate (the var
	// is mutable and any package in this binary could append to it), so
	// they are pinned against each other in both directions.
	if len(WatchedServices) != 2 {
		t.Errorf("WatchedServices = %v; if this list grew, T-3604's netWrite reasoning needs revisiting — see internal/api/servicestart.go", WatchedServices)
	}
	rejected := []string{
		"", "sshd", "pve-cluster", "pveproxy", "corosync",
		"dnsmasq.service", // the unit-file spelling is not the name we accept
		"DNSMASQ", "Frr",  // case matters
		"dnsmasq ", " frr", // no trimming: an allow-list that normalises is an allow-list with edge cases
		"dnsmasq; reboot", // shell metacharacters are irrelevant given fixed argv, and still rejected
		"../../bin/sh",
	}
	for _, unit := range rejected {
		if IsWatchedService(unit) {
			t.Errorf("IsWatchedService(%q) = true, want false", unit)
		}
	}
}
