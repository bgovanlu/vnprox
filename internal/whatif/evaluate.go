// SPDX-License-Identifier: Apache-2.0

package whatif

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/capacity"
	"github.com/bgovanlu/vnprox/internal/failsim"
)

// Evaluate answers "what breaks first if we add req.N guests of
// req.Profile" by evaluating all three axes and reporting which one binds
// (breaks at the lowest guest count). It performs no I/O and persists
// nothing (see doc.go).
func Evaluate(req Request) Verdict {
	v := Verdict{Profile: req.Profile, N: req.N}
	v.Capacity = evaluateCapacity(req)
	v.IPAM = evaluateIPAM(req)
	v.Failsim = evaluateFailsim(req)

	type candidate struct {
		name string
		n    int
	}
	var candidates []candidate
	if v.Capacity.Status == AxisBreaks && v.Capacity.BreaksAtN != nil {
		candidates = append(candidates, candidate{"capacity", *v.Capacity.BreaksAtN})
	}
	if v.IPAM.Status == AxisBreaks && v.IPAM.BreaksAtN != nil {
		candidates = append(candidates, candidate{"ipam", *v.IPAM.BreaksAtN})
	}
	if v.Failsim.Status == AxisBreaks && v.Failsim.BreaksAtN != nil {
		candidates = append(candidates, candidate{"failsim-impact", *v.Failsim.BreaksAtN})
	}
	// Deterministic tie-break: stable sort by N, then by the fixed
	// capacity < ipam < failsim-impact listing order above (candidates was
	// already built in that order, and sort.SliceStable preserves it for
	// equal N).
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].n < candidates[j].n })
	if len(candidates) > 0 {
		v.Binding = candidates[0].name
		n := candidates[0].n
		v.BindingAtN = &n
	}

	if v.Capacity.Status == AxisUnavailable {
		v.Unavailable = append(v.Unavailable, "capacity")
	}
	if v.IPAM.Status == AxisUnavailable {
		v.Unavailable = append(v.Unavailable, "ipam")
	}
	if v.Failsim.Status == AxisUnavailable {
		v.Unavailable = append(v.Unavailable, "failsim-impact")
	}

	v.Summary = summarize(v)
	return v
}

func summarize(v Verdict) string {
	base := fmt.Sprintf("adding %d guest(s) of profile %q", v.N, v.Profile.Name)
	s := fmt.Sprintf("%s: no evaluated axis breaks within N.", base)
	if v.Binding != "" {
		s = fmt.Sprintf("%s: %s is the binding constraint, breaking at N=%d.", base, v.Binding, *v.BindingAtN)
	}
	if len(v.Unavailable) > 0 {
		s += fmt.Sprintf(" (not evaluated: %v — treat as unknown, not as unconstrained)", v.Unavailable)
	}
	return s
}

// --- capacity axis -----------------------------------------------------

func evaluateCapacity(req Request) CapacityAxis {
	in := req.Capacity
	if in.LinkSpeedMbps <= 0 {
		return CapacityAxis{Status: AxisUnavailable, Reason: "link speed unknown for " + in.LinkRef}
	}
	if len(in.History) == 0 {
		return CapacityAxis{Status: AxisUnavailable, Reason: "no capacity rollup history yet for " + in.LinkRef}
	}

	latest := in.History[0]
	for _, a := range in.History[1:] {
		if a.BucketAt.After(latest.BucketAt) {
			latest = a
		}
	}

	addedPct := req.Profile.ExpectedMbps / float64(in.LinkSpeedMbps) * 100
	res := capacity.WhatIf(latest, addedPct, req.N)

	basis := fmt.Sprintf(
		"estimate: most recently observed peak utilization (%.1f%% on %s), derived from a daily rollup of a bounded metrics ring; assumes each added guest adds a constant %.2f Mbps (%.2f%% of the link's %d Mbps) with no additional organic growth beyond the added guests",
		latest.MaxUtil, latest.BucketAt.Format("2006-01-02"), req.Profile.ExpectedMbps, addedPct, in.LinkSpeedMbps,
	)

	axis := CapacityAxis{
		ConsumedPct:      res.ConsumedPctAtN,
		Basis:            basis,
		Estimated:        true,
		AlreadyOverToday: res.AlreadyOverToday,
	}
	switch {
	case res.AlreadyOverToday:
		axis.Status = AxisBreaks
		zero := 0
		axis.BreaksAtN = &zero
	case res.BreaksAtN != nil:
		axis.Status = AxisBreaks
		axis.BreaksAtN = res.BreaksAtN
	default:
		axis.Status = AxisOK
	}
	return axis
}

// --- ipam axis -----------------------------------------------------------

func evaluateIPAM(req Request) IPAMAxis {
	if len(req.IPAM.Subnets) == 0 {
		return IPAMAxis{Status: AxisUnavailable, Reason: "no IPAM pool resolved for this attachment"}
	}
	addrsPerGuest := max(1, req.Profile.NICCount)

	var tightest *IPAMAxis
	for _, sn := range req.IPAM.Subnets {
		free := sn.Total - sn.Allocated
		if free < 0 {
			free = 0
		}
		axis := IPAMAxis{
			Subnet:        sn.CIDR,
			FreeAddresses: free,
			AddrsPerGuest: addrsPerGuest,
			Estimated:     false,
		}
		if free < addrsPerGuest*req.N {
			breaks := free/addrsPerGuest + 1
			axis.Status = AxisBreaks
			axis.BreaksAtN = &breaks
		} else {
			axis.Status = AxisOK
		}
		if tightest == nil || axisBreaksEarlier(axis, *tightest) {
			cp := axis
			tightest = &cp
		}
	}
	return *tightest
}

// axisBreaksEarlier reports whether a's exhaustion point is more binding
// than b's — a breaks and b doesn't, or both break and a's N is lower.
func axisBreaksEarlier(a, b IPAMAxis) bool {
	if a.Status == AxisBreaks && b.Status != AxisBreaks {
		return true
	}
	if a.Status == AxisBreaks && b.Status == AxisBreaks {
		return *a.BreaksAtN < *b.BreaksAtN
	}
	return false
}

// --- failsim axis ----------------------------------------------------------

func evaluateFailsim(req Request) FailsimAxis {
	in := req.Failsim
	if in.Target.IsZero() {
		return FailsimAxis{Status: AxisUnavailable, Reason: "no failure target specified"}
	}
	if in.Snapshot.Len() == 0 {
		return FailsimAxis{Status: AxisUnavailable, Reason: "no inventory snapshot supplied"}
	}

	simIn := failsim.Input{Corosync: in.Corosync, Ceph: in.Ceph, Tunnels: in.Tunnels}

	simIn.Snapshot = in.Snapshot
	before := failsim.Simulate(simIn, in.Target)

	synthetic := syntheticGuests(req.Profile, req.N)
	augmented := augmentSnapshot(in.Snapshot, synthetic)
	simIn.Snapshot = augmented
	after := failsim.Simulate(simIn, in.Target)

	if !hasDim(before.NotEvaluated, failsim.DimGuestConnectivity) && hasDim(after.NotEvaluated, failsim.DimGuestConnectivity) {
		return FailsimAxis{
			Status: AxisUnavailable,
			Before: before,
			Reason: "could not resolve the profile's attachment (" + req.Profile.Attachment.Name + ") to a bridge or VNet — failure impact for the added guests is unknown",
		}
	}

	added := len(after.DisconnectedGuests) - len(before.DisconnectedGuests)
	if added < 0 {
		added = 0
	}
	axis := FailsimAxis{
		Status:            AxisOK,
		Before:            before,
		After:             after,
		AddedDisconnected: added,
	}
	if added > 0 {
		axis.Status = AxisBreaks
		one := 1
		axis.BreaksAtN = &one
	}
	return axis
}

func hasDim(dims []string, want string) bool {
	for _, d := range dims {
		if d == want {
			return true
		}
	}
	return false
}
