// SPDX-License-Identifier: Apache-2.0

// bundleschema.go is T-1902 AC2: "adding a new field to a collected
// structure without declaring it fails a test".
//
// The card's safety analysis says a support bundle must be redacted **by
// construction, not by review**, and that "a collector that cannot describe
// its output does not ship". This file is where that is made mechanical, in
// two dimensions:
//
//	bundleEntrySchema  — every FILE a bundle may contain, declared by name,
//	                     role, and what redaction its contents passed.
//	bundleFieldSchema  — every FIELD of every structured document, declared
//	                     with a disposition and a reason it is safe.
//
// Both are checked against reality rather than against themselves:
//
//   - TestBundleSchema_AC2_EveryEmittedFieldIsDeclared reflects over the
//     real Go types and fails in BOTH directions — an undeclared field is a
//     leak waiting to happen, and a declaration with no field behind it is a
//     schema that has stopped describing the code.
//   - TestBundle_AC2_EveryProducedEntryIsDeclared takes a real bundle from a
//     real seeded fixture and compares its actual entry list to
//     bundleEntrySchema, so a collector that starts writing a new file is
//     caught even though no Go type changed.
//   - TestBundleSchema_TheEnforcementIsNotVacuous runs the same walker over
//     a deliberately-undeclared type and requires it to complain. Without
//     that control, a walker that silently found nothing would make every
//     other assertion here meaningless.
//
// The rule that does the real work is disposition-based. Reflection can
// prove a bounded scalar is bounded; it cannot see inside a
// `json.RawMessage`, an `any`, or a `map`. So the walker REFUSES to let
// such a field be declared `emit` — it must name a redactor. That is the
// card's "every emitted field passes an explicit allowlist or a redactor",
// enforced by a test rather than by a reviewer noticing.

package backup

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// disposition says how a field's value is made safe.
type disposition string

const (
	// dispEmit: vnprox generates this value itself and its range is
	// bounded — an enum, a count, a timestamp, a path this code composed.
	// Only legal on types reflection can see through.
	dispEmit disposition = "emit"

	// dispScrub: free-form text vnprox does not own the content of (an
	// operator's changeset title, a log line, an error string, a remote
	// certificate's subject). Passed through Scrub.
	dispScrub disposition = "scrub"

	// dispRedactJSON: opaque JSON whose shape is defined elsewhere and
	// grows over time. Passed through redactJSON, which walks it by key
	// name so a sealed field invented next phase is redacted the day it
	// appears.
	dispRedactJSON disposition = "redact-json"

	// dispAllowlist: the value survives only if its KEY is on an explicit
	// allowlist (configKeyAllowlist, ifaceOptionAllowlist); otherwise it is
	// replaced. Used where the key set is open-ended but enumerable.
	dispAllowlist disposition = "allowlist"
)

// fieldDecl declares one exported field of one collected type.
type fieldDecl struct {
	// Field is the Go field name. Go names rather than JSON tags: the
	// property being defended is "someone added a field", which is a Go
	// edit.
	Field string
	// Disp is how the value is made safe.
	Disp disposition
	// Why must be non-empty. A declaration that cannot say why the field is
	// safe to put in a forum post is exactly the "collector that cannot
	// describe its output" the card refuses to ship.
	Why string
}

// bundleDocTypes are the roots of the reflection walk: every structured
// document a bundle emits. A new document type must be added here, which is
// itself the declaration that it exists.
func bundleDocTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(BundleEnvironment{}),
		reflect.TypeOf(BundleConfig{}),
		reflect.TypeOf(BundleStore{}),
		reflect.TypeOf(BundleChangesets{}),
		reflect.TypeOf(BundleFindings{}),
		reflect.TypeOf(BundleHostNetwork{}),
		reflect.TypeOf(BundlePeers{}),
		reflect.TypeOf(BundleProbes{}),
		reflect.TypeOf(BundleLogs{}),
		reflect.TypeOf(BundleIncident{}),
	}
}

// bundleFieldSchema is the declared inventory, keyed by Go type name.
//
//nolint:gochecknoglobals // a declared inventory, deliberately package-level; see this file's doc comment.
var bundleFieldSchema = map[string][]fieldDecl{
	"PathFact": {
		{"Path", dispEmit, "a path this code was configured with; never the file's contents"},
		{"Exists", dispEmit, "a boolean from os.Lstat"},
		{"IsDir", dispEmit, "a boolean from os.Lstat"},
		{"Mode", dispEmit, "permission bits, rendered octal — the diagnostic for a key file the daemon cannot read"},
		{"SizeBytes", dispEmit, "a byte count; a key file's length is not its value"},
		{"ModTime", dispEmit, "RFC3339 timestamp from os.Lstat"},
		{"Error", dispScrub, "an os.Lstat error string, which embeds a path"},
	},
	"BundleEnvironment": {
		{"Tool", dispEmit, "the literal \"vnproxctl\""},
		{"ToolVersion", dispEmit, "the build's version string"},
		{"GoVersion", dispEmit, "runtime.Version()"},
		{"OS", dispEmit, "runtime.GOOS"},
		{"Arch", dispEmit, "runtime.GOARCH"},
		{"Node", dispEmit, "this node's hostname — cluster topology, not a credential (docs/security.md states this explicitly)"},
		{"CollectedAt", dispEmit, "RFC3339 timestamp"},
		{"TimeZone", dispEmit, "the local zone abbreviation"},
		{"UTCOffsetSec", dispEmit, "an integer; clock skew breaks ticket lifetimes and commit-confirm timers"},
		{"Kernel", dispScrub, "/proc/sys/kernel/osrelease, a string this code did not generate"},
		{"OSRelease", dispScrub, "PRETTY_NAME from /etc/os-release, a string this code did not generate"},
		{"EffectiveUID", dispEmit, "os.Geteuid(); 'not running as root' explains a whole class of permission faults"},
		{"ConfigPath", dispEmit, "the --config path this invocation was given"},
		{"ProxmoxPresent", dispEmit, "whether /etc/pve exists"},
		{"Paths", dispEmit, "a slice of PathFact, itself declared above"},
	},
	"BundleConfig": {
		{"Path", dispEmit, "the config path this invocation was given"},
		{"Parsed", dispEmit, "a boolean"},
		{"ParseError", dispScrub, "a TOML decode error, which quotes the offending line"},
		{"Keys", dispEmit, "a slice of ConfigKey, itself declared below"},
	},
	"ConfigKey": {
		{"Key", dispEmit, "a dotted key NAME from the operator's file; names are listed even when values are not, because knowing [oidc] is configured at all is diagnostic"},
		{"Value", dispAllowlist, "emitted only if Key is in configKeyAllowlist; otherwise replaced with Redacted"},
		{"Redacted", dispEmit, "a boolean saying which of the two happened"},
		{"Reason", dispEmit, "one of a fixed set of English reasons this code composed"},
	},
	"BundleStore": {
		{"Path", dispEmit, "the store path from the config"},
		{"Exists", dispEmit, "a boolean from os.Stat"},
		{"SizeBytes", dispEmit, "a byte count"},
		{"WALBytes", dispEmit, "a byte count; a large -wal is itself a symptom"},
		{"SchemaVersion", dispEmit, "an integer read through store.InspectReadOnly, which cannot migrate"},
		{"BinarySchemaVersion", dispEmit, "an integer from store.LatestSchemaVersion"},
		{"MigrationState", dispEmit, "one of four fixed English strings this code composes"},
		{"IntegrityCheck", dispScrub, "PRAGMA integrity_check's result, which names pages and indexes on failure"},
		{"RuntimeLockHeld", dispEmit, "a boolean from store.RuntimeLockHeld — is a daemon actually running"},
		{"Error", dispScrub, "an error string, which embeds a path"},
		{"Tables", dispEmit, "a slice of TableFact, itself declared below"},
	},
	"TableFact": {
		{"Name", dispEmit, "a table name from sqlite_master — schema, not data"},
		{"Rows", dispEmit, "COUNT(*); no column value is ever read by this collector"},
	},
	"BundleChangesets": {
		{"Limit", dispEmit, "the requested tail length"},
		{"Total", dispEmit, "COUNT(*) over changesets"},
		{"Error", dispScrub, "an error string, which embeds a path"},
		{"Changesets", dispEmit, "a slice of BundleChangeset, itself declared below"},
	},
	"BundleChangeset": {
		{"ID", dispEmit, "a ULID generated by vnprox"},
		{"Title", dispScrub, "operator-typed free text"},
		{"Author", dispScrub, "a PVE username; identity, not a credential, but scrubbed anyway in case a ticket was pasted into it"},
		{"Status", dispEmit, "one of the documented status enum values"},
		{"Origin", dispEmit, "'ui'|'mcp'|'cli'"},
		{"ClusterID", dispEmit, "an id generated by vnprox"},
		{"CreatedAt", dispEmit, "a unix timestamp"},
		{"UpdatedAt", dispEmit, "a unix timestamp"},
		{"ConfirmDeadline", dispEmit, "a unix timestamp"},
		{"RevertTicketSealed", dispEmit, "whether a T-1805 ticket EXISTS; changesets.revert_ticket_enc is not in this collector's SELECT at all, so the ciphertext is never read, not merely dropped"},
		{"RevertTicketExpiresAt", dispEmit, "a unix timestamp; the expiry is not the ticket (see store.Changeset's own note on why it is the only half in the read model)"},
		{"OpCount", dispEmit, "a count"},
		{"Ops", dispRedactJSON, "changesets.ops_json — op params whose shape internal/change grows every phase; walked by key name so a sealed field invented later is redacted on arrival"},
		{"Plan", dispRedactJSON, "changesets.plan_json — the diff the card asks for, through the same walk"},
		{"ApplyLog", dispRedactJSON, "changesets.apply_log_json — per-node apply output, which can echo a command line"},
		{"Findings", dispRedactJSON, "changesets.findings_json — validation findings, free-form messages"},
	},
	"BundleIncident": {
		{"ID", dispEmit, "a ULID generated by vnprox"},
		{"Title", dispScrub, "operator-typed free text, exactly like a changeset title"},
		{"Status", dispEmit, "'open'|'closed'"},
		{"OpenedBy", dispScrub, "a PVE username; identity, not a credential, but scrubbed anyway in case a ticket was pasted into it"},
		{"OpenedAt", dispEmit, "a unix timestamp"},
		{"StartedAt", dispEmit, "a unix timestamp"},
		{"EndedAt", dispEmit, "a unix timestamp"},
		{"ClosedAt", dispEmit, "a unix timestamp"},
		{"Retroactive", dispEmit, "a boolean derived from openedAt vs startedAt"},
		{"WindowFrom", dispEmit, "a unix timestamp"},
		{"WindowTo", dispEmit, "a unix timestamp"},
		{"WindowLive", dispEmit, "a boolean: was the window's end resolved from the clock"},
		{"EventCount", dispEmit, "a count"},
		{"Events", dispEmit, "a slice of BundleIncidentEvent, itself declared below"},
		{"Sources", dispEmit, "a slice of BundleIncidentSource, itself declared below"},
		{"Caveats", dispScrub, "generated English that quotes source error strings, which embed paths"},
		{"DiffErrorCode", dispEmit, "one of docs/api.md's stable error codes"},
		{"DiffError", dispScrub, "the change engine's own refusal message, which names snapshot ids and times"},
		{"Diff", dispEmit, "a *BundleIncidentDiff, itself declared below"},
	},
	"BundleIncidentEvent": {
		{"At", dispEmit, "a unix timestamp"},
		{"Source", dispEmit, "one of the six timeline sources"},
		{"Kind", dispEmit, "a per-source enum vnprox composes (an audit action, a finding transition, a flow source)"},
		{"Summary", dispScrub, "free text: an operator's own annotation body, or a line vnprox composed from cluster-supplied names"},
		{"Actor", dispScrub, "a PVE username, scrubbed like every other identity a bundle carries"},
		{"Node", dispScrub, "a node name from the cluster, not from this code"},
		{"Ref", dispScrub, "an inventory Ref or finding id, composed from node/interface names the cluster supplied"},
		{"Result", dispScrub, "an audit result string; 'success'/'failure' in practice, but written by many call sites"},
		{"ChangesetID", dispEmit, "a ULID generated by vnprox"},
		{"CaptureID", dispEmit, "an id generated by vnprox"},
	},
	"BundleIncidentSource": {
		{"Source", dispEmit, "one of the six timeline sources"},
		{"Status", dispEmit, "'ok'|'unavailable'|'error'|'truncated'"},
		{"Count", dispEmit, "a count"},
		{"Detail", dispScrub, "why a source contributed nothing, which quotes an error string"},
	},
	"BundleIncidentDiff": {
		{"FromAt", dispEmit, "a unix timestamp"},
		{"ToAt", dispEmit, "a unix timestamp"},
		{"FromSnapshotID", dispEmit, "a ULID generated by vnprox"},
		{"ToSnapshotID", dispEmit, "a ULID generated by vnprox"},
		{"Added", dispEmit, "a count"},
		{"Removed", dispEmit, "a count"},
		{"Modified", dispEmit, "a count"},
		{"Unattributed", dispEmit, "a count of differences no changeset explains"},
		{"ComparedPaths", dispEmit, "paths this code named as the diff's scope (/etc/network/interfaces)"},
		{"OmittedPaths", dispEmit, "paths this code named as outside the diff's scope"},
		{"UnmatchedNodes", dispScrub, "node names from the cluster"},
		{"Entries", dispEmit, "a slice of BundleIncidentDiffEntry, itself declared below"},
	},
	"BundleIncidentDiffEntry": {
		{"Change", dispEmit, "'added'|'removed'|'modified'"},
		{"Ref", dispScrub, "an inventory Ref composed from cluster-supplied names"},
		{"Kind", dispEmit, "an entity kind from vnprox's own closed vocabulary"},
		{"Node", dispScrub, "a node name from the cluster"},
		{"Name", dispScrub, "an interface name from the cluster"},
		{"Attributed", dispEmit, "a boolean: did a changeset explain this difference"},
		{"ChangesetID", dispEmit, "a ULID generated by vnprox"},
		{"Actor", dispScrub, "a PVE username"},
		{"Fields", dispScrub, "the NAMES of the interfaces(5) options that changed — never their values, which is where a wireguard-private-key lives (see bundleincident.go's header)"},
	},
	"BundleFindings": {
		{"Limit", dispEmit, "the requested tail length"},
		{"Total", dispEmit, "COUNT(*) over finding_events"},
		{"Error", dispScrub, "an error string, which embeds a path"},
		{"Events", dispEmit, "a slice of BundleFindingEvent, itself declared below"},
	},
	"BundleFindingEvent": {
		{"FindingID", dispScrub, "an id vnprox composes from node/interface names; scrubbed because those names come from the cluster, not from this code"},
		{"Transition", dispEmit, "'new'|'escalated'|'resolved'"},
		{"At", dispEmit, "a unix timestamp"},
	},
	"BundleHostNetwork": {
		{"Path", dispEmit, "the interfaces(5) path this invocation was configured with"},
		{"Parsed", dispEmit, "a boolean"},
		{"ParseError", dispScrub, "a parse error, which quotes the offending line"},
		{"Auto", dispScrub, "interface names from the operator's file"},
		{"Sources", dispScrub, "`source`/`source-directory` paths from the operator's file"},
		{"Ifaces", dispEmit, "a slice of BundleIface, itself declared below"},
	},
	"BundleIface": {
		{"Name", dispScrub, "an interface name from the operator's file"},
		{"Family", dispEmit, "'inet'|'inet6'|..."},
		{"Method", dispEmit, "'static'|'manual'|'dhcp'|..."},
		{"Inherits", dispScrub, "template names from the operator's file"},
		{"Options", dispEmit, "a slice of BundleIfaceOption, itself declared below"},
	},
	"BundleIfaceOption": {
		{"Key", dispEmit, "the option NAME, listed even when the value is not: knowing a stanza carries wireguard-private-key is diagnostic, and the name is not the key"},
		{"Value", dispAllowlist, "emitted only if Key is in ifaceOptionAllowlist AND survives Scrub; otherwise replaced. This is the field that would leak a WireGuard private key out of /etc/network/interfaces if it were not gated"},
		{"Redacted", dispEmit, "a boolean saying which of the two happened"},
	},
	"BundlePeers": {
		{"Source", dispEmit, "the corosync.conf path this invocation was configured with"},
		{"Available", dispEmit, "whether that file was readable"},
		{"Note", dispScrub, "an explanation string, which can embed a read error"},
		{"Probed", dispEmit, "whether reachability was actually attempted (--no-probe turns it off)"},
		{"Peers", dispEmit, "a slice of BundlePeer, itself declared below"},
	},
	"BundlePeer": {
		{"Node", dispScrub, "a node name from corosync.conf"},
		{"Addr", dispScrub, "a ring address from corosync.conf"},
		{"Port", dispEmit, "the peer API port from this node's config"},
		{"State", dispEmit, "T-1906's trichotomy: 'ok'|'unreachable'|'untrusted'|'skipped'"},
		{"LatencyMS", dispEmit, "a measured duration"},
		{"TLSSubject", dispScrub, "a certificate subject presented by a REMOTE party — attacker-controlled text in the untrusted case, which is exactly the case it matters in"},
		{"TLSIssuer", dispScrub, "same, and the field that tells 'wrong CA' apart from 'no route'"},
		{"TLSNotAfter", dispEmit, "an RFC3339 timestamp this code formats"},
		{"Error", dispScrub, "a dial/handshake error, which embeds remote-supplied text"},
	},
	"BundleProbes": {
		{"Listen", dispEmit, "a ListenProbe, itself declared below"},
		{"Daemon", dispEmit, "a DaemonProbe, itself declared below"},
		{"KeyFiles", dispEmit, "a slice of KeyFileProbe, itself declared below"},
	},
	"ListenProbe": {
		{"Address", dispEmit, "[server] listen from the config"},
		{"State", dispEmit, "'free'|'in-use'|'unparsable'|'skipped'"},
		{"Error", dispScrub, "a bind error string"},
	},
	"DaemonProbe": {
		{"URL", dispEmit, "the /api/v1/health URL this code composed from [server] listen"},
		{"Reachable", dispEmit, "a boolean"},
		{"Status", dispEmit, "the health endpoint's status field"},
		{"Version", dispScrub, "the daemon's reported version string"},
		{"TLSSubject", dispScrub, "the local daemon's certificate subject"},
		{"TLSIssuer", dispScrub, "the local daemon's certificate issuer"},
		{"Error", dispScrub, "a request error string"},
		{"Collectors", dispEmit, "a slice of DaemonCollector, itself declared below"},
	},
	"DaemonCollector": {
		{"Name", dispEmit, "'pve'|'host'|'lldp'"},
		{"Node", dispScrub, "a cluster node name"},
		{"LastSuccess", dispEmit, "an RFC3339 timestamp"},
		{"LastAttempt", dispEmit, "an RFC3339 timestamp"},
		{"LastError", dispScrub, "the collector's last error — the single most useful string in a bundle, and the one most likely to quote a URL with credentials in it"},
		{"ConsecutiveFailures", dispEmit, "a count"},
	},
	"KeyFileProbe": {
		{"ClassID", dispEmit, "a SecretClass.ID from T-1901's declared inventory"},
		{"Name", dispEmit, "that class's operator-facing name"},
		{"File", dispEmit, "a PathFact — existence and mode only. This collector never opens the file"},
	},
	"BundleLogs": {
		{"Source", dispEmit, "how the log was obtained ('journalctl -u vnprox' or a file path)"},
		{"Collected", dispEmit, "a boolean"},
		{"Note", dispScrub, "an explanation string, which can embed a command error"},
		{"Lines", dispEmit, "a count"},
		{"Bytes", dispEmit, "a count"},
		{"Truncated", dispEmit, "whether the tail budget was hit"},
		{"Scrubbed", dispEmit, "how many lines Scrub changed — a nonzero count is the evidence that redaction ran"},
	},
}

// ---------------------------------------------------------------- entries

// entryDecl declares one file a bundle may contain.
type entryDecl struct {
	Name string
	Role string
	// Doc is the Go type serialised into this entry, or "" for entries that
	// are not a structured document (the readme, the log text).
	Doc string
	// Redaction describes, for the human reading the bundle, what was done
	// to this file's contents.
	Redaction string
	// About is the one-line description rendered into readme.txt, so the
	// bundle's "what's in here" is GENERATED from this declaration and
	// cannot describe a file that is not there (or omit one that is).
	About string
}

// bundleEntrySchema is every entry a bundle may contain.
//
// Note what is absent and must stay absent: there is no RoleStore entry.
// A support bundle never contains store/vnprox.db — it would carry the
// whole audit trail and every sealed credential's ciphertext into a forum
// thread. There is no RoleKey entry either, and manifest.validate would
// reject one anyway unless the manifest also declared IncludesKeyMaterial,
// which a bundle structurally cannot (see bundle.go's bundleCollector).
//
//nolint:gochecknoglobals // a declared inventory, deliberately package-level.
var bundleEntrySchema = []entryDecl{
	{entryBundleReadme, RoleMeta, "", "generated text",
		"what this bundle contains, what it deliberately omits, and how it was redacted."},
	{entryBundleEnvironment, RoleMeta, "BundleEnvironment", "typed fields, allowlisted",
		"node identity, build, kernel, clock, and the existence/mode of the paths vnprox depends on."},
	{entryBundleConfig, RoleConfig, "BundleConfig", "per-key allowlist",
		"vnprox.toml rendered key by key; any key not on the bundle's allowlist keeps its name and loses its value."},
	{entryBundleStore, RoleMeta, "BundleStore", "derived facts only",
		"the store's schema version, size, integrity and per-table row counts. The store FILE is never included."},
	{entryBundleChangesets, RoleMeta, "BundleChangesets", "typed fields + redactJSON",
		"the most recent changesets with their ops, plan and apply log, walked by key name and redacted."},
	{entryBundleFindings, RoleMeta, "BundleFindings", "typed fields, scrubbed",
		"the stored finding-transition history (new/escalated/resolved)."},
	{entryBundleHostNet, RoleMeta, "BundleHostNetwork", "per-option allowlist",
		"/etc/network/interfaces as structure: stanzas and allowlisted options. Never the file's text."},
	{entryBundlePeers, RoleMeta, "BundlePeers", "typed fields, scrubbed",
		"cluster peers from corosync.conf and, for each, ok / unreachable / untrusted."},
	{entryBundleProbes, RoleMeta, "BundleProbes", "typed fields, scrubbed",
		"live checks: is the listen port taken, is the daemon answering, do the declared key files exist and with what mode."},
	{entryBundleLogSummary, RoleMeta, "BundleLogs", "typed fields, scrubbed",
		"where the daemon log came from, how much was taken, and how many lines redaction changed."},
	{entryBundleLog, RoleMeta, "", "line-by-line Scrub",
		"the tail of the daemon's log, with every credential-shaped substring removed."},
	{entryBundleIncident, RoleMeta, "BundleIncident", "typed fields, scrubbed",
		"one incident's window and its merged timeline: findings, changesets, diagnosis runs, captures, flows and the operator's own notes. Present only in an incident export."},
}

// entryDeclFor looks an entry declaration up by name.
func entryDeclFor(name string) (entryDecl, bool) {
	for _, d := range bundleEntrySchema {
		if d.Name == name {
			return d, true
		}
	}
	return entryDecl{}, false
}

// ------------------------------------------------------- reflection walk

// schemaProblem is one disagreement between the declared schema and the
// real Go types.
type schemaProblem struct {
	Type    string
	Field   string
	Message string
}

func (p schemaProblem) String() string {
	if p.Field == "" {
		return fmt.Sprintf("%s: %s", p.Type, p.Message)
	}
	return fmt.Sprintf("%s.%s: %s", p.Type, p.Field, p.Message)
}

// checkSchema walks every type reachable from roots and compares it to
// schema, returning every disagreement.
//
// It is a pure function returning a slice rather than a *testing.T consumer
// for the same reason T-1807's CheckSeededV1 is: the enforcement test needs
// to assert that this walker FINDS problems when there are some (see
// TestBundleSchema_TheEnforcementIsNotVacuous), and a checker that calls
// t.Errorf itself cannot be asked that question.
func checkSchema(roots []reflect.Type, schema map[string][]fieldDecl) []schemaProblem {
	var problems []schemaProblem
	seen := map[string]bool{}
	visitedTypes := map[reflect.Type]bool{}

	var visit func(t reflect.Type)
	visit = func(t reflect.Type) {
		t = deref(t)
		if t.Kind() != reflect.Struct || visitedTypes[t] {
			return
		}
		visitedTypes[t] = true
		name := t.Name()
		if name == "" {
			problems = append(problems, schemaProblem{Type: t.String(), Message: "anonymous struct types cannot be declared; give it a name"})
			return
		}
		seen[name] = true

		decls, ok := schema[name]
		if !ok {
			problems = append(problems, schemaProblem{Type: name, Message: "type is emitted by a bundle document but has no entry in bundleFieldSchema — declare every field and why it is safe to put in a forum post"})
			return
		}
		byField := make(map[string]fieldDecl, len(decls))
		for _, d := range decls {
			if _, dup := byField[d.Field]; dup {
				problems = append(problems, schemaProblem{Type: name, Field: d.Field, Message: "declared twice"})
			}
			byField[d.Field] = d
		}

		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				// Unexported: encoding/json cannot serialise it, so it
				// cannot reach a bundle.
				continue
			}
			d, declared := byField[f.Name]
			if !declared {
				problems = append(problems, schemaProblem{Type: name, Field: f.Name,
					Message: "field is emitted but not declared in bundleFieldSchema — add a fieldDecl saying how it is made safe and why"})
				continue
			}
			delete(byField, f.Name)
			if strings.TrimSpace(d.Why) == "" {
				problems = append(problems, schemaProblem{Type: name, Field: f.Name,
					Message: "declared with an empty reason — a collector that cannot describe its output does not ship"})
			}
			if !validDisposition(d.Disp) {
				problems = append(problems, schemaProblem{Type: name, Field: f.Name,
					Message: fmt.Sprintf("unknown disposition %q", d.Disp)})
			}
			if opaque(f.Type) && d.Disp == dispEmit {
				problems = append(problems, schemaProblem{Type: name, Field: f.Name,
					Message: fmt.Sprintf("type %s is opaque to reflection, so it cannot be declared %q — "+
						"it must name a redactor (%q or %q)", f.Type, dispEmit, dispRedactJSON, dispAllowlist)})
			}
			visitElems(f.Type, visit)
		}
		for field := range byField {
			problems = append(problems, schemaProblem{Type: name, Field: field,
				Message: "declared in bundleFieldSchema but no such field exists — the schema has stopped describing the code"})
		}
	}

	for _, r := range roots {
		visit(r)
	}

	for name := range schema {
		if !seen[name] {
			problems = append(problems, schemaProblem{Type: name,
				Message: "declared in bundleFieldSchema but not reachable from any bundle document — remove it or wire it up"})
		}
	}

	sort.Slice(problems, func(i, j int) bool {
		if problems[i].Type != problems[j].Type {
			return problems[i].Type < problems[j].Type
		}
		return problems[i].Field < problems[j].Field
	})
	return problems
}

func validDisposition(d disposition) bool {
	switch d {
	case dispEmit, dispScrub, dispRedactJSON, dispAllowlist:
		return true
	default:
		return false
	}
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// visitElems recurses into the struct types a field's type contains.
func visitElems(t reflect.Type, visit func(reflect.Type)) {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		// json.RawMessage and any other []byte is a leaf, not a slice of
		// something worth walking into.
		if t != rawMessageType && (t.Kind() != reflect.Slice || t.Elem().Kind() != reflect.Uint8) {
			visitElems(t.Elem(), visit)
		}
	case reflect.Map:
		visitElems(t.Elem(), visit)
	case reflect.Struct:
		visit(t)
	default:
	}
}

//nolint:gochecknoglobals // a reflect.Type constant.
var rawMessageType = reflect.TypeOf(json.RawMessage{})

// opaque reports whether reflection can see through t well enough for
// "declared" to mean anything. json.RawMessage, []byte, any interface, and
// any map are all shapes whose *contents* are decided at runtime, so a
// field of such a type is only safe if something redacts its values.
func opaque(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Interface, reflect.Map, reflect.UnsafePointer, reflect.Chan, reflect.Func:
		return true
	case reflect.Slice, reflect.Array:
		if t == rawMessageType || t.Elem().Kind() == reflect.Uint8 {
			return true
		}
		return opaque(t.Elem())
	case reflect.Pointer:
		return opaque(t.Elem())
	default:
		return false
	}
}
