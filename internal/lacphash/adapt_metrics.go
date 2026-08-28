// SPDX-License-Identifier: Apache-2.0

package lacphash

import "github.com/bgovanlu/vnprox/internal/metrics"

// SlaveComparison pairs one PredictedSlave with the real per-slave rate
// counters internal/metrics.Sampler already tracks for the same bond
// (metrics.LiveMetric.Slaves — itself read from internal/host's
// /proc/net/bonding-derived slave list, the one part of this whole
// feature that is genuinely *observed* traffic, not a hash computed from
// documentation — see doc.go). Divergence between a slave's predicted
// share and its actual observed rate is the useful signal T-4110's card
// names: it can reveal a hash policy that is not distributing as assumed,
// *on this cluster's actual traffic*, without needing real hardware to
// show that specific claim. It is a strictly weaker claim than "the
// kernel's per-flow hash decision matches this package's Hash()" —
// Compare only says the *aggregate* rates line up (or don't); confirming
// the per-flow decision needs the hardware pass
// planning/reports/needs-hardware-validation.md's T-4110 entry names.
type SlaveComparison struct {
	Ref            string
	Name           string
	PredictedFlows int
	PredictedBytes int64
	ActualRxBps    float64
	ActualTxBps    float64
	// HasActual is false when metrics.Sampler has no live rate for this
	// slave yet (fewer than two samples observed — see
	// metrics.Sampler.Live's own documented contract), distinct from an
	// actual rate that is genuinely zero (a real, quiet slave).
	HasActual bool
}

// Compare merges pred's per-slave predictions with live's real per-slave
// rates. live may be nil (no live metric sample fetched/available for this
// bond yet), in which case every row's HasActual is false — a caller
// renders that as "no actual data yet", never a zeroed/misleading actual
// rate. Slaves are matched by Ref.
func Compare(pred Prediction, live *metrics.LiveMetric) []SlaveComparison {
	actualByRef := map[string]metrics.SlaveRate{}
	if live != nil {
		for _, sr := range live.Slaves {
			actualByRef[sr.Ref] = sr
		}
	}

	out := make([]SlaveComparison, len(pred.Slaves))
	for i, ps := range pred.Slaves {
		row := SlaveComparison{
			Ref:            ps.Ref,
			Name:           ps.Name,
			PredictedFlows: ps.Flows,
			PredictedBytes: ps.Bytes,
		}
		if sr, ok := actualByRef[ps.Ref]; ok {
			row.HasActual = true
			row.ActualRxBps = sr.Rates.RxBps
			row.ActualTxBps = sr.Rates.TxBps
		}
		out[i] = row
	}
	return out
}
