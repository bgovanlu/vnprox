package ifaces

import (
	"fmt"
	"os"
	"testing"
)

// fatalRecorder wraps a real *testing.T but records Fatalf instead of
// failing, so a test can assert that a helper under test fatals.
type fatalRecorder struct {
	testing.TB
	msg     string
	fataled bool
}

func (r *fatalRecorder) Helper() {}

func (r *fatalRecorder) Fatalf(format string, args ...any) {
	r.fataled = true
	r.msg = fmt.Sprintf(format, args...)
	// Do not Goexit: checkGolden's missing-golden branch returns via
	// Fatalf as its last action on every path we exercise here, so simply
	// recording is enough — and it keeps this test single-goroutine.
}

func (r *fatalRecorder) Errorf(format string, args ...any) {
	r.fataled = true
	r.msg = fmt.Sprintf(format, args...)
}

func (r *fatalRecorder) Logf(string, ...any) {}

// TestCheckGolden_MissingGoldenFails is audit finding F-21's regression
// test: a missing (deleted/renamed) golden file must fail the test, not
// silently bootstrap-and-pass, and nothing may be written to disk unless
// regeneration was explicitly requested.
func TestCheckGolden_MissingGoldenFails(t *testing.T) {
	if goldenUpdateRequested() {
		t.Skip("golden regeneration explicitly requested; missing-golden failure semantics do not apply")
	}
	const name = "zz-nonexistent-for-f21-test.interfaces"
	path := "testdata/golden/" + name
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("precondition: %s unexpectedly exists", path)
	}

	rec := &fatalRecorder{TB: t}
	checkGolden(rec, name, "any content\n")

	if !rec.fataled {
		t.Fatal("checkGolden with a missing golden file must fail the test, got a pass")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		_ = os.Remove(path)
		t.Fatalf("checkGolden must not bootstrap a missing golden without -update/VNPROX_UPDATE_GOLDEN=1 (stat err: %v)", err)
	}
}
