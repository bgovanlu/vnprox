package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeApplyClock builds a deterministic applyClock: now() starts at an
// arbitrary reference and advances by remoteApplyPollInterval every time
// sleep() is invoked — pollChangesetToCommitted therefore drives its own
// timeout deadline without any real wall-clock wait (T-1105 acceptance
// criterion 3: "tested with a short timeout, no real sleeps").
func fakeApplyClock() applyClock {
	var elapsed atomic.Int64 // nanoseconds advanced so far
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return applyClock{
		now: func() time.Time {
			return start.Add(time.Duration(elapsed.Load()))
		},
		sleep: func(d time.Duration) {
			elapsed.Add(int64(d))
		},
	}
}

func writeSpecFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing spec file: %v", err)
	}
	return path
}

func TestRunApply_UsageRequiresExactlyOnePlanOrApply(t *testing.T) {
	specPath := writeSpecFile(t, "specVersion: 1\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"apply", "--url", "https://example.invalid", "--token", "tok", specPath}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want ExitUsage (neither --plan nor --apply given)", code)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"apply", "--url", "https://example.invalid", "--token", "tok", "--plan", "--apply", specPath}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want ExitUsage (both --plan and --apply given)", code)
	}
}

func TestRunApplyPlan_CleanSpecExitsZero(t *testing.T) {
	var importCalls, discardCalls int
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/spec/import":
			importCalls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"cs1","title":"Spec import","author":"a","status":"validated","ops":[],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false,"notInSpec":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/changesets/cs1/diff":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"files":[],"ops":[]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/changesets/cs1":
			discardCalls++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	specPath := writeSpecFile(t, "specVersion: 1\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"apply", "--url", srv.URL, "--token", "tok", "--plan", specPath}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No changes: the spec matches live exactly.") {
		t.Errorf("stdout = %q, want the clean-plan message", stdout.String())
	}
	if importCalls != 1 {
		t.Errorf("importCalls = %d, want 1", importCalls)
	}
	if discardCalls != 1 {
		t.Errorf("discardCalls = %d, want 1 (the preview draft should be cleaned up)", discardCalls)
	}
}

func TestRunApplyPlan_PendingChangesExitsPendingCode(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/spec/import":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"cs1","title":"Spec import","author":"a","status":"validated","ops":[{"kind":"bridge.create"}],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false,"notInSpec":["bridge:pve1:vmbr0"]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/changesets/cs1/diff":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"files":[{"node":"pve1","path":"/etc/network/interfaces","unified":"+++ new bridge\n","changed":true}],"ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr1","node":"pve1","summary":"Create bridge vmbr1"}]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/changesets/cs1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	specPath := writeSpecFile(t, "specVersion: 1\nbridges: []\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"apply", "--url", srv.URL, "--token", "tok", "--plan", "-o", "json", specPath}, &stdout, &stderr)
	if code != ExitPending {
		t.Fatalf("exit code = %d, want ExitPending (stderr: %s)", code, stderr.String())
	}
	var decoded planResultWire
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout not valid JSON: %v (%s)", err, stdout.String())
	}
	if !decoded.Pending {
		t.Error("decoded.Pending = false, want true")
	}
	if len(decoded.NotInSpec) != 1 || decoded.NotInSpec[0] != "bridge:pve1:vmbr0" {
		t.Errorf("decoded.NotInSpec = %+v, want [bridge:pve1:vmbr0]", decoded.NotInSpec)
	}
}

func TestRunApplyApply_EndToEndSuccess(t *testing.T) {
	var applyCalls, confirmCalls int32
	getCount := 0
	confirmed := false
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/spec/import":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"cs1","title":"Spec import","author":"a","status":"validated","ops":[{"kind":"bridge.create"}],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false,"notInSpec":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/changesets/cs1/apply":
			applyCalls++
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"cs1","title":"t","author":"a","status":"applying","ops":[],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false}`))
		case r.Method == http.MethodGet && r.URL.Path == "/changesets/cs1":
			getCount++
			status := "applying"
			switch {
			case confirmed:
				status = "committed"
			case getCount >= 2:
				status = "awaiting_confirm"
			}
			_, _ = w.Write([]byte(`{"id":"cs1","title":"t","author":"a","status":"` + status + `","ops":[],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false}`))
		case r.Method == http.MethodPost && r.URL.Path == "/changesets/cs1/confirm":
			confirmCalls++
			confirmed = true
			_, _ = w.Write([]byte(`{"id":"cs1","title":"t","author":"a","status":"committed","ops":[],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	specPath := writeSpecFile(t, "specVersion: 1\n")
	var stdout, stderr bytes.Buffer
	code := runApplyWithClock([]string{"--url", srv.URL, "--token", "tok", "--apply", specPath}, &stdout, &stderr, fakeApplyClock())
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if applyCalls != 1 {
		t.Errorf("applyCalls = %d, want 1", applyCalls)
	}
	if confirmCalls != 1 {
		t.Errorf("confirmCalls = %d, want 1 (auto-confirm)", confirmCalls)
	}
	if !strings.Contains(stdout.String(), "committed") {
		t.Errorf("stdout = %q, want the final committed status", stdout.String())
	}
}

func TestRunApplyApply_StuckPastTimeoutExitsApplyTimeout(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/spec/import":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"cs1","title":"Spec import","author":"a","status":"validated","ops":[{"kind":"bridge.create"}],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false,"notInSpec":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/changesets/cs1/apply":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"cs1","title":"t","author":"a","status":"applying","ops":[],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false}`))
		case r.Method == http.MethodGet && r.URL.Path == "/changesets/cs1":
			// Never progresses past "applying" — simulates a stuck changeset.
			_, _ = w.Write([]byte(`{"id":"cs1","title":"t","author":"a","status":"applying","ops":[],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	specPath := writeSpecFile(t, "specVersion: 1\n")
	var stdout, stderr bytes.Buffer
	// A tiny --apply-timeout relative to remoteApplyPollInterval (2s): the
	// fake clock's sleep() advances by that whole interval per iteration, so
	// this deadline is exceeded on the very first poll iteration without any
	// real wall-clock wait at all.
	code := runApplyWithClock([]string{
		"--url", srv.URL, "--token", "tok", "--apply", "--apply-timeout", "1ms", specPath,
	}, &stdout, &stderr, fakeApplyClock())
	if code != ExitApplyTimeout {
		t.Fatalf("exit code = %d, want ExitApplyTimeout (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "did not reach committed") {
		t.Errorf("stderr = %q, want a timeout explanation", stderr.String())
	}
}

func TestRunApplyApply_BlockingFindingsIsExitPending(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/spec/import":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"cs1","title":"Spec import","author":"a","status":"validated","ops":[{"kind":"bridge.create"}],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false,"notInSpec":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/changesets/cs1/apply":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":{"code":"validation_failed","message":"changeset has blocking validation errors"}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	specPath := writeSpecFile(t, "specVersion: 1\n")
	var stdout, stderr bytes.Buffer
	code := runApplyWithClock([]string{"--url", srv.URL, "--token", "tok", "--apply", specPath}, &stdout, &stderr, fakeApplyClock())
	if code != ExitPending {
		t.Fatalf("exit code = %d, want ExitPending (stderr: %s)", code, stderr.String())
	}
}

func TestRunApply_MissingSpecFileIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"apply", "--url", "https://example.invalid", "--token", "tok", "--plan", "/no/such/spec.yaml"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want ExitUsage", code)
	}
}
