// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/store"
)

// flowFixtureRecord is one seeded flow.Record, expressed relative to daemon
// start time rather than as an absolute timestamp. T-1602/T-1603's
// microsegmentation planner learns and dry-runs against windows anchored on
// time.Now() (microsegwire.go's (*microsegAdapter).windows), so a corpus
// baked at a fixed wall-clock time would silently age out of the planner's
// training/held-out windows the moment the daemon has been running longer
// than those windows are wide. DaysAgo/OffsetSec are resolved against `now`
// once, at load time (T-3706).
type flowFixtureRecord struct {
	// GuestSide names which endpoint carries the fixture file's `guest`
	// ref — "src" or "dst". The other endpoint is an ordinary peer IP,
	// never ref-resolved: internal/flow.GraphResolver is deliberately
	// guest-nic-blind (its own doc comment), so a seeded corpus must say
	// which side is the guest directly rather than relying on IP
	// resolution to ever produce a guest ref.
	GuestSide string `json:"guestSide"`
	SrcIP     string `json:"srcIp"`
	DstIP     string `json:"dstIp"`
	Bytes     int64  `json:"bytes"`
	Packets   int64  `json:"packets"`
	// DaysAgo is fractional days before `now` this record was "observed".
	// Ordered after the string fields: govet fieldalignment wants the
	// pointer-bearing fields first.
	DaysAgo float64 `json:"daysAgo"`
	// OffsetSec nudges same-day records apart so no two share a unix
	// second (a flow_samples row has no natural intra-second dedup key —
	// see store.FlowSampleRepo.InsertBatch's doc comment).
	OffsetSec int64 `json:"offsetSec"`
	SrcPort   int   `json:"srcPort"`
	DstPort   int   `json:"dstPort"`
	Proto     int   `json:"proto"`
}

// flowFixtureFile is one *.json file under [flows] dev_fixture_dir: every
// record in it belongs to the same guest/node — a fixture family is one
// guest's traffic history, not a cluster-wide dump.
type flowFixtureFile struct {
	Guest   string              `json:"guest"`
	Node    string              `json:"node"`
	Records []flowFixtureRecord `json:"records"`
}

// loadFlowFixtures reads every *.json file directly under dir and seeds repo
// with the flow_samples they describe, timestamped relative to now. It is
// the [flows] analogue of loadFwLogFixtures (fwlog.go) — a static, additive
// corpus for a dev daemon with no real UDP exporters or host conntrack table
// to seed a flow-baseline history from, built specifically to unblock
// T-1602/T-1603's microsegmentation planner e2e coverage
// (web/e2e/microseg.spec.ts's AC4/AC5), which needs a guest carrying a
// seeded flow history that also exists in the daemon's live inventory
// (testdata/clusters/three-node-vlan.yaml's app01, guest:pve1:200).
//
// Unlike loadFwLogFixtures, this does NOT replace the real ingestion path:
// [flows]' listeners/samplers stay wired exactly as configured by
// setupFlows/setupHostSample. This seed data is inserted directly into
// flow_samples once at startup, before any of those actors have run — it is
// additive history, tagged flow.SourceFixture so it is never mistaken for a
// real sFlow/NetFlow/IPFIX/conntrack observation (docs/api.md's Flows
// section; GET /flows and the flow explorer render `source` verbatim).
func loadFlowFixtures(ctx context.Context, repo *store.FlowSampleRepo, dir string, now time.Time, logger *slog.Logger) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("reading flow fixture dir %s: %w", dir, err)
	}

	var samples []store.FlowSample
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		data, err := os.ReadFile(path) //nolint:gosec // fixed dev fixture path from [flows] dev_fixture_dir, empty (disabled) in every production config
		if err != nil {
			return 0, fmt.Errorf("reading flow fixture %s: %w", path, err)
		}
		var f flowFixtureFile
		if err := json.Unmarshal(data, &f); err != nil {
			return 0, fmt.Errorf("parsing flow fixture %s: %w", path, err)
		}
		if f.Guest == "" || f.Node == "" {
			return 0, fmt.Errorf("flow fixture %s: guest and node are required", path)
		}
		for i, r := range f.Records {
			if r.GuestSide != "src" && r.GuestSide != "dst" {
				return 0, fmt.Errorf("flow fixture %s: record %d has guestSide %q, want \"src\" or \"dst\"", path, i, r.GuestSide)
			}
			at := now.Add(-time.Duration(r.DaysAgo*float64(24*time.Hour))).Unix() + r.OffsetSec
			s := store.FlowSample{
				Node:    f.Node,
				SrcIP:   r.SrcIP,
				DstIP:   r.DstIP,
				Source:  string(flow.SourceFixture),
				At:      at,
				Bytes:   r.Bytes,
				Packets: r.Packets,
				SrcPort: r.SrcPort,
				DstPort: r.DstPort,
				Proto:   r.Proto,
			}
			if r.GuestSide == "src" {
				s.SrcRef = f.Guest
			} else {
				s.DstRef = f.Guest
			}
			samples = append(samples, s)
		}
	}

	if err := repo.InsertBatch(ctx, samples); err != nil {
		return 0, fmt.Errorf("seeding flow_samples from dev fixtures: %w", err)
	}
	if logger != nil {
		logger.Warn("flows: DEV MODE fixture corpus seeded into flow_samples — this is static test data, not real ingestion", "dir", dir, "count", len(samples))
	}
	return len(samples), nil
}
