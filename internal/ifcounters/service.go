// SPDX-License-Identifier: Apache-2.0

package ifcounters

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/latmesh"
)

// DefaultPollIntervalSec is used when Config.PollIntervalSec is unset.
// Coarser than internal/latmesh's 10s default and roughly in line with
// internal/mtuprobe's 300s — SNMP counters are useful on a similar
// dashboard-refresh cadence, not a sub-second one, and every unnecessary
// poll is an unsolicited packet at a switch this daemon doesn't own.
const DefaultPollIntervalSec = 60

// DefaultPollTimeout bounds a single switch's poll (dial + correlation walk
// + counter GET) within one tick.
const DefaultPollTimeout = 5 * time.Second

// Config configures a Service. Neighbors/Targets are independently
// optional: either being nil makes Tick a no-op (mirrors
// internal/mtuprobe.Config's nil-dependency degraded-mode convention).
type Config struct {
	Neighbors       NeighborLister
	Targets         TargetStore
	Logger          *slog.Logger
	Now             func() time.Time
	dial            dialFunc // test seam; real callers never set this (New defaults it)
	PollIntervalSec int
	PollTimeout     time.Duration
}

// Service is T-4013's poller and current-state store — Tick/RunLoop poll
// every LLDP-observed, operator-opted-in switch once per PollIntervalSec and
// hold each edge's latest Result in memory (see doc.go: never a second
// discovery mechanism, never a SQLite ring — mirrors
// internal/mtuprobe.Service's identical "current state, not a ring" shape).
type Service struct {
	logger  *slog.Logger
	now     func() time.Time
	results map[string]Result // key: ChassisID + "|" + LocalIface, see resultKey
	cfg     Config
	mu      sync.RWMutex
}

// New builds a Service from cfg, defaulting unset tunables.
func New(cfg Config) *Service {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.PollIntervalSec <= 0 {
		cfg.PollIntervalSec = DefaultPollIntervalSec
	}
	if cfg.PollTimeout <= 0 {
		cfg.PollTimeout = DefaultPollTimeout
	}
	if cfg.dial == nil {
		cfg.dial = realDial
	}
	return &Service{cfg: cfg, logger: cfg.Logger, now: cfg.Now, results: map[string]Result{}}
}

// Results returns every current Result, in no particular order — callers
// (the API handler, tests) sort as needed.
func (s *Service) Results() []Result {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Result, 0, len(s.results))
	for _, r := range s.results {
		out = append(out, r)
	}
	return out
}

func (s *Service) replace(results []Result) {
	next := make(map[string]Result, len(results))
	for _, r := range results {
		next[resultKey(r.ChassisID, r.LocalIface)] = r
	}
	s.mu.Lock()
	s.results = next
	s.mu.Unlock()
}

func resultKey(chassisID, localIface string) string { return chassisID + "|" + localIface }

// Tick discovers this tick's LLDP neighbor set, groups it by switch chassis,
// and — for every chassis with an enabled target — polls once and fans the
// result out to every local port that neighbor relationship covers. A
// neighbor with no LLDP ChassisID is skipped entirely (nothing to key a
// Result or a target lookup on). See doc.go for the "never a second
// discovery mechanism" and honest-states guarantees this method exists to
// keep.
func (s *Service) Tick(ctx context.Context) {
	if s.cfg.Neighbors == nil {
		return
	}
	neighbors := s.cfg.Neighbors.LLDPNeighbors()
	if len(neighbors) == 0 {
		s.replace(nil)
		return
	}

	targets := map[string]Target{}
	if s.cfg.Targets != nil {
		ts, err := s.cfg.Targets.ListEnabled(ctx)
		if err != nil {
			s.logger.Warn("ifcounters: listing SNMP targets failed, treating every switch as not configured this tick", "error", err)
		}
		for _, t := range ts {
			targets[t.ChassisID] = t
		}
	}

	groups := map[string][]*inventory.LldpNeighbor{}
	var order []string
	for _, n := range neighbors {
		if n == nil || n.ChassisID == "" {
			continue
		}
		if _, seen := groups[n.ChassisID]; !seen {
			order = append(order, n.ChassisID)
		}
		groups[n.ChassisID] = append(groups[n.ChassisID], n)
	}

	now := s.now().Unix()
	var results []Result
	for _, chassisID := range order {
		group := groups[chassisID]
		target, configured := targets[chassisID]
		if !configured {
			for _, n := range group {
				results = append(results, baseResult(n, now, StateNotConfigured))
			}
			continue
		}
		counters, err := pollChassisCounters(ctx, s.cfg.dial, target, group, s.cfg.PollTimeout)
		if err != nil {
			s.logger.Debug("ifcounters: poll failed", "chassisId", chassisID, "error", err)
			for _, n := range group {
				results = append(results, baseResult(n, now, StateUnreachable))
			}
			continue
		}
		for _, n := range group {
			c, ok := counters[n.PortID]
			if !ok {
				results = append(results, baseResult(n, now, StateNoCounters))
				continue
			}
			r := baseResult(n, now, StateOK)
			r.Counters = c
			results = append(results, r)
		}
	}
	s.replace(results)
}

func baseResult(n *inventory.LldpNeighbor, now int64, state State) Result {
	name := n.ChassisName
	if name == "" {
		name = n.ChassisID
	}
	return Result{
		ChassisID:  n.ChassisID,
		SwitchName: name,
		Node:       n.Node,
		LocalIface: n.LocalIface,
		SwitchPort: n.PortID,
		State:      state,
		At:         now,
	}
}

// RunLoop drives the periodic poll cycle on PollIntervalSec until ctx is
// cancelled — the exact shared ticker primitive internal/mtuprobe.Service.
// RunLoop and internal/latmesh.Service.RunLoop themselves use
// (latmesh.RunTicker, internal/latmesh/scheduler.go), not a second
// hand-rolled ticker loop. cmd/vnproxd registers this as its own supervised
// run-group actor the same way it registers those two.
func (s *Service) RunLoop(ctx context.Context) error {
	return latmesh.RunTicker(ctx, time.Duration(s.cfg.PollIntervalSec)*time.Second, s.Tick)
}
