package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

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
// revertTLSConfig is the TLS trust decision every PVE client built from a
// *user's* credential shares: pinned to the node's own pveproxy certificate in
// a real deployment, unpinned for a dev/test harness talking to a plain-HTTP
// pvemock (which sets dev_ticket_username and has no node certificate to
// trust). It mirrors buildCollectorPVEClient's own branching exactly.
//
// Factored out for T-1805: the sealed-ticket client that performs an
// unattended revert (revertGatewayFactory) must make the *same* trust decision
// as the login client whose ticket it is reusing — two copies of this
// branching drifting apart is how a revert would start failing TLS on real
// hardware while every test kept passing.
func revertTLSConfig(cfg *config.Config) pve.TLSConfig {
	tls := pve.TLSConfig{}
	if cfg.PVE.TicketUsername == "" {
		tls.CACertFile = config.DefaultPVECertPath
	}
	return tls
}

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
	loginTLS := revertTLSConfig(cfg)
	identityFactory := auth.NewClientIdentityFactory(pve.Config{
		APIURL: cfg.PVE.APIURL,
		TLS:    loginTLS,
	})

	// T-1207: OIDC SSO, wired only when [oidc] is configured (Enabled derived
	// from a set issuer). A deployment with no [oidc] section gets a nil
	// OIDCService, leaving the PVE-ticket-bridge-only login flow untouched.
	oidcSvc, err := setupOIDC(cfg, logger, db, cipher, loginTLS)
	if err != nil {
		return nil, nil, err
	}

	authSvc, err := auth.NewService(auth.Config{
		Sessions:    sessions,
		Audit:       auditRepo,
		Tokens:      tokens,
		NewIdentity: identityFactory,
		OIDC:        oidcSvc,
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

// setupOIDC constructs the OIDC SSO service from cfg.OIDC, or returns (nil, nil)
// when OIDC is not configured. The client secret is read from
// cfg.OIDC.ClientSecretFile (a root:root 0600 file, never inlined in the config
// in the clear); the group→role mapping table is translated from config into
// internal/auth.GroupMapping; and the per-cluster PVE-linkage resolver reads the
// encrypted oidc_pve_links store table, building linked PVE clients against the
// local cluster's PVE API with the same TLS trust the login client uses.
func setupOIDC(cfg *config.Config, logger *slog.Logger, db *store.DB, cipher *store.SessionCipher, loginTLS pve.TLSConfig) (*auth.OIDCService, error) {
	if !cfg.OIDC.Enabled {
		return nil, nil
	}

	var clientSecret string
	if cfg.OIDC.ClientSecretFile != "" {
		b, err := os.ReadFile(cfg.OIDC.ClientSecretFile)
		if err != nil {
			return nil, fmt.Errorf("reading oidc client secret file %s: %w", cfg.OIDC.ClientSecretFile, err)
		}
		clientSecret = strings.TrimSpace(string(b))
	}

	provider, err := auth.NewOIDCProvider(auth.OIDCProviderConfig{
		Issuer:       cfg.OIDC.Issuer,
		ClientID:     cfg.OIDC.ClientID,
		ClientSecret: clientSecret,
		RedirectURL:  cfg.OIDC.RedirectURL,
		Scopes:       cfg.OIDC.Scopes,
		GroupsClaim:  cfg.OIDC.GroupsClaim,
	})
	if err != nil {
		return nil, fmt.Errorf("constructing oidc provider: %w", err)
	}

	mappings := make([]auth.GroupMapping, 0, len(cfg.OIDC.Groups))
	for _, g := range cfg.OIDC.Groups {
		scopes := make([]auth.Cap, 0, len(g.Caps))
		for _, name := range g.Caps {
			scopes = append(scopes, auth.Cap(name))
		}
		mappings = append(mappings, auth.GroupMapping{Group: g.Group, Caps: auth.CapabilitiesFromScopes(scopes)})
	}

	resolver := auth.NewStorePVELinkResolver(
		store.NewOIDCPVELinkRepo(db), cipher, cfg.OIDC.ClusterID,
		pve.Config{APIURL: cfg.PVE.APIURL, TLS: loginTLS},
	)

	oidcSvc, err := auth.NewOIDCService(auth.OIDCConfig{
		Provider:  provider,
		Resolver:  resolver,
		Mappings:  mappings,
		ClusterID: cfg.OIDC.ClusterID,
	})
	if err != nil {
		return nil, fmt.Errorf("constructing oidc service: %w", err)
	}
	logger.Info("auth: OIDC SSO enabled", "issuer", cfg.OIDC.Issuer, "groups", len(mappings))
	return oidcSvc, nil
}
