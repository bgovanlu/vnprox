// SPDX-License-Identifier: Apache-2.0

package lacphash

// Slave is the ordered slave identity Predict needs — a minimal projection
// of inventory.Bond's Slaves/SlaveDetail fields, kept narrow deliberately
// (rather than importing internal/inventory.Bond directly) so this package
// stays a small, dependency-light composition a caller adapts its own
// richer type into.
type Slave struct {
	// Ref is the slave's inventory Ref string (e.g.
	// "physnic:pve1:eno1") — carried through unchanged into
	// PredictedSlave/SlaveComparison so a caller can correlate a result
	// row back to the topology map or to metrics.SlaveRate.Ref without a
	// second lookup.
	Ref  string
	Name string
	// Up reports whether this slave is currently eligible to carry
	// traffic (MII up). Only Up slaves are counted — a down slave never
	// receives the kernel hash's output, mirroring the real aggregate.
	Up bool
}

// WeightedTuple pairs a FlowTuple with the byte/packet weight the observed
// flow record it came from carried, so Predict's per-slave totals reflect
// observed volume, not just a bare flow count.
type WeightedTuple struct {
	Tuple   FlowTuple
	Bytes   int64
	Packets int64
}

// PredictedSlave is one eligible slave's predicted share of the input flow
// set under a policy.
type PredictedSlave struct {
	Ref     string
	Name    string
	Flows   int
	Bytes   int64
	Packets int64
}

// Prediction is Predict's result for one bond. Field order here follows
// docs/development.md's fieldalignment convention (strings before slices,
// then plain ints) rather than declaration/narrative order — Policy and
// UnclassifiedReason are two-word string headers, Slaves is a three-word
// slice header, Classified/Unclassified are single-word ints. Verified
// against `golangci-lint run` (govet's fieldalignment check), not just
// asserted.
type Prediction struct {
	Policy             Policy
	UnclassifiedReason string
	// Slaves has one entry per Up input slave, in the same order Predict
	// was given them — always present (possibly all-zero), so a caller
	// never has to guess which slaves exist from which happened to have
	// a flow land on them.
	Slaves       []PredictedSlave
	Classified   int
	Unclassified int
}

// Predict buckets each of tuples into one of slaves (Up slaves only — see
// Slave.Up) under policy, weighting each bucket by the tuple's own
// byte/packet count. It never errors: a tuple Hash can't classify under
// policy is counted in Unclassified/UnclassifiedReason instead (see
// doc.go's MAC-availability gap for the case this exists to cover), and a
// bond with zero eligible slaves returns a Prediction with an empty Slaves
// and every tuple unclassified (SelectSlave's ErrNoSlaves as the reason).
// The caller — the API/UI layer, not this pure function — decides how to
// render each of those cases as an honest empty state; Predict itself
// never panics or returns a Go error for an input shape it can fully
// describe in the result.
func Predict(policy Policy, slaves []Slave, tuples []WeightedTuple) Prediction {
	up := make([]Slave, 0, len(slaves))
	for _, s := range slaves {
		if s.Up {
			up = append(up, s)
		}
	}

	pred := Prediction{
		Policy: policy,
		Slaves: make([]PredictedSlave, len(up)),
	}
	for i, s := range up {
		pred.Slaves[i] = PredictedSlave{Ref: s.Ref, Name: s.Name}
	}

	for _, wt := range tuples {
		idx, err := SelectSlave(policy, wt.Tuple, len(up))
		if err != nil {
			pred.Unclassified++
			if pred.UnclassifiedReason == "" {
				pred.UnclassifiedReason = err.Error()
			}
			continue
		}
		pred.Classified++
		pred.Slaves[idx].Flows++
		pred.Slaves[idx].Bytes += wt.Bytes
		pred.Slaves[idx].Packets += wt.Packets
	}
	return pred
}
