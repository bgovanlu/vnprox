package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/bgovanlu/vnprox/internal/store"
)

// runRollbackNow implements `vnproxctl rollback-now <changeset-id>`
// (docs/deployment.md "Troubleshooting quick refs": "CLI escape hatch to
// trigger rollback when the UI is unreachable"). The mechanism is the same
// daemon-independent path as `snapshots restore` (direct DB read + local
// file write + `ifreload -a` exec — see openStore's doc comment; no HTTP
// API anywhere): it loads the changeset's own pre-apply snapshot, restores
// the local node's interfaces file from it, and marks the changeset
// terminal in the DB so a later daemon restart doesn't re-arm a rollback
// timer for it.
//
// Eligible statuses are the in-flight ones — `awaiting_confirm` (the "I
// confirmed a bad change... no wait, I didn't confirm, the UI is just gone
// and the timer's daemon is dead" case) and `applying`/its stuck remains —
// which move to rolled_back. A *committed* changeset is refused with a
// pointer at `snapshots restore`: its rollback is a reviewed restoring
// draft (docs/features/change-management.md §4), which requires the
// daemon; the CLI equivalent that "applies locally with ifreload,
// bypassing confirm" is exactly the snapshots restore command
// (docs/deployment.md words it that way for this reason).
func runRollbackNow(args []string, stdout, stderr io.Writer) int {
	return runRollbackNowEnv(newCLIEnv(), args, stdout, stderr)
}

func runRollbackNowEnv(env *cliEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl rollback-now", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to vnprox.toml (for storage.db_path)")
	nodeFlag := fs.String("node", "", "which captured node's file to restore (default: this host's name)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl rollback-now: expected exactly one changeset id")
		return 2
	}
	changesetID := fs.Arg(0)

	if env.geteuid() != 0 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl rollback-now: must run as root (writes /etc/network/interfaces and runs ifreload)")
		return 1
	}

	ctx := context.Background()
	db, err := openStore(ctx, *configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl rollback-now: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()

	changesets := store.NewChangesetRepo(db)
	cs, err := changesets.Get(ctx, changesetID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl rollback-now: %v\n", err)
		return 1
	}
	switch cs.Status {
	case "awaiting_confirm", "applying":
		// The disaster cases this command exists for.
	case "committed":
		_, _ = fmt.Fprintf(stderr, "vnproxctl rollback-now: changeset %s is already committed; use `vnproxctl snapshots list` and `vnproxctl snapshots restore <id>` to restore its pre-apply snapshot instead\n", changesetID)
		return 1
	default:
		_, _ = fmt.Fprintf(stderr, "vnproxctl rollback-now: changeset %s is in status %q; nothing to roll back\n", changesetID, cs.Status)
		return 1
	}

	pre, err := preSnapshotFor(ctx, db, changesetID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl rollback-now: %v\n", err)
		return 1
	}
	content, node, otherNodes, err := snapshotContentForNode(ctx, db, pre, *nodeFlag, env.hostname)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl rollback-now: %v\n", err)
		return 1
	}

	if err := restoreLocalInterfaces(ctx, env, content); err != nil {
		appendCLIAudit(ctx, db, env, "changeset.rollback.cli", "error", changesetID,
			map[string]any{"snapshotId": pre.ID, "node": node, "error": err.Error()})
		_, _ = fmt.Fprintf(stderr, "vnproxctl rollback-now: %v\n", err)
		return 1
	}

	// Mark the changeset terminal so a restarted daemon's
	// ArmPendingRollbacks doesn't re-arm a timer (awaiting_confirm) or run
	// interrupted-apply recovery (applying) over a node we just restored.
	// awaiting_confirm -> rolled_back and applying -> failed are exactly the
	// transitions the state machine allows from those states
	// (internal/change/changeset.go); this direct row update is the CLI's
	// one deliberate bypass of the service layer, unavoidable daemon-down.
	target := "rolled_back"
	if cs.Status == "applying" {
		target = "failed"
	}
	cs.Status = target
	cs.ConfirmDeadline = sql.NullInt64{}
	cs.UpdatedAt = env.now().Unix()
	if err := changesets.Update(ctx, cs); err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl rollback-now: files restored, but marking changeset %s %s failed: %v\n", changesetID, target, err)
		return 1
	}
	appendCLIAudit(ctx, db, env, "changeset.rollback.cli", target, changesetID,
		map[string]any{"snapshotId": pre.ID, "node": node})

	_, _ = fmt.Fprintf(stdout, "Rolled back changeset %s: restored %s from its pre-apply snapshot %s (node %s), reloaded the network, and marked it %s.\n",
		changesetID, env.interfacesPath, pre.ID, node, target)
	if len(otherNodes) > 0 {
		_, _ = fmt.Fprintf(stdout, "NOTE: the pre-apply snapshot also captured node(s) %s — run `vnproxctl snapshots restore %s` on each of them too.\n",
			strings.Join(otherNodes, ", "), pre.ID)
	}
	return 0
}

// preSnapshotFor finds the changeset's "pre" snapshot row (the byte-exact
// pre-apply state, captured before any mutation).
func preSnapshotFor(ctx context.Context, db *store.DB, changesetID string) (store.Snapshot, error) {
	rows, err := store.NewSnapshotRepo(db).List(ctx, changesetID)
	if err != nil {
		return store.Snapshot{}, fmt.Errorf("listing snapshots for changeset %s: %w", changesetID, err)
	}
	for _, row := range rows {
		if row.Kind == "pre" {
			return row, nil
		}
	}
	return store.Snapshot{}, fmt.Errorf("no pre-apply snapshot found for changeset %s", changesetID)
}
