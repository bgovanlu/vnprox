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

	"github.com/bgovanlu/vnprox/internal/capture"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/flow/hostsample"
	"github.com/bgovanlu/vnprox/internal/latmesh"
	"github.com/bgovanlu/vnprox/internal/mtuprobe"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/wan"
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

	// DefaultProtectedPath is where the onboarding-confirmed protected-
	// interface set lives (docs/features/blueprints.md §3:
	// "/etc/pve/vnprox/protected.json" — under pmxcfs, so cluster-wide).
	// Must stay in sync with internal/change.DefaultProtectedPath (this
	// package can't import internal/change; a config test pins the two
	// strings equal). Overridable via [safety] protected_path so a dev
	// daemon outside a real PVE node can persist the set somewhere
	// writable (audit-phase-2 F-13).
	DefaultProtectedPath = "/etc/pve/vnprox/protected.json"

	// DefaultSnapshotKeepDays and DefaultSnapshotPinDays are T-206's
	// documented snapshot-retention policy (planning/tasks/phase-2.md
	// T-206: "keep N days, default 90, committed-changeset snapshots
	// pinned 7d minimum per spec"); mirrors internal/store's identical
	// constants so config defaults and the store's own documented floor
	// agree without cmd/vnproxd having to import internal/store just for
	// the numbers.
	DefaultSnapshotKeepDays = 90
	DefaultSnapshotPinDays  = 7

	// DefaultFirewallLogPath is pve-firewall's conventional log location
	// (T-505, docs/features/firewall.md §4) — mirrors
	// internal/fwlog.DefaultLogPath; a config test pins the two strings
	// equal (this package can't import internal/fwlog for the constant
	// itself without an import cycle risk, following the same
	// duplicate-and-pin-equal precedent DefaultProtectedPath's doc comment
	// already establishes for internal/change).
	DefaultFirewallLogPath = "/var/log/pve-firewall.log"

	// DefaultMetricsKeyFile is where T-1001's Prometheus scrape token lives
	// (docs/security.md's Authentication section: "generated at install
	// alongside the session key"; docs/api.md's Metrics-exporter
	// subsection) — same directory, same root:root 0600 convention as
	// DefaultSessionKeyFile above, generated on first daemon start if
	// absent (cmd/vnproxd/server.go).
	DefaultMetricsKeyFile = "/etc/vnprox/keys/metrics.key"

	// DefaultBlueprintSigningKeyFile and DefaultBlueprintTrustedSignersDir
	// are T-1107's blueprint sharing bundle paths (docs/features/
	// blueprints.md §5): the daemon's own Ed25519 signing identity
	// (generated at first use, same root:root 0600 convention as
	// DefaultSessionKeyFile/DefaultMetricsKeyFile above) and the
	// admin-managed directory of pinned trusted signers.
	DefaultBlueprintSigningKeyFile    = "/etc/vnprox/keys/blueprint-signing.key"
	DefaultBlueprintTrustedSignersDir = "/etc/vnprox/keys/trusted-signers"

	// DefaultCaptureRoot is where T-1301's per-session .pcap files live
	// (docs/data-model.md / docs/security.md's Host footprint note) — under
	// /var/lib/vnprox (already an app-owned, root:root ReadWritePath in the
	// systemd unit), auto-purged past retention_hours.
	DefaultCaptureRoot = "/var/lib/vnprox/captures"
)

// Config is the fully parsed, defaulted, and validated daemon configuration.
type Config struct {
	PVE         PVEConfig
	Storage     StorageConfig
	FirewallLog FirewallLogConfig
	Blueprint   BlueprintConfig
	Peer        PeerConfig
	Safety      SafetyConfig
	Server      ServerConfig
	Metrics     MetricsConfig
	Capture     CaptureConfig
	Flows       FlowsConfig
	Collect     CollectConfig
	Retention   RetentionConfig
	Latmesh     LatmeshConfig
	MTUProbe    MTUProbeConfig
	Wan         WanConfig
}

// CaptureConfig is the [capture] section (T-1301): the server-enforced,
// un-overridable cap ceilings every capture session is bounded by, the root
// directory per-session .pcap files live in, and the BPF-filter
// instruction-count ceiling. These ceilings can never be raised by an API
// request, admin flag, or filter construction — a request may only ask for
// values at or below them (internal/capture.Coordinator.clampCaps). Unset
// fields fall back to internal/capture's own conservative defaults
// (capture.DefaultCaps / capture.DefaultMaxFilterInstructions).
type CaptureConfig struct {
	Root                  string
	MaxDurationSec        int
	MaxBytes              int64
	MaxPackets            int64
	RetentionHours        int
	MaxFilterInstructions int
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
	// ProtectedPath is where the protected-interface set is persisted;
	// defaults to DefaultProtectedPath (pmxcfs). Dev configs point it at a
	// repo-relative var/ path so PUT /protected-interfaces works without
	// root/pmxcfs (audit-phase-2 F-13).
	ProtectedPath string

	// DevInterfacesDir, when non-empty, sandboxes the daemon's host writer:
	// instead of the real /etc/network/interfaces(.new) + ifreload, the
	// change engine stages/commits files under this directory (seeded with
	// a small fixture) and the reload step is a logged no-op. Dev-only, per
	// the dev_ticket_* precedent in [pve] — production configs never set it
	// (audit-phase-2 F-22: `make dev` + a root shell must not be one POST
	// away from ifreloading the developer's machine).
	DevInterfacesDir string

	AllowDangerousOps bool
}

// PeerConfig is the [peer] section: where T-301's cluster secret lives on
// disk. Added by T-301 — no [peer] section existed before it needed a path
// to load/generate the secret at (docs/architecture.md §5,
// docs/deployment.md: "/etc/pve/vnprox/" under pmxcfs).
type PeerConfig struct {
	// SecretPath defaults to peer.DefaultSecretPath. Overridable so a dev
	// daemon outside a real PVE node (no /etc/pve mount) can run with a
	// writable path instead, mirroring the precedent [storage]'s db_path/
	// session_key_file set for T-105's own app-owned files.
	SecretPath string
}

// StorageConfig is the [storage] section: paths for vnprox's own app-owned
// SQLite database and the session-secret encryption key (docs/security.md
// "Authentication", docs/deployment.md "Backup"). Added by T-105 —
// internal/config previously had no field for either path.
type StorageConfig struct {
	DBPath         string
	SessionKeyFile string
}

// RetentionConfig is the [retention] section: the snapshot pruning policy
// T-206's retention job enforces (internal/store.SnapshotRetention).
type RetentionConfig struct {
	SnapshotKeepDays int
	SnapshotPinDays  int
}

// FirewallLogConfig is the [firewalllog] section (T-505): where this
// node's pve-firewall log lives, and the dev-only fixture override.
type FirewallLogConfig struct {
	// Path is the real pve-firewall log file this daemon tails
	// (docs/features/firewall.md §4). Defaults to
	// DefaultFirewallLogPath.
	Path string
	// DevFixtureDir, when non-empty, replaces the real file with a static
	// fixture corpus loaded from this directory (one file per node, named
	// "<node>.log") — dev-only, per the dev_ticket_*/dev_interfaces_dir
	// precedent in [pve]/[safety]: production configs never set it, and a
	// dev daemon against internal/pvemock (which has no real
	// /var/log/pve-firewall.log to read) has no other way to exercise the
	// log viewer at all.
	DevFixtureDir string
}

// CollectConfig is the [collect] section, parsed into durations.
type CollectConfig struct {
	PVEInterval  time.Duration
	HostInterval time.Duration
	LLDPInterval time.Duration
}

// MetricsConfig is the [metrics] section (T-1001, docs/api.md's
// Metrics-exporter subsection): whether `GET /metrics` is mounted at all,
// where its scrape-token file lives, and the optional CIDR source
// allowlist checked before the token (docs/security.md's Authentication
// section: "additive optional CIDR allowlist ... checked before the
// token"). Added by T-1001 — no [metrics] section existed before it needed
// somewhere to configure the exporter.
type MetricsConfig struct {
	KeyFile   string
	AllowFrom []*net.IPNet
	Enabled   bool
}

// BlueprintConfig is the [blueprint] section (T-1107): where the daemon's
// own bundle-signing Ed25519 identity and the admin-managed trusted-signers
// directory live (docs/features/blueprints.md §5). Added by T-1107 — no
// [blueprint] section existed before it needed either path.
type BlueprintConfig struct {
	SigningKeyFile    string
	TrustedSignersDir string
}

// FlowsConfig is the [flows] section (T-1002): per-node, opt-in flow
// ingestion — every listener defaults to *disabled* (docs/features/
// monitoring.md §3's "no packet capture, no flow sampling in v1" carried
// forward as "opt-in per node" for this phase, matching T-1004's own
// opt-in convention). Ports default to each protocol's conventional
// well-known port (internal/flow.Default{SFlow,NetFlow,IPFIX}Port);
// RetentionMinutes/MaxRows default to internal/flow's own documented ring
// bound (internal/flow.Default{RetentionMinutes,MaxRows}) — whichever
// prunes first, see that package's doc comment.
//
// ConntrackSamplingEnabled/EBPFSamplingEnabled (T-1004): the two host-local
// samplers in internal/flow/hostsample, each independently opt-in and off
// by default, matching every listener flag above. HostSampleIntervalSec is
// shared by both (internal/flow/hostsample.DefaultHostSampleInterval when
// unset/non-positive) — conntrack sampling is inherently a periodic
// snapshot/diff, coarser by design than T-1002's live UDP ingestion.
// EBPFSamplingEnabled additionally gates a systemd unit capability grant at
// install/upgrade time (packaging/debian/postinst; docs/security.md's Host
// footprint section) — never granted unconditionally.
type FlowsConfig struct {
	SFlowPort                int
	NetFlowPort              int
	IPFIXPort                int
	RetentionMinutes         int
	MaxRows                  int64
	HostSampleIntervalSec    int
	SFlowEnabled             bool
	NetFlowEnabled           bool
	IPFIXEnabled             bool
	ConntrackSamplingEnabled bool
	EBPFSamplingEnabled      bool
}

// LatmeshConfig is the [latmesh] section (T-1303): the continuous latency &
// loss mesh's own scheduling/retention knobs, always-on (unlike [flows]'
// per-protocol opt-in listeners — a low-rate node-to-node probe mesh has no
// external attack surface a listener does, so there's no "opt-in" gate to
// carry here). ProbeIntervalSec/RetentionMinutes/MaxRows default to
// internal/latmesh's own documented constants when unset/non-positive.
type LatmeshConfig struct {
	ProbeIntervalSec int
	RetentionMinutes int
	MaxRows          int64
}

// MTUProbeConfig is the [mtuprobe] section (T-1306): the path MTU prober's
// own scheduling knob. Always on, same as [latmesh] (a low-rate outbound
// DF-probe carries no listening-port attack surface to gate behind an
// opt-in flag). ProbeIntervalSec defaults to internal/mtuprobe's own
// documented constant (300s — deliberately coarser than [latmesh]'s 10s
// default, since MTU rarely changes) when unset/non-positive.
type MTUProbeConfig struct {
	ProbeIntervalSec int
}

// WanConfig is the [wan] section (T-1405): the WAN health probe's own
// scheduling/retention knobs, independent of [latmesh]'s (see internal/wan's
// package doc comment for why a WAN link's own ring gets its own bound
// rather than sharing latency_samples'). Always on, same reasoning as
// [latmesh]/[mtuprobe] — a low-rate outbound probe toward an operator-
// configured reference target carries no listening-port attack surface to
// gate behind an opt-in flag; a node with no configured targets simply has
// nothing to probe (internal/wan.TargetDiscoverer.Pairs returns empty).
// ProbeIntervalSec/RetentionMinutes/MaxRows default to internal/wan's own
// documented constants when unset/non-positive.
type WanConfig struct {
	ProbeIntervalSec int
	RetentionMinutes int
	MaxRows          int64
	// LossWarnPct is findings.HealthThresholds.WanLossWarnPct's config-file
	// override (0/unset keeps internal/wan.DefaultLossWarnPct, 20%).
	LossWarnPct float64
}

// rawConfig mirrors the TOML shape exactly (string durations, string paths)
// before defaulting/validation/type conversion.
type rawConfig struct {
	PVE         rawPVE         `toml:"pve"`
	Collect     rawCollect     `toml:"collect"`
	Storage     rawStorage     `toml:"storage"`
	FirewallLog rawFirewallLog `toml:"firewalllog"`
	Blueprint   rawBlueprint   `toml:"blueprint"`
	Peer        rawPeer        `toml:"peer"`
	Metrics     rawMetrics     `toml:"metrics"`
	Safety      rawSafety      `toml:"safety"`
	Server      rawServer      `toml:"server"`
	Capture     rawCapture     `toml:"capture"`
	Flows       rawFlows       `toml:"flows"`
	Retention   rawRetention   `toml:"retention"`
	Latmesh     rawLatmesh     `toml:"latmesh"`
	MTUProbe    rawMTUProbe    `toml:"mtuprobe"`
	Wan         rawWan         `toml:"wan"`
}

type rawCapture struct {
	Root                  string `toml:"root"`
	MaxDurationSec        int    `toml:"max_duration_sec"`
	MaxBytes              int64  `toml:"max_bytes"`
	MaxPackets            int64  `toml:"max_packets"`
	RetentionHours        int    `toml:"retention_hours"`
	MaxFilterInstructions int    `toml:"max_filter_instructions"`
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
	ProtectedPath     string `toml:"protected_path"`
	DevInterfacesDir  string `toml:"dev_interfaces_dir"`
	AllowDangerousOps bool   `toml:"allow_dangerous_ops"`
}

type rawStorage struct {
	DBPath         string `toml:"db_path"`
	SessionKeyFile string `toml:"session_key_file"`
}

type rawRetention struct {
	SnapshotKeepDays int `toml:"snapshot_keep_days"`
	SnapshotPinDays  int `toml:"snapshot_pin_days"`
}

type rawPeer struct {
	SecretPath string `toml:"secret_path"`
}

type rawFirewallLog struct {
	Path          string `toml:"path"`
	DevFixtureDir string `toml:"dev_fixture_dir"`
}

type rawCollect struct {
	PVEInterval  string `toml:"pve_interval"`
	HostInterval string `toml:"host_interval"`
	LLDPInterval string `toml:"lldp_interval"`
}

// rawMetrics mirrors [metrics]. Enabled is a *bool (not a plain bool) so
// Load can distinguish "not set in the file" (default true) from an
// explicit "enabled = false" — a plain bool can't make that distinction,
// since TOML decoding leaves an absent key at bool's zero value (false),
// which would otherwise be indistinguishable from an explicit false.
type rawMetrics struct {
	Enabled   *bool    `toml:"enabled"`
	KeyFile   string   `toml:"key_file"`
	AllowFrom []string `toml:"allow_from"`
}

type rawBlueprint struct {
	SigningKeyFile    string `toml:"signing_key_file"`
	TrustedSignersDir string `toml:"trusted_signers_dir"`
}

type rawFlows struct {
	SFlowPort                int   `toml:"sflow_port"`
	NetFlowPort              int   `toml:"netflow_port"`
	IPFIXPort                int   `toml:"ipfix_port"`
	RetentionMinutes         int   `toml:"retention_minutes"`
	MaxRows                  int64 `toml:"max_rows"`
	HostSampleIntervalSec    int   `toml:"host_sample_interval_sec"`
	SFlowEnabled             bool  `toml:"sflow_enabled"`
	NetFlowEnabled           bool  `toml:"netflow_enabled"`
	IPFIXEnabled             bool  `toml:"ipfix_enabled"`
	ConntrackSamplingEnabled bool  `toml:"conntrack_sampling_enabled"`
	EBPFSamplingEnabled      bool  `toml:"ebpf_sampling_enabled"`
}

type rawLatmesh struct {
	ProbeIntervalSec int   `toml:"probe_interval_sec"`
	RetentionMinutes int   `toml:"retention_minutes"`
	MaxRows          int64 `toml:"max_rows"`
}

type rawMTUProbe struct {
	ProbeIntervalSec int `toml:"probe_interval_sec"`
}

type rawWan struct {
	ProbeIntervalSec int     `toml:"probe_interval_sec"`
	RetentionMinutes int     `toml:"retention_minutes"`
	MaxRows          int64   `toml:"max_rows"`
	LossWarnPct      float64 `toml:"loss_warn_pct"`
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
	metricsCfg, err := resolveMetricsConfig(raw.Metrics)
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
			ProtectedPath:     firstNonEmpty(raw.Safety.ProtectedPath, DefaultProtectedPath),
			DevInterfacesDir:  raw.Safety.DevInterfacesDir,
		},
		Storage: StorageConfig{
			DBPath:         firstNonEmpty(raw.Storage.DBPath, DefaultDBPath),
			SessionKeyFile: firstNonEmpty(raw.Storage.SessionKeyFile, DefaultSessionKeyFile),
		},
		Peer: PeerConfig{
			SecretPath: firstNonEmpty(raw.Peer.SecretPath, peer.DefaultSecretPath),
		},
		Retention: RetentionConfig{
			SnapshotKeepDays: firstNonZeroInt(raw.Retention.SnapshotKeepDays, DefaultSnapshotKeepDays),
			SnapshotPinDays:  firstNonZeroInt(raw.Retention.SnapshotPinDays, DefaultSnapshotPinDays),
		},
		FirewallLog: FirewallLogConfig{
			Path:          firstNonEmpty(raw.FirewallLog.Path, DefaultFirewallLogPath),
			DevFixtureDir: raw.FirewallLog.DevFixtureDir,
		},
		Collect: collect,
		Metrics: metricsCfg,
		Blueprint: BlueprintConfig{
			SigningKeyFile:    firstNonEmpty(raw.Blueprint.SigningKeyFile, DefaultBlueprintSigningKeyFile),
			TrustedSignersDir: firstNonEmpty(raw.Blueprint.TrustedSignersDir, DefaultBlueprintTrustedSignersDir),
		},
		Flows: FlowsConfig{
			SFlowEnabled:     raw.Flows.SFlowEnabled,
			NetFlowEnabled:   raw.Flows.NetFlowEnabled,
			IPFIXEnabled:     raw.Flows.IPFIXEnabled,
			SFlowPort:        firstNonZeroInt(raw.Flows.SFlowPort, flow.DefaultSFlowPort),
			NetFlowPort:      firstNonZeroInt(raw.Flows.NetFlowPort, flow.DefaultNetFlowPort),
			IPFIXPort:        firstNonZeroInt(raw.Flows.IPFIXPort, flow.DefaultIPFIXPort),
			RetentionMinutes: firstNonZeroInt(raw.Flows.RetentionMinutes, flow.DefaultRetentionMinutes),
			MaxRows:          firstNonZeroInt64(raw.Flows.MaxRows, flow.DefaultMaxRows),

			ConntrackSamplingEnabled: raw.Flows.ConntrackSamplingEnabled,
			EBPFSamplingEnabled:      raw.Flows.EBPFSamplingEnabled,
			HostSampleIntervalSec:    firstNonZeroInt(raw.Flows.HostSampleIntervalSec, int(hostsample.DefaultHostSampleInterval/time.Second)),
		},
		Latmesh: LatmeshConfig{
			ProbeIntervalSec: firstNonZeroInt(raw.Latmesh.ProbeIntervalSec, latmesh.DefaultProbeIntervalSec),
			RetentionMinutes: firstNonZeroInt(raw.Latmesh.RetentionMinutes, latmesh.DefaultRetentionMinutes),
			MaxRows:          firstNonZeroInt64(raw.Latmesh.MaxRows, latmesh.DefaultMaxRows),
		},
		MTUProbe: MTUProbeConfig{
			ProbeIntervalSec: firstNonZeroInt(raw.MTUProbe.ProbeIntervalSec, mtuprobe.DefaultProbeIntervalSec),
		},
		Wan: WanConfig{
			ProbeIntervalSec: firstNonZeroInt(raw.Wan.ProbeIntervalSec, wan.DefaultProbeIntervalSec),
			RetentionMinutes: firstNonZeroInt(raw.Wan.RetentionMinutes, wan.DefaultRetentionMinutes),
			MaxRows:          firstNonZeroInt64(raw.Wan.MaxRows, wan.DefaultMaxRows),
			LossWarnPct:      raw.Wan.LossWarnPct,
		},
		Capture: CaptureConfig{
			Root:                  firstNonEmpty(raw.Capture.Root, DefaultCaptureRoot),
			MaxDurationSec:        firstNonZeroInt(raw.Capture.MaxDurationSec, capture.DefaultCaps.MaxDurationSec),
			MaxBytes:              firstNonZeroInt64(raw.Capture.MaxBytes, capture.DefaultCaps.MaxBytes),
			MaxPackets:            firstNonZeroInt64(raw.Capture.MaxPackets, capture.DefaultCaps.MaxPackets),
			RetentionHours:        firstNonZeroInt(raw.Capture.RetentionHours, capture.DefaultCaps.RetentionHours),
			MaxFilterInstructions: firstNonZeroInt(raw.Capture.MaxFilterInstructions, capture.DefaultMaxFilterInstructions),
		},
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// LoadStorageOnly reads and parses the config file at path and returns just
// its resolved [storage] section, without running Load's full validate()
// (listen-address syntax, retention bounds, PVE API URL parsing, and —
// critically — TLS certificate/key path *resolution*, which fails fast
// when neither an explicit override nor a real Proxmox node's
// /etc/pve/local/pve-ssl.pem is present).
//
// Added (T-607, a bug this task's own release-gate packaging-matrix run
// found): `vnproxctl snapshots list/restore` and `rollback-now` are
// docs/deployment.md's documented "daemon-independent disaster-recovery
// path" — they only ever need storage.db_path, and must work "even when
// the UI is unreachable" (docs/user-guide.md §4), which in practice also
// means "even when the daemon's own TLS cert is the very thing broken, or
// this is a bare container/dev host with no PVE certificate at all yet."
// Before this function existed, cmd/vnproxctl's openStore called the full
// Load, so these commands failed outright with ErrTLSCertMissing on any
// host without a resolvable PVE certificate — silently breaking the one
// guarantee they exist for. cmd/vnproxctl/status.go's `status` subcommand
// intentionally keeps using the full Load (it legitimately reports PVE/
// peer health, which needs the rest of the config).
func LoadStorageOnly(path string, logger *slog.Logger) (StorageConfig, error) {
	if logger == nil {
		logger = slog.Default()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return StorageConfig{}, fmt.Errorf("reading config file %s: %w", path, err)
	}

	var raw rawConfig
	meta, err := toml.Decode(string(data), &raw)
	if err != nil {
		return StorageConfig{}, fmt.Errorf("%w: parsing config file %s: %v", ErrInvalidConfig, path, err)
	}
	for _, key := range meta.Undecoded() {
		logger.Warn("config: unrecognized key, ignoring", "key", key.String(), "file", path)
	}

	return StorageConfig{
		DBPath:         firstNonEmpty(raw.Storage.DBPath, DefaultDBPath),
		SessionKeyFile: firstNonEmpty(raw.Storage.SessionKeyFile, DefaultSessionKeyFile),
	}, nil
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

	if c.Retention.SnapshotKeepDays <= 0 {
		return fmt.Errorf("%w: retention.snapshot_keep_days must be positive, got %d", ErrInvalidConfig, c.Retention.SnapshotKeepDays)
	}
	if c.Retention.SnapshotPinDays <= 0 {
		return fmt.Errorf("%w: retention.snapshot_pin_days must be positive, got %d", ErrInvalidConfig, c.Retention.SnapshotPinDays)
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

// resolveMetricsConfig defaults/parses [metrics]: Enabled defaults to true
// (see rawMetrics.Enabled's doc comment for why it's a *bool), KeyFile
// defaults to DefaultMetricsKeyFile, and every allow_from entry must be a
// syntactically valid CIDR — an invalid one fails Load fast, the same
// "no partial daemon startup on a bad config" treatment every other
// section's parsing gets.
func resolveMetricsConfig(raw rawMetrics) (MetricsConfig, error) {
	enabled := true
	if raw.Enabled != nil {
		enabled = *raw.Enabled
	}
	allowFrom, err := parseCIDRList(raw.AllowFrom)
	if err != nil {
		return MetricsConfig{}, err
	}
	return MetricsConfig{
		Enabled:   enabled,
		KeyFile:   firstNonEmpty(raw.KeyFile, DefaultMetricsKeyFile),
		AllowFrom: allowFrom,
	}, nil
}

// parseCIDRList parses [metrics] allow_from's string entries into *net.IPNet
// values. An empty/nil input returns a nil slice (docs/security.md: "unset
// allow_from (default) allows any source"), which mountMetricsExporterRoutes'
// allowlist check treats identically to "no restriction".
func parseCIDRList(raw []string) ([]*net.IPNet, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]*net.IPNet, 0, len(raw))
	for _, s := range raw {
		_, ipnet, err := net.ParseCIDR(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("%w: metrics.allow_from entry %q: %v", ErrInvalidConfig, s, err)
		}
		out = append(out, ipnet)
	}
	return out, nil
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

func firstNonZeroInt64(v, def int64) int64 {
	if v == 0 {
		return def
	}
	return v
}
