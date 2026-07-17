package probe

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// fakeExecClient is a scripted PVEExecer test double: AgentExec either
// fails once (execErr) or returns a fixed pid; AgentExecStatus replays
// statuses in order, holding on the last entry for any further poll (so a
// single {Exited:false} entry models "never completes", exercising Run's
// own timeout deadline without an unbounded loop).
type fakeExecClient struct {
	execErr   error
	statusErr error
	statuses  []pve.ExecResult
	pid       int
	calls     int
	execCalls int
}

func (f *fakeExecClient) AgentExec(context.Context, string, int, []string) (int, error) {
	f.execCalls++
	if f.execErr != nil {
		return 0, f.execErr
	}
	return f.pid, nil
}

func (f *fakeExecClient) AgentExecStatus(context.Context, string, int, int) (pve.ExecResult, error) {
	if f.statusErr != nil {
		return pve.ExecResult{}, f.statusErr
	}
	idx := f.calls
	if idx >= len(f.statuses) {
		idx = len(f.statuses) - 1
	}
	f.calls++
	return f.statuses[idx], nil
}

// TestRun_OutcomeClassification is AC1: table-driven coverage of outcome
// classification for ICMP reachable/unreachable/timeout and TCP
// open/refused/timeout, against a fake AgentExec/AgentExecStatus.
func TestRun_OutcomeClassification(t *testing.T) {
	tests := []struct {
		name    string
		proto   string
		client  *fakeExecClient
		want    Outcome
		timeout time.Duration
	}{
		{
			name:  "icmp reachable",
			proto: ProtoICMP,
			client: &fakeExecClient{statuses: []pve.ExecResult{
				{Exited: true, ExitCode: 0},
			}},
			want: OutcomeReachable,
		},
		{
			name:  "icmp unreachable (no reply)",
			proto: ProtoICMP,
			client: &fakeExecClient{statuses: []pve.ExecResult{
				{Exited: true, ExitCode: 1, OutData: "100% packet loss"},
			}},
			want: OutcomeUnreachable,
		},
		{
			name:  "icmp timeout (agent never reports exited)",
			proto: ProtoICMP,
			client: &fakeExecClient{statuses: []pve.ExecResult{
				{Exited: false},
			}},
			want:    OutcomeTimeout,
			timeout: 250 * time.Millisecond,
		},
		{
			name:  "icmp error (unexpected exit code)",
			proto: ProtoICMP,
			client: &fakeExecClient{statuses: []pve.ExecResult{
				{Exited: true, ExitCode: 2, ErrData: "unknown host"},
			}},
			want: OutcomeError,
		},
		{
			name:   "icmp error (exec transport failure)",
			proto:  ProtoICMP,
			client: &fakeExecClient{execErr: errors.New("agent unreachable")},
			want:   OutcomeError,
		},
		{
			name:  "tcp open",
			proto: ProtoTCP,
			client: &fakeExecClient{statuses: []pve.ExecResult{
				{Exited: true, ExitCode: 0},
			}},
			want: OutcomeReachable,
		},
		{
			name:  "tcp refused",
			proto: ProtoTCP,
			client: &fakeExecClient{statuses: []pve.ExecResult{
				{Exited: true, ExitCode: 1, ErrData: "nc: connect to 10.0.0.5 port 9999 (tcp) failed: Connection refused"},
			}},
			want: OutcomeUnreachable,
		},
		{
			name:  "tcp timeout",
			proto: ProtoTCP,
			client: &fakeExecClient{statuses: []pve.ExecResult{
				{Exited: false},
			}},
			want:    OutcomeTimeout,
			timeout: 250 * time.Millisecond,
		},
		{
			name:  "tcp error (poll transport failure)",
			proto: ProtoTCP,
			client: &fakeExecClient{
				statuses:  []pve.ExecResult{{Exited: false}},
				statusErr: errors.New("connection reset"),
			},
			want: OutcomeError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{Node: "pve1", VMID: 300, Proto: tc.proto, DstIP: "10.0.0.5", Port: 22, Timeout: tc.timeout}
			got := Run(context.Background(), tc.client, req)
			if got.Outcome != tc.want {
				t.Fatalf("Run() outcome = %q, want %q (result: %+v)", got.Outcome, tc.want, got)
			}
			if got.Outcome == OutcomeError && got.ExecError == "" {
				t.Errorf("OutcomeError result must carry ExecError, got %+v", got)
			}
		})
	}
}

func TestRun_UnsupportedProtocol(t *testing.T) {
	client := &fakeExecClient{}
	got := Run(context.Background(), client, Request{Node: "pve1", VMID: 300, Proto: "udp", DstIP: "10.0.0.5"})
	if got.Outcome != OutcomeError {
		t.Fatalf("outcome = %q, want error", got.Outcome)
	}
	if got.ExecError == "" {
		t.Error("expected ExecError to explain the unsupported protocol")
	}
	if client.execCalls != 0 {
		t.Errorf("AgentExec was called %d times, want 0 (unsupported protocol must fail before any exec attempt)", client.execCalls)
	}
}

func TestBuildCommand_UnsupportedProtocol(t *testing.T) {
	if _, ok := buildCommand("udp", "10.0.0.5", 53, time.Second); ok {
		t.Error("buildCommand(udp, ...) = ok, want unsupported (probe engine is tcp/icmp only)")
	}
}

func TestBuildCommand_EmbedsTargetForMockParsing(t *testing.T) {
	// pvemock's handleGuestAgentExec parses exactly these two shapes (see
	// command.go's doc comment) — this test pins the contract so a future
	// change to buildCommand's argv shape is caught here, not as a mystery
	// pvemock test failure.
	icmp, ok := buildCommand(ProtoICMP, "10.0.0.5", 0, 5*time.Second)
	if !ok || icmp[0] != "ping" || icmp[len(icmp)-1] != "10.0.0.5" {
		t.Errorf("icmp command = %v, want ping argv ending in the dst IP", icmp)
	}
	tcp, ok := buildCommand(ProtoTCP, "10.0.0.5", 443, 5*time.Second)
	if !ok || tcp[0] != "nc" || tcp[len(tcp)-2] != "10.0.0.5" || tcp[len(tcp)-1] != "443" {
		t.Errorf("tcp command = %v, want nc argv ending in <dst-ip> <port>", tcp)
	}
}
