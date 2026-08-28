// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
)

// ackTestRouter's clock is fixed at this instant (findings_ack_test.go), so
// a finding's acknowledgement floor can be placed on either side of it and
// the decision is provable by choosing the floor rather than by waiting.
const ackTestNow = int64(1_700_000_000)

// AC5, at the HTTP layer: `POST /findings/{id}/ack` on a finding whose
// acknowledgement floor has not been reached is refused 409 ack_too_early,
// and the refusal says when it becomes ackable. The control leg — an
// otherwise identical finding whose floor has passed, acked successfully in
// the same request sequence against the same router and clock — is what
// makes the refusal evidence of the floor rather than of the route being
// broken.
func TestFindingAck_RefusedBeforeTheAcknowledgementFloor(t *testing.T) {
	unackable := findings.Finding{
		ID: "health:change_break_glass|cs1", Source: findings.SourceHealth,
		Check: "change_break_glass", Severity: findings.SeverityError,
		Detail: "alice applied changeset cs1 under emergency break-glass", Nodes: []string{},
		AckableAt: ackTestNow + 3600,
	}
	ackable := findings.Finding{
		ID: "health:change_break_glass|cs0", Source: findings.SourceHealth,
		Check: "change_break_glass", Severity: findings.SeverityError,
		Detail: "alice applied changeset cs0 under emergency break-glass", Nodes: []string{},
		AckableAt: ackTestNow - 1,
	}
	st := newAckTestStore()
	r := ackTestRouter(t, st, &recordingAudit{},
		fakeFindingsService{findings: []findings.Finding{unackable, ackable}}, nil)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/"+unackable.ID+"/ack",
		map[string]any{"reason": "seen it, will review"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("ack status = %d, want 409, body: %s", rec.Code, rec.Body.String())
	}
	code, details := decodeAPIError(t, rec)
	if code != "ack_too_early" {
		t.Fatalf("error code = %q, want ack_too_early", code)
	}
	if got, _ := details["ackableAt"].(float64); int64(got) != unackable.AckableAt {
		t.Errorf("details.ackableAt = %v, want %d", details["ackableAt"], unackable.AckableAt)
	}
	if len(st.rows) != 0 {
		t.Fatalf("a refused ack wrote %d rows", len(st.rows))
	}

	// CONTROL LEG: same route, same router, same clock — a finding whose
	// floor has passed acks normally.
	rec = doJSON(t, r, http.MethodPost, "/api/v1/findings/"+ackable.ID+"/ack",
		map[string]any{"reason": "reviewed at the postmortem"})
	if rec.Code != http.StatusOK {
		t.Fatalf("ack of a finding past its floor = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if _, ok := st.rows[ackable.ID]; !ok {
		t.Fatal("the accepted ack wrote no row")
	}
}
