package baseline

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/flow"
)

// corpus_gen_test.go deterministically builds T-1601's two flow-baseline
// corpora and, when BASELINE_GEN=1, (re)writes them under testdata/. The
// committed JSON is what the real tests (and, later, T-1602's planner tests)
// load via ParseCorpus — regenerate with:
//
//	BASELINE_GEN=1 go test ./internal/baseline/ -run TestGenerateCorpora
//
// The builders are the single source of truth for the fixtures; the JSON is
// their serialization, checked in so the fixture is inspectable and shared
// verbatim across packages/tasks.

const (
	corpusRef   = "guest:pve1:100"
	corpusSrcIP = "10.0.0.5"
	protoTCP    = 6
	protoUDP    = 17

	cleanCorpusFile = "clean_injected_corpus.json"
	noiseCorpusFile = "noise_corpus.json"
)

// corpusWindow returns the fixed 14-day learning window both corpora share
// (Jan 1–Jan 15 2024 UTC, day-aligned).
func corpusWindow() Window {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	end := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC).Unix()
	return Window{Start: start, End: end}
}

func mkRec(dstIP string, proto, dstPort int, at, bytes int64) flow.Record {
	return flow.Record{
		Node:    "pve1",
		SrcRef:  corpusRef,
		SrcIP:   corpusSrcIP,
		DstIP:   dstIP,
		At:      at,
		Bytes:   bytes,
		Packets: 1,
		SrcPort: 40000,
		DstPort: dstPort,
		Proto:   proto,
		Source:  flow.SourceConntrack,
	}
}

// buildCleanInjectedCorpus builds a clean 14-day baseline (two steady talkers
// on tcp/443 and udp/53 within 10.0.0.0/24) plus one injected anomaly of each
// class in a recent window just after window.End.
func buildCleanInjectedCorpus() Corpus {
	w := corpusWindow()
	var recs []flow.Record
	for day := 0; day < 14; day++ {
		for hour := 0; hour < 24; hour++ {
			at := w.Start + int64(day)*86400 + int64(hour)*3600 + 60
			// small deterministic per-(day,hour) variation so stddev is
			// non-zero but the self-feedback ratio stays well under any spike
			// multiple.
			v := int64(1000 + (day*7+hour)%50) // 1000..1049
			recs = append(recs, mkRec("10.0.0.10", protoTCP, 443, at, v))
			recs = append(recs, mkRec("10.0.0.20", protoUDP, 53, at+1, v/2))
		}
	}

	recentDay := w.End + 86400 // the day after the learning window
	newPortAt := recentDay + 2*3600 + 60
	newSubnetAt := recentDay + 3*3600 + 60
	spikeAt := recentDay + 14*3600 + 60 // hour-of-day 14 (has a learned baseline)

	recent := []flow.Record{
		// new_port: a never-before-seen service port on a known subnet, small
		// volume (must not itself read as a spike).
		mkRec("10.0.0.10", protoTCP, 6667, newPortAt, 400),
		// new_subnet: a known service port toward a never-before-seen /24.
		mkRec("10.9.9.5", protoTCP, 443, newSubnetAt, 400),
		// volume_spike: a known port/subnet, but a single hour's volume many
		// times the baseline for that hour-of-day.
		mkRec("10.0.0.10", protoTCP, 443, spikeAt, 45000),
	}

	injected := []InjectedAnomaly{
		{Class: string(ClassNewPort), Subject: "tcp/6667"},
		{Class: string(ClassNewSubnet), Subject: "10.9.9.0/24"},
		{Class: string(ClassVolumeSpike), Subject: hourSubject(spikeAt / secondsPerHour)},
	}

	return Corpus{
		Name:     "clean-14d-baseline-plus-one-injected-anomaly-of-each-class",
		Ref:      corpusRef,
		Window:   w,
		Records:  recs,
		Recent:   recent,
		Injected: injected,
	}
}

// buildNoiseCorpus builds a 14-day "pure noise, no anomaly" corpus: varied but
// self-consistent traffic drawn from a fixed port/subnet vocabulary, whose own
// flows fed back through Detect raise nothing.
func buildNoiseCorpus() Corpus {
	w := corpusWindow()
	peers := []struct {
		ip    string
		proto int
		port  int
	}{
		{"10.0.0.10", protoTCP, 443},
		{"10.0.0.20", protoTCP, 80},
		{"10.0.1.30", protoUDP, 53},
	}
	var recs []flow.Record
	for day := 0; day < 14; day++ {
		for hour := 0; hour < 24; hour++ {
			at := w.Start + int64(day)*86400 + int64(hour)*3600 + 30
			for i, p := range peers {
				v := int64(800 + (day*31+hour*17+i*13)%800) // 800..1599, deterministic
				recs = append(recs, mkRec(p.ip, p.proto, p.port, at+int64(i), v))
			}
		}
	}
	return Corpus{
		Name:     "pure-noise-no-anomaly",
		Ref:      corpusRef,
		Window:   w,
		Records:  recs,
		Recent:   recs, // self-feedback: a baseline never flags its own training data
		Injected: nil,
	}
}

func TestGenerateCorpora(t *testing.T) {
	if os.Getenv("BASELINE_GEN") != "1" {
		t.Skip("set BASELINE_GEN=1 to (re)write testdata corpora")
	}
	for _, tc := range []struct {
		file   string
		corpus Corpus
	}{
		{cleanCorpusFile, buildCleanInjectedCorpus()},
		{noiseCorpusFile, buildNoiseCorpus()},
	} {
		data, err := tc.corpus.Marshal()
		if err != nil {
			t.Fatalf("marshal %s: %v", tc.file, err)
		}
		path := filepath.Join("testdata", tc.file)
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("wrote %s (%d records, %d recent)", path, len(tc.corpus.Records), len(tc.corpus.Recent))
	}
}
