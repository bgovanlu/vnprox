package host

import (
	"context"
	"os"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// procNetARPGolden is a golden /proc/net/arp sample (T-805 acceptance
// criterion 1): a resolved entry (flags 0x2, ATF_COMPLETE), a permanent
// entry (flags 0x6, ATF_COMPLETE|ATF_PERM), and an incomplete entry (flags
// 0x0, still-zero MAC — the kernel's "waiting on ARP" state) that must be
// excluded.
const procNetARPGolden = `IP address       HW type     Flags       HW address            Mask     Device
192.168.1.10     0x1         0x2         08:00:27:12:34:56     *        eth0
192.168.1.11     0x1         0x0         00:00:00:00:00:00     *        eth0
192.168.1.1      0x1         0x6         08:00:27:aa:bb:cc     *        eth0
`

func TestParseProcNetARP_GoldenSample(t *testing.T) {
	got, err := ParseProcNetARP([]byte(procNetARPGolden))
	if err != nil {
		t.Fatalf("ParseProcNetARP: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d neighbors, want 2 (0x0/incomplete excluded): %+v", len(got), got)
	}

	byIP := map[string]Neighbor{}
	for _, n := range got {
		byIP[n.IP] = n
	}

	reachable, ok := byIP["192.168.1.10"]
	if !ok {
		t.Fatal("192.168.1.10 (resolved, flags 0x2) missing")
	}
	if reachable.MAC != "08:00:27:12:34:56" || reachable.Iface != "eth0" || reachable.State != NeighborReachable {
		t.Errorf("reachable entry = %+v, want mac=08:00:27:12:34:56 iface=eth0 state=REACHABLE", reachable)
	}

	permanent, ok := byIP["192.168.1.1"]
	if !ok {
		t.Fatal("192.168.1.1 (permanent, flags 0x6) missing")
	}
	if permanent.State != NeighborPermanent {
		t.Errorf("permanent entry state = %q, want PERMANENT", permanent.State)
	}

	if _, ok := byIP["192.168.1.11"]; ok {
		t.Error("192.168.1.11 (incomplete, flags 0x0) must be excluded")
	}
}

func TestParseProcNetARP_MalformedLinesSkipped(t *testing.T) {
	data := "IP address HW type Flags HW address Mask Device\n" +
		"not enough fields\n" +
		"10.0.0.5  0x1  not-hex  aa:bb:cc:dd:ee:ff  *  eth0\n" +
		"\n" +
		"10.0.0.6     0x1         0x2         aa:bb:cc:dd:ee:01     *        eth1\n"
	got, err := ParseProcNetARP([]byte(data))
	if err != nil {
		t.Fatalf("ParseProcNetARP: %v", err)
	}
	if len(got) != 1 || got[0].IP != "10.0.0.6" {
		t.Fatalf("got = %+v, want just the one well-formed entry", got)
	}
}

func TestReadProcNetARP_UsesOverridablePath(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/arp"
	if err := os.WriteFile(path, []byte(procNetARPGolden), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	old := procNetARPPath
	procNetARPPath = path
	t.Cleanup(func() { procNetARPPath = old })

	got, err := readProcNetARP()
	if err != nil {
		t.Fatalf("readProcNetARP: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d neighbors, want 2", len(got))
	}
}

// TestFixtureReader_Neighbors is T-805 acceptance criterion 1's fixture
// half: a hand-built fixture (no shared testdata/clusters/ file needed for
// this pure adapter-level test — see the ipam-lab.yaml-based tests in
// internal/ipam for the end-to-end merge/confidence-label acceptance
// criteria) declaring a resolved, a stale, and a FAILED/INCOMPLETE
// neighbor, proving FixtureReader.Neighbors filters to resolved states
// exactly like the real parser does.
func TestFixtureReader_Neighbors(t *testing.T) {
	f := &pvemock.Fixture{
		Cluster: pvemock.ClusterSpec{Name: "neighbor-test", Quorate: true, Nodes: []pvemock.ClusterNodeSpec{{Name: "pve1", IP: "10.0.0.1", Online: true}}},
		Users:   []pvemock.UserSpec{{UserID: "root@pam", Password: "x", Privileges: []string{"*"}}},
		Nodes: map[string]*pvemock.NodeSpec{
			"pve1": {
				Neighbors: []pvemock.NeighborSpec{
					{IP: "10.50.0.55", Mac: "aa:bb:cc:dd:ee:01", Iface: "vmbr0", State: "REACHABLE"},
					{IP: "10.50.0.56", Mac: "aa:bb:cc:dd:ee:02", Iface: "vmbr0", State: "STALE"},
					{IP: "10.50.0.57", Mac: "00:00:00:00:00:00", Iface: "vmbr0", State: "INCOMPLETE"},
					{IP: "10.50.0.58", Mac: "aa:bb:cc:dd:ee:04", Iface: "vmbr0", State: "FAILED"},
					{IP: "10.50.0.59", Mac: "aa:bb:cc:dd:ee:05", Iface: "vmbr0"}, // no state -> defaults REACHABLE
				},
			},
		},
	}
	srv := pvemock.NewServer(f)
	r := NewFixtureReader(pvemock.NewFixtureHostReader(srv))

	got, err := r.Neighbors(context.Background(), "pve1")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	byIP := map[string]Neighbor{}
	for _, n := range got {
		byIP[n.IP] = n
	}
	if len(got) != 3 {
		t.Fatalf("got %d neighbors, want 3 (FAILED/INCOMPLETE excluded): %+v", len(got), got)
	}
	for _, want := range []string{"10.50.0.55", "10.50.0.56", "10.50.0.59"} {
		if _, ok := byIP[want]; !ok {
			t.Errorf("missing resolved entry %s", want)
		}
	}
	for _, exclude := range []string{"10.50.0.57", "10.50.0.58"} {
		if _, ok := byIP[exclude]; ok {
			t.Errorf("unresolved entry %s must be excluded", exclude)
		}
	}

	if _, err := r.Neighbors(context.Background(), "nosuchnode"); err == nil {
		t.Fatal("expected an error for an unknown node")
	}
}
