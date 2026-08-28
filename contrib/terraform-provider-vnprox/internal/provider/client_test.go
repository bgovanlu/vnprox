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
