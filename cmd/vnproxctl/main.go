// SPDX-License-Identifier: Apache-2.0

// Command vnproxctl is the vnprox operator CLI documented in
// docs/deployment.md "Troubleshooting quick refs" and docs/user-guide.md §6:
// a root-only, daemon-independent escape hatch for status checks and
// disaster recovery (snapshot restore, forced rollback) when the web UI is
// unreachable — plus (T-1105) an HTTP-backed command family for driving the
// change engine's read and changeset surfaces from CI/GitOps, over the
// exact same stage→validate→diff→apply→confirm/rollback safety guarantee
// the UI uses (CLAUDE.md: never a second mutation path).
//
// `status` talks to the local daemon's health endpoint; `snapshots
// list|restore` and `rollback-now` (T-206) are deliberately daemon-
// independent — direct SQLite reads of the daemon's own store (WAL mode
// permits this alongside a running daemon too), a local
// /etc/network/interfaces write with a timestamped backup, and a direct
// `ifreload -a` exec. No HTTP API is involved anywhere on that path: it
// must work when the daemon is stopped, because it *is* the documented
// recovery path for exactly that situation. T-606
// (planning/tasks/phase-6.md#T-606) wires this binary into the final
// packaging.
//
// `remote <subcommand>` and `apply` (T-1105) are the opposite: they require
// the daemon to be up, talk to its documented /api/v1 HTTP surface
// (docs/api.md) exclusively over a T-1104 bearer token (--token/
// VNPROX_TOKEN — never a PVE username/password from this CLI), and are
// namespaced under their own top-level command specifically so they never
// collide with `status`/`snapshots`/`rollback-now`'s existing daemon-
// independent meaning — see remote.go's package doc comment for the full
// naming-collision write-up this task card required. Every command in this
// binary supports `-o table` (default) or `-o json`; see exitcodes.go for
// the documented, stable exit-code table CI can branch on.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

// version is vnproxctl's reported version (--version). Overridden at build
// time the same way as vnproxd: go build -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// commandSpec is one top-level vnproxctl subcommand. commandTable below is
// the single source of truth both run's dispatch and `vnproxctl completion`
// (completion.go, T-4011) read: a subcommand added to this table is
// dispatchable AND completable in the same edit, so a new subcommand cannot
// silently lack completion the way it could if completion were a
// hand-maintained second list.
type commandSpec struct {
	run         func(args []string, stdout, stderr io.Writer) int
	name        string
	subcommands []string // leaf verbs a shell should offer after this command's name; nil for a flag-only command
}

// commandTable lists every top-level vnproxctl subcommand except the three
// handled directly in run below (-h/--help/help, --version, completion
// itself — none of these dispatches to a run* function the way every entry
// here does).
var commandTable = []commandSpec{
	{name: "status", run: runStatus},
	{name: "snapshots", subcommands: []string{"list", "restore"}, run: runSnapshots},
	{name: "rollback-now", run: runRollbackNow},
	{name: "backup", run: runBackup},
	{name: "restore", run: runRestore},
	{name: "certs", run: runCerts},
	{name: "doctor", run: runDoctor},
	{name: "upgrade-check", run: runUpgradeCheck},
	{name: "verify", run: runVerify},
	{name: "support-bundle", run: runSupportBundle},
	{name: "telemetry", subcommands: []string{"preview", "status", "send", "reset-id"}, run: runTelemetry},
	{name: "remote", subcommands: []string{"topology", "changesets", "findings", "drift", "audit"}, run: runRemote},
	{name: "policy", subcommands: []string{"lint", "examples", "test"}, run: runPolicy},
	{name: "gitsync", subcommands: []string{"status"}, run: runGitSync},
	{name: "hub", subcommands: []string{"publish", "index", "revoke", "verify", "keygen"}, run: runHub},
	{name: "plugin", subcommands: []string{"scaffold"}, run: runPlugin},
	{name: "apply", run: runApply},
	{name: "spec", subcommands: []string{"export", "import", "pin", "unpin"}, run: runSpec},
	{name: "watch", run: runWatch},
	{name: "wireguard", subcommands: []string{"list", "show", "create", "update", "delete", "peer-add", "peer-remove"}, run: runWireguard},
}

// run implements main's logic in a way that's testable without exec'ing a
// subprocess: it returns an exit code instead of calling os.Exit and takes
// explicit stdout/stderr writers, mirroring cmd/vnproxd's mainRun.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	case "--version":
		if _, err := fmt.Fprintf(stdout, "vnproxctl %s\n", version); err != nil {
			return 1
		}
		return 0
	case "completion":
		return runCompletion(args[1:], stdout, stderr)
	}

	for _, c := range commandTable {
		if c.name == args[0] {
			return c.run(args[1:], stdout, stderr)
		}
	}
	_, _ = fmt.Fprintf(stderr, "vnproxctl: unknown command %q\n\n", args[0])
	printUsage(stderr)
	return 2
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `vnproxctl - vnprox operator CLI

Usage:
  vnproxctl status                     Local daemon health/reachability check
  vnproxctl snapshots list             List time-machine snapshots (direct DB read; works daemon-down)
  vnproxctl snapshots restore <id>     Restore this node's interfaces file from a snapshot and ifreload
                                       (root only; direct DB + file write, bypasses confirm — the
                                       documented disaster-recovery path, works daemon-down)
  vnproxctl rollback-now <changeset>   Force rollback of an in-flight (awaiting-confirm/applying)
                                       changeset from its pre-apply snapshot (root only, daemon-down)
  vnproxctl backup                     Write an integrity-checked archive of vnprox's own state
                                       (consistent store snapshot + config). Safe with the daemon
                                       running. Key material ONLY with --include-keys.
  vnproxctl restore <archive>          Replace this node's store from a backup archive: refuses
                                       against a running daemon, refuses a store from a newer
                                       vnprox, forward-migrates, and swaps atomically.
  vnproxctl doctor                     Preflight and self-check: config, key permissions, pmxcfs,
                                       schema version, disk headroom, port conflicts, and (when the
                                       daemon is reachable) PVE access, privileges, peer-secret
                                       agreement and clock skew. Every problem names what to do
                                       about it. Read-only; works before install and daemon-down.
                                       Exits non-zero if any check FAILS (warnings do not gate).
  vnproxctl upgrade-check <version>    Preflight before upgrading a node's PVE version: checks a
                                       sourced catalog of known network-affecting PVE breaks (the
                                       T-3711 conntrack-procfs class, the nftables/iptables firewall
                                       engine split) against this host's live, probed state and
                                       reports which would fire on the way to <version> (e.g. "9.2").
                                       Read-only; same report shape as vnproxctl doctor.
                                       Exits non-zero only if a check FAILS (a warn does not gate).
  vnproxctl verify                     Run the hardware-validation suite against this cluster and
                                       print (and optionally sign, with --out) a report naming what
                                       was observed and the evidence each verdict rests on. Refuses
                                       to run against a mock PVE endpoint without --allow-mock.
                                       A check that cannot run reports SKIP with the hardware it
                                       needs — never PASS. Exits non-zero on any failure AND on a
                                       run in which nothing passed.
  vnproxctl support-bundle             Write a REDACTED diagnostic archive meant to be attached to
                                       a support thread: environment, allowlisted config, store
                                       facts (never the store), redacted changesets, scrubbed logs,
                                       peer reachability and live probes. Contains no credential.
  vnproxctl telemetry <sub>             Opt-in compatibility reporting, OFF by default and with no
                                       default endpoint: check ids and verdicts, versions, kernel,
                                       NIC hardware ids and a node count — never a hostname,
                                       address, MAC, guest or cluster name. "preview" prints the
                                       exact bytes that would be sent, "status" says whether it is
                                       on, "send" submits one report, "reset-id" throws away the
                                       correlator. See docs/security.md for the full field list.

HTTP-backed commands (T-1105) — require the daemon up and --token/VNPROX_TOKEN
(a T-1104 bearer token; never a PVE username/password from this CLI):

  vnproxctl remote topology              GET /topology
  vnproxctl remote changesets list       GET /changesets
  vnproxctl remote changesets get <id>   GET /changesets/{id}
  vnproxctl remote changesets diff <id>  GET /changesets/{id}/diff
  vnproxctl remote changesets create     POST /changesets (-f <file>, "-" = stdin)
  vnproxctl remote changesets validate <id>  POST /changesets/{id}/validate
  vnproxctl remote changesets apply <id>     POST /changesets/{id}/apply
  vnproxctl remote changesets confirm <id>   POST /changesets/{id}/confirm
  vnproxctl remote changesets rollback <id>  POST /changesets/{id}/rollback
  vnproxctl remote changesets discard <id>   DELETE /changesets/{id}
  vnproxctl remote findings              GET /findings
  vnproxctl remote drift                 GET /drift
  vnproxctl remote audit                 GET /audit
  vnproxctl apply <spec.yaml> --plan     POST /spec/import, print diff, exit 3 if pending
  vnproxctl apply <spec.yaml> --apply    ...then apply + poll to committed + auto-confirm
  vnproxctl policy lint --policy=f.yaml  validate a policy document locally (no daemon needed)
  vnproxctl policy examples              print the shipped example policy document
  vnproxctl policy test --policy=f.yaml --changeset=<id>
                                         POST /policies/test — evaluate rules against a real
                                         changeset without staging anything; exit 3 on a deny
  vnproxctl gitsync status               GET /gitsync/status — the git spec sync's last fetched
                                         commit, its last plan, and why its draft changeset is
                                         open. Read-only: there is no sync-now or apply verb,
                                         because a sync draft is applied like any other changeset

  vnproxctl spec export [--out <path>]   GET /spec — write the live cluster's declarative spec
                                         (config-as-code) as YAML to stdout or --out
  vnproxctl spec import <file>           POST /spec/import — diff a spec against live, stage a
                                         draft changeset, print its id and notInSpec. Never
                                         applies; review and apply it like any other changeset
  vnproxctl spec pin [<file>]            GET /spec/pin (bare) or POST /spec/pin <file> — the
                                         GitOps reconciler's declared desired state
  vnproxctl spec unpin                   DELETE /spec/pin

  vnproxctl wireguard list                GET /wireguard/tunnels, with each tunnel's up|down
                                         verdict (WgTunnelHasFreshHandshake — the one definition
                                         findings/UI/CLI all share)
  vnproxctl wireguard show <id>           One tunnel's full detail, filtered from the list above
  vnproxctl wireguard create --node --id --if-name [--carrier --addresses --listen-port --mtu]
                                         Stage a wg.tunnel.create op as a draft changeset
  vnproxctl wireguard update <id> --node [--listen-port --mtu --carrier --addresses]
                                         Stage a wg.tunnel.update op (only the flags you pass change)
  vnproxctl wireguard delete <id> --node  Stage a wg.tunnel.delete op
  vnproxctl wireguard peer-add <tunnelId> --node --public-key [--endpoint --allowed-ips
                                         --preshared-key --keepalive-sec --cluster-id]
                                         Stage a wg.peer.add op (re-submitting an existing
                                         public-key edits that peer in place)
  vnproxctl wireguard peer-remove <tunnelId> --node --public-key
                                         Stage a wg.peer.remove op
                                         Every wireguard write subcommand stages a draft changeset
                                         and stops — review with remote changesets diff, apply
                                         with remote changesets apply (never a second mutation path)

  vnproxctl watch                        Live view over the WS "events" topic (T-1104, frozen at
                                         D10): findings, applies and drift as they happen. Requires
                                         a token with the "automation" scope (fails fast with a
                                         clear error otherwise); reconnects on drop, printing the
                                         gap rather than going quiet. Plain line-oriented output
                                         when stdout isn't a terminal; -o json for
                                         newline-delimited events.

  vnproxctl completion bash|zsh          Print a shell completion script for this binary's
                                         subcommands and flags, generated from the same dispatch
                                         table main.go uses — source it, don't hand-maintain it

  vnproxctl hub <subcommand>           Publish to, and audit, the signed blueprint/plugin
                                       registry the Hub browses (T-2803): publish | index |
                                       revoke | verify | keygen. Local file work only — the
                                       registry is static hosting, not a service. Run
                                       "vnproxctl hub" for the subcommand reference.

  vnproxctl plugin scaffold <name>     Stamp out a complete, minimal, compiling
                                       findingProducer plugin (examples/plugin-template/) into
                                       a new directory renamed to <name>. Local file work only.
                                       Run "vnproxctl plugin --help" for flags and the
                                       in-process-vs-out-of-process note.

  vnproxctl --version                  Print the vnproxctl version
  vnproxctl certs                      Cluster TLS certificate inventory and problems
                                       (direct pmxcfs read; works daemon-down — which is when a
                                       certificate problem has usually taken the API with it)
  vnproxctl --help                     Show this help

verify flags:
  --suite <name>    hardware (default), multinode, or destructive
  --only <ids>      comma-separated check ids, instead of a whole suite (unknown id = error)
  --list            print every registered check, its matrix row and its hardware precondition
  --out <path>      write the signed report artifact here (re-verified after writing)
  --sign-key <path> Ed25519 key for the report (default: ephemeral — detects tampering, no provenance)
  --allow-mock      run against a mock/replay PVE endpoint; the report is stamped and is NOT
                    hardware evidence
  --i-understand    required by --suite=destructive; without it no write client is constructed
  --pve-url/--pve-token   the PVE endpoint to validate (default: [pve] from --config)
  --url/--token/--config/--insecure/-o    as for the remote family

policy flags:
  --policy <path>   policy YAML document (test: default is the cluster's installed rule set)
  --changeset <id>  changeset to evaluate against (test only, required)
  --token/--url/--config/--timeout/--insecure/-o  as for the remote family (test only)

status flags:
  --config <path>   vnprox.toml to read the listen address from (default /etc/vnprox/vnprox.toml)
  --url <url>       health endpoint URL, overriding --config lookup
  --insecure        skip TLS verification (default true; see docs/deployment.md)
  --timeout <dur>   request timeout (default 5s)
  -o <table|json>   output format (default table)

snapshots/rollback-now flags:
  --config <path>   vnprox.toml to read storage.db_path from (default /etc/vnprox/vnprox.toml)
  --node <name>     which captured node's file to restore (default: this host's name)
  --limit <n>       snapshots list: maximum rows (default 50)
  -o <table|json>   output format (default table)

backup flags:
  --config <path>   vnprox.toml to read paths from (default /etc/vnprox/vnprox.toml)
  --out-dir <dir>   where to write the archive (default /var/lib/vnprox/backups)
  --out <path>      exact archive path, overriding --out-dir
  --include-keys    ALSO archive key material — prints a warning naming exactly what that
                    means and requires an interactive confirmation (or --yes)
  --yes             skip --include-keys' interactive confirmation (the warning still prints)
  --keep <n>        after writing, keep only the newest n archives in the output directory
  -o <table|json>   output format (default table)

restore flags:
  --config <path>     vnprox.toml to read storage.db_path and server.listen from
  --dry-run           validate the archive and print the plan; change nothing
  --restore-config    also install the archive's vnprox.toml (current one moved aside)
  --restore-keys      also install the archive's key files (needs an --include-keys archive)
  -o <table|json>     output format (default table)

support-bundle flags:
  --config <path>     vnprox.toml to read paths from (default /etc/vnprox/vnprox.toml)
  --out-dir <dir>     where to write the bundle (default /var/lib/vnprox/support)
  --out <path>        exact archive path, overriding --out-dir
  --dry-run           collect exactly as a real run does, print the contents, write nothing
  --no-probe          make no outbound connection at all
  --changesets <n>    how many recent changesets to include (default 20)
  --log-lines <n>     how many journal lines to include (default 2000)
  --log-file <path>   read the daemon log from a file instead of journalctl
  --log-unit <name>   systemd unit to read the log from (default vnprox)
  --interfaces <path> interfaces(5) to parse (never included verbatim)
  --corosync <path>   corosync.conf to discover peers from
  -o <table|json>     output format (default table)

remote/apply flags (every command in this family):
  --config <path>          vnprox.toml to read the listen address from, absent --url
  --url <url>              override the daemon's /api/v1 base URL, skipping --config
  --token <token>          T-1104 bearer token (or set VNPROX_TOKEN)
  --insecure               skip TLS verification (default true)
  --timeout <dur>          per-request timeout (default 10s)
  -o <table|json>          output format (default table)
  apply's own: --plan | --apply, --confirm-timeout-sec, --apply-timeout (--apply's commit-wait bound)

spec flags (every subcommand): --config/--url/--token/--insecure/--timeout/-o, as for the remote
family. spec export's own: --out <path> (write the YAML there instead of stdout; ignored with
-o json).

wireguard flags: --config/--url/--token/--insecure/--timeout/-o, as for the remote family, on every
subcommand. Write subcommands' own: --node (required — the owning PVE node), --title (changeset
title override). create's own: --id (required), --if-name (required), --carrier, --addresses,
--listen-port, --mtu. update's own: --listen-port, --mtu, --carrier, --addresses (at least one
required; only flags actually passed change). peer-add's own: --public-key (required), --endpoint,
--allowed-ips, --preshared-key, --keepalive-sec, --cluster-id. peer-remove's own: --public-key
(required).

watch flags: --config/--url/--token/--insecure/--timeout/-o, as for the remote family (--timeout
bounds only the /auth/me preflight and each dial handshake, never the live connection itself).
watch's own: --kind <names> (comma-separated event names to show, e.g.
changeset.status,drift.changed; default: all), --max-events <n> (exit after this many events, 0 =
run until Ctrl-C/SIGTERM), --no-color (disable ANSI colors on a TTY). -o json emits
newline-delimited JSON, one object per line (documented in docs/cli-json.md's "watch" section).

Exit codes (stable, documented in exitcodes.go): 0 success, 1 error,
2 usage, 3 validation-failed/plan-pending, 4 auth, 5 network,
6 apply-timeout. The pre-existing status/snapshots/rollback-now commands
keep their original 0/1/2 convention unchanged.

See docs/deployment.md "Troubleshooting quick refs" for full semantics.
`)
}

// discardLogger returns a slog.Logger that drops everything, used when
// vnproxctl calls into internal/config purely to read values back out — an
// operator running a CLI status check should not see the daemon's own
// config-load log lines.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
