package apicontract

import (
	"net/http"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
)

// TestApplyThenRollback is T-1106 acceptance criterion 1's third required
// flow: apply a changeset, then explicitly roll it back (the manual
// POST .../rollback path documented in docs/api.md, distinct from the
// commit-confirm-timeout auto-rollback T-205/T-304 already cover
// elsewhere) — status ends rolled_back, and a subsequent confirm is
// rejected since the changeset is no longer awaiting_confirm.
func TestApplyThenRollback(t *testing.T) {
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
			token := h.mintToken("tok-rollback", "netRead", "netWrite")

			createBody := mustJSON(t, createChangesetRequest{
				Title: "apicontract: rollback candidate",
				Ops:   []change.Op{bridgeCreateOp(fx.node, "vmbr51")},
			})
			createResp := h.do(h.newRequest(http.MethodPost, "/api/v1/changesets", token, createBody))
			if createResp.StatusCode != http.StatusCreated {
				t.Fatalf("POST /changesets: status = %d, want 201", createResp.StatusCode)
			}
			var created changesetResponse
			decodeJSON(t, createResp, &created)
			id := created.ID

			applyResp := h.do(h.newRequest(http.MethodPost, "/api/v1/changesets/"+id+"/apply", token, mustJSON(t, map[string]any{})))
			if applyResp.StatusCode != http.StatusAccepted {
				t.Fatalf("POST /changesets/%s/apply: status = %d, want 202", id, applyResp.StatusCode)
			}
			var applied changesetResponse
			decodeJSON(t, applyResp, &applied)
			if applied.Status != string(change.StatusAwaitingConfirm) {
				t.Fatalf("status after apply = %q, want awaiting_confirm", applied.Status)
			}

			rollbackResp := h.do(h.newRequest(http.MethodPost, "/api/v1/changesets/"+id+"/rollback", token, nil))
			if rollbackResp.StatusCode != http.StatusOK {
				t.Fatalf("POST /changesets/%s/rollback: status = %d, want 200", id, rollbackResp.StatusCode)
			}
			var rolledBack changesetResponse
			decodeJSON(t, rollbackResp, &rolledBack)
			if rolledBack.Status != string(change.StatusRolledBack) {
				t.Fatalf("status after rollback = %q, want rolled_back", rolledBack.Status)
			}
			assertGolden(t, "rollback_"+fx.name+"_rollback", redactedChangeset(rolledBack))

			// The changeset is terminal now — a stray confirm must not
			// resurrect it into committed.
			confirmResp := h.do(h.newRequest(http.MethodPost, "/api/v1/changesets/"+id+"/confirm", token, nil))
			if confirmResp.StatusCode == http.StatusOK {
				t.Fatalf("POST /changesets/%s/confirm after rollback: status = 200, want a rejection (terminal state)", id)
			}
		})
	}
}
