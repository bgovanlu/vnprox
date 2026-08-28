// SPDX-License-Identifier: Apache-2.0

package baseline

import (
	"fmt"
	"sort"
	"time"

	"github.com/bgovanlu/vnprox/internal/flow"
)

// AnomalyClass names one of the three deviation classes Detect raises.
type AnomalyClass string

const (
	// ClassNewPort — a src/dst pair using a service port never seen in the
	// learning window.
	ClassNewPort AnomalyClass = "new_port"
	// ClassVolumeSpike — a wall-clock hour whose byte volume is >= the
	// configured multiple of that hour-of-day's baseline mean+stddev.
	ClassVolumeSpike AnomalyClass = "volume_spike"
	// ClassNewSubnet — the Ref communicated with a peer CIDR never observed
	// in the learning window.
	ClassNewSubnet AnomalyClass = "new_subnet"
)

// Anomaly is one explainable deviation from a learned Profile. It always names
// the baseline it deviated from (BaselineWindow, BaselineValue) and the
// deviation's magnitude (ObservedValue, DeviationFactor) — the machine-
// checkable "names its baseline and the deviation" contract T-1601's card
// requires. Subject is the specific port ("tcp/6667"), subnet ("10.9.0.0/24"),
// or hour bucket the anomaly concerns, and forms the finding's stable id.
//
// Field semantics by class:
//   - new_port / new_subnet (categorical): BaselineValue is 0 (the subject was
//     never observed while learning); ObservedValue is how many times it was
//     observed in the recent window; DeviationFactor mirrors ObservedValue
//     (baseline was zero — a wholly new observation).
//   - volume_spike (quantitative): BaselineValue is the hour-of-day's baseline
//     mean+stddev in bytes; ObservedValue is the observed hour's bytes;
//     DeviationFactor is ObservedValue/BaselineValue.
type Anomaly struct {
	Ref             string
	Class           AnomalyClass
	Subject         string
	BaselineWindow  Window
	BaselineValue   float64
	ObservedValue   float64
	DeviationFactor float64
}

// DetectConfig tunes Detect. A zero value is valid — withDefaults fills every
// non-positive field from the package defaults.
type DetectConfig struct {
	VolumeSpikeMultiple float64
	SubnetPrefixV4      int
	SubnetPrefixV6      int
}

// DefaultDetectConfig returns the documented defaults (10x volume spike, /24
// IPv4 and /64 IPv6 subnet aggregation).
func DefaultDetectConfig() DetectConfig {
	return DetectConfig{
		VolumeSpikeMultiple: DefaultVolumeSpikeMultiple,
		SubnetPrefixV4:      DefaultSubnetPrefixV4,
		SubnetPrefixV6:      DefaultSubnetPrefixV6,
	}
}

func (c DetectConfig) withDefaults() DetectConfig {
	if c.VolumeSpikeMultiple <= 0 {
		c.VolumeSpikeMultiple = DefaultVolumeSpikeMultiple
	}
	if c.SubnetPrefixV4 <= 0 {
		c.SubnetPrefixV4 = DefaultSubnetPrefixV4
	}
	if c.SubnetPrefixV6 <= 0 {
		c.SubnetPrefixV6 = DefaultSubnetPrefixV6
	}
	return c
}

// Detect replays recent flows against profile and returns the deviations, in
// a deterministic order (by class, then subject). An empty/absent baseline
// (profile.Empty) raises nothing: there is nothing to deviate from, so a
// cold-start Ref is silent (T-1601 AC5). Feeding a Profile's own
// learning-window flows back in raises nothing either — every port/subnet is
// already in the baseline set, and each hour's observed volume is one of the
// samples its own mean+stddev was computed from, so its ratio never reaches
// the (>=5x) spike multiple (T-1601 AC1).
func Detect(profile Profile, recent []flow.Record, cfg DetectConfig) []Anomaly {
	if profile.Empty() {
		return nil
	}
	cfg = cfg.withDefaults()

	portSet := make(map[PortKey]bool, len(profile.Ports))
	for _, p := range profile.Ports {
		portSet[p] = true
	}
	subnetSet := make(map[string]bool, len(profile.Subnets))
	for _, s := range profile.Subnets {
		subnetSet[s] = true
	}

	newPortObs := map[PortKey]float64{}
	newSubnetObs := map[string]float64{}
	bytesByHour := map[int64]int64{}

	for _, rec := range recent {
		m, ok := recordForRef(rec, profile.Ref)
		if !ok {
			continue
		}
		if m.hasPort && !portSet[m.port] {
			newPortObs[m.port]++
		}
		if sn, ok := peerSubnet(m.peerIP, cfg.SubnetPrefixV4, cfg.SubnetPrefixV6); ok && !subnetSet[sn] {
			newSubnetObs[sn]++
		}
		bytesByHour[rec.At/secondsPerHour] += rec.Bytes
	}

	var out []Anomaly
	for port, count := range newPortObs {
		out = append(out, Anomaly{
			Ref:             profile.Ref,
			Class:           ClassNewPort,
			Subject:         port.String(),
			BaselineWindow:  profile.Window,
			BaselineValue:   0,
			ObservedValue:   count,
			DeviationFactor: count,
		})
	}
	for sn, count := range newSubnetObs {
		out = append(out, Anomaly{
			Ref:             profile.Ref,
			Class:           ClassNewSubnet,
			Subject:         sn,
			BaselineWindow:  profile.Window,
			BaselineValue:   0,
			ObservedValue:   count,
			DeviationFactor: count,
		})
	}
	for absHour, obs := range bytesByHour {
		stat := profile.Hours[hourOfDay(absHour)]
		base := stat.Mean + stat.Stddev
		if base <= 0 {
			continue // no established baseline for that hour-of-day
		}
		obsF := float64(obs)
		factor := obsF / base
		if factor >= cfg.VolumeSpikeMultiple {
			out = append(out, Anomaly{
				Ref:             profile.Ref,
				Class:           ClassVolumeSpike,
				Subject:         hourSubject(absHour),
				BaselineWindow:  profile.Window,
				BaselineValue:   base,
				ObservedValue:   obsF,
				DeviationFactor: factor,
			})
		}
	}

	sortAnomalies(out)
	return out
}

// hourSubject renders a wall-clock hour bucket as a stable, human-legible
// subject (e.g. "2024-01-15T14:00Z"). Deterministic from absHour, so a
// persisting spike keeps the same finding id across detection cycles.
func hourSubject(absHour int64) string {
	return time.Unix(absHour*secondsPerHour, 0).UTC().Format("2006-01-02T15:00Z")
}

func sortAnomalies(a []Anomaly) {
	sort.Slice(a, func(i, j int) bool {
		if a[i].Class != a[j].Class {
			return a[i].Class < a[j].Class
		}
		return a[i].Subject < a[j].Subject
	})
}

// String renders an Anomaly compactly for logs/debugging (never the finding
// detail — that is internal/findings' job).
func (a Anomaly) String() string {
	return fmt.Sprintf("%s{ref=%s subject=%s baseline=%.1f observed=%.1f factor=%.2f}",
		a.Class, a.Ref, a.Subject, a.BaselineValue, a.ObservedValue, a.DeviationFactor)
}
