// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/metrics"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// setupCollect builds the T-104 collectors: a *collect.Collector wired to
// its own PVE client (see buildCollectorPVEClient), the real local-host
// reader, and graph — ready for its RunPVELoop/RunHostLoop/RunLLDPLoop to
// be registered with the run group. It also builds (T-303) the *peer.Client
// the host loop uses to fan out to every other cluster member, reusing the
// exact same PVE client for peer discovery (GET /cluster/status) rather
// than constructing a second one — returned alongside the collector so
// callers can reuse it for other cluster fan-out (peer.Server's audit/
// snapshot readers, internal/api's cluster-merge handlers).
//
// Collector construction failing (in practice: the PVE client's own
// construction failing — a missing/unreadable token file or CA cert) is
// deliberately not fatal to the whole daemon: runDaemon logs it and starts
// without live inventory polling (or cluster fan-out — peerClient is nil
// too in this case, since peer discovery is itself PVE-cluster-status-
// driven) rather than refusing to serve the UI/API at all. This matters
// today in particular because the documented production PVE-token
// provisioning (vnprox@pve!daemon) is a tracked but not yet implemented
// installer step (T-606; see packaging/bin/vnprox-setup), so a fresh
// production install's token file genuinely may not exist yet.
//
// pveClient itself is also returned (T-401): internal/sdn.Service reads
// PVE directly and live per request (see that package's doc comment for
// why), reusing the collectors' own read-only identity rather than
// building a second client — the same "returned alongside the collector so
// callers can reuse it" pattern peerClient already established for T-303's
// cluster fan-out.
func setupCollect(cfg *config.Config, graph *inventory.Graph, logger *slog.Logger, onDelta func(inventory.Delta), onStats func(ctx context.Context, node string, at time.Time, links []host.LinkState, stats map[string]host.IfaceStats), onServices func(node string, status map[string]bool), peerSecrets *peer.SecretStore, peerTrust *peer.Trust, selfMetrics *metrics.Registry, demoRT *demoRuntime) (*collect.Collector, *peer.Client, *pve.Client, error) {
	pveClient, err := buildCollectorPVEClient(cfg, demoRT)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("building collectors' PVE client: %w", err)
	}

	// T-2801: a demo daemon builds NO peer client. Peer fan-out dials the
	// addresses the fixture's own cluster status advertises (10.10.0.12,
	// 10.10.0.13) — real addresses on a real network, which plenty of
	// networks will route somewhere. The cluster-wide fixture reader below
	// answers for those nodes instead, so nothing is lost and nothing is
	// dialled.
	var peerClient *peer.Client
	if peerSecrets != nil && !demoRT.enabled() {
		peerClient = peer.NewClient(peer.ClientOptions{
			ClusterStatus: pveClient,
			Secrets:       peerSecrets,
			Logger:        logger,
			// T-1906: the daemon's one shared cluster-CA-pinned trust anchor
			// (runDaemon's peerTrust) — the collectors' fan-out makes the same
			// trust decision as the coordinator's client, never its own.
			Trust: peerTrust,
			// T-1903: this client's own RPCs (fetching each peer's host state
			// every host-loop tick) are exactly as much "a peer call" as
			// change/HA's coordination traffic — recorded through the same
			// registry as everything else.
			Metrics: peerMetricsRecorder(selfMetrics),
		})
	}

	c, err := collect.New(collect.Config{
		PVE: pveClient,
		// T-2801: a demo daemon reads the embedded fixture's host state, not
		// the machine it is running on. demoRuntime.hostReader is nil-safe
		// and returns host.NewReal() for a normal daemon, so this line is
		// unchanged behaviour outside demo mode.
		Host: demoRT.hostReader(),
		// Cluster-wide only in demo mode: the fixture reader answers for
		// every node, a real host.Reader answers only for its own.
		HostServesCluster: demoRT.enabled(),
		Peer:              peerClient,
		Graph:             graph,
		PVEInterval:       cfg.Collect.PVEInterval,
		HostInterval:      cfg.Collect.HostInterval,
		LLDPInterval:      cfg.Collect.LLDPInterval,
		Logger:            logger,
		OnDelta:           onDelta,
		// T-601: the metrics sampler's counter-ingestion hook, piggybacked
		// on this same host-loop poll (see collect.Config.OnStats's doc
		// comment) rather than a second poll loop.
		OnStats: onStats,
		// T-602: the findings engine's service-status hook, piggybacked on
		// this same host-loop poll exactly like OnStats above.
		OnServices: onServices,
		// T-1903: poll duration/outcome per source+node, mirroring (not
		// duplicating) Status()'s own last_success/last_attempt/
		// consecutive_failures bookkeeping — see collect.Config.OnPoll's
		// doc comment.
		//
		// T-2504 wraps that hook with soakLeakPollHook, which in every build
		// without the `soakleak` tag — i.e. every build this repo ships,
		// tests, or packages — is the identity function (soakleak_off.go).
		// Under that tag it is the "one goroutine per collection cycle" leak
		// fixture the soak gate must catch; there is no runtime switch.
		OnPoll: soakLeakPollHook(pollMetricsHook(selfMetrics)),
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("constructing collector: %w", err)
	}
	return c, peerClient, pveClient, nil
}

// pollMetricsHook returns a collect.Config.OnPoll closure recording
// through reg, or nil if reg is nil (a test caller of setupCollect that
// doesn't wire T-1903's registry) — collect.Collector's own onPoll is
// already nil-safe (reportPoll), so a nil return here is exactly "not
// observed", not a missing feature.
func pollMetricsHook(reg *metrics.Registry) func(source, node string, dur time.Duration, err error) {
	if reg == nil {
		return nil
	}
	return func(source, node string, dur time.Duration, err error) {
		reg.ObserveCollectorPoll(source, node, dur, err)
	}
}

// peerMetricsRecorder adapts reg to peer.MetricsRecorder, returning a
// genuinely nil interface value when reg is nil. Assigning a nil
// *metrics.Registry straight into an interface-typed field would instead
// produce a non-nil interface wrapping a nil pointer (Go's classic typed-
// nil footgun) — peer.Client's own `if c.opts.Metrics != nil` guard would
// then evaluate true and panic on first use. This adapter is the one place
// that decision is made correctly.
func peerMetricsRecorder(reg *metrics.Registry) peer.MetricsRecorder {
	if reg == nil {
		return nil
	}
	return reg
}

// buildCollectorPVEClient constructs the collectors' own PVE API client.
//
// Production (documented) path: the read-only API-token identity
// vnprox@pve!daemon (docs/security.md), TLS pinned to the node's own PVE
// certificate — the exact CA-pinning knob docs/architecture.md §9
// describes and cmd/vnproxd/auth.go's setupAuth left unresolved pending
// this task, since the daemon's own HTTPS listener (server.go) and the PVE
// API it polls are, on a real PVE node, the very same certificate
// (config.DefaultPVECertPath).
//
// Dev/test override path: if cfg.PVE.TicketUsername is set (see
// PVEConfig's doc comment — never set in a production config), authenticate
// with PVE ticket auth instead, against whatever cfg.PVE.APIURL points at
// (typically plain-HTTP internal/pvemock for local dev). This is the only
// way to exercise the collectors against pvemock at all: pvemock does not
// implement PVE API-token authentication.
//
// Demo path (T-2801): demoRT.httpClient() is the in-process transport that
// answers from the embedded fixture and cannot dial. It is passed as
// pve.Config.HTTPClient, which suppresses buildHTTPClient entirely — so a
// demo daemon's collector client is not "a real client pointed somewhere
// harmless", it is a client with no way to reach a network at all. Demo
// mode sets TicketUsername (see demoPVEConfig), so it takes the ticket
// branch below.
func buildCollectorPVEClient(cfg *config.Config, demoRT *demoRuntime) (*pve.Client, error) {
	if cfg.PVE.TicketUsername != "" {
		return pve.New(pve.Config{
			APIURL:     cfg.PVE.APIURL,
			Auth:       pve.AuthTicket,
			Username:   cfg.PVE.TicketUsername,
			Password:   cfg.PVE.TicketPassword,
			Realm:      cfg.PVE.TicketRealm,
			HTTPClient: demoRT.httpClient(),
		})
	}
	return pve.New(pve.Config{
		APIURL:     cfg.PVE.APIURL,
		Auth:       pve.AuthAPIToken,
		TokenFile:  cfg.PVE.TokenFile,
		TLS:        pve.TLSConfig{CACertFile: config.DefaultPVECertPath},
		HTTPClient: demoRT.httpClient(),
	})
}

// collectorHealthAdapter converts collect.Collector.Status() into
// internal/api's CollectorHealth shape, so neither package needs to import
// the other — this wiring layer is the only place that knows about both.
type collectorHealthAdapter struct {
	c *collect.Collector
}

// CollectorStatus implements api.CollectorHealth. Safe to call when c is
// nil (collectors failed to initialize): reports no sources rather than
// panicking.
func (a collectorHealthAdapter) CollectorStatus() []api.CollectorSourceStatus {
	if a.c == nil {
		return nil
	}
	st := a.c.Status()
	out := make([]api.CollectorSourceStatus, len(st.Sources))
	for i, s := range st.Sources {
		// Since T-303, collect.Collector.Status() already sets each
		// SourceStatus's own Node (one "host" entry per cluster node it
		// polls, "lldp" scoped to the local node, "pve" cluster-wide/
		// unscoped) — this adapter just copies it through rather than
		// inferring it from LocalNode.
		out[i] = api.CollectorSourceStatus{
			Name:                s.Name,
			Node:                s.Node,
			LastSuccess:         s.LastSuccess,
			LastAttempt:         s.LastAttempt,
			ConsecutiveFailures: s.ConsecutiveFailures,
			LastError:           s.LastError,
		}
	}
	return out
}
