// SPDX-License-Identifier: Apache-2.0

package guestinterior

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bgovanlu/vnprox/internal/probe"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// ErrAgentUnreachable is returned by FetchQEMU when the target guest's
// QEMU guest agent could not even be reached for the one read this
// package treats as load-bearing (GetGuestAgentInterfaces) — mirrors
// internal/pve's own "any error from this method means no agent data
// available" convention (GetGuestAgentInterfaces' doc comment), promoted
// to a sentinel here since callers (internal/api) need to distinguish
// "guest has no agent" from a genuine data problem to answer honestly.
var ErrAgentUnreachable = errors.New("guestinterior: qemu guest agent unreachable")

// execPollTimeout bounds how long fetchExec polls exec-status for one
// guest-agent-execed read before giving up — generous relative to
// internal/probe.DefaultTimeout since these commands (ip/cat/ss) run
// in-guest with no network wait, unlike a network probe.
const execPollTimeout = 8 * time.Second

const execPollInterval = 150 * time.Millisecond

// QEMUClient is the subset of *pve.Client FetchQEMU needs: T-802's
// existing guest-agent exec/exec-status seam (probe.PVEExecer) plus
// GetGuestAgentInterfaces for the addresses/interfaces read — the same
// combination internal/api's simulate.go already type-asserts a live
// ProbeClientProvider's value into via its own guestAgentInterfaceReader
// seam.
type QEMUClient interface {
	probe.PVEExecer
	GetGuestAgentInterfaces(ctx context.Context, node string, vmid int) ([]pve.AgentIface, error)
}

// FetchQEMU builds a qemu guest's interior View via the QEMU guest agent
// (T-802's AgentExec/AgentExecStatus/GetGuestAgentInterfaces seam,
// reused — not duplicated). Returns ErrAgentUnreachable-wrapping error if
// the agent cannot even answer network-get-interfaces; every other read
// (routes/DNS/listening sockets/gateway reachability) degrades to an
// empty/false result on its own failure rather than failing the whole
// fetch, since the agent having answered at all means there is a genuine
// view to report even if one sub-read comes back empty.
func FetchQEMU(ctx context.Context, client QEMUClient, node string, vmid int) (View, error) {
	agentIfaces, err := client.GetGuestAgentInterfaces(ctx, node, vmid)
	if err != nil {
		return View{}, fmt.Errorf("%w: %w", ErrAgentUnreachable, err)
	}
	interfaces, addresses := FromAgentInterfaces(agentIfaces)

	routesRaw, _ := fetchExec(ctx, client, node, vmid, []string{"ip", "-j", "route", "show"})
	routes := ParseIPRouteJSON(routesRaw)

	resolvRaw, _ := fetchExec(ctx, client, node, vmid, []string{"cat", "/etc/resolv.conf"})
	dns := ParseResolvConf(string(resolvRaw))

	socketsRaw, _ := fetchExec(ctx, client, node, vmid, []string{"ss", "-H", "-tuln"})
	sockets := ParseSS(string(socketsRaw))

	reachable := false
	if gw, ok := defaultGateway(routes); ok {
		res := probe.Run(ctx, client, probe.Request{Node: node, VMID: vmid, DstIP: gw, Proto: probe.ProtoICMP})
		reachable = res.Outcome == probe.OutcomeReachable
	}

	return View{
		Interfaces: interfaces, Addresses: addresses, Routes: routes, DNS: dns,
		ListeningSockets: sockets, DefaultGatewayReachable: reachable, Source: SourceQemuGA,
	}, nil
}

// fetchExec execs cmd inside the guest via the QEMU guest agent and polls
// exec-status to a bounded deadline, returning stdout on a clean
// (exit-code 0) completion. Mirrors internal/probe.Run's own poll loop
// (that package's Run is not reused directly here since it classifies a
// probe-shaped result, not an arbitrary command's raw stdout).
func fetchExec(ctx context.Context, client probe.PVEExecer, node string, vmid int, cmd []string) ([]byte, error) {
	pid, err := client.AgentExec(ctx, node, vmid, cmd)
	if err != nil {
		return nil, fmt.Errorf("guestinterior: starting guest-agent exec %v: %w", cmd, err)
	}
	deadline := time.Now().Add(execPollTimeout)
	for {
		status, err := client.AgentExecStatus(ctx, node, vmid, pid)
		if err != nil {
			return nil, fmt.Errorf("guestinterior: polling guest-agent exec-status: %w", err)
		}
		if status.Exited {
			if status.ExitCode != 0 {
				return nil, fmt.Errorf("guestinterior: exec %v exited %d: %s", cmd, status.ExitCode, status.ErrData)
			}
			return []byte(status.OutData), nil
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("guestinterior: exec %v did not complete within %s", cmd, execPollTimeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(execPollInterval):
		}
	}
}
