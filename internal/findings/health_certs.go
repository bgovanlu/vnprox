// health_certs.go adapts internal/certs' check results into the unified
// findings stream as source "cert" (T-2302).
//
// The certificate checks live in internal/certs rather than here because they
// are x509 reasoning, not inventory reasoning — this file is the seam, in the
// same shape CorosyncProvider/WanProvider/LatMeshProvider already use: a
// certificate has no inventory.Ref, so it cannot be fed through the graph like
// an interface or a bond.
//
// Every certificate finding is hysteresis-exempt. An expiry date, a SAN list,
// and an issuer are structural facts read from a file, not sampled live
// counters — debouncing them would delay a real signal by two cycles and
// suppress nothing, since there is no noise to suppress. This matches
// mgmt_single_path and orphan_vnet, and deliberately differs from every
// check derived from a running measurement.
//
// Nothing here is Fixable. vnprox does not renew or reissue certificates
// (planning/tasks/phase-23.md's scope note): PVE owns that, and each finding
// carries the exact command instead.

package findings

// CertProvider is the findings engine's seam onto internal/certs.Service.
type CertProvider interface {
	Issues() []CertIssue
}

// CertIssue mirrors certs.Issue. Duplicated as a plain struct rather than
// imported so internal/findings keeps no dependency on internal/certs — the
// same decoupling PeerTrustStatus uses for internal/peer.
type CertIssue struct {
	Check       string
	Severity    string
	Node        string
	Path        string
	Detail      string
	Remediation string
}

const certDocsLink = "docs/security.md#certificates"

// checkCertificates converts the provider's current issues into findings.
func checkCertificates(prov CertProvider) []Finding {
	if prov == nil {
		return nil
	}
	issues := prov.Issues()
	out := make([]Finding, 0, len(issues))
	for _, i := range issues {
		out = append(out, certFinding(i))
	}
	return out
}

func certFinding(i CertIssue) Finding {
	// The id is keyed by check plus node plus path so two problems with the
	// same node's two different certificates stay distinct, and so a finding
	// is stable across refreshes rather than churning.
	key := i.Node
	if i.Path != "" {
		key += "|" + i.Path
	}
	detail := i.Detail
	if i.Remediation != "" {
		detail += ". To fix: " + i.Remediation
	}
	var nodes []string
	if i.Node != "" {
		nodes = []string{i.Node}
	}
	return Finding{
		ID:       "cert:" + i.Check + "|" + key,
		Source:   SourceCert,
		Check:    i.Check,
		Severity: i.Severity,
		Detail:   detail,
		DocsLink: certDocsLink,
		Nodes:    sortedUnique(nodes),
		Fixable:  false,
	}
}
