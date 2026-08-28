// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// ---------------------------------------------------------------- AC1

// TestBackupRestore_AC1_RoundTrip is T-1901 AC1: "Backup → wipe → restore
// round-trips a store with changesets, snapshots, audit rows, and sealed
// credentials; every table's row count and a sampled row's contents match."
//
// Two design choices make it non-vacuous:
//
//   - The table list is DISCOVERED from sqlite_master, not written down.
//     A new table added by a future migration is compared automatically,
//     and the test fails if the discovery finds implausibly few tables —
//     so it cannot quietly degrade into comparing nothing.
//   - "Wipe" means the store file and its sidecars are actually deleted,
//     and the test asserts the store is gone before restoring. A restore
//     that silently did nothing would then be caught by every subsequent
//     assertion rather than by none of them.
func TestBackupRestore_AC1_RoundTrip(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	before := tableRowCounts(t, f.dbPath)
	if len(before) < 25 {
		t.Fatalf("discovered only %d tables in a migrated store — the discovery query is broken, "+
			"and this test would compare almost nothing", len(before))
	}
	beforeSample := sampleRows(t, f.dbPath)

	outDir := filepath.Join(t.TempDir(), "backups")
	res, err := Create(ctx, Options{
		ConfigPath: f.configPath, DBPath: f.dbPath, OutDir: outDir,
		Node: seededNode, ToolVersion: "test", Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.Manifest.SchemaVersion == 0 {
		t.Fatal("manifest recorded schema version 0 for a migrated store")
	}

	// --- wipe -----------------------------------------------------------
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(f.dbPath + suffix)
	}
	if _, statErr := os.Stat(f.dbPath); statErr == nil {
		t.Fatal("the store still exists after the wipe — the round trip proves nothing")
	}

	// --- restore ---------------------------------------------------------
	plan, err := Restore(ctx, RestoreOptions{
		ArchivePath: res.Path, DBPath: f.dbPath, ConfigPath: f.configPath,
		KeyDir: f.keyDir, Listen: f.listen, Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !plan.Applied {
		t.Fatal("Restore reported Applied = false")
	}

	after := tableRowCounts(t, f.dbPath)
	for table, want := range before {
		got, ok := after[table]
		if !ok {
			t.Errorf("table %q is missing after the restore", table)
			continue
		}
		if got != want {
			t.Errorf("table %q: %d rows after restore, %d before", table, got, want)
		}
	}
	for table := range after {
		if _, ok := before[table]; !ok {
			t.Errorf("table %q appeared out of nowhere after the restore", table)
		}
	}

	afterSample := sampleRows(t, f.dbPath)
	for k, want := range beforeSample {
		if got := afterSample[k]; got != want {
			t.Errorf("sampled row %s: got %q, want %q", k, got, want)
		}
	}

	// --- and the sealed credentials still decrypt under the same key -----
	// This is the property that makes a restore useful rather than merely
	// complete: the ciphertext survived byte-for-byte, so the node's own
	// session key still opens it.
	db, err := store.Open(ctx, f.dbPath)
	if err != nil {
		t.Fatalf("opening the restored store: %v", err)
	}
	defer func() { _ = db.Close() }()
	cipher, err := store.NewSessionCipher(f.sessionKey(t))
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}
	sess, err := store.NewSessionRepo(db, cipher).Get(ctx, "sess-1")
	if err != nil {
		t.Fatalf("reading the restored session: %v", err)
	}
	if sess.PVETicket != secretMarkers["pve_ticket"] {
		t.Errorf("restored PVE ticket = %q, want the sealed original", sess.PVETicket)
	}
	sealed, expires, err := store.NewChangesetRepo(db).RevertTicket(ctx, "cs-1")
	if err != nil {
		t.Fatalf("reading the restored revert ticket: %v", err)
	}
	if expires != 1700007200 {
		t.Errorf("restored revert ticket expiry = %d, want 1700007200", expires)
	}
	plain, err := cipher.Decrypt(sealed)
	if err != nil {
		t.Fatalf("decrypting the restored revert ticket: %v", err)
	}
	if string(plain) != secretMarkers["revert_ticket"] {
		t.Errorf("restored revert ticket = %q, want the sealed original", plain)
	}

	// The previous store was kept, not destroyed.
	if _, err := os.Stat(plan.PreRestorePath); err == nil {
		t.Error("a pre-restore copy exists even though the store had been wiped — nothing should have been moved aside")
	}
}

func fixedNow() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) }

// tableRowCounts discovers every user table and counts its rows.
func tableRowCounts(t *testing.T, path string) map[string]int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating tables: %v", err)
	}
	_ = rows.Close()

	out := make(map[string]int, len(names))
	for _, n := range names {
		var c int
		// Table names come from sqlite_master, not from user input.
		if err := db.QueryRow(`SELECT count(*) FROM "` + n + `"`).Scan(&c); err != nil {
			t.Fatalf("counting %s: %v", n, err)
		}
		out[n] = c
	}
	return out
}

// sampleRows reads one representative row from each of the tables T-1901's
// objective names, at column granularity. Row counts alone would not catch
// a restore that shuffled values between rows.
func sampleRows(t *testing.T, path string) map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer func() { _ = db.Close() }()

	queries := map[string]string{
		"changeset":     `SELECT id||'|'||title||'|'||author||'|'||status||'|'||ops_json FROM changesets WHERE id = 'cs-1'`,
		"snapshotPre":   `SELECT id||'|'||kind||'|'||files_json||'|'||changeset_id FROM snapshots WHERE id = 'snap-pre-1'`,
		"snapshotPost":  `SELECT id||'|'||kind||'|'||files_json||'|'||changeset_id FROM snapshots WHERE id = 'snap-post-1'`,
		"blob":          `SELECT sha256||'|'||size FROM blobs`,
		"auditFirst":    `SELECT username||'|'||action||'|'||result||'|'||at FROM audit_log ORDER BY id LIMIT 1`,
		"auditLast":     `SELECT username||'|'||action||'|'||result||'|'||at FROM audit_log ORDER BY id DESC LIMIT 1`,
		"layout":        `SELECT username||'|'||name||'|'||layout_json FROM layouts`,
		"tenant":        `SELECT id||'|'||name||'|'||created_by FROM tenants`,
		"tenantScope":   `SELECT tenant_id||'|'||scope_ref FROM tenant_scopes`,
		"blueprint":     `SELECT id||'|'||name||'|'||blueprint_json FROM blueprints`,
		"cluster":       `SELECT id||'|'||name||'|'||api_url||'|'||hex(credential_enc) FROM clusters`,
		"wgTunnel":      `SELECT id||'|'||node||'|'||if_name||'|'||hex(private_key_enc) FROM wireguard_tunnels`,
		"session":       `SELECT id||'|'||username||'|'||hex(pve_ticket_enc) FROM sessions`,
		"revertTicket":  `SELECT hex(revert_ticket_enc)||'|'||revert_ticket_expires_at FROM changesets WHERE id = 'cs-1'`,
		"apiToken":      `SELECT id||'|'||token_hash FROM api_tokens`,
		"schedule":      `SELECT changeset_id||'|'||callback_token_hash||'|'||status FROM changeset_schedules`,
		"annotation":    `SELECT id||'|'||ref||'|'||content FROM annotations`,
		"schemaVersion": `SELECT v FROM kv WHERE k = 'schema_version'`,
	}
	out := make(map[string]string, len(queries))
	for name, q := range queries {
		var v string
		if err := db.QueryRow(q).Scan(&v); err != nil {
			t.Fatalf("sampling %s (%s): %v", name, q, err)
		}
		out[name] = v
	}
	return out
}

// ---------------------------------------------------------------- AC2

// TestBackup_AC2_NoKeyMaterialWithoutTheFlag is T-1901 AC2, table-driven
// with one case per secret class, scanning the whole archive rather than
// individual files.
//
// The scan is over the archive's *decompressed* bytes — manifest, store
// pages, config, everything — because compression would otherwise hide a
// plaintext from a naive grep of the .tar.gz. That is the difference
// between a real assertion and a comforting one.
//
// The control row is the whole reason to believe the rest: an
// unsealed operator note carrying a marker string MUST be found by the same
// scanner. If it is not, the scanner is broken and every "not present"
// result is meaningless.
func TestBackup_AC2_NoKeyMaterialWithoutTheFlag(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	res, err := Create(ctx, Options{
		ConfigPath: f.configPath, DBPath: f.dbPath, KeyPaths: f.keyPaths,
		OutDir: filepath.Join(t.TempDir(), "backups"),
		Node:   seededNode, ToolVersion: "test", Now: fixedNow,
		IncludeKeys: false,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	plain := decompressArchive(t, res.Path)

	// --- the control: the scanner works ---------------------------------
	if !bytes.Contains(plain, []byte(unsealedMarker)) {
		t.Fatalf("CONTROL FAILED: an unsealed value that IS in the store was not found by the archive scan. "+
			"The scan is broken, so every 'secret not found' assertion below proves nothing. "+
			"(archive %s, %d decompressed bytes)", res.Path, len(plain))
	}

	// --- one case per secret class ---------------------------------------
	for _, c := range SecretClasses() {
		t.Run(c.ID, func(t *testing.T) {
			marker := secretMarkers[c.ID]
			if marker == "" {
				// StorageExternal: never collected, nothing seeded. Assert
				// the archive holds no entry claiming to be one, so the
				// case is not simply skipped.
				for _, e := range res.Manifest.Entries {
					if e.Role == RoleKey {
						t.Fatalf("archive contains a key entry %q for a class that is never collected", e.Name)
					}
				}
				return
			}
			if bytes.Contains(plain, []byte(marker)) {
				t.Errorf("the %s (%s) appears IN THE CLEAR in a backup taken without --include-keys. %s",
					c.Name, c.ID, c.Detail)
			}
		})
	}

	// --- and the archive says so ------------------------------------------
	if res.Manifest.IncludesKeyMaterial {
		t.Error("manifest marks a default backup as containing key material")
	}
	if len(res.Manifest.SecretClasses) != 0 {
		t.Errorf("manifest declares secret classes %v for a default backup", res.Manifest.SecretClasses)
	}
	for _, e := range res.Manifest.Entries {
		if e.Role == RoleKey {
			t.Errorf("a default backup carries a key entry %q", e.Name)
		}
		if strings.HasPrefix(e.Name, keyPrefix) {
			t.Errorf("a default backup carries an entry under %s: %q", keyPrefix, e.Name)
		}
	}
	// The store IS in there — otherwise "no secrets found" would be the
	// trivial consequence of an empty archive.
	if _, ok := res.Manifest.Entry(RoleStore); !ok {
		t.Fatal("the archive has no store entry at all")
	}
}

// TestBackup_AC2_WithKeysContainsExactlyWhatItPromises is the other half of
// AC2: --include-keys must actually include the key material (otherwise the
// flag is a lie and the warning is theatre), the manifest must be marked,
// and the filename must say so.
func TestBackup_AC2_WithKeysContainsExactlyWhatItPromises(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	res, err := Create(ctx, Options{
		ConfigPath: f.configPath, DBPath: f.dbPath, KeyPaths: f.keyPaths,
		OutDir: filepath.Join(t.TempDir(), "backups"),
		Node:   seededNode, ToolVersion: "test", Now: fixedNow,
		IncludeKeys: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	plain := decompressArchive(t, res.Path)

	for _, c := range SecretClassesBy(StorageKeyFile) {
		marker := secretMarkers[c.ID]
		if !bytes.Contains(plain, []byte(marker)) {
			t.Errorf("--include-keys did not actually include the %s (%s)", c.Name, c.ID)
		}
	}
	// Sealed classes stay sealed even here: --include-keys ships the key,
	// not decrypted columns.
	for _, c := range SecretClassesBy(StorageSealedColumn) {
		if bytes.Contains(plain, []byte(secretMarkers[c.ID])) {
			t.Errorf("the %s appears in the clear inside the store copy — the column is not actually sealed", c.Name)
		}
	}

	if !res.Manifest.IncludesKeyMaterial {
		t.Error("a --include-keys archive is not marked as containing key material")
	}
	wantClasses := secretClassIDs(SecretClassesBy(StorageKeyFile))
	got := append([]string{}, res.Manifest.SecretClasses...)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(wantClasses, ",") {
		t.Errorf("manifest secretClasses = %v, want %v", got, wantClasses)
	}
	if !strings.HasSuffix(filepath.Base(res.Path), "-with-keys.tar.gz") {
		t.Errorf("a key-bearing archive is named %q — the marking must be visible in an `ls`", filepath.Base(res.Path))
	}
	if info, err := os.Stat(res.Path); err != nil {
		t.Fatalf("stat: %v", err)
	} else if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key-bearing archive mode = %04o, want 0600", perm)
	}
	// Extracted key files keep 0600.
	for _, e := range res.Manifest.EntriesWithRole(RoleKey) {
		if fs := os.FileMode(e.Mode).Perm(); fs != 0o600 {
			t.Errorf("key entry %s recorded mode %04o, want 0600", e.Name, fs)
		}
	}
}

// TestKeyWarning_NamesEveryClass: the warning is the mechanism by which
// "opt-in, loudly" is loud. A warning that says "includes keys" and stops
// is not actionable.
func TestKeyWarning_NamesEveryClass(t *testing.T) {
	w := KeyWarning()
	for _, c := range SecretClassesBy(StorageKeyFile) {
		if !strings.Contains(w, c.Name) {
			t.Errorf("--include-keys warning does not name the %s", c.Name)
		}
	}
	for _, c := range SecretClassesBy(StorageSealedColumn) {
		if !strings.Contains(w, c.Name) {
			t.Errorf("--include-keys warning does not name the %s, which the session key makes readable", c.Name)
		}
		if !strings.Contains(w, c.Column) {
			t.Errorf("--include-keys warning does not name the column %s", c.Column)
		}
	}
	// And it says what the safe default is, so an operator who reads it can
	// still choose the other thing.
	if !strings.Contains(w, "WITHOUT --include-keys") {
		t.Error("the warning never tells the operator what the safe alternative is")
	}
}

// decompressArchive returns every byte of every entry, concatenated with
// the manifest, so a scan cannot be defeated by compression or by looking
// in the wrong file.
func decompressArchive(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gunzip %s: %v", path, err)
	}
	defer func() { _ = gz.Close() }()

	var out bytes.Buffer
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		out.WriteString(hdr.Name)
		out.WriteByte('\n')
		if _, err := io.Copy(&out, tr); err != nil {
			t.Fatalf("reading entry %s: %v", hdr.Name, err)
		}
	}
	return out.Bytes()
}

// ---------------------------------------------------------------- retention

// TestPrune_KeepsTheNewestAndTouchesNothingElse. Prune deletes files, so
// the interesting assertions are the negative ones.
func TestPrune_KeepsTheNewestAndTouchesNothingElse(t *testing.T) {
	dir := t.TempDir()
	var archives []string
	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("vnprox-backup-pve1-2026010%d-000000.tar.gz", i)
		archives = append(archives, name)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("seeding %s: %v", name, err)
		}
	}
	// Bystanders that must survive: another tool's backup, a note, a
	// key-bearing vnprox archive from a different node, and a directory.
	bystanders := []string{"pbs-backup-20260101.tar.gz", "notes.txt", "vnprox-backup-pve1.tar.gz"}
	for _, b := range bystanders {
		if err := os.WriteFile(filepath.Join(dir, b), []byte("y"), 0o600); err != nil {
			t.Fatalf("seeding %s: %v", b, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "vnprox-backup-pve1-20260109-000000.tar.gz.d"), 0o700); err != nil {
		t.Fatalf("seeding directory: %v", err)
	}

	removed, err := Prune(dir, 2, filepath.Join(dir, archives[4]))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(removed) != 3 {
		t.Errorf("Prune removed %d archives, want 3: %v", len(removed), removed)
	}
	for _, keep := range []string{archives[3], archives[4]} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("Prune removed %s, which is among the newest 2", keep)
		}
	}
	for _, gone := range archives[:3] {
		if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
			t.Errorf("Prune kept %s, which is outside the retention window", gone)
		}
	}
	for _, b := range bystanders {
		if _, err := os.Stat(filepath.Join(dir, b)); err != nil {
			t.Errorf("Prune deleted %s, which it does not own", b)
		}
	}

	// keep >= count is a no-op, and keep = 0 disables retention entirely.
	if got, _ := Prune(dir, 10, ""); len(got) != 0 {
		t.Errorf("Prune with keep > count removed %v", got)
	}
	if got, _ := Prune(dir, 0, ""); len(got) != 0 {
		t.Errorf("Prune with keep = 0 removed %v — retention must be opt-in", got)
	}
}

// TestCreate_KeepPrunesButNeverTheArchiveItJustWrote is the failure mode
// that would make automated retention destroy the thing it was asked to
// keep.
func TestCreate_KeepPrunesButNeverTheArchiveItJustWrote(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	outDir := filepath.Join(t.TempDir(), "backups")

	var last string
	for i := 0; i < 4; i++ {
		at := time.Date(2026, 3, 4, 5, 6, i, 0, time.UTC)
		res, err := Create(ctx, Options{
			ConfigPath: f.configPath, DBPath: f.dbPath, OutDir: outDir,
			Node: seededNode, ToolVersion: "test", Keep: 2,
			Now: func() time.Time { return at },
		})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		last = res.Path
		if _, err := os.Stat(res.Path); err != nil {
			t.Fatalf("Create %d pruned the archive it had just written: %v", i, err)
		}
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var found []string
	for _, e := range entries {
		found = append(found, e.Name())
	}
	if len(found) != 2 {
		t.Errorf("after four backups with --keep 2 the directory holds %v, want 2 archives", found)
	}
	if _, err := os.Stat(last); err != nil {
		t.Errorf("the newest archive is gone: %v", err)
	}
}

// TestCreate_NoStagingResidue: the staging directory holds an unencrypted
// copy of the store (and, with --include-keys, the session key). It must
// not survive the command, on either the success or the failure path.
func TestCreate_NoStagingResidue(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	outDir := filepath.Join(t.TempDir(), "backups")

	if _, err := Create(ctx, Options{
		ConfigPath: f.configPath, DBPath: f.dbPath, KeyPaths: f.keyPaths, OutDir: outDir,
		Node: seededNode, ToolVersion: "test", Now: fixedNow, IncludeKeys: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertOnlyArchives(t, outDir)

	// Failure path: a config file that does not exist makes the config
	// collector fail after the store has already been staged.
	if _, err := Create(ctx, Options{
		ConfigPath: filepath.Join(f.dir, "no-such-config.toml"),
		DBPath:     f.dbPath, OutDir: outDir,
		Node: seededNode, ToolVersion: "test",
		Now: func() time.Time { return fixedNow().Add(time.Minute) },
	}); err == nil {
		t.Fatal("Create succeeded with a missing config")
	}
	assertOnlyArchives(t, outDir)
}

func assertOnlyArchives(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("staging directory %q survived in %s — it holds an unencrypted store copy", e.Name(), dir)
		}
		if strings.HasSuffix(e.Name(), ".partial") {
			t.Errorf("a partial archive %q survived in %s", e.Name(), dir)
		}
	}
}
