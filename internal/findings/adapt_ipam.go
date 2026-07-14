package findings

// IPAMProvider is the seam internal/ipam's subnet/allocation conflict
// producer satisfies (via cmd/vnproxd's ipamFindingsAdapter, which converts
// ipam.Conflict values into the unified Finding shape — the composition
// root does the conversion so internal/ipam need not import this package).
// Nil Config.IPAM still means "contribute zero IPAM findings" (degraded
// mode with no PVE client), so the seam stays nil-safe.
//
// The seam returns the unified Finding shape directly (Source: SourceIPAM),
// so the producer/adapter constructs findings.Finding values straight away
// rather than exposing an IPAM-package-local type Engine would have to
// re-adapt.
type IPAMProvider interface {
	Findings() []Finding
}

// ipamFindings returns p's current findings, or nil when p is nil (the
// documented pending-T-405 state).
func ipamFindings(p IPAMProvider) []Finding {
	if p == nil {
		return nil
	}
	return p.Findings()
}
