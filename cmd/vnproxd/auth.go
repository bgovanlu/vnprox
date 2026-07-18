package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/store"
)

// setupAuth loads — generating on first run if absent — the session-secret
// encryption key (cfg.Storage.SessionKeyFile), and constructs the T-105
// auth.Service that internal/api's router mounts docs/api.md's /auth/*
// routes through.
//
// db/auditRepo/tokens are constructed once by the caller (server.go) and
// passed in, rather than this function opening its own store.DB/AuditRepo
// the way earlier versions of this function did: auditRepo in particular
// must be the exact same instance every other audit-writing call site in
// this binary uses (router.Options.Audit, ProbeAudit, LLDPAudit, ...) —
// T-1104's `audit.appended` WS/webhook event is wired via a single
// SetOnAppend hook on one shared *store.AuditRepo (see events.go's
// wireAuditAppendedEvents doc comment); a second, independent AuditRepo
// wrapping the same table would silently miss every append that went
// through it, which is exactly the bug a from-scratch review of this
// function's original "open my own db/auditRepo" shape would have shipped
// silently. tokens (T-1104's api_tokens repo) is optional — nil disables
// bearer-token auth entirely, matching auth.Config.Tokens' own nil-safe
// convention.
//
// The returned *store.SessionCipher is the same session-secret AES-256-GCM
// cipher sessions.pve_ticket_enc/csrf_token_enc use; T-1005 reuses it
// verbatim (rather than a second cipher/key pair) to encrypt
// alert_rules.target_secret_enc — docs/security.md's "secrets encrypted at
// rest" pattern is documented as one mechanism, not one per feature.
//
// **Correction (T-608, hardware validation):** the PVE-facing client this
// constructs (identityFactory below) used to be left at its zero-value TLS
// default (system CA pool), on the reasoning that T-104's later collector
// wiring would need the exact same CA-pinning knob and should decide it —
// but when T-104 landed (cmd/vnproxd/collect.go's buildCollectorPVEClient,
// TLS: pve.TLSConfig{CACertFile: config.DefaultPVECertPath}), this client
// was never revisited to match. The result: on any real PVE node (a
// self-signed pveproxy certificate, which is the default), interactive
// login failed outright with "could not reach the PVE API" — a TLS
// verification error the client only logged server-side — while the
// read-only collector/vnproxctl status paths worked fine, since they
// already trusted the node's own certificate. Every real deployment's
// login flow was completely broken until this was caught by actually
// logging in against a real node.
func setupAuth(cfg *config.Config, logger *slog.Logger, db *store.DB, auditRepo *store.AuditRepo, tokens *store.APITokenRepo) (*auth.Service, *store.SessionCipher, error) {
	if _, statErr := os.Stat(cfg.Storage.SessionKeyFile); errors.Is(statErr, os.ErrNotExist) {
		logger.Info("auth: generating session key", "path", cfg.Storage.SessionKeyFile)
		if genErr := store.GenerateKeyFile(cfg.Storage.SessionKeyFile); genErr != nil {
			return nil, nil, genErr
		}
	} else if statErr != nil {
		return nil, nil, fmt.Errorf("checking session key file %s: %w", cfg.Storage.SessionKeyFile, statErr)
	}

	key, err := store.LoadKeyFile(cfg.Storage.SessionKeyFile)
	if err != nil {
		return nil, nil, err
	}
	cipher, err := store.NewSessionCipher(key)
	if err != nil {
		return nil, nil, err
	}

	sessions := store.NewSessionRepo(db, cipher)

	// TLS trust mirrors buildCollectorPVEClient's own branching
	// (cmd/vnproxd/collect.go) exactly: dev/test harnesses set
	// dev_ticket_username to talk to a plain-HTTP pvemock and have no real
	// node certificate to trust; real deployments (no override) pin to the
	// node's own pveproxy certificate the same way the collector client
	// does.
	loginTLS := pve.TLSConfig{}
	if cfg.PVE.TicketUsername == "" {
		loginTLS.CACertFile = config.DefaultPVECertPath
	}
	identityFactory := auth.NewClientIdentityFactory(pve.Config{
		APIURL: cfg.PVE.APIURL,
		TLS:    loginTLS,
	})

	authSvc, err := auth.NewService(auth.Config{
		Sessions:    sessions,
		Audit:       auditRepo,
		Tokens:      tokens,
		NewIdentity: identityFactory,
		Logger:      logger,
		// T-605: `[server] read_only = true` (docs/features/blueprints.md
		// §3) forces every derived capability read-only, server-side, not
		// just in the UI — see auth.Config.ReadOnly's doc comment.
		ReadOnly: cfg.Server.ReadOnly,
	})
	if err != nil {
		return nil, nil, err
	}
	return authSvc, cipher, nil
}
