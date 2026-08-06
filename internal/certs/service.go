package certs

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultRefreshInterval is how often the inventory is rebuilt.
//
// Deliberately unhurried: certificates change on the order of months, and the
// scan reads a handful of small files from pmxcfs. The interval exists so a
// renewal is noticed without a restart, not so the view is real-time. It also
// bounds how long peer name resolution can be stale after a node's certificate
// is reissued — which is why it is minutes rather than hours.
const DefaultRefreshInterval = 5 * time.Minute

// ClusterFactsFunc supplies the cluster membership and peer dial addresses at
// scan time. Returning an error leaves the previous facts in place; a
// momentarily unavailable cluster status should not make every node's
// certificate look unverifiable.
type ClusterFactsFunc func(ctx context.Context) (ClusterFacts, error)

// ServiceOptions configures a Service.
type ServiceOptions struct {
	Logger *slog.Logger
	Now    func() time.Time
	Facts  ClusterFactsFunc
	// Root is the pmxcfs mount; empty means DefaultRoot.
	Root string
	// DaemonCertPath is this daemon's own serving certificate.
	DaemonCertPath string
	// LocalNode is this daemon's PVE node name. Empty means "resolve it from
	// the /etc/pve/local symlink".
	LocalNode string
	// ExpiryWarn is how far ahead cert_expiring looks; zero means
	// DefaultExpiryWarn.
	ExpiryWarn time.Duration
	// RefreshInterval; zero means DefaultRefreshInterval.
	RefreshInterval time.Duration
}

// Service keeps a current certificate inventory and the derived peer
// verification-name mapping.
//
// It is the seam between "what pmxcfs says about certificates" and the three
// consumers: the findings engine, the API/CLI views, and internal/peer's trust
// anchor. Safe for concurrent use.
type Service struct {
	now        func() time.Time
	log        *slog.Logger
	facts      ClusterFactsFunc
	names      map[string]string
	root       string
	daemonCert string
	localNode  string
	report     Report
	lastFacts  ClusterFacts
	expiryWarn time.Duration
	interval   time.Duration
	mu         sync.RWMutex
}

// NewService builds a Service and performs one synchronous scan, so the first
// peer request already has a name mapping and the startup preflight has
// something to report. A scan failure is logged, not fatal: a daemon that
// refused to start because it could not read /etc/pve would be strictly worse
// than one that starts and says so loudly.
func NewService(opts ServiceOptions) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	root := opts.Root
	if root == "" {
		root = DefaultRoot
	}
	interval := opts.RefreshInterval
	if interval <= 0 {
		interval = DefaultRefreshInterval
	}
	localNode := opts.LocalNode
	if localNode == "" {
		localNode = LocalNodeFromRoot(root)
	}
	s := &Service{
		log:        logger,
		now:        now,
		facts:      opts.Facts,
		root:       root,
		daemonCert: opts.DaemonCertPath,
		localNode:  localNode,
		expiryWarn: opts.ExpiryWarn,
		interval:   interval,
	}
	s.Refresh(context.Background())
	return s
}

// Refresh rebuilds the inventory, re-evaluates every check, and recomputes the
// verification-name mapping.
func (s *Service) Refresh(ctx context.Context) {
	facts := s.currentFacts(ctx)

	inv, err := Scan(Options{
		Root:           s.root,
		DaemonCertPath: s.daemonCert,
		LocalNode:      s.localNode,
		Now:            s.now,
	})
	if err != nil {
		s.log.Warn("certs: scanning the cluster certificate inventory failed", "root", s.root, "error", err)
		return
	}

	now := s.now()
	verify := func(leafPath string) error { return VerifyChain(s.root, leafPath, now) }
	issues := Evaluate(inv, facts, now, s.expiryWarn, verify)
	names := VerifyNames(inv, facts)

	s.mu.Lock()
	prev := s.report
	s.report = Report{Inventory: inv, Issues: issues}
	s.names = names
	s.lastFacts = facts
	s.mu.Unlock()

	s.logTransitions(prev, issues)
}

// logTransitions reports the count of blocking certificate problems when it
// changes, so a cluster that develops one says so in the log without the
// steady state reprinting every refresh.
func (s *Service) logTransitions(prev Report, issues []Issue) {
	countErrors := func(in []Issue) int {
		n := 0
		for _, i := range in {
			if i.Severity == SeverityError {
				n++
			}
		}
		return n
	}
	before, after := countErrors(prev.Issues), countErrors(issues)
	if before == after {
		return
	}
	if after == 0 {
		s.log.Info("certs: all cluster certificate problems have cleared")
		return
	}
	for _, i := range issues {
		if i.Severity == SeverityError {
			s.log.Warn("certs: "+i.Detail, "check", i.Check, "node", i.Node, "remediation", i.Remediation)
		}
	}
}

func (s *Service) currentFacts(ctx context.Context) ClusterFacts {
	if s.facts == nil {
		return ClusterFacts{}
	}
	facts, err := s.facts(ctx)
	if err != nil {
		s.mu.RLock()
		last := s.lastFacts
		s.mu.RUnlock()
		s.log.Debug("certs: cluster facts unavailable; reusing the last known set", "error", err)
		return last
	}
	return facts
}

// Run refreshes on a ticker until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Refresh(ctx)
		}
	}
}

// Report returns the current inventory and issues.
func (s *Service) Report() Report {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.report
}

// Issues returns just the current issues — the findings engine's seam.
func (s *Service) Issues() []Issue {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.report.Issues
}

// VerifyNameFor maps a peer's dial host to the identity its certificate should
// be verified as, or "" for "use the dial host". This is what is handed to
// internal/peer.Trust.
func (s *Service) VerifyNameFor(dialHost string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	name, ok := s.names[dialHost]
	if !ok || name == dialHost {
		return ""
	}
	return name
}

// Preflight logs the startup summary: what was found, and every blocking
// problem, named — before the first peer call rather than after it.
//
// This is the "it will fail later, for a knowable reason" report T-1906-bug-01
// asked for. A cluster whose certificates cannot authenticate its own peers
// says so at startup, in terms an operator can act on, instead of surfacing as
// an opaque handshake error under load later.
func (s *Service) Preflight() {
	rep := s.Report()
	s.log.Info("certs: cluster certificate inventory",
		"certificates", len(rep.Inventory.Certificates),
		"nodes", len(rep.Inventory.Nodes()),
		"cluster_ca", rep.Inventory.ClusterCA != nil,
		"issues", len(rep.Issues))

	for _, i := range rep.Issues {
		if i.Severity != SeverityError {
			continue
		}
		s.log.Error("certs: "+i.Detail, "check", i.Check, "node", i.Node, "remediation", i.Remediation)
	}

	s.mu.RLock()
	names := make(map[string]string, len(s.names))
	for k, v := range s.names {
		names[k] = v
	}
	s.mu.RUnlock()
	for addr, name := range names {
		if addr != name {
			s.log.Info("certs: this peer will be verified against a resolved name rather than its dial address",
				"dial_host", addr, "server_name", name)
		}
	}
}
