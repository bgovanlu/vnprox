// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"testing"
	"time"
)

var day0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// linearAggs builds n daily link rollups starting at day0 whose MaxUtil grows
// linearly: util(i) = base + slope*i.
func linearAggs(ref string, kind Kind, n int, base, slope float64) []Aggregate {
	out := make([]Aggregate, n)
	for i := range out {
		u := base + slope*float64(i)
		out[i] = Aggregate{
			BucketAt: day0.AddDate(0, 0, i),
			Ref:      ref,
			Kind:     kind,
			AvgUtil:  u,
			MaxUtil:  u,
		}
	}
	return out
}

func TestForecast_LinearGrowthCrossesWithinHorizon(t *testing.T) {
	// util(i) = 50 + 2i over 21 days (i=0..20, util 50..90) — still below
	// capacity in the observed window, projected to hit 100 at i=25.
	aggs := linearAggs("iface:pve1:vmbr1", KindLink, 21, 50, 2)

	proj := Forecast(aggs, 90)
	if proj.CrossesAt == nil {
		t.Fatal("Forecast returned nil CrossesAt for a clearly-growing series")
	}
	want := day0.AddDate(0, 0, 25) // 50 + 2*25 = 100
	if diff := proj.CrossesAt.Sub(want); diff < -24*time.Hour || diff > 24*time.Hour {
		t.Errorf("CrossesAt = %s, want ~%s (within 1 day)", proj.CrossesAt.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	if proj.Confidence < 0.99 {
		t.Errorf("Confidence = %.4f, want ~1.0 for a perfectly linear series", proj.Confidence)
	}
}

func TestForecast_FlatSeriesNoCrossing(t *testing.T) {
	aggs := linearAggs("iface:pve1:vmbr0", KindLink, 20, 40, 0)
	proj := Forecast(aggs, 90)
	if proj.CrossesAt != nil {
		t.Errorf("CrossesAt = %v, want nil for a flat series (no false-positive forecast)", proj.CrossesAt)
	}
}

func TestForecast_DecreasingSeriesNoCrossing(t *testing.T) {
	aggs := linearAggs("iface:pve1:vmbr0", KindLink, 20, 90, -1)
	proj := Forecast(aggs, 90)
	if proj.CrossesAt != nil {
		t.Errorf("CrossesAt = %v, want nil for a decreasing series", proj.CrossesAt)
	}
}

func TestForecast_CrossingBeyondHorizonSuppressed(t *testing.T) {
	// util(i) = 50 + 0.1i: crosses 100 ~500 days out. With a 90-day horizon,
	// no forecast fires even though the trend is upward.
	aggs := linearAggs("iface:pve1:vmbr1", KindLink, 30, 50, 0.1)
	proj := Forecast(aggs, 90)
	if proj.CrossesAt != nil {
		t.Errorf("CrossesAt = %v, want nil — crossing lies beyond the 90-day horizon", proj.CrossesAt)
	}
}

func TestForecast_SinglePointNoFit(t *testing.T) {
	aggs := linearAggs("iface:pve1:vmbr1", KindLink, 1, 50, 2)
	proj := Forecast(aggs, 90)
	if proj.CrossesAt != nil {
		t.Errorf("CrossesAt = %v, want nil — a single point can't fit a trend", proj.CrossesAt)
	}
}
