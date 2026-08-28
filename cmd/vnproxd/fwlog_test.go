// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/fwlog"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestLoadFwLogFixtures_SeedsPerNodeContent covers T-505's dev wiring: every
// "<node>.log" file in a fixture directory seeds that node's content in the
// resulting MemorySource, keyed by filename (extension stripped).
func TestLoadFwLogFixtures_SeedsPerNodeContent(t *testing.T) {
	mem := fwlog.NewMemorySource()
	if err := loadFwLogFixtures(mem, "../../testdata/firewall-logs"); err != nil {
		t.Fatalf("loadFwLogFixtures: %v", err)
	}

	for _, node := range []string{"pve1", "pve2"} {
		lines, _, _, err := mem.Tail(context.Background(), node, "", 100)
		if err != nil {
			t.Fatalf("Tail(%s): %v", node, err)
		}
		if len(lines) == 0 {
			t.Errorf("node %s: no lines loaded from its fixture file", node)
		}
	}
}

// TestLoadFwLogFixtures_MissingDirErrors covers the error path setupFwlog
// treats as non-fatal-but-logged (server.go: fwlogErr disables the
// feature rather than aborting daemon startup).
func TestLoadFwLogFixtures_MissingDirErrors(t *testing.T) {
	mem := fwlog.NewMemorySource()
	if err := loadFwLogFixtures(mem, "does-not-exist-anywhere"); err == nil {
		t.Fatal("expected an error for a nonexistent fixture directory")
	}
}

// TestSetupFwlog_DevFixtureMode covers setupFwlog's dev-fixture branch
// end-to-end: given a config pointing at the real testdata corpus, the
// resulting Service actually serves parsed, correlated entries once
// ticked, and the returned Source round-trips through
// fwLogPeerReaderAdapter into peer.FirewallLogReader's shape (the same
// adapter server.go wires into peer.ServerOptions.FirewallLog).
func TestSetupFwlog_DevFixtureMode(t *testing.T) {
	cfg := &config.Config{FirewallLog: config.FirewallLogConfig{DevFixtureDir: "../../testdata/firewall-logs"}}
	graph := inventory.NewGraph()

	svc, source, err := setupFwlog(cfg, graph, nil, nil, func() string { return "pve1" }, testLogger())
	if err != nil {
		t.Fatalf("setupFwlog: %v", err)
	}
	if svc == nil || source == nil {
		t.Fatal("setupFwlog returned a nil Service/Source on the success path")
	}

	svc.Tick(context.Background())
	page := svc.TailPage(fwlog.Filter{}, 0)
	if len(page.Items) == 0 {
		t.Fatal("expected at least one parsed entry from the fixture corpus after one Tick")
	}

	adapter := fwLogPeerReaderAdapter{src: source}
	lines, _, err := adapter.FirewallLogTail(context.Background(), "pve1", "", 10)
	if err != nil {
		t.Fatalf("fwLogPeerReaderAdapter.FirewallLogTail: %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("peer-reader adapter returned no lines for pve1")
	}
}

// TestSetupFwlog_ProductionModeDefaultsToRealFilePath covers the non-dev
// branch: an empty DevFixtureDir wires a *fwlog.FileSource at the
// configured (or default) path, never a MemorySource.
func TestSetupFwlog_ProductionModeDefaultsToRealFilePath(t *testing.T) {
	cfg := &config.Config{FirewallLog: config.FirewallLogConfig{Path: config.DefaultFirewallLogPath}}
	graph := inventory.NewGraph()

	svc, source, err := setupFwlog(cfg, graph, nil, nil, func() string { return "pve1" }, testLogger())
	if err != nil {
		t.Fatalf("setupFwlog: %v", err)
	}
	if svc == nil {
		t.Fatal("setupFwlog returned a nil Service on the success path")
	}
	fileSource, ok := source.(*fwlog.FileSource)
	if !ok {
		t.Fatalf("source = %T, want *fwlog.FileSource in production mode", source)
	}
	if fileSource.Path != config.DefaultFirewallLogPath {
		t.Errorf("FileSource.Path = %q, want %q", fileSource.Path, config.DefaultFirewallLogPath)
	}
}
