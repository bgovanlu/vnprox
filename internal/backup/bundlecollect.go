// SPDX-License-Identifier: Apache-2.0

// bundlecollect.go holds T-1902's collectors.
//
// Every one of them is a bundleCollector, which — unlike backup.Collector —
// has no Emits method. That is not an omission: it is the structural half
// of "a bundle contains no secret class". A bundle collector cannot declare
// that it emits one because the interface gives it nowhere to say so, and
// sealedCollector's Emits() returns nil unconditionally. The declaration is
// therefore a property of the type rather than of anyone's discipline; the
// runtime scan in bundle_test.go (AC1) is what proves the declaration is
// also HONEST.
//
// Every collector here is also offline-capable. A support bundle is most
// needed when the daemon will not start, so nothing here requires a running
// vnproxd: the store is read through store.InspectReadOnly (which cannot
// migrate it), peers come from corosync.conf rather than from a live peer
// client, and the daemon's own /health is one probe among many rather than
// the source of everything.

package backup

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/store"
)

// Archive entry names. Declared once here and referenced by
// bundleEntrySchema, so an entry cannot exist without a declaration
// describing it.
const (
	entryBundleReadme      = entryReadme
	entryBundleEnvironment = "environment.json"
	entryBundleConfig      = "config/vnprox.redacted.json"
	entryBundleStore       = "store/summary.json"
	entryBundleChangesets  = "changesets/recent.json"
	entryBundleFindings    = "findings/events.json"
	entryBundleHostNet     = "host/network.json"
	entryBundlePeers       = "peers.json"
	entryBundleProbes      = "probes.json"
	entryBundleLogSummary  = "logs/summary.json"
	entryBundleLog         = "logs/daemon.log"
)

// bundleCollector is a support-bundle collector.
//
// Compare backup.Collector: this interface has no Emits method. There is
// consequently no way to write a bundle collector that declares it emits a
// secret class, which is the card's "structurally enforced rather than
// reviewed" requirement taken literally.
type bundleCollector interface {
	Name() string
	Collect(ctx context.Context, st *Staging) error
}

// sealedCollector adapts a bundleCollector to backup.Collector with an
// unconditionally empty Emits(). It is the only adapter, and it has no
// field or option that could make Emits() return anything else.
type sealedCollector struct{ bundleCollector }

func (sealedCollector) Emits() []SecretClass { return nil }

// writeJSONEntry marshals v and stages it under name. One helper for every
// structured document, so indentation, permissions and role are uniform and
// no collector hand-rolls its own serialisation.
func writeJSONEntry(st *Staging, name, role string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("support-bundle: encoding %s: %w", name, err)
	}
	return st.WriteFile(name, role, 0o600, append(b, '\n'))
}

// pathFact stats path without ever opening it.
func pathFact(path string) PathFact {
	f := PathFact{Path: path}
	if path == "" {
		return f
	}
	info, err := os.Lstat(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			f.Error = Scrub(err.Error())
		}
		return f
	}
	f.Exists = true
	f.IsDir = info.IsDir()
	f.Mode = fmt.Sprintf("%04o", info.Mode().Perm())
	f.SizeBytes = info.Size()
	f.ModTime = info.ModTime().UTC().Format(time.RFC3339)
	return f
}

// ------------------------------------------------------------ environment

type envCollector struct {
	opts *BundleOptions
}

func (c envCollector) Name() string { return "environment" }

func (c envCollector) Collect(_ context.Context, st *Staging) error {
	now := c.opts.now()
	zone, offset := now.Local().Zone()
	doc := BundleEnvironment{
		Tool:         "vnproxctl",
		ToolVersion:  c.opts.ToolVersion,
		GoVersion:    runtime.Version(),
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Node:         c.opts.Node,
		CollectedAt:  now.UTC().Format(time.RFC3339),
		TimeZone:     zone,
		UTCOffsetSec: offset,
		Kernel:       Scrub(strings.TrimSpace(readSmallFile(c.opts.KernelReleasePath, 256))),
		OSRelease:    Scrub(osPrettyName(c.opts.OSReleasePath)),
		EffectiveUID: os.Geteuid(),
		ConfigPath:   c.opts.ConfigPath,
	}
	doc.ProxmoxPresent = pathFact(c.opts.PVEDir).Exists

	// The path set is what an operator actually gets wrong: a store the
	// daemon cannot write, a key directory with the wrong mode, a missing
	// /etc/pve. Existence and mode only — pathFact never opens anything.
	paths := []string{
		c.opts.ConfigPath,
		c.opts.DBPath,
		filepath.Dir(c.opts.DBPath),
		c.opts.InterfacesPath,
		c.opts.CorosyncPath,
		c.opts.PVEDir,
	}
	for _, k := range c.opts.KeyPaths {
		if k.Path != "" {
			paths = append(paths, filepath.Dir(k.Path))
		}
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" || p == "." || seen[p] {
			continue
		}
		seen[p] = true
		doc.Paths = append(doc.Paths, pathFact(p))
	}
	return writeJSONEntry(st, entryBundleEnvironment, RoleMeta, doc)
}

func readSmallFile(path string, limit int64) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path) //nolint:gosec // a fixed diagnostic path from BundleOptions, not from the archive.
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return ""
	}
	return string(b)
}

func osPrettyName(path string) string {
	for _, line := range strings.Split(readSmallFile(path, 8<<10), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

// ---------------------------------------------------------------- config

// configKeyAllowlist is the set of vnprox.toml keys whose VALUE a support
// bundle may carry.
//
// This is the sibling collector T-1901's handoff asked for, and the reason
// it is a sibling rather than a redaction *mode* on configCollector is
// stated there: modes get set wrong, separate collectors cannot be.
//
// The allowlist is deliberately generous about *paths* and stingy about
// everything else. "The token file is configured as /etc/vnprox/keys/pve"
// is frequently the whole diagnosis, and a path is not a credential — while
// a value like [pve] dev_ticket_password is a credential outright and is
// absent from this list, so it is replaced whether or not anyone remembers
// that it exists.
//
//nolint:gochecknoglobals // a declared allowlist, deliberately package-level.
var configKeyAllowlist = map[string]bool{
	// Where things live. Paths, never contents.
	"server.listen":                  true,
	"server.tls_cert":                true,
	"server.tls_key":                 true,
	"server.read_only":               true,
	"server.confirm_timeout_default": true,
	"storage.db_path":                true,
	"storage.session_key_file":       true,
	"pve.api_url":                    true,
	"pve.token_file":                 true,
	"peer.secret_path":               true,
	"peer.ca_file":                   true,
	"peer.tls_trust":                 true,
	"metrics.enabled":                true,
	"metrics.key_file":               true,
	"metrics.allow_from":             true,
	"blueprint.signing_key_file":     true,
	"blueprint.trusted_signers_dir":  true,
	"oidc.issuer":                    true,
	"oidc.client_id":                 true,
	"oidc.client_secret_file":        true,
	"oidc.redirect_url":              true,
	"oidc.groups_claim":              true,
	"oidc.cluster_id":                true,
	"oidc.scopes":                    true,

	// Behaviour. Every one of these changes what the daemon does and none
	// of them authenticates anything.
	"collect.pve_interval":               true,
	"collect.host_interval":              true,
	"collect.lldp_interval":              true,
	"safety.protected_path":              true,
	"safety.dev_interfaces_dir":          true,
	"safety.allow_dangerous_ops":         true,
	"security.snapshot_keep_days":        true,
	"security.snapshot_pin_days":         true,
	"firewalllog.enabled":                true,
	"capture.root":                       true,
	"capture.max_duration_sec":           true,
	"capture.max_bytes":                  true,
	"capture.max_packets":                true,
	"capture.retention_hours":            true,
	"capture.max_filter_instructions":    true,
	"flows.sflow_enabled":                true,
	"flows.netflow_enabled":              true,
	"flows.ipfix_enabled":                true,
	"flows.sflow_port":                   true,
	"flows.netflow_port":                 true,
	"flows.ipfix_port":                   true,
	"flows.retention_minutes":            true,
	"flows.max_rows":                     true,
	"ha.enabled":                         true,
	"ha.mode":                            true,
	"ha.instance_id":                     true,
	"ha.peer_node":                       true,
	"ha.peer_address":                    true,
	"ha.lease_ttl":                       true,
	"ha.renew_interval":                  true,
	"ha.fencing_margin":                  true,
	"mcp.enabled":                        true,
	"hub.registry_url":                   true,
	"switches.enabled":                   true,
	"wan.probe_interval_sec":             true,
	"latmesh.probe_interval_sec":         true,
	"mtuprobe.probe_interval_sec":        true,
	"ifcounters.poll_interval_sec":       true,
	"retention.aggregate_retention_days": true,
	"capacity.aggregate_retention_days":  true,
	"baseline.profile_retention_days":    true,
	"baseline.learn_interval_hours":      true,
}

type configBundleCollector struct {
	opts *BundleOptions
}

func (c configBundleCollector) Name() string { return "config" }

func (c configBundleCollector) Collect(_ context.Context, st *Staging) error {
	doc := BundleConfig{Path: c.opts.ConfigPath}
	data, err := os.ReadFile(c.opts.ConfigPath) //nolint:gosec // the operator's own --config path.
	switch {
	case err != nil:
		doc.ParseError = Scrub(err.Error())
	default:
		var raw map[string]any
		if _, decErr := toml.Decode(string(data), &raw); decErr != nil {
			doc.ParseError = Scrub(decErr.Error())
		} else {
			doc.Parsed = true
			doc.Keys = flattenConfig("", raw)
		}
	}
	if doc.Keys == nil {
		doc.Keys = []ConfigKey{}
	}
	return writeJSONEntry(st, entryBundleConfig, RoleConfig, doc)
}

// flattenConfig walks a decoded TOML document into dotted keys, applying
// the allowlist at every leaf.
//
// Note the direction: it enumerates what the OPERATOR'S FILE contains, not
// what vnprox's config struct knows about. A key vnprox does not recognise
// still appears — name only — because "this install sets a key this build
// ignores" is a real diagnosis, and because a projection driven by the
// config struct would silently drop exactly the keys nobody has thought
// about.
func flattenConfig(prefix string, v map[string]any) []ConfigKey {
	var out []ConfigKey
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		switch child := v[k].(type) {
		case map[string]any:
			out = append(out, flattenConfig(full, child)...)
		case []map[string]any:
			for i, item := range child {
				out = append(out, flattenConfig(fmt.Sprintf("%s[%d]", full, i), item)...)
			}
		default:
			out = append(out, configKey(full, child))
		}
	}
	return out
}

func configKey(full string, value any) ConfigKey {
	// The lookup key strips any [n] array index so a repeated table's keys
	// share one allowlist entry.
	lookup := arrayIndexPattern.ReplaceAllString(full, "")
	if !configKeyAllowlist[lookup] {
		return ConfigKey{
			Key: full, Value: Redacted, Redacted: true,
			Reason: "key is not in the support-bundle config allowlist",
		}
	}
	rendered := Scrub(renderTOMLValue(value))
	return ConfigKey{Key: full, Value: rendered}
}

func renderTOMLValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			parts = append(parts, renderTOMLValue(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case bool:
		return strconv.FormatBool(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

// ---------------------------------------------------------------- store

type storeFactsCollector struct {
	opts *BundleOptions
}

func (c storeFactsCollector) Name() string { return "store-facts" }

// Collect emits derived facts about the store and NEVER the store itself.
// See BundleStore's doc comment for why, and TestBundle_NeverCarriesTheStore
// for the assertion that keeps it true.
func (c storeFactsCollector) Collect(ctx context.Context, st *Staging) error {
	doc := BundleStore{Path: c.opts.DBPath, MigrationState: "unknown", Tables: []TableFact{}}
	if latest, err := store.LatestSchemaVersion(); err == nil {
		doc.BinarySchemaVersion = latest
	}

	if f := pathFact(c.opts.DBPath); f.Exists {
		doc.Exists = true
		doc.SizeBytes = f.SizeBytes
	} else {
		doc.MigrationState = "no store on this node"
		return writeJSONEntry(st, entryBundleStore, RoleMeta, doc)
	}
	doc.WALBytes = pathFact(c.opts.DBPath + "-wal").SizeBytes
	if held, err := store.RuntimeLockHeld(c.opts.DBPath); err == nil {
		doc.RuntimeLockHeld = held
	}

	// The schema version comes from store.InspectSchemaVersion rather than
	// from a query written out here: it is the same read T-1901's restore
	// path decides on, it knows the kv table's shape, and it returns 0 for
	// a never-migrated file rather than an error. Re-implementing it would
	// be a second source of truth for the one number this collector exists
	// to report.
	if version, verErr := store.InspectSchemaVersion(ctx, c.opts.DBPath); verErr == nil {
		doc.SchemaVersion = version
	} else {
		doc.Error = Scrub(verErr.Error())
	}

	err := store.InspectReadOnly(ctx, c.opts.DBPath, func(db *sql.DB) error {
		var integrity string
		if scanErr := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); scanErr == nil {
			doc.IntegrityCheck = Scrub(integrity)
		}

		rows, qErr := db.QueryContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
		if qErr != nil {
			return fmt.Errorf("listing tables: %w", qErr)
		}
		var names []string
		for rows.Next() {
			var n string
			if scanErr := rows.Scan(&n); scanErr != nil {
				_ = rows.Close()
				return fmt.Errorf("scanning table name: %w", scanErr)
			}
			names = append(names, n)
		}
		if closeErr := rows.Close(); closeErr != nil {
			return fmt.Errorf("closing table list: %w", closeErr)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			return fmt.Errorf("iterating table list: %w", rowsErr)
		}
		for _, n := range names {
			var count int64
			// The identifier comes from sqlite_master, not from any
			// external input, and is quoted; COUNT(*) reads no column
			// value, so no cell of any table can leave through here.
			if scanErr := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+strings.ReplaceAll(n, `"`, `""`)+`"`).Scan(&count); scanErr != nil {
				continue
			}
			doc.Tables = append(doc.Tables, TableFact{Name: n, Rows: count})
		}
		return nil
	})
	if err != nil {
		doc.Error = Scrub(err.Error())
		doc.MigrationState = "store could not be read"
		return writeJSONEntry(st, entryBundleStore, RoleMeta, doc)
	}

	switch {
	case doc.BinarySchemaVersion == 0:
		doc.MigrationState = "unknown: this build could not report its own schema version"
	case doc.SchemaVersion == doc.BinarySchemaVersion:
		doc.MigrationState = "up to date"
	case doc.SchemaVersion < doc.BinarySchemaVersion:
		doc.MigrationState = fmt.Sprintf("forward migration pending: store is at %d, this build expects %d",
			doc.SchemaVersion, doc.BinarySchemaVersion)
	default:
		doc.MigrationState = fmt.Sprintf("store is NEWER than this build: store is at %d, this build understands %d "+
			"— a downgraded or mismatched vnproxd will refuse to start", doc.SchemaVersion, doc.BinarySchemaVersion)
	}
	return writeJSONEntry(st, entryBundleStore, RoleMeta, doc)
}

// ---------------------------------------------------------- changesets

type changesetsCollector struct {
	opts *BundleOptions
}

func (c changesetsCollector) Name() string { return "changesets" }

func (c changesetsCollector) Collect(ctx context.Context, st *Staging) error {
	doc := BundleChangesets{Limit: c.opts.ChangesetLimit, Changesets: []BundleChangeset{}}
	if !pathFact(c.opts.DBPath).Exists {
		doc.Error = "no store on this node"
		return writeJSONEntry(st, entryBundleChangesets, RoleMeta, doc)
	}
	err := store.InspectReadOnly(ctx, c.opts.DBPath, func(db *sql.DB) error {
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM changesets`).Scan(&doc.Total)
		// The column list is written out rather than SELECT *: this is what
		// makes "the sealed revert ticket is never read" a property of the
		// query instead of a promise about what happens to the result.
		// changesets.revert_ticket_enc is deliberately absent.
		rows, qErr := db.QueryContext(ctx, `
			SELECT id, title, author, status,
			       COALESCE(cluster_id, ''), COALESCE(origin, 'ui'),
			       created_at, updated_at,
			       COALESCE(confirm_deadline, 0),
			       COALESCE(ops_json, ''), COALESCE(plan_json, ''),
			       COALESCE(apply_log_json, ''), COALESCE(findings_json, ''),
			       (revert_ticket_enc IS NOT NULL),
			       COALESCE(revert_ticket_expires_at, 0)
			  FROM changesets
			 ORDER BY created_at DESC, id DESC
			 LIMIT ?`, c.opts.ChangesetLimit)
		if qErr != nil {
			return fmt.Errorf("listing changesets: %w", qErr)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var cs BundleChangeset
			var ops, plan, applyLog, findings string
			if scanErr := rows.Scan(&cs.ID, &cs.Title, &cs.Author, &cs.Status,
				&cs.ClusterID, &cs.Origin, &cs.CreatedAt, &cs.UpdatedAt, &cs.ConfirmDeadline,
				&ops, &plan, &applyLog, &findings,
				&cs.RevertTicketSealed, &cs.RevertTicketExpiresAt); scanErr != nil {
				return fmt.Errorf("scanning changeset: %w", scanErr)
			}
			cs.Title = Scrub(cs.Title)
			cs.Author = Scrub(cs.Author)
			cs.Ops = redactJSON([]byte(ops))
			cs.Plan = redactJSON([]byte(plan))
			cs.ApplyLog = redactJSON([]byte(applyLog))
			cs.Findings = redactJSON([]byte(findings))
			cs.OpCount = jsonArrayLen([]byte(ops))
			doc.Changesets = append(doc.Changesets, cs)
		}
		return rows.Err()
	})
	if err != nil {
		doc.Error = Scrub(err.Error())
	}
	return writeJSONEntry(st, entryBundleChangesets, RoleMeta, doc)
}

func jsonArrayLen(raw []byte) int {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return 0
	}
	return len(arr)
}

// ---------------------------------------------------------------- findings

type findingsCollector struct {
	opts *BundleOptions
}

func (c findingsCollector) Name() string { return "findings" }

func (c findingsCollector) Collect(ctx context.Context, st *Staging) error {
	doc := BundleFindings{Limit: c.opts.FindingLimit, Events: []BundleFindingEvent{}}
	if !pathFact(c.opts.DBPath).Exists {
		doc.Error = "no store on this node"
		return writeJSONEntry(st, entryBundleFindings, RoleMeta, doc)
	}
	err := store.InspectReadOnly(ctx, c.opts.DBPath, func(db *sql.DB) error {
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM finding_events`).Scan(&doc.Total)
		rows, qErr := db.QueryContext(ctx,
			`SELECT finding_id, transition, at FROM finding_events ORDER BY at DESC, id DESC LIMIT ?`,
			c.opts.FindingLimit)
		if qErr != nil {
			return fmt.Errorf("listing finding events: %w", qErr)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var e BundleFindingEvent
			if scanErr := rows.Scan(&e.FindingID, &e.Transition, &e.At); scanErr != nil {
				return fmt.Errorf("scanning finding event: %w", scanErr)
			}
			e.FindingID = Scrub(e.FindingID)
			doc.Events = append(doc.Events, e)
		}
		return rows.Err()
	})
	if err != nil {
		doc.Error = Scrub(err.Error())
	}
	return writeJSONEntry(st, entryBundleFindings, RoleMeta, doc)
}

// ------------------------------------------------------------ host network

// ifaceOptionAllowlist is the set of interfaces(5) option names whose VALUE
// a support bundle may carry.
//
// This list is the single most safety-critical allowlist in the card. An
// operator's /etc/network/interfaces can legitimately hold a WireGuard
// private key — as a `wireguard-private-key` option, or hidden inside a
// `pre-up`/`up` script line — and that is precisely the value the card
// names as the failure being designed against. Everything not on this list
// keeps its option NAME (knowing a stanza configures WireGuard at all is
// diagnostic) and loses its value.
//
// The list is what you need to draw the network: addressing, bridges,
// bonds, VLANs, MTU, and the SDN/OVS shape. Nothing on it is a credential,
// and every value on it additionally passes Scrub.
//
//nolint:gochecknoglobals // a declared allowlist, deliberately package-level.
var ifaceOptionAllowlist = map[string]bool{
	"address": true, "netmask": true, "gateway": true, "broadcast": true,
	"network": true, "mtu": true, "hwaddress": true, "metric": true,
	"dns-nameservers": true, "dns-search": true, "scope": true,
	"pointopoint": true, "media": true, "post-up-delay": true,

	"bridge-ports": true, "bridge_ports": true, "bridge-stp": true, "bridge_stp": true,
	"bridge-fd": true, "bridge_fd": true, "bridge-vlan-aware": true, "bridge_vlan_aware": true,
	"bridge-vids": true, "bridge_vids": true, "bridge-pvid": true, "bridge_pvid": true,
	"bridge-vlan-protocol": true, "bridge-access": true, "bridge-maxwait": true,

	"bond-slaves": true, "bond_slaves": true, "bond-mode": true, "bond_mode": true,
	"bond-miimon": true, "bond_miimon": true, "bond-xmit-hash-policy": true,
	"bond_xmit_hash_policy": true, "bond-lacp-rate": true, "bond_lacp_rate": true,
	"bond-primary": true, "bond-updelay": true, "bond-downdelay": true,

	"vlan-id": true, "vlan_id": true, "vlan-raw-device": true, "vlan_raw_device": true,

	"ovs_type": true, "ovs_bridge": true, "ovs_bonds": true, "ovs_ports": true,
	"ovs_options": true, "ovs_tag": true, "ovs_mtu": true,

	"vxlan-id": true, "vxlan-svcnodeip": true, "vxlan-physdev": true,
	"vxlan_remoteip": true, "vxlan-local-tunnelip": true,

	"accept_ra": true, "autoconf": true, "privext": true,
	"vrf": true, "vrf-table": true, "alias": true, "comment": true,
}

type hostNetCollector struct {
	opts *BundleOptions
}

func (c hostNetCollector) Name() string { return "host-network" }

func (c hostNetCollector) Collect(_ context.Context, st *Staging) error {
	doc := BundleHostNetwork{Path: c.opts.InterfacesPath, Auto: []string{}, Ifaces: []BundleIface{}}
	data, err := os.ReadFile(c.opts.InterfacesPath) //nolint:gosec // a fixed diagnostic path from BundleOptions.
	if err != nil {
		doc.ParseError = Scrub(err.Error())
		return writeJSONEntry(st, entryBundleHostNet, RoleMeta, doc)
	}
	f, err := host.ParseInterfaces(data)
	if err != nil {
		doc.ParseError = Scrub(err.Error())
		return writeJSONEntry(st, entryBundleHostNet, RoleMeta, doc)
	}
	doc.Parsed = true
	for _, a := range f.AutoIfaces() {
		doc.Auto = append(doc.Auto, Scrub(a))
	}
	for _, e := range f.Entries {
		if e.Kind == host.KindSource || e.Kind == host.KindSourceDirectory {
			doc.Sources = append(doc.Sources, Scrub(e.Path))
		}
	}
	for _, e := range f.Ifaces() {
		iface := BundleIface{
			Name: Scrub(e.Name), Family: e.Family, Method: e.Method,
			Options: []BundleIfaceOption{},
		}
		for _, in := range e.Inherits {
			iface.Inherits = append(iface.Inherits, Scrub(in))
		}
		for _, opt := range e.Options() {
			o := BundleIfaceOption{Key: opt.Key}
			if !ifaceOptionAllowlist[strings.ToLower(opt.Key)] {
				o.Value, o.Redacted = Redacted, true
			} else {
				o.Value, o.Redacted = redactedOptionValue(opt.Key, opt.Value)
			}
			iface.Options = append(iface.Options, o)
		}
		doc.Ifaces = append(doc.Ifaces, iface)
	}
	return writeJSONEntry(st, entryBundleHostNet, RoleMeta, doc)
}

// ---------------------------------------------------------------- peers

type peersCollector struct {
	opts *BundleOptions
}

func (c peersCollector) Name() string { return "peers" }

func (c peersCollector) Collect(ctx context.Context, st *Staging) error {
	doc := BundlePeers{Source: c.opts.CorosyncPath, Probed: c.opts.Probe, Peers: []BundlePeer{}}
	cfg, err := host.ReadCorosyncConf(c.opts.CorosyncPath)
	if err != nil {
		doc.Note = Scrub(fmt.Sprintf("no cluster peers discovered: %v "+
			"(a single node that has never been clustered has no corosync.conf, which is normal)", err))
		return writeJSONEntry(st, entryBundlePeers, RoleMeta, doc)
	}
	doc.Available = true
	port := listenPort(c.opts.Listen)
	for _, n := range cfg.Nodes {
		addr := ""
		if len(n.RingAddrs) > 0 {
			addr = n.RingAddrs[0]
		}
		p := BundlePeer{Node: Scrub(n.Name), Addr: Scrub(addr), Port: port, State: "skipped"}
		if c.opts.Probe && addr != "" && n.Name != c.opts.Node {
			probePeer(ctx, &p, addr, port, c.opts.ProbeTimeout)
		}
		doc.Peers = append(doc.Peers, p)
	}
	return writeJSONEntry(st, entryBundlePeers, RoleMeta, doc)
}

// probePeer fills in one peer's reachability verdict, keeping T-1906's
// three states distinct: an operator must be able to tell a network problem
// from an attack, so a TLS failure is never reported as "unreachable".
//
// The handshake deliberately does not verify (InsecureSkipVerify): this is
// a *diagnostic* that reports the certificate it was shown, not a channel
// anything is sent over. No credential is transmitted — the probe closes
// the connection after the handshake and issues no request at all.
func probePeer(ctx context.Context, p *BundlePeer, addr string, port int, timeout time.Duration) {
	target := net.JoinHostPort(addr, strconv.Itoa(port))
	start := time.Now()
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		p.State = "unreachable"
		p.Error = Scrub(err.Error())
		return
	}
	defer func() { _ = conn.Close() }()

	//nolint:gosec // G402: a diagnostic handshake that sends nothing and reports what it was shown; see this function's doc comment.
	tc := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: addr})
	hsCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if hsErr := tc.HandshakeContext(hsCtx); hsErr != nil {
		p.State = "untrusted"
		p.Error = Scrub(hsErr.Error())
		p.LatencyMS = time.Since(start).Milliseconds()
		return
	}
	p.State = "ok"
	p.LatencyMS = time.Since(start).Milliseconds()
	if certs := tc.ConnectionState().PeerCertificates; len(certs) > 0 {
		p.TLSSubject = Scrub(certs[0].Subject.String())
		p.TLSIssuer = Scrub(certs[0].Issuer.String())
		p.TLSNotAfter = certs[0].NotAfter.UTC().Format(time.RFC3339)
	}
}

func listenPort(listen string) int {
	_, portStr, err := net.SplitHostPort(listen)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return port
}

// ---------------------------------------------------------------- probes

type probesCollector struct {
	opts *BundleOptions
}

func (c probesCollector) Name() string { return "probes" }

func (c probesCollector) Collect(ctx context.Context, st *Staging) error {
	doc := BundleProbes{
		Listen:   c.probeListen(),
		Daemon:   c.probeDaemon(ctx),
		KeyFiles: c.probeKeyFiles(),
	}
	return writeJSONEntry(st, entryBundleProbes, RoleMeta, doc)
}

// probeListen answers the port-conflict question directly: it tries to bind
// [server] listen. "In use" and "free" are both diagnoses — free while the
// daemon is supposed to be running means the daemon is not running.
func (c probesCollector) probeListen() ListenProbe {
	p := ListenProbe{Address: c.opts.Listen, State: "skipped"}
	if c.opts.Listen == "" || !c.opts.Probe {
		return p
	}
	if _, _, err := net.SplitHostPort(c.opts.Listen); err != nil {
		p.State, p.Error = "unparsable", Scrub(err.Error())
		return p
	}
	ln, err := net.Listen("tcp", c.opts.Listen)
	if err != nil {
		p.State, p.Error = "in-use", Scrub(err.Error())
		return p
	}
	_ = ln.Close()
	p.State = "free"
	return p
}

// probeDaemon reads GET /api/v1/health, the one route vnproxd serves
// without authentication (internal/api/router.go). Nothing is sent: no
// token, no session, no CSRF header. TLS is not verified because the
// daemon's certificate is routinely PVE's own self-signed one and the point
// is to report what it presented, not to trust it.
func (c probesCollector) probeDaemon(ctx context.Context) DaemonProbe {
	p := DaemonProbe{}
	if c.opts.Listen == "" || !c.opts.Probe {
		return p
	}
	host, port, err := net.SplitHostPort(c.opts.Listen)
	if err != nil {
		return p
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	p.URL = "https://" + net.JoinHostPort(host, port) + "/api/v1/health"

	client := &http.Client{
		Timeout: c.opts.ProbeTimeout,
		Transport: &http.Transport{
			//nolint:gosec // G402: a local diagnostic read of an unauthenticated route; nothing is sent. See this function's doc comment.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		p.Error = Scrub(err.Error())
		return p
	}
	resp, err := client.Do(req)
	if err != nil {
		p.Error = Scrub(err.Error())
		return p
	}
	defer func() { _ = resp.Body.Close() }()
	p.Reachable = true
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		p.TLSSubject = Scrub(resp.TLS.PeerCertificates[0].Subject.String())
		p.TLSIssuer = Scrub(resp.TLS.PeerCertificates[0].Issuer.String())
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		p.Error = Scrub(err.Error())
		return p
	}
	var health struct {
		Status     string `json:"status"`
		Version    string `json:"version"`
		Collectors []struct {
			Name                string    `json:"name"`
			Node                string    `json:"node"`
			LastSuccess         time.Time `json:"last_success"`
			LastAttempt         time.Time `json:"last_attempt"`
			LastError           string    `json:"last_error"`
			ConsecutiveFailures int       `json:"consecutive_failures"`
		} `json:"collectors"`
	}
	if jsonErr := json.Unmarshal(body, &health); jsonErr != nil {
		p.Error = Scrub(fmt.Sprintf("health response was not the documented shape: %v", jsonErr))
		return p
	}
	p.Status = health.Status
	p.Version = Scrub(health.Version)
	for _, s := range health.Collectors {
		dc := DaemonCollector{
			Name: s.Name, Node: Scrub(s.Node),
			LastError: Scrub(s.LastError), ConsecutiveFailures: s.ConsecutiveFailures,
		}
		if !s.LastSuccess.IsZero() {
			dc.LastSuccess = s.LastSuccess.UTC().Format(time.RFC3339)
		}
		if !s.LastAttempt.IsZero() {
			dc.LastAttempt = s.LastAttempt.UTC().Format(time.RFC3339)
		}
		p.Collectors = append(p.Collectors, dc)
	}
	return p
}

// probeKeyFiles reports existence and mode for every key-file class in
// T-1901's declared inventory. It never opens any of them.
func (c probesCollector) probeKeyFiles() []KeyFileProbe {
	byClass := map[string]string{}
	for _, k := range c.opts.KeyPaths {
		byClass[k.ClassID] = k.Path
	}
	out := []KeyFileProbe{}
	for _, class := range SecretClassesBy(StorageKeyFile) {
		out = append(out, KeyFileProbe{
			ClassID: class.ID, Name: class.Name, File: pathFact(byClass[class.ID]),
		})
	}
	return out
}

// ---------------------------------------------------------------- logs

// LogSource says where the daemon's log comes from. Path wins if set; it is
// what tests and non-systemd installs use. Otherwise Unit is read with
// journalctl, which is how a real Proxmox node stores vnproxd's output.
type LogSource struct {
	Path string
	Unit string
}

type logsCollector struct {
	opts *BundleOptions
}

func (c logsCollector) Name() string { return "logs" }

func (c logsCollector) Collect(ctx context.Context, st *Staging) error {
	summary := BundleLogs{}
	raw, source, note, err := c.read(ctx)
	summary.Source = source
	if err != nil {
		summary.Note = Scrub(fmt.Sprintf("%s: %v", note, err))
		if writeErr := st.WriteFile(entryBundleLog, RoleMeta, 0o600,
			[]byte("no daemon log was collected: "+summary.Note+"\n")); writeErr != nil {
			return writeErr
		}
		return writeJSONEntry(st, entryBundleLogSummary, RoleMeta, summary)
	}
	summary.Collected = true

	if int64(len(raw)) > c.opts.LogTailBytes {
		raw = raw[int64(len(raw))-c.opts.LogTailBytes:]
		// Drop the partial first line so the tail starts at a boundary.
		if i := strings.IndexByte(string(raw), '\n'); i >= 0 {
			raw = raw[i+1:]
		}
		summary.Truncated = true
	}

	lines := strings.Split(string(raw), "\n")
	scrubbed := 0
	for i, line := range lines {
		out := Scrub(line)
		if out != line {
			scrubbed++
		}
		lines[i] = out
	}
	text := strings.Join(lines, "\n")
	summary.Lines = len(lines)
	summary.Bytes = len(text)
	summary.Scrubbed = scrubbed

	if err := st.WriteFile(entryBundleLog, RoleMeta, 0o600, []byte(text)); err != nil {
		return err
	}
	return writeJSONEntry(st, entryBundleLogSummary, RoleMeta, summary)
}

func (c logsCollector) read(ctx context.Context) (data []byte, source, note string, err error) {
	if p := c.opts.LogSource.Path; p != "" {
		b, readErr := os.ReadFile(p) //nolint:gosec // a fixed diagnostic path from BundleOptions.
		return b, p, "reading " + p, readErr
	}
	unit := c.opts.LogSource.Unit
	if unit == "" {
		return nil, "", "no log source configured", errors.New("neither a log path nor a systemd unit was configured")
	}
	source = "journalctl -u " + unit
	bin, lookErr := exec.LookPath("journalctl")
	if lookErr != nil {
		return nil, source, "journalctl is not available on this host", lookErr
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	//nolint:gosec // G204: bin comes from LookPath and unit from this node's own config, never from an archive.
	out, runErr := exec.CommandContext(cmdCtx, bin, "-u", unit, "--no-pager", "-n",
		strconv.Itoa(c.opts.LogTailLines)).Output()
	if runErr != nil {
		return nil, source, "running journalctl", runErr
	}
	return out, source, "", nil
}
