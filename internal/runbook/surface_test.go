// SPDX-License-Identifier: Apache-2.0

package runbook

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
)

// forbiddenSeamMethods mirrors internal/plugin/surface_test.go's identical
// list — named here so this assertion fails loudly if a future edit adds
// any of them (by name or a differently-cased variant) to this package's
// change seam.
var forbiddenSeamMethods = []string{"apply", "confirm", "rollback", "discard", "approve"}

// TestChangeCreatorSeam_HasNoMutationMethods is T-4003 acceptance criterion
// 2: the change-engine interface this package holds (changeCreator) has no
// Apply/Confirm/Rollback-reachable method. Reflection-based, alongside
// stageonly.go's compile-time assertion of the same fact — the same
// belt-and-suspenders pairing internal/plugin/surface_test.go and
// internal/plugin/stager.go already use.
func TestChangeCreatorSeam_HasNoMutationMethods(t *testing.T) {
	seam := reflect.TypeOf((*changeCreator)(nil)).Elem()

	got := map[string]bool{}
	for i := 0; i < seam.NumMethod(); i++ {
		got[seam.Method(i).Name] = true
	}

	want := map[string]bool{"Create": true, "Validate": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changeCreator method set = %v, want exactly %v", keys(got), keys(want))
	}

	for name := range got {
		lower := strings.ToLower(name)
		for _, bad := range forbiddenSeamMethods {
			if strings.Contains(lower, bad) {
				t.Errorf("changeCreator exposes forbidden mutation method %q (matches %q)", name, bad)
			}
		}
	}
}

// TestChangeServiceHasMutationMethods is the control: *change.Service
// really does have Apply/Confirm/Rollback, proving changeCreator
// deliberately withholds methods that exist on the concrete service rather
// than the service simply lacking them.
func TestChangeServiceHasMutationMethods(t *testing.T) {
	svc := reflect.TypeOf((*change.Service)(nil))
	for _, m := range []string{"Apply", "Confirm", "Rollback"} {
		if _, ok := svc.MethodByName(m); !ok {
			t.Errorf("*change.Service unexpectedly has no %q method; this package's seam guarantee may be stale", m)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
