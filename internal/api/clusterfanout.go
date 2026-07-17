package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// --- Flow fan-out (T-1002) ---
//
// Mirrors fetchClusterAudit/fetchClusterSnapshots exactly (mergeClusterPage
// is generic over the item type precisely so a third fan-out consumer is a
// small addition, not a third hand-rolled merge). See flows.go for the
// flowRecordResponse/toFlowRecordResponse/peerFlowRecordToResponse
// conversions this uses.

func toPeerFlowFilter(f store.FlowFilter) peer.FlowFilter {
	return peer.FlowFilter{
		Guest: f.Guest, Subnet: f.Subnet, Source: f.Source,
		VLAN: f.VLAN, Port: f.Port, Proto: f.Proto, FromTs: f.FromTs, ToTs: f.ToTs,
	}
}

// fetchClusterFlows merges the local node's flow_samples ring with every
// reachable peer's (docs/architecture.md §7), for GET /flows.
func fetchClusterFlows(ctx context.Context, local FlowLocalSource, peers PeerFlowSource, filter store.FlowFilter, cursor string, limit int) ([]flowRecordResponse, string, bool, []string, error) {
	nodes, byNode, discoveryFailed := clusterSources(ctx, peers)
	peerFilter := toPeerFlowFilter(filter)

	fetch := func(ctx context.Context, node, cur string, lim int) ([]keyed[flowRecordResponse], string, error) {
		if node == localSourceKey {
			samples, next, err := local.Query(ctx, filter, cur, lim)
			if err != nil {
				return nil, "", err
			}
			out := make([]keyed[flowRecordResponse], len(samples))
			for i, s := range samples {
				out[i] = keyed[flowRecordResponse]{item: toFlowRecordResponse(s), at: s.At, tie: strconv.FormatInt(s.ID, 10)}
			}
			return out, next, nil
		}
		p, ok := byNode[node]
		if !ok {
			return nil, "", fmt.Errorf("api: peer %s: %w", node, peer.ErrPeerUnreachable)
		}
		recs, next, err := peers.Flows(ctx, p, peerFilter, cur, lim)
		if err != nil {
			return nil, "", err
		}
		out := make([]keyed[flowRecordResponse], len(recs))
		for i, rec := range recs {
			out[i] = keyed[flowRecordResponse]{item: peerFlowRecordToResponse(rec), at: rec.At, tie: strconv.FormatInt(rec.ID, 10)}
		}
		return out, next, nil
	}

	items, next, partial, failed, err := mergeClusterPage(ctx, nodes, fetch, cursor, limit)
	if err != nil {
		return nil, "", false, nil, err
	}
	if discoveryFailed {
		partial = true
		failed = append(failed, "<cluster peer discovery>")
	}
	return items, next, partial, failed, nil
}

// This file implements T-303's audit/snapshot list fan-out: docs/
// architecture.md §7's "Audit/snapshot queries in the UI fan out to peers
// and merge" — both tables are node-local app data (SQLite, one DB per
// node; see that section), so a cluster-wide view of either has to
// re-query every peer's own copy through the peer API and merge the pages,
// tolerating individual peer failures (never silently dropping a peer's
// contribution without saying so — the docs/api.md-documented `partial`/
// `failedNodes` fields this file adds to both list responses).
//
// The merge itself (mergeClusterPage) is generic over the item type so
// audit and snapshots share one correctness-critical implementation rather
// than two hand-rolled ones; see its doc comment for the algorithm.

// ClusterPeers discovers the current cluster peer list. *peer.Client
// satisfies this directly (its own Peers method has exactly this
// signature), so no adapter is needed in production wiring.
type ClusterPeers interface {
	Peers(ctx context.Context) ([]peer.Peer, error)
}

// PeerAuditSource is the peer-fan-out dependency GET /audit's cluster merge
// needs: peer discovery plus one page fetch per peer. *peer.Client
// satisfies this directly.
type PeerAuditSource interface {
	ClusterPeers
	Audit(ctx context.Context, p peer.Peer, filter peer.AuditFilter, cursor string, limit int) ([]peer.AuditRecord, string, error)
}

// PeerSnapshotSource is GET /snapshots' peer-fan-out dependency.
// *peer.Client satisfies this directly.
type PeerSnapshotSource interface {
	ClusterPeers
	Snapshots(ctx context.Context, p peer.Peer, cursor string, limit int) ([]peer.SnapshotRecord, string, error)
}

// localSourceKey is the node key mergeClusterPage/clusterCursorMap use for
// "this daemon's own local data" (as opposed to a named peer). Empty rather
// than a real hostname because it is guaranteed distinct from every valid
// PVE node name (which is never empty) without this package needing to
// know the local node's own name at all.
const localSourceKey = ""

// clusterCursorMap is a fan-out page's decoded opaque cursor: cluster-node
// name (localSourceKey for the local source) -> that source's own opaque
// sub-cursor to resume from. A source absent from the map on a non-initial
// page means "exhausted" — see decodeClusterCursor.
type clusterCursorMap map[string]string

// decodeClusterCursor decodes a fan-out page's cursor. cursor == "" is the
// documented "start from the newest item" convention (docs/api.md); it
// decodes to (nil, true, nil) — "initial page, every known source starts
// fresh" — which is the only situation in which a source's absence from
// the (nil) map does NOT mean "exhausted".
func decodeClusterCursor(cursor string) (m clusterCursorMap, initial bool, err error) {
	if cursor == "" {
		return nil, true, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, false, fmt.Errorf("api: malformed cluster cursor: %w", err)
	}
	var out clusterCursorMap
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false, fmt.Errorf("api: malformed cluster cursor: %w", err)
	}
	return out, false, nil
}

// encodeClusterCursor encodes a fan-out page's next cursor, or "" (the
// documented "no further page" sentinel) when m is empty.
func encodeClusterCursor(m clusterCursorMap) string {
	if len(m) == 0 {
		return ""
	}
	raw, err := json.Marshal(m)
	if err != nil {
		// m is map[string]string: json.Marshal cannot fail on it.
		panic(fmt.Sprintf("api: encoding cluster cursor: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// encodeKeysetCursor builds a "<at>:<tie>" resume token — the same keyset
// cursor grammar store.AuditRepo.ListPage/store.SnapshotRepo.ListPage
// themselves use (see those methods' doc comments). This is not a leaky
// coupling: this exact string is only ever fed back into one of those two
// methods (locally, or forwarded verbatim to a peer who feeds it to its own
// identical store method), so producer and consumer agree on the grammar by
// construction.
func encodeKeysetCursor(at int64, tie string) string {
	return strconv.FormatInt(at, 10) + ":" + tie
}

// keyed pairs a fan-out page item with the (at, tie) keyset position its
// origin source's own ListPage-equivalent ordered it by — at descending,
// tie (that source's own id/ULID, formatted) descending on ties. Every
// per-source fetcher mergeClusterPage calls must return items already in
// that order (its own local store / the peer it queried already guarantees
// this).
type keyed[T any] struct {
	item T
	tie  string
	at   int64
}

// clusterFetcher fetches one page from one named source (localSourceKey or
// a peer's node name) for the given sub-cursor/limit.
type clusterFetcher[T any] func(ctx context.Context, node, cursor string, limit int) ([]keyed[T], string, error)

// mergeClusterPage returns one merged, globally-ordered (newest-first) page
// across every source in sources, tolerating individual source failures.
//
// Algorithm: decode cursor into each source's own resume position (or
// "start fresh" on the initial page); fetch up to limit items from every
// non-exhausted source; merge-sort the combined pool by (at desc, tie desc,
// node desc — the node tiebreak only matters for the vanishingly-unlikely
// case of two different sources' items colliding on (at,tie)) and keep the
// top limit as this page's output. Because per-source fetches are
// individually ordered and the merge preserves that order, the items this
// page outputs from any one source are always a *prefix* of that source's
// fetched batch — which is what makes the following cursor-advancement
// rule correct: a source whose entire fetched batch was consumed advances
// to its own reported next-cursor (or is dropped as exhausted, if it
// reported none); a source only partially consumed resumes from the first
// unconsumed item's own keyset position (encodeKeysetCursor) — which is
// also the correct, idempotent "retry the same window" cursor for a source
// that contributed zero items (including one whose fetch errored: its
// original sub-cursor for this page is carried forward unchanged, so it is
// retried in full next time — this is also how a peer that comes back
// after an outage heals a cluster fan-out without ever losing or
// duplicating an item).
//
// The returned partial is true iff any source's fetch failed this page;
// failedNodes names them (empty when partial is false).
func mergeClusterPage[T any](ctx context.Context, sources []string, fetch clusterFetcher[T], cursor string, limit int) (items []T, nextCursor string, partial bool, failedNodes []string, err error) {
	subCursors, initial, err := decodeClusterCursor(cursor)
	if err != nil {
		return nil, "", false, nil, err
	}

	type sourceBatch struct {
		node    string
		nextSub string
		subUsed string
		items   []keyed[T]
		hasMore bool
		errored bool
	}

	batches := make([]sourceBatch, 0, len(sources))
	for _, node := range sources {
		sub, present := "", initial
		if !initial {
			sub, present = subCursors[node]
		}
		if !present {
			continue // exhausted on an earlier page; never revisited
		}
		fetched, next, ferr := fetch(ctx, node, sub, limit)
		if ferr != nil {
			partial = true
			failedNodes = append(failedNodes, node)
			batches = append(batches, sourceBatch{node: node, errored: true, subUsed: sub})
			continue
		}
		batches = append(batches, sourceBatch{node: node, items: fetched, nextSub: next, hasMore: next != "", subUsed: sub})
	}

	type pos struct{ batchIdx, itemIdx int }
	var all []pos
	for bi, b := range batches {
		if b.errored {
			continue
		}
		for ii := range b.items {
			all = append(all, pos{bi, ii})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		a := batches[all[i].batchIdx].items[all[i].itemIdx]
		b := batches[all[j].batchIdx].items[all[j].itemIdx]
		if a.at != b.at {
			return a.at > b.at
		}
		if a.tie != b.tie {
			return a.tie > b.tie
		}
		return batches[all[i].batchIdx].node > batches[all[j].batchIdx].node
	})
	if len(all) > limit {
		all = all[:limit]
	}

	consumed := make(map[int]int, len(batches))
	items = make([]T, 0, len(all))
	for _, p := range all {
		items = append(items, batches[p.batchIdx].items[p.itemIdx].item)
		consumed[p.batchIdx]++
	}

	nextMap := clusterCursorMap{}
	for bi, b := range batches {
		if b.errored {
			nextMap[b.node] = b.subUsed
			continue
		}
		c := consumed[bi]
		switch {
		case c == len(b.items) && !b.hasMore:
			// Fully consumed (including the "fetched zero items, node is
			// genuinely empty/exhausted" case, where c==0==len(items)) and
			// nothing further exists: exhausted, omit — this must be
			// checked before the c==0 case below, or an empty-but-not-
			// exhausted-looking node would never be dropped and
			// pagination would never terminate.
		case c == 0:
			// This source fetched at least one item but none made this
			// page's cut: resume from exactly where this fetch started, so
			// the same (unconsumed) batch is retried in full next page.
			nextMap[b.node] = b.subUsed
		case c < len(b.items):
			// Partially consumed: the underlying store's cursor semantics
			// are exclusive (encodeKeysetCursor(x) resumes strictly AFTER
			// x — see store.AuditRepo.ListPage's "at < ? OR (at=? AND
			// id<?)"), so resuming "at position c" (inclusive) requires
			// encoding position c-1, not c.
			nextMap[b.node] = encodeKeysetCursor(b.items[c-1].at, b.items[c-1].tie)
		case b.hasMore:
			nextMap[b.node] = b.nextSub
		}
	}

	return items, encodeClusterCursor(nextMap), partial, failedNodes, nil
}

// clusterSources returns the full source-node list (local first, then
// every peer sorted by name) mergeClusterPage should query, a node-name ->
// peer.Peer lookup for the peer entries (so per-node fetch closures don't
// each re-run discovery), and whether peer discovery itself failed (in
// which case the caller degrades to local-only for this page and reports
// partial regardless of whether any individual peer fetch would otherwise
// have succeeded — a cluster whose membership can't even be listed cannot
// claim a complete cluster-wide result). Discovery is called exactly once
// per page.
func clusterSources(ctx context.Context, peers ClusterPeers) (nodes []string, byNode map[string]peer.Peer, discoveryFailed bool) {
	nodes = []string{localSourceKey}
	byNode = map[string]peer.Peer{}
	if peers == nil {
		return nodes, byNode, false
	}
	list, err := peers.Peers(ctx)
	if err != nil {
		return nodes, byNode, true
	}
	names := make([]string, 0, len(list))
	for _, p := range list {
		names = append(names, p.Node)
		byNode[p.Node] = p
	}
	sort.Strings(names)
	return append(nodes, names...), byNode, false
}

// --- Audit fan-out ---

func toPeerAuditFilter(f store.AuditFilter) peer.AuditFilter {
	return peer.AuditFilter{
		User: f.User, Action: f.Action, Target: f.Target, Result: f.Result,
		ChangesetID: f.ChangesetID, From: f.From, To: f.To,
	}
}

func auditRecordToResponse(r peer.AuditRecord) auditEntryResponse {
	return auditEntryResponse{
		ID: r.ID, At: r.At, Username: r.Username, Action: r.Action,
		Target: r.Target, ChangesetID: r.ChangesetID, Result: r.Result, Detail: r.Detail,
	}
}

// fetchClusterAudit merges the local audit log with every reachable peer's
// (docs/architecture.md §7), for GET /audit.
func fetchClusterAudit(ctx context.Context, local AuditService, peers PeerAuditSource, filter store.AuditFilter, cursor string, limit int) ([]auditEntryResponse, string, bool, []string, error) {
	nodes, byNode, discoveryFailed := clusterSources(ctx, peers)
	peerFilter := toPeerAuditFilter(filter)

	fetch := func(ctx context.Context, node, cur string, lim int) ([]keyed[auditEntryResponse], string, error) {
		if node == localSourceKey {
			entries, next, err := local.ListPage(ctx, filter, cur, lim)
			if err != nil {
				return nil, "", err
			}
			out := make([]keyed[auditEntryResponse], len(entries))
			for i, e := range entries {
				out[i] = keyed[auditEntryResponse]{item: toAuditEntryResponse(e), at: e.At, tie: strconv.FormatInt(e.ID, 10)}
			}
			return out, next, nil
		}
		p, ok := byNode[node]
		if !ok {
			return nil, "", fmt.Errorf("api: peer %s: %w", node, peer.ErrPeerUnreachable)
		}
		recs, next, err := peers.Audit(ctx, p, peerFilter, cur, lim)
		if err != nil {
			return nil, "", err
		}
		out := make([]keyed[auditEntryResponse], len(recs))
		for i, r := range recs {
			out[i] = keyed[auditEntryResponse]{item: auditRecordToResponse(r), at: r.At, tie: strconv.FormatInt(r.ID, 10)}
		}
		return out, next, nil
	}

	items, next, partial, failed, err := mergeClusterPage(ctx, nodes, fetch, cursor, limit)
	if err != nil {
		return nil, "", false, nil, err
	}
	if discoveryFailed {
		partial = true
		failed = append(failed, "<cluster peer discovery>")
	}
	return items, next, partial, failed, nil
}

// --- Snapshot fan-out ---

func snapshotRecordToSummary(r peer.SnapshotRecord) change.SnapshotSummary {
	return change.SnapshotSummary{ID: r.ID, Kind: r.Kind, ChangesetID: r.ChangesetID, Note: r.Note, Nodes: r.Nodes, TakenAt: r.TakenAt}
}

// fetchClusterSnapshots merges the local snapshot list with every reachable
// peer's, for GET /snapshots.
func fetchClusterSnapshots(ctx context.Context, local SnapshotService, peers PeerSnapshotSource, cursor string, limit int) ([]change.SnapshotSummary, string, bool, []string, error) {
	nodes, byNode, discoveryFailed := clusterSources(ctx, peers)

	fetch := func(ctx context.Context, node, cur string, lim int) ([]keyed[change.SnapshotSummary], string, error) {
		if node == localSourceKey {
			items, next, err := local.ListSnapshots(ctx, cur, lim)
			if err != nil {
				return nil, "", err
			}
			out := make([]keyed[change.SnapshotSummary], len(items))
			for i, it := range items {
				out[i] = keyed[change.SnapshotSummary]{item: it, at: it.TakenAt, tie: it.ID}
			}
			return out, next, nil
		}
		p, ok := byNode[node]
		if !ok {
			return nil, "", fmt.Errorf("api: peer %s: %w", node, peer.ErrPeerUnreachable)
		}
		recs, next, err := peers.Snapshots(ctx, p, cur, lim)
		if err != nil {
			return nil, "", err
		}
		out := make([]keyed[change.SnapshotSummary], len(recs))
		for i, r := range recs {
			out[i] = keyed[change.SnapshotSummary]{item: snapshotRecordToSummary(r), at: r.TakenAt, tie: r.ID}
		}
		return out, next, nil
	}

	items, next, partial, failed, err := mergeClusterPage(ctx, nodes, fetch, cursor, limit)
	if err != nil {
		return nil, "", false, nil, err
	}
	if discoveryFailed {
		partial = true
		failed = append(failed, "<cluster peer discovery>")
	}
	return items, next, partial, failed, nil
}
