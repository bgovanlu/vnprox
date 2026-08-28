// SPDX-License-Identifier: Apache-2.0

// Package config implements daemon config file parsing and validation for
// vnproxd. The on-disk shape is the TOML file documented in
// docs/deployment.md ("Configuration file — /etc/vnprox/vnprox.toml"):
// [server], [pve], [safety], and [collect] sections. Unrecognized keys are
// logged as warnings, not treated as fatal, per that doc ("unknown keys are
// warnings, not fatals").
package config

import (
	"fmt"
	"io"
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
	"github.com/bgovanlu/vnprox/internal/ha"
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

	// DefaultAuditKeepDays mirrors store.DefaultAuditRetentionDays (T-1905:
	// 730 days/2 years, argued in that constant's own doc comment — audit
	// is the compliance/forensic record, so its ceiling is deliberately far
	// longer than every other bounded table in this arc) — the same
	// mirror-not-import convention DefaultSnapshotKeepDays above uses.
	DefaultAuditKeepDays = 730

	// DefaultSnapshotScheduleKeep is [retention] snapshot_schedule_keep's
	// default: how many automatic `scheduled` captures (T-2401) to retain.
	// 48 is chosen against the natural hourly cadence — two days of history
	// at one capture an hour — but the number that matters is not the
	// interval: captures are de-duplicated by content, so a stable cluster
	// holds one row no matter how often the timer fires, and 48 distinct
	// rows means 48 genuine config changes went unrecorded by a changeset.
	// That is already far more out-of-band editing than a healthy install
	// does.
	DefaultSnapshotScheduleKeep = 48

	// DefaultStoreWarnBytes is [retention] store_warn_bytes' default: the
	// on-disk size (internal/store.DB.SizeBytes — main file + WAL/SHM
	// sidecars, T-1903's existing size source) at which the
	// store_near_capacity finding (internal/findings) starts warning.
	// 4 GiB is chosen deliberately: vnprox runs on a PVE node's root
	// filesystem, shared with pmxcfs and the hypervisor's own writes, and
	// root partitions on real installs are commonly provisioned in the
	// tens of gigabytes, not hundreds — a vnprox store crossing several GiB
	// is already a strong signal that retention isn't keeping up,
	// regardless of how large the specific partition happens to be. An
	// operator with an unusually small or large root partition tunes this
	// directly; there is no attempt to introspect the actual filesystem
	// (statfs) here — that would be a second measurement of disk pressure
	// alongside the store's own size, which the task card explicitly asks
	// this finding to avoid ("reuse T-1903's size source").
	DefaultStoreWarnBytes int64 = 4 << 30 // 4 GiB

	// DefaultCapacityAggregateRetentionDays and
	// DefaultCapacityForecastHorizonDays are T-1606's documented [capacity]
	// defaults — mirror store.DefaultCapacityRetentionDays (400) and
	// capacity.DefaultForecastHorizonDays (90) so config defaults and those
	// packages' own constants agree without this package importing them just
	// for the numbers (the same mirror-not-import convention
	// DefaultSnapshotKeepDays uses above; config_test pins the equality).
	DefaultCapacityAggregateRetentionDays = 400
	DefaultCapacityForecastHorizonDays    = 90

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

	// DefaultBaselineProfileRetentionDays and DefaultBaselineLearnIntervalHours
	// are T-1601's [baseline] section defaults: a learned traffic baseline
	// (baseline_profiles) is retained 90 days — deliberately far longer than
	// flow_samples' own retention, so a learned shape outlives the raw flows
	// it was learned from — and the learn job recomputes baselines every 24h.
	DefaultBaselineProfileRetentionDays = 90
	DefaultBaselineLearnIntervalHours   = 24

	// DefaultBaselineAutoCaptureMaxPerWindow / DefaultBaselineAutoCaptureWindowMinutes
	// are T-4101's anomaly-triggered-capture storm cap: at most 3
	// auto-armed captures per rolling 60-minute window when
	// auto_capture_enabled = true (itself off by default) — conservative
	// enough that a genuine anomaly burst still gets SOME evidence
	// captured without an anomaly storm producing anywhere near a hundred
	// captures.
	DefaultBaselineAutoCaptureMaxPerWindow  = 3
	DefaultBaselineAutoCaptureWindowMinutes = 60

	// DefaultMCPPath is where T-1701's MCP Streamable-HTTP/SSE transport mounts
	// when [mcp] enabled = true (off by default). Under /api/v1 so it shares
	// the daemon's TLS/request-logging/security-header middleware, but it does
	// its own bearer-token auth (no session cookie / CSRF), the same exception
	// GET /metrics already carries.
	DefaultMCPPath = "/api/v1/mcp"
)

// Config is the fully parsed, defaulted, and validated daemon configuration.
// that adds a config section (28 already) and is constructed exactly once
// at startup — never a hot allocation path — so field order documents
// "one line per [section]", not packing.
//
//nolint:govet // fieldalignment: this struct grows by one field per task
type Config struct {
	PVE         PVEConfig
	Peer        PeerConfig
	Blueprint   BlueprintConfig
	Storage     StorageConfig
	FirewallLog FirewallLogConfig
	Safety      SafetyConfig
	Security    SecurityConfig
	Certs       CertsConfig
	OIDC        OIDCConfig
	GitSync     GitSyncConfig
	Changesets  ChangesetsConfig
	Hub         HubConfig
	Metrics     MetricsConfig
	HA          HAConfig
	Capture     CaptureConfig
	Server      ServerConfig
	Flows       FlowsConfig
	Retention   RetentionConfig
	Wan         WanConfig
	Latmesh     LatmeshConfig
	Collect     CollectConfig
	Capacity    CapacityConfig
	Baseline    BaselineConfig
	MTUProbe    MTUProbeConfig
	Webhooks    WebhooksConfig
	Switches    SwitchesConfig
	MCP         MCPConfig
	SIEMExport  SIEMExportConfig
	Demo        bool
}

// ChangesetsConfig is the [changesets] section (T-2003: change review —
// approvals, comments, side-by-side diff), generalizing T-1703's
// tenant-scoped request-changeset approval queue to every changeset. The
// zero value (ApprovalRequired false) is a complete no-op: every pre-T-2003
// deployment's apply behavior is byte-identical, since nothing gates apply
// on approval state unless an admin explicitly opts in.
type ChangesetsConfig struct {
	// Approvers, when non-empty, restricts who may record a review decision
	// to this exact list of identities (usernames). Empty (the default)
	// means anyone who can reach the route (i.e. anyone with netWrite) may
	// decide.
	// PolicyFile (T-2601) is the path to a declarative policy-as-code
	// document (`{id, description, severity, match, assert}` rules) the
	// daemon installs into the cluster's policy set at startup. Empty (the
	// default) means no policy file: the cluster keeps whatever rule set is
	// already installed, which for a fresh deployment is none at all —
	// nothing is refused that was not refused before.
	//
	// A configured file that cannot be parsed is FATAL at startup: a daemon
	// must never come up quietly enforcing a policy it could not read
	// (T-2601 acceptance criterion 5).
	PolicyFile string
	Approvers  []string
	// ProtectedClasses (T-2604) declares which classes of change require N
	// distinct approvers before apply. Each entry is
	// `[[changesets.protected_class]]` plus `class`/`approvals`, where class
	// is an op-type glob, "mgmtPath", or "tag:<policy rule tag>". Empty (the
	// default) is a complete no-op: no changeset is ever in a protected
	// class, so an upgrading install's apply behaviour is byte-identical
	// until an admin opts in. A malformed entry is FATAL at startup
	// (change.NewService refuses it) rather than dropped: a deployment must
	// never come up believing it has a gate it does not have — the same
	// reasoning that makes an unparsable policy file fatal.
	ProtectedClasses []ProtectedClassConfig
	// ApprovalRequired, when true, blocks POST /changesets/{id}/apply
	// server-side (change.Service's approval gate, apply.go's beginApply)
	// until the changeset carries a recorded "approved" review decision.
	ApprovalRequired bool
	// AllowSelfApproval, when false, refuses a review-approval decision made
	// by the changeset's own author (change.ErrSelfApprovalForbidden) —
	// mirrors internal/tenant's separation-of-duties rule for request-
	// changesets, generalized to every changeset. Defaults to true (self-
	// approval permitted) when the config file omits the key — the implicit
	// single-admin workflow every pre-T-2003 deployment already has, where
	// "the approver" and "the author" are routinely the same person.
	AllowSelfApproval bool
	// AutoRollbackOnError (T-2603) is the CLUSTER DEFAULT for the
	// finding-triggered auto-rollback: when a changeset's apply request does
	// not say either way, this decides whether a new `error` finding on an
	// entity the changeset touched rolls it back inside its commit-confirm
	// window. Defaults to false — off — so an upgrading install's apply
	// behaviour is byte-identical until an admin opts in, and a changeset can
	// still ask for the guard individually (`autoRollbackOnError` on the apply
	// body) regardless of what this says.
	AutoRollbackOnError bool
}

// ProtectedClassConfig is one `[[changesets.protected_class]]` entry.
type ProtectedClassConfig struct {
	// Class is an op-type glob ("fw.*", "sdn.*"), the reserved "mgmtPath",
	// or "tag:<tag>" naming a T-2601 policy rule's tag.
	Class string
	// Approvals is how many DISTINCT principals must approve. Omitted (or
	// below 2) means 2: listing a class unambiguously means "gate it", and a
	// two-person rule requiring one person is not one.
	Approvals int
}

// MCPConfig is the [mcp] section (T-1701): the read-only/stage-only MCP server
// for AI operators. Enabled is the daemon-level master flag; false (the
// default) ships the surface dark — no MCP HTTP route is mounted at all,
// matching [switches]' feature-flag-dark precedent. The transport mounts at the
// fixed DefaultMCPPath under /api/v1 and authenticates only via T-1104 bearer
// tokens carrying the `automation` scope; there is no MCP-specific credential to
// configure here, and no config key can widen it beyond reads + draft staging
// (that boundary is structural in internal/mcp, not a config toggle).
type MCPConfig struct {
	Enabled bool
}

// TelemetryConfig is the [telemetry] section (T-2503): the opt-in
// compatibility report `vnproxctl telemetry send` submits — check ids and
// verdicts, versions, kernel, NIC hardware ids and a node COUNT, and nothing
// else (docs/security.md, "Compatibility telemetry (T-2503)").
//
// It is deliberately NOT a field on Config. The daemon never sends
// telemetry — the sender is the operator's own `vnproxctl telemetry`, which
// reads this section through LoadTelemetryOnly — so vnproxd does not carry
// the endpoint in memory at all, and no future daemon-side code path can
// pick it up by accident from a config struct it already had. What Load
// DOES do is validate the section (ValidateTelemetry, below), so an opt-in
// with a typo in it is a loud startup failure on the node the operator
// edited rather than a silence they discover months later.
//
// The zero value is the shipped default and it is off in two independent
// ways: Enabled is false, and Endpoint is empty. vnprox ships NO default
// collector URL — there is no vnprox telemetry service, and a build that
// carried an endpoint would be one config-parsing bug away from contacting
// it. Turning telemetry on therefore requires an operator to name the
// collector themselves, which is what makes "no endpoint is contacted until
// an operator sets it" checkable rather than promised.
//
// enabled = true with no endpoint is FATAL rather than silently off: an
// operator who opted in and got nothing would have no way to tell that from
// a collector that never answered.
type TelemetryConfig struct {
	// Endpoint is the collector's absolute https:// URL. Required when
	// Enabled; https is required — a compatibility report is not secret, but
	// it carries the install-id correlator, and a plaintext default path
	// invites a network that rewrites it.
	Endpoint string
	// Enabled is the master switch. False — the default, and the value in
	// the shipped packaging/config/vnprox.toml — means no payload is built,
	// no store read for the install-id, and no request made.
	Enabled bool
}

// HAConfig is the [ha] section (T-1704: active/standby daemon high
// availability). Disabled by default (Enabled false) — a single-daemon
// deployment behaves exactly as it did pre-T-1704. When enabled, exactly one
// daemon in the pair sets bootstrap = true so a first boot never has both
// claim term 1. PeerAddr is the standby's host:port for the replication push;
// Mode selects the failover-announce mechanism ("vip" runs VipCommand, "dns"
// posts to DNSWebhook). Lease timings default sensibly (see internal/ha) when
// unset.
type HAConfig struct {
	InstanceID    string
	Mode          string
	PeerNode      string
	PeerAddr      string
	VipCommand    string
	DNSWebhook    string
	LeaseTTL      time.Duration
	RenewInterval time.Duration
	FencingMargin time.Duration
	LagThreshold  int
	Enabled       bool
	Bootstrap     bool
}

// HubConfig is the [hub] section (T-1705): the opt-in Blueprint & plugin hub
// client. RegistryURL is the base URL of the public registry whose
// `GET <registry>/index.json` contract the client browses/installs from — empty
// (the default) leaves the hub disabled (its routes still mount but return an
// empty catalog / "not available"). VettedSigners is the hub's own
// recognized-signer fingerprint allowlist, driving only the informational
// "vetted" badge — it is distinct from T-1107's per-admin trust store
// ([blueprint] trusted_signers_dir) and never bypasses that trust decision
// (docs/security.md's Hub vetted-tier note).
//
// IndexSigners (T-2803) is the fingerprint allowlist for the *registry's own
// index signing key*. When it is non-empty the daemon installs the T-2803
// index gate: the index must be signed by one of these keys or nothing is
// browsable, and the revocations carried inside that signed index are enforced
// on every artifact fetch. It is a third, distinct list from the two above —
// it says who may publish the *catalog*, never who may sign an *artifact*, and
// it can no more make an artifact installable than the vetted badge can.
//
// TrustUnsigned (T-2904) is `trust_unsigned`: whether POST /hub/install may
// honor a request's `trustUnsigned: true` flag for an UNSIGNED hub artifact.
// Off — the default — refuses such a request with an error naming this key;
// the request field stays schema-valid, it is just never sufficient on its
// own. On, the daemon warns at startup (the [peer] tls_trust precedent) and
// the existing explicit-trust flow applies. It has no effect whatsoever on
// signed artifacts: signature verification is never optional.
//
// This struct deliberately has no Sigstore field of any kind (T-3709). An
// operator can obtain an IndexSigners fingerprint via a Sigstore-verified,
// Fulcio/Rekor-logged key-custody attestation instead of an unverifiable
// side channel — but that verification (internal/hubreg/sigstoreverify)
// runs only inside vnproxctl, as an explicit, separate operator command
// (`vnproxctl hub verify --sigstore-key-bundle`), and its result is a
// fingerprint an operator copies into IndexSigners by hand. The daemon
// itself never performs Sigstore verification and has no config surface
// for it — see docs/hub-registry.md's "Sigstore-backed key custody"
// section and cmd/vnproxd's TestVnproxdDoesNotImportSigstore, which is
// this package's own half of the reason: pulling sigstore-go's dependency
// tree into a package cmd/vnproxd imports would defeat the whole split.
type HubConfig struct {
	RegistryURL   string
	VettedSigners []string
	IndexSigners  []string
	TrustUnsigned bool
}

// GitSyncConfig is the [gitsync] section (T-2701): a git repository as the
// source of *intent* for the declarative cluster network spec.
//
// **Off by default and off by construction.** Enabled false — the zero value
// — means nothing is fetched and no endpoint is contacted, ever; the poll
// loop blocks on shutdown and does nothing else. Proxmox remains the source
// of truth: when the repository and the cluster disagree, vnprox opens a
// DRAFT changeset for a human and stops. Nothing in this section can make
// the file authoritative over live config.
type GitSyncConfig struct {
	// URL is the repository (or, for provider = "raw", the directory base)
	// to read from. https only, except on loopback. A URL that embeds
	// credentials is refused: use TokenFile.
	URL string
	// Provider selects the read surface: "github", "gitlab" or "raw".
	// Empty infers from the host for github.com/gitlab.com and is a startup
	// error for anything else — guessing an API shape from an unknown host
	// is how a sync silently reads the wrong thing.
	Provider string
	// Ref is the branch, tag or sha to read. Defaults to "main".
	Ref string
	// Path is the spec document's path within the repository.
	Path string
	// TokenFile is a root:root 0600 file containing the host credential —
	// the same on-disk-secret convention [oidc] client_secret_file and
	// [pve] token_file already use. The credential is never written into
	// this config file, never placed in a URL, and never logged.
	//
	// It is READ-ONLY scope: everything T-2701 does is a fetch. Proposing a
	// changeset needs PushTokenFile below instead — a separate key holding a
	// separate credential, so turning on proposals is an explicit act and a
	// sync-only deployment's token is never silently widened.
	TokenFile string
	// PushTokenFile (T-2702) is a root:root 0600 file containing a
	// WRITE-scoped host credential: the one used to push a branch and open a
	// pull request for a proposed changeset. Empty — the default — means
	// POST /changesets/{id}/propose answers "not configured" and no write
	// credential is ever read from disk, let alone used.
	PushTokenFile string
	// AllowedSignersFile is an OpenSSH allowed-signers (or authorized_keys)
	// file listing the keys whose commit signatures are trusted. Required
	// when RequireSignedCommits is set.
	AllowedSignersFile string
	// PollInterval is the fetch cadence. Zero takes the package default.
	PollInterval time.Duration
	// Enabled is the master switch. False leaves the whole subsystem inert.
	// The two bools sit last so the struct packs; Enabled is nonetheless the
	// first key an operator writes (docs/deployment.md).
	Enabled bool
	// RequireSignedCommits refuses a commit whose signature this daemon
	// cannot verify locally against AllowedSignersFile. It fails closed: an
	// unsigned commit, an unsupported signature format, and a host that
	// cannot supply the signed commit object are all refusals.
	RequireSignedCommits bool
}

// SecurityConfig is the [security] section (T-1605: rogue-service detection).
// ProtectedSegments lists the bridge/segment names an operator has flagged
// protected — a MAC learned on one of them that matches no known
// guest/PhysNic/LLDP-neighbor in the inventory raises the
// unknown_mac_protected_segment finding (source "rogue"). Empty (the default)
// leaves that check disabled; the other three rogue checks do not depend on
// it.
type SecurityConfig struct {
	ProtectedSegments []string
}

// BaselineConfig is the [baseline] section (T-1601): the flow-baselining
// subsystem's own retention/scheduling knobs. Learning/detecting is always on
// (like [latmesh]/[mtuprobe] — learning from already-ingested flow_samples
// adds no external attack surface, so there's no opt-in gate to carry here).
// ProfileRetentionDays caps how long a learned baseline_profiles row is kept
// (default 90); LearnIntervalHours is the learn job's cadence (default 24).
// Both default from DefaultBaseline* when unset/non-positive.
//
// AutoCapture* (T-4101) is different: arming a bounded internal/capture
// session automatically, with no operator in the loop, IS a new decision a
// machine makes about a privacy/resource-sensitive action — so unlike the
// fields above, it is off by default (AutoCaptureEnabled's zero value) and
// needs an explicit opt-in distinct from [capture]'s own general
// availability. AutoCaptureMaxPerWindow/AutoCaptureWindowMinutes bound how
// many anomaly-armed captures may fire in a sliding window (default 3 per
// 60 minutes) — the storm cap: an anomaly burst (or every still-firing
// anomaly looking "new" again right after a daemon restart, since the
// tracker's new/seen state is in-memory only) must not produce a hundred
// captures. This is a NEW cap the trigger itself owns — it does not touch
// or duplicate internal/capture's own duration/size/packet-count ceilings,
// which every anomaly-armed session inherits completely unmodified
// (cmd/vnproxd/autocapture.go never sets DurationSec/MaxBytes/MaxPackets on
// its capture.StartRequest, so capture.Coordinator.clampCaps applies the
// exact same ceiling a manual session gets).
type BaselineConfig struct {
	ProfileRetentionDays     int
	LearnIntervalHours       int
	AutoCaptureMaxPerWindow  int
	AutoCaptureWindowMinutes int
	AutoCaptureEnabled       bool
}

// OIDCConfig is the [oidc] section (T-1207: OIDC SSO). Enabled is derived —
// true iff Issuer is set — so a deployment with no [oidc] section keeps the
// PVE-ticket-bridge-only login flow unchanged. The client secret is loaded from
// ClientSecretFile (a root:root 0600 file, the same on-disk-secret convention
// the session/metrics keys use), never inlined in the config in the clear. The
// group→role mapping table (Groups) maps OIDC group claims to vnprox capability
// bundles; the per-cluster OIDC-group→PVE-identity linkage that caps those
// bundles at real PVE ACLs lives in the encrypted oidc_pve_links store table,
// not here (docs/security.md's Authentication section).
type OIDCConfig struct {
	Issuer           string
	ClientID         string
	ClientSecretFile string
	RedirectURL      string
	GroupsClaim      string
	ClusterID        string
	Scopes           []string
	Groups           []OIDCGroupMapping
	Enabled          bool
}

// OIDCGroupMapping maps one OIDC group-claim value to a set of capability
// names (from internal/auth/caps.go's vocabulary: netRead, netWrite, sdnRead,
// sdnWrite, fwRead, fwWrite, guestNet, audit). cmd/vnproxd translates this into
// an internal/auth.GroupMapping, validating each cap name at startup.
type OIDCGroupMapping struct {
	Group string
	Caps  []string
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
	Listen      string
	TLSCert     string
	TLSKey      string
	TLSCertPath string
	TLSKeyPath  string

	// EmbedFrameAncestors (T-2901, TOML `embed_frame_ancestors`) lists the
	// origins allowed to iframe the /embed/* views in addition to
	// same-origin — each is emitted verbatim into those routes'
	// `frame-ancestors 'self' ...` CSP directive (internal/api). Empty or
	// absent means same-origin embedding only; every other route keeps
	// `frame-ancestors 'none'` regardless. Entries must be
	// scheme://host[:port] origins; anything else fails validate().
	EmbedFrameAncestors []string

	ConfirmTimeoutDefault int

	// DevLoginRateCapacity and DevLoginRateRefillSeconds are a
	// dev/testing-only override for the login brute-force limiter
	// (internal/auth.DefaultRateLimitConfig: 10 attempts, one token back
	// every 30s). Zero means "use the production default", so no shipped
	// config is affected.
	//
	// This exists for the Playwright suite (T-2108). Its 89 specs log in
	// ~82 times against one daemon from 127.0.0.1 as the same user, which
	// exceeds the refill rate and earns three HTTP 429s partway through the
	// run — surfacing as specs that time out waiting for a post-login
	// navigation, and *only* in full-suite runs. The limiter is behaving
	// correctly; a real operator logs in once and keeps a session. Rather
	// than weaken the production default or make 36 spec files share a
	// storageState, the e2e config raises it and everything else keeps the
	// shipped behaviour.
	//
	// Deliberately prefixed `dev_` like [pve]'s dev_ticket_* overrides, so
	// it reads as what it is in any config it appears in.
	DevLoginRateCapacity      int
	DevLoginRateRefillSeconds int

	// LoginRateUsernameCapacity and LoginRateUsernameRefillSeconds
	// (T-3303) override ONLY the login limiter's per-username bucket; the
	// per-IP bucket keeps the production default regardless. Zero means
	// "same as the per-IP bucket" (today's behavior, unaffected). Unlike
	// DevLoginRate* above, this is a supported production setting, not a
	// test-only escape hatch — it does not touch per-IP brute-force
	// protection for any account.
	//
	// Written for a public demo instance (internal/publicdemo): every
	// visitor's session is minted against the SAME low-privilege,
	// intentionally-public fixture credential, so the per-username bucket
	// throttles the whole instance's visitor onboarding rate globally
	// (10 new visitors per refill window, instance-wide) independent of how
	// many distinct real IPs are arriving — a false shared-fate failure
	// mode the per-IP bucket doesn't have, since it is keyed on each
	// visitor's own real address. Widening just this bucket for that one
	// shared credential removes it without touching any other account's
	// per-username protection.
	LoginRateUsernameCapacity      int
	LoginRateUsernameRefillSeconds int

	ReadOnly bool
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

// SwitchesConfig is the [switches] section (T-1205: guarded switch config
// push). Enabled is the daemon-level master flag gating the entire switch-push
// feature: false (the default) means no switch.port.update can ever be applied,
// regardless of any individual switch's own enabled flag in the switches table
// — switch push ships dark by construction (docs/security.md). This is a
// deliberate two-key interlock (daemon flag AND per-switch opt-in), so a stray
// enabled switch row can never by itself make pushes possible.
type SwitchesConfig struct {
	Enabled bool
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

	// CAFile is the trust anchor peer-API TLS pins (T-1906), defaulting to
	// peer.DefaultClusterCAPath (`/etc/pve/pve-root-ca.pem`) — the CA that
	// issues the PVE certificates real peer daemons serve
	// (docs/architecture.md §9). TOML key `ca_file`.
	CAFile string

	// TLSTrust is `tls_trust`: how a peer daemon's TLS certificate is
	// verified. The zero value is peer.TrustClusterCA — pinned. The two
	// non-default values are escape hatches for a host that genuinely has no
	// /etc/pve, and each requires its own exact TLSTrustAck literal, so no
	// single edit, unset key, absent file, or environment variable can turn
	// pinning off (T-1906 AC3). Load fails outright on an unknown value or a
	// missing/mismatched ack rather than quietly picking either the requested
	// or the safe mode.
	TLSTrust peer.TrustMode

	// TLSTrustAck is `tls_trust_ack`: the mode-specific acknowledgement
	// literal (peer.AckSystem / peer.AckInsecure) a non-default TLSTrust
	// requires.
	TLSTrustAck string
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
// T-206's retention job enforces (internal/store.SnapshotRetention), plus
// T-1905's audit_log age ceiling and the store_near_capacity finding's
// warning threshold.
type RetentionConfig struct {
	SnapshotKeepDays int
	SnapshotPinDays  int
	// AuditKeepDays bounds audit_log by age (internal/store.AuditRepo.
	// PruneRetention); no separate pin floor, unlike snapshots — an audit
	// row carries no rollback dependency (docs/data-model.md's retention
	// section). Must be positive; there is no "0 = forever" escape hatch,
	// matching SnapshotKeepDays/SnapshotPinDays's existing validation
	// stance — see DefaultAuditKeepDays for the argument.
	AuditKeepDays int
	// StoreWarnBytes is the app store's on-disk size (main file + WAL/SHM,
	// internal/store.DB.SizeBytes) at or above which the
	// store_near_capacity finding fires (internal/findings). See
	// DefaultStoreWarnBytes for the argument.
	StoreWarnBytes int64
	// SnapshotScheduleInterval (T-2401) is how often vnprox captures an
	// automatic `scheduled` snapshot of every node's interfaces file — the
	// restore point for a change vnprox did NOT make (an `ssh node && vi
	// /etc/network/interfaces && ifreload -a`).
	//
	// ZERO MEANS OFF, and that is the default. Unlike every other value in
	// this struct, 0 is a legitimate setting rather than a validation
	// failure: a capture is a full read of every node's config file, so the
	// operator opts in. See internal/change/snapshots_scheduled.go.
	SnapshotScheduleInterval time.Duration
	// SnapshotScheduleKeep bounds how many automatic captures are retained,
	// oldest pruned first. Count-based rather than age-based because "keep
	// the last N automatic captures" is the policy an operator can reason
	// about for something that fires on a timer. Scoped to the `scheduled`
	// kind in SQL: it can never delete a changeset's rollback point or a
	// human's manual snapshot.
	SnapshotScheduleKeep int
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
// WebhooksConfig is the [webhooks] section (T-2905): the destination
// policy for T-1005's outbound webhook deliveries. Both knobs default off
// — public https targets only — and each one active is WARNed at startup,
// the same loud-escape-hatch posture [peer] tls_trust and
// [hub] trust_unsigned use. AllowPrivateTargets admits loopback/RFC1918/
// link-local (incl. cloud metadata addresses) destinations;
// AllowInsecureTargets admits plain http.
type WebhooksConfig struct {
	AllowPrivateTargets  bool
	AllowInsecureTargets bool
}

type FlowsConfig struct {
	// DevFixtureDir, when non-empty, seeds flow_samples at startup from a
	// static corpus of JSON fixture files in this directory (T-3706) —
	// dev-only, mirroring [firewalllog]'s DevFixtureDir: production
	// configs never set it, and a dev daemon has no real UDP exporters or
	// host conntrack table to seed a historical corpus for the
	// microsegmentation planner (internal/microseg) to synthesize a
	// proposal from. Unlike DevFixtureDir's fwlog sibling, this does NOT
	// replace the real ingestion path — [flows]' listeners/samplers stay
	// exactly as configured; the fixture corpus is additive seed data
	// only. See cmd/vnproxd/flows_fixture.go.
	DevFixtureDir            string
	MaxRows                  int64
	SFlowPort                int
	NetFlowPort              int
	IPFIXPort                int
	RetentionMinutes         int
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

// CapacityConfig is the [capacity] section (T-1606): the capacity-forecasting
// rollup's own bounds. This card owns the network-intelligence arc's ONE
// deliberate retention extension, so its knobs live in their own section
// rather than reusing a raw-sample ring's [flows]/[latmesh]-style
// retention_minutes/max_rows — the aggregate is bounded by an age cap in
// *days*, not minutes/rows. AggregateRetentionDays defaults to
// store.DefaultCapacityRetentionDays (400, ~13 months); ForecastHorizonDays
// to capacity.DefaultForecastHorizonDays (90) — a forecast fires only when a
// projected capacity crossing falls within this many days.
type CapacityConfig struct {
	AggregateRetentionDays int
	ForecastHorizonDays    int
}

// CertsConfig is the [certs] section (T-2301): the cluster certificate
// inventory's own knobs. Both have safe defaults and exist mainly so a dev
// daemon with no /etc/pve, and a deployment with a different renewal cadence,
// need no code change.
type CertsConfig struct {
	// Root is the pmxcfs mount the inventory reads. Empty means
	// certs.DefaultRoot (/etc/pve). TOML key `root`.
	Root string
	// ExpiryWarnDays is how far ahead cert_expiring looks. Zero means
	// certs.DefaultExpiryWarn (30 days). TOML key `expiry_warn_days`.
	ExpiryWarnDays int
}

// ExpiryWarn converts ExpiryWarnDays to a duration; zero stays zero so the
// certs package applies its own default rather than this one silently
// deciding the policy in two places.
func (c CertsConfig) ExpiryWarn() time.Duration {
	if c.ExpiryWarnDays <= 0 {
		return 0
	}
	return time.Duration(c.ExpiryWarnDays) * 24 * time.Hour
}

// SIEMExportConfig is the [siemexport] section (T-4012): the bounded,
// best-effort streaming export of vnprox's audit log and findings stream
// to an operator-run SIEM (internal/siemexport's own doc.go has the full
// design). Off by default, per this product's opt-in convention for
// anything that sends data to an operator-chosen external destination
// ([telemetry], [mcp]) — the zero value (Enabled false) is a complete
// no-op, byte-identical to a pre-T-4012 daemon.
//
// Exactly one destination shape is active at a time, chosen by Format:
// "syslog" ships to Network+Address only (Path must be empty — syslog has
// no file-destination mode); "jsonl" ships to EITHER Path (a local file,
// opened append-only) OR Network+Address (a socket), never both — see
// resolveSIEMExportConfig for the exact refusal rules. This mirrors
// GitSyncConfig's own asymmetry: a disabled section is never checked (an
// operator who never turned this on must never see an error from it), an
// *enabled* one is checked strictly and fatally, because a daemon that
// came up believing it was exporting but silently was not is worse than a
// daemon that refused to start.
// field (format/network/address/path, then the two int knobs, then
// Enabled last) for readability; this is a config value read once at
// startup, never a hot allocation path.
//
//nolint:govet // fieldalignment: field order groups doc comments with their
type SIEMExportConfig struct {
	// Format is "syslog" (RFC 5424) or "jsonl" (newline-delimited JSON).
	Format string
	// Network is the destination transport: "tcp" | "udp" for syslog;
	// "tcp" | "udp" | "unix" for jsonl (when Path is empty).
	Network string
	// Address is host:port (tcp/udp) or a socket path (unix).
	Address string
	// Path is a jsonl-only local file destination, mutually exclusive
	// with Network/Address.
	Path string
	// BufferSize bounds the in-memory ring buffer siemexport.Exporter
	// drops the oldest event from once full. Zero/unset resolves to
	// siemexport.DefaultBufferSize.
	BufferSize int
	// Facility is the RFC 5424 facility number (0-23), syslog format
	// only. Zero/unset resolves to 16 (local0) — see
	// resolveSIEMExportConfig's firstNonZeroInt call; an operator who
	// really wants facility 0 (kern) must say so explicitly, the same
	// "zero means unset, not a deliberate choice" precedent
	// ChangesetsConfig.AllowSelfApproval's doc comment documents for its
	// own *bool.
	Facility int
	Enabled  bool
}

// rawConfig mirrors the TOML shape exactly (string durations, string paths)
// before defaulting/validation/type conversion.
//
// [section], grown per task) — same "constructed once at startup, never a
// hot path" rationale.
//
//nolint:govet // fieldalignment: mirrors Config's own struct (one line per
type rawConfig struct {
	PVE         rawPVE         `toml:"pve"`
	Peer        rawPeer        `toml:"peer"`
	Collect     rawCollect     `toml:"collect"`
	FirewallLog rawFirewallLog `toml:"firewalllog"`
	Blueprint   rawBlueprint   `toml:"blueprint"`
	Storage     rawStorage     `toml:"storage"`
	OIDC        rawOIDC        `toml:"oidc"`
	GitSync     rawGitSync     `toml:"gitsync"`
	Metrics     rawMetrics     `toml:"metrics"`
	Safety      rawSafety      `toml:"safety"`
	Certs       rawCerts       `toml:"certs"`
	Telemetry   rawTelemetry   `toml:"telemetry"`
	Security    rawSecurity    `toml:"security"`
	HA          rawHA          `toml:"ha"`
	Changesets  rawChangesets  `toml:"changesets"`
	Hub         rawHub         `toml:"hub"`
	Retention   rawRetention   `toml:"retention"`
	Capture     rawCapture     `toml:"capture"`
	Server      rawServer      `toml:"server"`
	Flows       rawFlows       `toml:"flows"`
	Wan         rawWan         `toml:"wan"`
	Latmesh     rawLatmesh     `toml:"latmesh"`
	Capacity    rawCapacity    `toml:"capacity"`
	Baseline    rawBaseline    `toml:"baseline"`
	MTUProbe    rawMTUProbe    `toml:"mtuprobe"`
	Webhooks    rawWebhooks    `toml:"webhooks"`
	Switches    rawSwitches    `toml:"switches"`
	MCP         rawMCP         `toml:"mcp"`
	SIEMExport  rawSIEMExport  `toml:"siemexport"`
}

// rawChangesets mirrors [changesets] (T-2003). AllowSelfApproval is a *bool
// (not a plain bool) for the same reason rawMetrics.Enabled is: Load must
// distinguish "not set in the file" (default true — self-approval permitted)
// from an explicit "allow_self_approval = false", which a plain bool
// (defaulting to false either way) can't do.
type rawChangesets struct {
	AllowSelfApproval *bool    `toml:"allow_self_approval"`
	PolicyFile        string   `toml:"policy_file"`
	Approvers         []string `toml:"approvers"`
	// ProtectedClass (T-2604) mirrors `[[changesets.protected_class]]` — an
	// array of tables rather than a map, so each entry reads as a sentence
	// ("class = \"fw.*\"", "approvals = 2") instead of as a quoted key whose
	// dots and asterisks TOML would otherwise make the reader parse.
	ProtectedClass   []rawProtectedClass `toml:"protected_class"`
	ApprovalRequired bool                `toml:"approval_required"`
	// AutoRollbackOnError (T-2603) is the cluster default for the
	// finding-triggered rollback inside the commit-confirm window. A plain
	// bool is right here (unlike AllowSelfApproval above): the documented
	// default is OFF, which is also the zero value, so "not set" and
	// "explicitly false" mean the same thing and never need distinguishing.
	AutoRollbackOnError bool `toml:"auto_rollback_on_error"`
}

type rawProtectedClass struct {
	Class     string `toml:"class"`
	Approvals int    `toml:"approvals"`
}

type rawMCP struct {
	Enabled bool `toml:"enabled"`
}

// rawTelemetry is [telemetry] (T-2503). There is deliberately no default for
// `endpoint`: an absent section decodes to the zero value, which is off with
// nowhere to send to.
type rawTelemetry struct {
	Endpoint string `toml:"endpoint"`
	Enabled  bool   `toml:"enabled"`
}

type rawGitSync struct {
	URL                  string `toml:"url"`
	Provider             string `toml:"provider"`
	Ref                  string `toml:"ref"`
	Path                 string `toml:"path"`
	PollInterval         string `toml:"poll_interval"`
	TokenFile            string `toml:"token_file"`
	PushTokenFile        string `toml:"push_token_file"`
	AllowedSignersFile   string `toml:"allowed_signers_file"`
	Enabled              bool   `toml:"enabled"`
	RequireSignedCommits bool   `toml:"require_signed_commits"`
}

// rawSIEMExport is [siemexport] (T-4012). See SIEMExportConfig's doc
// comment for the field shapes and resolveSIEMExportConfig for the
// validation rules.
type rawSIEMExport struct {
	Format     string `toml:"format"`
	Network    string `toml:"network"`
	Address    string `toml:"address"`
	Path       string `toml:"path"`
	BufferSize int    `toml:"buffer_size"`
	Facility   int    `toml:"facility"`
	Enabled    bool   `toml:"enabled"`
}

type rawHA struct {
	InstanceID    string `toml:"instance_id"`
	Mode          string `toml:"mode"`
	PeerNode      string `toml:"peer_node"`
	PeerAddr      string `toml:"peer_address"`
	VipCommand    string `toml:"vip_command"`
	DNSWebhook    string `toml:"dns_webhook"`
	LeaseTTL      string `toml:"lease_ttl"`
	RenewInterval string `toml:"renew_interval"`
	FencingMargin string `toml:"fencing_margin"`
	LagThreshold  int    `toml:"replication_lag_threshold"`
	Enabled       bool   `toml:"enabled"`
	Bootstrap     bool   `toml:"bootstrap"`
}

type rawCapacity struct {
	AggregateRetentionDays int `toml:"aggregate_retention_days"`
	ForecastHorizonDays    int `toml:"forecast_horizon_days"`
}

type rawSecurity struct {
	ProtectedSegments []string `toml:"protected_segments"`
}

type rawBaseline struct {
	ProfileRetentionDays     int  `toml:"profile_retention_days"`
	LearnIntervalHours       int  `toml:"learn_interval_hours"`
	AutoCaptureMaxPerWindow  int  `toml:"auto_capture_max_per_window"`
	AutoCaptureWindowMinutes int  `toml:"auto_capture_window_minutes"`
	AutoCaptureEnabled       bool `toml:"auto_capture_enabled"`
}

type rawOIDC struct {
	Issuer           string         `toml:"issuer"`
	ClientID         string         `toml:"client_id"`
	ClientSecretFile string         `toml:"client_secret_file"`
	RedirectURL      string         `toml:"redirect_url"`
	GroupsClaim      string         `toml:"groups_claim"`
	ClusterID        string         `toml:"cluster_id"`
	Scopes           []string       `toml:"scopes"`
	Groups           []rawOIDCGroup `toml:"group"`
}

type rawOIDCGroup struct {
	Name string   `toml:"name"`
	Caps []string `toml:"caps"`
}

type rawCapture struct {
	Root                  string `toml:"root"`
	MaxDurationSec        int    `toml:"max_duration_sec"`
	MaxBytes              int64  `toml:"max_bytes"`
	MaxPackets            int64  `toml:"max_packets"`
	RetentionHours        int    `toml:"retention_hours"`
	MaxFilterInstructions int    `toml:"max_filter_instructions"`
}

type rawSwitches struct {
	Enabled bool `toml:"enabled"`
}

type rawServer struct {
	Listen                         string   `toml:"listen"`
	TLSCert                        string   `toml:"tls_cert"`
	TLSKey                         string   `toml:"tls_key"`
	EmbedFrameAncestors            []string `toml:"embed_frame_ancestors"`
	ConfirmTimeoutDefault          int      `toml:"confirm_timeout_default"`
	DevLoginRateCapacity           int      `toml:"dev_login_rate_capacity"`
	DevLoginRateRefillSeconds      int      `toml:"dev_login_rate_refill_seconds"`
	LoginRateUsernameCapacity      int      `toml:"login_rate_username_capacity"`
	LoginRateUsernameRefillSeconds int      `toml:"login_rate_username_refill_seconds"`
	ReadOnly                       bool     `toml:"read_only"`
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

// Field order is packing-driven (govet's fieldalignment): the one
// pointer-bearing field first, so the GC's scan prefix is 8 bytes rather than
// 40. TOML decoding is key-based, so order here carries no meaning.
type rawRetention struct {
	SnapshotScheduleInterval string `toml:"snapshot_schedule_interval"`
	StoreWarnBytes           int64  `toml:"store_warn_bytes"`
	SnapshotKeepDays         int    `toml:"snapshot_keep_days"`
	SnapshotPinDays          int    `toml:"snapshot_pin_days"`
	AuditKeepDays            int    `toml:"audit_keep_days"`
	SnapshotScheduleKeep     int    `toml:"snapshot_schedule_keep"`
}

type rawPeer struct {
	SecretPath  string `toml:"secret_path"`
	CAFile      string `toml:"ca_file"`
	TLSTrust    string `toml:"tls_trust"`
	TLSTrustAck string `toml:"tls_trust_ack"`
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

type rawHub struct {
	RegistryURL   string   `toml:"registry_url"`
	VettedSigners []string `toml:"vetted_signers"`
	IndexSigners  []string `toml:"index_signers"`
	TrustUnsigned bool     `toml:"trust_unsigned"`
}

type rawWebhooks struct {
	AllowPrivateTargets  bool `toml:"allow_private_targets"`
	AllowInsecureTargets bool `toml:"allow_insecure_targets"`
}

type rawFlows struct {
	// DevFixtureDir leads: govet's fieldalignment wants the pointer-bearing
	// field ahead of the scalars (T-3706 added it and tripped the check).
	DevFixtureDir            string `toml:"dev_fixture_dir"`
	MaxRows                  int64  `toml:"max_rows"`
	SFlowPort                int    `toml:"sflow_port"`
	NetFlowPort              int    `toml:"netflow_port"`
	IPFIXPort                int    `toml:"ipfix_port"`
	RetentionMinutes         int    `toml:"retention_minutes"`
	HostSampleIntervalSec    int    `toml:"host_sample_interval_sec"`
	SFlowEnabled             bool   `toml:"sflow_enabled"`
	NetFlowEnabled           bool   `toml:"netflow_enabled"`
	IPFIXEnabled             bool   `toml:"ipfix_enabled"`
	ConntrackSamplingEnabled bool   `toml:"conntrack_sampling_enabled"`
	EBPFSamplingEnabled      bool   `toml:"ebpf_sampling_enabled"`
}

type rawLatmesh struct {
	ProbeIntervalSec int   `toml:"probe_interval_sec"`
	RetentionMinutes int   `toml:"retention_minutes"`
	MaxRows          int64 `toml:"max_rows"`
}

type rawMTUProbe struct {
	ProbeIntervalSec int `toml:"probe_interval_sec"`
}

type rawCerts struct {
	Root           string `toml:"root"`
	ExpiryWarnDays int    `toml:"expiry_warn_days"`
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
	return loadBytes(data, path, logger)
}

// loadBytes is Load's body, split out so LoadDemo can build a daemon config
// from a document it holds in memory rather than requiring one on disk.
// path is used only for error/log messages.
func loadBytes(data []byte, path string, logger *slog.Logger) (*Config, error) {
	var raw rawConfig
	meta, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, fmt.Errorf("%w: parsing config file %s: %v", ErrInvalidConfig, path, err)
	}

	for _, key := range meta.Undecoded() {
		logger.Warn("config: unrecognized key, ignoring", "key", key.String(), "file", path)
	}

	// T-2503: validated here rather than in validate() because the resolved
	// value is deliberately not carried on Config — see TelemetryConfig. An
	// opt-in the CLI would refuse must not be one the daemon starts on.
	if telemetryErr := ValidateTelemetry(resolveTelemetryConfig(raw.Telemetry)); telemetryErr != nil {
		return nil, fmt.Errorf("in %s: %w", path, telemetryErr)
	}

	collect, err := resolveCollectConfig(raw.Collect)
	if err != nil {
		return nil, err
	}
	metricsCfg, err := resolveMetricsConfig(raw.Metrics)
	if err != nil {
		return nil, err
	}
	haCfg, err := resolveHAConfig(raw.HA)
	if err != nil {
		return nil, err
	}
	peerCfg, err := resolvePeerConfig(raw.Peer)
	if err != nil {
		return nil, err
	}
	gitSyncCfg, err := resolveGitSyncConfig(raw.GitSync)
	if err != nil {
		return nil, err
	}
	siemExportCfg, err := resolveSIEMExportConfig(raw.SIEMExport)
	if err != nil {
		return nil, err
	}
	// T-2401: absent/"" means OFF (the default), so this cannot go through
	// parseDurationOrDefault, which treats a non-positive duration as an
	// error. A malformed string is still fatal — an operator who wrote
	// "1hour" must not silently get "disabled".
	var snapshotScheduleInterval time.Duration
	if raw.Retention.SnapshotScheduleInterval != "" {
		snapshotScheduleInterval, err = time.ParseDuration(raw.Retention.SnapshotScheduleInterval)
		if err != nil {
			return nil, fmt.Errorf("%w: retention.snapshot_schedule_interval %q: %v",
				ErrInvalidConfig, raw.Retention.SnapshotScheduleInterval, err)
		}
	}

	cfg := &Config{
		Server: ServerConfig{
			Listen:                firstNonEmpty(raw.Server.Listen, DefaultListen),
			ReadOnly:              raw.Server.ReadOnly,
			ConfirmTimeoutDefault: firstNonZeroInt(raw.Server.ConfirmTimeoutDefault, DefaultConfirmTimeoutDefault),
			TLSCert:               raw.Server.TLSCert,
			TLSKey:                raw.Server.TLSKey,
			// Zero stays zero: internal/auth falls back to its production
			// default, so omitting these keys changes nothing.
			DevLoginRateCapacity:      raw.Server.DevLoginRateCapacity,
			DevLoginRateRefillSeconds: raw.Server.DevLoginRateRefillSeconds,
			// T-3303: zero stays zero — falls back to the per-IP bucket's
			// config, same as before this field existed.
			LoginRateUsernameCapacity:      raw.Server.LoginRateUsernameCapacity,
			LoginRateUsernameRefillSeconds: raw.Server.LoginRateUsernameRefillSeconds,
			// T-2901: nil stays nil — same-origin embedding only.
			EmbedFrameAncestors: raw.Server.EmbedFrameAncestors,
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
		Switches: SwitchesConfig{
			Enabled: raw.Switches.Enabled,
		},
		Changesets: ChangesetsConfig{
			ApprovalRequired:  raw.Changesets.ApprovalRequired,
			AllowSelfApproval: raw.Changesets.AllowSelfApproval == nil || *raw.Changesets.AllowSelfApproval,
			Approvers:         raw.Changesets.Approvers,
			PolicyFile:        raw.Changesets.PolicyFile,
			// T-2603: off unless the file says otherwise.
			AutoRollbackOnError: raw.Changesets.AutoRollbackOnError,
			// T-2604: empty unless the file declares protected classes.
			ProtectedClasses: protectedClasses(raw.Changesets.ProtectedClass),
		},
		Security: SecurityConfig{
			ProtectedSegments: raw.Security.ProtectedSegments,
		},
		Storage: StorageConfig{
			DBPath:         firstNonEmpty(raw.Storage.DBPath, DefaultDBPath),
			SessionKeyFile: firstNonEmpty(raw.Storage.SessionKeyFile, DefaultSessionKeyFile),
		},
		Peer: peerCfg,
		HA:   haCfg,
		Retention: RetentionConfig{
			SnapshotKeepDays: firstNonZeroInt(raw.Retention.SnapshotKeepDays, DefaultSnapshotKeepDays),
			SnapshotPinDays:  firstNonZeroInt(raw.Retention.SnapshotPinDays, DefaultSnapshotPinDays),
			AuditKeepDays:    firstNonZeroInt(raw.Retention.AuditKeepDays, DefaultAuditKeepDays),
			StoreWarnBytes:   firstNonZeroInt64(raw.Retention.StoreWarnBytes, DefaultStoreWarnBytes),
			// T-2401: deliberately NOT run through parseDurationOrDefault,
			// which refuses a non-positive duration — here 0/absent is the
			// documented "off" default, not an error. A malformed string is
			// still fatal (validate()).
			SnapshotScheduleInterval: snapshotScheduleInterval,
			SnapshotScheduleKeep:     firstNonZeroInt(raw.Retention.SnapshotScheduleKeep, DefaultSnapshotScheduleKeep),
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
		Hub: HubConfig{
			RegistryURL:   raw.Hub.RegistryURL,
			VettedSigners: raw.Hub.VettedSigners,
			IndexSigners:  raw.Hub.IndexSigners,
			TrustUnsigned: raw.Hub.TrustUnsigned,
		},
		Webhooks: WebhooksConfig{
			AllowPrivateTargets:  raw.Webhooks.AllowPrivateTargets,
			AllowInsecureTargets: raw.Webhooks.AllowInsecureTargets,
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
			DevFixtureDir:            raw.Flows.DevFixtureDir,
		},
		Latmesh: LatmeshConfig{
			ProbeIntervalSec: firstNonZeroInt(raw.Latmesh.ProbeIntervalSec, latmesh.DefaultProbeIntervalSec),
			RetentionMinutes: firstNonZeroInt(raw.Latmesh.RetentionMinutes, latmesh.DefaultRetentionMinutes),
			MaxRows:          firstNonZeroInt64(raw.Latmesh.MaxRows, latmesh.DefaultMaxRows),
		},
		Baseline: BaselineConfig{
			ProfileRetentionDays:     firstNonZeroInt(raw.Baseline.ProfileRetentionDays, DefaultBaselineProfileRetentionDays),
			LearnIntervalHours:       firstNonZeroInt(raw.Baseline.LearnIntervalHours, DefaultBaselineLearnIntervalHours),
			AutoCaptureEnabled:       raw.Baseline.AutoCaptureEnabled,
			AutoCaptureMaxPerWindow:  firstNonZeroInt(raw.Baseline.AutoCaptureMaxPerWindow, DefaultBaselineAutoCaptureMaxPerWindow),
			AutoCaptureWindowMinutes: firstNonZeroInt(raw.Baseline.AutoCaptureWindowMinutes, DefaultBaselineAutoCaptureWindowMinutes),
		},
		MCP: MCPConfig{
			Enabled: raw.MCP.Enabled,
		},
		MTUProbe: MTUProbeConfig{
			ProbeIntervalSec: firstNonZeroInt(raw.MTUProbe.ProbeIntervalSec, mtuprobe.DefaultProbeIntervalSec),
		},
		Certs: CertsConfig{
			Root:           raw.Certs.Root,
			ExpiryWarnDays: raw.Certs.ExpiryWarnDays,
		},
		Wan: WanConfig{
			ProbeIntervalSec: firstNonZeroInt(raw.Wan.ProbeIntervalSec, wan.DefaultProbeIntervalSec),
			RetentionMinutes: firstNonZeroInt(raw.Wan.RetentionMinutes, wan.DefaultRetentionMinutes),
			MaxRows:          firstNonZeroInt64(raw.Wan.MaxRows, wan.DefaultMaxRows),
			LossWarnPct:      raw.Wan.LossWarnPct,
		},
		Capacity: CapacityConfig{
			AggregateRetentionDays: firstNonZeroInt(raw.Capacity.AggregateRetentionDays, DefaultCapacityAggregateRetentionDays),
			ForecastHorizonDays:    firstNonZeroInt(raw.Capacity.ForecastHorizonDays, DefaultCapacityForecastHorizonDays),
		},
		Capture: CaptureConfig{
			Root:                  firstNonEmpty(raw.Capture.Root, DefaultCaptureRoot),
			MaxDurationSec:        firstNonZeroInt(raw.Capture.MaxDurationSec, capture.DefaultCaps.MaxDurationSec),
			MaxBytes:              firstNonZeroInt64(raw.Capture.MaxBytes, capture.DefaultCaps.MaxBytes),
			MaxPackets:            firstNonZeroInt64(raw.Capture.MaxPackets, capture.DefaultCaps.MaxPackets),
			RetentionHours:        firstNonZeroInt(raw.Capture.RetentionHours, capture.DefaultCaps.RetentionHours),
			MaxFilterInstructions: firstNonZeroInt(raw.Capture.MaxFilterInstructions, capture.DefaultMaxFilterInstructions),
		},
		OIDC:       resolveOIDCConfig(raw.OIDC),
		GitSync:    gitSyncCfg,
		SIEMExport: siemExportCfg,
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

// LoadTelemetryOnly reads just the [telemetry] section (T-2503), for the same
// reason LoadStorageOnly exists: `vnproxctl telemetry` is a local, daemon-
// independent command family, and running the full Load would make it fail on
// any host without a resolvable PVE TLS certificate — a dev box, or the very
// broken node an operator is trying to report about.
//
// The section's own validation IS applied (ValidateTelemetry): a config that
// opts in without naming an https endpoint is an error here exactly as it is
// at daemon startup, because the failure mode this guards — "I turned it on
// and assumed it worked" — is the same in both places.
func LoadTelemetryOnly(path string, logger *slog.Logger) (TelemetryConfig, error) {
	if logger == nil {
		logger = slog.Default()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return TelemetryConfig{}, fmt.Errorf("reading config file %s: %w", path, err)
	}

	var raw rawConfig
	meta, err := toml.Decode(string(data), &raw)
	if err != nil {
		return TelemetryConfig{}, fmt.Errorf("%w: parsing config file %s: %v", ErrInvalidConfig, path, err)
	}
	for _, key := range meta.Undecoded() {
		logger.Warn("config: unrecognized key, ignoring", "key", key.String(), "file", path)
	}

	cfg := resolveTelemetryConfig(raw.Telemetry)
	if err := ValidateTelemetry(cfg); err != nil {
		return TelemetryConfig{}, fmt.Errorf("in %s: %w", path, err)
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

	// T-2901: each embed_frame_ancestors entry is emitted verbatim into the
	// /embed/* routes' frame-ancestors CSP directive, so a malformed one
	// would silently corrupt the whole header — fail startup instead,
	// naming the key and the value.
	for _, origin := range c.Server.EmbedFrameAncestors {
		if err := validateEmbedFrameAncestor(origin); err != nil {
			return fmt.Errorf("%w: server.embed_frame_ancestors entry %q: %v", ErrInvalidConfig, origin, err)
		}
	}

	if c.Retention.SnapshotKeepDays <= 0 {
		return fmt.Errorf("%w: retention.snapshot_keep_days must be positive, got %d", ErrInvalidConfig, c.Retention.SnapshotKeepDays)
	}
	if c.Retention.SnapshotPinDays <= 0 {
		return fmt.Errorf("%w: retention.snapshot_pin_days must be positive, got %d", ErrInvalidConfig, c.Retention.SnapshotPinDays)
	}
	if c.Retention.AuditKeepDays <= 0 {
		return fmt.Errorf("%w: retention.audit_keep_days must be positive, got %d", ErrInvalidConfig, c.Retention.AuditKeepDays)
	}
	if c.Retention.StoreWarnBytes <= 0 {
		return fmt.Errorf("%w: retention.store_warn_bytes must be positive, got %d", ErrInvalidConfig, c.Retention.StoreWarnBytes)
	}
	// T-2401: interval 0 is "off" and is fine. A NEGATIVE interval is not —
	// it can only come from a hand-written config and means something the
	// operator did not intend, so it fails loudly rather than being silently
	// treated as off.
	if c.Retention.SnapshotScheduleInterval < 0 {
		return fmt.Errorf("%w: retention.snapshot_schedule_interval must not be negative, got %s", ErrInvalidConfig, c.Retention.SnapshotScheduleInterval)
	}
	if c.Retention.SnapshotScheduleKeep <= 0 {
		return fmt.Errorf("%w: retention.snapshot_schedule_keep must be positive, got %d", ErrInvalidConfig, c.Retention.SnapshotScheduleKeep)
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

	if err := c.validateOIDC(); err != nil {
		return err
	}

	return nil
}

// resolveTelemetryConfig maps the raw [telemetry] section. It defaults
// nothing: an absent section is off, with no endpoint.
func resolveTelemetryConfig(raw rawTelemetry) TelemetryConfig {
	return TelemetryConfig{
		Enabled:  raw.Enabled,
		Endpoint: strings.TrimSpace(raw.Endpoint),
	}
}

// ValidateTelemetry is the [telemetry] section's semantic check, exported so
// the CLI's LoadTelemetryOnly applies exactly the same rules the daemon's
// full Load does rather than a second, drifting copy.
//
// Disabled (the default) is always valid, whatever else the section says —
// an operator who left a commented-out endpoint behind after turning
// telemetry off should not be unable to start.
func ValidateTelemetry(t TelemetryConfig) error {
	if !t.Enabled {
		return nil
	}
	if t.Endpoint == "" {
		return fmt.Errorf("%w: telemetry.enabled is true but telemetry.endpoint is empty. vnprox ships no default collector: opting in means naming the https:// endpoint the report goes to", ErrInvalidConfig)
	}
	u, err := url.ParseRequestURI(t.Endpoint)
	if err != nil {
		return fmt.Errorf("%w: telemetry.endpoint %q: %v", ErrInvalidConfig, t.Endpoint, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: telemetry.endpoint %q must be https (got scheme %q)", ErrInvalidConfig, t.Endpoint, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("%w: telemetry.endpoint %q names no host", ErrInvalidConfig, t.Endpoint)
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

// validateEmbedFrameAncestor checks one [server] embed_frame_ancestors
// entry (T-2901): a web origin of the form scheme://host[:port], e.g.
// "https://wiki.example" — the shape CSP3 frame-ancestors accepts as a
// host-source. No path, query, fragment or credentials, and none of the
// characters that would break out of the emitted header directive.
func validateEmbedFrameAncestor(v string) error {
	if strings.ContainsAny(v, " \t;,'\"") {
		return fmt.Errorf("must be an origin like \"https://wiki.example\" (no spaces, quotes, ';' or ',')")
	}
	u, err := url.Parse(v)
	if err != nil {
		return fmt.Errorf("must be an origin like \"https://wiki.example\": %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("missing host")
	}
	if u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("must be an origin only (scheme://host[:port]), with no path, query or credentials")
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

// resolveHAConfig maps the raw [ha] section to HAConfig, parsing its duration
// fields and validating the mode. HA disabled (the default) skips all
// validation. When enabled, mode must be "vip" or "dns" and a peer address is
// required (there is no active/standby pair without a peer to replicate to).
func resolveHAConfig(raw rawHA) (HAConfig, error) {
	if !raw.Enabled {
		return HAConfig{}, nil
	}
	if !ha.ValidMode(raw.Mode) {
		return HAConfig{}, fmt.Errorf("%w: ha.mode %q must be %q or %q", ErrInvalidConfig, raw.Mode, ha.ModeVIP, ha.ModeDNS)
	}
	if raw.PeerAddr == "" {
		return HAConfig{}, fmt.Errorf("%w: ha.peer_address is required when ha.enabled = true", ErrInvalidConfig)
	}
	leaseTTL, err := parseDurationOrDefault(raw.LeaseTTL, ha.DefaultLeaseTTL, "ha.lease_ttl")
	if err != nil {
		return HAConfig{}, err
	}
	renew, err := parseDurationOrDefault(raw.RenewInterval, ha.DefaultRenewInterval, "ha.renew_interval")
	if err != nil {
		return HAConfig{}, err
	}
	margin, err := parseDurationOrDefault(raw.FencingMargin, ha.DefaultFencingMargin, "ha.fencing_margin")
	if err != nil {
		return HAConfig{}, err
	}
	return HAConfig{
		Enabled: true, InstanceID: raw.InstanceID, Mode: raw.Mode,
		PeerNode: raw.PeerNode, PeerAddr: raw.PeerAddr,
		VipCommand: raw.VipCommand, DNSWebhook: raw.DNSWebhook,
		LeaseTTL: leaseTTL, RenewInterval: renew, FencingMargin: margin,
		LagThreshold: raw.LagThreshold, Bootstrap: raw.Bootstrap,
	}, nil
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

// resolvePeerConfig defaults and validates [peer] (T-1906).
//
// The default is the secure one and needs no keys at all: peer TLS is pinned
// to `/etc/pve/pve-root-ca.pem`. `tls_trust` is only ever needed to *weaken*
// that, and weakening it is a two-key interlock — the mode plus its own exact
// `tls_trust_ack` literal, which differs per mode so copying a "system" config
// and editing only the mode line does not produce an unverified one.
//
// Every failure here is fatal to Load, i.e. the daemon refuses to start:
//
//   - An unknown `tls_trust` value must not silently become the safe mode,
//     because an operator who asked for something and got something else
//     learns nothing.
//   - A missing or wrong `tls_trust_ack` must not silently become the safe
//     mode either, for the same reason, and obviously must not become the
//     requested one.
//
// Neither failure can affect a production node, which sets no `[peer]`
// tls_trust key at all.
func resolvePeerConfig(raw rawPeer) (PeerConfig, error) {
	mode, err := peer.ParseTrustMode(strings.TrimSpace(raw.TLSTrust))
	if err != nil {
		return PeerConfig{}, fmt.Errorf("%w: peer.tls_trust: %v", ErrInvalidConfig, err)
	}
	cfg := PeerConfig{
		SecretPath:  firstNonEmpty(raw.SecretPath, peer.DefaultSecretPath),
		CAFile:      firstNonEmpty(raw.CAFile, peer.DefaultClusterCAPath),
		TLSTrust:    mode,
		TLSTrustAck: raw.TLSTrustAck,
	}
	// The library (peer.NewTrust) is the enforcement point for the
	// acknowledgement, not this function — so a caller that builds a Trust
	// without going through config gets the identical interlock. Calling it
	// here just moves the failure to config-load time, where it belongs.
	if _, err := peer.NewTrust(peer.TrustOptions{
		Mode:   cfg.TLSTrust,
		CAFile: cfg.CAFile,
		Ack:    cfg.TLSTrustAck,
		Logger: discardingLogger(),
	}); err != nil {
		return PeerConfig{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	return cfg, nil
}

// discardingLogger silences the validation-only peer.NewTrust call above: the
// real, operator-visible escape-hatch banner is emitted by the daemon's own
// Trust, once per startup, and must not be duplicated (or, worse, be emitted
// only here and then forgotten) by config parsing.
func discardingLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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

// resolveOIDCConfig defaults [oidc]: Enabled is derived from a set issuer,
// GroupsClaim defaults to "groups", and the group→role mapping table is carried
// through verbatim (cap-name validation happens in validate()). Scopes are
// passed through as-is; internal/auth's provider always adds "openid".
// resolveGitSyncConfig resolves and validates the [gitsync] section
// (T-2701). A disabled section is returned as-is without a single check: an
// operator who never turned this on must never see an error from it, and a
// half-filled disabled section is not a misconfiguration of anything.
//
// An *enabled* section, by contrast, is checked strictly and fatally. That
// asymmetry is deliberate: a daemon that came up with a sync it could not
// perform would look configured while reconciling nothing, which is the
// worst of the three possible states. Note the boundary this draws — a
// remote that is merely *unreachable* is never a startup failure (T-2701
// AC7); only a remote that is not describable is.
func resolveGitSyncConfig(raw rawGitSync) (GitSyncConfig, error) {
	cfg := GitSyncConfig{
		Enabled:              raw.Enabled,
		URL:                  strings.TrimSpace(raw.URL),
		Provider:             strings.TrimSpace(raw.Provider),
		Ref:                  firstNonEmpty(strings.TrimSpace(raw.Ref), "main"),
		Path:                 strings.TrimSpace(raw.Path),
		TokenFile:            strings.TrimSpace(raw.TokenFile),
		PushTokenFile:        strings.TrimSpace(raw.PushTokenFile),
		RequireSignedCommits: raw.RequireSignedCommits,
		AllowedSignersFile:   strings.TrimSpace(raw.AllowedSignersFile),
	}
	if raw.PollInterval != "" {
		d, err := time.ParseDuration(raw.PollInterval)
		if err != nil {
			return GitSyncConfig{}, fmt.Errorf("%w: gitsync.poll_interval %q: %v", ErrInvalidConfig, raw.PollInterval, err)
		}
		if d <= 0 {
			return GitSyncConfig{}, fmt.Errorf("%w: gitsync.poll_interval must be positive, got %q", ErrInvalidConfig, raw.PollInterval)
		}
		cfg.PollInterval = d
	}
	if !cfg.Enabled {
		return cfg, nil
	}
	if cfg.URL == "" {
		return GitSyncConfig{}, fmt.Errorf("%w: gitsync.url is required when gitsync.enabled is true", ErrInvalidConfig)
	}
	if cfg.Path == "" {
		return GitSyncConfig{}, fmt.Errorf("%w: gitsync.path is required when gitsync.enabled is true", ErrInvalidConfig)
	}
	switch cfg.Provider {
	case "", "github", "gitlab", "raw":
	default:
		return GitSyncConfig{}, fmt.Errorf("%w: gitsync.provider %q is not one of github, gitlab, raw", ErrInvalidConfig, cfg.Provider)
	}
	if cfg.RequireSignedCommits && cfg.AllowedSignersFile == "" {
		// Without trust anchors "require signatures" could only ever mean
		// "refuse everything", which is a configuration mistake worth
		// naming rather than a policy worth honouring.
		return GitSyncConfig{}, fmt.Errorf("%w: gitsync.allowed_signers_file is required when gitsync.require_signed_commits is true", ErrInvalidConfig)
	}
	return cfg, nil
}

// defaultSIEMExportFacility is RFC 5424's local0 — a reasonable default
// for an application's own logs, distinct from every reserved
// kernel/mail/daemon facility a SIEM's routing rules might already treat
// specially.
const defaultSIEMExportFacility = 16

// resolveSIEMExportConfig resolves and validates [siemexport] (T-4012).
// Mirrors resolveGitSyncConfig's asymmetry (that function's own doc
// comment has the argument): a disabled section is returned as-is, with
// no check at all; an *enabled* one is checked strictly and fatally,
// because a daemon that believed it was streaming audit/finding events to
// a SIEM but silently was not is a worse failure mode than refusing to
// start.
func resolveSIEMExportConfig(raw rawSIEMExport) (SIEMExportConfig, error) {
	cfg := SIEMExportConfig{
		Enabled:    raw.Enabled,
		Format:     strings.TrimSpace(raw.Format),
		Network:    strings.TrimSpace(raw.Network),
		Address:    strings.TrimSpace(raw.Address),
		Path:       strings.TrimSpace(raw.Path),
		BufferSize: firstNonZeroInt(raw.BufferSize, 4096),
		Facility:   firstNonZeroInt(raw.Facility, defaultSIEMExportFacility),
	}
	if !cfg.Enabled {
		return cfg, nil
	}

	if cfg.Facility < 0 || cfg.Facility > 23 {
		return SIEMExportConfig{}, fmt.Errorf("%w: siemexport.facility must be 0-23, got %d", ErrInvalidConfig, cfg.Facility)
	}
	if cfg.BufferSize <= 0 {
		return SIEMExportConfig{}, fmt.Errorf("%w: siemexport.buffer_size must be positive, got %d", ErrInvalidConfig, cfg.BufferSize)
	}

	switch cfg.Format {
	case "syslog":
		if cfg.Path != "" {
			return SIEMExportConfig{}, fmt.Errorf("%w: siemexport.path must not be set when siemexport.format is \"syslog\" (syslog has no file destination)", ErrInvalidConfig)
		}
		switch cfg.Network {
		case "tcp", "udp":
		case "":
			return SIEMExportConfig{}, fmt.Errorf("%w: siemexport.network is required (\"tcp\" or \"udp\") when siemexport.format is \"syslog\"", ErrInvalidConfig)
		default:
			return SIEMExportConfig{}, fmt.Errorf("%w: siemexport.network %q is not one of tcp, udp", ErrInvalidConfig, cfg.Network)
		}
		if cfg.Address == "" {
			return SIEMExportConfig{}, fmt.Errorf("%w: siemexport.address is required when siemexport.format is \"syslog\"", ErrInvalidConfig)
		}
	case "jsonl":
		hasSocket := cfg.Network != "" || cfg.Address != ""
		if cfg.Path != "" && hasSocket {
			return SIEMExportConfig{}, fmt.Errorf("%w: siemexport.path and siemexport.network/address are mutually exclusive when siemexport.format is \"jsonl\"", ErrInvalidConfig)
		}
		if cfg.Path == "" && !hasSocket {
			return SIEMExportConfig{}, fmt.Errorf("%w: siemexport.path, or siemexport.network and siemexport.address, is required when siemexport.format is \"jsonl\"", ErrInvalidConfig)
		}
		if cfg.Path == "" {
			switch cfg.Network {
			case "tcp", "udp", "unix":
			case "":
				return SIEMExportConfig{}, fmt.Errorf("%w: siemexport.network is required (\"tcp\", \"udp\", or \"unix\") when siemexport.address is set", ErrInvalidConfig)
			default:
				return SIEMExportConfig{}, fmt.Errorf("%w: siemexport.network %q is not one of tcp, udp, unix", ErrInvalidConfig, cfg.Network)
			}
			if cfg.Address == "" {
				return SIEMExportConfig{}, fmt.Errorf("%w: siemexport.address is required when siemexport.network is set", ErrInvalidConfig)
			}
		}
	case "":
		return SIEMExportConfig{}, fmt.Errorf("%w: siemexport.format is required (\"syslog\" or \"jsonl\") when siemexport.enabled is true", ErrInvalidConfig)
	default:
		return SIEMExportConfig{}, fmt.Errorf("%w: siemexport.format %q is not one of syslog, jsonl", ErrInvalidConfig, cfg.Format)
	}

	return cfg, nil
}

func resolveOIDCConfig(raw rawOIDC) OIDCConfig {
	groups := make([]OIDCGroupMapping, 0, len(raw.Groups))
	for _, g := range raw.Groups {
		groups = append(groups, OIDCGroupMapping{Group: g.Name, Caps: g.Caps})
	}
	return OIDCConfig{
		Enabled:          strings.TrimSpace(raw.Issuer) != "",
		Issuer:           raw.Issuer,
		ClientID:         raw.ClientID,
		ClientSecretFile: raw.ClientSecretFile,
		RedirectURL:      raw.RedirectURL,
		GroupsClaim:      firstNonEmpty(raw.GroupsClaim, "groups"),
		ClusterID:        raw.ClusterID,
		Scopes:           raw.Scopes,
		Groups:           groups,
	}
}

// knownCapNames is the capability vocabulary [[oidc.group]] `caps` entries are
// validated against — kept in sync with internal/auth/caps.go's AllCaps (this
// package cannot import internal/auth without a cycle risk with cmd/vnproxd's
// wiring, following the same duplicate-and-validate precedent DefaultProtectedPath
// uses). "automation"/"capture" are intentionally omitted: neither is a
// group-mappable role (automation is token-only; capture is a Sys.Console-gated
// PVE-derived cap, never granted through an OIDC bundle).
var knownCapNames = map[string]bool{
	"netRead": true, "netWrite": true, "sdnRead": true, "sdnWrite": true,
	"fwRead": true, "fwWrite": true, "guestNet": true, "audit": true,
}

// validateOIDC checks [oidc] semantics when enabled: a client id and redirect
// URL are required, and every group→role cap name must be recognized.
func (c *Config) validateOIDC() error {
	if !c.OIDC.Enabled {
		return nil
	}
	if strings.TrimSpace(c.OIDC.ClientID) == "" {
		return fmt.Errorf("%w: oidc.client_id is required when oidc.issuer is set", ErrInvalidConfig)
	}
	if strings.TrimSpace(c.OIDC.RedirectURL) == "" {
		return fmt.Errorf("%w: oidc.redirect_url is required when oidc.issuer is set", ErrInvalidConfig)
	}
	if _, err := url.ParseRequestURI(c.OIDC.Issuer); err != nil {
		return fmt.Errorf("%w: oidc.issuer %q: %v", ErrInvalidConfig, c.OIDC.Issuer, err)
	}
	for _, g := range c.OIDC.Groups {
		if strings.TrimSpace(g.Group) == "" {
			return fmt.Errorf("%w: an [[oidc.group]] entry is missing name", ErrInvalidConfig)
		}
		for _, cap := range g.Caps {
			if !knownCapNames[cap] {
				return fmt.Errorf("%w: [[oidc.group]] %q has unknown cap %q", ErrInvalidConfig, g.Group, cap)
			}
		}
	}
	return nil
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

// protectedClasses converts the raw `[[changesets.protected_class]]` entries
// (T-2604) into their typed form. Validation of the class NAMES themselves
// deliberately lives in internal/change (NormalizeProtectedClasses), next to
// the op-type vocabulary and the policy globbing they are checked against —
// this layer only reshapes what the file said.
func protectedClasses(raw []rawProtectedClass) []ProtectedClassConfig {
	if len(raw) == 0 {
		return nil
	}
	out := make([]ProtectedClassConfig, 0, len(raw))
	for _, r := range raw {
		out = append(out, ProtectedClassConfig(r))
	}
	return out
}
