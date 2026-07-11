package findings

// IPAMProvider is the seam T-405's subnet/allocation conflict producer will
// satisfy once that task lands. As of this task, internal/ipam is still a
// stub package (only doc.go — see the T-602 completion report's note on
// checking `git log -- internal/ipam` / planning/reports/T-405.md before
// assuming otherwise), so there is nothing to adapt yet: Engine simply
// contributes zero IPAM findings while Config.IPAM is nil.
//
// The seam already returns the unified Finding shape directly (Source:
// SourceIPAM) rather than some IPAM-package-local type Engine would need to
// adapt — T-405 can construct findings.Finding values straight away using
// the same newHealthFinding-style helper this package's own health checks
// use (or a small ipam-local equivalent), so wiring IPAM in later is a
// one-line Config.IPAM assignment in cmd/vnproxd, not a redesign of this
// package's producer contract.
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
