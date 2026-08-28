// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/flow"
)

func discardLoggerHostSample() *slog.Logger {
	return slog.New(slog.NewTextHandler(&discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestSetupHostSample_BothDisabled_NoActors is T-1004's AC4: with neither
// [flows] conntrack_sampling_enabled nor ebpf_sampling_enabled set, no
// sampler goroutine is ever registered with the daemon's run group — the
// same "every goroutine has an owner" convention docs/development.md's Go
// standards documents, verified here at the run-group's own actor
// inventory (an empty actors slice means runGroup.run starts nothing for
// this subsystem).
func TestSetupHostSample_BothDisabled_NoActors(t *testing.T) {
	cfg := &config.Config{Flows: config.FlowsConfig{}}
	svc := flow.New(flow.Config{})

	active, actors := setupHostSample(cfg, svc, func() string { return "pve1" }, discardLoggerHostSample())
	if active != "" {
		t.Errorf("active sampler = %q, want \"\" when both are disabled", active)
	}
	if len(actors) != 0 {
		t.Errorf("got %d actors, want 0 when both samplers are disabled", len(actors))
	}
}

// TestSetupHostSample_ConntrackEnabled_RegistersOneActor covers the
// opt-in-enables-exactly-one-actor path: enabling conntrack sampling alone
// registers one actor and surfaces "conntrack" as the active sampler.
func TestSetupHostSample_ConntrackEnabled_RegistersOneActor(t *testing.T) {
	cfg := &config.Config{Flows: config.FlowsConfig{ConntrackSamplingEnabled: true, HostSampleIntervalSec: 10}}
	svc := flow.New(flow.Config{})

	active, actors := setupHostSample(cfg, svc, func() string { return "pve1" }, discardLoggerHostSample())
	if active != "conntrack" {
		t.Errorf("active sampler = %q, want %q", active, "conntrack")
	}
	if len(actors) != 1 {
		t.Fatalf("got %d actors, want 1", len(actors))
	}

	// The registered actor must behave like every other run-group actor:
	// return promptly once ctx is cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- actors[0](ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("actor returned %v, want nil after ctx cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("actor did not return after ctx cancellation")
	}
}

// TestSetupHostSample_EBPFEnabledAloneProbeFails_NoActiveSampler covers
// AC3's "falls back to ... fully disabled" branch: with only
// ebpf_sampling_enabled set and no live kernel support (this test's build
// has no -tags ebpf, so the probe always fails deterministically — see
// internal/flow/hostsample/ebpf_stub.go), no sampler is reported active,
// even though the (harmless, log-only) probe actor is still registered.
func TestSetupHostSample_EBPFEnabledAloneProbeFails_NoActiveSampler(t *testing.T) {
	cfg := &config.Config{Flows: config.FlowsConfig{EBPFSamplingEnabled: true}}
	svc := flow.New(flow.Config{})

	active, actors := setupHostSample(cfg, svc, func() string { return "pve1" }, discardLoggerHostSample())
	if active != "" {
		t.Errorf("active sampler = %q, want \"\" (eBPF probe fails in this build)", active)
	}
	if len(actors) != 1 {
		t.Fatalf("got %d actors, want 1 (the probe/fallback actor)", len(actors))
	}

	ctx := context.Background()
	if err := actors[0](ctx); err != nil {
		t.Errorf("eBPF actor returned %v, want nil (probe failure must never be fatal)", err)
	}
}
