package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/store"
)

// setupAuth opens vnprox's own SQLite store (cfg.Storage.DBPath), loads —
// generating on first run if absent — the session-secret encryption key
// (cfg.Storage.SessionKeyFile), and constructs the T-105 auth.Service that
// internal/api's router mounts docs/api.md's /auth/* routes through.
//
// The returned *store.DB must be closed by the caller on shutdown. The
// returned *store.SessionCipher is the same session-secret AES-256-GCM
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
func setupAuth(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*auth.Service, *store.DB, *store.SessionCipher, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.Storage.DBPath), 0o750); err != nil {
		return nil, nil, nil, fmt.Errorf("creating storage directory for %s: %w", cfg.Storage.DBPath, err)
	}
	db, err := store.Open(ctx, cfg.Storage.DBPath)
	if err != nil {
		return nil, nil, nil, err
	}

	if _, statErr := os.Stat(cfg.Storage.SessionKeyFile); errors.Is(statErr, os.ErrNotExist) {
		logger.Info("auth: generating session key", "path", cfg.Storage.SessionKeyFile)
		if genErr := store.GenerateKeyFile(cfg.Storage.SessionKeyFile); genErr != nil {
			_ = db.Close()
			return nil, nil, nil, genErr
		}
	} else if statErr != nil {
		_ = db.Close()
		return nil, nil, nil, fmt.Errorf("checking session key file %s: %w", cfg.Storage.SessionKeyFile, statErr)
	}

	key, err := store.LoadKeyFile(cfg.Storage.SessionKeyFile)
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}
	cipher, err := store.NewSessionCipher(key)
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}

	sessions := store.NewSessionRepo(db, cipher)
	auditRepo := store.NewAuditRepo(db)

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
		NewIdentity: identityFactory,
		Logger:      logger,
		// T-605: `[server] read_only = true` (docs/features/blueprints.md
		// §3) forces every derived capability read-only, server-side, not
		// just in the UI — see auth.Config.ReadOnly's doc comment.
		ReadOnly: cfg.Server.ReadOnly,
	})
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}
	return authSvc, db, cipher, nil
}
