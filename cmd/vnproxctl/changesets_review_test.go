// changesets_review_test.go proves T-2003 acceptance criterion 2's second
// required bypass: `vnproxctl remote changesets apply` — a non-browser,
// non-UI caller entirely outside the SPA — is refused an unapproved
// changeset's apply exactly like a direct HTTP call is (changesets_test.go's
// TestRunRemoteChangesetsList_JSONMatchesAPIShape and friends already prove
// this package's HTTP plumbing against a scripted fake daemon; this file
// goes one step further and points vnproxctl at a REAL *change.Service, so
// the refusal these tests assert on is genuinely computed by the same
// approval gate (internal/change/apply.go's beginApply) the HTTP layer and
// the service layer's own tests exercise — not a canned string this test
// file made up).
//
// The HTTP surface here is a small hand-rolled mux, not the full
// internal/api router (which requires session/bearer-token auth machinery
// this package has no reason to stand up) — but the two routes it serves
// (`POST /changesets`, `POST /changesets/{id}/apply`) call straight into the
// real change.Service and translate its real error value into the exact
// documented error envelope (`{"error":{"code":...}}`), so what vnproxctl
// parses and reports is the daemon's real answer, not a stand-in.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

// panicNodeAgent models a NodeAgent that must never be touched — if the
// approval gate ever let apply reach the node layer, this panics the test
// immediately rather than letting a weaker assertion possibly miss it.
type panicNodeAgent struct{}

func (panicNodeAgent) ReadInterfaces(context.Context, string) (string, error) {
	panic("panicNodeAgent: ReadInterfaces called — apply should have been refused before any node mutation")
}
func (panicNodeAgent) StageInterfaces(context.Context, string, string) error {
	panic("panicNodeAgent: StageInterfaces called — apply should have been refused before any node mutation")
}
func (panicNodeAgent) ReloadInterfaces(context.Context, string) error {
	panic("panicNodeAgent: ReloadInterfaces called — apply should have been refused before any node mutation")
}
func (panicNodeAgent) DiscardStaged(context.Context, string) error {
	panic("panicNodeAgent: DiscardStaged called — apply should have been refused before any node mutation")
}

// stubNodeAgent is a trivial, always-succeeding NodeAgent — used only by
// this file's control test (proving apply genuinely succeeds with no
// approval policy configured, as a sanity check that the refusal the other
// test asserts on is really the approval gate and not some unrelated
// harness failure).
type stubNodeAgent struct{}

func (stubNodeAgent) ReadInterfaces(context.Context, string) (string, error) {
	return "auto lo\niface lo inet loopback\n", nil
}
func (stubNodeAgent) StageInterfaces(context.Context, string, string) error { return nil }
func (stubNodeAgent) ReloadInterfaces(context.Context, string) error        { return nil }
func (stubNodeAgent) DiscardStaged(context.Context, string) error           { return nil }

// newReviewTestService builds a real, apply-configured *change.Service with
// T-2003's review surface wired.
func newReviewTestService(t *testing.T, approval change.ApprovalConfig, nodes change.NodeAgent) *change.Service {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc, err := change.NewService(change.Config{
		Changesets: store.NewChangesetRepo(db),
		Audit:      store.NewAuditRepo(db),
		Snapshots:  store.NewSnapshotRepo(db),
		Blobs:      store.NewBlobRepo(db),
		Nodes:      nodes,
		Comments:   store.NewChangesetCommentRepo(db),
		Approvals:  store.NewChangesetApprovalRepo(db),
		Approval:   approval,
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc
}

// reviewGateHandler serves just enough of docs/api.md's changesets surface
// (create + apply) for vnproxctl's remote-changesets subcommands to drive,
// backed by a real change.Service.
func reviewGateHandler(t *testing.T, svc *change.Service) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			writeAPIError(w, http.StatusUnauthorized, "not_authenticated", "missing/invalid bearer token")
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/changesets":
			var req struct {
				Title string      `json:"title"`
				Ops   []change.Op `json:"ops"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeAPIError(w, http.StatusBadRequest, "validation_failed", err.Error())
				return
			}
			cs, err := svc.Create(r.Context(), "root@pam", req.Title, req.Ops)
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
				return
			}
			writeChangesetWire(w, http.StatusCreated, cs)

		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/apply"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/changesets/"), "/apply")
			cs, err := svc.Apply(r.Context(), id, "root@pam", nil, 0)
			if err != nil {
				writeApplyErrorForTest(w, err)
				return
			}
			writeChangesetWire(w, http.StatusAccepted, cs)

		default:
			writeAPIError(w, http.StatusNotFound, "not_found", "no such route")
		}
	}
}

func writeChangesetWire(w http.ResponseWriter, status int, cs change.Changeset) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": cs.ID, "title": cs.Title, "author": cs.Author, "status": string(cs.Status),
		"ops": []any{}, "findings": []any{}, "createdAt": cs.CreatedAt, "updatedAt": cs.UpdatedAt,
		"touchesMgmtPath": false,
	})
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message}})
}

// writeApplyErrorForTest maps change.Service.Apply's real error values to
// docs/api.md's error envelope — the same mapping internal/api/changesets.go's
// writeApplyError performs in production, reproduced narrowly here (this
// package cannot import internal/api's unexported handler) for exactly the
// one error family this test cares about, T-2003's approval gate.
func writeApplyErrorForTest(w http.ResponseWriter, err error) {
	var approvalRequired *change.ErrApprovalRequired
	if errors.As(err, &approvalRequired) {
		writeAPIError(w, http.StatusUnprocessableEntity, "approval_required", err.Error())
		return
	}
	writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
}

// createViaCLI drives `vnproxctl remote changesets create` end to end
// (no hand-crafted HTTP on this side of the test either) and returns the
// created changeset's id, parsed from the CLI's own `-o json` stdout.
func createViaCLI(t *testing.T, url string) string {
	t.Helper()
	dir := t.TempDir()
	specFile := filepath.Join(dir, "spec.json")
	spec := `{"title":"add vmbr1","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr1","params":{}}]}`
	if err := os.WriteFile(specFile, []byte(spec), 0o600); err != nil {
		t.Fatalf("writing spec file: %v", err)
	}

	var stdout, stderr strings.Builder
	code := run([]string{"remote", "changesets", "create", "--url", url, "--token", "tok", "-f", specFile, "-o", "json"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("vnproxctl remote changesets create: exit %d, stderr: %s", code, stderr.String())
	}
	var out changesetWire
	if err := json.Unmarshal([]byte(stdout.String()), &out); err != nil {
		t.Fatalf("decoding create stdout: %v (%s)", err, stdout.String())
	}
	if out.ID == "" {
		t.Fatalf("created changeset has no id: %s", stdout.String())
	}
	return out.ID
}

// TestRemoteChangesetsApply_ApprovalRequired_RefusedByVnproxctl is T-2003
// acceptance criterion 2's `vnproxctl` bypass: staging a changeset and
// applying it — both entirely through the vnproxctl binary's own command
// line, never touching the web UI — is refused with the daemon's real
// approval_required error, mapped to vnproxctl's documented ExitPending exit
// code (422 -> ExitPending, exitcodes.go) and reported on stderr.
// panicNodeAgent (above) proves this refusal happens before any node
// mutation is even attempted: a gate that let this apply through would
// panic the test process, not just fail an assertion.
func TestRemoteChangesetsApply_ApprovalRequired_RefusedByVnproxctl(t *testing.T) {
	svc := newReviewTestService(t, change.ApprovalConfig{Required: true, AllowSelfApproval: true}, panicNodeAgent{})
	srv := newFakeVnproxd(t, reviewGateHandler(t, svc))

	id := createViaCLI(t, srv.URL)

	var stdout, stderr strings.Builder
	code := run([]string{"remote", "changesets", "apply", "--url", srv.URL, "--token", "tok", id}, &stdout, &stderr)
	if code != ExitPending {
		t.Fatalf("vnproxctl remote changesets apply: exit = %d, want ExitPending (422 approval_required); stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "approval_required") {
		t.Errorf("stderr = %q, want it to mention approval_required", stderr.String())
	}
}

// TestRemoteChangesetsApply_ApprovalRequired_NotConfigured_AppliesFine is
// the control: with no approval policy configured, the identical vnproxctl
// invocation succeeds — proving the refusal above is the gate, not some
// other unrelated failure in this test's harness.
func TestRemoteChangesetsApply_ApprovalRequired_NotConfigured_AppliesFine(t *testing.T) {
	svc := newReviewTestService(t, change.ApprovalConfig{}, stubNodeAgent{})
	srv := newFakeVnproxd(t, reviewGateHandler(t, svc))

	id := createViaCLI(t, srv.URL)

	var stdout, stderr strings.Builder
	code := run([]string{"remote", "changesets", "apply", "--url", srv.URL, "--token", "tok", id}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("vnproxctl remote changesets apply (no approval policy): exit = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
