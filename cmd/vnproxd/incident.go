// incident.go wires T-2804's incident view.
//
// Note what this composition does NOT do: it starts no goroutine, registers
// no run-group member, and adds no poll loop. An incident is a view over
// history the daemon already records, so the whole wiring is a set of
// existing repositories handed to a service that reads them on request. If
// this file ever needs a `runGroup.Add`, something has gone wrong with the
// feature's central claim.

package main

import (
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/backup"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/incident"
	"github.com/bgovanlu/vnprox/internal/store"
)

// setupIncidents builds the incident service from repositories that already
// exist for other reasons.
//
// changeSvc is passed as the T-2704 diff seam only — incident.Config.Diff is
// a one-method read interface, so no apply/stage path is reachable from here
// even though the concrete type has one.
func setupIncidents(
	cfg *config.Config,
	configPath string,
	db *store.DB,
	auditRepo *store.AuditRepo,
	findingEventRepo *store.FindingEventRepo,
	flowRepo *store.FlowSampleRepo,
	changeSvc *change.Service,
	localNode func() string,
	logger *slog.Logger,
) *incident.Service {
	node := ""
	if localNode != nil {
		node = localNode()
	}
	return incident.New(incident.Config{
		Store:         store.NewIncidentRepo(db),
		FindingEvents: findingEventRepo,
		Audit:         auditRepo,
		Captures:      store.NewCaptureRepo(db),
		Flows:         flowRepo,
		Diff:          changeSvc,
		Bundler:       incident.BundlerFunc(backup.Bundle),
		// The export is the same support bundle `vnproxctl support-bundle`
		// produces, from the same configuration, plus one entry. Probe is
		// left off: this bundle is produced inside a request the operator is
		// waiting on, and the peer/health probes are the slow part.
		ExportBase: backup.BundleOptions{
			ConfigPath:  configPath,
			DBPath:      cfg.Storage.DBPath,
			Listen:      cfg.Server.Listen,
			KeyPaths:    incidentKeyPaths(cfg),
			Node:        node,
			ToolVersion: version,
			Probe:       false,
			Logger:      logger,
		},
		Logger: logger,
	})
}

// incidentKeyPaths mirrors vnproxctl's keyPathRefsFor: the declared key files
// are reported by existence and mode only and are never opened, which is what
// makes "the daemon cannot read its own session key" a diagnosable fault.
func incidentKeyPaths(cfg *config.Config) []backup.KeyPathRef {
	refs := []backup.KeyPathRef{
		{ClassID: "session_key", Path: cfg.Storage.SessionKeyFile},
		{ClassID: "pve_api_token", Path: cfg.PVE.TokenFile},
		{ClassID: "metrics_scrape_token", Path: cfg.Metrics.KeyFile},
		{ClassID: "blueprint_signing_key", Path: cfg.Blueprint.SigningKeyFile},
	}
	if cfg.OIDC.ClientSecretFile != "" {
		refs = append(refs, backup.KeyPathRef{ClassID: "oidc_client_secret", Path: cfg.OIDC.ClientSecretFile})
	}
	return refs
}
