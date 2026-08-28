// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/metrics"
	"github.com/bgovanlu/vnprox/internal/siemexport"
)

// setupSIEMExport builds T-4012's export sink and Exporter from cfg, or
// returns (nil, nil) when the section is disabled — the same "nil means
// the feature quietly doesn't exist" degradation every other optional
// subsystem in this file uses. selfMetrics drives the push-model
// vnprox_siemexport_events_total series (docs/features/monitoring.md §9's
// "SIEM export" subsection); it is nil-safe (a nil Registry — never
// actually constructed nil in server.go today, but this function does not
// assume that).
//
// cfg is already fully validated by internal/config.resolveSIEMExportConfig
// (format/network/address/path combinations, buffer_size, facility range)
// by the time Load returns it, so this function only has to dispatch on
// Format — there is no "half-configured" state left to detect here.
func setupSIEMExport(cfg config.SIEMExportConfig, selfMetrics *metrics.Registry, logger *slog.Logger) (*siemexport.Exporter, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	sink, err := buildSIEMExportSink(cfg)
	if err != nil {
		return nil, err
	}

	adapter := siemMetricsAdapter{registry: selfMetrics}
	exporter := siemexport.NewExporter(sink, cfg.BufferSize, adapter, logger)
	if selfMetrics != nil {
		exporter.SetOnSent(selfMetrics.ObserveSIEMExportSent)
	}
	return exporter, nil
}

func buildSIEMExportSink(cfg config.SIEMExportConfig) (siemexport.Sink, error) {
	switch cfg.Format {
	case "syslog":
		return siemexport.NewSyslogSink(cfg.Network, cfg.Address, cfg.Facility), nil
	case "jsonl":
		if cfg.Path != "" {
			return siemexport.NewJSONLFileSink(cfg.Path)
		}
		return siemexport.NewJSONLNetSink(cfg.Network, cfg.Address), nil
	default:
		// Unreachable: resolveSIEMExportConfig already refuses any other
		// value for an enabled section. Named explicitly rather than
		// panicking, in case that invariant is ever loosened without this
		// switch being revisited.
		return nil, fmt.Errorf("siemexport: unsupported format %q (config validation should have refused this)", cfg.Format)
	}
}

// siemMetricsAdapter adapts *metrics.Registry into siemexport.DropObserver
// — the decoupling conversion this composition root performs for every
// cross-package hook (the same pattern alertRuleProviderAdapter/
// webhookProviderAdapter use elsewhere in this file's siblings): neither
// internal/metrics nor internal/siemexport imports the other.
type siemMetricsAdapter struct {
	registry *metrics.Registry
}

func (a siemMetricsAdapter) SIEMExportDropped(reason siemexport.DropReason) {
	if a.registry == nil {
		return
	}
	a.registry.ObserveSIEMExportDropped(string(reason))
}

// siemFindingsTracker is findings.Config.OnCycle's SIEM-export consumer
// (docs/features/monitoring.md §12's "fed from the existing fan-in"
// paragraph): it diffs the full findings list OnCycle already hands it
// every cycle against its own previous-cycle snapshot and exports every
// finding that newly appears, changes severity, or resolves —
// independent of internal/findings.Engine's own Notifier transition
// tracking, which only fires above Config.NotifyThreshold (see
// internal/siemexport/doc.go's "Field mapping" section for why that
// threshold is the wrong gate for an audit/SIEM export).
//
// Safe for the one call pattern it is used under (OnCycle calls are
// serialized by findings.Engine's own run loop — see engine.go — so the
// mutex here is belt-and-braces, not load-bearing against concurrent
// observe calls).
//
// allocation path.
//
//nolint:govet // fieldalignment: one instance per daemon, never a hot
type siemFindingsTracker struct {
	exporter *siemexport.Exporter

	mu   sync.Mutex
	prev map[string]findings.Finding
}

func newSIEMFindingsTracker(exporter *siemexport.Exporter) *siemFindingsTracker {
	return &siemFindingsTracker{exporter: exporter, prev: map[string]findings.Finding{}}
}

// observe is registered as (one fan-out leg of) findings.Config.OnCycle.
// A nil tracker or nil exporter is a no-op — the same nil-safe-optional-
// dependency convention every other producer/consumer in this codebase
// follows, so a disabled [siemexport] section costs this call nothing
// beyond the branch itself.
func (s *siemFindingsTracker) observe(_ context.Context, fs []findings.Finding) {
	if s == nil || s.exporter == nil {
		return
	}

	cur := make(map[string]findings.Finding, len(fs))
	for _, f := range fs {
		cur[f.ID] = f
	}

	s.mu.Lock()
	prev := s.prev
	s.prev = cur
	s.mu.Unlock()

	for id, f := range cur {
		prevF, existed := prev[id]
		switch {
		case !existed:
			s.exporter.ExportFinding(siemFindingInput(f, siemexport.TransitionNew))
		case prevF.Severity != f.Severity:
			s.exporter.ExportFinding(siemFindingInput(f, siemexport.TransitionChanged))
		}
	}
	for id, f := range prev {
		if _, stillPresent := cur[id]; !stillPresent {
			s.exporter.ExportFinding(siemFindingInput(f, siemexport.TransitionResolved))
		}
	}
}

func siemFindingInput(f findings.Finding, transition string) siemexport.FindingInput {
	return siemexport.FindingInput{
		ID: f.ID, Source: string(f.Source), Check: f.Check, Severity: f.Severity,
		Detail: f.Detail, Nodes: f.Nodes, Refs: f.Refs, Transition: transition,
	}
}
