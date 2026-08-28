// SPDX-License-Identifier: Apache-2.0

// topodiff.go implements T-2704's point-in-time topology diff — the half that
// needs the store: resolving the two ends of the range, and attributing each
// difference to the changeset that explains it, or honestly marking it as
// explained by nothing at all.
//
// WHY THIS LIVES IN THE CHANGE ENGINE. The pure comparison is in
// internal/topology (pitdiff.go) and knows nothing about changesets. Only this
// package can answer the question that makes the feature worth having — "did
// vnprox do this?" — because only this package holds the snapshot series
// T-2401 records on a schedule and the changeset/audit history T-2403 reads.
//
// THE MARKING IS THE PRODUCT. A difference explained by a changeset names it.
// A difference explained by nothing is marked `attributed: false`, because an
// unattributed change is an out-of-band change — the `ssh node && vi
// /etc/network/interfaces` the drift checker exists to catch. Under-reporting
// attribution costs an operator a click; over-reporting it hides the one row
// they needed to see. Everything below is biased accordingly.
//
// READ-ONLY, STRUCTURALLY. Nothing here stages, applies, writes a store row,
// or takes a snapshot. The only outbound call is NodeAgent.ReadInterfaces,
// which the seam's own contract defines as a read.

package change

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// TopologyDiffNowToken is the documented `to=now` sentinel meaning "the live
// cluster, read right now". `live` is accepted as an alias because
// GET /snapshots/diff already spells the same idea that way and an operator
// who learned one route should not be punished on the other.
const (
	TopologyDiffNowToken  = "now"
	TopologyDiffLiveToken = "live"
)

// DiffPoint identifies one resolved end of a diff range.
//
// Live and SnapshotID are mutually exclusive: a resolved point is either a
// stored snapshot (SnapshotID/Kind/At describe it) or the live cluster read at
// At. There is no third case, and in particular there is no "empty point" —
// failing to resolve returns an error, never a point that quietly compares
// against nothing (see ErrNoSnapshotForPoint).
type DiffPoint struct {
	Requested  string `json:"requested"`
	SnapshotID string `json:"snapshotId,omitempty"`
	Kind       string `json:"kind,omitempty"`
	At         int64  `json:"at"`
	Live       bool   `json:"live,omitempty"`
}

// DiffCoverage states what the diff actually compared.
//
// It is part of the contract rather than diagnostics. A diff over two
// snapshots that captured different node sets can only honestly speak about
// their intersection; saying so is what stops "pve3 was not in the older
// capture" from rendering as "every interface on pve3 was deleted".
type DiffCoverage struct {
	Nodes          []string        `json:"nodes"`
	Paths          []string        `json:"paths"`
	UnmatchedNodes []UnmatchedNode `json:"unmatchedNodes,omitempty"`
	OmittedPaths   []string        `json:"omittedPaths,omitempty"`
}

// UnmatchedNode is one node captured at only one end of the range, named
// rather than silently dropped.
type UnmatchedNode struct {
	Node      string `json:"node"`
	PresentIn string `json:"presentIn"` // "from" | "to"
}

// TopologyDiff is GET /topology/diff's response.
//
// Field ORDER here is dictated by govet's fieldalignment check, not by
// readability; read the JSON tags, not the declaration order.
type TopologyDiff struct {
	Added    []topology.EntityDiff `json:"added"`
	Removed  []topology.EntityDiff `json:"removed"`
	Modified []topology.EntityDiff `json:"modified"`
	Coverage DiffCoverage          `json:"coverage"`
	From     DiffPoint             `json:"from"`
	To       DiffPoint             `json:"to"`
	// Unattributed is how many of the reported differences no changeset
	// explains — the out-of-band change count, and the number an operator
	// actually reacts to. Always serialised, including as 0.
	Unattributed int `json:"unattributedCount"`
}

// SnapshotPoint names one snapshot in an error's "nearest available" list.
type SnapshotPoint struct {
	SnapshotID string `json:"snapshotId"`
	Kind       string `json:"kind"`
	TakenAt    int64  `json:"takenAt"`
}

// ErrNoSnapshotForPoint is returned when one end of the requested range has no
// snapshot behind it.
//
// It exists because the alternative — returning an empty diff — is a lie. An
// empty diff reads as "nothing changed about this cluster", and a caller has
// no way to tell that from "vnprox has no idea what this cluster looked like
// then". So the error NAMES the nearest snapshots that do exist (T-2704 AC4),
// which is also the actionable part: it tells the operator which range they
// could ask for instead.
type ErrNoSnapshotForPoint struct {
	Side      string // "from" | "to"
	Requested string
	Nearest   []SnapshotPoint
	At        int64
}

func (e *ErrNoSnapshotForPoint) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "change: no snapshot covers the %s point %q (%s)", e.Side, e.Requested, formatDiffTime(e.At))
	if len(e.Nearest) == 0 {
		b.WriteString("; there are no snapshots at all — enable scheduled snapshots ([snapshots] interval) or take a manual one")
		return b.String()
	}
	b.WriteString("; nearest available: ")
	parts := make([]string, 0, len(e.Nearest))
	for _, n := range e.Nearest {
		parts = append(parts, fmt.Sprintf("%s (%s, %s)", n.SnapshotID, n.Kind, formatDiffTime(n.TakenAt)))
	}
	b.WriteString(strings.Join(parts, ", "))
	return b.String()
}

// ErrDiffRangeInverted is returned when `from` resolves later than `to`. A
// backwards range is a caller mistake, not a diff with negative content, so it
// is refused rather than silently swapped — silently swapping would render
// additions as removals.
type ErrDiffRangeInverted struct {
	FromAt int64
	ToAt   int64
}

func (e *ErrDiffRangeInverted) Error() string {
	return fmt.Sprintf("change: diff range is inverted: from (%s) is after to (%s)",
		formatDiffTime(e.FromAt), formatDiffTime(e.ToAt))
}

func formatDiffTime(at int64) string {
	return time.Unix(at, 0).UTC().Format(time.RFC3339)
}

// topologyDiffPath is the one captured file the point-in-time diff is computed
// over.
//
// SCOPE, STATED RATHER THAN DISCOVERED. Every snapshot kind captures each
// node's /etc/network/interfaces, and `to=now` can read it live; that file is
// therefore the only source both ends of an arbitrary range are guaranteed to
// have. A `pre`/`post` snapshot ALSO carries synthetic SDN config files
// (apply_snapshot.go's sdn*SnapshotPath), but a `scheduled` one does not — so
// including them would make a scheduled→pre diff report every SDN zone in the
// cluster as newly added, which is exactly the class of false statement this
// card exists to avoid. Those paths are reported in DiffCoverage.OmittedPaths
// instead of being compared. Extending the diff to SDN requires scheduled
// captures to record SDN config too; that is a follow-up, not a silent gap.
const topologyDiffPath = interfacesPath

// TopologyDiff computes docs/api.md's `GET /topology/diff?from=&to=`.
//
// from/to each accept a snapshot id, a unix-seconds timestamp, an RFC3339
// timestamp, or (to only) the `now`/`live` sentinel. A timestamp resolves to
// the newest snapshot taken at or before it — "the cluster as of Tuesday" is
// whatever capture had already happened by Tuesday.
func (s *Service) TopologyDiff(ctx context.Context, from, to string) (*TopologyDiff, error) {
	if s.snapshots == nil || s.blobs == nil {
		return nil, &ErrApplyNotConfigured{}
	}

	fromPoint, fromFiles, err := s.resolveDiffPoint(ctx, "from", from, nil)
	if err != nil {
		return nil, err
	}
	toPoint, toFiles, err := s.resolveDiffPoint(ctx, "to", to, fromFiles)
	if err != nil {
		return nil, err
	}
	if fromPoint.At > toPoint.At {
		return nil, &ErrDiffRangeInverted{FromAt: fromPoint.At, ToAt: toPoint.At}
	}

	fromByNode, fromOmitted := interfacesByNode(fromFiles)
	toByNode, toOmitted := interfacesByNode(toFiles)

	nodes, unmatched := reconcileDiffNodes(fromByNode, toByNode)

	fromEnts, err := entitiesForNodes(fromByNode, nodes)
	if err != nil {
		return nil, fmt.Errorf("change: reading topology at %s: %w", fromPoint.Requested, err)
	}
	toEnts, err := entitiesForNodes(toByNode, nodes)
	if err != nil {
		return nil, fmt.Errorf("change: reading topology at %s: %w", toPoint.Requested, err)
	}

	diffs := topology.DiffPoints(fromEnts, toEnts)

	attributions, err := s.attributionsInRange(ctx, fromPoint, toPoint)
	if err != nil {
		return nil, err
	}

	out := &TopologyDiff{
		From:     fromPoint,
		To:       toPoint,
		Added:    []topology.EntityDiff{},
		Removed:  []topology.EntityDiff{},
		Modified: []topology.EntityDiff{},
		Coverage: DiffCoverage{
			Nodes:          nodes,
			Paths:          []string{topologyDiffPath},
			UnmatchedNodes: unmatched,
			OmittedPaths:   mergeOmittedPaths(fromOmitted, toOmitted),
		},
	}
	for _, d := range diffs {
		if a, ok := attributions[d.Ref]; ok {
			d.Attribution = a
		} else {
			out.Unattributed++
		}
		switch d.Change {
		case topology.DiffAdded:
			out.Added = append(out.Added, d)
		case topology.DiffRemoved:
			out.Removed = append(out.Removed, d)
		case topology.DiffModified:
			out.Modified = append(out.Modified, d)
		}
	}

	s.log.Info("change: computed point-in-time topology diff",
		"from", fromPoint.Requested, "to", toPoint.Requested,
		"nodes", len(nodes), "added", len(out.Added), "removed", len(out.Removed),
		"modified", len(out.Modified), "unattributed", out.Unattributed)
	return out, nil
}

// resolveDiffPoint turns one caller-supplied endpoint into a resolved point
// plus that point's captured files.
//
// liveFrom is the other end's file list, used only when this point is `now`:
// the live read has to know WHICH nodes to read, and the honest answer is "the
// ones the other end captured" — reading a node the other end never captured
// would produce a one-sided entity set that DiffPoints would have to call
// added, when in truth it was simply never observed before.
func (s *Service) resolveDiffPoint(ctx context.Context, side, requested string, liveFrom []snapshotFile) (DiffPoint, []snapshotFile, error) {
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" {
		return DiffPoint{}, nil, fmt.Errorf("change: diff %s point is required (a snapshot id, a timestamp, or %q)", side, TopologyDiffNowToken)
	}

	if isNowToken(trimmed) {
		if side != "to" {
			return DiffPoint{}, nil, fmt.Errorf("change: %q is only valid as the `to` point of a diff", trimmed)
		}
		files, err := s.readLiveInterfaces(ctx, liveFrom)
		if err != nil {
			return DiffPoint{}, nil, err
		}
		return DiffPoint{Requested: trimmed, Live: true, At: s.now().Unix()}, files, nil
	}

	if at, ok := parseDiffTimestamp(trimmed); ok {
		row, err := s.snapshots.LatestAtOrBefore(ctx, at)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return DiffPoint{}, nil, s.noSnapshotError(ctx, side, trimmed, at)
			}
			return DiffPoint{}, nil, fmt.Errorf("change: resolving diff %s point %q: %w", side, trimmed, err)
		}
		files, err := s.loadSnapshotFiles(ctx, row.ID)
		if err != nil {
			return DiffPoint{}, nil, err
		}
		return DiffPoint{Requested: trimmed, SnapshotID: row.ID, Kind: row.Kind, At: row.TakenAt}, files, nil
	}

	row, err := s.snapshots.Get(ctx, trimmed)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return DiffPoint{}, nil, fmt.Errorf("change: diff %s point %q names no snapshot: %w", side, trimmed, store.ErrNotFound)
		}
		return DiffPoint{}, nil, fmt.Errorf("change: resolving diff %s point %q: %w", side, trimmed, err)
	}
	files, err := s.loadSnapshotFiles(ctx, row.ID)
	if err != nil {
		return DiffPoint{}, nil, err
	}
	return DiffPoint{Requested: trimmed, SnapshotID: row.ID, Kind: row.Kind, At: row.TakenAt}, files, nil
}

func isNowToken(v string) bool {
	return strings.EqualFold(v, TopologyDiffNowToken) || strings.EqualFold(v, TopologyDiffLiveToken)
}

// parseDiffTimestamp recognises the two timestamp spellings a caller might
// send: unix seconds, and RFC3339. Anything else is treated as a snapshot id,
// which is why this returns ok rather than an error — a ULID is not a
// malformed timestamp, it is a different kind of thing.
func parseDiffTimestamp(v string) (int64, bool) {
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n, true
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.Unix(), true
	}
	return 0, false
}

// noSnapshotError builds AC4's stated error: which point could not be
// resolved, and which snapshots DO exist nearest to it on either side.
func (s *Service) noSnapshotError(ctx context.Context, side, requested string, at int64) error {
	e := &ErrNoSnapshotForPoint{Side: side, Requested: requested, At: at}
	if row, err := s.snapshots.LatestAtOrBefore(ctx, at); err == nil {
		e.Nearest = append(e.Nearest, SnapshotPoint{SnapshotID: row.ID, Kind: row.Kind, TakenAt: row.TakenAt})
	}
	if row, err := s.snapshots.EarliestAtOrAfter(ctx, at); err == nil {
		e.Nearest = append(e.Nearest, SnapshotPoint{SnapshotID: row.ID, Kind: row.Kind, TakenAt: row.TakenAt})
	}
	return e
}

// readLiveInterfaces reads each node captured at the other end of the range,
// right now. Read-only: ReadInterfaces is the seam's documented read.
func (s *Service) readLiveInterfaces(ctx context.Context, other []snapshotFile) ([]snapshotFile, error) {
	if s.nodes == nil {
		return nil, &ErrApplyNotConfigured{}
	}
	seen := map[string]bool{}
	nodes := make([]string, 0, len(other))
	for _, f := range other {
		if f.Path != topologyDiffPath || f.Node == "" || seen[f.Node] {
			continue
		}
		seen[f.Node] = true
		nodes = append(nodes, f.Node)
	}
	sort.Strings(nodes)

	out := make([]snapshotFile, 0, len(nodes))
	for _, node := range nodes {
		content, err := s.nodes.ReadInterfaces(ctx, node)
		if err != nil {
			return nil, fmt.Errorf("change: reading live %s on node %s for topology diff: %w", topologyDiffPath, node, err)
		}
		out = append(out, snapshotFile{Node: node, Path: topologyDiffPath, Content: content})
	}
	return out, nil
}

// interfacesByNode splits a captured file list into the per-node interfaces
// files the diff compares and the set of other paths it deliberately does not
// (see topologyDiffPath's doc comment).
func interfacesByNode(files []snapshotFile) (byNode map[string]string, omitted []string) {
	byNode = make(map[string]string, len(files))
	omittedSet := map[string]bool{}
	for _, f := range files {
		if f.Path == topologyDiffPath && f.Node != "" {
			byNode[f.Node] = f.Content
			continue
		}
		omittedSet[f.Path] = true
	}
	for p := range omittedSet {
		omitted = append(omitted, p)
	}
	sort.Strings(omitted)
	return byNode, omitted
}

func mergeOmittedPaths(a, b []string) []string {
	set := map[string]bool{}
	for _, p := range append(append([]string{}, a...), b...) {
		set[p] = true
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

// reconcileDiffNodes returns the nodes captured at BOTH ends (sorted, the
// stable comparison key this whole feature is ordered by) plus the ones
// captured at only one end.
//
// The one-sided nodes are named, not dropped silently: "pve3 was only in the
// newer capture" is a real thing an operator needs to know, and it is NOT the
// same statement as "pve3's bridges were all created during this range".
func reconcileDiffNodes(from, to map[string]string) (nodes []string, unmatched []UnmatchedNode) {
	for node := range from {
		if _, ok := to[node]; ok {
			nodes = append(nodes, node)
		} else {
			unmatched = append(unmatched, UnmatchedNode{Node: node, PresentIn: "from"})
		}
	}
	for node := range to {
		if _, ok := from[node]; !ok {
			unmatched = append(unmatched, UnmatchedNode{Node: node, PresentIn: "to"})
		}
	}
	sort.Strings(nodes)
	sort.Slice(unmatched, func(i, j int) bool {
		if unmatched[i].Node != unmatched[j].Node {
			return unmatched[i].Node < unmatched[j].Node
		}
		return unmatched[i].PresentIn < unmatched[j].PresentIn
	})
	return nodes, unmatched
}

// entitiesForNodes parses each in-scope node's captured file into comparable
// entities, in a stable node order.
func entitiesForNodes(byNode map[string]string, nodes []string) ([]topology.PointEntity, error) {
	out := []topology.PointEntity{}
	for _, node := range nodes {
		ents, err := topology.EntitiesFromInterfaces(node, byNode[node])
		if err != nil {
			return nil, err
		}
		out = append(out, ents...)
	}
	return out, nil
}

// attributionsInRange indexes, by entity ref string, the changeset that
// explains a change to that entity within the range.
//
// HOW A CHANGESET IS CONSIDERED TO HAVE HAPPENED IN THE RANGE. Not by its
// UpdatedAt (which moves on confirm, on rollback, on a comment) but by its
// own apply-lifecycle audit rows — the record of when something actually
// happened, which is the same source T-2403's blame feed merges for the same
// reason. A changeset with no such row in the range attributes nothing.
//
// ONE DELIBERATE EXCLUSION. If the `from` point is a changeset's own `post`
// snapshot, that changeset's effect is already baked into `from`; any further
// difference on the entities it touched happened AFTER it, so attributing them
// to it would be exactly the mis-attribution this card warns against. It is
// dropped.
//
// Matching is by exact op target ref. A changeset that touched vmbr0 explains
// a difference in vmbr0 and nothing else — not its ports, not its node's other
// bridges. Anything broader would let one legitimate changeset launder every
// out-of-band edit made in the same window.
func (s *Service) attributionsInRange(ctx context.Context, from, to DiffPoint) (map[string]topology.DiffAttribution, error) {
	out := map[string]topology.DiffAttribution{}
	if s.audit == nil || s.repo == nil {
		return out, nil
	}

	rows, err := s.audit.ListActionsInRange(ctx, store.ChangesetLifecycleActions, from.At, to.At)
	if err != nil {
		return nil, fmt.Errorf("change: listing changeset activity for topology diff: %w", err)
	}

	excluded, err := s.changesetOwningPostSnapshot(ctx, from)
	if err != nil {
		return nil, err
	}

	type activity struct {
		actor string
		at    int64
	}
	acts := map[string]activity{}
	for _, r := range rows {
		id := r.ChangesetID.String
		if id == "" || id == excluded {
			continue
		}
		// Keep the LAST lifecycle event in the range: it is the one closest
		// to the state `to` observes.
		if prev, ok := acts[id]; !ok || r.At >= prev.at {
			acts[id] = activity{at: r.At, actor: r.Username}
		}
	}
	if len(acts) == 0 {
		return out, nil
	}

	ids := make([]string, 0, len(acts))
	for id := range acts {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		row, err := s.repo.Get(ctx, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("change: loading changeset %s for topology diff attribution: %w", id, err)
		}
		cs, convErr := fromStoreRow(row)
		if convErr != nil {
			// One undecodable changeset must not blank every attribution in
			// the range; the entities it touched simply fall back to
			// unattributed, which is the honest default.
			s.log.Warn("change: skipping undecodable changeset in topology diff attribution", "changeset_id", id, "error", convErr)
			continue
		}
		act := acts[id]
		actor := act.actor
		if actor == "" {
			actor = cs.Author
		}
		attribution := topology.DiffAttribution{
			Attributed:     true,
			ChangesetID:    cs.ID,
			ChangesetTitle: cs.Title,
			Actor:          actor,
			At:             act.at,
		}
		for _, op := range cs.Ops {
			if op.Target.IsZero() {
				continue
			}
			// Later changesets win: `acts` is walked in id order, and a ULID
			// id is time-ordered, so the last writer of a ref is the most
			// recent one to have touched it.
			out[op.Target.String()] = attribution
		}
	}
	return out, nil
}

// changesetOwningPostSnapshot returns the changeset id whose `post` snapshot
// IS the `from` point, or "" when the point is not one. See
// attributionsInRange's "one deliberate exclusion".
func (s *Service) changesetOwningPostSnapshot(ctx context.Context, from DiffPoint) (string, error) {
	if from.SnapshotID == "" || from.Kind != snapshotKindPost {
		return "", nil
	}
	row, err := s.snapshots.Get(ctx, from.SnapshotID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("change: reading diff from-point snapshot %s: %w", from.SnapshotID, err)
	}
	return row.ChangesetID.String, nil
}
