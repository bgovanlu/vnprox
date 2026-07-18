// Package capturemock is T-1301's scripted, hardware-free capture agent: it
// satisfies internal/capture.Agent by generating a deterministic sequence of
// real Ethernet frames into a classic-pcap file, honoring the session's
// server-enforced packet/byte caps (whichever is hit first stops the
// capture). It is the sibling of internal/pvemock — the fixture backing every
// capture test and `make dev` run, since there is no live Proxmox cluster to
// capture against.
//
// The frames it emits are the canonical corpus T-1302's in-browser decoder
// consumes (Ethernet/VLAN/ARP/IP/ICMP/TCP/UDP/DNS/DHCP — see frames.go); the
// same corpus is materialized to testdata/captures/ by GenerateCorpus.
package capturemock

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/capture"
)

// defaultBurstFrames is how many frames a scripted capture writes when no
// cap cuts it short — one per protocol in the corpus, so a decoder test
// against a default capture sees every protocol exactly once.
var defaultBurstFrames = len(CorpusOrder)

// Agent is the scripted capture agent (internal/capture.Agent).
type Agent struct {
	// Now sources packet timestamps; defaults to time.Now.
	Now func() time.Time
	// BurstFrames overrides how many frames an uncapped capture writes.
	BurstFrames int
}

// NewAgent returns a scripted Agent with defaults.
func NewAgent() *Agent { return &Agent{} }

var _ capture.Agent = (*Agent)(nil)

// Start writes a bounded burst of frames to spec.FilePath, stopping early if
// the session's packet or byte cap is reached (marking the capture
// completed), and returns a Process reporting the final accounting. The
// whole burst is written synchronously so the file exists and is complete by
// the time Start returns — appropriate for a deterministic mock (a real
// agent would stream over the capture's lifetime).
func (a *Agent) Start(_ context.Context, spec capture.Spec) (capture.Process, error) {
	now := a.Now
	if now == nil {
		now = time.Now
	}
	burst := a.BurstFrames
	if burst <= 0 {
		burst = defaultBurstFrames
	}

	f, err := os.Create(spec.FilePath)
	if err != nil {
		return nil, fmt.Errorf("capturemock: creating capture file %s: %w", spec.FilePath, err)
	}
	defer func() { _ = f.Close() }()
	bw := bufio.NewWriter(f)
	pw, err := newPcapWriter(bw)
	if err != nil {
		return nil, fmt.Errorf("capturemock: writing pcap header: %w", err)
	}

	start := now()
	var packets, bytes int64
	completedByCap := false
	for i := 0; i < burst; i++ {
		frame := buildFrame(CorpusOrder[i%len(CorpusOrder)])
		recSize := int64(16 + len(frame)) // pcap record header + frame

		// Server caps are enforced by the capture loop itself (docs/security.md):
		// stop before writing a frame that would exceed a cap.
		if spec.Caps.MaxPackets > 0 && packets >= spec.Caps.MaxPackets {
			completedByCap = true
			break
		}
		if spec.Caps.MaxBytes > 0 && pw.written+recSize > spec.Caps.MaxBytes {
			completedByCap = true
			break
		}

		ts := start.Add(time.Duration(i) * time.Millisecond)
		if _, err := pw.writePacket(uint32(ts.Unix()), uint32(ts.Nanosecond()/1000), frame); err != nil {
			return nil, fmt.Errorf("capturemock: writing packet: %w", err)
		}
		packets++
		bytes = pw.written
	}
	if err := bw.Flush(); err != nil {
		return nil, fmt.Errorf("capturemock: flushing capture file: %w", err)
	}

	status := capture.StatusRunning
	if completedByCap {
		status = capture.StatusCompleted
	}
	return &process{res: capture.Result{Packets: packets, Bytes: bytes, Status: status}}, nil
}

// process is the scripted capture's handle. Because Start writes the whole
// burst synchronously, the accounting is final by construction; Stop only
// flips a still-running capture to stopped.
type process struct {
	res capture.Result
	mu  sync.Mutex
}

func (p *process) Result() capture.Result {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.res
}

func (p *process) Stop(_ context.Context) capture.Result {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.res.Status == capture.StatusRunning {
		p.res.Status = capture.StatusStopped
	}
	return p.res
}
