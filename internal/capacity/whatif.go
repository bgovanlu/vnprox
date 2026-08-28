// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"math"
)

// WhatIfResult is WhatIf's answer: whether, and at what synthetic guest
// count, a hypothetical added load first reaches CapacityThresholdPct.
type WhatIfResult struct {
	// BreaksAtN is the smallest guest count in [1, n] at which the projected
	// utilization reaches CapacityThresholdPct, or nil when it does not
	// within n guests.
	BreaksAtN *int
	// AlreadyOverToday is true when latest.MaxUtil is already at/over
	// CapacityThresholdPct, independent of any addition — the link is full
	// today, before any guest in this request is added.
	AlreadyOverToday bool
	// ConsumedPctAtN is the projected utilization (percent) if all n guests
	// are added, computed whether or not that crosses the threshold.
	ConsumedPctAtN float64
}

// WhatIf projects a link's utilization forward under a synthetic,
// guest-count-indexed load increment anchored at the link's most recently
// observed daily rollup (latest), and reports whether/at-what-N the added
// load reaches CapacityThresholdPct within n guests.
//
// This answers a different question than Forecast: Forecast projects an
// *observed* multi-day trend forward in real time. WhatIf projects a
// *hypothetical* guest-count-indexed load forward — one synthetic day per
// added guest — anchored at latest.MaxUtil, deliberately not blended with
// whatever the link's own organic trend might do over that same span
// (attributing how much of a future trend would happen independent of the
// added guests is not something this package can know, so it isn't
// guessed at).
//
// It is not a new crossing formula: it constructs a two-point synthetic
// series (today, and n guests from now) and calls Forecast on it, so the
// existing linear-fit + CapacityThresholdPct + "already over" clamp logic
// runs unmodified. Because the series is exactly two points, the fit is
// exact (no statistical noise), which is why this result carries no
// Confidence field the way Projection does — reporting a fabricated R² for
// an exact two-point line would be misleading, not informative.
func WhatIf(latest Aggregate, addedPctPerGuest float64, n int) WhatIfResult {
	consumedAtN := latest.MaxUtil + addedPctPerGuest*float64(n)
	res := WhatIfResult{ConsumedPctAtN: consumedAtN}

	if latest.MaxUtil >= CapacityThresholdPct {
		res.AlreadyOverToday = true
		return res
	}
	if addedPctPerGuest <= 0 || n <= 0 {
		return res
	}

	// afterOne anchors the synthetic series' second point one guest (one
	// synthetic day) after latest, giving Forecast an exact slope of
	// addedPctPerGuest/day to extrapolate from — see the doc comment above
	// for why the series must lead with the real slope rather than jump
	// straight to the n-guest endpoint (Forecast's "already over capacity
	// within the observed window" clamp would otherwise collapse any
	// already-over-100 endpoint to "full as of the last point", discarding
	// exactly the earlier crossing guest-count this function exists to find).
	afterOne := Aggregate{
		BucketAt: latest.BucketAt.AddDate(0, 0, 1),
		Ref:      latest.Ref,
		Kind:     latest.Kind,
		MaxUtil:  latest.MaxUtil + addedPctPerGuest,
		AvgUtil:  latest.MaxUtil + addedPctPerGuest,
	}
	if n == 1 {
		// No room to extrapolate further — the answer is entirely in
		// afterOne itself.
		if afterOne.MaxUtil >= CapacityThresholdPct {
			one := 1
			res.BreaksAtN = &one
		}
		return res
	}

	// horizonDays bounds the extrapolation window to exactly guest N,
	// measured from afterOne (guest 1): afterOne's day + (n-1) more
	// synthetic days lands exactly on guest N's day.
	proj := Forecast([]Aggregate{latest, afterOne}, n-1)
	if proj.CrossesAt == nil {
		return res
	}

	days := proj.CrossesAt.Sub(latest.BucketAt).Hours() / 24
	breaks := int(math.Ceil(days))
	if breaks < 1 {
		breaks = 1
	}
	if breaks > n {
		breaks = n
	}
	res.BreaksAtN = &breaks
	return res
}
