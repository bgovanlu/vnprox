// SPDX-License-Identifier: Apache-2.0

package guestinterior

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// fakeQEMUClient is a scripted QEMUClient test double keyed by the exact
// argv AgentExec receives (mirroring internal/pvemock's own
// parseProbeCommand/parseInteriorCommand argv-matching convention), so one
// fake can answer FetchQEMU's several different exec calls (routes/
// resolv.conf/sockets/gateway ping) distinctly.
type fakeQEMUClient struct {
	ifacesErr   error
	outcomes    map[string]pve.ExecResult
	pidCommands map[int]string
	ifaces      []pve.AgentIface
	nextPID     int
}

func newFakeQEMUClient() *fakeQEMUClient {
	return &fakeQEMUClient{outcomes: map[string]pve.ExecResult{}, pidCommands: map[int]string{}}
}

func (f *fakeQEMUClient) GetGuestAgentInterfaces(context.Context, string, int) ([]pve.AgentIface, error) {
	return f.ifaces, f.ifacesErr
}

func (f *fakeQEMUClient) AgentExec(_ context.Context, _ string, _ int, command []string) (int, error) {
	f.nextPID++
	f.pidCommands[f.nextPID] = strings.Join(command, " ")
	return f.nextPID, nil
}

func (f *fakeQEMUClient) AgentExecStatus(_ context.Context, _ string, _ int, pid int) (pve.ExecResult, error) {
	cmd := f.pidCommands[pid]
	if res, ok := f.outcomes[cmd]; ok {
		return res, nil
	}
	// Unscripted command: exit 0 with no output, so a test that doesn't
	// care about (e.g.) the gateway ping doesn't need to script it too.
	return pve.ExecResult{Exited: true, ExitCode: 0}, nil
}

const (
	cmdRoutes  = "ip -j route show"
	cmdResolv  = "cat /etc/resolv.conf"
	cmdSockets = "ss -H -tuln"
)

func pingCmd(secs, ip string) string { return "ping -c 1 -W " + secs + " " + ip }

// TestFetchQEMU_AgentUnreachable is AC1's negative case: an agent that
// can't even answer network-get-interfaces reports ErrAgentUnreachable,
// not a silent empty view.
func TestFetchQEMU_AgentUnreachable(t *testing.T) {
	client := newFakeQEMUClient()
	client.ifacesErr = errors.New("agent not reachable")
	_, err := FetchQEMU(context.Background(), client, "pve1", 200)
	if !errors.Is(err, ErrAgentUnreachable) {
		t.Fatalf("FetchQEMU() error = %v, want wrapping ErrAgentUnreachable", err)
	}
}

// TestFetchQEMU_RepresentativeResponse is AC1: a representative QEMU-GA
// response set covering interfaces/routes/DNS/sockets (plus gateway
// reachability), against a fake AgentExec/AgentExecStatus.
func TestFetchQEMU_RepresentativeResponse(t *testing.T) {
	client := newFakeQEMUClient()
	client.ifaces = []pve.AgentIface{
		{Name: "eth0", HardwareAddr: "bc:24:11:aa:00:c8", IPAddresses: []pve.AgentIPAddress{
			{IPAddress: "10.10.0.200", IPAddressType: "ipv4", Prefix: 24},
		}},
	}
	client.outcomes[cmdRoutes] = pve.ExecResult{Exited: true, ExitCode: 0,
		OutData: `[{"dst":"default","gateway":"10.10.0.1","dev":"eth0"},{"dst":"10.10.0.0/24","dev":"eth0"}]`}
	client.outcomes[cmdResolv] = pve.ExecResult{Exited: true, ExitCode: 0, OutData: "nameserver 1.1.1.1\n"}
	client.outcomes[cmdSockets] = pve.ExecResult{Exited: true, ExitCode: 0,
		OutData: "tcp   LISTEN 0      128          0.0.0.0:22         0.0.0.0:*\n"}
	client.outcomes[pingCmd("5", "10.10.0.1")] = pve.ExecResult{Exited: true, ExitCode: 0}

	view, err := FetchQEMU(context.Background(), client, "pve1", 200)
	if err != nil {
		t.Fatalf("FetchQEMU: %v", err)
	}
	if view.Source != SourceQemuGA {
		t.Errorf("Source = %q, want %q", view.Source, SourceQemuGA)
	}
	if len(view.Interfaces) != 1 || view.Interfaces[0].Name != "eth0" {
		t.Errorf("Interfaces = %+v, want exactly eth0", view.Interfaces)
	}
	if len(view.Addresses) != 1 || view.Addresses[0].IP != "10.10.0.200" {
		t.Errorf("Addresses = %+v, want 10.10.0.200", view.Addresses)
	}
	if len(view.Routes) != 2 {
		t.Errorf("Routes = %+v, want 2 entries", view.Routes)
	}
	if len(view.DNS.Nameservers) != 1 || view.DNS.Nameservers[0] != "1.1.1.1" {
		t.Errorf("DNS = %+v, want nameserver 1.1.1.1", view.DNS)
	}
	if len(view.ListeningSockets) != 1 || view.ListeningSockets[0].LocalPort != 22 {
		t.Errorf("ListeningSockets = %+v, want one entry on port 22", view.ListeningSockets)
	}
	if !view.DefaultGatewayReachable {
		t.Errorf("DefaultGatewayReachable = false, want true (scripted ping success)")
	}
}

// TestFetchQEMU_GatewayUnreachable covers the negative reachability
// branch: a scripted ping failure (exit 1, "no reply") reports
// DefaultGatewayReachable false, not an error — a fetch failure on one
// sub-read must not fail the whole view.
func TestFetchQEMU_GatewayUnreachable(t *testing.T) {
	client := newFakeQEMUClient()
	client.ifaces = []pve.AgentIface{{Name: "eth0"}}
	client.outcomes[cmdRoutes] = pve.ExecResult{Exited: true, ExitCode: 0,
		OutData: `[{"dst":"default","gateway":"10.10.0.1","dev":"eth0"}]`}
	client.outcomes[pingCmd("5", "10.10.0.1")] = pve.ExecResult{Exited: true, ExitCode: 1, OutData: "100% packet loss"}

	view, err := FetchQEMU(context.Background(), client, "pve1", 200)
	if err != nil {
		t.Fatalf("FetchQEMU: %v", err)
	}
	if view.DefaultGatewayReachable {
		t.Errorf("DefaultGatewayReachable = true, want false (scripted no-reply)")
	}
}

// TestFetchQEMU_SubReadFailureDegradesGracefully: routes/DNS/sockets each
// erroring individually still yields a complete (if partly empty) view,
// not a hard failure — since GetGuestAgentInterfaces answered, there is a
// genuine view to report.
func TestFetchQEMU_SubReadFailureDegradesGracefully(t *testing.T) {
	client := newFakeQEMUClient()
	client.ifaces = []pve.AgentIface{{Name: "eth0"}}
	client.outcomes[cmdRoutes] = pve.ExecResult{Exited: true, ExitCode: 1, ErrData: "ip: command not found"}
	client.outcomes[cmdResolv] = pve.ExecResult{Exited: true, ExitCode: 1, ErrData: "no such file"}
	client.outcomes[cmdSockets] = pve.ExecResult{Exited: true, ExitCode: 1, ErrData: "ss: command not found"}

	view, err := FetchQEMU(context.Background(), client, "pve1", 200)
	if err != nil {
		t.Fatalf("FetchQEMU: %v", err)
	}
	if len(view.Routes) != 0 || len(view.DNS.Nameservers) != 0 || len(view.ListeningSockets) != 0 {
		t.Errorf("view = %+v, want empty routes/dns/sockets on sub-read failure", view)
	}
	if view.DefaultGatewayReachable {
		t.Errorf("DefaultGatewayReachable = true, want false (no route data at all)")
	}
}
