// health_gitsync.go adapts T-2701's git spec sync into the unified findings
// stream (source "gitsync"). internal/gitsync computes these conditions on
// its own poll cadence — an unreachable remote, an unparseable document, a
// commit it refused to trust, and the plain fact that intent and reality
// currently disagree — and this file only renders them.
//
// Every one of these findings is detection-only: Fixable stays false, and
// there is no fix path from here into the change engine. The *action* a
// divergence produces is a draft changeset gitsync already opened for a
// human to review, which is a stronger and more reviewable artifact than a
// "fix" button, and is the shape T-2701's invariant requires.

package findings

import "sort"

// GitSyncIssue is one condition internal/gitsync reports. It is a plain
// value so this package does not depend on internal/gitsync; cmd/vnproxd's
// gitSyncFindingsAdapter converts, the same seam shape FederationProvider
// and PeerTrustProvider already use.
type GitSyncIssue struct {
	Check    string
	Severity string
	Detail   string
}

// GitSyncProvider is the findings engine's seam onto the git spec sync. A
// nil provider (the default — gitsync is off unless configured) skips this
// producer entirely, the same degradation every other optional Config field
// uses.
type GitSyncProvider interface {
	GitSyncIssues() []GitSyncIssue
}

const gitSyncDocsLink = "docs/api.md#git-spec-sync"

// gitSyncFindings renders the provider's current issues. It is deliberately
// hysteresis-exempt: these are not sampled measurements but facts about the
// last completed poll (a fetch either failed or it did not; a document either
// parses or it does not), so a rise window would only delay a real signal —
// the same reasoning health_certs.go's checks already carry.
func gitSyncFindings(prov GitSyncProvider) []Finding {
	if prov == nil {
		return nil
	}
	issues := prov.GitSyncIssues()
	out := make([]Finding, 0, len(issues))
	for _, iss := range issues {
		severity := iss.Severity
		if severityRank[severity] == 0 && severity != SeverityInfo {
			// An unrecognised severity from a producer must not silently
			// rank below info; name it a warning rather than losing it.
			severity = SeverityWarning
		}
		out = append(out, Finding{
			ID:       "gitsync:" + iss.Check,
			Source:   SourceGitSync,
			Check:    iss.Check,
			Severity: severity,
			Detail:   iss.Detail,
			Nodes:    []string{},
			DocsLink: gitSyncDocsLink,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
