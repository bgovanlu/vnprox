// SPDX-License-Identifier: Apache-2.0

package fwlog

import (
	"encoding/json"
	"strconv"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TopicFirewallLog is the WS subscribe topic name for this package's
// `firewall.log.batch` event (docs/api.md's WebSocket section: a client
// subscribes to "firewall.log" to receive these pushes, the same way it
// subscribes to "topology" or "drift").
const TopicFirewallLog = "firewall.log"

// EntryView is the JSON wire shape of one log line, shared by the REST
// `GET /firewall/log` response (internal/api/fwlog.go) and the
// `firewall.log.batch` WS event (Service.broadcast) — defined here rather
// than in internal/api so both producers use exactly one contract (the
// same "self-contained wire DTO" precedent internal/topology/hub.go's
// deltaEvent sets for `topology.delta`).
type EntryView struct {
	GuestRef    string          `json:"guestRef,omitempty"`
	Node        string          `json:"node"`
	Direction   string          `json:"direction,omitempty"`
	Action      string          `json:"action,omitempty"`
	Proto       string          `json:"proto,omitempty"`
	Source      string          `json:"source,omitempty"`
	Dest        string          `json:"dest,omitempty"`
	Sport       string          `json:"sport,omitempty"`
	Dport       string          `json:"dport,omitempty"`
	Raw         string          `json:"raw"`
	Correlation CorrelationView `json:"correlation"`
	Seq         int64           `json:"seq"`
	VMID        int             `json:"vmid"`
	At          int64           `json:"at,omitempty"` // unix seconds; omitted if the line's timestamp was unparsable
}

// RuleRefView is CorrelationView's deep-link target.
type RuleRefView struct {
	GuestRef  string `json:"guestRef"`
	Origin    string `json:"origin"`
	GroupName string `json:"groupName,omitempty"`
	Pos       int    `json:"pos"`
}

// CorrelationView is Correlation's wire shape.
type CorrelationView struct {
	Rule               *RuleRefView `json:"rule,omitempty"`
	Status             string       `json:"status"`
	Reason             string       `json:"reason,omitempty"`
	CandidatePositions []int        `json:"candidatePositions,omitempty"`
}

// ToRuleRefView converts a RuleRef to its wire shape — shared by
// CorrelationView.Rule (log entries, below) and T-1006's
// GET /firewall/analytics response (internal/api/fwlog.go), one contract
// for both correlated-log producers.
func ToRuleRefView(r RuleRef) RuleRefView {
	return RuleRefView(r)
}

// ToEntryView converts one buffered StreamEntry to its wire shape.
func ToEntryView(se StreamEntry) EntryView {
	v := EntryView{
		Seq: se.Seq, Node: se.Entry.Node, VMID: se.Entry.VMID, Direction: se.Entry.Direction,
		Action: se.Entry.Action, Proto: se.Entry.Proto, Source: se.Entry.Source, Dest: se.Entry.Dest,
		Sport: se.Entry.Sport, Dport: se.Entry.Dport, Raw: se.Entry.Raw,
		Correlation: toCorrelationView(se.Correlation),
	}
	if se.Entry.Guest {
		v.GuestRef = inventory.Ref{Kind: inventory.KindGuest, Node: se.Entry.Node, ID: strconv.Itoa(se.Entry.VMID)}.String()
	}
	if se.Entry.HasTimestamp {
		v.At = se.Entry.Timestamp.Unix()
	}
	return v
}

func toCorrelationView(c Correlation) CorrelationView {
	v := CorrelationView{Status: string(c.Status), Reason: c.Reason, CandidatePositions: c.CandidatePositions}
	if c.Rule != nil {
		rv := ToRuleRefView(*c.Rule)
		v.Rule = &rv
	}
	return v
}

// batchEvent is the `firewall.log.batch` WS push: the flat "event" name
// field every server->client message on /api/ws carries (see
// internal/topology/hub.go's deltaEvent doc comment), the newly parsed
// entries this tick (already rate-capped — see Service.Tick), and the
// cumulative rate-cap drop count so the client can render AC3's "N lines
// dropped" indicator without tracking a running total itself.
type batchEvent struct {
	Event        string      `json:"event"`
	Entries      []EntryView `json:"entries"`
	DroppedTotal int64       `json:"droppedTotal"`
}

func marshalBatchEvent(e batchEvent) ([]byte, error) {
	return json.Marshal(e)
}
