//go:build linux

package host

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/vishvananda/netlink"
)

// canReadNetlink probes whether this process can actually list netlink
// links right now. Reading link state (as opposed to changing it) does
// not require CAP_NET_ADMIN on a normal Linux system, but some sandboxes
// deny AF_NETLINK sockets outright (seccomp, unprivileged user namespaces
// without /proc/sys/net exposed, etc); this lets the integration tests
// below skip cleanly rather than fail when that's the case, per T-102 AC3
// ("skippable-if-unprivileged").
func canReadNetlink(t *testing.T) bool {
	t.Helper()
	_, err := netlink.LinkList()
	return err == nil
}

// TestReal_Links is the netlink integration test: it exercises Real.Links
// against this sandbox's actual live network state. It does not assert
// anything about specific NICs (hardware varies per machine/CI runner) —
// only that the loopback interface is always present and reports sane
// baseline fields, which is true on every Linux host regardless of
// privilege level.
func TestReal_Links(t *testing.T) {
	if !canReadNetlink(t) {
		t.Skip("this sandbox cannot open netlink sockets (no CAP_NET_ADMIN/netlink access) — skipping live netlink integration test")
	}

	r := NewReal()
	links, err := r.Links(context.Background(), "")
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if len(links) == 0 {
		t.Fatalf("Links returned no interfaces at all")
	}

	var lo *LinkState
	for i := range links {
		if links[i].Name == "lo" {
			lo = &links[i]
		}
	}
	if lo == nil {
		t.Fatalf("loopback interface 'lo' not found in %+v", links)
	}
	if !lo.LinkUp {
		t.Errorf("lo.LinkUp = false, want true (loopback is always up)")
	}
	if lo.MTU <= 0 {
		t.Errorf("lo.MTU = %d, want > 0", lo.MTU)
	}
	t.Logf("read %d live interfaces from netlink; lo = %+v", len(links), *lo)
}

// TestReal_Stats exercises Real.Stats against this sandbox's actual
// /sys/class/net/*/statistics tree, which — unlike netlink — is plain
// file I/O and needs no special privilege, so this test does not skip.
func TestReal_Stats(t *testing.T) {
	r := NewReal()
	stats, err := r.Stats(context.Background(), "")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if _, ok := stats["lo"]; !ok {
		t.Errorf("Stats() = %+v, want an entry for lo", stats)
	}
}

// TestReal_InterfacesFile exercises Real.InterfacesFile against this
// sandbox's actual /etc/network/interfaces, skipping cleanly if the file
// does not exist (e.g. a non-Debian-based sandbox, or one managed by
// systemd-networkd/netplan instead of ifupdown).
func TestReal_InterfacesFile(t *testing.T) {
	r := NewReal()
	raw, err := r.InterfacesFile(context.Background(), "", false)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
			t.Skipf("no /etc/network/interfaces on this sandbox (not ifupdown-managed): %v", err)
		}
		t.Fatalf("InterfacesFile: %v", err)
	}
	pf, err := ParseInterfaces([]byte(raw))
	if err != nil {
		t.Fatalf("this sandbox's real /etc/network/interfaces did not parse: %v\n%s", err, raw)
	}
	if got := pf.Render(); got != raw {
		t.Errorf("this sandbox's real /etc/network/interfaces did not round-trip through the parser")
	}
	t.Logf("parsed %d entries from the real interfaces file", len(pf.Entries))
}

// TestReal_LLDP exercises Real.LLDP, skipping cleanly when lldpd's
// lldpctl binary is not installed — LLDP data is strictly a userspace
// daemon protocol with no netlink/kernel source, so there is no
// unprivileged fallback the way there is for links/stats/ethtool.
func TestReal_LLDP(t *testing.T) {
	r := NewReal()
	if _, err := exec.LookPath(r.LLDPCommand[0]); err != nil {
		t.Skipf("lldpd (%s) not installed on this sandbox — skipping LLDP integration test", r.LLDPCommand[0])
	}
	if _, err := r.LLDP(context.Background(), ""); err != nil {
		t.Fatalf("LLDP: %v", err)
	}
}

// TestReal_BondDetail_NoLiveBonds exercises the /proc/net/bonding path
// against this sandbox, skipping cleanly (rather than failing) when the
// bonding driver isn't loaded/no bonds exist here — which is the common
// case for a plain VM/CI runner with a single virtio NIC.
func TestReal_BondDetail_NoLiveBonds(t *testing.T) {
	entries, err := os.ReadDir(procNetBondingDir)
	if err != nil {
		t.Skipf("no /proc/net/bonding on this sandbox (bonding driver not loaded, no bonds configured): %v", err)
	}
	for _, e := range entries {
		bd, err := readBondDetail(e.Name())
		if err != nil {
			t.Errorf("readBondDetail(%s): %v", e.Name(), err)
			continue
		}
		t.Logf("live bond %s: mode=%s slaves=%d", e.Name(), bd.Mode, len(bd.Slaves))
	}
}
