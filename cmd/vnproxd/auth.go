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
// The returned *store.DB must be closed by the caller on shutdown. Note:
// production TLS trust for the PVE-facing client this constructs
// (identityFactory below) is left at its zero-value default (system CA
// pool) rather than pinned to the node's PVE certificate — architecture.md
// §9's CA-pinning story is genuinely T-101's TLSConfig knob to wire up, but
// no earlier task has plumbed a concrete value through cmd/vnproxd yet
// (there is no PVE collector wiring before this task; T-104 is later in the
// plan). Flagged here rather than solved, since it's outside T-105's own
// scope (login/session/CSRF/capabilities) and best decided alongside
// T-104's collector wiring, which needs the exact same knob.
func setupAuth(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*auth.Service, *store.DB, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.Storage.DBPath), 0o750); err != nil {
		return nil, nil, fmt.Errorf("creating storage directory for %s: %w", cfg.Storage.DBPath, err)
	}
	db, err := store.Open(ctx, cfg.Storage.DBPath)
	if err != nil {
		return nil, nil, err
	}

	if _, statErr := os.Stat(cfg.Storage.SessionKeyFile); errors.Is(statErr, os.ErrNotExist) {
		logger.Info("auth: generating session key", "path", cfg.Storage.SessionKeyFile)
		if genErr := store.GenerateKeyFile(cfg.Storage.SessionKeyFile); genErr != nil {
			_ = db.Close()
			return nil, nil, genErr
		}
	} else if statErr != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("checking session key file %s: %w", cfg.Storage.SessionKeyFile, statErr)
	}

	key, err := store.LoadKeyFile(cfg.Storage.SessionKeyFile)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	cipher, err := store.NewSessionCipher(key)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}

	sessions := store.NewSessionRepo(db, cipher)
	auditRepo := store.NewAuditRepo(db)

	identityFactory := auth.NewClientIdentityFactory(pve.Config{APIURL: cfg.PVE.APIURL})

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
		return nil, nil, err
	}
	return authSvc, db, nil
}
