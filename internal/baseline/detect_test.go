// SPDX-License-Identifier: Apache-2.0

package baseline

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/flow"
)

func loadCorpus(t *testing.T, file string) Corpus {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatalf("reading corpus %s: %v", file, err)
	}
	c, err := ParseCorpus(data)
	if err != nil {
		t.Fatalf("parsing corpus %s: %v", file, err)
	}
	return c
}

// TestDetect_BaselineNeverFlagsOwnTrainingData is T-1601 AC1: Learn over the
// window then Detect on the same window's own flows raises nothing — for the
// pure-noise corpus and for the clean baseline of the injected corpus.
func TestDetect_BaselineNeverFlagsOwnTrainingData(t *testing.T) {
	for _, file := range []string{noiseCorpusFile, cleanCorpusFile} {
		c := loadCorpus(t, file)
		prof := Learn(c.Records, c.Ref, c.Window)
		if prof.Empty() {
			t.Fatalf("%s: Learn produced an empty profile", file)
		}
		// Feed the learning-window flows back in as the "recent" batch.
		got := Detect(prof, c.Records, DefaultDetectConfig())
		if len(got) != 0 {
			t.Errorf("%s: Detect on training data raised %d anomalies, want 0:\n%v", file, len(got), got)
		}
	}
}

// TestDetect_InjectedAnomalies is T-1601 AC2: against the injected corpus,
// Detect raises exactly one anomaly per injected class, each with the right
// class and the right structured baseline/observed/deviation fields.
func TestDetect_InjectedAnomalies(t *testing.T) {
	c := loadCorpus(t, cleanCorpusFile)
	prof := Learn(c.Records, c.Ref, c.Window)

	got := Detect(prof, c.Recent, DefaultDetectConfig())
	if len(got) != len(c.Injected) {
		t.Fatalf("Detect raised %d anomalies, want %d:\n%v", len(got), len(c.Injected), got)
	}

	byClass := map[AnomalyClass]Anomaly{}
	for _, a := range got {
		if _, dup := byClass[a.Class]; dup {
			t.Fatalf("duplicate anomaly class %s", a.Class)
		}
		byClass[a.Class] = a
	}

	// The (class, subject) set must match the corpus's declared injections
	// exactly — no more, no fewer.
	wantSubject := map[string]string{}
	for _, inj := range c.Injected {
		wantSubject[inj.Class] = inj.Subject
	}
	for cls, a := range byClass {
		if want := wantSubject[string(cls)]; want != a.Subject {
			t.Errorf("%s subject = %q, want %q", cls, a.Subject, want)
		}
		if a.Ref != c.Ref {
			t.Errorf("%s ref = %q, want %q", cls, a.Ref, c.Ref)
		}
		if a.BaselineWindow != c.Window {
			t.Errorf("%s baselineWindow = %+v, want %+v", cls, a.BaselineWindow, c.Window)
		}
	}

	// new_port / new_subnet are categorical: baseline 0, observed 1 occurrence.
	for _, cls := range []AnomalyClass{ClassNewPort, ClassNewSubnet} {
		a := byClass[cls]
		if a.BaselineValue != 0 {
			t.Errorf("%s baselineValue = %v, want 0", cls, a.BaselineValue)
		}
		if a.ObservedValue != 1 {
			t.Errorf("%s observedValue = %v, want 1", cls, a.ObservedValue)
		}
		if a.DeviationFactor != 1 {
			t.Errorf("%s deviationFactor = %v, want 1", cls, a.DeviationFactor)
		}
	}

	// volume_spike is quantitative: baseline is that hour-of-day's mean+stddev,
	// observed is the injected hour's bytes (45000), factor is their ratio and
	// clears the default 10x multiple.
	spike := byClass[ClassVolumeSpike]
	if spike.ObservedValue != 45000 {
		t.Errorf("volume_spike observedValue = %v, want 45000", spike.ObservedValue)
	}
	wantBase := prof.Hours[14].Mean + prof.Hours[14].Stddev
	if math.Abs(spike.BaselineValue-wantBase) > 1e-9 {
		t.Errorf("volume_spike baselineValue = %v, want %v (hour-14 mean+stddev)", spike.BaselineValue, wantBase)
	}
	if wantFactor := 45000.0 / wantBase; math.Abs(spike.DeviationFactor-wantFactor) > 1e-9 {
		t.Errorf("volume_spike deviationFactor = %v, want %v", spike.DeviationFactor, wantFactor)
	}
	if spike.DeviationFactor < DefaultVolumeSpikeMultiple {
		t.Errorf("volume_spike deviationFactor = %v, want >= %v", spike.DeviationFactor, DefaultVolumeSpikeMultiple)
	}
}

// TestDetect_VolumeSpikeThreshold is T-1601 AC3: against a synthetic
// step-change series with a flat baseline of 1000 bytes/hour, volume_spike
// fires only once the configured multiple is crossed.
func TestDetect_VolumeSpikeThreshold(t *testing.T) {
	const (
		ref     = "guest:pve1:200"
		baseVol = 1000
		days    = 10
		hod     = 10 // hour-of-day the series lives in
	)
	// Baseline: one flow per day at hour-of-day 10, constant 1000 bytes, on a
	// fixed known port/subnet (so a recent same-port/subnet flow can only ever
	// read as a volume_spike, never new_port/new_subnet).
	base := time.Date(2024, 1, 1, hod, 0, 0, 0, time.UTC).Unix()
	window := Window{Start: base - 3600, End: base + int64(days)*86400}
	var recs []flow.Record
	for d := 0; d < days; d++ {
		recs = append(recs, flow.Record{
			SrcRef: ref, SrcIP: "10.0.0.5", DstIP: "10.0.0.10",
			At: base + int64(d)*86400, Bytes: baseVol, DstPort: 443, Proto: protoTCP,
		})
	}
	prof := Learn(recs, ref, window)
	if got := prof.Hours[hod].Mean; got != baseVol {
		t.Fatalf("baseline hour-%d mean = %v, want %v", hod, got, baseVol)
	}
	if got := prof.Hours[hod].Stddev; got != 0 {
		t.Fatalf("baseline hour-%d stddev = %v, want 0 (constant series)", hod, got)
	}

	recentAt := base + int64(days+1)*86400 // hour-of-day 10, past the window
	cases := []struct {
		name     string
		multiple float64
		bytes    int64
		want     bool
	}{
		{"5x-multiple/exactly-5x-fires", 5, 5 * baseVol, true},
		{"5x-multiple/just-under-5x-silent", 5, 5*baseVol - 1, false},
		{"10x-multiple/12x-fires", 10, 12 * baseVol, true},
		{"10x-multiple/9x-silent", 10, 9 * baseVol, false},
		{"20x-multiple/12x-silent", 20, 12 * baseVol, false},
		{"20x-multiple/exactly-20x-fires", 20, 20 * baseVol, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recent := []flow.Record{{
				SrcRef: ref, SrcIP: "10.0.0.5", DstIP: "10.0.0.10",
				At: recentAt, Bytes: tc.bytes, DstPort: 443, Proto: protoTCP,
			}}
			got := Detect(prof, recent, DetectConfig{VolumeSpikeMultiple: tc.multiple})
			var spikes int
			for _, a := range got {
				if a.Class == ClassVolumeSpike {
					spikes++
				}
			}
			fired := spikes > 0
			if fired != tc.want {
				t.Errorf("multiple=%.0f bytes=%d: fired=%v (spikes=%d), want %v", tc.multiple, tc.bytes, fired, spikes, tc.want)
			}
		})
	}
}

// TestDetect_ColdStartSilent is T-1601 AC5: a Ref with no flow history in the
// window produces no baseline and no anomalies.
func TestDetect_ColdStartSilent(t *testing.T) {
	window := corpusWindow()
	// A Ref with zero matching flows learns an empty profile.
	prof := Learn(nil, "guest:pve1:999", window)
	if !prof.Empty() {
		t.Fatalf("cold-start Learn produced a non-empty profile: %+v", prof)
	}
	// Even handed a batch of recent flows, an empty baseline raises nothing.
	recent := []flow.Record{
		{SrcRef: "guest:pve1:999", SrcIP: "10.0.0.5", DstIP: "203.0.113.1", At: window.End + 3600, Bytes: 9999, DstPort: 6667, Proto: protoTCP},
	}
	if got := Detect(prof, recent, DefaultDetectConfig()); len(got) != 0 {
		t.Errorf("Detect on an empty baseline raised %d anomalies, want 0: %v", len(got), got)
	}
}
