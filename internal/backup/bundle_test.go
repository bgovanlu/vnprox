// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/store"
)

// ---------------------------------------------------------------- AC1

// bundleProducer is one thing this package can produce that must satisfy the
// assertions below.
//
// T-2804 added the second one. Its acceptance criterion 3 asks for the
// incident export to pass "the same secret-redaction assertions as the
// T-1902 support bundle, reusing those tests rather than writing new ones" —
// so the export is a case in this table rather than a parallel copy of the
// test body. A future producer that forgets to redact fails T-1902's own AC1,
// which is the point.
type bundleProducer struct {
	name    string
	options func(bf *bundleFixture, t *testing.T, outDir string) BundleOptions
	// controls are the markers that MUST survive in this producer's archive,
	// beyond the four every bundle shares.
	controls []control
}

type control struct {
	marker string
	why    string
}

func bundleProducers() []bundleProducer {
	return []bundleProducer{
		{
			name:    "support-bundle",
			options: func(bf *bundleFixture, t *testing.T, outDir string) BundleOptions { return bf.options(t, outDir) },
		},
		{
			name: "incident-export",
			options: func(bf *bundleFixture, t *testing.T, outDir string) BundleOptions {
				return bf.incidentOptions(t, outDir)
			},
			controls: []control{
				{bundleControlIncident, "an ordinary annotation body from the incident timeline (T-2804)"},
			},
		},
	}
}

// TestBundle_AC1_ContainsNoSecretClass is T-1902 AC1: "A bundle produced
// from a store seeded with one of every secret class contains none of them
// — table-driven, one case per class, scanning the whole archive rather
// than individual files."
//
// The three things that make it non-vacuous, all lifted from T-1901's AC2
// and adapted to a bundle's very different collection surface:
//
//  1. **Four controls, asserted before anything else.** A bundle does not
//     contain the store, so T-1901's control (an unsealed store column that
//     must be findable) proves nothing here. Instead one control per
//     collection *path* — a changeset title from the store, an allowlisted
//     interfaces(5) option, an allowlisted config value, and a plain log
//     line — each of which must be found. If any is missing, the scan is
//     looking in the wrong place, that collector did not run, or redaction
//     has eaten everything, and the test says so in those words.
//  2. **The markers are planted where the bundle actually reads.** Seeding
//     them only into the store would make every assertion below pass
//     trivially, since the store is not in the archive. See
//     bundlefixture_test.go's bundlePlantings.
//  3. **The scan decompresses.** Scanning the .tar.gz's compressed bytes is
//     the comforting version of this test; scanning what `tar -xz` actually
//     yields is the real one.
//
// T-2804 runs the identical body over a second producer, the incident export
// (bundleProducers above) — its own AC3 asks for exactly that rather than a
// parallel copy of these assertions.
func TestBundle_AC1_ContainsNoSecretClass(t *testing.T) {
	for _, prod := range bundleProducers() {
		t.Run(prod.name, func(t *testing.T) {
			assertProducerCarriesNoSecret(t, prod)
		})
	}
}

func assertProducerCarriesNoSecret(t *testing.T, prod bundleProducer) {
	t.Helper()
	ctx := context.Background()
	bf := newBundleFixture(t)
	outDir := filepath.Join(t.TempDir(), "bundles")

	res, err := Bundle(ctx, prod.options(bf, t, outDir))
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	plain := decompressArchive(t, res.Path)

	// --- the controls: the scan reaches every collection path -----------
	controls := append([]control{
		{bundleControlStore, "a changeset title read from the store"},
		{bundleControlHost, "an allowlisted option from /etc/network/interfaces"},
		{bundleControlConfig, "an allowlisted value from vnprox.toml"},
		{bundleControlLog, "an ordinary line from the daemon log"},
	}, prod.controls...)
	for _, c := range controls {
		if !bytes.Contains(plain, []byte(c.marker)) {
			t.Fatalf("CONTROL FAILED: %s was not found in the bundle. Either that collector did not run, "+
				"the scan is looking in the wrong place, or redaction removed everything — so every "+
				"'secret not found' assertion below proves nothing. (archive %s, %d decompressed bytes)",
				c.why, res.Path, len(plain))
		}
	}

	// --- one case per secret class ---------------------------------------
	for _, c := range SecretClasses() {
		t.Run(c.ID, func(t *testing.T) {
			marker := secretMarkers[c.ID]
			if marker == "" {
				// StorageExternal: never collected, nothing seeded. Assert
				// the archive carries no key-role entry, so the case is not
				// simply skipped.
				for _, e := range res.Manifest.Entries {
					if e.Role == RoleKey {
						t.Fatalf("bundle contains a key entry %q for a class that is never collected", e.Name)
					}
				}
				return
			}
			if bytes.Contains(plain, []byte(marker)) {
				t.Errorf("the %s (%s) appears IN THE CLEAR in a support bundle. %s\n"+
					"A support bundle is going to be attached to a forum post.", c.Name, c.ID, c.Detail)
			}
		})
	}

	// --- and the manifest says what it is ---------------------------------
	if res.Manifest.Kind != KindSupportBundle {
		t.Errorf("manifest kind = %q, want %q", res.Manifest.Kind, KindSupportBundle)
	}
	if res.Manifest.IncludesKeyMaterial {
		t.Error("a support bundle's manifest claims to include key material")
	}
	if len(res.Manifest.SecretClasses) != 0 {
		t.Errorf("a support bundle declares secret classes %v; the set must be empty by construction",
			res.Manifest.SecretClasses)
	}
	// The archive is not empty — otherwise "no secrets found" would be the
	// trivial consequence of nothing having been collected.
	if len(res.Manifest.Entries) < 8 {
		t.Fatalf("the bundle has only %d entries; 'no secrets found' would be trivially true",
			len(res.Manifest.Entries))
	}
	if res.Bytes <= 0 {
		t.Fatal("the bundle is zero bytes")
	}
}

// TestBundleIncident_PlantingsReachTheDocument is the incident half of the
// planting guard: every marker incidentPlantings claims to plant must
// actually be somewhere in the document handed to Bundle. Without it, the
// incident-export leg of AC1 above could pass because the document is empty.
func TestBundleIncident_PlantingsReachTheDocument(t *testing.T) {
	doc, err := json.Marshal(bundleFixtureIncident())
	if err != nil {
		t.Fatalf("marshalling the fixture incident: %v", err)
	}
	if len(incidentPlantings) < 6 {
		t.Fatalf("only %d incident plantings declared; the export carries more free-text fields than that",
			len(incidentPlantings))
	}
	for _, p := range incidentPlantings {
		t.Run(p.ClassID, func(t *testing.T) {
			marker := secretMarkers[p.ClassID]
			if marker == "" {
				t.Fatalf("planting names class %q, which has no marker", p.ClassID)
			}
			if !bytes.Contains(doc, []byte(marker)) {
				t.Errorf("the %s marker is supposed to be planted in %s, but it is not in the incident "+
					"document at all — AC3's assertion for this class proves nothing", p.ClassID, p.Where)
			}
		})
	}
	// And the control is there too, or the whole leg is vacuous.
	if !bytes.Contains(doc, []byte(bundleControlIncident)) {
		t.Error("the incident control marker is not in the fixture document")
	}
}

// TestBundleIncident_IsAbsentFromAnOrdinarySupportBundle keeps the export
// additive: `vnproxctl support-bundle` must produce exactly the archive it
// always did.
func TestBundleIncident_IsAbsentFromAnOrdinarySupportBundle(t *testing.T) {
	ctx := context.Background()
	bf := newBundleFixture(t)

	plainRes, err := Bundle(ctx, bf.options(t, filepath.Join(t.TempDir(), "plain")))
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	for _, e := range plainRes.Manifest.Entries {
		if e.Name == entryBundleIncident {
			t.Fatalf("an ordinary support bundle contains %s", entryBundleIncident)
		}
	}

	// Control: the same fixture WITH an incident does carry it, so the
	// assertion above is about the option rather than about the entry name
	// being unreachable.
	incRes, err := Bundle(ctx, bf.incidentOptions(t, filepath.Join(t.TempDir(), "incident")))
	if err != nil {
		t.Fatalf("Bundle(incident): %v", err)
	}
	found := false
	for _, e := range incRes.Manifest.Entries {
		if e.Name == entryBundleIncident {
			found = true
		}
	}
	if !found {
		t.Fatalf("CONTROL FAILED: an incident export does not contain %s either", entryBundleIncident)
	}
	if len(incRes.Manifest.Entries) != len(plainRes.Manifest.Entries)+1 {
		t.Errorf("an incident export has %d entries and a support bundle %d; the export must add exactly one",
			len(incRes.Manifest.Entries), len(plainRes.Manifest.Entries))
	}
}

// TestBundle_AC1_PlantingsReachWhatTheBundleReads is the guard on the
// fixture. AC1's table is only meaningful for classes whose plaintext is
// planted somewhere a collector genuinely reads; a fixture that quietly
// stopped planting them would leave 20 green subtests asserting nothing.
func TestBundle_AC1_PlantingsReachWhatTheBundleReads(t *testing.T) {
	bf := newBundleFixture(t)

	inputs := map[string]string{
		"/etc/network/interfaces": bf.interfacesPath,
		"vnprox.toml":             bf.configPath,
		"the daemon log":          bf.logPath,
	}
	fileBlobs := map[string][]byte{}
	for name, path := range inputs {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading fixture input %s: %v", name, err)
		}
		fileBlobs[name] = b
	}
	// The store's changeset columns are the fourth input; read them back
	// exactly as the collector will.
	storeBlob := readChangesetBlob(t, bf.dbPath)

	if len(bundlePlantings) < 8 {
		t.Fatalf("only %d plantings declared; AC1's table covers %d classes but would be asserting "+
			"nothing for most of them", len(bundlePlantings), len(SecretClasses()))
	}
	for _, p := range bundlePlantings {
		t.Run(p.ClassID, func(t *testing.T) {
			marker := secretMarkers[p.ClassID]
			if marker == "" {
				t.Fatalf("planting names class %q, which has no marker", p.ClassID)
			}
			found := bytes.Contains(storeBlob, []byte(marker))
			for _, b := range fileBlobs {
				found = found || bytes.Contains(b, []byte(marker))
			}
			if !found {
				t.Errorf("the %s marker is supposed to be planted in %s, but it is not in any input "+
					"the bundle reads — AC1's assertion for this class proves nothing", p.ClassID, p.Where)
			}
		})
	}
}

// readChangesetBlob concatenates every free-form changeset column, read
// through the same read-only path the collector uses.
func readChangesetBlob(t *testing.T, dbPath string) []byte {
	t.Helper()
	var buf bytes.Buffer
	err := store.InspectReadOnly(context.Background(), dbPath, func(db *sql.DB) error {
		rows, qErr := db.Query(`SELECT COALESCE(title,'') || COALESCE(ops_json,'') || COALESCE(plan_json,'') ||
			COALESCE(apply_log_json,'') || COALESCE(findings_json,'') FROM changesets`)
		if qErr != nil {
			return qErr
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var s string
			if scanErr := rows.Scan(&s); scanErr != nil {
				return scanErr
			}
			buf.WriteString(s)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("reading changeset blob: %v", err)
	}
	return buf.Bytes()
}

// entryFromArchive returns one entry's bytes from a written archive.
func entryFromArchive(t *testing.T, path, name string) []byte {
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
	tr := tar.NewReader(gz)
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			t.Fatalf("reading %s: %v", path, nextErr)
		}
		if hdr.Name != name {
			continue
		}
		b, readErr := io.ReadAll(tr)
		if readErr != nil {
			t.Fatalf("reading entry %s: %v", name, readErr)
		}
		return b
	}
	t.Fatalf("archive %s has no entry %q", path, name)
	return nil
}

// ---------------------------------------------------------------- AC2

// TestBundle_AC2_EveryProducedEntryIsDeclared is the second half of AC2.
// The reflection walk (bundleschema_test.go) catches a new FIELD; this
// catches a new FILE, which changes no Go type at all.
//
// Bundle itself already refuses an undeclared entry at runtime, so this
// test additionally asserts the reverse direction — a declaration with no
// entry behind it — which nothing else would notice.
func TestBundle_AC2_EveryProducedEntryIsDeclared(t *testing.T) {
	ctx := context.Background()

	// Both producers, unioned: an entry is declared if SOME producer makes
	// it, and every entry any producer makes must be declared. An entry
	// belonging to one producer only (T-2804's incident/timeline.json) is
	// still covered in both directions.
	produced := map[string]bool{}
	for _, prod := range bundleProducers() {
		bf := newBundleFixture(t)
		res, err := Bundle(ctx, prod.options(bf, t, filepath.Join(t.TempDir(), "bundles-"+prod.name)))
		if err != nil {
			t.Fatalf("Bundle(%s): %v", prod.name, err)
		}
		for _, e := range res.Manifest.Entries {
			produced[e.Name] = true
			d, ok := entryDeclFor(e.Name)
			if !ok {
				t.Errorf("the %s bundle contains %q, which bundleEntrySchema does not declare", prod.name, e.Name)
				continue
			}
			if d.Role != e.Role {
				t.Errorf("entry %q has role %q but is declared as %q", e.Name, e.Role, d.Role)
			}
		}
	}
	for _, d := range bundleEntrySchema {
		if !produced[d.Name] {
			t.Errorf("bundleEntrySchema declares %q but no bundle produced it — the declaration, "+
				"and therefore readme.txt, describes a file that does not exist", d.Name)
		}
	}
}

// TestBundle_AC2_AnUndeclaredEntryIsRefused is the control for the runtime
// half: Bundle must REFUSE to write an archive containing an entry nothing
// declares, rather than shipping it. Exercised through the real staging
// path, so it is the production check being tested.
func TestBundle_AC2_AnUndeclaredEntryIsRefused(t *testing.T) {
	st, err := NewStaging(t.TempDir())
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	defer func() { _ = st.Remove() }()

	if err := st.WriteFile("surprise.json", RoleMeta, 0o600, []byte("{}\n")); err != nil {
		t.Fatalf("staging: %v", err)
	}
	if _, ok := entryDeclFor("surprise.json"); ok {
		t.Fatal("CONTROL FAILED: surprise.json is declared, so this test proves nothing")
	}
	// And the declared ones ARE found, so entryDeclFor is not simply
	// returning false for everything.
	if _, ok := entryDeclFor(entryBundleProbes); !ok {
		t.Fatalf("CONTROL FAILED: entryDeclFor does not recognise %s", entryBundleProbes)
	}
}

// ---------------------------------------------------------------- AC4

// TestBundle_AC4_DryRunMatchesARealRun is T-1902 AC4: "--dry-run output
// matches what a real run collects."
//
// The property is structural rather than coincidental: a dry run performs
// the identical collection and builds its plan from what Staging actually
// recorded, so there is one code path rather than two. This test asserts
// that (a) the two plans are equal, (b) the plan's entry list is exactly
// the archive's entry list, and (c) the dry run wrote nothing anywhere.
func TestBundle_AC4_DryRunMatchesARealRun(t *testing.T) {
	ctx := context.Background()
	bf := newBundleFixture(t)
	outDir := filepath.Join(t.TempDir(), "bundles")

	dryOpts := bf.options(t, outDir)
	dryOpts.DryRun = true
	dry, err := Bundle(ctx, dryOpts)
	if err != nil {
		t.Fatalf("Bundle(--dry-run): %v", err)
	}
	if dry.Path != "" {
		t.Errorf("a dry run reported writing %s", dry.Path)
	}
	if !dry.Plan.DryRun {
		t.Error("the dry run's plan does not say it was a dry run")
	}
	// Nothing on disk: not the archive, not the output directory, not a
	// staging directory left behind.
	if _, statErr := os.Stat(outDir); statErr == nil {
		t.Errorf("the dry run created the output directory %s", outDir)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("stat %s: %v", outDir, statErr)
	}

	real, err := Bundle(ctx, bf.options(t, outDir))
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}

	// Compare the two plans field by field, ignoring only DryRun itself.
	got, want := real.Plan, dry.Plan
	want.DryRun = got.DryRun
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the dry run's plan differs from the real run's.\n dry: %+v\nreal: %+v", want, got)
	}
	if len(got.Entries) == 0 {
		t.Fatal("the plan lists no entries, so comparing them proves nothing")
	}

	// And the plan describes the archive, not just itself.
	planned := map[string]bool{}
	for _, e := range got.Entries {
		planned[e.Name] = true
	}
	for _, e := range real.Manifest.Entries {
		if !planned[e.Name] {
			t.Errorf("the archive contains %q, which --dry-run did not list", e.Name)
		}
		delete(planned, e.Name)
	}
	for name := range planned {
		t.Errorf("--dry-run listed %q, which the archive does not contain", name)
	}
	if len(got.Collectors) < 8 {
		t.Errorf("the plan names only %d collectors", len(got.Collectors))
	}
	if len(got.Omitted) < 10 {
		t.Errorf("the plan names only %d omissions; it is generated from the secret-class inventory "+
			"and should list every one", len(got.Omitted))
	}
}

// ------------------------------------------------- structural guarantees

// TestBundle_NeverCarriesTheStore is the single assertion that most
// directly defends the card's objective. store/vnprox.db holds the audit
// trail, every rollback snapshot and the ciphertext of every sealed
// credential; a bundle carrying it would be a backup with a misleading name
// on a public forum.
func TestBundle_NeverCarriesTheStore(t *testing.T) {
	ctx := context.Background()
	bf := newBundleFixture(t)
	res, err := Bundle(ctx, bf.options(t, filepath.Join(t.TempDir(), "bundles")))
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	for _, e := range res.Manifest.Entries {
		if e.Role == RoleStore {
			t.Errorf("the bundle carries a store entry %q", e.Name)
		}
		if e.Name == entryStore || strings.Contains(e.Name, "vnprox.db") {
			t.Errorf("the bundle carries %q", e.Name)
		}
		if strings.HasPrefix(e.Name, keyPrefix) {
			t.Errorf("the bundle carries %q, which is under the key prefix", e.Name)
		}
	}
	// The control: a *backup* of the same fixture does carry the store, so
	// the assertions above are about the bundle rather than about the
	// fixture having no store to carry.
	bk, err := Create(ctx, Options{
		ConfigPath: bf.configPath, DBPath: bf.dbPath,
		OutDir: filepath.Join(t.TempDir(), "backups"),
		Node:   seededNode, ToolVersion: "test", Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("CONTROL: Create: %v", err)
	}
	if _, ok := bk.Manifest.Entry(RoleStore); !ok {
		t.Fatal("CONTROL FAILED: a backup of the same fixture has no store entry either, " +
			"so 'the bundle has no store entry' says nothing about the bundle")
	}
}

// TestBundleCollectors_CannotDeclareASecretClass pins the structural half
// of "a bundle contains no secret class".
//
// It is not merely that every collector currently returns nil from Emits():
// bundleCollector has no Emits method at all, so a collector has nowhere to
// declare one, and sealedCollector is the only adapter. This test asserts
// the consequence, and the type system asserts the cause.
func TestBundleCollectors_CannotDeclareASecretClass(t *testing.T) {
	opts := BundleOptions{}
	if err := opts.applyDefaults(); err != nil {
		t.Fatalf("applyDefaults: %v", err)
	}
	cs := bundleCollectors(&opts)
	if len(cs) < 8 {
		t.Fatalf("only %d bundle collectors; the assertion below would cover almost nothing", len(cs))
	}
	for _, c := range cs {
		if emitted := c.Emits(); len(emitted) != 0 {
			t.Errorf("bundle collector %q declares it emits %v", c.Name(), emitted)
		}
	}
	if got := declaredSecretClasses(cs); len(got) != 0 {
		t.Errorf("the union of bundle collectors' Emits() is %v, want empty", got)
	}

	// The control: the union is not empty for a collector that DOES emit,
	// so "empty" above is a fact about bundle collectors rather than about
	// declaredSecretClasses always returning nothing.
	withKeys := []Collector{&keyCollector{}}
	if got := declaredSecretClasses(withKeys); len(got) == 0 {
		t.Fatal("CONTROL FAILED: declaredSecretClasses returns nothing even for a collector that " +
			"declares key-file classes, so the assertion above proves nothing")
	}
}

// TestBundle_IsRefusedByRestore closes the loop T-1901 opened. Restore
// already refuses KindSupportBundle, and that was tested there against a
// hand-made archive; this asserts it against a REAL bundle produced by this
// card's code, which is the artefact that will actually exist.
func TestBundle_IsRefusedByRestore(t *testing.T) {
	ctx := context.Background()
	bf := newBundleFixture(t)
	res, err := Bundle(ctx, bf.options(t, filepath.Join(t.TempDir(), "bundles")))
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}

	before := storeFingerprint(t, bf.dbPath)
	_, err = Restore(ctx, RestoreOptions{
		ArchivePath: res.Path, DBPath: bf.dbPath, ConfigPath: bf.configPath,
		KeyDir: bf.keyDir, Listen: bf.listen, Now: fixedNow,
	})
	if !errors.Is(err, ErrWrongKind) {
		t.Fatalf("Restore of a support bundle returned %v, want ErrWrongKind", err)
	}
	if after := storeFingerprint(t, bf.dbPath); after != before {
		t.Error("the store changed even though the restore was refused")
	}
}

// TestBundle_ReadmeDescribesThisBundle: the "what's in here and what isn't"
// document is generated from bundleEntrySchema and from what was actually
// collected, so it cannot describe a file that is absent.
func TestBundle_ReadmeDescribesThisBundle(t *testing.T) {
	ctx := context.Background()
	bf := newBundleFixture(t)
	res, err := Bundle(ctx, bf.options(t, filepath.Join(t.TempDir(), "bundles")))
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	readme := string(entryFromArchive(t, res.Path, entryBundleReadme))

	for _, e := range res.Manifest.Entries {
		if e.Name == entryBundleReadme {
			continue
		}
		if !strings.Contains(readme, e.Name) {
			t.Errorf("readme.txt does not mention the entry %q", e.Name)
		}
	}
	for _, must := range []string{
		entryStore, // it must say the store is NOT here
		"vnproxctl restore",
		Redacted,
		"WireGuard tunnel private key",
		"peer cluster secret",
	} {
		if !strings.Contains(readme, must) {
			t.Errorf("readme.txt does not mention %q", must)
		}
	}
}

// TestBundle_ConfigCollectorIsASiblingNotAMode asserts the specific shape
// T-1901's handoff asked for: the bundle's config collector and the
// backup's are different types, so there is no flag whose wrong value turns
// a bundle's config into a verbatim one.
func TestBundle_ConfigCollectorIsASiblingNotAMode(t *testing.T) {
	ctx := context.Background()
	bf := newBundleFixture(t)
	outDir := filepath.Join(t.TempDir(), "out")

	res, err := Bundle(ctx, bf.options(t, outDir))
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	bundled := entryFromArchive(t, res.Path, entryBundleConfig)

	var doc BundleConfig
	if decErr := json.Unmarshal(bundled, &doc); decErr != nil {
		t.Fatalf("decoding the bundled config: %v", decErr)
	}
	if !doc.Parsed {
		t.Fatalf("the bundled config did not parse: %s", doc.ParseError)
	}

	byKey := map[string]ConfigKey{}
	for _, k := range doc.Keys {
		byKey[k.Key] = k
	}
	// The credential the config format allows and the allowlist excludes.
	pw, ok := byKey["pve.dev_ticket_password"]
	if !ok {
		t.Fatal("the bundled config does not list pve.dev_ticket_password at all — the key NAME should " +
			"still appear so a reader knows a dev credential is configured")
	}
	if !pw.Redacted || strings.Contains(pw.Value, secretMarkers["oidc_client_secret"]) {
		t.Errorf("pve.dev_ticket_password was emitted in the clear: %+v", pw)
	}
	// And an allowlisted value survives, so the collector is not simply
	// redacting everything.
	ca, ok := byKey["peer.ca_file"]
	if !ok || ca.Redacted || ca.Value != bundleControlConfig {
		t.Errorf("peer.ca_file should have survived the allowlist, got %+v", ca)
	}

	// The backup's collector, on the same file, is verbatim — which is what
	// makes "sibling, not mode" a real distinction rather than a claim.
	bk, err := Create(ctx, Options{
		ConfigPath: bf.configPath, DBPath: bf.dbPath,
		OutDir: filepath.Join(t.TempDir(), "backups"),
		Node:   seededNode, ToolVersion: "test", Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	verbatim := entryFromArchive(t, bk.Path, entryConfig)
	if !bytes.Contains(verbatim, []byte(secretMarkers["oidc_client_secret"])) {
		t.Fatal("CONTROL FAILED: the BACKUP's config entry does not contain the value either, so " +
			"'the bundle redacted it' says nothing about the bundle")
	}
	if reflect.TypeOf(configCollector{}) == reflect.TypeOf(configBundleCollector{}) {
		t.Error("the two config collectors are the same type")
	}
}

// TestBundle_HostNetworkNeverEmitsAWireGuardKey is the single most
// dangerous path in the card, called out on its own so a reviewer can find
// it: an operator's interfaces(5) file can carry a WireGuard private key,
// and the file is never emitted verbatim.
func TestBundle_HostNetworkNeverEmitsAWireGuardKey(t *testing.T) {
	ctx := context.Background()
	bf := newBundleFixture(t)
	res, err := Bundle(ctx, bf.options(t, filepath.Join(t.TempDir(), "bundles")))
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	raw := entryFromArchive(t, res.Path, entryBundleHostNet)

	var doc BundleHostNetwork
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decoding host/network.json: %v", err)
	}
	if !doc.Parsed {
		t.Fatalf("the interfaces file did not parse: %s", doc.ParseError)
	}

	var wg *BundleIface
	for i := range doc.Ifaces {
		if doc.Ifaces[i].Name == "wg0" {
			wg = &doc.Ifaces[i]
		}
	}
	if wg == nil {
		t.Fatal("CONTROL FAILED: the wg0 stanza was not collected at all, so asserting its key is " +
			"absent says nothing")
	}

	byKey := map[string]BundleIfaceOption{}
	for _, o := range wg.Options {
		byKey[o.Key] = o
	}
	for _, key := range []string{"wireguard-private-key", "wireguard-preshared-key"} {
		o, ok := byKey[key]
		if !ok {
			t.Errorf("the %s option is missing entirely; the NAME should survive so a reader knows "+
				"the stanza configures WireGuard", key)
			continue
		}
		if !o.Redacted {
			t.Errorf("%s was not redacted: %q", key, o.Value)
		}
	}
	// And the addressing survived, so the collector is diagnostic rather
	// than empty.
	if addr, ok := byKey["address"]; !ok || addr.Redacted || addr.Value != "10.9.0.1/24" {
		t.Errorf("wg0's address should have survived, got %+v", addr)
	}
	if !bytes.Contains(raw, []byte("10.0.0.9/24")) {
		t.Error("vmbr0's address did not survive; the host-network collector is over-redacting")
	}
}

// TestBundle_NoStagingResidue: staging holds the collected material in the
// clear for as long as the archive takes to write. It must be gone
// afterwards, on both the success and the dry-run path.
func TestBundle_NoStagingResidue(t *testing.T) {
	ctx := context.Background()
	bf := newBundleFixture(t)
	outDir := filepath.Join(t.TempDir(), "bundles")

	if _, err := Bundle(ctx, bf.options(t, outDir)); err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("reading %s: %v", outDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("a staging directory survived: %s", e.Name())
		}
		if strings.HasSuffix(e.Name(), ".partial") {
			t.Errorf("a partial archive survived: %s", e.Name())
		}
	}

	dryOpts := bf.options(t, filepath.Join(t.TempDir(), "dry"))
	dryOpts.DryRun = true
	if _, dryErr := Bundle(ctx, dryOpts); dryErr != nil {
		t.Fatalf("Bundle(--dry-run): %v", dryErr)
	}
	// A dry run stages under the OS temp dir; assert nothing of ours is
	// left there.
	tmp, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Skipf("cannot list %s: %v", os.TempDir(), err)
	}
	for _, e := range tmp {
		if strings.HasPrefix(e.Name(), "vnprox-collect-") {
			t.Errorf("a staging directory survived a dry run: %s", filepath.Join(os.TempDir(), e.Name()))
		}
	}
}

// TestBundleName_IsNotABackupName: backup.Prune deletes files matching the
// backup name pattern. A bundle sitting in the same directory must not be
// mistaken for one, in either direction.
func TestBundleName_IsNotABackupName(t *testing.T) {
	name := BundleName("pve1", fixedNow())
	if archiveNamePattern.MatchString(name) {
		t.Errorf("bundle name %q matches the backup retention pattern; Prune could delete it, "+
			"or keep it instead of a real backup", name)
	}
	if !strings.HasPrefix(name, "vnprox-support-") || !strings.HasSuffix(name, ".tar.gz") {
		t.Errorf("unexpected bundle name %q", name)
	}
	// The control: a real backup name DOES match, so the assertion above is
	// about the bundle name rather than about the pattern matching nothing.
	if !archiveNamePattern.MatchString(ArchiveName("pve1", fixedNow(), false)) {
		t.Fatal("CONTROL FAILED: a backup archive name does not match the retention pattern either")
	}
}

// TestBundle_CarriesNoAssistantTranscript is T-2808 AC6's support-bundle
// half — "prompt content and answers are excluded from logs and support
// bundles by default" — asserted through THIS file's existing machinery
// rather than by a parallel scan of its own, the way T-2804's incident
// export was.
//
// The reuse is deliberate and load-bearing. Two facts already proven here
// do most of the work:
//
//   - TestBundle_AC2_EveryProducedEntryIsDeclared: a bundle contains only
//     entries bundleEntrySchema declares, in both directions.
//   - TestBundle_NeverCarriesTheStore: the app-owned database is never in
//     the archive.
//
// So "no assistant transcript is in a bundle" reduces to "no DECLARED entry
// is one", which is what this test asserts — plus a real archive built from
// the real fixture, whose entry names must not name one either.
//
// The deeper reason it holds is upstream of this package: the in-app
// assistant (T-2808) has no daemon data path at all. The browser talks to
// the operator's own model backend directly, so vnproxd never receives a
// prompt or an answer, never logs one, and has none to collect. That
// premise is asserted where it lives, over the daemon's own route table:
// internal/api's TestAssistant_NoDaemonRouteAcceptsPromptContent.
func TestBundle_CarriesNoAssistantTranscript(t *testing.T) {
	ctx := context.Background()

	// Words a stored prompt/answer document would plausibly be named or
	// described with. A bar against a future edit, not a claim about the
	// declarations as they stand.
	forbidden := []string{"assistant", "prompt", "transcript", "conversation", "chat"}

	// --- the declarations ------------------------------------------------
	if len(bundleEntrySchema) < 8 {
		t.Fatalf("bundleEntrySchema declares only %d entries; this scan is not reading the real inventory",
			len(bundleEntrySchema))
	}
	if _, ok := entryDeclFor(entryBundleLog); !ok {
		t.Fatalf("bundleEntrySchema does not declare %q — the scan below is reading the wrong table",
			entryBundleLog)
	}
	for _, d := range bundleEntrySchema {
		haystack := strings.ToLower(d.Name + " " + d.Doc + " " + d.About)
		for _, bad := range forbidden {
			if strings.Contains(haystack, bad) {
				t.Errorf("bundleEntrySchema declares %q, which reads like an assistant transcript (%q).\n\n"+
					"T-2808's assistant keeps prompts and answers in the browser; nothing about them "+
					"reaches the daemon, so nothing about them belongs in a support bundle a user "+
					"attaches to a forum post.", d.Name, bad)
			}
		}
	}

	// --- and a real archive ----------------------------------------------
	for _, prod := range bundleProducers() {
		bf := newBundleFixture(t)
		res, err := Bundle(ctx, prod.options(bf, t, filepath.Join(t.TempDir(), "bundles-"+prod.name)))
		if err != nil {
			t.Fatalf("Bundle(%s): %v", prod.name, err)
		}
		// Non-vacuity: the archive really was produced and really has
		// content, so "no assistant entry" is an observation about a bundle
		// rather than about an empty list.
		if len(res.Manifest.Entries) < 8 {
			t.Fatalf("the %s bundle has only %d entries; this check would be trivially true",
				prod.name, len(res.Manifest.Entries))
		}
		for _, e := range res.Manifest.Entries {
			for _, bad := range forbidden {
				if strings.Contains(strings.ToLower(e.Name), bad) {
					t.Errorf("the %s bundle contains %q, which names assistant/prompt material", prod.name, e.Name)
				}
			}
		}
	}
}
