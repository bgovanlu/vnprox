package api

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// fakeItem is a synthetic keyset-paginated row for exercising
// mergeClusterPage without any real store/HTTP involved: node identifies
// its origin source, (at,tie) is its sort key, val is a unique payload for
// equality checks.
type fakeItem struct {
	node string
	tie  string
	val  string
	at   int64
}

// sortDesc sorts items exactly the way mergeClusterPage's own merge key
// orders them (at desc, tie desc, node desc) — used both to pre-sort each
// fake node's own store (ListPage-equivalent order) and to compute the
// property test's expected global order.
func sortDesc(items []fakeItem) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.at != b.at {
			return a.at > b.at
		}
		if a.tie != b.tie {
			return a.tie > b.tie
		}
		return a.node > b.node
	})
}

func decodeTestKeysetCursor(cursor string) (int64, string, error) {
	atStr, tie, ok := strings.Cut(cursor, ":")
	if !ok {
		return 0, "", fmt.Errorf("bad cursor %q", cursor)
	}
	at, err := strconv.ParseInt(atStr, 10, 64)
	if err != nil {
		return 0, "", err
	}
	return at, tie, nil
}

// fakeListPage mimics store.AuditRepo.ListPage/store.SnapshotRepo.ListPage's
// cursor contract over an in-memory, already-descending-sorted slice.
func fakeListPage(items []fakeItem, cursor string, limit int) ([]fakeItem, string, error) {
	start := 0
	if cursor != "" {
		at, tie, err := decodeTestKeysetCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		start = len(items)
		for i, it := range items {
			if it.at < at || (it.at == at && it.tie < tie) {
				start = i
				break
			}
		}
	}
	end := start + limit
	hasMore := end < len(items)
	if !hasMore {
		end = len(items)
	}
	page := items[start:end]
	next := ""
	if hasMore && len(page) > 0 {
		last := page[len(page)-1]
		next = encodeKeysetCursor(last.at, last.tie)
	}
	return page, next, nil
}

// nodeStore is one fake cluster member's item set plus optional failure
// injection (failUntilPage: this node's fetch errors on every call until
// its own call-count reaches this value, simulating a peer that's down for
// its first N requests then heals — T-303 AC2's "peer's return heals
// everything without restarts", exercised at the merge-engine level).
type nodeStore struct {
	items         []fakeItem
	failUntilPage int
	calls         int
}

func newFakeFetch(stores map[string]*nodeStore) clusterFetcher[string] {
	return func(_ context.Context, node, cursor string, limit int) ([]keyed[string], string, error) {
		ns := stores[node]
		ns.calls++
		if ns.calls <= ns.failUntilPage {
			return nil, "", errors.New("fake: node unreachable")
		}
		page, next, err := fakeListPage(ns.items, cursor, limit)
		if err != nil {
			return nil, "", err
		}
		out := make([]keyed[string], len(page))
		for i, it := range page {
			out[i] = keyed[string]{item: it.val, at: it.at, tie: it.tie}
		}
		return out, next, nil
	}
}

// walkAllPages drives mergeClusterPage from the initial cursor until it
// reports no further page, capping at maxPages so a pagination-correctness
// bug (an infinite loop) fails the test instead of hanging it.
func walkAllPages(t *testing.T, nodes []string, fetch clusterFetcher[string], limit, maxPages int) (items []string, sawPartial bool) {
	t.Helper()
	cursor := ""
	for page := 0; page < maxPages; page++ {
		got, next, partial, _, err := mergeClusterPage(context.Background(), nodes, fetch, cursor, limit)
		if err != nil {
			t.Fatalf("mergeClusterPage: %v", err)
		}
		if partial {
			sawPartial = true
		}
		items = append(items, got...)
		if next == "" {
			return items, sawPartial
		}
		cursor = next
	}
	t.Fatalf("pagination did not terminate within %d pages", maxPages)
	return nil, false
}

// TestMergeClusterPage_Property is T-303 AC3: "Merged audit/snapshot
// pagination is stable (no duplicates/gaps across pages with interleaved
// timestamps — property test)." Randomized per-node item sets (with heavy
// timestamp collisions, to actually exercise interleaving and the tie-break
// path) are walked page by page through the shared merge engine every list
// endpoint's fan-out uses (fetchClusterAudit/fetchClusterSnapshots), and the
// full concatenation across every page must equal — item-for-item, in
// order — the single globally-sorted merge of every node's items.
func TestMergeClusterPage_Property(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		numNodes := 1 + rng.Intn(4)
		stores := map[string]*nodeStore{}
		var nodes []string
		var all []fakeItem
		for n := 0; n < numNodes; n++ {
			name := fmt.Sprintf("n%d", n)
			nodes = append(nodes, name)
			count := rng.Intn(20)
			var items []fakeItem
			for i := 0; i < count; i++ {
				it := fakeItem{
					node: name,
					at:   int64(rng.Intn(6)), // small range: forces heavy timestamp collisions
					tie:  fmt.Sprintf("%s-%04d", name, i),
					val:  fmt.Sprintf("%s/%d", name, i),
				}
				items = append(items, it)
				all = append(all, it)
			}
			sortDesc(items)
			stores[name] = &nodeStore{items: items}
		}

		limit := 1 + rng.Intn(7)
		got, sawPartial := walkAllPages(t, nodes, newFakeFetch(stores), limit, 10_000)
		if sawPartial {
			t.Fatalf("trial %d: unexpected partial result with no injected failures", trial)
		}

		sortDesc(all)
		want := make([]string, len(all))
		for i, it := range all {
			want[i] = it.val
		}

		if len(got) != len(want) {
			t.Fatalf("trial %d: got %d items, want %d (nodes=%v limit=%d)", trial, len(got), len(want), nodes, limit)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("trial %d: item %d = %q, want %q (nodes=%v limit=%d)\ngot:  %v\nwant: %v",
					trial, i, got[i], want[i], nodes, limit, got, want)
			}
		}

		// No duplicates, no gaps: the sets must match exactly too (implied by
		// the ordered comparison above, re-checked explicitly for clarity).
		seen := map[string]int{}
		for _, v := range got {
			seen[v]++
		}
		for _, v := range want {
			if seen[v] != 1 {
				t.Fatalf("trial %d: item %q appeared %d times, want exactly 1", trial, v, seen[v])
			}
		}
	}
}

// TestMergeClusterPage_FailedNodeRetriedNeverLost is T-303 AC2's merge-layer
// analogue: a node that fails for its first two fetches (then heals) never
// loses or duplicates an item versus a node that never failed at all, and
// every page it failed on is correctly flagged partial. It does NOT assert
// the exact interleaved global order survives the outage: while "flaky" is
// down, "healthy"'s items necessarily surface first (there is nothing else
// to merge them against yet) — global recency ordering across the outage
// window is an accepted, documented tradeoff (mergeClusterPage's doc
// comment) for "never lose or duplicate an item, and always say when a
// source is missing." What must hold regardless: the full item set is
// exactly covered once (no loss, no duplication), and each node's own
// items still come out in that node's own correct relative order.
func TestMergeClusterPage_FailedNodeRetriedNeverLost(t *testing.T) {
	itemsFor := func(node string, n int) []fakeItem {
		items := make([]fakeItem, n)
		for i := 0; i < n; i++ {
			items[i] = fakeItem{node: node, at: int64(100 - i), tie: fmt.Sprintf("%s-%04d", node, i), val: fmt.Sprintf("%s/%d", node, i)}
		}
		sortDesc(items)
		return items
	}

	stores := map[string]*nodeStore{
		"healthy": {items: itemsFor("healthy", 12)},
		"flaky":   {items: itemsFor("flaky", 12), failUntilPage: 2},
	}
	nodes := []string{"healthy", "flaky"}

	got, sawPartial := walkAllPages(t, nodes, newFakeFetch(stores), 3, 10_000)
	if !sawPartial {
		t.Fatal("expected at least one page to be flagged partial while \"flaky\" was down")
	}

	wantSet := map[string]int{}
	for i := 0; i < 12; i++ {
		wantSet[fmt.Sprintf("healthy/%d", i)] = 0
		wantSet[fmt.Sprintf("flaky/%d", i)] = 0
	}
	var gotHealthy, gotFlaky []string
	for _, v := range got {
		if _, ok := wantSet[v]; !ok {
			t.Fatalf("unexpected item %q", v)
		}
		wantSet[v]++
		if strings.HasPrefix(v, "healthy/") {
			gotHealthy = append(gotHealthy, v)
		} else {
			gotFlaky = append(gotFlaky, v)
		}
	}
	for v, n := range wantSet {
		if n != 1 {
			t.Errorf("item %q appeared %d times, want exactly 1", v, n)
		}
	}
	for i, v := range gotHealthy {
		if want := fmt.Sprintf("healthy/%d", i); v != want {
			t.Errorf("healthy items out of order: position %d = %q, want %q (full sequence %v)", i, v, want, gotHealthy)
		}
	}
	for i, v := range gotFlaky {
		if want := fmt.Sprintf("flaky/%d", i); v != want {
			t.Errorf("flaky items out of order: position %d = %q, want %q (full sequence %v)", i, v, want, gotFlaky)
		}
	}
}

// TestMergeClusterPage_PermanentlyDeadNodeStaysPartialForever documents the
// deliberate design tradeoff noted on mergeClusterPage: a node that never
// recovers is retried on every subsequent page (never marked exhausted),
// so pagination never terminates on its own — but every page still reports
// partial+that node's name, and the other (healthy) node's items are still
// fully, correctly delivered without any node ever being silently dropped.
func TestMergeClusterPage_PermanentlyDeadNodeStaysPartialForever(t *testing.T) {
	stores := map[string]*nodeStore{
		"healthy": {items: []fakeItem{{node: "healthy", at: 1, tie: "healthy-0001", val: "healthy/0"}}},
		"dead":    {items: nil, failUntilPage: 1 << 30},
	}
	nodes := []string{"healthy", "dead"}
	fetch := newFakeFetch(stores)

	items, next, partial, failed, err := mergeClusterPage(context.Background(), nodes, fetch, "", 10)
	if err != nil {
		t.Fatalf("mergeClusterPage: %v", err)
	}
	if !partial || len(failed) != 1 || failed[0] != "dead" {
		t.Fatalf("partial=%v failed=%v, want partial=true failed=[dead]", partial, failed)
	}
	if len(items) != 1 || items[0] != "healthy/0" {
		t.Fatalf("items = %v, want [healthy/0]", items)
	}
	if next == "" {
		t.Fatal("expected a non-empty nextCursor while \"dead\" has never been successfully queried")
	}

	// A second page: healthy is now exhausted (dropped), dead is retried
	// and still fails — partial stays true, no items, cursor still non-empty.
	items, _, partial, failed, err = mergeClusterPage(context.Background(), nodes, fetch, next, 10)
	if err != nil {
		t.Fatalf("mergeClusterPage (page 2): %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("page 2 items = %v, want none", items)
	}
	if !partial || len(failed) != 1 || failed[0] != "dead" {
		t.Fatalf("page 2 partial=%v failed=%v, want partial=true failed=[dead]", partial, failed)
	}
}
