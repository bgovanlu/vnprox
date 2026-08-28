//go:build linux

// SPDX-License-Identifier: Apache-2.0

package host

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// containerPID resolves vmid to its lxc init process's pid via
// pve-container's cgroup layout.
//
// **Needs hardware validation** (flagged per CLAUDE.md and this task's
// card, planning/reports/needs-hardware-validation.md carries the tracking
// entry): PVE 8.x's default cgroupv2-unified layout places a running
// container's processes under /sys/fs/cgroup/lxc/<vmid>/cgroup.procs — this
// function implements exactly that one deliberately narrow target profile
// (matching internal/probe/command.go's own "one profile, not a guess"
// precedent) rather than also attempting a cgroupv1 fallback, which this
// codebase has no fixture or hardware to verify against.
func containerPID(vmid int) (int, error) {
	path := fmt.Sprintf("/sys/fs/cgroup/lxc/%d/cgroup.procs", vmid)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("host: resolving container %d's pid via %s: %w", vmid, path, err)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("host: container %d's cgroup.procs (%s) is empty — container may not be running", vmid, path)
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, fmt.Errorf("host: parsing container %d's pid from %s: %w", vmid, path, err)
	}
	return pid, nil
}

// nsenterExec runs argv0/argv... inside pid's network namespace via
// `nsenter --net=/proc/<pid>/ns/net -- <argv0> <argv...>`, using r's
// configured NsenterPath, returning combined stdout (stderr is discarded —
// every caller below only wants stdout on success and treats any non-zero
// exit as a plain error, matching internal/pve's own AgentExec "the exit
// code is the answer" contract).
func (r *Real) nsenterExec(ctx context.Context, pid int, argv0 string, argv ...string) ([]byte, error) {
	full := append([]string{"--net=/proc/" + strconv.Itoa(pid) + "/ns/net", "--", argv0}, argv...)
	cmd := exec.CommandContext(ctx, r.NsenterPath, full...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("host: nsenter --net=/proc/%d/ns/net -- %s %s: %w", pid, argv0, strings.Join(argv, " "), err)
	}
	return out.Bytes(), nil
}

// ContainerInterior implements Reader (T-1304): resolves vmid's pid, then
// reads its network namespace's interfaces/routes/listening sockets via
// nsenter+ip/ss, and its container rootfs's /etc/resolv.conf directly (a
// plain file read needs no netns entry). A missing resolv.conf (container
// has none, or the read fails for any reason) degrades to an empty
// ResolvConf rather than failing the whole read — matching
// GetGuestAgentInterfaces' "absent is normal, not a fault" convention.
func (r *Real) ContainerInterior(ctx context.Context, _ string, vmid int) (ContainerInteriorRaw, error) {
	pid, err := containerPID(vmid)
	if err != nil {
		return ContainerInteriorRaw{}, err
	}

	addrJSON, err := r.nsenterExec(ctx, pid, r.IPPath, "-j", "addr", "show")
	if err != nil {
		return ContainerInteriorRaw{}, fmt.Errorf("host: container %d: reading interfaces/addresses: %w", vmid, err)
	}
	routeJSON, err := r.nsenterExec(ctx, pid, r.IPPath, "-j", "route", "show")
	if err != nil {
		return ContainerInteriorRaw{}, fmt.Errorf("host: container %d: reading routes: %w", vmid, err)
	}
	sockets, err := r.nsenterExec(ctx, pid, r.SSPath, "-H", "-tuln")
	if err != nil {
		return ContainerInteriorRaw{}, fmt.Errorf("host: container %d: reading listening sockets: %w", vmid, err)
	}
	resolvConf, _ := os.ReadFile(fmt.Sprintf("/proc/%d/root/etc/resolv.conf", pid))

	return ContainerInteriorRaw{
		AddrJSON: addrJSON, RouteJSON: routeJSON, ResolvConf: resolvConf, Sockets: sockets,
	}, nil
}

// ContainerPing implements Reader (T-1304): a single best-effort ping from
// inside vmid's network namespace toward targetIP. Any failure (pid
// resolution, nsenter/ping transport error, or a genuine "no reply" —
// ping's own exit code does not distinguish those beyond "0 means a reply
// arrived") reports false, never an error — the caller (internal/
// guestinterior) treats this the same "the attempt itself may fail
// silently" way internal/probe.Run's qemu-path ping does, just without a
// separate OutcomeError bucket (see ContainerInteriorRaw's doc comment on
// reader.go for why).
func (r *Real) ContainerPing(ctx context.Context, _ string, vmid int, targetIP string) (bool, error) {
	pid, err := containerPID(vmid)
	if err != nil {
		return false, nil //nolint:nilerr // best-effort: pid resolution failure means "couldn't reach it", not a transport fault worth propagating
	}
	_, err = r.nsenterExec(ctx, pid, r.PingPath, "-c", "1", "-W", "2", targetIP)
	return err == nil, nil
}
