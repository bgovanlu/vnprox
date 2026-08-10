package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/certs"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/dhcp"
	"github.com/bgovanlu/vnprox/internal/docexport"
	"github.com/bgovanlu/vnprox/internal/evpn"
	"github.com/bgovanlu/vnprox/internal/federation"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/ha"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/hub"
	"github.com/bgovanlu/vnprox/internal/ingress"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/ipv6"
	"github.com/bgovanlu/vnprox/internal/k8s"
	"github.com/bgovanlu/vnprox/internal/metrics"
	"github.com/bgovanlu/vnprox/internal/neighbor"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/plugin"
	"github.com/bgovanlu/vnprox/internal/sdn"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/tenant"
	"github.com/bgovanlu/vnprox/internal/topology"
	webui "github.com/bgovanlu/vnprox/web"
)

// certPollInterval is the mtime-polling fallback interval for TLS
// cert/key hot-reload (see config.CertProvider.Watch's doc comment for why
// polling instead of fsnotify).
const certPollInterval = 30 * time.Second

// peerSecretPollInterval is the mtime-polling interval for the cluster
// secret (peer.SecretStore.Watch's doc comment: same "changes on the order
// of a cluster's lifetime" rationale as certPollInterval).
const peerSecretPollInterval = 30 * time.Second

// metricPruneInterval is how often the metric_samples prune loop enforces
// store.MetricRetention (24h, docs/data-model.md §2). Hourly keeps the
// table within ~4% of the retention window at negligible cost.
const metricPruneInterval = time.Hour

// capacityPruneInterval is how often the capacity_aggregates prune loop
// (T-1606) enforces [capacity] aggregate_retention_days. These are daily
// rollup rows on a ~13-month window, so a far coarser cadence than
// metricPruneInterval suffices — running a few times a day keeps the table
// trimmed without churn.
const capacityPruneInterval = 6 * time.Hour

// snapshotRetentionInterval is how often the snapshot retention job
// (T-206) enforces cfg.Retention's keep/pin-days policy. Snapshots accrue
// far more slowly than metric samples (one pre/post pair per apply, not a
// per-poll-interval row), so a coarser cadence than metricPruneInterval is
// plenty — this only needs to run at least once within any given day to
// keep the "keep N days" window accurate to within a day.
const snapshotRetentionInterval = 6 * time.Hour

// auditPruneInterval is how often the audit_log retention prune loop
// (T-1905) enforces [retention] audit_keep_days. Audit rows accrue on
// every mutation attempt, more often than snapshots but far less often
// than metric samples, and the window is measured in years, not hours — a
// daily cadence keeps the table within a day of the configured ceiling at
// negligible cost.
const auditPruneInterval = 24 * time.Hour

// compactionInterval is how often the store compaction loop (T-1905)
// reclaims a batch of freed pages via PRAGMA incremental_vacuum. Coarser
// than the retention loops that feed it freed space — compaction is
// housekeeping over whatever those already freed, not something that needs
// to race them. Mirrors store.DefaultCompactionInterval, named as its own
// constant here for the same reason every other interval in this file is:
// so cmd/vnproxd's own cadence choices are visible together, independent
// of internal/store's own default.
const compactionInterval = store.DefaultCompactionInterval

// shutdownGrace bounds how long the HTTP server's graceful Shutdown may
// take; it is a safety net, not the expected duration — acceptance
// criterion 3 requires the whole process to exit within 3s of SIGTERM even
// with an in-flight slow request, so any real handler must finish well
// inside this.
const shutdownGrace = 10 * time.Second

// distRootFS returns the embedded frontend build rooted at the site root
// (stripping the "dist" prefix embed.FS retains). See web/assets.go for why
// the embed lives in its own tiny package.
func distRootFS() (fs.FS, error) {
	sub, err := fs.Sub(webui.DistFS, "dist")
	if err != nil {
		return nil, fmt.Errorf("preparing embedded web/dist filesystem: %w", err)
	}
	return sub, nil
}

// runDaemon loads config, wires the HTTPS server + TLS cert watcher into a
// supervised run group, and blocks until ctx is cancelled (SIGINT/SIGTERM)
// or a subsystem fails.
func runDaemon(ctx context.Context, configPath string, logger *slog.Logger) error {
	cfg, err := config.Load(configPath, logger)
	if err != nil {
		return fmt.Errorf("loading config %s: %w", configPath, err)
	}

	certProvider, err := config.NewCertProvider(cfg.Server.TLSCertPath, cfg.Server.TLSKeyPath, logger)
	if err != nil {
		return fmt.Errorf("initializing TLS: %w", err)
	}

	// T-301: the cluster secret every /api/peer/* request is HMAC-verified
	// against (docs/architecture.md §5). Generated on first use if absent
	// (docs/deployment.md: "first node only; pmxcfs replicates it") —
	// fatal on any other load failure, the same treatment certProvider
	// above gets, since a daemon that can neither sign nor verify peer
	// requests cannot safely participate in cluster coordination.
	peerSecrets, err := peer.LoadOrGenerateSecret(cfg.Peer.SecretPath, logger)
	if err != nil {
		return fmt.Errorf("initializing peer cluster secret: %w", err)
	}

	// T-1906: the peer API's TLS trust anchor. One *peer.Trust per daemon,
	// shared by every peer client below, so there is exactly one trust
	// decision, one CA re-read cadence, and one startup banner per process.
	// Construction fails only on a config this daemon must not run with (an
	// unknown [peer] tls_trust, or an escape hatch without its exact
	// acknowledgement) — the same fatal treatment the cluster secret above
	// gets, and unreachable for a production node, which sets neither key.
	peerTrust, err := newPeerTrust(cfg, logger)
	if err != nil {
		return fmt.Errorf("initializing peer TLS trust: %w", err)
	}

	distFS, err := distRootFS()
	if err != nil {
		return err
	}

	// The store.DB, its AuditRepo, and its APITokenRepo are constructed
	// once, here, and reused everywhere below (router.Options.Audit,
	// ProbeAudit, LLDPAudit, TokenAudit, setupAuth's own login/logout/
	// token.use audit writes, ...) — T-1104's `audit.appended` event is
	// wired via a single SetOnAppend hook on this one auditRepo instance
	// (events.go's wireAuditAppendedEvents doc comment), so every audit
	// write in this daemon must go through it, not a second AuditRepo
	// wrapping the same table.
	if err = os.MkdirAll(filepath.Dir(cfg.Storage.DBPath), 0o750); err != nil {
		return fmt.Errorf("creating storage directory for %s: %w", cfg.Storage.DBPath, err)
	}
	// T-1901: advertise, for this process's whole lifetime, that this
	// daemon owns this store, so `vnproxctl restore` refuses to swap the
	// file out from under it. The lock is advisory and released by the
	// kernel however the process ends, so it cannot go stale and block a
	// later recovery.
	//
	// Deliberately NON-FATAL: this exists to make a restore safe, and must
	// not become a new way for the daemon to refuse to start. Failing to
	// take it (a read-only /var/lib, an exotic filesystem with no flock,
	// or genuinely a second daemon on the same store — already a bug
	// today, and one this does not newly enforce) logs a warning and
	// carries on exactly as before this landed.
	if runtimeLock, lockErr := store.AcquireRuntimeLock(cfg.Storage.DBPath); lockErr != nil {
		logger.Warn("could not take the store runtime lock; `vnproxctl restore` will fall back to its listen-address probe to detect this daemon",
			"path", store.RuntimeLockPath(cfg.Storage.DBPath), "error", lockErr)
	} else {
		defer func() { _ = runtimeLock.Release() }()
	}

	db, err := store.Open(ctx, cfg.Storage.DBPath)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer func() { _ = db.Close() }()

	// T-1903: the daemon's own self-observability registry — HTTP RED,
	// collector poll duration/counters, change-engine outcomes, store
	// query duration, and peer-RPC duration/counters all funnel through
	// this one *metrics.Registry, rendered by GET /metrics alongside the
	// existing cluster-derived families (docs/features/monitoring.md §9).
	// Wired to db immediately so every store call from here on (including
	// ones made during the rest of this function's setup) is observed.
	selfMetrics := metrics.NewRegistry(logger)
	db.SetQueryObserver(func(op string, dur time.Duration, _ error) {
		selfMetrics.ObserveStoreQuery(op, dur)
	})

	// T-1905: one-time conversion to SQLite incremental auto-vacuum mode,
	// so RunCompactionLoop (registered with the run group below) has a free
	// list to reclaim from. Deliberately run here, before the daemon starts
	// serving (the same timing class as the schema migrations store.Open
	// already ran) rather than from a periodic loop against a live store —
	// see internal/store/compact.go's package doc comment for why: a store
	// already converted (every store this function has ever run against
	// before, and any store created fresh) is a fast no-op; an EXISTING
	// pre-T-1905 store pays a one-time full-VACUUM cost proportional to its
	// size on this first startup after upgrading — logged here so it is
	// not a silent stall, and documented in docs/deployment.md's sizing
	// section so it is not a surprise.
	if vacConverted, vacTook, vacErr := store.EnsureIncrementalVacuum(ctx, db); vacErr != nil {
		logger.Warn("store: could not enable incremental auto-vacuum; periodic compaction will stay a no-op until this succeeds", "error", vacErr)
	} else if vacConverted {
		logger.Info("store: enabled incremental auto-vacuum (one-time compaction)", "took", vacTook)
	}

	auditRepo := store.NewAuditRepo(db)
	apiTokenRepo := store.NewAPITokenRepo(db)
	webhookRepo := store.NewWebhookRepo(db)

	authSvc, sessionCipher, err := setupAuth(cfg, logger, db, auditRepo, apiTokenRepo)
	if err != nil {
		return fmt.Errorf("initializing auth: %w", err)
	}

	graph := inventory.NewGraph()
	topoSvc := topology.NewService(graph, logger)
	// T-1102: the pinned-spec table (the GitOps reconciler's declared
	// desired state) — constructed here, ahead of driftSvc below, so its
	// spec_drift check family can read it every cycle via specPinAdapter.
	// Also reused verbatim as the router's api.Options.SpecPin seam below
	// (GET/POST/DELETE /spec/pin), the same "one repo, two seams" pattern
	// simDivergenceRepo/auditRepo already establish elsewhere in this file.
	pinnedSpecRepo := store.NewPinnedSpecRepo(db)
	// T-1104: audit.appended broadcasts over the same shared WS hub
	// topoSvc's Broadcast already backs for topology.delta/changeset.status/
	// drift.changed/findings.changed — wired against the one shared
	// auditRepo instance (see this file's construction-order doc comment
	// above). The webhook Dispatcher is wired as that hub's event sink
	// right alongside, so both the WS "events" topic and registered
	// webhook targets are fed from the exact same fan-in point (hub.go's
	// eventsSourceTopics doc comment).
	wireAuditAppendedEvents(auditRepo, topoSvc, logger)
	setupAutomation(webhookRepo, sessionCipher, topoSvc, logger)
	// T-305: the drift detector runs its own 30s cycle over the same live
	// graph the collectors populate, independent of any one poll loop
	// (docs/features/topology.md §6); its findings changing broadcasts
	// `drift.changed` over the same shared WS hub topoSvc's Broadcast
	// already backs for internal/change's `changeset.status` events.
	driftSvc := setupDrift(graph, topoSvc, specPinAdapter{repo: pinnedSpecRepo, logger: logger}, logger)

	// T-601: the metrics sampler is constructed before setupCollect so its
	// Ingest method can be wired in as collect.Config.OnStats (the host
	// loop's per-tick counter hook) — it persists a 24h, 30s-downsampled
	// counter ring via the same metric_samples repo the hourly prune loop
	// below enforces retention on, and pushes docs/api.md's `metrics.sample`
	// WS event over topoSvc's shared hub, exactly like driftSvc's
	// `drift.changed` above and changeSvc's `changeset.status` below.
	metricSamples := store.NewMetricSampleRepo(db)
	metricsSampler := metrics.New(metrics.Config{
		Store:  metricSamples,
		WS:     topoSvc,
		Logger: logger,
	})

	// T-1001: the Prometheus scrape token GET /metrics is gated on
	// (docs/security.md's Authentication section) — loaded now, ahead of
	// findingsEngine/driftSvc/changeSvc below, purely so it can sit next to
	// metricsSampler's own construction; it isn't consumed until
	// api.NewRouter is called far below. A nil/empty token (cfg.Metrics.Enabled
	// == false) means api.Options.MetricsExporter simply never mounts
	// GET /metrics.
	metricsToken, err := setupMetricsExporterToken(cfg, logger)
	if err != nil {
		return fmt.Errorf("initializing metrics exporter: %w", err)
	}

	// T-1107: the daemon's own Ed25519 bundle-signing identity (generated at
	// first use, docs/features/blueprints.md §5) — loaded here alongside the
	// metrics token above for the same reason, not consumed until
	// api.NewRouter/blueprintSvc are constructed far below.
	blueprintSigningKey, err := setupBlueprintSigningKey(cfg, logger)
	if err != nil {
		return fmt.Errorf("initializing blueprint signing key: %w", err)
	}
	blueprintTrust := blueprint.NewTrustStore(cfg.Blueprint.TrustedSignersDir)

	// T-1705: the opt-in Blueprint & plugin hub client. Constructed only when a
	// registry URL is configured ([hub] registry_url) — an empty URL leaves
	// hubClient nil, which skips mounting the hub routes entirely (the hub is
	// off by default). A malformed URL is logged and the hub stays off rather
	// than failing daemon startup. The vetted-signer set drives only the
	// informational badge; it never gates an install.
	var hubClient *hub.Client
	if regURL := cfg.Hub.RegistryURL; regURL != "" {
		hc, herr := hub.NewClient(regURL)
		if herr != nil {
			logger.Warn("blueprint & plugin hub disabled: invalid registry URL", "url", regURL, "err", herr)
		} else {
			hubClient = hc
		}
	}
	hubVetted := hub.NewVettedSet(cfg.Hub.VettedSigners)

	// T-602: the unified findings engine's IngestServices is wired in as
	// collect.Config.OnServices below (the same "piggyback on the host
	// loop's existing per-tick hook" pattern OnStats already established
	// for metricsSampler) — but the engine itself needs graph/driftSvc/
	// topoSvc/metricsSampler/a notifier, none of which exist until after
	// setupCollect returns the PVE client the notifier reuses, so
	// findingsEngine is constructed just below setupCollect and its
	// IngestServices method is closed over here for setupCollect's benefit.
	var findingsEngine *findings.Engine
	onServices := func(node string, status map[string]bool) {
		if findingsEngine != nil {
			findingsEngine.IngestServices(node, status)
		}
	}

	// T-303: peerClient (built from the same PVE client the collectors use
	// for cluster-status-based discovery) is nil exactly when collectErr is
	// non-nil — see setupCollect's doc comment — so every peerClient use
	// below is already guarded by the same "collectors initialized OK"
	// nil-safety collector's own uses need.
	collector, peerClient, sdnPVEClient, collectErr := setupCollect(cfg, graph, logger, topoSvc.OnDelta, metricsSampler.Ingest, onServices, peerSecrets, peerTrust, selfMetrics)
	if collectErr != nil {
		logger.Error("collect: failed to initialize PVE/host collectors; starting without live inventory polling or cluster fan-out", "error", collectErr)
	}
	// T-301/T-304/T-406: the peer server (below, peerSrv) and the local
	// node's own T-406 DHCP-lease read both need a host.Reader over real
	// netlink/interfaces(5)/lldpd/dnsmasq state — host.NewReal() has no
	// dependencies of its own, so it's built here (rather than down by
	// peerSrv, where it used to live) so dhcpSvc below can use the same
	// instance too.
	realHost := host.NewReal()
	// localNode is a closure over collector (T-303's own doc comment
	// explains why: collector may not have completed a poll cycle yet at
	// startup) — built here, ahead of dhcpSvc/ipamSvc below, so both can
	// close over the same func value; clusterNodes/clusterTimers further
	// down reuse this identical variable.
	localNode := func() string {
		if collector == nil {
			return ""
		}
		return collector.Status().LocalNode
	}
	// T-401: GET /sdn reads PVE directly and live (internal/sdn.Service's
	// doc comment) via the same read-only client the collectors use — nil
	// exactly when collectErr is non-nil, mirroring peerClient's own
	// nil-safety above.
	var sdnSvc *sdn.Service
	// sdnDNSSvc backs T-1204's GET /sdn/dns. Nil-typed-interface until
	// assigned, the same degraded-mode pattern ipamSvc/dhcpAPISvc use below,
	// so mountSDNDNSRoutes' `svc == nil` check gets a true nil interface.
	var sdnDNSSvc api.SDNDNSService
	if sdnPVEClient != nil {
		sdnSvc = sdn.NewService(sdnPVEClient)
		sdnDNSSvc = sdn.NewDNSService(sdnPVEClient)
	}
	// T-1206: PBS network awareness — reads PVE's own storage.cfg + backup
	// jobs once (sdnPVEClient, already available), re-projected against the
	// live graph on each /topology or /pbs read (see pbswire.go). Always
	// constructed; degrades to an empty overlay when sdnPVEClient is nil or
	// no PBS storage is configured.
	pbsAdapter := setupPBS(ctx, sdnPVEClient, graph, logger)
	// T-406: internal/dhcp.Service fans DHCP-lease reads across the
	// cluster (local node via realHost, every peer via peerClient) into
	// ipam.Observation values — always constructed (mirrors evpnSvc's own
	// unconditional construction below: it tolerates a nil Peers/empty
	// LocalNode internally, so there's no typed-nil risk assigning it
	// directly into ipam.Config.Leases, an interface field).
	var dhcpPeers dhcp.PeerSource
	if peerClient != nil {
		dhcpPeers = peerClient
	}
	dhcpSvc := dhcp.NewService(dhcp.Config{Host: realHost, Peers: dhcpPeers, LocalNode: localNode, Logger: logger})

	// T-805: internal/neighbor.Service fans ARP/IPv6-neighbor reads across
	// the cluster (local node via realHost, every peer via peerClient) into
	// ipam.Observation{Source: "neighbor"} values — the same
	// always-construct, tolerate-nil-Peers shape dhcpSvc above uses, wired
	// into ipam.Config.Neighbors below (the interface point T-405 reserved
	// for it, docs/features/ipam.md §1's "known gap" this task closes).
	var neighborPeers neighbor.PeerSource
	if peerClient != nil {
		neighborPeers = peerClient
	}
	neighborSvc := neighbor.NewService(neighbor.Config{Host: realHost, Peers: neighborPeers, LocalNode: localNode, Logger: logger})

	// T-405/T-406: GET /ipam/subnets(/{cidr}/allocations) and GET /sdn/dhcp
	// read PVE's IPAM plugin(s) directly and live, for the same "never
	// stale relative to what a reserve/release apply just changed" reason
	// sdnSvc above does — plus the cluster's own inventory graph
	// (guest/bridge data for the guest-agent enrichment source and
	// detected non-SDN subnets) and dhcpSvc above (T-406's lease
	// enrichment source, wired into exactly the interface point T-405 left
	// open for it — ipam.Config.Leases).
	//
	// ipamConcrete is kept as a second, concrete-typed handle (alongside
	// the interface-typed ipamSvc below) purely so cmd/vnproxd's own
	// dhcpAllocationsAdapter (changeagent.go) can call its exported
	// AllAllocations method for T-406's DHCP-range-overlap advisory check
	// — ipamSvc itself (declared as the api.IPAMService interface, not a
	// concrete *ipam.Service) only ever assigned inside the
	// sdnPVEClient != nil branch, so an unset case is a true nil
	// interface — the safe pattern peerAudit/peerSnapshots below already
	// use, avoiding the "non-nil interface wrapping a typed nil pointer"
	// footgun a bare `var ipamSvc *ipam.Service` would risk at
	// Options{IPAM: ipamSvc} (mountIPAMRoutes' `svc == nil` degraded-mode
	// check needs a literal nil interface to work).
	var ipamConcrete *ipam.Service
	var ipamSvc api.IPAMService
	// dhcpAPISvc is the same concrete *ipam.Service value as ipamSvc, typed
	// as api.DHCPService (docs/api.md's GET /sdn/dhcp seam) — api.IPAMService
	// doesn't declare the DHCP method, so ipamSvc's own interface-typed
	// variable can't be assigned directly to a DHCPService-typed field; a
	// second interface-typed variable, assigned from the same nil check, is
	// the same "true nil interface until assigned" pattern as ipamSvc
	// itself.
	var dhcpAPISvc api.DHCPService
	// guestInteriorIPAM is the same concrete *ipam.Service value as ipamSvc,
	// typed as api.GuestInteriorIPAMSource (T-1304's IPAM cross-check
	// annotation) — same true-nil-interface-until-assigned pattern as
	// dhcpAPISvc above.
	var guestInteriorIPAM api.GuestInteriorIPAMSource
	// conntrackGuests backs GET /conntrack's `guest=` filter (T-1305):
	// ipamConcrete.GuestIPs resolves a guest ref to the IPs vnprox has
	// evidence for, the same enrichment-observation source ipamSvc's own
	// subnet merge reads. Same true-nil-interface-until-assigned pattern as
	// ipamSvc/dhcpAPISvc above — api.IPAMService doesn't declare GuestIPs,
	// so this needs its own interface-typed variable.
	var conntrackGuests api.ConntrackGuestResolver
	// edgeIPAM (T-1403) is the same concrete *ipam.Service value as
	// ipamSvc, typed as api.EdgeIPAMSource (the Edge & NAT cockpit's
	// port-forward -> guest correlation) — same true-nil-interface-until-
	// assigned pattern as guestInteriorIPAM/conntrackGuests above.
	var edgeIPAM api.EdgeIPAMSource
	// k8sIPAMSrc is the same concrete *ipam.Service value as ipamSvc, typed
	// as api.K8sIPAMSource (T-1501's node<->guest correlation seam,
	// AllAllocations) — the identical "true nil interface until assigned"
	// pattern as ipamSvc/dhcpAPISvc above, assigned in the same branch.
	var k8sIPAMSrc api.K8sIPAMSource
	// ipamExternalSvc is the same concrete *ipam.Service, typed as
	// api.IPAMExternalService (T-1203's external-subnet CRUD + NetBox/phpIPAM
	// bidirectional-sync seam) — assigned in the same branch, same "true nil
	// interface until assigned" pattern as the seams above.
	var ipamExternalSvc api.IPAMExternalService
	if sdnPVEClient != nil {
		ipamConcrete = ipam.NewService(ipam.Config{
			PVE: sdnPVEClient, Inventory: graph, Leases: dhcpSvc, Neighbors: neighborSvc,
			// T-1203: the app-owned external-subnet store. ExternalIPAM (the
			// concrete NetBox/phpIPAM write client) is deliberately left nil —
			// the sync engine + preview/apply/audit ceremony are complete and
			// tested against internal/ipam's HTTP double, but a production
			// client keyed to real NetBox/phpIPAM API shapes needs hardware
			// validation (see planning/reports/needs-hardware-validation.md),
			// so sync routes report "not configured" until that lands.
			External: store.NewExternalSubnetRepo(db),
		})
		ipamSvc = ipamConcrete
		dhcpAPISvc = ipamConcrete
		guestInteriorIPAM = ipamConcrete
		conntrackGuests = ipamConcrete
		edgeIPAM = ipamConcrete
		k8sIPAMSrc = ipamConcrete
		ipamExternalSvc = ipamConcrete
	}
	// changeAllocations adapts ipamConcrete into change.AllocationsSource
	// for T-406's DHCP-range-overlap advisory check — see
	// dhcpAllocationsAdapter's doc comment in changeagent.go for why
	// internal/change never imports internal/ipam directly. Same
	// true-nil-interface-until-assigned pattern as ipamSvc above.
	var changeAllocations change.AllocationsSource
	if ipamConcrete != nil {
		changeAllocations = dhcpAllocationsAdapter{ipam: ipamConcrete}
	}

	// T-602: one findings stream unifying drift (T-305), the LLDP VLAN
	// cross-check (T-302), and this task's own health checks — composed
	// over the same live graph/metrics substrate every other read path
	// shares (docs/architecture.md §2/§3). The notifier reuses sdnPVEClient
	// (the collectors' read-only PVE identity) rather than building a
	// third client.
	//
	// T-702: mgmtStatusAdapter is wired in now (findings.Engine is
	// constructed before change.Service exists, below) and filled in with
	// its real target once changeSvc is built — see the adapter's own doc
	// comment (findings.go) for why.
	mgmtAdapter := &mgmtStatusAdapter{}
	// T-803: corosyncStatusAdapter reuses realHost/localNode (already built
	// above for dhcpSvc/neighborSvc) — local-node-only for now, see its own
	// doc comment (findings.go) for the documented cluster-fan-out gap.
	corosyncAdapter := corosyncStatusAdapter{baseCtx: ctx, host: realHost, localNode: localNode, logger: logger}
	// T-1006: fwAnalyticsAdapter is wired in now (findings.Engine is
	// constructed before *fwlog.Service exists, below) and filled in with
	// its real target once fwlogSvc is built — see the adapter's own doc
	// comment (findings.go) for why, mirroring mgmtAdapter above.
	fwAnalyticsAdapterVal := &fwAnalyticsAdapter{}
	// T-1103: scheduleAdapter is wired in now (findings.Engine is
	// constructed before change.Service exists, below) and filled in with
	// its real target once changeSvc is built — mirrors mgmtAdapter above.
	scheduleAdapter := &scheduleMissedAdapter{}
	// T-1704: the HA leader guard (fences change.Service's unattended
	// apply/confirm/rollback timers) and the ha_replication_degraded findings
	// provider are both late-bound — the ha.Manager is constructed after both
	// change.Service and the findings engine. Until set (or when HA is
	// disabled) the guard reports leader=true, so single-daemon behaviour is
	// unchanged.
	haGuard := &haLeaderGuard{}
	haFindAdapter := &haFindingsAdapter{}
	// T-1504: flowClassifier is built now (it only needs corosync.conf,
	// already readable) and registered into api.Options.FlowClassifier
	// below; flowClassifyAdapterVal is wired in now (findings.Engine is
	// constructed before setupFlows builds flowRepo, below) and filled in
	// with both once setupFlows returns — mirrors scheduleAdapter above.
	// See serviceclassify.go's doc comment for the full picture.
	flowClassifier := setupFlowClassifier(logger)
	flowClassifyAdapterVal := &flowClassifyAdapter{}
	// T-1503: Ceph network awareness — reads PVE's own Ceph public/cluster
	// network declaration + OSD placement once (sdnPVEClient, already
	// available above), registers the two CIDRs with flowClassifier
	// (T-1503 supplies T-1504's classifier, per classify.go's doc comment),
	// and returns the CephProvider findings.Config.Ceph wires in below —
	// see cephwire.go's own doc comment for the full picture.
	cephAdapter := setupCeph(ctx, sdnPVEClient, graph, flowClassifier, logger)
	// T-1604: failure-impact simulator adapter — reuses cephAdapter's already-
	// read corosync/Ceph side-tables plus the live graph. Its changeset seam
	// is filled once changeSvc is built (mirrors scheduleAdapter below).
	failsimSvc := newFailsimAdapter(graph, cephAdapter)
	// T-1005: alert_rules/alert_deliveries repos + the webhook Notifier,
	// composed alongside PVE's own notification-target hook via
	// multiNotifier — independent delivery paths, per that task's card, not
	// a replacement for pvenotify.go. AlertSecretCipher is the same
	// session-secret cipher sessions.pve_ticket_enc uses (setupAuth's doc
	// comment).
	alertRuleRepo := store.NewAlertRuleRepo(db)
	alertDeliveryRepo := store.NewAlertDeliveryRepo(db)
	// T-2407: the durable deferral queue behind quiet hours and digest
	// coalescing. A table rather than memory, so an eight-hour quiet window
	// survives a restart — see 0036_alert_quiet_hours.sql.
	alertPendingRepo := store.NewAlertPendingRepo(db)
	webhookNotifier := setupAlertWebhookNotifier(alertRuleRepo, alertDeliveryRepo, alertPendingRepo, sessionCipher, logger)
	// T-1007: finding_events, populated from this exact Notifier fan-out
	// (evaluateNotifications' existing transition detection, reused rather
	// than duplicated) — see findings.go's setupFindingEventsNotifier doc
	// comment. Constructed here (not down by auditRepo below) so it can
	// join the same multiNotifier call.
	findingEventRepo := store.NewFindingEventRepo(db)
	findingEventsNotifier := setupFindingEventsNotifier(findingEventRepo, logger)
	findingsNotifier := newMultiNotifier(setupFindingsNotifier(sdnPVEClient, logger), webhookNotifier, findingEventsNotifier)
	// T-806: the persisted sim_divergence finding store, created here (db
	// is available; findingsEngine is built next) and reused verbatim as
	// the router's api.Options.SimDivergence write-side seam below.
	simDivergenceRepo := store.NewSimDivergenceRepo(db)
	// T-1303: the latency & loss mesh — constructed here (db/graph/
	// localNode are all available) so its *latmesh.Service satisfies
	// findings.Config.LatMesh directly below and api.Options.LatMesh
	// further down (setupLatMesh's own doc comment); latMeshActors is
	// registered with the run group alongside every other owned goroutine,
	// below. latMeshDiscoverer is the same *latmesh.GraphDiscoverer instance
	// T-1306's setupMTUProbe reuses just below, rather than building a
	// second, functionally-identical one.
	latMeshSvc, latMeshDiscoverer, latMeshActors := setupLatMesh(cfg, db, graph, localNode, logger)
	// T-1507: the migration network planner (purely advisory — see
	// internal/migration's own doc.go). Constructed here since graph/
	// sdnPVEClient/latMeshSvc are all already available; migrationTrafficVal
	// is filled in with its real flow_samples target once setupFlows
	// returns, below — mirrors flowClassifyAdapterVal's identical
	// two-step wiring just above.
	migrationPlanner, migrationTrafficVal := setupMigrationPlanner(graph, sdnPVEClient, latMeshSvc)
	// T-1306: the path MTU prober — built on latMeshDiscoverer directly
	// (internal/mtuprobe's own package doc comment: "reuses T-1303's
	// infrastructure, does not duplicate it"), on its own coarser interval.
	// Its Service satisfies findings.Config.MTU (vxlan_underlay_mtu's
	// measured upgrade) directly below and api.Options.MTUProbe further
	// down; mtuProbeActors joins the same run group.
	mtuProbeSvc, mtuProbeActors := setupMTUProbe(cfg, latMeshDiscoverer, logger)
	// T-1401: WireGuard. The read service (store config + live wg-show-dump
	// status) backs both the findings engine's wg_handshake_stale/
	// wg_endpoint_drift checks and the api.Options.WireGuard read routes; the
	// on-node gateway (built near the change engine below) executes the wg.*
	// changeset ops.
	wgRepo := store.NewWireGuardRepo(db)
	wgReadSvc := newWireGuardReadService(wgRepo, localNode, logger)
	// T-1406: ingress visibility — the operator-configured reverse-proxy
	// target list, read by api.Options.IngressTargets/GET /ingress/status
	// below.
	ingressTargetRepo := store.NewIngressTargetRepo(db)
	// T-1405: WAN & upstream health — a node-local scheduler reusing
	// *latmesh.Service itself (setupWan's own doc comment), probing this
	// node's own operator-configured reference targets. Its Service
	// satisfies findings.Config.Wan (wan_degraded) directly below and
	// api.Options.Wan (GET /wan/status, GET/PUT /wan/targets) further down;
	// wanActors joins the same run group. wanLossWarnPct keeps the
	// findings check's own threshold in sync with the Service's per-uplink
	// "degraded" verdict rather than letting the two silently drift apart.
	wanSvc, wanLossWarnPct, wanActors := setupWan(cfg, db, localNode, logger)
	wanThresholds := findings.DefaultThresholds
	wanThresholds.WanLossWarnPct = wanLossWarnPct
	// T-1905: [retention] store_warn_bytes threaded into the same shared
	// HealthThresholds passed to setupFindings below, mirroring
	// wanLossWarnPct's identical wiring immediately above.
	wanThresholds.StoreCapacityWarnBytes = cfg.Retention.StoreWarnBytes
	// T-1501: the read-only Kubernetes overlay engine — the cluster registry
	// (app-owned kubeconfig targets) and the poller whose cached
	// NodePort-exposure findings feed the findings engine below and whose
	// overlay reads back the api.Options.K8sPoller routes further down.
	k8sClusterRepo := store.NewK8sClusterRepo(db)
	k8sPoller := k8s.NewPoller()
	// T-1605: rogue-service detection feed (cluster-wide neighbor table via
	// neighborSvc) plus the operator-flagged [security] protected_segments list.
	rogueAdapter := rogueScanAdapter{baseCtx: ctx, neighbors: neighborSvc, logger: logger}
	// T-1606: capacity forecasting — the daily rollup job, the aggregate prune
	// loop, the forecast findings provider, and the retention-bounded export
	// service. Built here so its provider can feed the findings engine below;
	// its rollup/prune actors are registered in the run group further down.
	capacityRollupActor, capacityPruneActor, capacityProvider, capacityExportSvc := setupCapacity(ctx, cfg, db, graph, metricSamples, ipamConcrete, logger)
	// T-1601: flow baselining. The profiles repo backs the scheduled learn job
	// + retention prune loop (registered with the run group below);
	// baselineSvcVal is the findings.BaselineProvider whose RecentAnomalies()
	// runs internal/baseline.Detect each findings cycle — wired in now (the
	// findings engine is built before setupFlows creates flowRepo) and pointed
	// at the real recent-samples source via set() once setupFlows returns,
	// mirroring flowClassifyAdapterVal's identical two-step wiring above.
	baselineProfileRepo := store.NewBaselineProfileRepo(db)
	baselineSvcVal := newBaselineService(baselineProfileRepo, cfg.Baseline, logger)
	// T-1602: microsegmentation planner adapter — reads the guest's observed
	// flow corpus, its learned baseline (baselineProfileRepo, for anomaly
	// exclusion), and its live firewall view. Its flow_samples source is
	// late-bound via set() once setupFlows returns (mirrors baselineSvcVal).
	microsegSvc := newMicrosegAdapter(graph, baselineProfileRepo)
	// T-1407: federationTunnelAdapter is wired in with its targets unset —
	// federation.NewService/NewAggregator are constructed further below,
	// after the findings engine — and filled in via set() once they exist
	// (fedTunnelAdapter.set below), mirroring baselineSvcVal/
	// flowClassifyAdapterVal's identical two-step wiring in this function.
	fedTunnelAdapter := &federationTunnelAdapter{baseCtx: ctx}
	// T-1906: same two-step wiring — the coordinator's peer client is built
	// further below, so the peer-TLS-posture adapter is passed in unset and
	// pointed at that client via set() once it exists.
	peerTrustAdapterVal := &peerTrustAdapter{}
	// T-1905: storeCapacityAdapter reports the app store's own on-disk size
	// (SizeBytes, T-1903's existing source) for store_near_capacity.
	storeCapacitySvc := storeCapacityAdapter{db: db, localNode: localNode}

	// T-2301..T-2303: the cluster certificate inventory. Reads pmxcfs, which
	// already holds every node's certificate locally, so this needs no peer
	// fan-out — and is therefore available precisely when peers are
	// unreachable, which is when a certificate problem is the likely cause.
	//
	// Wiring order matters: the service scans once during construction, so
	// attachCertVerifyNames below hands peerTrust a usable mapping before the
	// first peer request, and Preflight reports any blocking problem at
	// startup rather than letting it surface as an opaque handshake error
	// later (T-1906-bug-01's "warn before the first peer call" requirement).
	certSvc := certs.NewService(certs.ServiceOptions{
		Logger:         logger.With("component", "certs"),
		Facts:          certClusterFacts(sdnPVEClient),
		Root:           cfg.Certs.Root,
		DaemonCertPath: cfg.Server.TLSCertPath,
		LocalNode:      localNode(),
		ExpiryWarn:     cfg.Certs.ExpiryWarn(),
	})
	attachCertVerifyNames(peerTrust, certSvc)
	certSvc.Preflight()
	go certSvc.Run(ctx)
	certFindings := certFindingsAdapter{svc: certSvc}
	findingsEngine = setupFindings(ctx, graph, driftSvc, topoSvc, metricsSampler, mgmtAdapter, corosyncAdapter, fwAnalyticsAdapterVal, scheduleAdapter, latMeshSvc, mtuProbeSvc, wgReadSvc, wanSvc, flowClassifyAdapterVal, k8sPoller, cephAdapter, rogueAdapter, cfg.Security.ProtectedSegments, capacityProvider, baselineSvcVal, fedTunnelAdapter, peerTrustAdapterVal, storeCapacitySvc, certFindings, webhookRepo, findingsNotifier, topoSvc, ipamConcrete, simDivergenceRepo, wanThresholds, haFindAdapter, logger)

	// T-605: the config documentation export (docs/features/blueprints.md
	// §4) reads the exact same live sources the rest of this file's read
	// routes already do — the shared inventory graph, sdnSvc (nil-safe,
	// mirroring GET /sdn's own degraded-mode treatment above), and topoSvc
	// for both the topology/SVG section and the LLDP ports table.
	var docExportSDN docexport.SDNSource
	if sdnSvc != nil {
		docExportSDN = sdnSvc
	}
	docExportSvc := &docexport.Service{
		Inventory: graph,
		SDN:       docExportSDN,
		Ports:     topoSvc,
		Topo:      topoSvc,
	}

	// T-1607: the network posture score & report — a scheduled computation job
	// folding failsimSvc's SPOF inventory, findingsEngine's baseline-anomaly /
	// drift finding counts, baselineProfileRepo's learned-profile count (the
	// cold-start honesty signal), and the graph's own resolved firewall view
	// (segmentation + exposed ports) into one explainable score, persisted to
	// posture_scores. Its compute/prune actors are registered in the run group
	// below; postureRead backs GET /posture, /posture/history, /export/posture.
	postureComputeActor, posturePruneActor, postureRead := setupPosture(graph, failsimSvc, findingsEngine, baselineProfileRepo, db, logger)

	// changeSvc reuses topoSvc's WS hub for changeset.status broadcasts
	// (docs/api.md's WebSocket section documents one shared /api/ws
	// connection multiplexing "topology"/"changesets"/... topics alike —
	// see topology.Service.Broadcast and internal/change.Broadcaster), and
	// validates against the same live *inventory.Graph collect populates
	// (T-202: Service.Validate/Create/UpdateDraft snapshot it read-only —
	// this package never polls or mutates inventory itself).
	// The apply engine (T-205): the host writer for interfaces(5) files, the
	// pre/post snapshot store, and the collector as the post-terminal
	// inventory refresher. Refresher is nil-safe (collector may be nil).
	var refresher change.InventoryRefresher
	if collector != nil {
		refresher = collector
	}
	// The host writer: the real /etc/network/interfaces agent by default, or
	// — when [safety] dev_interfaces_dir is set (dev.toml does; production
	// configs never do) — a sandboxed agent that can only touch files under
	// that directory and never execs a real ifreload (audit-phase-2 F-22:
	// `make dev` used to wire the production agent, leaving the developer's
	// machine one authenticated POST away from a real ifreload).
	//
	// nodeAgent's static type is the concrete *hostNodeAgent (not the
	// change.NodeAgent interface) so the same instance also backs T-301's
	// peer.HostWriter seam below: they mutate the very same
	// /etc/network/interfaces(.new) files, so they must share its
	// instance-level mutex, not each hold their own — two separate
	// instances would let a peer-API-triggered write and a local changeset
	// apply race on disk with no mutual exclusion at all.
	nodeAgent := newHostNodeAgent(logger)
	if dir := cfg.Safety.DevInterfacesDir; dir != "" {
		devAgent, devErr := newDevNodeAgent(dir, logger)
		if devErr != nil {
			return fmt.Errorf("initializing dev interfaces sandbox: %w", devErr)
		}
		logger.Warn("change: DEV MODE host writer — interfaces file operations are sandboxed and ifreload is a no-op", "dir", dir)
		nodeAgent = devAgent
	}
	snapshotRepo := store.NewSnapshotRepo(db)
	blobRepo := store.NewBlobRepo(db)
	// auditRepo/apiTokenRepo/webhookRepo were constructed once, up above
	// (alongside setupAuth), and are reused here.
	// T-1304: the per-guest guest-interior-inspector opt-in preference
	// (app-owned UI state, off by default per guest).
	guestInteriorToggleRepo := store.NewGuestInteriorToggleRepo(db)

	// T-1505: QoS shape storage + the node-local tc/HTB gateway, mirroring
	// nodeAgent's own real-vs-dev-sandbox split immediately above (a
	// `make dev` daemon must never exec real tc any more than it may
	// rewrite /etc/network/interfaces). localNode is the same closure
	// every other node-scoped gateway below reuses (see its own doc
	// comment further down this function).
	qosShapeRepo := store.NewQosShapeRepo(db)
	var qosGateway *hostQosGateway
	if cfg.Safety.DevInterfacesDir != "" {
		qosGateway = newDevQosGateway(qosShapeRepo, localNode, logger)
	} else {
		qosGateway = newHostQosGateway(qosShapeRepo, localNode, logger)
	}
	qosReadSvc := newQosReadService(qosShapeRepo)

	// T-1201: the federation core — the cluster registry (credential sealed
	// at rest with the same session cipher every other secret column uses)
	// plus the read aggregator. With zero clusters attached (a single-cluster
	// deployment), the registry is empty and the aggregator's membership
	// resolver returns an empty map, so wiring it in is inert — federation is
	// additive, invisible until a cluster is actually attached.
	federationSvc, err := federation.NewService(federation.Config{
		Clusters: store.NewClusterRepo(db),
		Cipher:   sessionCipher,
		Logger:   logger,
		// Resolves a cluster's effective tunnel linkage from the peer-level
		// wireguard_peers.cluster_id annotation whenever no explicit
		// clusters.wg_tunnel_id override is stored, so the connect-clusters
		// wizard's tagged peer links the cluster without a second write path
		// into the clusters table (federation.TunnelLinker's doc comment).
		TunnelLinker: wgRepo,
	})
	if err != nil {
		return fmt.Errorf("initializing federation service: %w", err)
	}
	// T-1407: point the tunnel adapter at the now-built federation service +
	// WireGuard store repo, then hand it to the Aggregator as its
	// TunnelHealth seam — a cluster whose linked tunnel is down is excluded
	// from every aggregate read below rather than counted as an ordinary
	// unreachable cluster (see federationtunnel.go's doc comment).
	fedTunnelAdapter.set(federationSvc, wgRepo, wgReadSvc)
	federationAgg := federation.NewAggregator(federationSvc, federation.WithTunnelHealth(fedTunnelAdapter))

	// T-304: the local-timer protocol's node-side agent — every daemon runs
	// one, independent of whether it ends up coordinating anything, so it
	// can answer a coordinator's arm/cancel/status calls for its own node
	// (peer.ServerOptions.Timers below) as well as serve this node's own
	// Apply calls directly (ClusterTimerAgent's "local" branch, no HTTP
	// round trip to itself).
	nodeTimerRepo := store.NewNodeTimerRepo(db)
	localTimers := change.NewLocalTimerAgent(change.LocalTimerConfig{
		Nodes:  nodeAgent,
		Repo:   nodeTimerRepo,
		Logger: logger,
	})
	if armErr := localTimers.ArmPendingOnStartup(ctx); armErr != nil {
		logger.Error("change: re-arming pending local rollback timers on startup", "error", armErr)
	}
	defer localTimers.StopTimers()

	// The coordinator side of T-304: a dedicated peer client (cluster
	// secret + PVE-cluster-status discovery) and the local-vs-peer routing
	// agents that let internal/change treat every node — this one or a
	// peer's — uniformly. discoverPVEClient failing is non-fatal (mirrors
	// setupCollect's own tolerance below): a daemon that can't yet reach PVE
	// for cluster-status coordinates only its own node, exactly the
	// documented single-node "zero peers" case.
	// clusterStatusSource is left a nil interface (not a non-nil interface
	// wrapping a nil *pve.Client — a classic Go footgun) on discovery-client
	// construction failure: peer.Client.Peers documents a nil
	// ClusterStatusSource as "the documented single-node zero-peers case",
	// exactly the degraded-but-safe behavior wanted here.
	//
	// coordPeerClient is deliberately a second *peer.Client, independent of
	// T-303's peerClient above (also secret+cluster-status-driven, but tied
	// to the collectors' own PVE client and its failure mode): T-304's
	// coordination path must keep working (in degraded, local-node-only
	// form) even when the collectors' PVE client has failed to build,
	// whereas T-303's peerClient is intentionally nil in exactly that case.
	// Unifying these into one shared client is a reasonable future cleanup
	// (T-305/T-306 or a hardening pass) but not attempted here — see
	// planning/reports/T-303.md and T-304.md, developed concurrently and
	// integrated by hand.
	var clusterStatusSource peer.ClusterStatusSource
	if discoveryClient, discErr := buildCollectorPVEClient(cfg); discErr != nil {
		logger.Warn("change: building peer-discovery PVE client; multi-node coordination unavailable until this succeeds", "error", discErr)
	} else {
		clusterStatusSource = discoveryClient
	}
	coordPeerClient := peer.NewClient(peer.ClientOptions{
		ClusterStatus: clusterStatusSource,
		Secrets:       peerSecrets,
		Logger:        logger,
		// T-1906: the shared, cluster-CA-pinned trust anchor. This is the
		// client that carries cross-node changeset application and
		// distributed rollback timers, so it is the one that must not accept
		// an arbitrary publicly-trusted certificate.
		Trust: peerTrust,
		// T-1903: coordination-path peer RPC latency/outcome — selfMetrics is
		// always non-nil here (constructed unconditionally at the top of this
		// function), so no nil-guard adapter is needed the way setupCollect's
		// standalone-function call site needs one.
		Metrics: selfMetrics,
	})
	peerTrustAdapterVal.set(coordPeerClient, localNode)
	// localNode is the same closure already built up above (before
	// dhcpSvc/ipamSvc) — reused here, not redeclared, so every one of its
	// callers throughout this function shares one variable.
	peerLocator := change.NewDiscoveringPeerLocator(coordPeerClient)
	clusterNodes := change.NewClusterNodeAgent(localNode, nodeAgent, coordPeerClient, peerLocator)
	clusterTimers := change.NewClusterTimerAgent(localNode, localTimers, coordPeerClient, peerLocator)

	changeSvc, err := change.NewService(change.Config{
		Changesets:  store.NewChangesetRepo(db),
		Audit:       auditRepo,
		WS:          topoSvc,
		Inventory:   graph,
		Allocations: changeAllocations,
		// T-1201: cross-cluster changeset scoping. LocalClusterID stays "" (the
		// implicit default cluster) so the check is inert for a single-cluster
		// deployment; the aggregator supplies node->cluster membership once
		// clusters are attached.
		ClusterMembership: federationAgg,
		Logger:            logger,
		ProtectedPath:     cfg.Safety.ProtectedPath,
		AllowDangerousOps: cfg.Safety.AllowDangerousOps,
		Nodes:             clusterNodes,
		Timers:            clusterTimers,
		// T-1505: node-local QoS gateway (qos.shape.* ops) — daemon-level,
		// no user ticket needed, exactly like Nodes above.
		Qos: qosGateway,
		// T-1401: the node-local WireGuard gateway (keygen on-node, sealed
		// private key via the same session cipher, fixed-argv wg/wg-quick
		// exec). Daemon-level, so wg rollback works on the unattended
		// commit-confirm-timeout path too.
		WG: newHostWGGateway(wgRepo, sessionCipher, localNode, logger),
		// T-1401 Finding 1: seal a wg.peer.add op's preshared key at
		// stage/create time with the same session cipher, so the plaintext PSK
		// never lands in changesets.ops_json or a read response.
		Sealer: sessionCipher,
		// T-1805 / D1: the same session cipher also seals (and unseals) the
		// apply-time revert ticket — one key, one primitive, for every at-rest
		// credential class in this product (docs/security.md). RevertGateways
		// turns an unsealed ticket back into a non-renewing PVE client so the
		// commit-confirm-timeout and crash-recovery reverts can restore a
		// changeset's firewall/SDN portion with no live user session.
		RevertGateways: revertGatewayFactory{apiURL: cfg.PVE.APIURL, tls: revertTLSConfig(cfg)},
		// T-1401 Finding 2: resolve an existing tunnel's stored carrier so a
		// carrier-less wg op (peer add/remove, delete, MTU-only update) on a
		// mgmt-path tunnel is caught by the scheduling gate the same way the
		// API interlock catches it.
		WgCarriers:     wgReadSvc,
		Snapshots:      snapshotRepo,
		Blobs:          blobRepo,
		Refresher:      refresher,
		ConfirmTimeout: time.Duration(cfg.Server.ConfirmTimeoutDefault) * time.Second,
		// The manual-rollback window tracks the snapshot-retention pin so a
		// still-offered rollback always has its pre-apply snapshot (audit
		// phase-2 F-10).
		RollbackWindowDays: cfg.Retention.SnapshotPinDays,
		// T-1103: changeset_schedules — Config.Clock is left nil (defaults to
		// wrapping the real time.Now() Config.Now itself falls back to, since
		// this daemon has no reason to inject a fake one in production).
		Schedules: store.NewChangeScheduleRepo(db),
		// T-1604: additive failure-impact pre-flight on unattended applies —
		// the scheduler consults this at windowStart on top of (never instead
		// of) its existing touchesMgmtPath exclusion.
		ImpactPreflight: failsimSvc,
		// T-1704: single-writer fence. haGuard.IsLeader gates the unattended
		// auto-rollback timer and the scheduler tick on this daemon holding the
		// HA leader lease; nil-until-set / HA-disabled reports leader=true, so
		// non-HA deployments behave exactly as before.
		LeaderGuard: haGuard.IsLeader,
		// T-1903: apply/confirm/rollback/unattended-revert outcomes and
		// awaiting_confirm duration.
		Metrics: selfMetrics,
		// T-2003: change review — per-op/changeset comments and the
		// review-approval gate, generalizing T-1703's tenant approval queue.
		// Comments/Approvals are always wired (cheap, app-owned tables on the
		// same shared db every other repo here uses); Approval carries the
		// deployment's actual policy from [changesets], off by default so an
		// upgrading install's apply behavior is unchanged until an admin
		// opts in.
		Comments:  store.NewChangesetCommentRepo(db),
		Approvals: store.NewChangesetApprovalRepo(db),
		// T-2601: the declarative policy-as-code rule set. Always wired
		// (an app-owned table on the same shared db); an install with no
		// policy simply has an empty rule set, which produces no findings
		// and changes nothing.
		Policies: store.NewPolicySetRepo(db),
		// T-2602: the persisted pause of a staged (canary) apply. Always
		// wired (an app-owned table on the same shared db), because a hold
		// that cannot be recorded is exactly the unknown state the card
		// forbids — a deployment without it simply cannot request `mode:
		// canary` at all. Canary (the `gate: auto` evidence source) is left
		// unwired for now: automatic promotion is refused at validation time
		// with a message saying so, which is honest, whereas promoting on
		// evidence nothing gathered would not be. T-2603 wires the findings
		// engine in behind this seam.
		Stages: store.NewChangesetStageRepo(db),
		Approval: change.ApprovalConfig{
			Required:          cfg.Changesets.ApprovalRequired,
			AllowSelfApproval: cfg.Changesets.AllowSelfApproval,
			Approvers:         cfg.Changesets.Approvers,
		},
	})
	if err != nil {
		return fmt.Errorf("initializing change engine: %w", err)
	}
	// T-2601: install the configured policy file into the cluster's policy
	// set. A file that cannot be parsed is FATAL — a daemon must never come
	// up quietly enforcing a policy it could not read (acceptance criterion
	// 5). Installing is idempotent: an unchanged file writes no new
	// revision and no audit entry.
	if path := cfg.Changesets.PolicyFile; path != "" {
		set, perr := change.LoadPolicyFile(path)
		if perr != nil {
			return fmt.Errorf("loading policy file: %w", perr)
		}
		if _, perr = changeSvc.SetPolicySet(ctx, "system", set); perr != nil {
			return fmt.Errorf("installing policy file %s: %w", path, perr)
		}
		logger.Info("change: policy file installed", "path", path, "rules", len(set.Rules))
	}

	// T-702: point the findings engine's mgmt_single_path check at the now-
	// real change.Service (see mgmtAdapter's construction/doc comment above).
	mgmtAdapter.set(changeSvc)
	// T-1103: point the findings engine's schedule_missed check at the
	// now-real change.Service (see scheduleAdapter's construction/doc
	// comment above).
	scheduleAdapter.set(changeSvc)
	// T-1604: point the failure-impact adapter's changeset lookup at the
	// now-real change.Service (its scheduler-facing PreflightImpact needs no
	// change.Service, so only the HTTP preflight-impact path is late-bound).
	failsimSvc.set(changeSvc)
	// Re-arm commit-confirm rollback timers persisted across a restart, and
	// recover any apply interrupted by a crash (docs/development.md: "Rollback
	// timers must survive daemon restart ... re-armed on startup").
	if armErr := changeSvc.ArmPendingRollbacks(ctx); armErr != nil {
		logger.Error("change: re-arming pending rollbacks on startup", "error", armErr)
	}
	defer changeSvc.StopTimers()
	// T-2602: canary-hold timers survive a restart the same way — stopped
	// here, re-armed from changeset_apply_stages by ArmPendingRollbacks above.
	defer changeSvc.StopHoldTimers()
	// T-1702: the capability-scoped plugin registry. Built-ins register through
	// the same registry as third-party plugins (proving the extension interfaces
	// by use); the change engine is handed in as the stage-only seam — the
	// registry never exposes Apply/Confirm/Rollback to plugin code. Repo/Audit
	// reuse the same shared *store.DB / *store.AuditRepo every other subsystem
	// uses, so plugin lifecycle events land in the one audit log.
	pluginRegistry := plugin.NewRegistry(plugin.Config{
		Repo:   store.NewPluginRepo(db),
		Change: changeSvc,
		Audit:  auditRepo,
		Logger: logger,
	})
	// T-1705: the hub's plugin-install adapter — turns a hub-verified manifest
	// into a live registration and installs it through the same registry above
	// (which re-validates the capability scope). See hubinstall.go.
	hubInstaller := hubPluginInstaller{registry: pluginRegistry}
	// T-1103: an eager tick right at startup — mirrors ArmPendingRollbacks
	// above — so a schedule whose window (or, for missedWindowPolicy "skip",
	// whose windowEnd) already passed while this daemon was down is resolved
	// immediately rather than waiting for RunScheduler's first real tick
	// (safety-analysis scenario 1, "daemon down mid-window").
	changeSvc.TickSchedules(ctx)

	// T-1704: active/standby HA. Disabled by default (single daemon) — when
	// [ha] enabled = true, build the fenced-lease manager, point the leader
	// guard and findings provider at it, and establish this daemon's initial
	// role from the persisted lease (a still-valid lease it holds resumes
	// active, re-arming timers from the same absolute deadlines). The manager's
	// renew/replicate loop is added to the run group far below.
	var haMgr *ha.Manager
	if cfg.HA.Enabled {
		haMgr, err = buildHAManager(cfg.HA, db, changeSvc, coordPeerClient, logger)
		if err != nil {
			return fmt.Errorf("initializing HA manager: %w", err)
		}
		haGuard.set(haMgr)
		haFindAdapter.set(haMgr)
		if startErr := haMgr.Start(ctx); startErr != nil {
			logger.Error("ha: establishing initial HA role at startup", "error", startErr)
		}
	}

	// T-505: the firewall log viewer's cluster-wide tailer/correlator.
	// Built before peerSrv below so the same local log source
	// (fwlogSource) backs both this daemon's own polling (fwlogSvc) and
	// what it serves to peers over GET /api/peer/firewall/log
	// (fwLogPeerReaderAdapter) — one source of truth for "this node's own
	// log", not two independently-opened readers of the same file.
	// coordPeerClient (constructed above for T-304's coordinator) is
	// reused for fan-out rather than building a third peer.Client — it
	// already carries the same cluster-secret/discovery wiring T-303's
	// peerClient does.
	fwlogSvc, fwlogSource, fwlogErr := setupFwlog(cfg, graph, topoSvc, coordPeerClient, localNode, logger)
	if fwlogErr != nil {
		logger.Error("fwlog: failed to initialize the firewall log viewer's local source; the feature will report empty/unavailable", "error", fwlogErr)
	}
	// T-1006: point the findings engine's fw_rule_unused check at the now-
	// real *fwlog.Service (see fwAnalyticsAdapterVal's construction/doc
	// comment above). Safe even when fwlogSvc is nil (setupFwlog's
	// dev-fixture failure path) — fwAnalyticsAdapter.Analytics degrades to
	// an empty Analytics value exactly like a never-set adapter.
	fwAnalyticsAdapterVal.set(fwlogSvc)

	// T-1002: the flow ingestion engine — sFlow/NetFlow/IPFIX UDP listeners
	// (off by default, opt-in per node via [flows] in vnprox.toml), the
	// bounded flow_samples ring (store.FlowSampleRepo), inventory-resolved
	// srcRef/dstRef (flow.GraphResolver, refreshed from the same live graph
	// every other read path shares), and the `flow.batch` WS push over the
	// same shared hub topoSvc already backs for metrics.sample/drift.changed/
	// changeset.status. GET /flows and GET /api/peer/flows both read
	// directly off flowRepo (store.FlowSampleRepo already satisfies both
	// api.FlowLocalSource and, via flowPeerAdapter, peer.FlowReader — the
	// same "small interface, real type satisfies it for free" shape
	// AuditService/AuditReader use). flowActors are registered with the run
	// group below, alongside every other supervised subsystem. flowSvc
	// itself is reused by setupHostSample right below (T-1004) so its two
	// host-local samplers feed the exact same *flow.Service/ring — no
	// second storage path.
	flowSvc, flowRepo, flowActors := setupFlows(cfg, db, graph, topoSvc, localNode, logger)
	// T-1504: now that flowRepo exists, point the findings engine's
	// service_traffic_on_wrong_network check at the real recent-samples
	// source (see flowClassifyAdapter's own doc comment above).
	flowClassifyAdapterVal.set(flowRepo, flowClassifier)
	// T-1507: same real target, for the migration planner's current-
	// migration-traffic-volume input (migrationTrafficAdapter's own doc
	// comment, cmd/vnproxd/migration.go).
	migrationTrafficVal.set(flowRepo, flowClassifier)
	// T-1601: point the baseline learn job + anomaly detector at the real
	// flow_samples source now that flowRepo exists (baseline.go's own doc
	// comment) — mirrors flowClassifyAdapterVal.set above.
	baselineSvcVal.set(flowRepo)
	// T-1602: give the microseg planner its flow_samples source now that
	// flowRepo exists (mirrors baselineSvcVal.set above).
	microsegSvc.set(flowRepo)

	// T-1004: host-local flow sampling (conntrack/eBPF) — both strictly
	// opt-in per node via [flows] conntrack_sampling_enabled/
	// ebpf_sampling_enabled, off by default; see setupHostSample's doc
	// comment (cmd/vnproxd/hostsample.go). activeHostSampler ("",
	// "conntrack", or "ebpf") is surfaced read-only on GET /config's
	// Settings payload below (api.InstanceInfo.HostSampler).
	activeHostSampler, hostSampleActors := setupHostSample(cfg, flowSvc, localNode, logger)

	// T-301/T-304: the peer server backs the documented /api/peer/host/* and
	// /api/peer/timer/* routes with the real netlink/interfaces(5)/lldpd
	// reader (realHost, built earlier above) for reads and the same
	// nodeAgent/localTimers constructed above for writes.
	//
	// T-302: the same host.Real also backs the guided-install route
	// (POST /api/peer/host/lldp/install, docs/features/lldp-discovery.md
	// §1) via its InstallLLDPD method (host/lldp_install_linux.go).
	var fwLogReader peer.FirewallLogReader
	if fwlogSource != nil {
		fwLogReader = fwLogPeerReaderAdapter{src: fwlogSource}
	}
	// T-1301: the packet-capture coordinator — validates filters, clamps
	// caps to the configured (un-overridable) ceilings, runs node-local
	// captures via the scripted agent, fans multi-point captures out to peers
	// via coordPeerClient, persists app-owned intent to capture_sessions, and
	// audits start/stop. Its retention sweep (RunSweepLoop) and shutdown
	// StopAll are registered/deferred below. peerCaptureClient is nil-safe:
	// with no peer client the coordinator serves only its own node (the
	// documented single-node case).
	captureCoord := setupCapture(cfg, db, auditRepo, coordPeerClient, localNode, logger)
	defer captureCoord.StopAll(context.Background())

	// T-1704: the standby side of HA replication — the active pushes batches to
	// POST /api/peer/ha/replicate. Left nil (503) when HA is disabled, avoiding
	// the typed-nil-interface trap.
	var replicationSink peer.ReplicationSink
	if haMgr != nil {
		replicationSink = ha.NewReceiveSink(haMgr)
	}
	peerSrv := peer.NewServer(peer.ServerOptions{
		Secrets:       peerSecrets,
		Reader:        realHost,
		Writer:        nodeAgent,
		Audit:         auditPeerAdapter{auditRepo},
		Snapshots:     snapshotPeerAdapter{changeSvc},
		Timers:        localTimers,
		LLDPInstaller: realHost,
		FirewallLog:   fwLogReader,
		Flows:         flowPeerAdapter{repo: flowRepo},
		Capture:       capturePeerAdapter{coord: captureCoord},
		Replication:   replicationSink,
		Version:       version,
		Logger:        logger,
	})

	// T-303: peerClient also backs GET /audit and GET /snapshots' cluster
	// fan-out (docs/architecture.md §7) — nil-safe, same as Peer above:
	// when it's nil (no PVE client, or a genuinely peerless single-node
	// deployment reporting zero peers) both routes stay exactly as
	// node-local as they were before T-303.
	var peerAudit api.PeerAuditSource
	var peerSnapshots api.PeerSnapshotSource
	var lldpPeerInstaller api.PeerLLDPInstaller
	var peerFlows api.PeerFlowSource
	// T-1304: guestInteriorPeers backs GET /guests/{ref}/interior's lxc
	// path for a guest whose node is a peer, not this daemon's own — the
	// same nil-safe typed-interface pattern every other peerClient-backed
	// Options field above uses.
	var guestInteriorPeers api.PeerContainerSource
	// peerConntrack backs GET /conntrack's cluster fan-out (T-1305), the
	// same peerClient every other cluster-wide read route above uses.
	var peerConntrack api.PeerConntrackSource
	if peerClient != nil {
		peerAudit = peerClient
		peerSnapshots = peerClient
		lldpPeerInstaller = peerClient
		peerFlows = peerClient
		guestInteriorPeers = peerClient
		peerConntrack = peerClient
	}

	// T-404: GET /sdn/evpn/status fans FRR/BGP state across the cluster
	// via the same realHost reader (local node) and peerClient (peers)
	// every other node-local observability route above already uses;
	// sdnSvc (may be nil) backs exit-node health. evpnSDN/evpnPeers are
	// built as typed-nil-safe interface values (not a bare `sdnSvc`/
	// `peerClient` assignment) for the same "non-nil interface wrapping a
	// nil concrete pointer" footgun clusterStatusSource's own comment
	// above already calls out — a nil *sdn.Service assigned directly to
	// an evpn.SDNZoneSource field would make evpn.Service's own nil check
	// pass and then panic calling Tree() on a nil receiver.
	var evpnPeers evpn.PeerSource
	if peerClient != nil {
		evpnPeers = peerClient
	}
	var evpnSDN evpn.SDNZoneSource
	if sdnSvc != nil {
		evpnSDN = sdnSvc
	}
	evpnSvc := evpn.NewService(evpn.Config{
		Host:      realHost,
		Peers:     evpnPeers,
		LocalNode: localNode,
		SDN:       evpnSDN,
	})

	// T-1404: GET /ipv6/segments fans IPv6 RA/DHCPv6 observations across
	// the cluster the same way evpnSvc above fans FRR/BGP state — same
	// realHost/peerClient/localNode dependencies, same typed-nil-safety
	// reasoning for ipv6Peers.
	var ipv6Peers ipv6.PeerSource
	if peerClient != nil {
		ipv6Peers = peerClient
	}
	ipv6Svc := ipv6.NewService(ipv6.Config{
		Host:      realHost,
		Peers:     ipv6Peers,
		LocalNode: localNode,
		Graph:     graph,
	})

	// T-603: blueprints diff/instantiate against the same live inventory
	// graph every other read path (topology, drift, sim) shares — never a
	// separate copy (docs/architecture.md §2/§3).
	blueprintSvc := blueprint.New(blueprint.Config{
		Repo:      store.NewBlueprintRepo(db),
		Inventory: graph,
	})

	// fwlogSvc is a *fwlog.Service, possibly nil (setupFwlog's dev-fixture
	// load failure path) — assigned through an explicit nil check rather
	// than handed to api.Options.FwLog directly, so a nil *fwlog.Service
	// never becomes a non-nil FwLogService interface value wrapping a nil
	// pointer (the classic Go footgun peer.Client.Peers' own doc comment
	// calls out; mountFwLogRoutes' `if svc == nil` guard only works
	// against a truly nil interface).
	var fwLogAPI api.FwLogService
	if fwlogSvc != nil {
		fwLogAPI = fwlogSvc
	}

	// T-1703: multi-tenancy & self-service. The tenant service resolves each
	// caller's server-side scope against the tenants/tenant_scopes/
	// tenant_members tables, expanding coarse scopes (a VLAN/VNet) to their
	// member guests/subnets live against the same shared inventory graph. It is
	// always wired but inert until an admin creates a tenant (no membership =>
	// unscoped reads), so a single-tenant deployment is unaffected.
	tenantRepo := store.NewTenantRepo(db)
	tenantSvc, err := tenant.NewService(tenant.Config{
		Store:    tenantRepo,
		Expander: tenant.NewGraphExpander(graph),
	})
	if err != nil {
		return fmt.Errorf("setting up tenant service: %w", err)
	}
	// Approval routing reuses T-1005's alert plumbing: a pending
	// request-changeset raises a routed finding to the tenant's approver group.
	tenantNotifier := tenantApprovalNotifier{notifier: webhookNotifier, logger: logger}

	// T-1704: pass the HA manager to GET /ha/status as a clean nil interface
	// when HA is disabled (a typed-nil *ha.Manager would defeat the route's own
	// nil check and panic on Status()).
	var haStatus api.HAStatusService
	if haMgr != nil {
		haStatus = haMgr
	}
	// T-2402: one AckService, shared by GET /findings (which decorates each
	// finding with its acknowledgement) and by the Prometheus exporter (which
	// splits vnprox_findings_open from vnprox_findings_acked). Sharing one
	// instance is what keeps the two surfaces from ever disagreeing about
	// whether a given finding is currently acked.
	findingAcks := findings.NewAckService(findingAckStoreAdapter{repo: store.NewFindingAckRepo(db)}, nil)

	apiOpts := api.Options{
		Version: version,
		// Non-secret operational config for the Settings page's Instance
		// section (GET /config). Deliberately excludes every secret/token/
		// key/password — see api.InstanceInfo.
		Instance: api.InstanceInfo{
			Version:                  version,
			Listen:                   cfg.Server.Listen,
			PVEAPIURL:                cfg.PVE.APIURL,
			ProtectedPath:            cfg.Safety.ProtectedPath,
			PVEInterval:              cfg.Collect.PVEInterval.String(),
			HostInterval:             cfg.Collect.HostInterval.String(),
			LLDPInterval:             cfg.Collect.LLDPInterval.String(),
			ConfirmTimeoutDefaultSec: cfg.Server.ConfirmTimeoutDefault,
			SnapshotKeepDays:         cfg.Retention.SnapshotKeepDays,
			SnapshotPinDays:          cfg.Retention.SnapshotPinDays,
			ReadOnly:                 cfg.Server.ReadOnly,
			AllowDangerousOps:        cfg.Safety.AllowDangerousOps,
			MetricsEnabled:           cfg.Metrics.Enabled,
			HostSampler:              activeHostSampler,
		},
		DistFS:     distFS,
		Logger:     logger,
		Auth:       authServiceAdapter{authSvc},
		Collectors: collectorHealthAdapter{collector},
		Topology:   topoSvc,
		LLDP:       topoSvc,
		Drift:      driftSvc,
		Findings:   findingsEngine,
		// T-2402: acknowledgement is app-owned triage state, so it needs the
		// store. The ack routes mount only when this is non-nil; GET
		// /findings behaves exactly as it did before when it is not.
		FindingAcks: findingAcks,
		// T-2403: entity change history, served by the change service (which
		// already owns the changeset, audit, and snapshot repos this merges).
		EntityHistory: changeSvc,
		// T-2406: the checks vnproxctl cannot answer without the daemon's
		// authenticated PVE client. See cmd/vnproxd/doctorlive.go for which
		// two this closes and which two it deliberately does not.
		DoctorLive:   newDoctorLiveRunner(cfg.PVE.APIURL, cfg.PVE.TokenFile, sdnPVEClient),
		FindingAudit: auditRepo,
		Certs:        certSvc,
		FDB:          topoSvc,
		Metrics:      metricsSampler,
		// T-1001: metricsSampler also satisfies MetricsCounterService
		// (AllCounters) — same underlying object as Metrics above, wired
		// through the dedicated exporter seam GET /metrics uses.
		MetricsCounters: metricsSampler,
		MetricsExporter: api.MetricsExporterConfig{
			Token:        metricsToken,
			AllowFrom:    cfg.Metrics.AllowFrom,
			BuildVersion: version,
			FindingAcks:  findingAcks,
		},
		// T-1903: the daemon's own self-observability registry (HTTP RED via
		// this router's own redMetricsMiddleware, plus whatever collect/
		// change/store/peer wiring above fed the same *Registry into) and the
		// store's pull-model size/schema-version seam — both rendered by
		// GET /metrics alongside the cluster-derived families above.
		SelfMetrics:       selfMetrics,
		Store:             db,
		Layouts:           store.NewLayoutRepo(db),
		Annotations:       store.NewAnnotationRepo(db),
		AlertRules:        alertRuleRepo,
		AlertDeliveries:   alertDeliveryRepo,
		AlertSecretCipher: sessionCipher,
		Federation:        federationSvc,
		FederationAudit:   auditRepo,
		FederationAgg:     federationAgg,
		// T-1203: cross-cluster IPAM conflicts via T-1201's aggregator. The
		// adapter maps federation.ClusterSubnets into the api seam's
		// ipam.ClusterSubnets shape.
		FederationIPAM: federationIPAMAdapter{agg: federationAgg},
		Changesets:     changeSvc,
		Tenant:         tenantSvc,
		TenantStore:    tenantRepo,
		TenantNotifier: tenantNotifier,
		Snapshots:      changeSvc,
		// T-2704: GET /topology/diff — the same change engine, holding the
		// snapshot series and the changeset history the attribution needs.
		TopologyDiff: changeSvc,
		Audit:        auditRepo,
		// T-1704: GET /ha/status (role/lease/replication-lag). haStatus is a
		// clean nil interface when HA is disabled (avoiding the typed-nil trap),
		// so the route simply isn't mounted.
		HA: haStatus,
		// T-1007: GET /history/events merges the same audit_log (narrowed to
		// the changeset-lifecycle action set) with finding_events.
		History:              auditRepo,
		HistoryFindingEvents: findingEventRepo,
		SDN:                  sdnSvc,
		SDNDNS:               sdnDNSSvc,
		IPAM:                 ipamSvc,
		IPAMExternal:         ipamExternalSvc,
		IPAMExternalAudit:    auditRepo,
		EVPN:                 evpnSvc,
		IPv6:                 ipv6Svc,
		DHCP:                 dhcpAPISvc,
		PVEGateways:          pveGatewayProvider{authSvc},
		Protected:            changeSvc,
		PBS:                  pbsAdapter,
		Firewall:             graph,
		Blueprints:           blueprintSvc,
		Spec:                 graph,
		// T-1102: pinned-spec pin/unpin, backed by the same repo driftSvc's
		// spec_drift check reads (see pinnedSpecRepo's construction above).
		SpecPin:      pinnedSpecRepo,
		SpecPinAudit: auditRepo,
		// T-2601: the policy-as-code admin surface. Enforcement is not
		// here — it is in the change engine's validate stage — so this
		// mounts only the read/replace/test routes.
		Policy: changeSvc,
		// T-1107: blueprint sharing bundles (docs/features/blueprints.md §5) —
		// BlueprintSignersAudit reuses the same *store.AuditRepo every other
		// audited route family in this Options literal (LLDPAudit, ProbeAudit)
		// already shares.
		BlueprintSigningKey:   blueprintSigningKey,
		BlueprintTrust:        blueprintTrust,
		BlueprintSignersAudit: auditRepo,
		Simulator:             graph,
		ProbeClients:          probeClientProvider{authSvc},
		ProbeAudit:            auditRepo,
		SimDivergence:         simDivergenceRepo,
		// T-1505: shape-awareness for both simulate routes (a shaped-hop
		// caveat) and GET /topology's shaping-active badge, plus the
		// read-only GET /qos/shapes route — all backed by the same
		// node-local store the qos.shape.* apply/rollback executor writes.
		QosShapes: qosReadSvc,
		Qos:       qosReadSvc,
		// T-1304: guest network interior inspector — GuestInteriorGraph
		// reuses the same live graph Simulator/Firewall above already
		// wire in; GuestInteriorHost reuses realHost (local lxc reads);
		// GuestInteriorPeers reuses peerClient (peer lxc reads, nil-safe
		// per guestInteriorPeers' own construction above); GuestInteriorIPAM
		// reuses ipamConcrete (nil-safe — the same true-nil-interface-
		// until-assigned pattern ipamSvc itself uses, since ipamConcrete
		// may be a nil *ipam.Service).
		GuestInteriorToggles: guestInteriorToggleRepo,
		GuestInteriorGraph:   graph,
		GuestInteriorHost:    realHost,
		GuestInteriorPeers:   guestInteriorPeers,
		GuestInteriorIPAM:    guestInteriorIPAM,
		// T-1403: Edge & NAT cockpit — EdgeInterfaces reuses changeSvc
		// (Changesets' own ReadRawInterfaces), EdgeGraph reuses the same
		// live graph, EdgeIPAM reuses ipamConcrete (nil-safe, same pattern
		// as GuestInteriorIPAM above).
		EdgeInterfaces: changeSvc,
		EdgeGraph:      graph,
		EdgeIPAM:       edgeIPAM,
		// T-1406: ingress visibility — IngressTargets is the app-owned
		// operator-configured target list, IngressSecretCipher reuses the
		// identical session-secret cipher AlertSecretCipher/
		// WebhookSecretCipher above already wire in, and
		// IngressDiscoverers is the default HAProxy/nginx/Caddy/Traefik
		// registry (the seam T-1702's plugin SDK later extends).
		IngressTargets:      ingressTargetRepo,
		IngressSecretCipher: sessionCipher,
		IngressDiscoverers:  ingress.NewDefaultRegistry(nil),
		FwLog:               fwLogAPI,
		Peer:                peerSrv,
		PeerAudit:           peerAudit,
		PeerSnapshots:       peerSnapshots,
		Flows:               flowRepo,
		PeerFlows:           peerFlows,
		FlowClassifier:      flowClassifier,
		LatMesh:             latMeshSvc,
		MTUProbe:            mtuProbeSvc,
		Ceph:                cephAdapter,
		Failsim:             failsimSvc,
		Microseg:            microsegSvc,
		WireGuard:           wgReadSvc,
		WgCarriers:          wgReadSvc,
		Wan:                 wanSvc,
		WanAudit:            auditRepo,
		Captures:            captureCoord,
		Conntrack:           realHost,
		PeerConntrack:       peerConntrack,
		ConntrackGuests:     conntrackGuests,
		// T-605: config documentation export (Tools -> Export documentation)
		// and the onboarding walkthrough's "LLDP offer" step's guided
		// install, both additive to docs/api.md's original contract (see
		// docexport.go/lldpinstall.go's doc comments).
		DocExport: docExportSvc,
		Capacity:  capacityExportSvc,
		Posture:   postureRead,
		Plugins:   pluginRegistry,
		// T-1705: Blueprint & plugin hub. HubClient nil (no [hub] registry_url)
		// skips the routes; PluginInstaller reuses pluginRegistry above so a hub
		// plugin install goes through T-1702's capability-scoped registry.
		HubClient:         hubClientOrNil(hubClient),
		HubVetting:        hubVetted,
		PluginInstaller:   hubInstaller,
		LLDPInstaller:     realHost,
		LLDPPeerInstaller: lldpPeerInstaller,
		LLDPAudit:         auditRepo,
		LocalNode:         localNode,
		// T-1104: automation tokens + webhook registrations, both audited
		// via the same shared auditRepo every other route in this daemon
		// uses.
		Tokens:              apiTokenRepo,
		TokenAudit:          auditRepo,
		Webhooks:            webhookRepo,
		WebhookSecretCipher: sessionCipher,
		// T-1501: Kubernetes overlay mapping engine (read-only forever).
		// K8sClusters/K8sAudit reuse the same *store.AuditRepo/db
		// everything else in this file wires in; K8sSecretCipher reuses
		// the identical session-secret cipher AlertSecretCipher/
		// IngressSecretCipher-shaped fields elsewhere use;
		// K8sGraph is the same live *inventory.Graph every other read
		// path shares; K8sIPAM is nil-safe (k8sIPAMSrc above), only
		// narrowing node<->guest correlation, never failing the route.
		K8sClusters:     k8sClusterRepo,
		K8sSecretCipher: sessionCipher,
		K8sPoller:       k8sPoller,
		K8sGraph:        graph,
		K8sIPAM:         k8sIPAMSrc,
		K8sAudit:        auditRepo,
		// T-1507: the migration network planner (purely advisory, read-only
		// — see internal/migration's own doc.go).
		Migration: migrationPlanner,
	}

	// T-1701: the read-only/stage-only MCP server for AI operators. Off by
	// default ([mcp] enabled=false) — when enabled it is mounted raw at
	// api.DefaultMCPPath, authenticating solely via T-1104 automation bearer
	// tokens. Built from the SAME live services the HTTP read handlers use
	// (staging goes through changeSvc's change engine, never around it) and the
	// SAME diagnosis ladder POST /diagnose runs. See mcpwire.go.
	if cfg.MCP.Enabled {
		mcpSrv, mcpErr := setupMCP(apiOpts, changeSvc, apiTokenRepo, auditRepo, topoSvc, findingsEngine, flowRepo, ipamSvc, graph, logger)
		if mcpErr != nil {
			return fmt.Errorf("setting up MCP server: %w", mcpErr)
		}
		apiOpts.MCP = mcpSrv.HTTPHandler()
		logger.Info("mcp: read-only/stage-only MCP server enabled", "path", api.DefaultMCPPath)
	}

	handler := api.NewRouter(apiOpts)

	srv := &http.Server{
		Addr:    cfg.Server.Listen,
		Handler: handler,
		TLSConfig: &tls.Config{
			GetCertificate: certProvider.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		},
		ReadHeaderTimeout: 10 * time.Second,
	}

	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	defer signal.Stop(sighup)

	var g runGroup
	g.add(func(ctx context.Context) error {
		return certProvider.Watch(ctx, sighup, certPollInterval)
	})
	// Picks up a cluster secret rotated on disk — most notably the very
	// first node's generated secret propagating to every other node via
	// pmxcfs (docs/deployment.md: "first node only; pmxcfs replicates
	// it") — without requiring a daemon restart.
	g.add(func(ctx context.Context) error {
		return peerSecrets.Watch(ctx, peerSecretPollInterval)
	})
	g.add(func(ctx context.Context) error {
		return serveHTTPS(ctx, srv, nil, logger)
	})
	if pprofActor := maybeStartDebugPprof(logger); pprofActor != nil {
		g.add(pprofActor)
	}
	g.add(authSvc.RunRenewalLoop)
	// metric_samples retention (store.MetricRetention): RunPruneLoop's doc
	// comment assigns the wiring to the daemon, and without it the table
	// grows unboundedly once metrics flow (audit phase-0 F-01). Reuses the
	// same *store.MetricSampleRepo instance T-601's metricsSampler above
	// writes through, rather than a second repo over the same table.
	g.add(func(ctx context.Context) error {
		return metricSamples.RunPruneLoop(ctx, metricPruneInterval, func(err error) {
			logger.Error("store: metric_samples prune failed", "error", err)
		})
	})
	// T-1007: finding_events is bounded to the same window as metric_samples
	// (store.MetricRetention) and pruned alongside it — same interval, same
	// "log and keep going" failure contract, its own repo instance/loop
	// (not a second responsibility bolted onto metricSamples.RunPruneLoop)
	// so either table's prune cadence can be retuned independently later.
	g.add(func(ctx context.Context) error {
		return findingEventRepo.RunPruneLoop(ctx, metricPruneInterval, func(err error) {
			logger.Error("store: finding_events prune failed", "error", err)
		})
	})
	// T-1606: the capacity forecasting daily rollup job and its aggregate
	// prune loop. The rollup computes yesterday's downsampled utilization
	// bucket (restart-safe/idempotent); the prune loop enforces
	// [capacity] aggregate_retention_days on the arc's one deliberate
	// retention extension. Both owned and shut down here like every other
	// actor in this group.
	g.add(capacityRollupActor)
	g.add(capacityPruneActor)
	// T-1607: the scheduled posture-score computation (daily; run-once-then-tick)
	// and the posture_scores retention prune loop (keep last
	// DefaultPostureKeepCount computations or DefaultPostureRetentionDays by age).
	// Both supervised goroutines, the same "log and keep going" failure contract
	// every other loop here follows (posture.go's doc comments).
	g.add(postureComputeActor)
	g.add(posturePruneActor)
	// T-1601: the flow-baseline learn job (recomputes per-Ref baselines from
	// the retained flow window on [baseline] learn cadence) and the
	// baseline_profiles retention prune loop ([baseline] profile_retention_days,
	// default 90) — both supervised goroutines, the same "log and keep going"
	// failure contract every other loop here follows (baseline.go's doc
	// comments).
	g.add(func(ctx context.Context) error {
		return baselineSvcVal.RunLearnLoop(ctx, time.Duration(cfg.Baseline.LearnIntervalHours)*time.Hour)
	})
	g.add(func(ctx context.Context) error {
		return baselineProfileRepo.RunPruneLoop(ctx, baselinePruneInterval, cfg.Baseline.ProfileRetentionDays, func(err error) {
			logger.Error("store: baseline_profiles prune failed", "error", err)
		})
	})
	// Snapshot retention (T-206, docs/features/change-management.md §4):
	// keep cfg.Retention.SnapshotKeepDays of history, floored at
	// SnapshotPinDays for any snapshot linked to a committed changeset (the
	// manual-rollback window), then reclaim orphaned blob storage.
	g.add(func(ctx context.Context) error {
		return store.RunSnapshotRetentionLoop(ctx, snapshotRepo, blobRepo, snapshotRetentionInterval,
			cfg.Retention.SnapshotKeepDays, cfg.Retention.SnapshotPinDays, func(err error) {
				logger.Error("store: snapshot retention failed", "error", err)
			})
	})
	// audit_log retention (T-1905): enforces [retention] audit_keep_days
	// (default 730d — internal/store.DefaultAuditRetentionDays's own doc
	// comment has the argument). No pin/in-flight guardrail here, unlike
	// snapshots — an audit row carries no rollback dependency.
	g.add(func(ctx context.Context) error {
		return auditRepo.RunPruneLoop(ctx, auditPruneInterval, cfg.Retention.AuditKeepDays, func(err error) {
			logger.Error("store: audit_log retention failed", "error", err)
		})
	})
	// Store compaction (T-1905): reclaims a bounded batch of freed pages
	// each tick via SQLite incremental auto-vacuum — see
	// internal/store/compact.go's package doc comment for why this never
	// blocks a concurrent reader (WAL mode) and never does the expensive
	// one-time full-VACUUM conversion itself (that already ran once above,
	// before this run group started).
	g.add(func(ctx context.Context) error {
		return store.RunCompactionLoop(ctx, db, compactionInterval, store.DefaultCompactionMaxPages, func(err error) {
			logger.Error("store: compaction failed", "error", err)
		})
	})
	if collector != nil {
		g.add(collector.RunPVELoop)
		g.add(collector.RunHostLoop)
		g.add(collector.RunLLDPLoop)
	}
	g.add(driftSvc.RunLoop)
	// T-1103: the maintenance-window scheduler's own supervised, periodic
	// tick (change.Service.RunScheduler/TickSchedules' doc comments) — owned
	// and shut down here exactly like every other actor in this group.
	g.add(func(ctx context.Context) error {
		return changeSvc.RunScheduler(ctx, change.DefaultScheduleCheckInterval)
	})
	// T-2401: automatic `scheduled` snapshots — the restore point for a
	// change vnprox did NOT make.
	//
	// Registered ONLY when enabled. runGroup.run cancels every other actor as
	// soon as ANY actor returns, so an actor that returns immediately (which
	// RunSnapshotScheduler does at interval 0, the default) shuts the whole
	// daemon down at startup. Registering it unconditionally did exactly that
	// and was caught by cmd/vnproxd's own daemon tests, which is why the
	// guard is here at the registration site rather than only inside the loop.
	if cfg.Retention.SnapshotScheduleInterval > 0 {
		g.add(func(ctx context.Context) error {
			return changeSvc.RunSnapshotScheduler(ctx,
				cfg.Retention.SnapshotScheduleInterval, cfg.Retention.SnapshotScheduleKeep, logger)
		})
	}
	// T-2407: deliver alerts whose quiet-hours or digest hold has expired.
	//
	// Always registered, and RunFlushLoop always blocks until ctx is done —
	// unlike the snapshot scheduler above, which returns immediately when
	// disabled. The distinction matters because runGroup.run cancels every
	// actor as soon as any actor returns: an actor must either block for the
	// daemon's lifetime or not be registered at all.
	g.add(func(ctx context.Context) error {
		return webhookNotifier.RunFlushLoop(ctx, findings.DefaultFlushInterval, logger)
	})
	// T-1704: the HA manager's renew/replicate/promote loop — owned and shut
	// down here like every other actor. Only added when HA is enabled.
	if haMgr != nil {
		g.add(haMgr.RunLoop)
	}
	if fwlogSvc != nil {
		// T-505: continuously merges the local + every peer's pve-firewall
		// log into the shared, rate-capped buffer GET /firewall/log and the
		// `firewall.log.batch` WS push both read from (see
		// internal/fwlog.Service.Run's doc comment). Runs unconditionally
		// once initialized, the same "always on, not gated behind a
		// subscriber" treatment driftSvc.RunLoop above gets.
		g.add(fwlogSvc.Run)
	}
	g.add(findingsEngine.RunLoop)
	// T-1002: the flow ring's prune loop, the resolver refresh loop, and
	// (only when a given protocol's [flows] *_enabled key is true on this
	// node) that protocol's UDP listener — see setupFlows' doc comment.
	// Every actor here already degrades a bind failure to a logged error
	// rather than a fatal one (flowListenerActor), so registering them
	// unconditionally is safe even with every listener disabled (an empty
	// flowActors slice in that case).
	for _, actor := range flowActors {
		g.add(actor)
	}
	// T-1004: host-local flow samplers — an empty hostSampleActors slice
	// (both [flows] sampler flags unset) means this loop registers nothing,
	// so no sampler goroutine ever starts (AC4).
	for _, actor := range hostSampleActors {
		g.add(actor)
	}
	// T-1303: the latency & loss mesh's own probe loop and prune loop
	// (setupLatMesh's doc comment) — always on, the same "always registered,
	// each actor degrades independently" treatment flowActors/
	// hostSampleActors above get.
	for _, actor := range latMeshActors {
		g.add(actor)
	}
	// T-1306: the path MTU prober's own probe loop (setupMTUProbe's doc
	// comment) — always on, its own coarser interval; no prune-loop actor
	// (current-state only, no SQLite ring — internal/mtuprobe's doc.go).
	for _, actor := range mtuProbeActors {
		g.add(actor)
	}
	// T-1405: the WAN health probe loop and prune loop (setupWan's doc
	// comment) — always on, same "always registered, each actor degrades
	// independently" treatment every other probe-loop actor above gets; a
	// node with no configured WAN targets simply has nothing to probe.
	for _, actor := range wanActors {
		g.add(actor)
	}
	// T-1301: the capture retention sweep — deletes per-session .pcap files
	// past their retention_hours (including files orphaned by a daemon
	// restart mid-capture), on the same owned-goroutine/shutdown-path pattern
	// every other prune loop above follows.
	g.add(func(ctx context.Context) error {
		return captureCoord.RunSweepLoop(ctx, captureSweepInterval)
	})
	// T-2504: the soak gate's deliberate leak fixtures. Empty in every build
	// without the `soakleak` tag (soakleak_off.go) — which is every build
	// this repo ships, tests, lints, or packages — so this loop registers
	// nothing at all in production. Under that tag each returned actor still
	// honours this group's contract: it blocks for the daemon's lifetime and
	// returns nil on cancellation (see soakleak.go).
	for _, leakActor := range soakLeakActors(db, logger) {
		g.add(leakActor)
	}

	logger.Info("vnproxd starting",
		"version", version,
		"listen", cfg.Server.Listen,
		"tls_cert", cfg.Server.TLSCertPath,
		"tls_key", cfg.Server.TLSKeyPath,
		"read_only", cfg.Server.ReadOnly,
	)

	err = g.run(ctx)
	logger.Info("vnproxd stopped")
	return err
}

// serveHTTPS runs srv until ctx is cancelled, at which point it drains
// in-flight requests via a bounded graceful Shutdown. It returns nil on a
// clean shutdown (including the expected http.ErrServerClosed from
// Shutdown) or a wrapped error otherwise.
//
// ln is normally nil, in which case srv.Addr is bound the usual way
// (ListenAndServeTLS); tests pass an already-bound listener (so they can
// learn the ephemeral port before serving starts) via srv.ServeTLS instead.
func serveHTTPS(ctx context.Context, srv *http.Server, ln net.Listener, logger *slog.Logger) error {
	serveErr := make(chan error, 1)
	go func() {
		// cert/key args are empty: TLSConfig.GetCertificate/Certificates
		// supplies the keypair, which is what makes hot-reload possible.
		if ln != nil {
			serveErr <- srv.ServeTLS(ln, "", "")
		} else {
			serveErr <- srv.ListenAndServeTLS("", "")
		}
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("https server: %w", err)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutting down: draining in-flight requests")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("https server shutdown: %w", err)
		}
		<-serveErr // let the ListenAndServeTLS goroutine finish, don't leak it
		return nil
	}
}
