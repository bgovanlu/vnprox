// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
)

func newTestAgent(t *testing.T, reload func(context.Context) error) *hostNodeAgent {
	t.Helper()
	dir := t.TempDir()
	ifPath := filepath.Join(dir, "interfaces")
	if err := os.WriteFile(ifPath, []byte("original\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return &hostNodeAgent{
		interfacesPath: ifPath,
		pendingPath:    filepath.Join(dir, "interfaces.new"),
		reload:         reload,
		log:            slog.Default(),
	}
}

func TestHostNodeAgent_StageReloadCommits(t *testing.T) {
	a := newTestAgent(t, func(context.Context) error { return nil })
	ctx := context.Background()

	if err := a.StageInterfaces(ctx, "pve1", "new content\n"); err != nil {
		t.Fatalf("stage: %v", err)
	}
	// Not committed until reload.
	if got, _ := a.ReadInterfaces(ctx, "pve1"); got != "original\n" {
		t.Fatalf("committed changed before reload: %q", got)
	}
	if err := a.ReloadInterfaces(ctx, "pve1"); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, _ := a.ReadInterfaces(ctx, "pve1"); got != "new content\n" {
		t.Fatalf("committed = %q, want new content", got)
	}
	// Staged file is cleaned up.
	if _, err := os.Stat(a.pendingPath); !os.IsNotExist(err) {
		t.Fatal("staged file not removed after reload")
	}
}

func TestHostNodeAgent_ReloadFailureRestores(t *testing.T) {
	reloadErr := errors.New("ifreload boom")
	calls := 0
	a := newTestAgent(t, func(context.Context) error {
		calls++
		// First call (the real apply) fails; the restore re-reload succeeds.
		if calls == 1 {
			return reloadErr
		}
		return nil
	})
	ctx := context.Background()

	if err := a.StageInterfaces(ctx, "pve1", "broken content\n"); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if err := a.ReloadInterfaces(ctx, "pve1"); err == nil {
		t.Fatal("expected reload to fail")
	}
	// Committed file must be byte-identical to the pre-reload original.
	if got, _ := a.ReadInterfaces(ctx, "pve1"); got != "original\n" {
		t.Fatalf("committed = %q, want restored original", got)
	}
}

func TestHostNodeAgent_Discard(t *testing.T) {
	a := newTestAgent(t, func(context.Context) error { return nil })
	ctx := context.Background()
	if err := a.StageInterfaces(ctx, "pve1", "x\n"); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if err := a.DiscardStaged(ctx, "pve1"); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if _, err := os.Stat(a.pendingPath); !os.IsNotExist(err) {
		t.Fatal("staged file still present after discard")
	}
	// Discarding again (nothing staged) is a no-op, not an error.
	if err := a.DiscardStaged(ctx, "pve1"); err != nil {
		t.Fatalf("discard (idempotent): %v", err)
	}
}

func TestUPIDNode(t *testing.T) {
	if got := upidNode("UPID:pve1:00001:00002:0003:sdnapply:sdn:root@pam:"); got != "pve1" {
		t.Fatalf("upidNode = %q, want pve1", got)
	}
	if got := upidNode("garbage"); got != "" {
		t.Fatalf("upidNode(garbage) = %q, want empty", got)
	}
}

// TestNewDevNodeAgent covers the [safety] dev_interfaces_dir sandbox
// (audit-phase-2 F-22): the agent operates only under the sandbox dir,
// seeds a fixture on first use, keeps an existing file on reuse, and its
// reload never execs a real ifreload.
func TestNewDevNodeAgent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dev-host")
	a, err := newDevNodeAgent(dir, testLogger())
	if err != nil {
		t.Fatalf("newDevNodeAgent: %v", err)
	}

	if a.interfacesPath != filepath.Join(dir, "interfaces") || a.pendingPath != filepath.Join(dir, "interfaces.new") {
		t.Fatalf("paths = %q/%q, want them under the sandbox dir %q", a.interfacesPath, a.pendingPath, dir)
	}

	ctx := context.Background()
	got, err := a.ReadInterfaces(ctx, "pve1")
	if err != nil {
		t.Fatalf("ReadInterfaces: %v", err)
	}
	if !strings.Contains(got, "vmbr0") {
		t.Errorf("seeded sandbox file does not contain the fixture bridge: %q", got)
	}

	// Full stage -> reload cycle must succeed with no real ifreload binary
	// involved and commit the staged content to the sandboxed file.
	want := got + "\n# staged by test\n"
	if stageErr := a.StageInterfaces(ctx, "pve1", want); stageErr != nil {
		t.Fatalf("StageInterfaces: %v", stageErr)
	}
	if reloadErr := a.ReloadInterfaces(ctx, "pve1"); reloadErr != nil {
		t.Fatalf("ReloadInterfaces (no-op reload) returned error: %v", reloadErr)
	}
	after, err := a.ReadInterfaces(ctx, "pve1")
	if err != nil {
		t.Fatalf("ReadInterfaces after reload: %v", err)
	}
	if after != want {
		t.Errorf("committed content = %q, want the staged content", after)
	}

	// Re-opening the sandbox must keep the existing (edited) file, not
	// re-seed over it.
	b, err := newDevNodeAgent(dir, testLogger())
	if err != nil {
		t.Fatalf("newDevNodeAgent (reuse): %v", err)
	}
	again, err := b.ReadInterfaces(ctx, "pve1")
	if err != nil {
		t.Fatalf("ReadInterfaces (reuse): %v", err)
	}
	if again != want {
		t.Errorf("re-seeded over an existing sandbox file: got %q", again)
	}
}

// TestDevConfig_SandboxesHostWriter pins the F-22 remediation at the config
// level: the checked-in dev config must never wire the production host
// agent against the real /etc/network/interfaces.
func TestDevConfig_SandboxesHostWriter(t *testing.T) {
	// config.Load validates relative TLS paths against the cwd, and
	// dev.toml's paths are repo-root-relative (matching `make dev`).
	t.Chdir(filepath.Join("..", ".."))
	cfg, err := config.Load(filepath.Join("testdata", "dev.toml"), testLogger())
	if err != nil {
		t.Fatalf("loading testdata/dev.toml: %v", err)
	}
	if cfg.Safety.DevInterfacesDir == "" {
		t.Fatal("testdata/dev.toml must set [safety] dev_interfaces_dir — a dev daemon must not operate on the real /etc/network/interfaces (audit-phase-2 F-22)")
	}
}

// TestFirewallCompileStatus_501DegradesToUnverifiedOK is the regression
// test for T-3202's real-hardware finding: GET /nodes/{node}/firewall/
// status does not exist on real PVE (9.2.4/9.2.10 — pvesh ls only ever
// lists log/options/rules under /nodes/{node}/firewall) even though this
// codebase modeled it as a real endpoint. Every fw.*-touching changeset's
// apply used to hard-fail its fw_verify step (and therefore roll back)
// against any real node, regardless of whether pve-firewall actually
// compiled the change cleanly, because a 501 propagated as an ordinary
// error. pveGateway.FirewallCompileStatus must treat that one specific
// condition — PVE saying the route itself doesn't exist — as "verification
// unavailable" (OK, with an explanatory message), not a step failure.
func TestFirewallCompileStatus_501DegradesToUnverifiedOK(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"data":null,"message":"Method 'GET /nodes/pvecube/firewall/status' not implemented"}`))
	}))
	defer stub.Close()

	client, err := pve.New(pve.Config{APIURL: stub.URL, Auth: pve.AuthAPIToken, TokenValue: "vnprox@pve!daemon=00000000-0000-0000-0000-000000000000"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	gw := &pveGateway{client: client}

	status, err := gw.FirewallCompileStatus(context.Background(), "pvecube")
	if err != nil {
		t.Fatalf("FirewallCompileStatus returned an error for a 501, want a degraded-but-OK status: %v", err)
	}
	if !status.OK {
		t.Errorf("status.OK = false, want true (a 501 means unverifiable, not a bad compile)")
	}
	if status.Message == "" {
		t.Error("status.Message is empty, want an explanation that verification was unavailable")
	}
}

// TestFirewallCompileStatus_OtherErrorsStillFail proves the 501 handling
// above is narrowly scoped: a genuine reachability/auth failure (not "this
// route doesn't exist") must still propagate and still fail the fw_verify
// step, exactly as before this fix.
func TestFirewallCompileStatus_OtherErrorsStillFail(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"data":null,"message":"some other internal failure"}`))
	}))
	defer stub.Close()

	client, err := pve.New(pve.Config{APIURL: stub.URL, Auth: pve.AuthAPIToken, TokenValue: "vnprox@pve!daemon=00000000-0000-0000-0000-000000000000"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	gw := &pveGateway{client: client}

	if _, err := gw.FirewallCompileStatus(context.Background(), "pvecube"); err == nil {
		t.Fatal("FirewallCompileStatus returned no error for a genuine 500 failure, want it to still propagate")
	}
}

// TestRestoreFirewallScope_OmitsEmptyPolicyInOut is the regression test for
// T-3202 Scenario 5's live-hardware rollback failure: a node whose firewall
// options were never explicitly given an in/out policy reports GET .../
// firewall/options with no policy_in/policy_out field at all (real PVE
// 9.2.10, confirmed against pvecube — only digest/enable came back), so the
// pre-apply snapshot captures both as "". reconcileFwScope used to send
// PolicyIn/PolicyOut unconditionally whenever includeInOut was true,
// unlike the PolicyForward/LogLevelForward guard right below it — PUT
// .../firewall/options with policy_in="" is not a valid PVE enum value and
// real PVE rejected it with 400 "Parameter verification failed", which is
// exactly the error T-3202 Scenario 5 hit live: the DROP rule itself
// reverted, but the options restore step (and therefore the whole rollback
// step) reported "failed". This must not regress: an empty captured
// policy must be omitted from the restore PUT, exactly like PolicyForward/
// LogLevelForward already are.
func TestRestoreFirewallScope_OmitsEmptyPolicyInOut(t *testing.T) {
	var putBody map[string]any
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/firewall/rules"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/firewall/options"):
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &putBody); err != nil {
				t.Errorf("decoding PUT options body: %v", err)
			}
			_, _ = w.Write([]byte(`{"data":null}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"data":null,"message":"not found"}`))
		}
	}))
	defer stub.Close()

	client, err := pve.New(pve.Config{APIURL: stub.URL, Auth: pve.AuthAPIToken, TokenValue: "vnprox@pve!daemon=00000000-0000-0000-0000-000000000000"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	gw := &pveGateway{client: client}

	// The captured pre-apply snapshot of a node scope that never had an
	// explicit in/out policy: PolicyIn/PolicyOut come back empty.
	snapshot := `{"options":{"enable":false},"rules":[]}`
	target := inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pvecube", ID: "node"}
	if err := gw.RestoreFirewallScope(context.Background(), target, snapshot); err != nil {
		t.Fatalf("RestoreFirewallScope: %v", err)
	}
	if _, ok := putBody["policy_in"]; ok {
		t.Errorf("PUT options body sent policy_in = %#v for an empty captured policy, want it omitted", putBody["policy_in"])
	}
	if _, ok := putBody["policy_out"]; ok {
		t.Errorf("PUT options body sent policy_out = %#v for an empty captured policy, want it omitted", putBody["policy_out"])
	}
}
