package main

import (
	"fmt"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
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
func setupCollect(cfg *config.Config, graph *inventory.Graph, logger *slog.Logger, onDelta func(inventory.Delta), peerSecrets *peer.SecretStore) (*collect.Collector, *peer.Client, *pve.Client, error) {
	pveClient, err := buildCollectorPVEClient(cfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("building collectors' PVE client: %w", err)
	}

	var peerClient *peer.Client
	if peerSecrets != nil {
		peerClient = peer.NewClient(peer.ClientOptions{
			ClusterStatus: pveClient,
			Secrets:       peerSecrets,
			Logger:        logger,
		})
	}

	c, err := collect.New(collect.Config{
		PVE:          pveClient,
		Host:         host.NewReal(),
		Peer:         peerClient,
		Graph:        graph,
		PVEInterval:  cfg.Collect.PVEInterval,
		HostInterval: cfg.Collect.HostInterval,
		LLDPInterval: cfg.Collect.LLDPInterval,
		Logger:       logger,
		OnDelta:      onDelta,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("constructing collector: %w", err)
	}
	return c, peerClient, pveClient, nil
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
func buildCollectorPVEClient(cfg *config.Config) (*pve.Client, error) {
	if cfg.PVE.TicketUsername != "" {
		return pve.New(pve.Config{
			APIURL:   cfg.PVE.APIURL,
			Auth:     pve.AuthTicket,
			Username: cfg.PVE.TicketUsername,
			Password: cfg.PVE.TicketPassword,
			Realm:    cfg.PVE.TicketRealm,
		})
	}
	return pve.New(pve.Config{
		APIURL:    cfg.PVE.APIURL,
		Auth:      pve.AuthAPIToken,
		TokenFile: cfg.PVE.TokenFile,
		TLS:       pve.TLSConfig{CACertFile: config.DefaultPVECertPath},
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
