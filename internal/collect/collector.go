package collect

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// Default poll intervals, matching config.Default{PVE,Host,LLDP}Interval
// (docs/deployment.md's [collect] section). Duplicated here as a defensive
// fallback for direct construction (e.g. in tests) that doesn't go through
// internal/config — cmd/vnproxd always supplies the already-defaulted
// durations from a loaded config.Config, so in production these never
// apply.
const (
	DefaultPVEInterval  = 10 * time.Second
	DefaultHostInterval = 5 * time.Second
	DefaultLLDPInterval = 30 * time.Second
)

// maxBackoff caps exponential backoff after consecutive poll failures
// (deliverable 5: "backoff on PVE errors ... without killing the loop").
const maxBackoff = 60 * time.Second

// Config configures a Collector. PVE, Host, and Graph are required; the
// rest have sane defaults.
type Config struct {
	Host  host.Reader
	PVE   *pve.Client
	Graph *inventory.Graph
	// Peer is T-303's cluster fan-out dependency: when set, the host loop
	// polls every other cluster member's netlink links, interfaces file,
	// and stats through this Client (docs/architecture.md §1: "peer
	// vnproxd instances on other cluster nodes for node-local data"), in
	// addition to this daemon's own node via Host. Nil (the default,
	// matching every pre-T-303 caller) preserves the original local-only
	// behavior exactly — single-node deployments have zero peers per
	// internal/peer's documented contract, so this is never required for
	// correctness on a single node.
	Peer    *peer.Client
	Logger  *slog.Logger
	OnDelta func(inventory.Delta)
	// OnStats is T-601's metrics sampler hook: called once per successfully
	// polled node (local node directly, each reachable peer via
	// peerHostReader), every host-loop tick, with that node's raw interface
	// counters (host.Reader.Stats) alongside the same Links() read used to
	// populate inventory — the sampler needs Links for interface kind/speed/
	// bond-slave metadata, not just the counters themselves. Nil (the
	// default, matching every pre-T-601 caller) preserves the original
	// "read stats, discard" behavior exactly: hostPollStateFor's doc comment
	// used to note counters were "read per deliverable 2 but still
	// discarded ... modeling them is internal/metrics' future job" — this is
	// that job. A failed reader.Stats read never reaches this hook at all
	// (same as before: the read's own error is logged and swallowed there).
	OnStats func(ctx context.Context, node string, at time.Time, links []host.LinkState, stats map[string]host.IfaceStats)
	// OnServices is T-602's findings-engine hook: called once per
	// successfully polled node (local directly, each reachable peer via
	// peerHostReader), every host-loop tick, with that node's current
	// host.Reader.Services result (T-602's watched systemd unit status —
	// see internal/findings/health_service.go). Nil (the default) simply
	// skips the Services() read entirely — the same "no hook, no work"
	// contract OnStats already establishes for reader.Stats.
	OnServices func(node string, status map[string]bool)
	// OnPoll is T-1903's self-observability hook: called once per poll
	// attempt (RunPVELoop/RunHostLoop/RunLLDPLoop's ticks, plus RefreshNow's
	// on-demand ones), with the same (source, node) scoping
	// collect.SourceStatus already uses ("pve"/""; "host"/<node>; "lldp"/
	// <localNode>) and the attempt's own duration/outcome. It is purely
	// additive to — never a replacement for — recordResult/recordNodeResult's
	// existing lastSuccess/lastAttempt/consecutiveFailures bookkeeping
	// (Status(), consumed by GET /health): that bookkeeping is unchanged by
	// this field's presence. Nil (the default) is a no-op, the same
	// nil-safe-optional-hook convention OnStats/OnServices/OnDelta already
	// establish.
	OnPoll       func(source, node string, dur time.Duration, err error)
	LocalNode    string
	PVEInterval  time.Duration
	HostInterval time.Duration
	LLDPInterval time.Duration
	// HostServesCluster (T-2801) declares that Host answers for every
	// cluster node, not only this daemon's own. The host loop then reads
	// every node through Host and never builds a peerHostReader.
	//
	// False for every real deployment and every pre-T-2801 caller: a real
	// host.Reader reads netlink, procfs and lldpctl on the machine it runs
	// on, and cannot answer for another node however politely you ask —
	// which is exactly why the peer API exists. It is true for exactly one
	// caller, `vnproxd --demo`, whose Host is a fixture reader over the
	// whole synthetic cluster (internal/host.FixtureReader). Without it a
	// demo daemon would dial its fixture's own node addresses (10.10.0.12,
	// 10.10.0.13, ...) over the real network — addresses that resolve to
	// SOMETHING on plenty of networks — which is the precise opposite of
	// "no network access".
	HostServesCluster bool
}

// sourceState is the staleness/backoff bookkeeping for one named poll loop
// ("pve", "host", or "lldp").
type sourceState struct {
	lastSuccess         time.Time
	lastAttempt         time.Time
	lastErr             error
	consecutiveFailures int
}

// Collector runs the PVE and host poll loops that keep an inventory.Graph
// current (docs/architecture.md §3). Construct with New; start its loops by
// registering RunPVELoop, RunHostLoop, and RunLLDPLoop with cmd/vnproxd's
// runGroup. A Collector is safe for concurrent use.
type Collector struct {
	host       host.Reader
	pve        *pve.Client
	peerClient *peer.Client
	graph      *inventory.Graph
	log        *slog.Logger
	onDelta    func(inventory.Delta)
	onStats    func(ctx context.Context, node string, at time.Time, links []host.LinkState, stats map[string]host.IfaceStats)
	onServices func(node string, status map[string]bool)
	onPoll     func(source, node string, dur time.Duration, err error)
	status     map[string]*sourceState
	// hostNodeStatus is the per-cluster-node staleness/backoff bookkeeping
	// for the "host" source (T-303: unlike "pve" and "lldp", which stay a
	// single entry, "host" now polls every cluster member — self directly,
	// every other node through peerClient — so Status() needs one entry
	// per node, keyed here by node name; guarded by statusMu like status).
	hostNodeStatus map[string]*sourceState
	// seenNodes is the cluster membership observed by the previous
	// successful cluster-status poll (guarded by mu). pvePollAll compares
	// it against the current membership to retire departed nodes' entities
	// (see retireDepartedNodes).
	seenNodes map[string]bool
	// peers is the peer address book learned from the same GET
	// /cluster/status poll that discovers seenNodes (guarded by mu),
	// keyed by node name. Populated only when Config.Peer is set; used by
	// hostPollOnce to fan out to every peer without a second discovery
	// round-trip per host-loop tick.
	peers     map[string]peer.Peer
	localNode string
	// clusterNodes is every node name the last cluster-status poll saw
	// (guarded by mu). Populated only when hostServesCluster is set — the
	// demo daemon's case, where one cluster-wide fixture reader answers for
	// every node and there is no peer address book to fan out to.
	//
	// After localNode, not before it: fieldalignment counts the scanned
	// prefix, and a slice's len/cap tail inside that prefix is what a
	// string-before-slice ordering avoids.
	clusterNodes []string
	pveInterval  time.Duration
	hostInterval time.Duration
	lldpInterval time.Duration
	mu           sync.Mutex
	statusMu     sync.Mutex
	// hostServesCluster reports that Config.Host answers for EVERY cluster
	// node, not only this daemon's own — see Config.HostServesCluster.
	// Last, and a bool: fieldalignment wants the pointer-bearing fields
	// packed ahead of it.
	hostServesCluster bool
}

// New builds a Collector from cfg. It performs no network calls; polling
// only happens once a loop is started (or RefreshNow is called).
func New(cfg Config) (*Collector, error) {
	if cfg.PVE == nil {
		return nil, fmt.Errorf("collect: Config.PVE is required")
	}
	if cfg.Host == nil {
		return nil, fmt.Errorf("collect: Config.Host is required")
	}
	if cfg.Graph == nil {
		return nil, fmt.Errorf("collect: Config.Graph is required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	pveInterval := cfg.PVEInterval
	if pveInterval <= 0 {
		pveInterval = DefaultPVEInterval
	}
	hostInterval := cfg.HostInterval
	if hostInterval <= 0 {
		hostInterval = DefaultHostInterval
	}
	lldpInterval := cfg.LLDPInterval
	if lldpInterval <= 0 {
		lldpInterval = DefaultLLDPInterval
	}

	return &Collector{
		pve:          cfg.PVE,
		host:         cfg.Host,
		peerClient:   cfg.Peer,
		graph:        cfg.Graph,
		log:          logger,
		pveInterval:  pveInterval,
		hostInterval: hostInterval,
		lldpInterval: lldpInterval,
		onDelta:      cfg.OnDelta,
		onStats:      cfg.OnStats,
		onServices:   cfg.OnServices,
		onPoll:       cfg.OnPoll,
		localNode:    cfg.LocalNode,

		hostServesCluster: cfg.HostServesCluster,
		status: map[string]*sourceState{
			"pve":  {},
			"host": {},
			"lldp": {},
		},
		hostNodeStatus: map[string]*sourceState{},
		peers:          map[string]peer.Peer{},
	}, nil
}

// getLocalNode returns the node the host/LLDP pollers currently target, or
// "" if not yet known (before the first successful PVE cluster-status poll,
// unless Config.LocalNode seeded it).
func (c *Collector) getLocalNode() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.localNode
}

// setLocalNode records the node discovered as "local" by a cluster-status
// poll (GET /cluster/status's per-row "local" flag).
func (c *Collector) setLocalNode(node string) {
	if node == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.localNode != node {
		c.log.Info("collect: discovered local node", "node", node)
	}
	c.localNode = node
}

// setPeers records the peer address book discovered by the most recent
// successful cluster-status poll (T-303). Called only when Config.Peer is
// set; hostPollOnce reads it back via getPeers.
func (c *Collector) setPeers(peers map[string]peer.Peer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.peers = peers
}

// setClusterNodes records every node name the most recent successful
// cluster-status poll saw. Populated only when hostServesCluster is set;
// hostPollOnce reads it back via getClusterNodes.
func (c *Collector) setClusterNodes(nodes []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clusterNodes = nodes
}

// getClusterNodes returns a stable-ordered copy of the cluster membership
// (empty before the first cluster-status poll, or when hostServesCluster is
// unset).
func (c *Collector) getClusterNodes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]string(nil), c.clusterNodes...)
	sort.Strings(out)
	return out
}

// getPeers returns a stable-ordered snapshot of the current peer address
// book (empty, never nil, before the first cluster-status poll or when
// Config.Peer is unset).
func (c *Collector) getPeers() []peer.Peer {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]peer.Peer, 0, len(c.peers))
	for _, p := range c.peers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out
}

// recordNodeResult updates one cluster node's "host" source staleness/
// backoff bookkeeping (T-303's per-node counterpart to recordResult, which
// stays keyed purely by loop name for "pve"/"lldp" and for "host"'s
// backoff-driving local-node result — see hostPollOnce).
func (c *Collector) recordNodeResult(node string, attemptTime time.Time, err error) {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	st := c.hostNodeStatus[node]
	if st == nil {
		st = &sourceState{}
		c.hostNodeStatus[node] = st
	}
	st.lastAttempt = attemptTime
	if err == nil {
		if st.consecutiveFailures > 0 {
			c.log.Info("collect: peer host poll recovered", "node", node, "previous_failures", st.consecutiveFailures)
		}
		st.lastSuccess = attemptTime
		st.lastErr = nil
		st.consecutiveFailures = 0
		return
	}
	st.lastErr = err
	st.consecutiveFailures++
}

// retireHostNodeStatus drops node's per-node "host" staleness bookkeeping
// (called by retireDepartedNodes when a node leaves the PVE cluster, so a
// departed node doesn't linger forever in Status().Sources).
func (c *Collector) retireHostNodeStatus(node string) {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	delete(c.hostNodeStatus, node)
}

// recordResult updates the named loop's staleness/backoff bookkeeping.
func (c *Collector) recordResult(name string, attemptTime time.Time, err error) {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	st := c.status[name]
	if st == nil {
		st = &sourceState{}
		c.status[name] = st
	}
	st.lastAttempt = attemptTime
	if err == nil {
		if st.consecutiveFailures > 0 {
			c.log.Info("collect: poll recovered", "source", name, "previous_failures", st.consecutiveFailures)
		}
		st.lastSuccess = attemptTime
		st.lastErr = nil
		st.consecutiveFailures = 0
		return
	}
	st.lastErr = err
	st.consecutiveFailures++
	// Log level intentionally stays at Warn on every failure rather than
	// escalating/suppressing: backoff itself (backoffFor) already thins
	// the log rate out as failures accumulate (10s -> 20s -> ... ->
	// 60s-capped retries), so this does not "spam" in practice.
	c.log.Warn("collect: poll failed", "source", name, "consecutive_failures", st.consecutiveFailures, "error", err)
}

// reportPoll invokes onPoll (T-1903), if configured. Called alongside — but
// independently of — recordResult/recordNodeResult at every poll call site,
// so a change here can never affect Status()'s existing staleness/backoff
// bookkeeping.
func (c *Collector) reportPoll(source, node string, dur time.Duration, err error) {
	if c.onPoll != nil {
		c.onPoll(source, node, dur, err)
	}
}

// consecutiveFailures reports the named loop's current failure streak.
func (c *Collector) consecutiveFailures(name string) int {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	if st := c.status[name]; st != nil {
		return st.consecutiveFailures
	}
	return 0
}

// SourceStatus is one poll loop's staleness/health snapshot. Node scopes
// the source to a single cluster node's topology band (docs/api.md's
// staleness.sources[].node); empty means cluster-wide. Since T-303, "host"
// carries one SourceStatus per cluster member (self plus every reachable
// peer) rather than a single local-node-only entry — "lldp" and "pve" are
// unaffected (see Status's doc comment).
type SourceStatus struct {
	LastSuccess         time.Time
	LastAttempt         time.Time
	Name                string
	Node                string
	LastError           string
	ConsecutiveFailures int
}

// Status is a point-in-time snapshot of every poll loop's staleness. "pve"
// is always exactly one cluster-wide (Node == "") entry; "lldp" is always
// exactly one local-node-scoped entry (T-302 owns fanning LLDP out
// cluster-wide; until then this stays local-only, matching pre-T-303
// behavior); "host" is one entry per cluster node this daemon knows about
// (itself plus every peer discovered via the most recent cluster-status
// poll, T-303) — before that first discovery (or on a single-node
// deployment with Config.Peer unset), it is the same single local-node
// entry pre-T-303 callers already expect. Deliverable 4: exposed so
// /api/v1/health (via a small adapter cmd/vnproxd wires in) can surface
// per-source staleness without this package knowing anything about HTTP or
// JSON shapes.
type Status struct {
	LocalNode string
	Sources   []SourceStatus
}

// toSourceStatus projects one sourceState (nil-safe: an unpolled loop
// reports as its zero value) into the public SourceStatus shape.
func toSourceStatus(name, node string, st *sourceState) SourceStatus {
	if st == nil {
		return SourceStatus{Name: name, Node: node}
	}
	s := SourceStatus{
		Name: name, Node: node,
		LastSuccess:         st.lastSuccess,
		LastAttempt:         st.lastAttempt,
		ConsecutiveFailures: st.consecutiveFailures,
	}
	if st.lastErr != nil {
		s.LastError = st.lastErr.Error()
	}
	return s
}

// Status returns a snapshot of every loop's current staleness/health.
func (c *Collector) Status() Status {
	localNode := c.getLocalNode()

	c.statusMu.Lock()
	defer c.statusMu.Unlock()

	out := make([]SourceStatus, 0, 2+len(c.hostNodeStatus))
	out = append(out, toSourceStatus("pve", "", c.status["pve"]))

	if len(c.hostNodeStatus) == 0 {
		// Nothing has been discovered/polled yet (or Config.Peer is unset
		// and even the local node hasn't been polled once) — report the
		// single unscoped placeholder pre-T-303 callers expect.
		out = append(out, SourceStatus{Name: "host"})
	} else {
		nodes := make([]string, 0, len(c.hostNodeStatus))
		for n := range c.hostNodeStatus {
			nodes = append(nodes, n)
		}
		sort.Strings(nodes)
		for _, n := range nodes {
			out = append(out, toSourceStatus("host", n, c.hostNodeStatus[n]))
		}
	}

	out = append(out, toSourceStatus("lldp", localNode, c.status["lldp"]))
	return Status{LocalNode: localNode, Sources: out}
}
