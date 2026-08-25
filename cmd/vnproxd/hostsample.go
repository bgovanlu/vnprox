package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/flow/hostsample"
)

// setupHostSample wires T-1004's two host-local flow samplers
// (internal/flow/hostsample) into flowSvc — the exact same *flow.Service
// T-1002's UDP listeners feed via setupFlows (flows.go), so every emitted
// flow.Record lands in the one flow_samples ring regardless of which
// sampler (or protocol listener) produced it: GET /flows, the flow
// explorer, and map flow painting need no awareness of the source.
//
// Both samplers are strictly opt-in per node ([flows]
// conntrack_sampling_enabled / ebpf_sampling_enabled in vnprox.toml, both
// false by default) — with neither set, this returns a nil activeSampler
// and an empty actors slice, so no sampler goroutine is ever started
// (matching T-1004's AC4 and the run group's "every goroutine has an
// owner" convention, docs/development.md's Go standards).
//
// activeSampler is what GET /config's Settings payload surfaces
// (api.InstanceInfo.HostSampler): "" (none), "conntrack", or "ebpf" — the
// eBPF prober runs once at startup here (not per-request) to decide which
// name to report, since a probe failure permanently falls back to
// conntrack-only (or fully disabled) for this daemon's lifetime, matching
// InstanceInfo's existing "captured once at daemon start" contract.
func setupHostSample(cfg *config.Config, flowSvc *flow.Service, localNode func() string, logger *slog.Logger) (activeSampler string, actors []func(context.Context) error) {
	interval := time.Duration(cfg.Flows.HostSampleIntervalSec) * time.Second
	if interval <= 0 {
		interval = hostsample.DefaultHostSampleInterval
	}

	ingest := func(ctx context.Context, records []flow.Record) {
		node := localNode()
		for i := range records {
			records[i].Node = node
		}
		flowSvc.Ingest(ctx, records)
	}

	// eBPF is checked (and, if viable, would take priority as the
	// higher-fidelity source) before conntrack — but see
	// internal/flow/hostsample's package doc comment: actual per-packet
	// BPF attachment is not implemented in this codebase yet, so
	// EBPFSampler.Run always falls back to logging its probe result and
	// returning nil, regardless of build tag. It is still registered as
	// an actor whenever enabled, both to exercise/log the probe on every
	// startup and so a future attachment implementation only needs to
	// change EBPFSampler.Run's body, not this wiring.
	if cfg.Flows.EBPFSamplingEnabled {
		sampler := hostsample.NewEBPFSampler(localNode())
		sampler.Logger = logger
		if err := sampler.Probe(); err != nil {
			logger.Warn("hostsample: eBPF sampling was enabled but the kernel-feature probe failed; falling back to conntrack-only sampling", "error", err)
		} else {
			activeSampler = "ebpf"
		}
		actors = append(actors, func(ctx context.Context) error {
			return sampler.Run(ctx, ingest)
		})
	}

	if cfg.Flows.ConntrackSamplingEnabled {
		if activeSampler == "" {
			activeSampler = "conntrack"
		}
		sampler := hostsample.NewConntrackSampler(hostsample.NewNetlinkConntrackReader(), localNode())
		sampler.Logger = logger
		actors = append(actors, func(ctx context.Context) error {
			return sampler.Run(ctx, interval, ingest)
		})
	}

	return activeSampler, actors
}
