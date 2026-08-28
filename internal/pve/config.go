// SPDX-License-Identifier: Apache-2.0

package pve

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// AuthMode selects how a Client authenticates to PVE.
type AuthMode int

const (
	// AuthTicket logs in with PVE user credentials (POST /access/ticket)
	// and renews the resulting ticket before it goes stale. Used when
	// vnprox performs writes with a logged-in user's own PVE privileges
	// (docs/architecture.md §6).
	AuthTicket AuthMode = iota

	// AuthAPIToken sends a fixed "Authorization: PVEAPIToken=..." header
	// on every request. Used for the daemon's read-only polling identity,
	// vnprox@pve!daemon (docs/security.md, docs/deployment.md).
	AuthAPIToken
)

// DefaultTicketRenewAfter is how long the client waits after issuing (or
// last renewing) a ticket before proactively renewing it again. PVE
// tickets expire at 2h; docs/security.md specifies renewing at ~1h30. It
// is a Config field (not a constant) precisely so tests can inject a much
// shorter threshold against a short-TTL fixture and observe a real renewal
// happen (T-101 acceptance criterion 1).
const DefaultTicketRenewAfter = 90 * time.Minute

// TicketLifetime is how long a PVE ticket stays valid after issue
// (docs/security.md: "PVE tickets expire at 2h"). It is the basis for the
// expiry stamped on T-1805's sealed revert ticket, and therefore for the
// apply-time report of how much of a commit-confirm window unattended
// revert can actually cover. **Needs hardware validation**: real PVE's
// exact behaviour near the boundary (whether a ticket in its final minutes
// is still accepted for a mutating call) has never been observed on iron —
// see planning/reports/needs-hardware-validation.md.
const TicketLifetime = 2 * time.Hour

// DefaultHTTPTimeout bounds a single PVE HTTP request (not task waits,
// which have their own WaitOptions.Timeout).
const DefaultHTTPTimeout = 30 * time.Second

// TLSConfig drives the client's transport TLS behavior
// (docs/architecture.md §9, docs/security.md "Transport"): pin to the
// node's local pveproxy certificate, or fall back to the system trust
// pool.
type TLSConfig struct {
	// CACertFile, if set, is a PEM file (typically the node's own
	// pveproxy certificate, e.g. /etc/pve/local/pve-ssl.pem) the client
	// trusts exclusively for TLS verification. Empty means "use the
	// system certificate pool".
	CACertFile string

	// ServerName overrides the SNI/verification hostname, for cases
	// where APIURL's host doesn't match the certificate's subject (e.g.
	// connecting via an IP while the cert names the hostname).
	ServerName string

	// InsecureSkipVerify disables TLS verification entirely. Never set
	// this in production; it exists for local dev against a self-signed
	// throwaway cert. Config.validate does not police it — the caller
	// (cmd/vnproxd, tests) is responsible for only setting it in
	// non-production paths.
	InsecureSkipVerify bool
}

// Config configures a Client. Field names/shapes intentionally mirror
// config.PVEConfig (APIURL, TokenFile) so the daemon's wiring layer
// (cmd/vnproxd) can build a pve.Config directly from a loaded
// config.Config without any translation beyond picking the auth mode:
//
//	pve.Config{
//	    APIURL:    cfg.PVE.APIURL,
//	    Auth:      pve.AuthAPIToken,
//	    TokenFile: cfg.PVE.TokenFile,
//	}
//
// internal/pve intentionally does not import internal/config itself, to
// keep the client package independent of the daemon's config-file
// parsing concerns.
type Config struct {
	// HTTPClient, if set, is used as-is instead of one built from TLS.
	// Tests point this at an httptest.Server's client (or leave it nil
	// for a server that doesn't use TLS at all, as internal/pvemock's
	// does).
	HTTPClient *http.Client
	// Logger receives debug-level request instrumentation (method, path,
	// status, duration — never ticket/CSRF/token values). Nil means
	// slog.Default().
	Logger *slog.Logger

	// APIURL is the PVE API base, e.g. "https://127.0.0.1:8006"
	// (config.PVEConfig.APIURL / config.DefaultPVEAPIURL). Requests are
	// sent to APIURL + "/api2/json/...".
	APIURL string

	// --- AuthTicket fields ---

	Username string
	Password string
	Realm    string
	// Ticket/CSRFToken build a **sealed-ticket** client: one that
	// authenticates with an already-issued PVE ticket and its CSRF token and
	// **never renews and never logs in**. It is the credential form T-1805's
	// unattended revert uses — the applying user's ticket, unsealed from
	// changesets.revert_ticket_enc long after the request that issued it
	// ended, with no password anywhere in the process to fall back on.
	//
	// Set both together, with Auth == AuthTicket and Password empty. Once the
	// ticket expires PVE rejects the request (surfacing as *ErrPVEAuth) —
	// deliberately, rather than the client silently re-authenticating: a
	// daemon that could mint fresh tickets from a sealed one would be able to
	// extend a user's credential lifetime indefinitely, which is exactly the
	// standing-privileged-credential property D1 rejected.
	Ticket    string
	CSRFToken string
	// OTP is a one-time TOTP/U2F code passed through to PVE on the
	// initial login (docs/security.md "OTP/second factor passthrough").
	// It is not sent on ticket-as-password renewals (the normal renewal
	// path); it is only ever replayed on the rare plaintext-password
	// fallback, where a real time-based code would likely be stale anyway
	// — see ticketAuth in auth.go.
	OTP string

	// --- AuthAPIToken fields ---

	// TokenValue, if set, is used directly as the value sent after
	// "PVEAPIToken=" in the Authorization header (the full
	// "user@realm!tokenid=secret" string). Takes precedence over
	// TokenFile; primarily for tests that want to inject a token value
	// without a file on disk.
	TokenValue string
	// TokenFile is a path (config.PVEConfig.TokenFile /
	// config.DefaultPVETokenFile) to a file whose trimmed contents are
	// the same "user@realm!tokenid=secret" value. Read once at
	// construction time.
	TokenFile string

	// --- record mode (T-2502) ---

	// RecordDir, if set, turns on cassette recording into
	// <RecordDir>/<RecordPVEVersion>/ (see record.go). It overrides the
	// VNPROX_PVE_RECORD environment variable, which is the documented
	// operator flow; the field exists so tests can drive record mode
	// without mutating process-wide state.
	RecordDir string
	// RecordPVEVersion is the PVE release label recorded in every cassette
	// and used as the directory name. Overrides VNPROX_PVE_VERSION.
	// Required whenever recording is on: New fails rather than defaulting
	// it, because a cassette that cannot say which PVE produced it has
	// thrown away the one thing it has over a hand-written fixture.
	RecordPVEVersion string

	// TLS drives the client's transport TLS behavior; see TLSConfig.
	TLS TLSConfig

	// TicketRenewAfter overrides DefaultTicketRenewAfter. Zero means use
	// the default; tests inject a short duration to exercise renewal
	// against a short-TTL fixture.
	TicketRenewAfter time.Duration
	// RequestTimeout bounds each individual HTTP request. Zero means
	// DefaultHTTPTimeout.
	RequestTimeout time.Duration

	// Auth selects the authentication mode.
	Auth AuthMode
}

func (cfg Config) validate() error {
	if cfg.APIURL == "" {
		return fmt.Errorf("pve: Config.APIURL is required")
	}
	if _, err := url.ParseRequestURI(cfg.APIURL); err != nil {
		return fmt.Errorf("pve: Config.APIURL %q: %w", cfg.APIURL, err)
	}
	switch cfg.Auth {
	case AuthTicket:
		// Sealed-ticket mode (T-1805): an already-issued ticket + CSRF token
		// and no credentials at all. Both halves are required — a ticket
		// without its CSRF token cannot perform the mutating calls a revert is
		// made of, so accepting one alone would build a client that silently
		// fails every write it exists to make.
		if cfg.Ticket != "" || cfg.CSRFToken != "" {
			if cfg.Ticket == "" || cfg.CSRFToken == "" {
				return fmt.Errorf("pve: Config.Ticket and Config.CSRFToken must be set together for a sealed-ticket client")
			}
			if cfg.Password != "" {
				return fmt.Errorf("pve: Config.Password must be empty for a sealed-ticket client (it never renews)")
			}
			return nil
		}
		if cfg.Username == "" {
			return fmt.Errorf("pve: Config.Username is required for AuthTicket")
		}
		if cfg.Password == "" {
			return fmt.Errorf("pve: Config.Password is required for AuthTicket")
		}
	case AuthAPIToken:
		if cfg.TokenValue == "" && cfg.TokenFile == "" {
			return fmt.Errorf("pve: one of Config.TokenValue or Config.TokenFile is required for AuthAPIToken")
		}
	default:
		return fmt.Errorf("pve: unknown Config.Auth mode %d", cfg.Auth)
	}
	return nil
}

// New builds a Client from cfg, validating it and (for AuthAPIToken with
// TokenFile set) reading the token file. It does not perform any network
// call; ticket-auth login happens lazily on the first request.
func New(cfg Config) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	base, err := url.Parse(strings.TrimRight(cfg.APIURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("pve: parsing Config.APIURL: %w", err)
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	httpc := cfg.HTTPClient
	if httpc == nil {
		httpc, err = buildHTTPClient(cfg.TLS, cfg.RequestTimeout)
		if err != nil {
			return nil, err
		}
	}

	rec, err := newRecorder(cfg, logger)
	if err != nil {
		return nil, err
	}

	c := &Client{
		baseURL: base,
		httpc:   httpc,
		log:     logger,
		rec:     rec,
	}

	switch cfg.Auth {
	case AuthTicket:
		if cfg.Ticket != "" {
			c.auth = &ticketAuth{ticket: cfg.Ticket, csrf: cfg.CSRFToken, sealed: true}
			break
		}
		renewAfter := cfg.TicketRenewAfter
		if renewAfter <= 0 {
			renewAfter = DefaultTicketRenewAfter
		}
		c.auth = &ticketAuth{
			username:   cfg.Username,
			password:   cfg.Password,
			realm:      cfg.Realm,
			otp:        cfg.OTP,
			renewAfter: renewAfter,
		}
	case AuthAPIToken:
		token := cfg.TokenValue
		if token == "" {
			token, err = readTokenFile(cfg.TokenFile)
			if err != nil {
				return nil, err
			}
		}
		c.auth = &tokenAuth{token: token}
	}

	return c, nil
}

func readTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("pve: reading token file %s: %w", path, err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("pve: token file %s is empty", path)
	}
	return token, nil
}

func buildHTTPClient(cfg TLSConfig, requestTimeout time.Duration) (*http.Client, error) {
	timeout := requestTimeout
	if timeout <= 0 {
		timeout = DefaultHTTPTimeout
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // explicit opt-in, see TLSConfig doc.
		ServerName:         cfg.ServerName,
	}
	if cfg.CACertFile != "" {
		pem, err := os.ReadFile(cfg.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("pve: reading TLS CA cert %s: %w", cfg.CACertFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("pve: no valid certificates found in %s", cfg.CACertFile)
		}
		tlsConfig.RootCAs = pool
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}, nil
}
