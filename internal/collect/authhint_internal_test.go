package collect

// TestRecordResult_PVEAuthFailureLogsRegenerationHint covers the daemon-side
// half of planning/reports/blocked-validation.md §2.2's fix: a bare "poll
// failed" line does not tell an operator that a 401 specifically means the
// configured token was rejected, which — for vnprox@pve!daemon, a single
// cluster-wide credential — is very likely to mean it was regenerated on
// another cluster node rather than "PVE is unreachable". recordResult is the
// one place every poll loop's result (pve/host/lldp) already funnels
// through (see its own doc comment), so the extra hint is asserted here
// directly rather than through a full pvePollAll fixture round trip.

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/pve"
)

func newAuthHintTestCollector(buf *bytes.Buffer) *Collector {
	logger := slog.New(slog.NewTextHandler(buf, nil))
	return &Collector{
		log:    logger,
		status: map[string]*sourceState{"pve": {}},
	}
}

func TestRecordResult_PVEAuthFailureLogsRegenerationHint(t *testing.T) {
	var buf bytes.Buffer
	c := newAuthHintTestCollector(&buf)

	c.recordResult("pve", time.Now(), &pve.ErrPVEAuth{Message: "invalid token value!"})

	out := buf.String()
	if !strings.Contains(out, "collect: poll failed") {
		t.Errorf("expected the usual poll-failed line, got: %s", out)
	}
	if !strings.Contains(out, "regenerated on another cluster node") {
		t.Errorf("expected a regeneration hint for a PVE auth failure, got: %s", out)
	}
}

func TestRecordResult_NonAuthFailureOmitsRegenerationHint(t *testing.T) {
	var buf bytes.Buffer
	c := newAuthHintTestCollector(&buf)

	c.recordResult("pve", time.Now(), &pve.ErrPVETransport{})

	out := buf.String()
	if !strings.Contains(out, "collect: poll failed") {
		t.Errorf("expected the usual poll-failed line, got: %s", out)
	}
	if strings.Contains(out, "regenerated on another cluster node") {
		t.Errorf("a transport failure must not print the auth-specific regeneration hint, got: %s", out)
	}
}
