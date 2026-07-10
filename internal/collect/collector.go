package collect

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
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
	Host         host.Reader
	PVE          *pve.Client
	Graph        *inventory.Graph
	Logger       *slog.Logger
	OnDelta      func(inventory.Delta)
	LocalNode    string
	PVEInterval  time.Duration
	HostInterval time.Duration
	LLDPInterval time.Duration
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
	host    host.Reader
	pve     *pve.Client
	graph   *inventory.Graph
	log     *slog.Logger
	onDelta func(inventory.Delta)
	status  map[string]*sourceState
	// seenNodes is the cluster membership observed by the previous
	// successful cluster-status poll (guarded by mu). pvePollAll compares
	// it against the current membership to retire departed nodes' entities
	// (see retireDepartedNodes).
	seenNodes    map[string]bool
	localNode    string
	pveInterval  time.Duration
	hostInterval time.Duration
	lldpInterval time.Duration
	mu           sync.Mutex
	statusMu     sync.Mutex
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
		graph:        cfg.Graph,
		log:          logger,
		pveInterval:  pveInterval,
		hostInterval: hostInterval,
		lldpInterval: lldpInterval,
		onDelta:      cfg.OnDelta,
		localNode:    cfg.LocalNode,
		status: map[string]*sourceState{
			"pve":  {},
			"host": {},
			"lldp": {},
		},
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

// consecutiveFailures reports the named loop's current failure streak.
func (c *Collector) consecutiveFailures(name string) int {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	if st := c.status[name]; st != nil {
		return st.consecutiveFailures
	}
	return 0
}

// SourceStatus is one poll loop's staleness/health snapshot.
type SourceStatus struct {
	LastSuccess         time.Time
	LastAttempt         time.Time
	Name                string
	LastError           string
	ConsecutiveFailures int
}

// Status is a point-in-time snapshot of every poll loop's staleness, keyed
// by loop name ("pve", "host", "lldp"). Deliverable 4: exposed so
// /api/v1/health (via a small adapter cmd/vnproxd wires in) can surface
// per-source staleness without this package knowing anything about HTTP or
// JSON shapes.
type Status struct {
	LocalNode string
	Sources   []SourceStatus
}

// Status returns a snapshot of every loop's current staleness/health.
func (c *Collector) Status() Status {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	names := []string{"pve", "host", "lldp"}
	out := make([]SourceStatus, 0, len(names))
	for _, name := range names {
		st := c.status[name]
		if st == nil {
			out = append(out, SourceStatus{Name: name})
			continue
		}
		s := SourceStatus{
			Name:                name,
			LastSuccess:         st.lastSuccess,
			LastAttempt:         st.lastAttempt,
			ConsecutiveFailures: st.consecutiveFailures,
		}
		if st.lastErr != nil {
			s.LastError = st.lastErr.Error()
		}
		out = append(out, s)
	}
	return Status{LocalNode: c.getLocalNode(), Sources: out}
}
