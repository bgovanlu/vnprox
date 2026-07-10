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
	"syscall"
	"time"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
	webui "github.com/bgovanlu/vnprox/web"
)

// certPollInterval is the mtime-polling fallback interval for TLS
// cert/key hot-reload (see config.CertProvider.Watch's doc comment for why
// polling instead of fsnotify).
const certPollInterval = 30 * time.Second

// metricPruneInterval is how often the metric_samples prune loop enforces
// store.MetricRetention (24h, docs/data-model.md §2). Hourly keeps the
// table within ~4% of the retention window at negligible cost.
const metricPruneInterval = time.Hour

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

	distFS, err := distRootFS()
	if err != nil {
		return err
	}

	authSvc, db, err := setupAuth(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("initializing auth: %w", err)
	}
	defer func() { _ = db.Close() }()

	graph := inventory.NewGraph()
	topoSvc := topology.NewService(graph, logger)
	collector, collectErr := setupCollect(cfg, graph, logger, topoSvc.OnDelta)
	if collectErr != nil {
		logger.Error("collect: failed to initialize PVE/host collectors; starting without live inventory polling", "error", collectErr)
	}

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
	var nodeAgent change.NodeAgent = newHostNodeAgent(logger)
	if dir := cfg.Safety.DevInterfacesDir; dir != "" {
		devAgent, devErr := newDevNodeAgent(dir, logger)
		if devErr != nil {
			return fmt.Errorf("initializing dev interfaces sandbox: %w", devErr)
		}
		logger.Warn("change: DEV MODE host writer — interfaces file operations are sandboxed and ifreload is a no-op", "dir", dir)
		nodeAgent = devAgent
	}
	changeSvc, err := change.NewService(change.Config{
		Changesets:        store.NewChangesetRepo(db),
		Audit:             store.NewAuditRepo(db),
		WS:                topoSvc,
		Inventory:         graph,
		Logger:            logger,
		ProtectedPath:     cfg.Safety.ProtectedPath,
		AllowDangerousOps: cfg.Safety.AllowDangerousOps,
		Nodes:             nodeAgent,
		Snapshots:         store.NewSnapshotRepo(db),
		Refresher:         refresher,
		ConfirmTimeout:    time.Duration(cfg.Server.ConfirmTimeoutDefault) * time.Second,
	})
	if err != nil {
		return fmt.Errorf("initializing change engine: %w", err)
	}
	// Re-arm commit-confirm rollback timers persisted across a restart, and
	// recover any apply interrupted by a crash (docs/development.md: "Rollback
	// timers must survive daemon restart ... re-armed on startup").
	if armErr := changeSvc.ArmPendingRollbacks(ctx); armErr != nil {
		logger.Error("change: re-arming pending rollbacks on startup", "error", armErr)
	}
	defer changeSvc.StopTimers()

	handler := api.NewRouter(api.Options{
		Version:     version,
		DistFS:      distFS,
		Logger:      logger,
		Auth:        authServiceAdapter{authSvc},
		Collectors:  collectorHealthAdapter{collector},
		Topology:    topoSvc,
		Layouts:     store.NewLayoutRepo(db),
		Changesets:  changeSvc,
		PVEGateways: pveGatewayProvider{authSvc},
		Protected:   changeSvc,
	})

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
	g.add(func(ctx context.Context) error {
		return serveHTTPS(ctx, srv, nil, logger)
	})
	g.add(authSvc.RunRenewalLoop)
	// metric_samples retention (store.MetricRetention): RunPruneLoop's doc
	// comment assigns the wiring to the daemon, and without it the table
	// grows unboundedly once metrics flow (audit phase-0 F-01).
	metricSamples := store.NewMetricSampleRepo(db)
	g.add(func(ctx context.Context) error {
		return metricSamples.RunPruneLoop(ctx, metricPruneInterval, func(err error) {
			logger.Error("store: metric_samples prune failed", "error", err)
		})
	})
	if collector != nil {
		g.add(collector.RunPVELoop)
		g.add(collector.RunHostLoop)
		g.add(collector.RunLLDPLoop)
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
