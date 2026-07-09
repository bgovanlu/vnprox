// Package config implements daemon config file parsing and validation for
// vnproxd. The on-disk shape is the TOML file documented in
// docs/deployment.md ("Configuration file — /etc/vnprox/vnprox.toml"):
// [server], [pve], [safety], and [collect] sections. Unrecognized keys are
// logged as warnings, not treated as fatal, per that doc ("unknown keys are
// warnings, not fatals").
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Defaults mirror the example config in docs/deployment.md.
const (
	DefaultListen                = "0.0.0.0:8007"
	DefaultConfirmTimeoutDefault = 120

	DefaultPVEAPIURL    = "https://127.0.0.1:8006"
	DefaultPVETokenFile = "/etc/vnprox/keys/pve-token"

	DefaultPVEInterval  = 10 * time.Second
	DefaultHostInterval = 5 * time.Second
	DefaultLLDPInterval = 30 * time.Second

	// DefaultPVECertPath and DefaultPVEKeyPath are the node's PVE-managed
	// certificate, reused by default per architecture.md §9 so the browser
	// trust story matches PVE's own UI.
	DefaultPVECertPath = "/etc/pve/local/pve-ssl.pem"
	DefaultPVEKeyPath  = "/etc/pve/local/pve-ssl.key"

	// DefaultDBPath and DefaultSessionKeyFile are vnprox's own app-owned
	// storage paths (docs/deployment.md "Backup": "/var/lib/vnprox/vnprox.db";
	// docs/security.md "Authentication": the session cipher key). Added by
	// T-105 (internal/auth) — no [storage] section existed before it needed
	// somewhere to load the SQLite store and the AES-256-GCM session key
	// from, and both are genuinely daemon-wide paths, not auth-specific
	// ones, hence their own section rather than living under [server].
	DefaultDBPath         = "/var/lib/vnprox/vnprox.db"
	DefaultSessionKeyFile = "/etc/vnprox/keys/session.key"
)

// Config is the fully parsed, defaulted, and validated daemon configuration.
type Config struct {
	PVE     PVEConfig
	Storage StorageConfig
	Server  ServerConfig
	Collect CollectConfig
	Safety  SafetyConfig
}

// ServerConfig is the [server] section.
type ServerConfig struct {
	Listen                string
	TLSCert               string
	TLSKey                string
	TLSCertPath           string
	TLSKeyPath            string
	ConfirmTimeoutDefault int
	ReadOnly              bool
}

// PVEConfig is the [pve] section.
type PVEConfig struct {
	APIURL    string
	TokenFile string

	// TicketUsername, TicketPassword, TicketRealm are an optional,
	// dev/testing-only override for the collectors' own PVE client
	// (internal/collect, T-104): when TicketUsername is set, collectors
	// authenticate with PVE ticket auth (these credentials) instead of
	// the documented production API-token identity vnprox@pve!daemon
	// (docs/security.md). This exists because internal/pvemock — the
	// only PVE server this project's dev/test setups talk to — does not
	// implement PVE API-token authentication at all (a documented T-101
	// gap: every token-mode request is rejected with 401; see
	// internal/pve/integration_test.go's TestAPIToken commentary), so a
	// dev config pointed at pvemock has no other way to exercise the
	// collectors. Mirrors the precedent set by this same section's
	// api_url (dev.toml uses http:// against pvemock; production always
	// uses https://): a documented, deliberate dev-only deviation, never
	// touched by a production config file. Left unset, PVE.APIURL +
	// PVE.TokenFile + AuthAPIToken is used exactly as documented.
	TicketUsername string
	TicketPassword string
	TicketRealm    string
}

// SafetyConfig is the [safety] section.
type SafetyConfig struct {
	AllowDangerousOps bool
}

// StorageConfig is the [storage] section: paths for vnprox's own app-owned
// SQLite database and the session-secret encryption key (docs/security.md
// "Authentication", docs/deployment.md "Backup"). Added by T-105 —
// internal/config previously had no field for either path.
type StorageConfig struct {
	DBPath         string
	SessionKeyFile string
}

// CollectConfig is the [collect] section, parsed into durations.
type CollectConfig struct {
	PVEInterval  time.Duration
	HostInterval time.Duration
	LLDPInterval time.Duration
}

// rawConfig mirrors the TOML shape exactly (string durations, string paths)
// before defaulting/validation/type conversion.
type rawConfig struct {
	Collect rawCollect `toml:"collect"`
	PVE     rawPVE     `toml:"pve"`
	Storage rawStorage `toml:"storage"`
	Server  rawServer  `toml:"server"`
	Safety  rawSafety  `toml:"safety"`
}

type rawServer struct {
	Listen                string `toml:"listen"`
	TLSCert               string `toml:"tls_cert"`
	TLSKey                string `toml:"tls_key"`
	ReadOnly              bool   `toml:"read_only"`
	ConfirmTimeoutDefault int    `toml:"confirm_timeout_default"`
}

type rawPVE struct {
	APIURL         string `toml:"api_url"`
	TokenFile      string `toml:"token_file"`
	TicketUsername string `toml:"dev_ticket_username"`
	TicketPassword string `toml:"dev_ticket_password"`
	TicketRealm    string `toml:"dev_ticket_realm"`
}

type rawSafety struct {
	AllowDangerousOps bool `toml:"allow_dangerous_ops"`
}

type rawStorage struct {
	DBPath         string `toml:"db_path"`
	SessionKeyFile string `toml:"session_key_file"`
}

type rawCollect struct {
	PVEInterval  string `toml:"pve_interval"`
	HostInterval string `toml:"host_interval"`
	LLDPInterval string `toml:"lldp_interval"`
}

// Load reads, parses, defaults, and validates the config file at path.
// Invalid values (bad listen address, missing explicitly-configured TLS
// files, malformed durations, ...) fail fast with a wrapped, descriptive
// error and no partial daemon startup. Unrecognized keys are logged via
// logger (slog.Default() if nil) and otherwise ignored, matching the
// documented "unknown keys are warnings, not fatals" upgrade behavior.
func Load(path string, logger *slog.Logger) (*Config, error) {
	if logger == nil {
		logger = slog.Default()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	var raw rawConfig
	meta, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, fmt.Errorf("%w: parsing config file %s: %v", ErrInvalidConfig, path, err)
	}

	for _, key := range meta.Undecoded() {
		logger.Warn("config: unrecognized key, ignoring", "key", key.String(), "file", path)
	}

	collect, err := resolveCollectConfig(raw.Collect)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Server: ServerConfig{
			Listen:                firstNonEmpty(raw.Server.Listen, DefaultListen),
			ReadOnly:              raw.Server.ReadOnly,
			ConfirmTimeoutDefault: firstNonZeroInt(raw.Server.ConfirmTimeoutDefault, DefaultConfirmTimeoutDefault),
			TLSCert:               raw.Server.TLSCert,
			TLSKey:                raw.Server.TLSKey,
		},
		PVE: PVEConfig{
			APIURL:         firstNonEmpty(raw.PVE.APIURL, DefaultPVEAPIURL),
			TokenFile:      firstNonEmpty(raw.PVE.TokenFile, DefaultPVETokenFile),
			TicketUsername: raw.PVE.TicketUsername,
			TicketPassword: raw.PVE.TicketPassword,
			TicketRealm:    raw.PVE.TicketRealm,
		},
		Safety: SafetyConfig{
			AllowDangerousOps: raw.Safety.AllowDangerousOps,
		},
		Storage: StorageConfig{
			DBPath:         firstNonEmpty(raw.Storage.DBPath, DefaultDBPath),
			SessionKeyFile: firstNonEmpty(raw.Storage.SessionKeyFile, DefaultSessionKeyFile),
		},
		Collect: collect,
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// validate checks semantic constraints beyond what TOML decoding enforces
// and resolves the effective TLS certificate/key paths. It is the single
// place acceptance-criterion-4 failures (bad listen address, missing cert
// paths) are produced.
func (c *Config) validate() error {
	if err := validateListen(c.Server.Listen); err != nil {
		return fmt.Errorf("server.listen: %w", err)
	}

	if c.Server.ConfirmTimeoutDefault <= 0 {
		return fmt.Errorf("%w: server.confirm_timeout_default must be positive, got %d", ErrInvalidConfig, c.Server.ConfirmTimeoutDefault)
	}

	certPath, keyPath, err := resolveTLSPaths(c.Server.TLSCert, c.Server.TLSKey)
	if err != nil {
		return err
	}
	c.Server.TLSCertPath = certPath
	c.Server.TLSKeyPath = keyPath

	if _, err := url.ParseRequestURI(c.PVE.APIURL); err != nil {
		return fmt.Errorf("%w: pve.api_url %q: %v", ErrInvalidConfig, c.PVE.APIURL, err)
	}

	return nil
}

// validateListen checks addr is a syntactically valid "host:port" (or
// ":port") with a port in the valid TCP range. It intentionally accepts
// hostnames as well as IPs (net.SplitHostPort/Listen resolve those later);
// it exists to reject typos and malformed strings early with a clear error
// rather than an opaque bind failure at Listen time.
func validateListen(addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%w: %q must be a host:port address: %v", ErrInvalidConfig, addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%w: %q has an invalid port", ErrInvalidConfig, addr)
	}
	if host != "" && net.ParseIP(host) == nil && strings.ContainsAny(host, " \t/\\") {
		return fmt.Errorf("%w: %q has an invalid host", ErrInvalidConfig, addr)
	}
	return nil
}

// resolveTLSPaths implements the TLS cert-selection rule from
// architecture.md §9 / security.md "Transport": reuse the node's PVE
// certificate by default, or use an explicit tls_cert/tls_key override.
// Whichever is selected must exist on disk at startup — there is no
// insecure fallback.
func resolveTLSPaths(certOverride, keyOverride string) (certPath, keyPath string, err error) {
	switch {
	case certOverride == "" && keyOverride == "":
		if _, statErr := os.Stat(DefaultPVECertPath); statErr != nil {
			return "", "", fmt.Errorf("%w: PVE certificate not found at %s (this daemon expects to run on a Proxmox VE node; set server.tls_cert/tls_key in a dev environment to override)", ErrTLSCertMissing, DefaultPVECertPath)
		}
		if _, statErr := os.Stat(DefaultPVEKeyPath); statErr != nil {
			return "", "", fmt.Errorf("%w: PVE certificate key not found at %s", ErrTLSCertMissing, DefaultPVEKeyPath)
		}
		return DefaultPVECertPath, DefaultPVEKeyPath, nil

	case certOverride == "" || keyOverride == "":
		return "", "", fmt.Errorf("%w: server.tls_cert and server.tls_key must both be set to override the default PVE certificate, or both left empty", ErrInvalidConfig)

	default:
		if _, statErr := os.Stat(certOverride); statErr != nil {
			return "", "", fmt.Errorf("%w: server.tls_cert %s: %v", ErrTLSCertMissing, certOverride, statErr)
		}
		if _, statErr := os.Stat(keyOverride); statErr != nil {
			return "", "", fmt.Errorf("%w: server.tls_key %s: %v", ErrTLSCertMissing, keyOverride, statErr)
		}
		return certOverride, keyOverride, nil
	}
}

func resolveCollectConfig(raw rawCollect) (CollectConfig, error) {
	pveInterval, err := parseDurationOrDefault(raw.PVEInterval, DefaultPVEInterval, "collect.pve_interval")
	if err != nil {
		return CollectConfig{}, err
	}
	hostInterval, err := parseDurationOrDefault(raw.HostInterval, DefaultHostInterval, "collect.host_interval")
	if err != nil {
		return CollectConfig{}, err
	}
	lldpInterval, err := parseDurationOrDefault(raw.LLDPInterval, DefaultLLDPInterval, "collect.lldp_interval")
	if err != nil {
		return CollectConfig{}, err
	}
	return CollectConfig{
		PVEInterval:  pveInterval,
		HostInterval: hostInterval,
		LLDPInterval: lldpInterval,
	}, nil
}

func parseDurationOrDefault(raw string, def time.Duration, field string) (time.Duration, error) {
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %s %q: %v", ErrInvalidConfig, field, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%w: %s must be positive, got %s", ErrInvalidConfig, field, d)
	}
	return d, nil
}

func firstNonEmpty(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func firstNonZeroInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}
