// SPDX-License-Identifier: Apache-2.0

package pvecassette_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pvecassette"
)

// writeRaw drops a file into dir *without* going through Writer.
//
// That is the whole point of these cases: the write-side guard cannot be
// the only guard, because a cassette is a file in a git repository. It can
// be hand-edited, resolved badly in a merge, or pasted in by someone who
// recorded it with a build that predates the guard. Every one of those
// arrives on disk without Writer ever seeing it.
func writeRaw(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

const goodCassette = `{
  "recordedAt": "2026-08-10T12:00:00Z",
  "pveVersion": "8.3.5",
  "method": "GET",
  "path": "/api2/json/cluster/status",
  "status": 200,
  "body": "{\"data\":[{\"type\":\"cluster\",\"name\":\"pve-cluster-a\",\"quorate\":1}]}\n"
}
`

// TestLoad_RefusesAHandEditedSecret: the scan runs on the way in as well
// as on the way out.
func TestLoad_RefusesAHandEditedSecret(t *testing.T) {
	dir := t.TempDir()
	const ticket = "PVE:root@pam:68A1B2C3::c2VjcmV0c2lnbmF0dXJlYmFzZTY0ZGF0YQ=="
	path := writeRaw(t, dir, "poisoned.json", `{
  "recordedAt": "2026-08-10T12:00:00Z",
  "pveVersion": "8.3.5",
  "method": "POST",
  "path": "/api2/json/access/ticket",
  "status": 200,
  "body": "{\"data\":{\"ticket\":\"`+ticket+`\"}}"
}
`)

	_, err := pvecassette.Load(path)
	if err == nil {
		t.Fatal("Load accepted a cassette carrying a PVE ticket")
	}
	if !errors.Is(err, pvecassette.ErrSecretInCassette) {
		t.Errorf("error is not ErrSecretInCassette: %v", err)
	}
	if !strings.Contains(err.Error(), "body.data.ticket") {
		t.Errorf("error does not name the offending field: %v", err)
	}

	// And the directory walk inherits the refusal rather than skipping the
	// file and loading the rest.
	writeRaw(t, dir, "fine.json", goodCassette)
	if _, err := pvecassette.LoadDir(dir); !errors.Is(err, pvecassette.ErrSecretInCassette) {
		t.Errorf("LoadDir error = %v, want ErrSecretInCassette", err)
	}
}

// TestLoadDir_RefusesTwoCassettesForOneRequest: which file answered would
// silently decide what a test observed.
func TestLoadDir_RefusesTwoCassettesForOneRequest(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "a.json", goodCassette)
	writeRaw(t, dir, "b.json", strings.Replace(goodCassette, `\"quorate\":1`, `\"quorate\":0`, 1))

	_, err := pvecassette.LoadDir(dir)
	if !errors.Is(err, pvecassette.ErrDuplicateCassette) {
		t.Fatalf("LoadDir error = %v, want ErrDuplicateCassette", err)
	}
	for _, name := range []string{"a.json", "b.json"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error does not name %s, so nobody can go delete one: %v", name, err)
		}
	}
}

// TestLoadDir_ReadsNestedVersionDirectories: cassettes live under
// <root>/<pve-version>/, and a caller pointed at the root gets all of
// them.
func TestLoadDir_ReadsNestedVersionDirectories(t *testing.T) {
	root := t.TempDir()
	v1 := filepath.Join(root, "8.3.5")
	if err := os.Mkdir(v1, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeRaw(t, v1, "status.json", goodCassette)
	// Not a cassette: must be ignored rather than fail the walk.
	writeRaw(t, v1, "README.md", "these came from pve1 in the lab\n")

	set, err := pvecassette.LoadDir(root)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if got := pvecassette.Keys(set); len(got) != 1 || got[0] != "GET /api2/json/cluster/status" {
		t.Errorf("Keys() = %v", got)
	}
}

// TestLoad_RefusesUnknownFields: a cassette with a field this build does
// not understand was written by something else, and quietly ignoring it
// would mean replaying a recording whose semantics we only half know.
func TestLoad_RefusesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := writeRaw(t, dir, "future.json",
		strings.Replace(goodCassette, `"status": 200,`, `"status": 200,
  "requestHeaders": {"Authorization": "PVEAPIToken=root@pam!daemon=deadbeef"},`, 1))

	if _, err := pvecassette.Load(path); err == nil {
		t.Error("Load accepted a cassette with an unknown field")
	}
}

// TestLoadDir_MissingDirectory: a typo in a path must not present as "no
// cassettes matched".
func TestLoadDir_MissingDirectory(t *testing.T) {
	if _, err := pvecassette.LoadDir(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("LoadDir accepted a directory that does not exist")
	}
}
