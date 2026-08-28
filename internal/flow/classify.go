// SPDX-License-Identifier: Apache-2.0

// classify.go implements T-1504's service-network attribution: classifying
// migration, backup (PBS), Ceph, and corosync traffic in the flow explorer
// and history playback (both of which render straight off GET /flows —
// history playback's scrubber replays flow-painted edges by re-querying
// GET /flows with a fromTs/toTs window, docs/api.md's History section — so
// this package's classifier is the one thing both surfaces need).
//
// Honesty contract (stated explicitly per this task's card, AC5): every
// classification decision here uses only a Record's own metadata already
// carried by internal/flow (IP/CIDR containment, an exact known address,
// VLAN, protocol/port) — never packet payload. This package is not, and
// must never become, an IDS; it has no import of any capture/payload
// package (internal/capture, internal/pvemock's packet fixtures, etc.) and
// never will.
//
// Design: a Classifier holds zero or more NetworkSource values per
// NetworkSourceKind, registered via RegisterNetworkSource. This card
// (T-1504) registers corosync's ring addresses (from
// internal/host.CorosyncConfig, already read for the safety-interlock
// protected-set detector) and, where an operator has declared one, PVE's
// migration network. Two classes are deliberately *not* wired to any live
// source by this card:
//
//   - backup (PBS): T-1206 (PBS network awareness, Phase 12) is not present
//     in this repo yet (confirmed — no internal/pbs package, no
//     pbs-host/backup-path inventory entities exist). NewBackupPathSource
//     below is a declared-but-inert seam: the mechanism and shape exist
//     (exercised directly by this package's own tests with synthetic PBS
//     addresses) so that whenever T-1206 lands and starts discovering real
//     node→PBS backup-path edges, wiring it into the classifier at
//     cmd/vnproxd is a one-line RegisterNetworkSource call, not a design
//     change here.
//   - ceph-public/ceph-cluster: T-1503 registers these itself once it
//     discovers Ceph's public/cluster CIDRs from PVE's own Ceph config —
//     "T-1503 does not implement its own classification logic, it supplies
//     T-1504's engine with Ceph's network declarations"
//     (planning/tasks/phase-15.md). No Ceph-specific constructor exists
//     here; T-1503 uses the generic NewCIDRSource directly with
//     NetworkSourceKindCeph and ServiceClassCephPublic/ServiceClassCephCluster.
//
// T-1507 (migration planner) consumes this package's ServiceClassMigration
// tag directly (headroom = link capacity minus current
// ServiceClassMigration volume) — no second migration-traffic detector.

package flow

import (
	"fmt"
	"net"
	"strings"
	"sync"
)

// ServiceClass is a flow's attributed service, per this task's card. A
// Record that matches no registered NetworkSource is ServiceClassUnclassified
// — never a guess.
type ServiceClass string

const (
	ServiceClassMigration    ServiceClass = "migration"
	ServiceClassBackup       ServiceClass = "backup"
	ServiceClassCephPublic   ServiceClass = "ceph-public"
	ServiceClassCephCluster  ServiceClass = "ceph-cluster"
	ServiceClassCorosync     ServiceClass = "corosync"
	ServiceClassUnclassified ServiceClass = "unclassified"
)

// NetworkSourceKind names which known-network declaration a registered
// NetworkSource contributes — the RegisterNetworkSource(kind, source)
// extension point this card's design requires so T-1503/T-1507 can extend
// the input set without touching this file's classification core.
type NetworkSourceKind string

const (
	NetworkSourceKindCorosync  NetworkSourceKind = "corosync"
	NetworkSourceKindMigration NetworkSourceKind = "migration"
	NetworkSourceKindBackup    NetworkSourceKind = "backup"
	NetworkSourceKindCeph      NetworkSourceKind = "ceph"
)

// classifyPrecedence is the fixed, deterministic order Classifier checks
// registered NetworkSource kinds in: a real cluster keeps these networks
// disjoint, but this package never assumes it — corosync's tiny,
// safety-critical footprint is checked first so an address that happens to
// also fall in a broader declared range (e.g. a Ceph CIDR that happens to
// span a corosync ring address) is never miscategorized away from the
// smaller, more specific class.
var classifyPrecedence = []NetworkSourceKind{
	NetworkSourceKindCorosync,
	NetworkSourceKindMigration,
	NetworkSourceKindBackup,
	NetworkSourceKindCeph,
}

// NetworkSource is one declared-network input a Classifier checks a
// Record's own metadata against. Implementations in this file (addrSetSource,
// cidrSource) never look at anything beyond Record's IP/port/VLAN fields —
// see this file's doc comment's honesty contract.
type NetworkSource interface {
	Kind() NetworkSourceKind
	// Classify reports the ServiceClass this source assigns rec, if any.
	Classify(rec Record) (class ServiceClass, ok bool)
	// DeclaredVLANs is the VLAN id set this source's network is declared to
	// live on, for service_traffic_on_wrong_network to compare a matched
	// record's own VLAN against. An empty/nil result means "no VLAN
	// declared for this source" — the wrong-network check has nothing to
	// judge and stays silent for traffic this source classifies, the same
	// "never guessed" stance the rest of this package takes.
	DeclaredVLANs() []int
}

// Classified is one Record's classification verdict (Classifier.Verdict/
// ClassifyBatch) — the shape internal/findings' service_traffic_on_wrong_network
// check (T-1504) and, via GET /flows, the flow explorer/history playback
// consume.
type Classified struct {
	ServiceClass ServiceClass
	Record       Record
	WrongNetwork bool
}

// Classifier assigns a ServiceClass (and a wrong-network verdict) to
// flow.Records from zero or more registered NetworkSources, using only
// Record's own metadata — see this file's doc comment.
type Classifier struct {
	sources map[NetworkSourceKind][]NetworkSource
	mu      sync.RWMutex
}

// NewClassifier builds an empty Classifier — Classify/Verdict return
// ServiceClassUnclassified for everything until sources are registered.
func NewClassifier() *Classifier {
	return &Classifier{sources: map[NetworkSourceKind][]NetworkSource{}}
}

// RegisterNetworkSource adds source under kind, appended after any sources
// already registered for that kind (checked in registration order — the
// first match under a kind wins). This is the extension point T-1503
// (Ceph CIDRs) and future work (T-1206-backed backup-path edges) use to
// grow the input set without any change to this file's classification
// logic. Safe for concurrent use with Classify/Verdict.
func (c *Classifier) RegisterNetworkSource(kind NetworkSourceKind, source NetworkSource) {
	if source == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sources[kind] = append(c.sources[kind], source)
}

// Classify returns rec's ServiceClass — ServiceClassUnclassified when no
// registered source matches.
func (c *Classifier) Classify(rec Record) ServiceClass {
	return c.Verdict(rec).ServiceClass
}

// Verdict is Classify plus the wrong-network comparison — see Classified's
// doc comment. Sources are checked in classifyPrecedence order, each kind's
// own sources in registration order; the first match wins.
func (c *Classifier) Verdict(rec Record) Classified {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, kind := range classifyPrecedence {
		for _, src := range c.sources[kind] {
			class, ok := src.Classify(rec)
			if !ok {
				continue
			}
			wrong := false
			if rec.VLAN != 0 {
				if declared := src.DeclaredVLANs(); len(declared) > 0 && !containsInt(declared, rec.VLAN) {
					wrong = true
				}
			}
			return Classified{Record: rec, ServiceClass: class, WrongNetwork: wrong}
		}
	}
	return Classified{Record: rec, ServiceClass: ServiceClassUnclassified}
}

// ClassifyBatch runs Verdict over every record — the shape
// internal/findings' FlowProvider seam (T-1504's service_traffic_on_wrong_network
// check) and cmd/vnproxd's GET /flows response wiring both use.
func (c *Classifier) ClassifyBatch(records []Record) []Classified {
	out := make([]Classified, len(records))
	for i, r := range records {
		out[i] = c.Verdict(r)
	}
	return out
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// --- Built-in NetworkSource implementations ---------------------------

// addrSetSource classifies by exact SrcIP/DstIP membership in a fixed
// address set (e.g. corosync ring addresses, PBS host addresses) — the
// right match shape for a small set of individual hosts, as opposed to a
// CIDR range.
type addrSetSource struct {
	kind  NetworkSourceKind
	class ServiceClass
	addrs map[string]struct{}
	vlans []int
}

func (s *addrSetSource) Kind() NetworkSourceKind { return s.kind }
func (s *addrSetSource) DeclaredVLANs() []int    { return s.vlans }

func (s *addrSetSource) Classify(rec Record) (ServiceClass, bool) {
	if _, ok := s.addrs[rec.SrcIP]; ok {
		return s.class, true
	}
	if _, ok := s.addrs[rec.DstIP]; ok {
		return s.class, true
	}
	return "", false
}

// NewAddrSetSource builds a NetworkSource matching an exact set of
// addresses (SrcIP or DstIP), tagging a match class under kind. vlans is
// this source's declared VLAN set (nil/empty means "none declared" — see
// NetworkSource.DeclaredVLANs).
func NewAddrSetSource(kind NetworkSourceKind, class ServiceClass, addrs []string, vlans []int) NetworkSource {
	set := make(map[string]struct{}, len(addrs))
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		set[a] = struct{}{}
	}
	return &addrSetSource{kind: kind, class: class, addrs: set, vlans: append([]int(nil), vlans...)}
}

// cidrSource classifies by SrcIP/DstIP CIDR containment — the right match
// shape for a declared network range (a migration network, a Ceph
// public/cluster CIDR).
type cidrSource struct {
	kind  NetworkSourceKind
	class ServiceClass
	nets  []*net.IPNet
	vlans []int
}

func (s *cidrSource) Kind() NetworkSourceKind { return s.kind }
func (s *cidrSource) DeclaredVLANs() []int    { return s.vlans }

func (s *cidrSource) contains(ipStr string) bool {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}
	for _, n := range s.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *cidrSource) Classify(rec Record) (ServiceClass, bool) {
	if s.contains(rec.SrcIP) || s.contains(rec.DstIP) {
		return s.class, true
	}
	return "", false
}

// NewCIDRSource builds a NetworkSource matching a declared set of CIDRs
// (SrcIP or DstIP contained in any of them), tagging a match class under
// kind. Returns an error if any cidr fails to parse — a malformed
// declaration is a caller/config bug, never silently ignored. vlans is this
// source's declared VLAN set (see NetworkSource.DeclaredVLANs).
//
// This is the generic constructor T-1503 (Ceph) uses directly, registering
// its public/cluster CIDRs under NetworkSourceKindCeph with
// ServiceClassCephPublic/ServiceClassCephCluster — T-1503 supplies network
// declarations, it does not implement its own classification logic.
func NewCIDRSource(kind NetworkSourceKind, class ServiceClass, cidrs []string, vlans []int) (NetworkSource, error) {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		_, ipnet, err := net.ParseCIDR(c)
		if err != nil {
			return nil, fmt.Errorf("flow: parsing declared %s CIDR %q: %w", kind, c, err)
		}
		nets = append(nets, ipnet)
	}
	return &cidrSource{kind: kind, class: class, nets: nets, vlans: append([]int(nil), vlans...)}, nil
}

// NewCorosyncSource builds ServiceClassCorosync's NetworkSource from
// corosync.conf's own configured ring addresses (internal/host.CorosyncConfig
// — the same static-address substrate T-803's corosync_link_degraded check
// and internal/change.DetectProtected's safety interlocks already read;
// see this file's doc comment for why corosync's own UDP port is
// deliberately *not* used as a second signal — its exact per-ring port
// numbering is unverified against real hardware,
// planning/reports/needs-hardware-validation.md). vlans is corosync's
// declared VLAN(s), when an operator/composition root knows them (nil
// leaves service_traffic_on_wrong_network silent for corosync traffic —
// see NetworkSource.DeclaredVLANs).
func NewCorosyncSource(ringAddrs []string, vlans []int) NetworkSource {
	return NewAddrSetSource(NetworkSourceKindCorosync, ServiceClassCorosync, ringAddrs, vlans)
}

// NewMigrationNetworkSource builds ServiceClassMigration's NetworkSource
// from a declared migration-network CIDR set. No live reader of PVE's own
// datacenter.cfg `migration: network=` exists anywhere in this codebase yet
// (a repeatedly-documented gap — see docs/api.md's Fabric-scope note on
// T-1303's latency mesh and planning/reports/needs-hardware-validation.md's
// "neither a PVE migration network ... nor a distinct storage network is
// modeled anywhere in internal/inventory/internal/pve yet"); this
// constructor is the declaration-shaped input such a reader (or an
// operator-supplied config value, cmd/vnproxd's current wiring) feeds once
// a CIDR is known. vlans is the migration network's declared VLAN(s).
func NewMigrationNetworkSource(cidrs []string, vlans []int) (NetworkSource, error) {
	return NewCIDRSource(NetworkSourceKindMigration, ServiceClassMigration, cidrs, vlans)
}

// NewBackupPathSource builds ServiceClassBackup's NetworkSource from a set
// of PBS host addresses — the "extends T-1206" attribution this task's
// card names. T-1206 (PBS network awareness, Phase 12: pbs-host inventory
// entities + node→PBS backup-path edges) is not present in this repo (see
// this file's doc comment) — this constructor is a declared-but-inert seam:
// it works correctly given real PBS addresses (proven by this package's own
// tests) but cmd/vnproxd's production wiring does not call it today, since
// there is no live source of PBS addresses to pass it. When T-1206 lands,
// wiring `RegisterNetworkSource(NetworkSourceKindBackup,
// flow.NewBackupPathSource(pbsAddrs, vlans))` at the composition root is
// the entire integration step. vlans is backup traffic's declared VLAN(s).
func NewBackupPathSource(pbsAddrs []string, vlans []int) NetworkSource {
	return NewAddrSetSource(NetworkSourceKindBackup, ServiceClassBackup, pbsAddrs, vlans)
}
