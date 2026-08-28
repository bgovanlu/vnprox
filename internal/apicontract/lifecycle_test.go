// SPDX-License-Identifier: Apache-2.0

package apicontract

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestLifecycle_TokenAuthed_CreateValidateApplyConfirm is T-1106 acceptance
// criterion 1's first flow: a bearer token with {netRead, netWrite} scopes
// drives an ordinary changeset all the way from draft to committed —
// create -> validate -> apply -> confirm — over HTTP, against both
// single-node and three-node-vlan, the two fixtures the card names. Every
// response is checked against a golden fixture captured from these same
// real handlers (see golden_test.go's -update flag), so a schema
// regression in internal/api/changesets.go fails here, in this repo's own
// `make check`, not downstream in an external Terraform provider.
func TestLifecycle_TokenAuthed_CreateValidateApplyConfirm(t *testing.T) {
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
			token := h.mintToken("tok-lifecycle", "netRead", "netWrite")

			// A token scoped only netRead+automation must not be able to
			// create a changeset (T-1104 AC1's "same token 403s without
			// netWrite" — exercised at the route this suite actually cares
			// about, the changeset write path).
			roToken := h.mintToken("tok-lifecycle-ro", "netRead")
			createBody := mustJSON(t, createChangesetRequest{
				Title: "apicontract: add vmbr50",
				Ops:   []change.Op{bridgeCreateOp(fx.node, "vmbr50")},
			})
			roReq := h.newRequest(http.MethodPost, "/api/v1/changesets", roToken, createBody)
			roResp := h.do(roReq)
			if roResp.StatusCode != http.StatusForbidden {
				t.Fatalf("netRead-only token POST /changesets: status = %d, want 403", roResp.StatusCode)
			}

			// --- create (draft, auto-validated per T-201) ---
			createReq := h.newRequest(http.MethodPost, "/api/v1/changesets", token, createBody)
			createResp := h.do(createReq)
			if createResp.StatusCode != http.StatusCreated {
				t.Fatalf("POST /changesets: status = %d, want 201", createResp.StatusCode)
			}
			var created changesetResponse
			decodeJSON(t, createResp, &created)
			if created.Status != string(change.StatusDraft) && created.Status != string(change.StatusValidated) {
				t.Fatalf("status after create = %q, want draft or validated", created.Status)
			}
			for _, f := range created.Findings {
				if f.Severity == change.SeverityError {
					t.Fatalf("unexpected blocking finding on a bare bridge.create: %+v", f)
				}
			}
			assertGolden(t, "lifecycle_"+fx.name+"_create", redactedChangeset(created))

			id := created.ID

			// --- validate ---
			validateReq := h.newRequest(http.MethodPost, "/api/v1/changesets/"+id+"/validate", token, nil)
			validateResp := h.do(validateReq)
			if validateResp.StatusCode != http.StatusOK {
				t.Fatalf("POST /changesets/%s/validate: status = %d, want 200", id, validateResp.StatusCode)
			}
			var validated changesetResponse
			decodeJSON(t, validateResp, &validated)
			if validated.Status != string(change.StatusValidated) {
				t.Fatalf("status after validate = %q, want validated", validated.Status)
			}
			assertGolden(t, "lifecycle_"+fx.name+"_validate", redactedChangeset(validated))

			// --- apply ---
			applyReq := h.newRequest(http.MethodPost, "/api/v1/changesets/"+id+"/apply", token, mustJSON(t, map[string]any{}))
			applyResp := h.do(applyReq)
			if applyResp.StatusCode != http.StatusAccepted {
				t.Fatalf("POST /changesets/%s/apply: status = %d, want 202", id, applyResp.StatusCode)
			}
			var applied changesetResponse
			decodeJSON(t, applyResp, &applied)
			if applied.Status != string(change.StatusAwaitingConfirm) {
				t.Fatalf("status after apply = %q, want awaiting_confirm", applied.Status)
			}
			if applied.ConfirmDeadline == nil {
				t.Fatal("confirmDeadline not set after apply")
			}
			assertGolden(t, "lifecycle_"+fx.name+"_apply", redactedChangeset(applied))

			// --- confirm ---
			confirmReq := h.newRequest(http.MethodPost, "/api/v1/changesets/"+id+"/confirm", token, nil)
			confirmResp := h.do(confirmReq)
			if confirmResp.StatusCode != http.StatusOK {
				t.Fatalf("POST /changesets/%s/confirm: status = %d, want 200", id, confirmResp.StatusCode)
			}
			var committed changesetResponse
			decodeJSON(t, confirmResp, &committed)
			if committed.Status != string(change.StatusCommitted) {
				t.Fatalf("status after confirm = %q, want committed", committed.Status)
			}
			if committed.ConfirmDeadline != nil {
				t.Error("confirmDeadline still set after confirm")
			}
			assertGolden(t, "lifecycle_"+fx.name+"_confirm", redactedChangeset(committed))
		})
	}
}

// --- shared op/request builders + decode helpers ---------------------

type createChangesetRequest struct {
	Title string      `json:"title"`
	Ops   []change.Op `json:"ops"`
}

func bridgeCreateOp(node, name string) change.Op {
	return change.Op{
		Type:   change.OpBridgeCreate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name},
		Params: &change.BridgeCreateParams{Comments: "created by internal/apicontract"},
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling request body: %v", err)
	}
	return b
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decoding %s response: %v", resp.Request.URL.Path, err)
	}
}
