// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/gitsync"
)

func TestRunGitSync_UnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gitsync", "sync-now"}, &stdout, &stderr); code != ExitUsage {
		t.Fatalf("exit code = %d, want ExitUsage — there is no sync-now verb", code)
	}
	if code := run([]string{"gitsync"}, &stdout, &stderr); code != ExitUsage {
		t.Fatalf("bare `gitsync` exit code = %d, want ExitUsage", code)
	}
}

// TestRunGitSyncStatus_TableAndJSON drives the command against a fake daemon
// and asserts the three things the card requires the status to show: the
// last fetched sha, the last plan, and why the draft exists.
func TestRunGitSyncStatus_TableAndJSON(t *testing.T) {
	want := gitsync.Status{
		Enabled: true, Remote: "https://github.com/org/infra (github)",
		Ref: "main", Path: "network/cluster.yaml", PollIntervalSeconds: 300,
		RequireSignedCommits: true,
		LastFetchedSHA:       "abcdef0123456789abcdef0123456789abcdef01",
		LastFetchAt:          1754000000, LastSuccessAt: 1754000000,
		LastSigner:      "ops@example.com",
		PlanOpCount:     2,
		Plan:            []string{"bridge.update bridge:pve1:vmbr0", "bridge.create bridge:pve1:vmbr9"},
		NotInSpec:       []string{"vlan:pve2:vmbr0.30"},
		OpenChangesetID: "01JABCDEF", OpenChangesetReason: "the spec at network/cluster.yaml @ abcdef012345 differs from live state in 2 place(s); vnprox staged the reconciling ops for review and applied nothing",
	}

	var gotPath string
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		requireBearerToken(t, r, "tok")
		gotPath = r.URL.Path
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET — `gitsync status` must never mutate", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	})

	var stdout, stderr bytes.Buffer
	if code := run([]string{"gitsync", "status", "--url", srv.URL, "--token", "tok"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if gotPath != "/gitsync/status" {
		t.Errorf("path = %q, want /gitsync/status", gotPath)
	}
	out := stdout.String()
	for _, needle := range []string{
		want.LastFetchedSHA,               // the last fetched sha
		"bridge.update bridge:pve1:vmbr0", // the last plan
		want.OpenChangesetID,              // the draft
		"applied nothing",                 // why it exists
		"vlan:pve2:vmbr0.30",              // reported, never deleted
		"ops@example.com",                 // the verified signer
		"never applies a sync draft",      // the standing reminder
	} {
		if !strings.Contains(out, needle) {
			t.Errorf("table output is missing %q:\n%s", needle, out)
		}
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"gitsync", "status", "--url", srv.URL, "--token", "tok", "-o", "json"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("json exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	var decoded gitsync.Status
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if decoded.LastFetchedSHA != want.LastFetchedSHA || decoded.OpenChangesetID != want.OpenChangesetID {
		t.Errorf("decoded = %+v, want the daemon's status verbatim", decoded)
	}
}

// TestRunGitSyncStatus_DisabledSaysSo: an operator who has not configured
// the sync must get a plain "disabled", not an empty table they have to
// interpret.
func TestRunGitSyncStatus_DisabledSaysSo(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gitsync.Status{Enabled: false})
	})
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gitsync", "status", "--url", srv.URL, "--token", "tok"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "disabled") {
		t.Errorf("stdout = %q, want it to say the sync is disabled", stdout.String())
	}
}

// TestRunGitSyncStatus_NoTokenNeverDials mirrors the remote family's own
// fail-fast contract: no credential, no daemon call.
func TestRunGitSyncStatus_NoTokenNeverDials(t *testing.T) {
	dialed := false
	srv := newFakeVnproxd(t, func(http.ResponseWriter, *http.Request) { dialed = true })
	t.Setenv("VNPROX_TOKEN", "")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gitsync", "status", "--url", srv.URL}, &stdout, &stderr); code != ExitAuth {
		t.Fatalf("exit code = %d, want ExitAuth", code)
	}
	if dialed {
		t.Error("a daemon call was attempted without a token")
	}
}
