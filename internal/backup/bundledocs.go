// SPDX-License-Identifier: Apache-2.0

// bundledocs.go declares every structured document a support bundle can
// contain.
//
// These types are the *whole* of what a bundle emits in structured form.
// Every exported field of every type reachable from bundleDocTypes is
// checked against bundleschema.go's declared inventory by reflection
// (TestBundleSchema_AC2_EveryEmittedFieldIsDeclared), so a field added here
// without a declaration fails the suite. That is T-1902 AC2, and it is the
// reason these types are gathered in one file rather than living next to
// the collectors that build them: the enforcement point has to be able to
// enumerate them.
//
// Two conventions the schema enforces:
//
//   - A field whose type is `any`, `json.RawMessage`, `map[string]any` or
//     any other shape reflection cannot see inside MUST be declared with
//     the `redacted` disposition, naming the redactor it passes through.
//     Reflection cannot prove such a field is safe, so the schema refuses
//     to let it be declared as plainly emitted.
//   - A field carrying free-form text vnprox does not generate (an
//     operator's changeset title, a log line, a remote certificate's
//     subject) is likewise `redacted` — the field is known, the content is
//     not.

package backup

import "encoding/json"

// ---------------------------------------------------------------- shared

// PathFact is what a bundle says about a file: that it exists, how big it
// is, and who may read it. Never its contents. Every path-shaped diagnostic
// in a bundle is one of these, which is what makes "a bundle never contains
// a key file" checkable by reading one type rather than every collector.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type PathFact struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	IsDir     bool   `json:"isDir,omitempty"`
	Mode      string `json:"mode,omitempty"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
	ModTime   string `json:"modTime,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ---------------------------------------------------------- environment

// BundleEnvironment is `environment.json`: what this node is and what
// vnprox build is on it. The first thing anyone reading a stranger's bundle
// wants.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleEnvironment struct {
	Tool           string     `json:"tool"`
	ToolVersion    string     `json:"toolVersion"`
	GoVersion      string     `json:"goVersion"`
	OS             string     `json:"os"`
	Arch           string     `json:"arch"`
	Node           string     `json:"node"`
	CollectedAt    string     `json:"collectedAt"`
	TimeZone       string     `json:"timeZone"`
	UTCOffsetSec   int        `json:"utcOffsetSec"`
	Kernel         string     `json:"kernel,omitempty"`
	OSRelease      string     `json:"osRelease,omitempty"`
	EffectiveUID   int        `json:"effectiveUid"`
	ConfigPath     string     `json:"configPath"`
	ProxmoxPresent bool       `json:"proxmoxPresent"`
	Paths          []PathFact `json:"paths"`
}

// ---------------------------------------------------------------- config

// BundleConfig is `config/vnprox.redacted.json`: this node's vnprox.toml,
// rendered key by key through an explicit allowlist.
//
// It is a *sibling* of T-1901's configCollector, not a mode on it. A backup
// captures vnprox.toml verbatim because a backup must reconstruct the node;
// a bundle must never do that, and the difference is a different collector
// so that it cannot be a wrongly-set flag.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleConfig struct {
	Path       string      `json:"path"`
	Parsed     bool        `json:"parsed"`
	ParseError string      `json:"parseError,omitempty"`
	Keys       []ConfigKey `json:"keys"`
}

// ConfigKey is one dotted key from vnprox.toml. A key that is not in
// configKeyAllowlist is still *listed* — knowing that `[oidc]` is
// configured at all is diagnostic — but its value is replaced.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type ConfigKey struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Redacted bool   `json:"redacted,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// ---------------------------------------------------------------- store

// BundleStore is `store/summary.json`: everything about the store except
// the store.
//
// The store file itself is NEVER in a bundle. It carries the full audit
// trail, every rollback snapshot, and the ciphertext of every sealed
// credential — putting it in a file destined for a forum thread would
// defeat the card outright. These are derived facts only, read through
// store.InspectReadOnly so that inspecting a store a bundle exists to
// diagnose cannot migrate it.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleStore struct {
	Path                string      `json:"path"`
	Exists              bool        `json:"exists"`
	SizeBytes           int64       `json:"sizeBytes"`
	WALBytes            int64       `json:"walBytes"`
	SchemaVersion       int         `json:"schemaVersion"`
	BinarySchemaVersion int         `json:"binarySchemaVersion"`
	MigrationState      string      `json:"migrationState"`
	IntegrityCheck      string      `json:"integrityCheck,omitempty"`
	RuntimeLockHeld     bool        `json:"runtimeLockHeld"`
	Error               string      `json:"error,omitempty"`
	Tables              []TableFact `json:"tables"`
}

// TableFact is one table's name and row count. Names come from
// sqlite_master and counts from COUNT(*): no column value ever leaves the
// database through this collector, so it is secret-free by construction
// rather than by redaction.
type TableFact struct {
	Name string `json:"name"`
	Rows int64  `json:"rows"`
}

// ---------------------------------------------------------- changesets

// BundleChangesets is `changesets/recent.json`.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleChangesets struct {
	Limit      int               `json:"limit"`
	Total      int               `json:"total"`
	Error      string            `json:"error,omitempty"`
	Changesets []BundleChangeset `json:"changesets"`
}

// BundleChangeset is one changeset as a bundle reports it: the metadata
// verbatim, the free-form text scrubbed, and the four JSON columns walked
// by redactJSON.
//
// The revert ticket (T-1805) is represented by its *existence and expiry*
// and nothing else. changesets.revert_ticket_enc is not in the SELECT at
// all — the query names its columns, so the ciphertext is not merely
// dropped after being read, it is never read.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleChangeset struct {
	ID                    string          `json:"id"`
	Title                 string          `json:"title"`
	Author                string          `json:"author"`
	Status                string          `json:"status"`
	Origin                string          `json:"origin,omitempty"`
	ClusterID             string          `json:"clusterId,omitempty"`
	CreatedAt             int64           `json:"createdAt"`
	UpdatedAt             int64           `json:"updatedAt"`
	ConfirmDeadline       int64           `json:"confirmDeadline,omitempty"`
	RevertTicketSealed    bool            `json:"revertTicketSealed"`
	RevertTicketExpiresAt int64           `json:"revertTicketExpiresAt,omitempty"`
	OpCount               int             `json:"opCount"`
	Ops                   json.RawMessage `json:"ops,omitempty"`
	Plan                  json.RawMessage `json:"plan,omitempty"`
	ApplyLog              json.RawMessage `json:"applyLog,omitempty"`
	Findings              json.RawMessage `json:"findings,omitempty"`
}

// ---------------------------------------------------------------- findings

// BundleFindings is `findings/events.json`: the stored finding-transition
// history (finding_events). The live findings and drift lists are computed
// by a running daemon and are reported, when one is reachable, by
// BundleProbes.Daemon instead — a bundle is most needed when the daemon is
// down, so the offline half is the primary one.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleFindings struct {
	Limit  int                  `json:"limit"`
	Total  int                  `json:"total"`
	Error  string               `json:"error,omitempty"`
	Events []BundleFindingEvent `json:"events"`
}

// BundleFindingEvent is one finding transition.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleFindingEvent struct {
	FindingID  string `json:"findingId"`
	Transition string `json:"transition"`
	At         int64  `json:"at"`
}

// ------------------------------------------------------------ host network

// BundleHostNetwork is `host/network.json`: /etc/network/interfaces as a
// structure rather than as text.
//
// Emitting the file verbatim would be the single most dangerous thing this
// card could do: an operator's interfaces(5) file can legitimately carry a
// WireGuard private key (a `wireguard-private-key` option, or a `pre-up wg
// set ... private-key` script line), and that is precisely the value the
// card names as the failure being designed against. So the file is parsed
// with internal/host's existing AST and re-emitted option by option through
// ifaceOptionAllowlist; anything not on the allowlist keeps its key and
// loses its value.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleHostNetwork struct {
	Path       string        `json:"path"`
	Parsed     bool          `json:"parsed"`
	ParseError string        `json:"parseError,omitempty"`
	Auto       []string      `json:"auto"`
	Sources    []string      `json:"sources,omitempty"`
	Ifaces     []BundleIface `json:"ifaces"`
}

// BundleIface is one iface stanza.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleIface struct {
	Name     string              `json:"name"`
	Family   string              `json:"family"`
	Method   string              `json:"method"`
	Inherits []string            `json:"inherits,omitempty"`
	Options  []BundleIfaceOption `json:"options"`
}

// BundleIfaceOption is one option line inside a stanza.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleIfaceOption struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Redacted bool   `json:"redacted,omitempty"`
}

// ---------------------------------------------------------------- peers

// BundlePeers is `peers.json`: who this node's cluster peers are and
// whether vnprox can talk to them.
//
// Peers are discovered from corosync.conf rather than from a running
// daemon, so this works on an install whose daemon will not start.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundlePeers struct {
	Source    string       `json:"source"`
	Available bool         `json:"available"`
	Note      string       `json:"note,omitempty"`
	Probed    bool         `json:"probed"`
	Peers     []BundlePeer `json:"peers"`
}

// BundlePeer is one peer's reachability verdict, in T-1906's trichotomy:
// an operator must be able to tell a network problem from an attack, so
// "unreachable" and "untrusted" are never collapsed.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundlePeer struct {
	Node        string `json:"node"`
	Addr        string `json:"addr"`
	Port        int    `json:"port"`
	State       string `json:"state"`
	LatencyMS   int64  `json:"latencyMs,omitempty"`
	TLSSubject  string `json:"tlsSubject,omitempty"`
	TLSIssuer   string `json:"tlsIssuer,omitempty"`
	TLSNotAfter string `json:"tlsNotAfter,omitempty"`
	Error       string `json:"error,omitempty"`
}

// ---------------------------------------------------------------- probes

// BundleProbes is `probes.json`: the active, read-only checks that turn "it
// doesn't work" into a named cause. Everything here is *computed at
// collection time* rather than copied from somewhere, which is what lets a
// bundle diagnose a fault whose symptom never reached a log.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleProbes struct {
	Listen   ListenProbe    `json:"listen"`
	Daemon   DaemonProbe    `json:"daemon"`
	KeyFiles []KeyFileProbe `json:"keyFiles"`
}

// ListenProbe answers "is something already on vnprox's port". This is the
// port-conflict diagnosis, and it is the reason it is a probe rather than a
// log grep: a daemon that failed to bind three weeks ago and has been
// restart-looping since has a log full of noise and one useful fact, and
// this is the fact.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type ListenProbe struct {
	Address string `json:"address"`
	State   string `json:"state"`
	Error   string `json:"error,omitempty"`
}

// DaemonProbe is the local daemon's own view of itself, if it is up:
// GET /api/v1/health, the one route vnprox serves without authentication.
// Carries collector staleness per source, which is what "collector health
// and poll history" means when the daemon is running.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type DaemonProbe struct {
	URL        string            `json:"url"`
	Reachable  bool              `json:"reachable"`
	Status     string            `json:"status,omitempty"`
	Version    string            `json:"version,omitempty"`
	TLSSubject string            `json:"tlsSubject,omitempty"`
	TLSIssuer  string            `json:"tlsIssuer,omitempty"`
	Error      string            `json:"error,omitempty"`
	Collectors []DaemonCollector `json:"collectors,omitempty"`
}

// DaemonCollector mirrors internal/api's CollectorSourceStatus.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type DaemonCollector struct {
	Name                string `json:"name"`
	Node                string `json:"node,omitempty"`
	LastSuccess         string `json:"lastSuccess,omitempty"`
	LastAttempt         string `json:"lastAttempt,omitempty"`
	LastError           string `json:"lastError,omitempty"`
	ConsecutiveFailures int    `json:"consecutiveFailures,omitempty"`
}

// KeyFileProbe reports one declared secret file's *existence and mode*, and
// nothing else, ever.
//
// The list is driven by SecretClassesBy(StorageKeyFile) — T-1901's declared
// inventory — so a key-file class added to that inventory is covered here
// the day it lands. That is deliberate: the inventory already has a test
// keeping it honest against the real schema, and hanging this off it means
// a bundle's coverage cannot drift from the backup warning's.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type KeyFileProbe struct {
	ClassID string   `json:"classId"`
	Name    string   `json:"name"`
	File    PathFact `json:"file"`
}

// ---------------------------------------------------------------- logs

// BundleLogs is `logs/summary.json`; the log text itself is a separate
// entry (`logs/daemon.log`) so a reader can `less` it.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleLogs struct {
	Source    string `json:"source"`
	Collected bool   `json:"collected"`
	Note      string `json:"note,omitempty"`
	Lines     int    `json:"lines"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated"`
	Scrubbed  int    `json:"scrubbed"`
}
