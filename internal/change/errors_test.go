// SPDX-License-Identifier: Apache-2.0

package change

import (
	"strings"
	"testing"
)

func TestOpDecodeError_Error(t *testing.T) {
	err := &OpDecodeError{Path: "params.mtuu", Message: `unknown field "mtuu"`}
	got := err.Error()
	if !strings.Contains(got, "params.mtuu") || !strings.Contains(got, "mtuu") {
		t.Errorf("Error() = %q, want it to mention the path and message", got)
	}
}

func TestErrIllegalTransition_Error(t *testing.T) {
	err := &ErrIllegalTransition{From: StatusCommitted, To: StatusDraft}
	got := err.Error()
	if !strings.Contains(got, "committed") || !strings.Contains(got, "draft") {
		t.Errorf("Error() = %q, want it to mention both statuses", got)
	}
}
