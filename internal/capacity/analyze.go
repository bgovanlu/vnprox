package capacity

import (
	"fmt"
	"sort"
	"time"
)

// Analyze groups aggregates by (ref, kind), fits a Forecast to each group, and
// returns a ForecastFinding for every ref projected to cross capacity within
// horizonDays. Refs whose trend is flat/decreasing or whose crossing is beyond
// the horizon contribute nothing (Forecast returns a nil CrossesAt), so a
// stable network raises no capacity findings at all.
//
// The result is deterministically ordered by (kind, ref) so a re-run over
// unchanged aggregates produces byte-identical findings — the stable-id
// property the findings engine's change/notify tracking depends on.
func Analyze(aggregates []Aggregate, horizonDays int) []ForecastFinding {
	if horizonDays <= 0 {
		horizonDays = DefaultForecastHorizonDays
	}

	type key struct {
		ref  string
		kind Kind
	}
	groups := make(map[key][]Aggregate)
	order := make([]key, 0)
	for _, a := range aggregates {
		k := key{ref: a.Ref, kind: a.Kind}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], a)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].kind != order[j].kind {
			return order[i].kind < order[j].kind
		}
		return order[i].ref < order[j].ref
	})

	var out []ForecastFinding
	for _, k := range order {
		proj := Forecast(groups[k], horizonDays)
		if proj.CrossesAt == nil {
			continue
		}
		out = append(out, ForecastFinding{
			Ref:         k.ref,
			Kind:        k.kind,
			Check:       checkFor(k.kind),
			CrossesAt:   *proj.CrossesAt,
			HorizonDays: horizonDays,
			Confidence:  proj.Confidence,
			Detail:      forecastDetail(k.ref, k.kind, *proj.CrossesAt, horizonDays, len(groups[k]), proj.Confidence),
		})
	}
	return out
}

// checkFor maps a Kind to its findings `check` value.
func checkFor(kind Kind) string {
	if kind == KindIPAMPool {
		return CheckIPAMForecast
	}
	return CheckLinkForecast
}

// forecastDetail renders the plain-English finding detail, naming the
// projected exhaustion date and the horizon used (T-1606's detail contract).
func forecastDetail(ref string, kind Kind, crossesAt time.Time, horizonDays, samples int, confidence float64) string {
	subject := "link " + ref + " utilization"
	verb := "reach capacity"
	if kind == KindIPAMPool {
		subject = "IPAM pool " + ref + " allocation"
		verb = "exhaust its address space"
	}
	return fmt.Sprintf(
		"%s is trending up and is projected to %s around %s — within the %d-day forecast horizon (linear trend over %d daily rollups, fit confidence %.0f%%)",
		subject, verb, crossesAt.Format("2006-01-02"), horizonDays, samples, confidence*100,
	)
}
