package failsim

import "github.com/bgovanlu/vnprox/internal/inventory"

// Abort reasons PreflightUnsafe returns — machine-stable audit keys the
// scheduler stamps distinctly from T-1103's own touchesMgmtPath exclusion.
const (
	ReasonQuorumRisk   = "quorum_risk"
	ReasonMgmtPathLoss = "mgmt_path_loss"
)

var severityRank = map[string]int{
	SeverityNone:     0,
	SeverityInfo:     1,
	SeverityWarning:  2,
	SeverityCritical: 3,
}

// Preflight computes the Impact of the worst-affected entity among refs — the
// pre-flight verdict a changeset's touched refs produce, consumed by both
// POST /changesets/{id}/preflight-impact and T-1103's scheduler at
// windowStart. "Worst" is the highest-severity Impact (ties broken by ref
// order for a deterministic result). An empty ref set yields a zero Impact.
func Preflight(in Input, refs []inventory.Ref) Impact {
	worst := Impact{Severity: SeverityNone}
	first := true
	for _, ref := range refs {
		im := Simulate(in, ref)
		if first || severityRank[im.Severity] > severityRank[worst.Severity] {
			worst = im
			first = false
		}
	}
	return worst
}

// PreflightUnsafe reports whether an Impact must veto an *unattended* apply,
// and the machine reason for audit. The veto classes are quorum risk and
// management-path loss — the two an operator could not recover from without
// being present. Guest/VLAN disconnection alone is a warning the review
// surface shows, not an unattended-apply veto.
//
// This is deliberately additive to — never a replacement for — T-1103's
// existing touchesMgmtPath exclusion: the scheduler applies that gate first
// and unconditionally, and consults this one only afterward as one more
// reason to abort. A clean verdict here never grants a bypass of any existing
// safety gate.
func PreflightUnsafe(im Impact) (unsafe bool, reason string) {
	if im.QuorumRisk {
		return true, ReasonQuorumRisk
	}
	if len(im.MgmtPathLoss) > 0 {
		return true, ReasonMgmtPathLoss
	}
	return false, ""
}
