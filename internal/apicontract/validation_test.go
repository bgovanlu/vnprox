package apicontract

import (
	"net/http"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestValidationErrorPath_BlocksApply is T-1106 acceptance criterion 1's
// second required flow: a changeset whose ops fail validation must never
// reach applying/committed — it comes back with a blocking error finding,
// stuck at draft, and a subsequent POST .../apply is rejected outright.
// The op used (bridge.create targeting an already-existing bridge name,
// vmbr0, present in every fixture) exercises internal/change's real
// referential.already_exists check (validate_referential.go) against the
// real live inventory this harness populated from pvemock — not a
// hand-asserted fake finding.
func TestValidationErrorPath_BlocksApply(t *testing.T) {
	for _, fx := range []struct {
		name string
		path string
		node string
	}{
		{name: "single-node", path: fixtureSingleNode, node: "pve1"},
		{name: "three-node-vlan", path: fixtureThreeNode, node: "pve1"},
	} {
		t.Run(fx.name, func(t *testing.T) {
			h := newContractHarness(t, fx.path)
			token := h.mintToken("tok-validation", "netRead", "netWrite")

			body := mustJSON(t, createChangesetRequest{
				Title: "apicontract: duplicate bridge name",
				Ops: []change.Op{{
					Type:   change.OpBridgeCreate,
					Target: inventory.Ref{Kind: inventory.KindBridge, Node: fx.node, ID: "vmbr0"},
					Params: &change.BridgeCreateParams{Comments: "should fail: vmbr0 already exists"},
				}},
			})
			createReq := h.newRequest(http.MethodPost, "/api/v1/changesets", token, body)
			createResp := h.do(createReq)
			if createResp.StatusCode != http.StatusCreated {
				t.Fatalf("POST /changesets: status = %d, want 201", createResp.StatusCode)
			}
			var created changesetResponse
			decodeJSON(t, createResp, &created)

			if created.Status != string(change.StatusDraft) {
				t.Fatalf("status = %q, want draft (a blocking finding must never auto-promote to validated)", created.Status)
			}
			foundCode := ""
			for _, f := range created.Findings {
				if f.Severity == change.SeverityError {
					foundCode = f.Code
				}
			}
			if foundCode != "referential.already_exists" {
				t.Fatalf("expected a referential.already_exists error finding, findings = %+v", created.Findings)
			}
			assertGolden(t, "validation_"+fx.name+"_create", redactedChangeset(created))

			// The blocking finding must also carry through an explicit
			// re-validate call.
			validateReq := h.newRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/validate", token, nil)
			validateResp := h.do(validateReq)
			if validateResp.StatusCode != http.StatusOK {
				t.Fatalf("POST /changesets/%s/validate: status = %d, want 200", created.ID, validateResp.StatusCode)
			}
			var validated changesetResponse
			decodeJSON(t, validateResp, &validated)
			if validated.Status != string(change.StatusDraft) {
				t.Fatalf("status after validate = %q, want draft (still blocked)", validated.Status)
			}

			// apply must be rejected outright — the changeset never
			// transitions to applying/awaiting_confirm.
			applyReq := h.newRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/apply", token, mustJSON(t, map[string]any{}))
			applyResp := h.do(applyReq)
			if applyResp.StatusCode == http.StatusAccepted {
				t.Fatalf("POST /changesets/%s/apply on a blocked changeset: status = 202, want a rejection", created.ID)
			}
			var applyErr errorResponse
			decodeJSON(t, applyResp, &applyErr)
			if applyErr.Error.Code == "" {
				t.Fatalf("apply rejection has no error.code: status=%d body=%+v", applyResp.StatusCode, applyErr)
			}
		})
	}
}
