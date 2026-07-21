// health_hareplication.go implements the "ha_replication_degraded" health
// check (T-1704): one finding when the active daemon's state replication to
// its HA standby is degraded — the last replication push failed (the standby
// is unreachable) or the standby is lagging past the configured audit-lag
// threshold. Surfaced through the same unified findings stream every other
// producer feeds so an operator notices a standby that has silently fallen out
// of sync (and would be stale on promotion) without separately polling
// GET /ha/status.
//
// Detection-only (like schedule_missed / mgmt_single_path): there is no single
// obviously-correct automated response — the operator decides whether to
// investigate the link, the standby, or the lag threshold.

package findings

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/ha"
)

const CheckHAReplicationDegraded = "ha_replication_degraded"

const haReplicationDocsLink = "docs/architecture.md#11-ha-topology-active-standby"

// HAReplicationProvider is the seam checkHAReplicationDegraded needs:
// *ha.Manager.Status, read live each cycle. Context-free, matching the other
// health providers' shape; nil (HA disabled) yields no findings.
type HAReplicationProvider interface {
	Status() ha.Status
}

// checkHAReplicationDegraded flags one finding while HA replication is
// degraded. A nil provider (HA not wired) or a healthy/standby status yields
// nothing.
func checkHAReplicationDegraded(p HAReplicationProvider) []Finding {
	if p == nil {
		return nil
	}
	st := p.Status()
	if !st.ReplicationDegraded {
		return nil
	}
	detail := fmt.Sprintf(
		"HA state replication is degraded (role %s, lag %d audit rows) — the standby may be unreachable or falling behind, and would be stale if it had to take over",
		st.Role, st.ReplicationLag,
	)
	if st.LastError != "" {
		detail += ": " + st.LastError
	}
	f := newHealthFinding(CheckHAReplicationDegraded, SeverityWarning, detail, nil, nil)
	f.DocsLink = haReplicationDocsLink
	return []Finding{f}
}
