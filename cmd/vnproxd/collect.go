package main

import (
	"fmt"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// setupCollect builds the T-104 collectors: a *collect.Collector wired to
// its own PVE client (see buildCollectorPVEClient), the real local-host
// reader, and graph — ready for its RunPVELoop/RunHostLoop/RunLLDPLoop to
// be registered with the run group.
//
// Collector construction failing (in practice: the PVE client's own
// construction failing — a missing/unreadable token file or CA cert) is
// deliberately not fatal to the whole daemon: runDaemon logs it and starts
// without live inventory polling rather than refusing to serve the UI/API
// at all. This matters today in particular because the documented
// production PVE-token provisioning (vnprox@pve!daemon) is a tracked but
// not yet implemented installer step (T-606; see packaging/bin/vnprox-setup),
// so a fresh production install's token file genuinely may not exist yet.
func setupCollect(cfg *config.Config, graph *inventory.Graph, logger *slog.Logger, onDelta func(inventory.Delta)) (*collect.Collector, error) {
	pveClient, err := buildCollectorPVEClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("building collectors' PVE client: %w", err)
	}

	c, err := collect.New(collect.Config{
		PVE:          pveClient,
		Host:         host.NewReal(),
		Graph:        graph,
		PVEInterval:  cfg.Collect.PVEInterval,
		HostInterval: cfg.Collect.HostInterval,
		LLDPInterval: cfg.Collect.LLDPInterval,
		Logger:       logger,
		OnDelta:      onDelta,
	})
	if err != nil {
		return nil, fmt.Errorf("constructing collector: %w", err)
	}
	return c, nil
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
		out[i] = api.CollectorSourceStatus{
			Name:                s.Name,
			LastSuccess:         s.LastSuccess,
			LastAttempt:         s.LastAttempt,
			ConsecutiveFailures: s.ConsecutiveFailures,
			LastError:           s.LastError,
		}
	}
	return out
}
