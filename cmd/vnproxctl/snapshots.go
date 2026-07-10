package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/store"
)

// cliActor is the audit-log username vnproxctl's disaster-path writes are
// attributed to. It is deliberately not a PVE identity: this path runs as
// local root with no session, and the audit trail should say so plainly.
const cliActor = "root@cli(vnproxctl)"

// snapshotFileEntry mirrors internal/change's files_json entry shape
// ({node,path,sha256}, plus the legacy T-205 inline `content` fallback).
// Redefined here rather than exported from internal/change because the
// files_json column shape is the documented contract (docs/data-model.md
// §2), not that package's internal type.
type snapshotFileEntry struct {
	Node    string `json:"node"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Content string `json:"content"` // legacy pre-0002 inline shape only
}

// cliEnv bundles the host-touching seams of the disaster-recovery commands
// so tests can point them at a temp dir and a fake ifreload — the same
// injection pattern cmd/vnproxd's hostNodeAgent uses. Production values
// come from newCLIEnv.
type cliEnv struct {
	geteuid        func() int
	hostname       func() (string, error)
	ifreload       func(ctx context.Context) error
	now            func() time.Time
	interfacesPath string
}

func newCLIEnv() *cliEnv {
	return &cliEnv{
		geteuid:  os.Geteuid,
		hostname: os.Hostname,
		ifreload: func(ctx context.Context) error {
			cmd := exec.CommandContext(ctx, "ifreload", "-a")
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("ifreload -a: %w: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		},
		interfacesPath: "/etc/network/interfaces",
		now:            time.Now,
	}
}

// openStore opens the daemon's own SQLite DB directly (mechanism note —
// this is the whole point of the disaster path, docs/deployment.md
// "Troubleshooting quick refs": these commands must work with the daemon
// stopped, so there is no HTTP API involved anywhere; vnproxctl reads
// vnprox.toml for storage.db_path, opens the DB with the same
// internal/store package the daemon uses — WAL mode allows a concurrent
// reader/writer even if the daemon *is* up — pulls the snapshot's blob
// content out, writes /etc/network/interfaces itself, and execs
// `ifreload -a` directly).
func openStore(ctx context.Context, configPath string) (*store.DB, error) {
	cfg, err := config.Load(configPath, discardLogger())
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", configPath, err)
	}
	db, err := store.Open(ctx, cfg.Storage.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening vnprox store %s: %w", cfg.Storage.DBPath, err)
	}
	return db, nil
}

// runSnapshots implements `vnproxctl snapshots list|restore` (T-206), the
// documented daemon-independent disaster-recovery path.
func runSnapshots(args []string, stdout, stderr io.Writer) int {
	return runSnapshotsEnv(newCLIEnv(), args, stdout, stderr)
}

func runSnapshotsEnv(env *cliEnv, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl snapshots: expected a subcommand (list, restore <id>)")
		return 2
	}
	switch args[0] {
	case "list":
		return runSnapshotsList(args[1:], stdout, stderr)
	case "restore":
		return runSnapshotsRestore(env, args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "vnproxctl snapshots: unknown subcommand %q\n", args[0])
		return 2
	}
}

func runSnapshotsList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl snapshots list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to vnprox.toml (for storage.db_path)")
	limit := fs.Int("limit", 50, "maximum snapshots to list (newest first)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx := context.Background()
	db, err := openStore(ctx, *configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl snapshots list: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()

	rows, _, err := store.NewSnapshotRepo(db).ListPage(ctx, "", *limit)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl snapshots list: %v\n", err)
		return 1
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(stdout, "No snapshots.")
		return 0
	}

	_, _ = fmt.Fprintf(stdout, "%-26s  %-9s  %-19s  %-26s  %-20s  %s\n", "ID", "KIND", "TAKEN (UTC)", "CHANGESET", "NODES", "NOTE")
	for _, row := range rows {
		var files []snapshotFileEntry
		_ = json.Unmarshal([]byte(row.FilesJSON), &files)
		nodes := make([]string, 0, len(files))
		seen := map[string]bool{}
		for _, f := range files {
			if !seen[f.Node] {
				seen[f.Node] = true
				nodes = append(nodes, f.Node)
			}
		}
		_, _ = fmt.Fprintf(stdout, "%-26s  %-9s  %-19s  %-26s  %-20s  %s\n",
			row.ID, row.Kind,
			time.Unix(row.TakenAt, 0).UTC().Format("2006-01-02 15:04:05"),
			row.ChangesetID.String, strings.Join(nodes, ","), row.Note.String,
		)
	}
	return 0
}

func runSnapshotsRestore(env *cliEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl snapshots restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to vnprox.toml (for storage.db_path)")
	nodeFlag := fs.String("node", "", "which captured node's file to restore (default: this host's name)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl snapshots restore: expected exactly one snapshot id")
		return 2
	}
	snapshotID := fs.Arg(0)

	if env.geteuid() != 0 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl snapshots restore: must run as root (writes /etc/network/interfaces and runs ifreload)")
		return 1
	}

	ctx := context.Background()
	db, err := openStore(ctx, *configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl snapshots restore: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()

	row, err := store.NewSnapshotRepo(db).Get(ctx, snapshotID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl snapshots restore: %v\n", err)
		return 1
	}
	content, node, otherNodes, err := snapshotContentForNode(ctx, db, row, *nodeFlag, env.hostname)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl snapshots restore: %v\n", err)
		return 1
	}

	if err := restoreLocalInterfaces(ctx, env, content); err != nil {
		appendCLIAudit(ctx, db, env, "snapshot.restore.cli", "error", row.ChangesetID.String,
			map[string]any{"snapshotId": snapshotID, "node": node, "error": err.Error()})
		_, _ = fmt.Fprintf(stderr, "vnproxctl snapshots restore: %v\n", err)
		return 1
	}
	appendCLIAudit(ctx, db, env, "snapshot.restore.cli", "success", row.ChangesetID.String,
		map[string]any{"snapshotId": snapshotID, "node": node})

	_, _ = fmt.Fprintf(stdout, "Restored %s from snapshot %s (node %s) and reloaded the network.\n", env.interfacesPath, snapshotID, node)
	if len(otherNodes) > 0 {
		_, _ = fmt.Fprintf(stdout, "NOTE: this snapshot also captured node(s) %s — run `vnproxctl snapshots restore %s` on each of them too.\n",
			strings.Join(otherNodes, ", "), snapshotID)
	}
	return 0
}

// snapshotContentForNode picks the snapshot's file entry for the local node
// (or the --node override), hydrates its content from the blob store (or
// the legacy inline field), and reports which other nodes the snapshot also
// captured, so the operator knows the restore is per-node.
func snapshotContentForNode(ctx context.Context, db *store.DB, row store.Snapshot, nodeOverride string, hostname func() (string, error)) (content, node string, otherNodes []string, err error) {
	var files []snapshotFileEntry
	if decodeErr := json.Unmarshal([]byte(row.FilesJSON), &files); decodeErr != nil {
		return "", "", nil, fmt.Errorf("decoding snapshot %s files: %w", row.ID, decodeErr)
	}
	if len(files) == 0 {
		return "", "", nil, fmt.Errorf("snapshot %s captured no files", row.ID)
	}

	node = nodeOverride
	if node == "" {
		h, hostErr := hostname()
		if hostErr != nil {
			return "", "", nil, fmt.Errorf("determining local hostname (use --node): %w", hostErr)
		}
		node = h
	}

	var picked *snapshotFileEntry
	var captured []string
	for i, f := range files {
		captured = append(captured, f.Node)
		if f.Node == node {
			picked = &files[i]
		} else {
			otherNodes = append(otherNodes, f.Node)
		}
	}
	if picked == nil {
		return "", "", nil, fmt.Errorf("snapshot %s has no file for node %q (captured: %s); pass --node to pick one", row.ID, node, strings.Join(captured, ", "))
	}

	content = picked.Content // legacy inline shape
	if content == "" {
		content, err = store.NewBlobRepo(db).Get(ctx, picked.SHA256)
		if err != nil {
			return "", "", nil, fmt.Errorf("reading blob %s for node %s: %w", picked.SHA256, node, err)
		}
	}
	return content, node, otherNodes, nil
}

// restoreLocalInterfaces writes content over the local interfaces file and
// reloads, with a timestamped backup + restore-on-failure, mirroring
// cmd/vnproxd's hostNodeAgent.ReloadInterfaces sequence: back up the current
// file, write the new content, ifreload; on reload failure put the backup
// back and re-reload before returning the error, so a bad restore never
// leaves the node half-configured. The backup file is deliberately left on
// disk on success (an operator running a disaster recovery wants the
// previous state recoverable by hand too).
func restoreLocalInterfaces(ctx context.Context, env *cliEnv, content string) error {
	current, err := os.ReadFile(env.interfacesPath)
	if err != nil {
		return fmt.Errorf("reading %s for backup: %w", env.interfacesPath, err)
	}
	backupPath := fmt.Sprintf("%s.vnprox-backup-%s", env.interfacesPath, env.now().UTC().Format("20060102-150405"))
	if err := os.WriteFile(backupPath, current, 0o644); err != nil {
		return fmt.Errorf("writing backup %s: %w", backupPath, err)
	}
	if err := os.WriteFile(env.interfacesPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", env.interfacesPath, err)
	}
	if err := env.ifreload(ctx); err != nil {
		if restoreErr := os.WriteFile(env.interfacesPath, current, 0o644); restoreErr != nil {
			return fmt.Errorf("reloading network: %w (AND restoring the previous file failed: %v — recover manually from %s)", err, restoreErr, backupPath)
		}
		if reErr := env.ifreload(ctx); reErr != nil {
			return fmt.Errorf("reloading network: %w (previous file restored, but re-reload also failed: %v)", err, reErr)
		}
		return fmt.Errorf("reloading network (previous file restored and reloaded): %w", err)
	}
	return nil
}

// appendCLIAudit best-effort appends an audit row for a CLI disaster-path
// action, attributed to cliActor. Failures are ignored: the restore itself
// already happened (or already failed), and a broken audit write must not
// change the command's outcome mid-disaster.
func appendCLIAudit(ctx context.Context, db *store.DB, env *cliEnv, action, result, changesetID string, detail map[string]any) {
	var detailJSON sql.NullString
	if b, err := json.Marshal(detail); err == nil {
		detailJSON = sql.NullString{String: string(b), Valid: true}
	}
	_, _ = store.NewAuditRepo(db).Append(ctx, store.AuditEntry{
		At:          env.now().Unix(),
		Username:    cliActor,
		Action:      action,
		Result:      result,
		ChangesetID: sql.NullString{String: changesetID, Valid: changesetID != ""},
		DetailJSON:  detailJSON,
	})
}
