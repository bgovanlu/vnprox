// Package posture computes one periodically-recomputed network security /
// resilience score with NAMED, independently-inspectable contributing factors
// (T-1607) — never an opaque single number. It folds four already-shipped
// intelligence surfaces into one explainable report:
//
//   - SPOF resilience — T-1604's internal/failsim single-point-of-failure
//     inventory (fewer / lower-impact SPOFs score higher).
//   - Segmentation — the fraction of guests carrying an actually-applied
//     T-1602 microsegmentation policy (only applied coverage counts; a proposal
//     that was dry-run but never staged leaves no trace in the inventory graph
//     and so counts unsegmented).
//   - Exposed ports — guest-scope firewall rules whose resolved evaluation
//     order (internal/fw, T-501) permits inbound from any source with no
//     narrower rule ahead of it.
//   - Anomaly rate — T-1601's source:"baseline" finding count over a trailing
//     window, normalized per guest.
//   - Drift hygiene — the existing internal/drift open-finding count,
//     normalized cluster-wide.
//
// # Honesty contract (load-bearing)
//
// The score must never treat "unknown" as "good/secure". A dimension the
// underlying surface could not actually evaluate is reported transparently as
// a PARTIAL / QUALIFIED score, exactly like internal/failsim's own
// NotEvaluated / internal/sim's caveat contract it inherits:
//
//   - A factor that cannot be assessed at all (a cold-start baseline with no
//     learned profiles — where "zero anomalies" means "we have never looked",
//     not "nothing is wrong") is marked Evaluated=false and EXCLUDED from the
//     overall weighted score entirely, rather than silently contributing a
//     perfect 100. Its slot is still present in Factors (the "never opaque"
//     guarantee), just uncounted, with Caveat naming why.
//   - A factor that was computed but rests on an incomplete underlying picture
//     (failsim reporting NotEvaluated dimensions — quorum/Ceph/tunnels it could
//     not assess for this deployment) still contributes its known-SPOF score,
//     but carries a Caveat and flips Posture.Qualified true: the reported score
//     is a CEILING, real posture may be lower.
//
// Either mechanism sets Posture.Qualified, the single machine-checkable "do not
// read this as a clean bill of health" signal a caller (or the exported report)
// surfaces alongside the number.
//
// This package is pure: Score is a deterministic function of its Inputs, holds
// no clock and no store, and never mutates or persists anything. Persistence
// (the bounded posture_scores table) and the scheduled recomputation live in
// the composition root (cmd/vnproxd); the Markdown/HTML report extends T-605's
// internal/docexport renderer rather than introducing a parallel one.
package posture
