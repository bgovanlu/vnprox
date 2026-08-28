// SPDX-License-Identifier: Apache-2.0

package lacphash

import (
	"net"
	"testing"

	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/metrics"
)

// lab-simulated fixtures: T-4110 is hardware-flagged (no real switch to
// observe a real 802.3ad aggregate's traffic against), so every flow
// record and bond slave used in this file's tests is synthesized here,
// not captured from real hardware. Clearly labeled as such per the task
// card's requirement, not presented as an observation.

func TestPredict_BucketsByHashAndWeighsByBytes(t *testing.T) {
	slaves := []Slave{
		{Ref: "physnic:pve1:eno1", Name: "eno1", Up: true},
		{Ref: "physnic:pve1:eno2", Name: "eno2", Up: true},
	}
	v4a, v4b := "10.0.0.1", "10.0.0.2"

	// Two lab-simulated flows with distinct source ports (layer3+4
	// discriminates on port) — SelectSlave with numSlaves=2 must land
	// them somewhere in [0,2), and Predict's totals must reflect the
	// Bytes/Packets each carried, not just a flow count of 1 each.
	tuples := []WeightedTuple{
		{Tuple: FlowTuple{SrcIP: mustIP(v4a), DstIP: mustIP(v4b), SrcPort: 1000, DstPort: 443, Proto: 6}, Bytes: 500, Packets: 5},
		{Tuple: FlowTuple{SrcIP: mustIP(v4a), DstIP: mustIP(v4b), SrcPort: 1000, DstPort: 443, Proto: 6}, Bytes: 500, Packets: 5},
	}
	pred := Predict(PolicyLayer34, slaves, tuples)

	if pred.Classified != 2 || pred.Unclassified != 0 {
		t.Fatalf("Classified=%d Unclassified=%d, want 2/0", pred.Classified, pred.Unclassified)
	}
	if len(pred.Slaves) != 2 {
		t.Fatalf("len(Slaves) = %d, want 2", len(pred.Slaves))
	}
	// Identical tuples must land on the identical slave (determinism —
	// same flow, same policy, same hash every time).
	var totalFlows int
	var totalBytes int64
	for _, s := range pred.Slaves {
		totalFlows += s.Flows
		totalBytes += s.Bytes
		if s.Flows != 0 && s.Flows != 2 {
			t.Fatalf("identical tuples split across slaves: %+v", pred.Slaves)
		}
	}
	if totalFlows != 2 {
		t.Fatalf("total predicted flows = %d, want 2", totalFlows)
	}
	if totalBytes != 1000 {
		t.Fatalf("total predicted bytes = %d, want 1000 (byte-weighted, not flow-count-only)", totalBytes)
	}
}

func TestPredict_DownSlaveExcluded(t *testing.T) {
	slaves := []Slave{
		{Ref: "physnic:pve1:eno1", Name: "eno1", Up: true},
		{Ref: "physnic:pve1:eno2", Name: "eno2", Up: false}, // down: never eligible
	}
	tuples := []WeightedTuple{
		{Tuple: FlowTuple{SrcIP: mustIP("10.0.0.1"), DstIP: mustIP("10.0.0.2"), SrcPort: 1, DstPort: 2, Proto: 6}, Bytes: 10, Packets: 1},
	}
	pred := Predict(PolicyLayer34, slaves, tuples)
	if len(pred.Slaves) != 1 {
		t.Fatalf("len(Slaves) = %d, want 1 (down slave excluded)", len(pred.Slaves))
	}
	if pred.Slaves[0].Ref != "physnic:pve1:eno1" {
		t.Fatalf("only Up slave should be present, got %+v", pred.Slaves)
	}
}

// TestPredict_NoEligibleSlaves is one of the honest-empty-states this
// task's report documents: a bond with zero Up slaves predicts nothing,
// and every tuple is counted (not silently dropped) as unclassified with
// ErrNoSlaves as the reason.
func TestPredict_NoEligibleSlaves(t *testing.T) {
	tuples := []WeightedTuple{
		{Tuple: FlowTuple{SrcIP: mustIP("10.0.0.1"), DstIP: mustIP("10.0.0.2"), Proto: 6}, Bytes: 10, Packets: 1},
	}
	pred := Predict(PolicyLayer34, nil, tuples)
	if len(pred.Slaves) != 0 {
		t.Fatalf("Slaves = %+v, want empty", pred.Slaves)
	}
	if pred.Unclassified != 1 || pred.Classified != 0 {
		t.Fatalf("Classified=%d Unclassified=%d, want 0/1", pred.Classified, pred.Unclassified)
	}
	if pred.UnclassifiedReason == "" {
		t.Fatal("UnclassifiedReason is empty, want ErrNoSlaves' message")
	}
}

// TestPredict_MACPolicyOnFlowDerivedTuples is the honesty case doc.go
// promises: layer2/layer2+3 tuples built from flow.Record data (no MAC
// available) are reported as unclassified with ErrMACRequired, never
// silently hashed against a zero/guessed MAC.
func TestPredict_MACPolicyOnFlowDerivedTuples(t *testing.T) {
	slaves := []Slave{{Ref: "physnic:pve1:eno1", Name: "eno1", Up: true}}
	rec := flow.Record{Node: "pve1", SrcIP: "10.0.0.1", DstIP: "10.0.0.2", Proto: 6, Bytes: 100, Packets: 1}
	wt, ok := FlowTupleFromRecord(rec)
	if !ok {
		t.Fatal("FlowTupleFromRecord rejected a well-formed record")
	}
	for _, policy := range []Policy{PolicyLayer2, PolicyLayer23, PolicyEncap23} {
		pred := Predict(policy, slaves, []WeightedTuple{wt})
		if pred.Unclassified != 1 || pred.Classified != 0 {
			t.Fatalf("policy %s: Classified=%d Unclassified=%d, want 0/1 (no MAC in flow-derived tuple)", policy, pred.Classified, pred.Unclassified)
		}
	}
	// The two IP-only policies must classify the exact same tuple fine.
	for _, policy := range []Policy{PolicyLayer34, PolicyEncap34} {
		pred := Predict(policy, slaves, []WeightedTuple{wt})
		if pred.Classified != 1 {
			t.Fatalf("policy %s: Classified=%d, want 1", policy, pred.Classified)
		}
	}
}

func TestFlowTupleFromRecord_MalformedIPRejected(t *testing.T) {
	rec := flow.Record{Node: "pve1", SrcIP: "not-an-ip", DstIP: "10.0.0.2", Proto: 6}
	if _, ok := FlowTupleFromRecord(rec); ok {
		t.Fatal("FlowTupleFromRecord accepted a malformed SrcIP")
	}
}

func TestFlowTupleFromRecord_ClampsOutOfRangePort(t *testing.T) {
	rec := flow.Record{Node: "pve1", SrcIP: "10.0.0.1", DstIP: "10.0.0.2", SrcPort: 70000, Proto: 6}
	wt, ok := FlowTupleFromRecord(rec)
	if !ok {
		t.Fatal("FlowTupleFromRecord rejected a well-formed record")
	}
	if wt.Tuple.SrcPort != 0 {
		t.Fatalf("SrcPort = %d, want 0 (out-of-range clamped, not wrapped)", wt.Tuple.SrcPort)
	}
}

func TestCompare_MergesActualByRef(t *testing.T) {
	pred := Prediction{
		Policy: PolicyLayer34,
		Slaves: []PredictedSlave{
			{Ref: "physnic:pve1:eno1", Name: "eno1", Flows: 3, Bytes: 300},
			{Ref: "physnic:pve1:eno2", Name: "eno2", Flows: 1, Bytes: 100},
		},
	}
	live := &metrics.LiveMetric{
		Ref: "bond:pve1:bond0",
		Slaves: []metrics.SlaveRate{
			{Ref: "physnic:pve1:eno1", Active: true, Rates: metrics.Rates{RxBps: 900, TxBps: 900}},
			// eno2 deliberately absent: no live sample yet for it.
		},
	}
	rows := Compare(pred, live)
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	if !rows[0].HasActual || rows[0].ActualRxBps != 900 {
		t.Fatalf("eno1 row = %+v, want HasActual with RxBps 900", rows[0])
	}
	if rows[1].HasActual {
		t.Fatalf("eno2 row = %+v, want HasActual=false (no live sample)", rows[1])
	}
}

func TestCompare_NilLiveMetric(t *testing.T) {
	pred := Prediction{Slaves: []PredictedSlave{{Ref: "physnic:pve1:eno1", Name: "eno1"}}}
	rows := Compare(pred, nil)
	if len(rows) != 1 || rows[0].HasActual {
		t.Fatalf("rows = %+v, want one row with HasActual=false", rows)
	}
}

func mustIP(s string) net.IP {
	ip := net.ParseIP(s)
	if ip == nil {
		panic("bad test IP: " + s)
	}
	return ip
}
