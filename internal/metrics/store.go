// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"database/sql"

	"github.com/bgovanlu/vnprox/internal/store"
)

// nullInt wraps u as a valid sql.NullInt64 (store.MetricSample's column
// type), matching how internal/store's own callers populate it.
func nullInt(u uint64) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(u), Valid: true}
}

// toMetricSample renders one downsampled Counters snapshot as the
// store.MetricSample row shape (docs/data-model.md §2's metric_samples
// table), keyed by ref and the 30s-bucket-aligned unix timestamp at.
func toMetricSample(ref string, at int64, c Counters) store.MetricSample {
	return store.MetricSample{
		Ref: ref, At: at,
		RxBytes: nullInt(c.RxBytes), TxBytes: nullInt(c.TxBytes),
		RxPkts: nullInt(c.RxPkts), TxPkts: nullInt(c.TxPkts),
		RxErrs: nullInt(c.RxErrs), TxErrs: nullInt(c.TxErrs),
		RxDrop: nullInt(c.RxDrop), TxDrop: nullInt(c.TxDrop),
	}
}

// countersFromRow is toMetricSample's inverse, reading back a stored row
// (unset/NULL columns — never actually written by this package, but
// tolerated for forward compatibility — read as 0).
func countersFromRow(s store.MetricSample) Counters {
	v := func(n sql.NullInt64) uint64 {
		if !n.Valid || n.Int64 < 0 {
			return 0
		}
		return uint64(n.Int64)
	}
	return Counters{
		RxBytes: v(s.RxBytes), TxBytes: v(s.TxBytes),
		RxPkts: v(s.RxPkts), TxPkts: v(s.TxPkts),
		RxErrs: v(s.RxErrs), TxErrs: v(s.TxErrs),
		RxDrop: v(s.RxDrop), TxDrop: v(s.TxDrop),
	}
}
