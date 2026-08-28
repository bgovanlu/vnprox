// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/fwlog"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// fwlogSnapshotAdapter adapts *inventory.Graph to fwlog.SnapshotSource,
// the exact same "live graph -> pure fw.Snapshot" conversion
// internal/api/firewall.go's firewallSnapshot helper performs for GET
// /firewall/rulesets — duplicated here (rather than exported from
// internal/api, which would create an internal/fwlog -> internal/api
// dependency in the wrong direction) since it is one line.
type fwlogSnapshotAdapter struct {
	graph *inventory.Graph
}

func (a fwlogSnapshotAdapter) FirewallSnapshot() fw.Snapshot {
	return fw.BuildSnapshot(a.graph.Snapshot().All())
}

// fwLogPeerReaderAdapter adapts a local fwlog.Source (this daemon's own
// log source) to peer.FirewallLogReader — the peer-server-side seam a
// remote peer's Service.Tick calls into via GET /api/peer/firewall/log.
// It drops Source.Tail's `reset` return value: the peer route's caller
// (another node's Service, whose own tailBytes-equivalent cursor
// bookkeeping already treats an out-of-range/malformed cursor as "restart
// from 0" transparently) doesn't need it surfaced separately, unlike a
// local Tick call which could in principle log it.
type fwLogPeerReaderAdapter struct {
	src fwlog.Source
}

func (a fwLogPeerReaderAdapter) FirewallLogTail(ctx context.Context, node, cursor string, maxLines int) ([]string, string, error) {
	lines, next, _, err := a.src.Tail(ctx, node, cursor, maxLines)
	return lines, next, err
}

// setupFwlog builds T-505's *fwlog.Service: the local log source (a real
// file, or — in dev mode — a static fixture corpus, see
// loadFwLogFixtures), cluster fan-out via peerClient (nil-safe: a
// single-node deployment simply never fans out), rule correlation against
// graph's live firewall data, and the `firewall.log.batch` WS push over
// ws. Returns the local Source too, so callers can wire the same instance
// into peer.ServerOptions.FirewallLog (fwLogPeerReaderAdapter) — this
// daemon must be able to serve its own log to peers using the exact same
// source its own Service polls, not a second one.
func setupFwlog(cfg *config.Config, graph *inventory.Graph, ws fwlog.Broadcaster, peerClient *peer.Client, localNode func() string, logger *slog.Logger) (*fwlog.Service, fwlog.Source, error) {
	var source fwlog.Source
	if cfg.FirewallLog.DevFixtureDir != "" {
		mem := fwlog.NewMemorySource()
		if err := loadFwLogFixtures(mem, cfg.FirewallLog.DevFixtureDir); err != nil {
			return nil, nil, fmt.Errorf("loading firewall log dev fixtures: %w", err)
		}
		logger.Warn("fwlog: DEV MODE log source — serving a static fixture corpus, not a real file", "dir", cfg.FirewallLog.DevFixtureDir)
		source = mem
	} else {
		source = &fwlog.FileSource{Path: cfg.FirewallLog.Path}
	}

	var peerSource fwlog.PeerSource
	if peerClient != nil {
		peerSource = peerClient
	}

	svc := fwlog.New(fwlog.Config{
		Local:     source,
		LocalNode: localNode,
		Peers:     peerSource,
		Snapshot:  fwlogSnapshotAdapter{graph: graph},
		WS:        ws,
		Logger:    logger,
	})
	return svc, source, nil
}

// loadFwLogFixtures seeds mem from every "<node>.log" file directly under
// dir (T-505's fixture corpus — see testdata/firewall-logs/ and
// docs/development.md's mock-PVE-server convention: pve-firewall's log is
// a node-local file, not part of the PVE HTTP API internal/pvemock
// otherwise imitates, so it gets its own small flat-file fixture mechanism
// rather than another field in the YAML cluster fixture schema).
func loadFwLogFixtures(mem *fwlog.MemorySource, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading firewall log fixture dir %s: %w", dir, err)
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			return fmt.Errorf("reading firewall log fixture %s: %w", ent.Name(), err)
		}
		node := strings.TrimSuffix(ent.Name(), ".log")
		mem.Seed(node, string(data))
	}
	return nil
}
