package probe

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// Proto names the two live-probe protocols this package supports — a
// narrower set than internal/sim's proto vocabulary (tcp|udp|icmp|any),
// since a live probe needs one concrete, executable action, not a static
// rule match. UDP is deliberately out of scope: a UDP "probe" has no
// reliable reachability signal without an application-layer response, so
// this package does not claim to answer that question rather than
// returning a misleading result.
const (
	ProtoICMP = "icmp"
	ProtoTCP  = "tcp"
)

// Outcome classifies one probe attempt's observed result (docs/api.md's
// POST /simulate/verify `observed.outcome`).
type Outcome string

const (
	// OutcomeReachable: the probe succeeded (ICMP echo replied, or the TCP
	// handshake completed).
	OutcomeReachable Outcome = "reachable"
	// OutcomeUnreachable: the probe ran to completion but failed to reach
	// the destination (no ICMP reply, TCP connection refused/reset).
	OutcomeUnreachable Outcome = "unreachable"
	// OutcomeTimeout: the probe did not complete within the bounded
	// deadline (Request.Timeout) — distinct from OutcomeUnreachable because
	// a silent timeout (packet dropped somewhere, no response at all) and
	// an active refusal are different signals worth keeping apart.
	OutcomeTimeout Outcome = "timeout"
	// OutcomeError: the probe itself could not be attempted or could not
	// be classified (guest agent unreachable, exec transport failure,
	// unsupported protocol, unparseable result) — the honesty-contract
	// "no claim" bucket: never conflated with OutcomeUnreachable, which is
	// a genuine negative result.
	OutcomeError Outcome = "error"
)

// PVEExecer is the subset of *pve.Client this package needs: qemu
// guest-agent exec + poll (mirrors internal/ipam.PVEReader's "small
// interface, real type satisfies it directly" seam over the same client
// package). *pve.Client satisfies this directly.
type PVEExecer interface {
	AgentExec(ctx context.Context, node string, vmid int, command []string) (int, error)
	AgentExecStatus(ctx context.Context, node string, vmid int, pid int) (pve.ExecResult, error)
}

// DefaultTimeout bounds how long Run polls exec-status before giving up and
// reporting OutcomeTimeout, when Request.Timeout is zero.
const DefaultTimeout = 5 * time.Second

// pollInterval is the wait between exec-status polls.
const pollInterval = 200 * time.Millisecond

// Request describes one live probe attempt: run Proto from the qemu guest
// at (Node,VMID) toward DstIP:Port.
type Request struct {
	Node    string
	DstIP   string
	Proto   string // ProtoICMP | ProtoTCP
	VMID    int
	Port    int           // required (>0) for ProtoTCP; ignored for ProtoICMP
	Timeout time.Duration // zero uses DefaultTimeout
}

// Result is one probe attempt's classified outcome.
type Result struct {
	Outcome   Outcome
	Detail    string // human-readable context, always safe to surface
	ExecError string // set (non-empty) iff Outcome == OutcomeError and the
	// failure came from the exec attempt itself (agent unreachable,
	// transport error, unsupported protocol) rather than a classification
	// ambiguity — docs/api.md's `observed.execError`.
}

// Run execs req's probe command inside the source guest via the QEMU guest
// agent, polls exec-status to a bounded deadline, and classifies the
// outcome. It never returns a Go error: every failure mode (unsupported
// protocol, exec transport failure, poll timeout, cancelled context) is
// represented in the returned Result so callers (internal/api's
// POST /simulate/verify) always have a complete, audit-worthy answer —
// docs/features/firewall.md §5/§6's honesty contract applied to a live
// probe: "the attempt itself is the answer" even when the attempt failed.
func Run(ctx context.Context, client PVEExecer, req Request) Result {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	cmd, ok := buildCommand(req.Proto, req.DstIP, req.Port, timeout)
	if !ok {
		return Result{Outcome: OutcomeError, ExecError: fmt.Sprintf("probe: unsupported protocol %q (supported: %s, %s)", req.Proto, ProtoICMP, ProtoTCP)}
	}

	pid, err := client.AgentExec(ctx, req.Node, req.VMID, cmd)
	if err != nil {
		return Result{Outcome: OutcomeError, ExecError: fmt.Sprintf("probe: starting guest-agent exec: %v", err)}
	}

	deadline := time.Now().Add(timeout)
	for {
		status, err := client.AgentExecStatus(ctx, req.Node, req.VMID, pid)
		if err != nil {
			return Result{Outcome: OutcomeError, ExecError: fmt.Sprintf("probe: polling guest-agent exec-status: %v", err)}
		}
		if status.Exited {
			return classify(req.Proto, status)
		}
		if !time.Now().Before(deadline) {
			return Result{Outcome: OutcomeTimeout, Detail: fmt.Sprintf("probe did not complete within %s", timeout)}
		}
		select {
		case <-ctx.Done():
			return Result{Outcome: OutcomeError, ExecError: fmt.Sprintf("probe: %v", ctx.Err())}
		case <-time.After(pollInterval):
		}
	}
}

// classify interprets one completed (Exited == true) exec-status result per
// req's protocol. Only Run calls this — a still-running exec never reaches
// here (Run's own deadline handles that as OutcomeTimeout).
func classify(proto string, status pve.ExecResult) Result {
	switch proto {
	case ProtoICMP:
		switch status.ExitCode {
		case 0:
			return Result{Outcome: OutcomeReachable, Detail: "ping: 1 packet transmitted, 1 received"}
		case 1:
			return Result{Outcome: OutcomeUnreachable, Detail: firstNonEmpty(status.OutData, "ping: 100% packet loss, no reply")}
		default:
			return Result{Outcome: OutcomeError, ExecError: fmt.Sprintf("probe: ping exited %d: %s", status.ExitCode, firstNonEmpty(status.ErrData, status.OutData))}
		}
	case ProtoTCP:
		switch status.ExitCode {
		case 0:
			return Result{Outcome: OutcomeReachable, Detail: "tcp connect succeeded"}
		case 1:
			combined := strings.ToLower(status.OutData + " " + status.ErrData)
			if strings.Contains(combined, "refused") {
				return Result{Outcome: OutcomeUnreachable, Detail: "tcp connection refused"}
			}
			return Result{Outcome: OutcomeUnreachable, Detail: firstNonEmpty(status.ErrData, status.OutData)}
		default:
			return Result{Outcome: OutcomeError, ExecError: fmt.Sprintf("probe: tcp connect attempt exited %d: %s", status.ExitCode, firstNonEmpty(status.ErrData, status.OutData))}
		}
	default:
		// Unreachable given Run's own buildCommand guard, but classify is
		// exported-adjacent enough (unit-tested directly) to answer
		// honestly rather than panic on an unexpected proto.
		return Result{Outcome: OutcomeError, ExecError: fmt.Sprintf("probe: cannot classify result for unsupported protocol %q", proto)}
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
