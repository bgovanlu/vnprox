package auth

import (
	"sync"
	"time"
)

// tokenUseAggregator is T-2903's hourly rollup for token.use audit rows.
// Bounded by construction: one small entry per token seen in the last two
// hours, swept opportunistically on every call — the same
// no-unbounded-map discipline internal/peer's replayCache follows (and the
// login limiter's buckets map, until T-2905, did not).
type tokenUseAggregator struct {
	// byToken maps token ID -> the UTC hour currently being counted and
	// how many uses it has seen.
	byToken map[string]*tokenUseWindow
	mu      sync.Mutex
}

type tokenUseWindow struct {
	hourStart int64 // unix seconds, truncated to the hour
	count     int
}

func newTokenUseAggregator() *tokenUseAggregator {
	return &tokenUseAggregator{byToken: map[string]*tokenUseWindow{}}
}

// observe records one use of tokenID at now. emit is true when the caller
// should append an audit row — the first use in each UTC hour — and
// prevCount then carries the finished previous hour's request count (0 when
// there is no previous hour, i.e. the first row ever or after an idle gap).
func (a *tokenUseAggregator) observe(tokenID string, now time.Time) (emit bool, prevCount int) {
	hour := now.UTC().Truncate(time.Hour).Unix()

	a.mu.Lock()
	defer a.mu.Unlock()

	// Opportunistic sweep: entries idle for 2+ hours are dead weight — their
	// prevHourCount can no longer be attributed to an adjacent row anyway.
	for id, w := range a.byToken {
		if hour-w.hourStart >= 2*3600 {
			delete(a.byToken, id)
		}
	}

	w, ok := a.byToken[tokenID]
	if !ok {
		a.byToken[tokenID] = &tokenUseWindow{hourStart: hour, count: 1}
		return true, 0
	}
	if w.hourStart == hour {
		w.count++
		return false, 0
	}
	prev := 0
	if hour-w.hourStart < 2*3600 { // adjacent hour: the count is attributable
		prev = w.count
	}
	w.hourStart, w.count = hour, 1
	return true, prev
}
