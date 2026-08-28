// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
)

// forbiddenSeamMethods are the mutation methods a plugin must never reach. Named
// here so the assertion fails loudly if a future edit adds any of them (by name
// or by a differently-cased variant) to the plugin-facing change seam — the same
// regression-guard style as T-1701 AC1's tool-registry enumeration.
var forbiddenSeamMethods = []string{"apply", "confirm", "rollback", "discard"}

// TestStagerSurface_HasNoMutationMethods is T-1702 AC3: the plugin-facing change
// seam (Stager) exposes exactly the stage-only pair and no Apply/Confirm/
// Rollback reachable method. It is a structural (reflection) assertion, not a
// prose claim.
func TestStagerSurface_HasNoMutationMethods(t *testing.T) {
	stagerType := reflect.TypeOf((*Stager)(nil)).Elem()

	got := map[string]bool{}
	for i := 0; i < stagerType.NumMethod(); i++ {
		got[stagerType.Method(i).Name] = true
	}

	// Exactly Create and Validate, nothing else.
	want := map[string]bool{"Create": true, "Validate": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Stager method set = %v, want exactly %v", keys(got), keys(want))
	}

	// Belt and suspenders: no method name matches a forbidden mutation verb.
	for name := range got {
		lower := strings.ToLower(name)
		for _, bad := range forbiddenSeamMethods {
			if strings.Contains(lower, bad) {
				t.Errorf("Stager exposes forbidden mutation method %q (matches %q)", name, bad)
			}
		}
	}
}

// TestChangeCreatorSeam_HasNoMutationMethods asserts the same for the internal
// changeCreator interface the SDK actually binds *change.Service through — the
// compiler-enforced narrowing that makes the guarantee real, not just a wrapper.
func TestChangeCreatorSeam_HasNoMutationMethods(t *testing.T) {
	seam := reflect.TypeOf((*changeCreator)(nil)).Elem()
	for i := 0; i < seam.NumMethod(); i++ {
		lower := strings.ToLower(seam.Method(i).Name)
		for _, bad := range forbiddenSeamMethods {
			if strings.Contains(lower, bad) {
				t.Errorf("changeCreator seam exposes forbidden mutation method %q", seam.Method(i).Name)
			}
		}
	}
}

// TestChangeServiceHasMutationMethods is the control: *change.Service really does
// have Apply/Confirm/Rollback — proving the seam above deliberately withholds
// methods that exist on the concrete service, rather than the service simply
// lacking them. If a refactor renamed these, this test flags that the surface
// test's guarantee needs re-checking.
func TestChangeServiceHasMutationMethods(t *testing.T) {
	svc := reflect.TypeOf((*change.Service)(nil))
	for _, m := range []string{"Apply", "Confirm", "Rollback"} {
		if _, ok := svc.MethodByName(m); !ok {
			t.Errorf("*change.Service unexpectedly has no %q method; the AC3 seam guarantee may be stale", m)
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
