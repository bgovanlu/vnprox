// SPDX-License-Identifier: Apache-2.0

package baseline

import (
	"encoding/json"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/flow"
)

// InjectedAnomaly documents one deviation a test corpus deliberately injects
// on top of its clean baseline: the class Detect must classify it as and the
// Subject it must flag. T-1601's AC2 asserts Detect surfaces exactly the
// corpus's InjectedAnomaly set (no more, no fewer); T-1602's planner tests
// reuse the same field to assert an injected/anomalous flow is excluded from
// what the planner treats as legitimate.
type InjectedAnomaly struct {
	Class   string `json:"class"`   // new_port | volume_spike | new_subnet
	Subject string `json:"subject"` // the port/subnet/hour the injected flows target
}

// Corpus is a self-describing synthetic flow-baseline fixture (T-1601's new
// fixture family, design §5). Records are the learning-window flows a Profile
// is Learn'd from; Recent are the flows Detect replays against that Profile;
// Injected names the anomalies Detect is expected to raise from
// Detect(Learn(Records), Recent). A "pure noise, no anomaly" corpus sets
// Recent to its own Records (or a subset) and leaves Injected empty — Detect
// must then raise nothing (a baseline never flags its own training data).
//
// Serialized under internal/baseline/testdata/*.json and reused verbatim by
// T-1602's planner tests through ParseCorpus — the single, shared fixture
// format both cards read, never a re-derived copy.
type Corpus struct {
	Name     string            `json:"name"`
	Ref      string            `json:"ref"`
	Records  []flow.Record     `json:"records"`
	Recent   []flow.Record     `json:"recent"`
	Injected []InjectedAnomaly `json:"injected"`
	Window   Window            `json:"window"`
}

// ParseCorpus decodes a Corpus from its serialized JSON form.
func ParseCorpus(data []byte) (Corpus, error) {
	var c Corpus
	if err := json.Unmarshal(data, &c); err != nil {
		return Corpus{}, fmt.Errorf("baseline: parsing corpus: %w", err)
	}
	return c, nil
}

// Marshal serializes a Corpus to indented JSON (the on-disk testdata form).
func (c Corpus) Marshal() ([]byte, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("baseline: marshaling corpus %s: %w", c.Name, err)
	}
	return data, nil
}
