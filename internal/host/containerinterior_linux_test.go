//go:build linux

package host

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"testing"
)

// TestContainerPID_NotFound exercises the honest-error path: no
// /sys/fs/cgroup/lxc/<vmid> at all (true on every non-PVE sandbox,
// including CI) always errors rather than panicking or guessing a pid.
func TestContainerPID_NotFound(t *testing.T) {
	if _, err := containerPID(999999); err == nil {
		t.Fatal("containerPID(999999) = nil error, want an error (no such cgroup on this sandbox)")
	}
}

// TestReal_ContainerInterior_LiveLXC is a best-effort integration test: if
// this sandbox is actually a PVE host running at least one lxc container
// (or otherwise has a matching cgroup layout), exercise the real
// nsenter+ip/ss exec path end to end. Skipped otherwise — no CI runner or
// plain dev sandbox has this, matching TestReal_FRRBGPSummary_Installed's
// own "skip if the real dependency isn't present" convention.
func TestReal_ContainerInterior_LiveLXC(t *testing.T) {
	entries, err := os.ReadDir("/sys/fs/cgroup/lxc")
	if err != nil || len(entries) == 0 {
		t.Skip("no /sys/fs/cgroup/lxc on this sandbox (not a PVE host, or no lxc guests running) — skipping live container-interior integration test")
	}
	r := NewReal()
	for _, bin := range []string{r.NsenterPath, r.IPPath, r.SSPath} {
		if _, lookErr := exec.LookPath(bin); lookErr != nil {
			t.Skipf("%s not installed on this sandbox — skipping live container-interior integration test", bin)
		}
	}

	vmid, err := strconv.Atoi(entries[0].Name())
	if err != nil {
		t.Skipf("could not parse a vmid from cgroup entry %q: %v", entries[0].Name(), err)
	}

	raw, err := r.ContainerInterior(context.Background(), "", vmid)
	if err != nil {
		t.Fatalf("ContainerInterior(%d): %v", vmid, err)
	}
	if len(raw.AddrJSON) == 0 {
		t.Errorf("ContainerInterior(%d).AddrJSON is empty, want at least loopback's ip -j addr show output", vmid)
	}
	t.Logf("live container %d: addrJSON=%d bytes routeJSON=%d bytes sockets=%d bytes", vmid, len(raw.AddrJSON), len(raw.RouteJSON), len(raw.Sockets))
}
