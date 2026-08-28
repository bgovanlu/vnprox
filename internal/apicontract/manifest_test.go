// SPDX-License-Identifier: Apache-2.0

// T-2101: the "published contract artifact" half of this package.
//
// automationContractRoutes is a hand-curated, deliberately minimal mirror of
// docs/api.md's "Automation contract" route checklist (T-1106) — every route
// external tooling (terraform-provider-vnprox, ansible-collection-vnprox)
// depends on, and nothing else, exactly matching that section's own
// framing. It is not derived from internal/apidoc's full route walk (which
// describes the *entire* API surface, most of which carries no stability
// promise at all): the automation contract is a curated subset by design,
// and generating it from the full surface would silently widen the promise
// every time an unrelated route was added anywhere in the daemon.
//
// TestAutomationContractManifest_MatchesPublishedArtifact keeps
// docs/automation-contract.json (the versioned, machine-readable artifact a
// downstream repo's tooling can read without parsing markdown) in sync with
// the table below, the same -update convention golden_test.go's own
// assertGolden already established in this package:
//
//	go test ./internal/apicontract/... -run TestAutomationContractManifest -update
//
// (also `make contract-export`).
//
// TestAutomationContractManifest_RoutesExist drives every route in the list
// (except POST /tokens — see its comment) against the real in-process
// handlers and fails if any of them 404s, so a route rename/removal is
// caught here, in this repo's own `make check`, rather than silently
// breaking a downstream provider's next `terraform plan`.
package apicontract

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
)

// automationContractVersion tracks docs/api.md's "Automation contract"
// section's own stability declaration: the changeset API (every route
// below except GET /spec and POST /spec/import, which are T-1101's spec
// surface) is declared stable at v1.7. This value changes only when that
// section's own version sentence changes — never silently alongside an
// unrelated route addition.
const automationContractVersion = "v1.7"

type automationContractRoute struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Purpose string `json:"purpose"`
}

type automationContractManifest struct {
	Version string                    `json:"version"`
	Source  string                    `json:"source"`
	Routes  []automationContractRoute `json:"routes"`
}

var automationContractRoutes = []automationContractRoute{
	{Method: http.MethodPost, Path: "/api/v1/tokens", Purpose: "mint the bearer token automation authenticates with"},
	{Method: http.MethodGet, Path: "/api/v1/spec", Purpose: "render current cluster intent as specVersion: 1 YAML"},
	{Method: http.MethodPost, Path: "/api/v1/spec/import", Purpose: "diff the tracked spec against live state into a draft changeset + notInSpec (terraform plan's drift-detection primitive)"},
	{Method: http.MethodPost, Path: "/api/v1/changesets", Purpose: "create a draft changeset directly"},
	{Method: http.MethodGet, Path: "/api/v1/changesets/{id}", Purpose: "poll changeset status/findings"},
	{Method: http.MethodPost, Path: "/api/v1/changesets/{id}/validate", Purpose: "re-run validation (plan's \"does this still apply cleanly\" check)"},
	{Method: http.MethodGet, Path: "/api/v1/changesets/{id}/diff", Purpose: "rendered diff (plan's human-readable change summary)"},
	{Method: http.MethodPost, Path: "/api/v1/changesets/{id}/apply", Purpose: "apply (terraform apply's mutating step)"},
	{Method: http.MethodPost, Path: "/api/v1/changesets/{id}/confirm", Purpose: "commit-confirm within the window"},
	{Method: http.MethodPost, Path: "/api/v1/changesets/{id}/rollback", Purpose: "manual rollback"},
	{Method: http.MethodDelete, Path: "/api/v1/changesets/{id}", Purpose: "discard an unwanted draft"},
}

// TestAutomationContractManifest_MatchesPublishedArtifact is this task's
// golden-fixture check for docs/automation-contract.json, exactly mirroring
// golden_test.go's assertGolden convention (same -update flag).
func TestAutomationContractManifest_MatchesPublishedArtifact(t *testing.T) {
	manifest := automationContractManifest{
		Version: automationContractVersion,
		Source:  "docs/api.md#automation-contract-t-1106",
		Routes:  automationContractRoutes,
	}
	gotJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshaling the automation contract manifest: %v", err)
	}
	gotJSON = append(gotJSON, '\n')

	path := filepath.Join("..", "..", "docs", "automation-contract.json")
	if *updateGolden {
		if writeErr := os.WriteFile(path, gotJSON, 0o644); writeErr != nil {
			t.Fatalf("writing %s: %v", path, writeErr)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s (run `make contract-export`, or `go test ./internal/apicontract/... -update`, to create it): %v", path, err)
	}
	if !bytes.Equal(want, gotJSON) {
		t.Errorf("docs/automation-contract.json is stale relative to automationContractRoutes in manifest_test.go. "+
			"Run `make contract-export` after confirming the change is intended.\n--- got ---\n%s\n--- want ---\n%s",
			gotJSON, want)
	}
}

// TestAutomationContractManifest_RoutesExist drives every route in
// automationContractRoutes (except POST /tokens, see below) against the
// real in-process handlers and fails if any answers 404 — the "route
// missing entirely" signal a rename or removal produces, distinct from the
// wire-shape regressions the four flow tests' golden fixtures already pin.
//
// POST /tokens is not dialed here: the in-process harness's login is
// deliberately stubbed (harness_test.go's errNoLoginInContractSuite), so
// there is no session to call it with. conformance_external_test.go's
// bootstrap calls it for real, against a real out-of-process daemon, which
// is the one place in this package that can — see that file's doc comment.
func TestAutomationContractManifest_RoutesExist(t *testing.T) {
	h := newContractHarness(t, fixtureSingleNode)
	token := h.mintToken("tok-manifest", "netRead", "netWrite")

	assertExists := func(t *testing.T, method, path string, body []byte) *http.Response {
		t.Helper()
		resp := h.do(h.newRequest(method, path, token, body))
		// 404: the path pattern itself is unregistered. 405: the pattern is
		// registered for other methods but not this one (chi's behavior when
		// a sibling method shares the pattern, e.g. DELETE removed from a
		// path GET/PUT still serve) — both mean "this route is not part of
		// the daemon's contract" and are exactly what a route rename/removal
		// produces; only 405 is caught by mutation-testing this test against
		// a real removed method (see this file's package doc).
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status %d — route missing; docs/automation-contract.json/docs/api.md's Automation contract section promises it exists", method, path, resp.StatusCode)
		}
		return resp
	}

	specResp := assertExists(t, http.MethodGet, "/api/v1/spec", nil)
	var exported specExportResponse
	decodeJSON(t, specResp, &exported)

	assertExists(t, http.MethodPost, "/api/v1/spec/import", mustJSON(t, map[string]string{"content": exported.Content}))

	createResp := assertExists(t, http.MethodPost, "/api/v1/changesets", mustJSON(t, createChangesetRequest{
		Title: "apicontract: manifest route-existence smoke",
		Ops:   []change.Op{bridgeCreateOp(h.localNode, "vmbr52")},
	}))
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /changesets: status = %d, want 201", createResp.StatusCode)
	}
	var created changesetResponse
	decodeJSON(t, createResp, &created)
	id := created.ID

	assertExists(t, http.MethodGet, "/api/v1/changesets/"+id, nil)
	assertExists(t, http.MethodPost, "/api/v1/changesets/"+id+"/validate", nil)
	assertExists(t, http.MethodGet, "/api/v1/changesets/"+id+"/diff", nil)

	applyResp := assertExists(t, http.MethodPost, "/api/v1/changesets/"+id+"/apply", mustJSON(t, map[string]any{}))
	if applyResp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /changesets/%s/apply: status = %d, want 202", id, applyResp.StatusCode)
	}
	assertExists(t, http.MethodPost, "/api/v1/changesets/"+id+"/confirm", nil)

	// DELETE needs its own fresh draft — the one above is committed now.
	discardResp := h.do(h.newRequest(http.MethodPost, "/api/v1/changesets", token, mustJSON(t, createChangesetRequest{
		Title: "apicontract: manifest route-existence smoke (discard)",
		Ops:   []change.Op{bridgeCreateOp(h.localNode, "vmbr53")},
	})))
	var discard changesetResponse
	decodeJSON(t, discardResp, &discard)
	assertExists(t, http.MethodDelete, "/api/v1/changesets/"+discard.ID, nil)

	// rollback needs its own applied-but-not-confirmed changeset.
	rollbackCandidateResp := h.do(h.newRequest(http.MethodPost, "/api/v1/changesets", token, mustJSON(t, createChangesetRequest{
		Title: "apicontract: manifest route-existence smoke (rollback)",
		Ops:   []change.Op{bridgeCreateOp(h.localNode, "vmbr54")},
	})))
	var rollbackCandidate changesetResponse
	decodeJSON(t, rollbackCandidateResp, &rollbackCandidate)
	h.do(h.newRequest(http.MethodPost, "/api/v1/changesets/"+rollbackCandidate.ID+"/apply", token, mustJSON(t, map[string]any{})))
	assertExists(t, http.MethodPost, "/api/v1/changesets/"+rollbackCandidate.ID+"/rollback", nil)
}
