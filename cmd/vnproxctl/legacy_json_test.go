// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestSnapshotsList_JSONOutput pins `vnproxctl snapshots list -o json`
// (T-1105's retrofit) against the same daemon-down fixture the pre-existing
// table-output tests use (seedDisasterFixture, snapshots_test.go).
func TestSnapshotsList_JSONOutput(t *testing.T) {
	configPath, _, snapshotID, changesetID := seedDisasterFixture(t, "committed")

	var stdout, stderr bytes.Buffer
	code := runSnapshotsEnv(newCLIEnv(), []string{"list", "--config", configPath, "-o", "json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	var out []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout not valid JSON: %v (%s)", err, stdout.String())
	}
	if len(out) != 1 || out[0]["id"] != snapshotID || out[0]["changesetId"] != changesetID {
		t.Errorf("decoded = %+v, want one snapshot %s/%s", out, snapshotID, changesetID)
	}
}

// TestRollbackNow_JSONOutput pins `vnproxctl rollback-now -o json`.
func TestRollbackNow_JSONOutput(t *testing.T) {
	configPath, ifacesPath, _, changesetID := seedDisasterFixture(t, "awaiting_confirm")
	reloads := 0
	env := testEnv(ifacesPath, &reloads)

	var stdout, stderr bytes.Buffer
	code := runRollbackNowEnv(env, []string{"--config", configPath, "-o", "json", changesetID}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout not valid JSON: %v (%s)", err, stdout.String())
	}
	if out["changesetId"] != changesetID || out["status"] != "rolled_back" {
		t.Errorf("decoded = %+v, want changesetId %s status rolled_back", out, changesetID)
	}
}
