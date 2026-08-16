package verify

import (
	"context"
	"strings"
	"testing"
)

// TestPWAServable_RunsWithoutADaemonToken is the regression test for a defect
// found on 2026-08-16 while deploying Phase 29 to a real node: `pwa.servable`
// read its RootProbe off Deps.Daemon, which vnproxctl only builds when a
// bearer token is configured. A freshly installed node has no token, so the
// one check written to detect the v4.0.0 CSP defect skipped on exactly the
// deployments that had it — it reported "0 passed, 1 skipped" against a node
// serving `worker-src 'none'`.
//
// None of the three paths the check reads (`/`, `/manifest.webmanifest`,
// `/sw.js`) is authenticated, so Deps.Root now carries an anonymous prober
// and the check must both PASS and FAIL correctly with Deps.Daemon nil.
func TestPWAServable_RunsWithoutADaemonToken(t *testing.T) {
	t.Parallel()

	t.Run("passes on a healthy root surface with no daemon client at all", func(t *testing.T) {
		t.Parallel()
		d := healthyDeps()
		root, ok := d.Daemon.(*fakeDaemon)
		if !ok {
			t.Fatalf("fixture daemon is not a *fakeDaemon")
		}
		d.Root = root
		d.Daemon = nil // the freshly-installed-node case: no token, no API client

		got := checkPWAServable(context.Background(), d)
		if got.Status != StatusPass {
			t.Fatalf("status = %v (%s), want pass — the check must not need a bearer token to read unauthenticated paths", got.Status, got.Detail)
		}
	})

	t.Run("still catches the v4.0.0 CSP defect with no daemon client", func(t *testing.T) {
		t.Parallel()
		d := healthyDeps()
		root, ok := d.Daemon.(*fakeDaemon)
		if !ok {
			t.Fatalf("fixture daemon is not a *fakeDaemon")
		}
		root.rootResponses["/"] = fakeRootResponse{header: map[string]string{
			"Content-Security-Policy": "default-src 'self'; worker-src 'none'; manifest-src 'none'",
		}, body: "<!doctype html>"}
		d.Root = root
		d.Daemon = nil

		got := checkPWAServable(context.Background(), d)
		if got.Status != StatusFail {
			t.Fatalf("status = %v (%s), want fail — this is the exact policy that shipped in v4.0.0", got.Status, got.Detail)
		}
		for _, want := range []string{"worker-src", "manifest-src"} {
			if !strings.Contains(got.Detail, want) {
				t.Errorf("detail %q does not name the %s directive", got.Detail, want)
			}
		}
	})

	t.Run("skips with an actionable reason when neither probe is wired", func(t *testing.T) {
		t.Parallel()
		d := healthyDeps()
		d.Daemon = nil
		d.Root = nil

		got := checkPWAServable(context.Background(), d)
		if got.Status != StatusSkip {
			t.Fatalf("status = %v, want skip", got.Status)
		}
		// The old message told the operator to pass --token. It is the
		// wrong instruction: a token is not required, and following it on a
		// node with no tokens minted is a dead end.
		if strings.Contains(got.Detail, "--token") {
			t.Errorf("skip reason still asks for a token, which this check does not need: %q", got.Detail)
		}
		if !strings.Contains(got.Detail, "--url") {
			t.Errorf("skip reason should name --url as the fix: %q", got.Detail)
		}
	})
}
