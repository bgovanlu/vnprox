// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"reflect"
	"strings"
	"testing"
)

// TestClient_HasNoApplyMethod is this module's structural half of T-4001's
// stage-only contract (this file's package doc comment in client.go): a
// reflection check over *client's method set, so a future PR that adds an
// ApplyChangeset/ConfirmChangeset/RollbackChangeset method here fails a
// named test in CI instead of silently widening what a `terraform apply`
// can do. This is the module-boundary equivalent of
// internal/mcp/stageonly.go's compile-time assertion in the main module —
// a separate Go module has no interface-satisfaction trick to lean on
// (there is no shared interface across the module boundary), so this test
// is the enforcement mechanism here.
func TestClient_HasNoApplyMethod(t *testing.T) {
	forbidden := []string{"apply", "confirm", "rollback", "approve", "reject"}

	typ := reflect.TypeOf(&client{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := strings.ToLower(typ.Method(i).Name)
		for _, f := range forbidden {
			if strings.Contains(name, f) {
				t.Errorf(
					"*client has method %q, whose name contains forbidden substring %q — "+
						"this provider's resources must never reach past POST /changesets/{id}/validate "+
						"(see client.go's package doc comment and README.md's \"The stage-only contract\" section)",
					typ.Method(i).Name, f,
				)
			}
		}
	}
}

// TestChangesetEditable pins the draft/validated eligibility check every
// resource's Update/Delete branches on (changesetEditable in
// resource_bridge.go) to the exact two statuses change.Changeset.Editable()
// recognizes in the main module (internal/change/changeset.go) — this test
// exists so a change to that main-module method's status set is caught here
// too, since this file cannot import it to share the logic directly (see
// this package's module-boundary doc comments).
func TestChangesetEditable(t *testing.T) {
	cases := map[string]bool{
		"draft":     true,
		"validated": true,
		"applying":  false,
		"applied":   false,
		"":          false,
	}
	for status, want := range cases {
		if got := changesetEditable(status); got != want {
			t.Errorf("changesetEditable(%q) = %v, want %v", status, got, want)
		}
	}
}

// TestLivenessGate_CatchesEveryStagedStatus pins ADR-0012's published gate
// pattern to the statuses a stage-only apply can actually leave behind.
//
// The ADR requires an integration to "publish a gate pattern its users can
// adopt to make a pipeline honest, and keep that pattern tested rather than
// merely written down". This is that test. The README documents:
//
//	condition = vnprox_bridge.example.changeset_status == "applied"
//
// so the property to hold is: **for every status a `terraform apply` can leave
// this resource in, that condition must be FALSE**, or the gate passes over an
// unconverged network — the exact failure ADR-0012 exists to prevent.
//
// Deliberately a unit test and not an addition to the acceptance suite.
// TestAccResources_StageOnly needs TF_ACC and a running daemon, so it does not
// run in ordinary CI; a gate whose correctness is only checked when someone
// remembers to stand up a stack is the shape of guard this project keeps
// finding green while it measures nothing.
func TestLivenessGate_CatchesEveryStagedStatus(t *testing.T) {
	// The gate, transcribed from the README's `check` block. If the README's
	// condition changes, this must change with it — which is the point of
	// writing it out rather than calling a shared helper: there IS no shared
	// helper, because the real gate lives in a user's HCL.
	gatePasses := func(changesetStatus string) bool { return changesetStatus == "applied" }

	// Every status a stage-only apply can leave behind. "draft" and
	// "validated" are what the provider actually produces (see
	// TestAccResources_StageOnly, which asserts the changeset never advances
	// past validated); the rest are included because a human applying it
	// out-of-band is the normal path, and the gate must not pass early on any
	// intermediate state either.
	staged := []string{"draft", "validated", "applying", "awaiting_confirm", "rolled_back", ""}
	for _, status := range staged {
		if gatePasses(status) {
			t.Errorf("gate passed on changeset_status %q — a pipeline would report a converged network over a staged changeset", status)
		}
	}

	// And the one case it must pass, or the gate is unsatisfiable and users
	// will delete it rather than debug it.
	if !gatePasses("applied") {
		t.Error(`gate rejected changeset_status "applied" — the gate can never be satisfied`)
	}
}
