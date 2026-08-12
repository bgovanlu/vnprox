package gitsync_test

// preview_seam_test.go pins the seam T-2702's pull-request body renders its
// post-apply projection through.
//
// T-2702 shipped with `Preview` nil because T-2605 did not exist yet, so every
// proposal said "Not available in this build". Now that it does exist, the ONE
// thing that could still silently keep that section empty is a signature drift
// between the seam and the method that satisfies it — a mismatch a nil
// interface field would hide until someone read a pull request. This assertion
// is a compile-time check that *change.Service is a PreviewSource, so wiring it
// in cmd/vnproxd is the only remaining step.

import (
	"reflect"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/gitsync"
)

// The compile-time half. If T-2605's method signature ever drifts from the
// seam, this line stops building.
var _ gitsync.PreviewSource = (*change.Service)(nil)

// The runtime half, so the pin is a named, greppable test rather than a bare
// declaration a cleanup could delete as unused.
func TestPreviewSource_IsSatisfiedByTheChangeService(t *testing.T) {
	seam := reflect.TypeOf((*gitsync.PreviewSource)(nil)).Elem()
	if svc := reflect.TypeOf((*change.Service)(nil)); !svc.Implements(seam) {
		t.Fatalf("%s does not satisfy gitsync.PreviewSource", svc)
	}
}
