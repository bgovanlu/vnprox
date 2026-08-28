// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/store"
)

// bundlescenario_test.go is T-1902 AC3: "The bundle is sufficient to
// diagnose a scripted broken install (wrong PVE credential, port conflict,
// failed migration) without shell access — proven by three fixture
// scenarios."
//
// Two rules the three scenarios below follow, and they are what separates
// this from a test that greps for the word "error":
//
//  1. **Every assertion reads the ARCHIVE**, never the fixture. "Without
//     shell access" is the requirement, so the test is only allowed to know
//     what someone holding the .tar.gz would know.
//  2. **Every scenario has a healthy control.** A diagnosis that is also
//     present in a working install is not a diagnosis. Each case therefore
//     asserts the evidence appears in the broken bundle AND is absent from
//     the healthy one, which is the assertion that actually has content.

// ---------------------------------------------------- scenario helpers

// diagnosis is what a reader can extract from a bundle without a shell.
// Built by parsing the archive's own entries, so it cannot see anything the
// bundle does not contain.
//
//nolint:govet // fieldalignment: a test helper struct.
type diagnosis struct {
	Store   BundleStore
	Probes  BundleProbes
	Logs    BundleLogs
	LogText string
	Config  BundleConfig
}

func diagnose(t *testing.T, archivePath string) diagnosis {
	t.Helper()
	var d diagnosis
	decode := func(entry string, into any) {
		t.Helper()
		if err := json.Unmarshal(entryFromArchive(t, archivePath, entry), into); err != nil {
			t.Fatalf("decoding %s from %s: %v", entry, archivePath, err)
		}
	}
	decode(entryBundleStore, &d.Store)
	decode(entryBundleProbes, &d.Probes)
	decode(entryBundleLogSummary, &d.Logs)
	decode(entryBundleConfig, &d.Config)
	d.LogText = string(entryFromArchive(t, archivePath, entryBundleLog))
	return d
}

func (d diagnosis) keyFile(t *testing.T, classID string) KeyFileProbe {
	t.Helper()
	for _, k := range d.Probes.KeyFiles {
		if k.ClassID == classID {
			return k
		}
	}
	t.Fatalf("the bundle's probes.json has no entry for key class %q; it is driven by "+
		"SecretClassesBy(StorageKeyFile), so this means the inventory and the probe have diverged", classID)
	return KeyFileProbe{}
}

// bundleFor produces a real bundle from opts and returns what a reader
// could diagnose from it.
func bundleFor(t *testing.T, opts BundleOptions) diagnosis {
	t.Helper()
	opts.OutDir = filepath.Join(t.TempDir(), "bundles")
	res, err := Bundle(context.Background(), opts)
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	return diagnose(t, res.Path)
}

// ------------------------------------------------------- scenario one

// TestBundle_AC3_Scenario1_WrongPVECredential.
//
// The fault: vnproxd is running and cannot authenticate to Proxmox. What
// the reader must be able to conclude, holding only the archive: that the
// PVE credential is being REJECTED (not missing, not unreachable), and
// which file the credential comes from — the two facts that separate "roll
// the token" from "the token file is not where the config says".
//
// The second subtest is the other half of that distinction, and it is
// computed rather than transported: the token file is deleted, and the
// bundle's key-file probe says so without any log line mentioning it.
func TestBundle_AC3_Scenario1_WrongPVECredential(t *testing.T) {
	const authFailure = `level=ERROR msg="pve poll failed" source=pve status=401 body="authentication failure"`

	t.Run("the credential is rejected", func(t *testing.T) {
		bf := newBundleFixture(t)
		appendLog(t, bf.logPath, authFailure)
		broken := bundleFor(t, bf.options(t, ""))

		if !strings.Contains(broken.LogText, "status=401") {
			t.Errorf("the bundle's log does not carry the 401; the reader cannot tell a rejected "+
				"credential from an unreachable cluster:\n%s", broken.LogText)
		}
		if !strings.Contains(broken.LogText, "authentication failure") {
			t.Error("the bundle's log does not carry the reason PVE gave")
		}
		// ...and the credential's FILE is present and readable, which is
		// what says "rotate the token" rather than "fix the path".
		tok := broken.keyFile(t, "pve_api_token")
		if !tok.File.Exists {
			t.Errorf("the bundle reports the PVE token file as absent, but it is there: %+v", tok.File)
		}
		if tok.File.Mode != "0600" {
			t.Errorf("the bundle reports the PVE token file's mode as %q, want 0600", tok.File.Mode)
		}
		// ...and the config says where the daemon was pointed.
		if !configValue(t, broken.Config, "pve.api_url", "https://127.0.0.1:8006/api2/json") {
			t.Error("the bundle does not report [pve] api_url, so the reader cannot tell which cluster failed")
		}
		if !configHasKey(broken.Config, "pve.token_file") {
			t.Error("the bundle does not report [pve] token_file")
		}
		// And redaction demonstrably ran over the same log.
		if broken.Logs.Scrubbed == 0 {
			t.Error("logs/summary.json reports zero scrubbed lines, but the fixture log carries a PVE " +
				"ticket and an API token — either the log was not scrubbed or it was not collected")
		}

		// The control: a healthy install's bundle carries none of this.
		healthy := bundleFor(t, newBundleFixture(t).options(t, ""))
		if strings.Contains(healthy.LogText, "status=401") {
			t.Fatal("CONTROL FAILED: a healthy install's bundle also reports a 401, so finding one " +
				"in the broken bundle diagnoses nothing")
		}
		if !healthy.keyFile(t, "pve_api_token").File.Exists {
			t.Fatal("CONTROL FAILED: the healthy fixture has no PVE token file either")
		}
	})

	t.Run("the credential file is missing", func(t *testing.T) {
		bf := newBundleFixture(t)
		tokenPath := filepath.Join(bf.keyDir, "pve-token")
		if err := os.Remove(tokenPath); err != nil {
			t.Fatalf("removing the token file: %v", err)
		}
		d := bundleFor(t, bf.options(t, ""))

		tok := d.keyFile(t, "pve_api_token")
		if tok.File.Exists {
			t.Errorf("the bundle reports the deleted PVE token file as present: %+v", tok.File)
		}
		if tok.Name == "" {
			t.Error("the probe does not name the class in operator-facing terms")
		}
		// Every OTHER key file is still reported present, so "missing" is a
		// fact about this file rather than about the probe being broken.
		if !d.keyFile(t, "session_key").File.Exists {
			t.Fatal("CONTROL FAILED: the probe reports the session key as missing too, so it is " +
				"reporting everything as missing")
		}
	})
}

// ------------------------------------------------------- scenario two

// TestBundle_AC3_Scenario2_PortConflict.
//
// The fault: something else already owns vnprox's port (on a real node,
// almost always Proxmox Backup Server on :8007 — the collision
// docs/deployment.md already documents). vnproxd restart-loops and the
// operator sees "it doesn't work".
//
// This one is entirely COMPUTED. The bundle does not need a log line to
// have survived, because the probe binds the configured address itself and
// reports the answer. That matters: a crash-looping daemon's journal is
// mostly noise, and on a fresh install there may be no journal at all.
//
// Machine-sharing note: the conflicting listener binds 127.0.0.1:0 and the
// fixture config is rewritten with whatever port the kernel assigned. No
// fixed port is claimed anywhere in this file.
func TestBundle_AC3_Scenario2_PortConflict(t *testing.T) {
	bf := newBundleFixture(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding a conflicting listener: %v", err)
	}
	addr := ln.Addr().String()

	opts := bf.options(t, "")
	opts.Listen = addr

	// --- the control, FIRST: with nothing on the port, the probe says free.
	// Running it first means a probe that always answered "in-use" fails
	// here rather than passing the interesting assertion below.
	freeOpts := opts
	freeOpts.Listen = freeAddress(t)
	if free := bundleFor(t, freeOpts); free.Probes.Listen.State != "free" {
		t.Fatalf("CONTROL FAILED: an unbound address probes as %q, not \"free\" (%s). "+
			"The in-use assertion below would prove nothing.",
			free.Probes.Listen.State, free.Probes.Listen.Error)
	}

	broken := bundleFor(t, opts)
	if broken.Probes.Listen.State != "in-use" {
		t.Errorf("the bundle reports the conflicting listen address as %q, want \"in-use\" — "+
			"the reader cannot diagnose a port conflict without shell access", broken.Probes.Listen.State)
	}
	if broken.Probes.Listen.Address != addr {
		t.Errorf("the bundle reports the listen address as %q, want %q", broken.Probes.Listen.Address, addr)
	}
	if broken.Probes.Listen.Error == "" {
		t.Error("the bundle does not carry the bind error, which is what names the conflict")
	}
	// The config half: the reader must be able to see what the daemon was
	// configured to bind, independently of the probe.
	if !configHasKey(broken.Config, "server.listen") {
		t.Error("the bundle does not report [server] listen")
	}
	// And the daemon is not answering on it, which is how the reader tells
	// "someone else has the port" from "vnprox has the port and is fine".
	if broken.Probes.Daemon.Reachable {
		t.Error("the bundle reports the daemon as reachable on a port owned by something else")
	}

	if closeErr := ln.Close(); closeErr != nil {
		t.Fatalf("closing the conflicting listener: %v", closeErr)
	}
	// Same address, nothing on it now: the probe must flip. This is what
	// proves the verdict tracks reality rather than the config.
	after := bundleFor(t, opts)
	if after.Probes.Listen.State != "free" {
		t.Errorf("after the conflict was removed the probe still reports %q", after.Probes.Listen.State)
	}
}

// ----------------------------------------------------- scenario three

// TestBundle_AC3_Scenario3_FailedMigration.
//
// The fault: the store's schema version and the binary's do not agree, so
// vnproxd refuses to start. This is the situation where a naive diagnostic
// tool does the most damage — reading the store with the ordinary opener
// would MIGRATE it, changing the very thing being diagnosed. The bundle
// reads through store.InspectReadOnly (query_only(1)), so it cannot.
//
// Both directions are covered because they need opposite remedies: a store
// from a NEWER build means "you downgraded, put the newer binary back",
// while a store behind the binary means "the migration has not run yet".
func TestBundle_AC3_Scenario3_FailedMigration(t *testing.T) {
	latest, err := store.LatestSchemaVersion()
	if err != nil {
		t.Fatalf("LatestSchemaVersion: %v", err)
	}

	// --- the control, first: a healthy store reports "up to date". -------
	healthyFixture := newBundleFixture(t)
	healthy := bundleFor(t, healthyFixture.options(t, ""))
	if !strings.Contains(healthy.Store.MigrationState, "up to date") {
		t.Fatalf("CONTROL FAILED: a healthy store's migration state is %q, not \"up to date\". "+
			"Every assertion below would prove nothing.", healthy.Store.MigrationState)
	}
	if healthy.Store.SchemaVersion != latest {
		t.Fatalf("CONTROL FAILED: a healthy store reports schema %d, this build is at %d",
			healthy.Store.SchemaVersion, latest)
	}
	if healthy.Store.IntegrityCheck != "ok" {
		t.Errorf("a healthy store's integrity_check is %q, want \"ok\"", healthy.Store.IntegrityCheck)
	}
	if len(healthy.Store.Tables) < 25 {
		t.Errorf("the bundle reports only %d tables; the row counts are what tell a reader whether "+
			"the store has data in it at all", len(healthy.Store.Tables))
	}

	cases := []struct {
		name        string
		wantState   string
		wantMention []string
		version     int
	}{
		{
			name:        "the store is from a newer build (a downgrade)",
			version:     latest + 5,
			wantState:   "NEWER than this build",
			wantMention: []string{"refuse to start"},
		},
		{
			name:      "the store is behind the binary (a migration that has not run)",
			version:   latest - 3,
			wantState: "forward migration pending",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bf := newBundleFixture(t)
			setSchemaVersion(t, bf.dbPath, tc.version)
			before := storeFingerprint(t, bf.dbPath)

			d := bundleFor(t, bf.options(t, ""))

			if !strings.Contains(d.Store.MigrationState, tc.wantState) {
				t.Errorf("migration state = %q, want it to mention %q", d.Store.MigrationState, tc.wantState)
			}
			for _, m := range tc.wantMention {
				if !strings.Contains(d.Store.MigrationState, m) {
					t.Errorf("migration state = %q, want it to mention %q", d.Store.MigrationState, m)
				}
			}
			if d.Store.SchemaVersion != tc.version {
				t.Errorf("the bundle reports schema %d, want %d", d.Store.SchemaVersion, tc.version)
			}
			if d.Store.BinarySchemaVersion != latest {
				t.Errorf("the bundle reports the binary's schema as %d, want %d",
					d.Store.BinarySchemaVersion, latest)
			}
			// The one that matters most: producing the bundle did not
			// migrate, repair or otherwise touch the store it was asked to
			// diagnose.
			if after := storeFingerprint(t, bf.dbPath); after != before {
				t.Error("producing a support bundle CHANGED the store — a diagnostic tool must not " +
					"mutate the thing it is diagnosing (store.InspectReadOnly uses query_only(1))")
			}
		})
	}
}

// TestBundle_AC3_ReadingABundleNeverNeedsTheHost is the umbrella assertion
// for AC3's "without shell access": every entry a reader needs is inside
// the archive, and the archive is self-describing.
func TestBundle_AC3_ReadingABundleNeverNeedsTheHost(t *testing.T) {
	bf := newBundleFixture(t)
	res, err := Bundle(context.Background(), bf.options(t, filepath.Join(t.TempDir(), "bundles")))
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	// Everything a diagnosis needs decodes from the archive alone.
	d := diagnose(t, res.Path)
	if d.Store.Path == "" || d.Config.Path == "" {
		t.Error("the bundle does not record the paths it described")
	}
	if len(d.Probes.KeyFiles) != len(SecretClassesBy(StorageKeyFile)) {
		t.Errorf("the bundle probes %d key files but the inventory declares %d key-file classes",
			len(d.Probes.KeyFiles), len(SecretClassesBy(StorageKeyFile)))
	}
	// And the archive validates on its own terms, which is what a reader on
	// a different machine will do to it.
	f, err := os.Open(res.Path)
	if err != nil {
		t.Fatalf("opening the bundle: %v", err)
	}
	defer func() { _ = f.Close() }()
	m, err := Inspect(f, DefaultLimits())
	if err != nil {
		t.Fatalf("the bundle does not validate: %v", err)
	}
	if m.Kind != KindSupportBundle {
		t.Errorf("the bundle validates as kind %q", m.Kind)
	}
}

// ------------------------------------------------------------- helpers

func appendLog(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("appending to %s: %v", path, err)
	}
}

// setSchemaVersion rewrites kv.schema_version, simulating a store written
// by a different build. Writable on purpose: this is the fixture, not the
// code under test.
func setSchemaVersion(t *testing.T, dbPath string, version int) {
	t.Helper()
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("opening the fixture store: %v", err)
	}
	if _, execErr := db.Conn().ExecContext(context.Background(),
		`UPDATE kv SET v = ? WHERE k = 'schema_version'`, version); execErr != nil {
		_ = db.Close()
		t.Fatalf("setting schema version: %v", execErr)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("closing the fixture store: %v", closeErr)
	}
	got, err := store.InspectSchemaVersion(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("re-reading the schema version: %v", err)
	}
	if got != version {
		t.Fatalf("the fixture did not take: schema version is %d, wanted %d", got, version)
	}
}

// freeAddress returns a 127.0.0.1 address that is not bound, by binding one
// and letting it go. No fixed port is claimed.
func freeAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("finding a free address: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("releasing %s: %v", addr, err)
	}
	return addr
}

func configHasKey(c BundleConfig, key string) bool {
	for _, k := range c.Keys {
		if k.Key == key {
			return true
		}
	}
	return false
}

func configValue(t *testing.T, c BundleConfig, key, want string) bool {
	t.Helper()
	for _, k := range c.Keys {
		if k.Key != key {
			continue
		}
		if k.Redacted {
			t.Errorf("config key %q was redacted; it is on the allowlist and should have survived", key)
			return false
		}
		return k.Value == want
	}
	return false
}
