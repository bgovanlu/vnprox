// SPDX-License-Identifier: Apache-2.0

package demo

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bgovanlu/vnprox/internal/flow"
)

// DefaultFlowSeedInterval is how often the demo flow corpus is replayed.
//
// Once at startup is not enough: every Flow Explorer query and every map
// flow-painting window is relative to now, so a demo instance left open for
// an afternoon would show an empty flow screen — which reads as "this
// product's flow feature does not work", the opposite of the point. Five
// minutes keeps the most recent sample under the tightest window the UI
// offers without making the ring grow noticeably.
const DefaultFlowSeedInterval = 5 * time.Minute

// FlowCorpus is dataset/flows.yaml, parsed. See that file's header for why
// its timestamps are relative.
type FlowCorpus struct {
	Flows []FlowSpec `yaml:"flows"`
}

// FlowSpec is one fixture-declared flow sample. It mirrors flow.Record
// field for field except for AtOffsetSec, which replaces Record.At — see
// dataset/flows.yaml on why an absolute timestamp in a checked-in fixture
// is a bug waiting for tomorrow.
type FlowSpec struct {
	Node   string `yaml:"node"`
	SrcIP  string `yaml:"src_ip"`
	DstIP  string `yaml:"dst_ip"`
	Source string `yaml:"source"`
	// AtOffsetSec is seconds before the seed moment. Zero means "now";
	// positive values are rejected, because a demo that ships flows from
	// the future is a demo that has stopped being believable.
	AtOffsetSec int   `yaml:"at_offset_sec"`
	Bytes       int64 `yaml:"bytes"`
	Packets     int64 `yaml:"packets"`
	SrcPort     int   `yaml:"src_port,omitempty"`
	DstPort     int   `yaml:"dst_port,omitempty"`
	Proto       int   `yaml:"proto"`
	VLAN        int   `yaml:"vlan,omitempty"`
}

// knownFlowSources is the closed set of flow.Source values a corpus entry
// may name. Closed on purpose: a typo'd source silently becomes an unknown
// string that the Flow Explorer's source filter then never matches, which
// looks like a missing feature rather than a bad fixture.
var knownFlowSources = map[string]flow.Source{
	string(flow.SourceSFlow):     flow.SourceSFlow,
	string(flow.SourceNetFlow5):  flow.SourceNetFlow5,
	string(flow.SourceNetFlow9):  flow.SourceNetFlow9,
	string(flow.SourceIPFIX):     flow.SourceIPFIX,
	string(flow.SourceConntrack): flow.SourceConntrack,
}

func parseFlowCorpus(raw []byte) (FlowCorpus, error) {
	var corpus FlowCorpus
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&corpus); err != nil {
		return FlowCorpus{}, fmt.Errorf("demo: parsing embedded %s: %w", FlowsFixturePath, err)
	}
	if len(corpus.Flows) == 0 {
		return FlowCorpus{}, fmt.Errorf("demo: embedded %s declares no flows; the demo's Flow Explorer would render empty", FlowsFixturePath)
	}
	for i, f := range corpus.Flows {
		if f.Node == "" || f.SrcIP == "" || f.DstIP == "" {
			return FlowCorpus{}, fmt.Errorf("demo: %s entry %d has no node/src_ip/dst_ip", FlowsFixturePath, i)
		}
		if f.AtOffsetSec > 0 {
			return FlowCorpus{}, fmt.Errorf("demo: %s entry %d has at_offset_sec %d; offsets are seconds BEFORE the seed moment and must be <= 0", FlowsFixturePath, i, f.AtOffsetSec)
		}
		if _, ok := knownFlowSources[f.Source]; !ok {
			return FlowCorpus{}, fmt.Errorf("demo: %s entry %d has unknown source %q; want one of sflow/netflow5/netflow9/ipfix/conntrack", FlowsFixturePath, i, f.Source)
		}
	}
	return corpus, nil
}

// Records renders the corpus as flow.Records observed relative to now.
func (c FlowCorpus) Records(now time.Time) []flow.Record {
	out := make([]flow.Record, 0, len(c.Flows))
	for _, f := range c.Flows {
		out = append(out, flow.Record{
			Node:    f.Node,
			SrcIP:   f.SrcIP,
			DstIP:   f.DstIP,
			Source:  knownFlowSources[f.Source],
			At:      now.Add(time.Duration(f.AtOffsetSec) * time.Second).Unix(),
			Bytes:   f.Bytes,
			Packets: f.Packets,
			SrcPort: f.SrcPort,
			DstPort: f.DstPort,
			Proto:   f.Proto,
			VLAN:    f.VLAN,
		})
	}
	return out
}

// FlowIngester is the one method the seeder needs from *flow.Service. A
// seam, not an abstraction: it exists so this package does not have to
// construct a whole flow service in its own tests.
type FlowIngester interface {
	Ingest(ctx context.Context, records []flow.Record)
}

// RunFlowSeeder replays the demo flow corpus into ingester immediately and
// then every interval until ctx is done. It is a run-group actor: it owns
// one goroutine and returns nil on cancellation, like every other actor in
// cmd/vnproxd's group.
//
// The records go through flow.Service.Ingest — the same entry point the
// NetFlow/sFlow/IPFIX decoders and the conntrack sampler use — so they get
// the same inventory resolution, the same ring, the same WS broadcast and
// the same retention as a real sample. There is no demo-only flow path.
//
// This IS a write, to the app-owned flow_samples ring. That is deliberate
// and is not in tension with demo mode's write-refusal: demo mode refuses
// *mutations requested through the API*, exactly as the card frames it
// ("every write path is a no-op that reports what it would have done" is
// about the change engine and the API surface). A demo daemon still
// observes its synthetic world into its own store, the same way it still
// fills the inventory graph — otherwise there would be nothing to look at.
func RunFlowSeeder(ctx context.Context, corpus FlowCorpus, ingester FlowIngester, interval time.Duration, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = DefaultFlowSeedInterval
	}
	seed := func() {
		records := corpus.Records(time.Now())
		ingester.Ingest(ctx, records)
		logger.Debug("demo: seeded the synthetic flow corpus", "records", len(records))
	}
	seed()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			seed()
		}
	}
}
