// SPDX-License-Identifier: Apache-2.0

package hostsample

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/bgovanlu/vnprox/internal/flow"
)

// TestEBPFSampler_ProbeFailsCleanly covers AC3's mandatory negative path:
// the eBPF probe fails on the CI kernel (this test's build has no -tags
// ebpf, so ebpf_stub.go's EBPFSampler is the one compiled in, whose Probe
// always fails "not compiled into this binary" — a deterministic,
// portable, CI-safe stand-in for "no CAP_BPF/CAP_PERFMON" that needs no
// real kernel probing at all) and is wrapped in ErrEBPFUnavailable.
func TestEBPFSampler_ProbeFailsCleanly(t *testing.T) {
	s := NewEBPFSampler("pve1")
	err := s.Probe()
	if err == nil {
		t.Fatal("Probe() = nil, want a non-nil error (this build has no ebpf tag)")
	}
	if !errors.Is(err, ErrEBPFUnavailable) {
		t.Errorf("Probe() error = %v, want it to wrap ErrEBPFUnavailable", err)
	}
}

// TestEBPFSampler_RunLogsWarningAndFallsBackCleanly asserts AC3 in full:
// a failed probe logs a structured slog warning (parseable, naming the
// capability/feature reason) and Run returns nil — never fatal — so the
// daemon continues on conntrack-only (or fully disabled) sampling.
func TestEBPFSampler_RunLogsWarningAndFallsBackCleanly(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	s := NewEBPFSampler("pve1")
	s.Logger = logger

	ingestCalled := false
	err := s.Run(context.Background(), func(_ context.Context, _ []flow.Record) { ingestCalled = true })
	if err != nil {
		t.Fatalf("Run() = %v, want nil (probe failure must never be fatal)", err)
	}
	if ingestCalled {
		t.Fatal("Run invoked ingest despite a failed probe — no eBPF-sourced records should ever be produced by this build")
	}

	out := buf.String()
	if !bytes.Contains([]byte(out), []byte("level=WARN")) {
		t.Errorf("expected a WARN-level log line, got: %s", out)
	}
	if !bytes.Contains([]byte(out), []byte("eBPF")) {
		t.Errorf("expected the log line to name eBPF, got: %s", out)
	}
	if !bytes.Contains([]byte(out), []byte("conntrack")) {
		t.Errorf("expected the log line to name the conntrack-only fallback, got: %s", out)
	}
}
