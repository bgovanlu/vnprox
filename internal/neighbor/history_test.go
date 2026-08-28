// SPDX-License-Identifier: Apache-2.0

package neighbor

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeHistoryStore is a hand-rolled in-memory HistoryStore test double,
// mirroring internal/flow's own in-memory FlowStore fakes.
type fakeHistoryStore struct {
	rows   []store.NeighborBinding
	nextID int64
}

func (f *fakeHistoryStore) Insert(_ context.Context, b store.NeighborBinding) error {
	f.nextID++
	b.ID = f.nextID
	f.rows = append(f.rows, b)
	return nil
}

func (f *fakeHistoryStore) LatestByIP(_ context.Context, node string) (map[string]store.NeighborBinding, error) {
	out := map[string]store.NeighborBinding{}
	for _, b := range f.rows {
		if b.Node != node {
			continue
		}
		if cur, ok := out[b.IP]; !ok || b.ID > cur.ID {
			out[b.IP] = b
		}
	}
	return out, nil
}

func (f *fakeHistoryStore) Query(_ context.Context, filter store.NeighborBindingFilter, _ string, limit int) ([]store.NeighborBinding, string, error) {
	var out []store.NeighborBinding
	for i := len(f.rows) - 1; i >= 0; i-- {
		b := f.rows[i]
		if filter.Node != "" && b.Node != filter.Node {
			continue
		}
		if filter.IP != "" && b.IP != filter.IP {
			continue
		}
		if filter.MAC != "" && b.MAC != filter.MAC {
			continue
		}
		out = append(out, b)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, "", nil
}

func (f *fakeHistoryStore) PruneOlderThan(_ context.Context, cutoff int64) (int64, error) {
	var kept []store.NeighborBinding
	var n int64
	for _, b := range f.rows {
		if b.At < cutoff {
			n++
			continue
		}
		kept = append(kept, b)
	}
	f.rows = kept
	return n, nil
}

func (f *fakeHistoryStore) PruneToCap(_ context.Context, maxRows int64) (int64, error) {
	if maxRows <= 0 || int64(len(f.rows)) <= maxRows {
		return 0, nil
	}
	n := int64(len(f.rows)) - maxRows
	f.rows = f.rows[n:]
	return n, nil
}

func (f *fakeHistoryStore) CandidateIPsSince(_ context.Context, node string, since int64) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, b := range f.rows {
		if b.Node != node || b.At < since {
			continue
		}
		if !seen[b.IP] {
			seen[b.IP] = true
			out = append(out, b.IP)
		}
	}
	return out, nil
}

func (f *fakeHistoryStore) CandidateMACsSince(_ context.Context, node string, since int64) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, b := range f.rows {
		if b.Node != node || b.At < since {
			continue
		}
		if !seen[b.MAC] {
			seen[b.MAC] = true
			out = append(out, b.MAC)
		}
	}
	return out, nil
}

func (f *fakeHistoryStore) CountSince(_ context.Context, node, ip, mac string, since int64) (int64, error) {
	var n int64
	for _, b := range f.rows {
		if b.Node != node || b.At < since || !b.PrevMAC.Valid {
			continue
		}
		if ip != "" && b.IP != ip {
			continue
		}
		if mac != "" && b.MAC != mac {
			continue
		}
		n++
	}
	return n, nil
}

func (f *fakeHistoryStore) DistinctIPsSince(_ context.Context, node, mac string, since int64) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, b := range f.rows {
		if b.Node != node || b.MAC != mac || b.At < since {
			continue
		}
		if !seen[b.IP] {
			seen[b.IP] = true
			out = append(out, b.IP)
		}
	}
	return out, nil
}

func newTestRecorder(t *testing.T, h *fakeHost, s *fakeHistoryStore, node string, now time.Time) *HistoryRecorder {
	t.Helper()
	clock := now
	return NewHistoryRecorder(HistoryConfig{
		Host:      h,
		Store:     s,
		LocalNode: func() string { return node },
		Now:       func() time.Time { return clock },
	})
}

func TestHistoryRecorder_Poll_FirstSeenIsNotAChange(t *testing.T) {
	h := &fakeHost{neighbors: map[string][]host.Neighbor{
		"pve1": {{IP: "10.0.0.1", MAC: "aa:aa:aa:aa:aa:01", Iface: "vmbr0", State: host.NeighborReachable}},
	}}
	s := &fakeHistoryStore{}
	r := newTestRecorder(t, h, s, "pve1", time.Unix(1000, 0))

	changes, err := r.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	if !changes[0].FirstSeen {
		t.Fatalf("expected FirstSeen=true for a never-before-seen IP")
	}
	if changes[0].PrevMAC.Valid {
		t.Fatalf("expected NULL prev_mac for a first-seen row, got %+v", changes[0].PrevMAC)
	}
}

func TestHistoryRecorder_Poll_UnchangedBindingWritesNothing(t *testing.T) {
	h := &fakeHost{neighbors: map[string][]host.Neighbor{
		"pve1": {{IP: "10.0.0.1", MAC: "aa:aa:aa:aa:aa:01", Iface: "vmbr0", State: host.NeighborReachable}},
	}}
	s := &fakeHistoryStore{}
	r := newTestRecorder(t, h, s, "pve1", time.Unix(1000, 0))
	ctx := context.Background()

	if _, err := r.Poll(ctx); err != nil {
		t.Fatalf("Poll 1: %v", err)
	}
	if len(s.rows) != 1 {
		t.Fatalf("after first poll, got %d rows, want 1", len(s.rows))
	}

	// Second poll, same observation, later time: must not write a second
	// row — this is the "append-on-change, not append-on-every-poll"
	// contract the whole ring's bound depends on.
	changes, err := r.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll 2: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("got %d changes on an unchanged poll, want 0", len(changes))
	}
	if len(s.rows) != 1 {
		t.Fatalf("after second (unchanged) poll, got %d rows, want still 1", len(s.rows))
	}
}

func TestHistoryRecorder_Poll_RebindIsRecordedWithPrevMAC(t *testing.T) {
	h := &fakeHost{neighbors: map[string][]host.Neighbor{
		"pve1": {{IP: "10.0.0.1", MAC: "aa:aa:aa:aa:aa:01", Iface: "vmbr0", State: host.NeighborReachable}},
	}}
	s := &fakeHistoryStore{}
	r := newTestRecorder(t, h, s, "pve1", time.Unix(1000, 0))
	ctx := context.Background()

	if _, err := r.Poll(ctx); err != nil {
		t.Fatalf("Poll 1: %v", err)
	}

	// A different MAC now answers for the same IP.
	h.neighbors["pve1"][0].MAC = "aa:aa:aa:aa:aa:02"
	changes, err := r.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll 2: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	if changes[0].FirstSeen {
		t.Fatalf("a rebind must not report FirstSeen=true")
	}
	if !changes[0].PrevMAC.Valid || changes[0].PrevMAC.String != "aa:aa:aa:aa:aa:01" {
		t.Fatalf("PrevMAC = %+v, want the earlier MAC", changes[0].PrevMAC)
	}
	if changes[0].MAC != "aa:aa:aa:aa:aa:02" {
		t.Fatalf("MAC = %q, want the new MAC", changes[0].MAC)
	}
}

func TestHistoryRecorder_Poll_NodeNotYetDiscoveredIsANoOp(t *testing.T) {
	h := &fakeHost{neighbors: map[string][]host.Neighbor{
		"pve1": {{IP: "10.0.0.1", MAC: "aa:aa:aa:aa:aa:01"}},
	}}
	s := &fakeHistoryStore{}
	r := NewHistoryRecorder(HistoryConfig{Host: h, Store: s, LocalNode: func() string { return "" }})

	changes, err := r.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(changes) != 0 || len(s.rows) != 0 {
		t.Fatalf("expected a no-op before local node discovery, got %d changes / %d rows", len(changes), len(s.rows))
	}
}

// insertTransitions inserts n synthetic MAC-change rows for (node, ip),
// spaced 1 second apart starting at startAt, each with a distinct MAC so
// every row after the first has a non-NULL prev_mac (a genuine transition).
func insertTransitions(t *testing.T, s *fakeHistoryStore, node, ip string, n int, startAt int64) {
	t.Helper()
	var prevMAC sql.NullString
	for i := 0; i < n; i++ {
		mac := macN(i)
		b := store.NeighborBinding{At: startAt + int64(i), Node: node, IP: ip, MAC: mac, PrevMAC: prevMAC}
		if err := s.Insert(context.Background(), b); err != nil {
			t.Fatalf("seeding transition %d: %v", i, err)
		}
		prevMAC = sql.NullString{String: mac, Valid: true}
	}
}

func macN(i int) string {
	hex := "0123456789abcdef"
	return "aa:aa:aa:aa:aa:" + string(hex[(i/16)%16]) + string(hex[i%16])
}

// TestHistoryRecorder_Flaps_IPChurn_ThresholdBoundary is the card's own
// explicit ask: table-driven coverage of the flap threshold's boundary
// (IPFlapThreshold=3 within IPFlapWindow=2m).
func TestHistoryRecorder_Flaps_IPChurn_ThresholdBoundary(t *testing.T) {
	tests := []struct {
		name        string
		transitions int // genuine transitions (non-NULL prev_mac) within the window
		wantFlap    bool
	}{
		{"zero transitions: stable binding, no flap", 0, false},
		{"one transition: a clean single rebind, not a flap", 1, false},
		{"two transitions: just under threshold, not yet a flap", IPFlapThreshold - 1, false},
		{"exactly at threshold: a flap", IPFlapThreshold, true},
		{"over threshold: still a flap", IPFlapThreshold + 2, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeHistoryStore{}
			now := time.Unix(10_000, 0)
			// First row (first-seen, NULL prev_mac) plus tc.transitions
			// genuine rebinds, all within the window.
			insertTransitions(t, s, "pve1", "10.0.0.1", tc.transitions+1, now.Add(-30*time.Second).Unix())

			r := newTestRecorder(t, &fakeHost{}, s, "pve1", now)
			flaps, err := r.Flaps(context.Background(), now)
			if err != nil {
				t.Fatalf("Flaps: %v", err)
			}
			got := hasFlap(flaps, FlapKindIPChurn, "10.0.0.1")
			if got != tc.wantFlap {
				t.Fatalf("transitions=%d: got flap=%v, want %v (flaps=%+v)", tc.transitions, got, tc.wantFlap, flaps)
			}
		})
	}
}

// TestHistoryRecorder_Flaps_IPChurn_OutsideWindowDoesNotCount verifies old
// transitions outside IPFlapWindow don't contribute to the count, even
// when there are enough of them in total.
func TestHistoryRecorder_Flaps_IPChurn_OutsideWindowDoesNotCount(t *testing.T) {
	s := &fakeHistoryStore{}
	now := time.Unix(10_000, 0)
	// IPFlapThreshold genuine transitions, but all well before the window
	// (started way more than IPFlapWindow ago).
	insertTransitions(t, s, "pve1", "10.0.0.1", IPFlapThreshold+1, now.Add(-2*IPFlapWindow).Unix())

	r := newTestRecorder(t, &fakeHost{}, s, "pve1", now)
	flaps, err := r.Flaps(context.Background(), now)
	if err != nil {
		t.Fatalf("Flaps: %v", err)
	}
	if hasFlap(flaps, FlapKindIPChurn, "10.0.0.1") {
		t.Fatalf("stale transitions outside the window must not count as a flap: %+v", flaps)
	}
}

func TestHistoryRecorder_Flaps_MACClaim_ThresholdBoundary(t *testing.T) {
	tests := []struct {
		name     string
		ips      int
		wantFlap bool
	}{
		{"one IP: an ordinary single binding", 1, false},
		{"just under threshold", MACClaimThreshold - 1, false},
		{"exactly at threshold", MACClaimThreshold, true},
		{"over threshold", MACClaimThreshold + 3, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeHistoryStore{}
			now := time.Unix(10_000, 0)
			mac := "bb:bb:bb:bb:bb:01"
			for i := 0; i < tc.ips; i++ {
				ip := ipN(i)
				b := store.NeighborBinding{At: now.Add(-30 * time.Second).Unix(), Node: "pve1", IP: ip, MAC: mac}
				if err := s.Insert(context.Background(), b); err != nil {
					t.Fatalf("seeding claim %d: %v", i, err)
				}
			}
			r := newTestRecorder(t, &fakeHost{}, s, "pve1", now)
			flaps, err := r.Flaps(context.Background(), now)
			if err != nil {
				t.Fatalf("Flaps: %v", err)
			}
			got := hasFlap(flaps, FlapKindMACClaim, mac)
			if got != tc.wantFlap {
				t.Fatalf("ips=%d: got flap=%v, want %v (flaps=%+v)", tc.ips, got, tc.wantFlap, flaps)
			}
		})
	}
}

func ipN(i int) string {
	return "10.0.0." + string(rune('1'+i))
}

func hasFlap(flaps []FlapEvent, kind FlapKind, key string) bool {
	for _, f := range flaps {
		if f.Kind != kind {
			continue
		}
		if kind == FlapKindIPChurn && f.IP == key {
			return true
		}
		if kind == FlapKindMACClaim && f.MAC == key {
			return true
		}
	}
	return false
}
