// SPDX-License-Identifier: Apache-2.0

// timeline.go assembles the view. It writes nothing, anywhere: every function
// in this file takes a window and returns events.
//
// The assembly is deliberately boring — one query per source, one merge, one
// sort — because the interesting property is what it does NOT do. Nothing
// here subscribes, polls, caches or copies, so an incident opened over last
// Tuesday and an incident opened live last Tuesday run the identical queries
// with the identical arguments and therefore produce the identical events.

package incident

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

// changesetTimelineActions is the audit action set the `changeset` source
// reads: the T-205 apply lifecycle GET /history/events already uses, plus
// `changeset.create` — the card asks for "changesets staged or applied", and
// staging is exactly that row (internal/change.Service.Create writes it).
func changesetTimelineActions() []string {
	return append([]string{"changeset.create"}, store.ChangesetLifecycleActions...)
}

// diagnoseTimelineActions is the `diagnosis` source: the one audit row
// internal/api/diagnose.go writes per POST /diagnose. The per-step
// `diagnose.step` rows are deliberately NOT included — five rows per run would
// bury the other four sources in a ladder's own detail, and the run row names
// the target and the outcome, which is what a timeline is for.
func diagnoseTimelineActions() []string { return []string{"diagnose.run"} }

// Timeline assembles one incident's view.
//
// A source that fails does not fail the timeline: its status says so and every
// other source still contributes, the same degrade-per-source contract GET
// /flows' partial/failedNodes established. A source that is not wired on this
// node reports `unavailable`, which is a different statement from "returned
// nothing".
func (s *Service) Timeline(ctx context.Context, id string) (*Timeline, error) {
	row, err := s.cfg.Store.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("incident: reading %s: %w", id, err)
	}
	inc, err := s.withAnnotations(ctx, row)
	if err != nil {
		return nil, err
	}

	win := Window{From: row.StartedAt, To: row.EndedAt}
	if win.To <= 0 {
		win.To, win.Live = s.now(), true
	}

	tl := &Timeline{
		Incident: inc,
		Window:   win,
		Events:   []Event{},
		Sources:  []SourceStatus{},
		Caveats:  []string{},
	}

	statuses := map[Source]SourceStatus{}
	add := func(st SourceStatus, events []Event) {
		st.Count = len(events)
		statuses[st.Source] = st
		tl.Events = append(tl.Events, events...)
	}

	add(s.findingEvents(ctx, win))
	add(s.changesetEvents(ctx, win))
	add(s.diagnosisEvents(ctx, win))
	add(s.captureEvents(ctx, win))
	add(s.flowEvents(ctx, win))
	add(annotationEvents(inc))

	// One timeline, strictly chronological. The id tie-break makes the order
	// of two events sharing a second deterministic rather than dependent on
	// which source happened to be queried first.
	sort.SliceStable(tl.Events, func(i, j int) bool {
		if tl.Events[i].At != tl.Events[j].At {
			return tl.Events[i].At < tl.Events[j].At
		}
		return tl.Events[i].ID < tl.Events[j].ID
	})

	for _, src := range Sources() {
		if st, ok := statuses[src]; ok {
			tl.Sources = append(tl.Sources, st)
		}
	}

	s.attachDiff(ctx, tl, win)
	tl.Caveats = caveatsFor(tl)
	return tl, nil
}

// ---------------------------------------------------------------- sources

func (s *Service) findingEvents(ctx context.Context, win Window) (SourceStatus, []Event) {
	st := SourceStatus{Source: SourceFinding, Status: StatusOK}
	if s.cfg.FindingEvents == nil {
		st.Status, st.Detail = StatusUnavailable, "no finding-event history is recorded on this node"
		return st, nil
	}
	rows, err := s.cfg.FindingEvents.ListByTimeRange(ctx, win.From, win.To)
	if err != nil {
		return failed(st, "listing finding transitions", err), nil
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, Event{
			ID:         fmt.Sprintf("finding:%d", r.ID),
			At:         r.At,
			Source:     SourceFinding,
			Kind:       r.Transition,
			Summary:    fmt.Sprintf("finding %s %s", r.FindingID, r.Transition),
			FindingID:  r.FindingID,
			Transition: r.Transition,
			Ref:        r.FindingID,
		})
	}
	return st, out
}

func (s *Service) changesetEvents(ctx context.Context, win Window) (SourceStatus, []Event) {
	st := SourceStatus{Source: SourceChangeset, Status: StatusOK}
	if s.cfg.Audit == nil {
		st.Status, st.Detail = StatusUnavailable, "no audit log is available on this node"
		return st, nil
	}
	rows, err := s.cfg.Audit.ListActionsInRange(ctx, changesetTimelineActions(), win.From, win.To)
	if err != nil {
		return failed(st, "listing changeset activity", err), nil
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, Event{
			ID:          fmt.Sprintf("changeset:%d", r.ID),
			At:          r.At,
			Source:      SourceChangeset,
			Kind:        r.Action,
			Summary:     changesetSummary(r),
			Actor:       r.Username,
			Result:      r.Result,
			Action:      r.Action,
			ChangesetID: r.ChangesetID.String,
			Ref:         r.Target.String,
		})
	}
	return st, out
}

func changesetSummary(r store.AuditEntry) string {
	var b strings.Builder
	b.WriteString(r.Action)
	if r.ChangesetID.Valid && r.ChangesetID.String != "" {
		b.WriteString(" ")
		b.WriteString(r.ChangesetID.String)
	}
	if r.Result != "" {
		fmt.Fprintf(&b, " (%s)", r.Result)
	}
	return b.String()
}

func (s *Service) diagnosisEvents(ctx context.Context, win Window) (SourceStatus, []Event) {
	st := SourceStatus{Source: SourceDiagnosis, Status: StatusOK}
	if s.cfg.Audit == nil {
		st.Status, st.Detail = StatusUnavailable, "no audit log is available on this node"
		return st, nil
	}
	rows, err := s.cfg.Audit.ListActionsInRange(ctx, diagnoseTimelineActions(), win.From, win.To)
	if err != nil {
		return failed(st, "listing diagnosis runs", err), nil
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		target := r.Target.String
		summary := "diagnosis ladder run"
		if target != "" {
			summary += " on " + target
		}
		if r.Result != "" {
			summary += fmt.Sprintf(" (%s)", r.Result)
		}
		out = append(out, Event{
			ID:      fmt.Sprintf("diagnosis:%d", r.ID),
			At:      r.At,
			Source:  SourceDiagnosis,
			Kind:    r.Action,
			Summary: summary,
			Actor:   r.Username,
			Result:  r.Result,
			Action:  r.Action,
			Ref:     target,
		})
	}
	return st, out
}

// captureEvents emits up to two events per capture session — the start and,
// when it is in the window and has happened, the stop. A capture that started
// before the incident and was still running through it therefore contributes
// nothing, which is correct: the timeline records what HAPPENED in the window.
func (s *Service) captureEvents(ctx context.Context, win Window) (SourceStatus, []Event) {
	st := SourceStatus{Source: SourceCapture, Status: StatusOK}
	if s.cfg.Captures == nil {
		st.Status, st.Detail = StatusUnavailable, "packet capture is not configured on this node"
		return st, nil
	}
	rows, err := s.cfg.Captures.List(ctx)
	if err != nil {
		return failed(st, "listing capture sessions", err), nil
	}
	var out []Event
	for _, c := range rows {
		if inWindow(c.StartedAt, win) {
			out = append(out, Event{
				ID:        "capture:start:" + c.ID,
				At:        c.StartedAt,
				Source:    SourceCapture,
				Kind:      "started",
				Summary:   fmt.Sprintf("capture started on %s", captureWhere(c)),
				Actor:     c.StartedBy,
				Node:      c.Node,
				Ref:       c.TargetRef,
				CaptureID: c.ID,
			})
		}
		if c.StoppedAt > 0 && inWindow(c.StoppedAt, win) {
			out = append(out, Event{
				ID:        "capture:stop:" + c.ID,
				At:        c.StoppedAt,
				Source:    SourceCapture,
				Kind:      "stopped",
				Summary:   fmt.Sprintf("capture %s on %s (%d packets)", c.Status, captureWhere(c), c.Packets),
				Node:      c.Node,
				Ref:       c.TargetRef,
				Result:    c.Status,
				CaptureID: c.ID,
			})
		}
	}
	return st, out
}

func captureWhere(c store.CaptureSession) string {
	switch {
	case c.TargetRef != "" && c.Node != "":
		return c.TargetRef + " (" + c.Node + ")"
	case c.TargetRef != "":
		return c.TargetRef
	case c.Node != "":
		return c.Node
	default:
		return "an unnamed target"
	}
}

// flowEvents contributes the sampled flow records in the window, newest-first
// from the store and capped at Config.FlowLimit.
//
// The cap is reported rather than hidden: a window with more flows than the
// cap says `truncated` and names the number it took. A timeline that silently
// dropped half a window's traffic would be worse than one that carried none,
// because a reader would draw conclusions from it.
func (s *Service) flowEvents(ctx context.Context, win Window) (SourceStatus, []Event) {
	st := SourceStatus{Source: SourceFlow, Status: StatusOK}
	if s.cfg.Flows == nil {
		st.Status, st.Detail = StatusUnavailable, "no flow samples are collected on this node"
		return st, nil
	}
	rows, _, err := s.cfg.Flows.Query(ctx,
		store.FlowFilter{FromTs: win.From, ToTs: win.To}, "", s.cfg.FlowLimit)
	if err != nil {
		return failed(st, "querying flow samples", err), nil
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, Event{
			ID:      fmt.Sprintf("flow:%d", r.ID),
			At:      r.At,
			Source:  SourceFlow,
			Kind:    r.Source,
			Summary: flowSummary(r),
			Node:    r.Node,
			Ref:     r.SrcRef,
		})
	}
	if len(rows) >= s.cfg.FlowLimit {
		st.Status = StatusTruncated
		st.Detail = fmt.Sprintf("the newest %d flow samples in the window are shown; there may be more", s.cfg.FlowLimit)
	}
	return st, out
}

func flowSummary(r store.FlowSample) string {
	src, dst := r.SrcIP, r.DstIP
	if r.SrcPort > 0 {
		src += ":" + strconv.Itoa(r.SrcPort)
	}
	if r.DstPort > 0 {
		dst += ":" + strconv.Itoa(r.DstPort)
	}
	return fmt.Sprintf("flow %s -> %s (proto %d, %d bytes)", src, dst, r.Proto, r.Bytes)
}

// annotationEvents places the operator's own observations on the same
// timeline as everything else. They come from the incident record, which is
// why this one takes no context: it is already loaded.
func annotationEvents(inc Incident) (SourceStatus, []Event) {
	st := SourceStatus{Source: SourceAnnotation, Status: StatusOK}
	out := make([]Event, 0, len(inc.Annotations))
	for _, a := range inc.Annotations {
		out = append(out, Event{
			ID:           "annotation:" + a.ID,
			At:           a.At,
			Source:       SourceAnnotation,
			Kind:         "note",
			Summary:      a.Body,
			Actor:        a.Author,
			AnnotationID: a.ID,
		})
	}
	return st, out
}

func inWindow(at int64, win Window) bool { return at >= win.From && at <= win.To }

func failed(st SourceStatus, what string, err error) SourceStatus {
	st.Status = StatusError
	st.Detail = fmt.Sprintf("%s failed: %v", what, err)
	return st
}

// ------------------------------------------------------------------- diff

// attachDiff runs T-2704's point-in-time diff across the incident's window.
//
// The refusal path is the interesting one. change.TopologyDiff returns a TYPED
// error for a range it cannot cover, never an empty diff, and that error names
// the snapshots that do exist. Surfacing it verbatim is the difference between
// "nothing changed while your network was down" (false, and an operator would
// act on it) and "vnprox has no capture from before 09:00; the nearest are
// ...".
func (s *Service) attachDiff(ctx context.Context, tl *Timeline, win Window) {
	if s.cfg.Diff == nil {
		tl.DiffErrorCode = "apply_unavailable"
		tl.DiffError = "the snapshot store is not available on this node, so no point-in-time diff could be computed"
		return
	}
	to := strconv.FormatInt(win.To, 10)
	if win.Live {
		to = change.TopologyDiffNowToken
	}
	diff, err := s.cfg.Diff.TopologyDiff(ctx, strconv.FormatInt(win.From, 10), to)
	if err != nil {
		tl.DiffErrorCode, tl.DiffError = diffErrorCode(err), err.Error()
		s.cfg.Logger.Info("incident: no point-in-time diff for this window",
			"incidentId", tl.Incident.ID, "code", tl.DiffErrorCode, "error", err)
		return
	}
	tl.Diff = diff
}

// diffErrorCode maps the change engine's typed diff refusals onto the same
// stable docs/api.md codes GET /topology/diff already returns, so a client
// handles one vocabulary rather than two.
func diffErrorCode(err error) string {
	var missing *change.ErrNoSnapshotForPoint
	if errors.As(err, &missing) {
		return "no_snapshot_in_range"
	}
	var inverted *change.ErrDiffRangeInverted
	if errors.As(err, &inverted) {
		return "validation_failed"
	}
	var notConfigured *change.ErrApplyNotConfigured
	if errors.As(err, &notConfigured) {
		return "apply_unavailable"
	}
	if errors.Is(err, store.ErrNotFound) {
		return "not_found"
	}
	return "internal_error"
}

// ---------------------------------------------------------------- caveats

// caveatsFor derives the timeline's own disclosures.
//
// Every one is computed from what the assembly actually observed — a source
// status, the diff's own Coverage — rather than written down as a constant.
// A caveat that is a constant outlives the limit it describes; this one
// cannot, because there is nowhere to state a scope other than the scope the
// diff reported.
func caveatsFor(tl *Timeline) []string {
	out := []string{}

	for _, st := range tl.Sources {
		switch st.Status {
		case StatusUnavailable:
			out = append(out, fmt.Sprintf("%s events are missing from this timeline: %s", st.Source, st.Detail))
		case StatusError:
			out = append(out, fmt.Sprintf("%s events may be incomplete: %s", st.Source, st.Detail))
		case StatusTruncated:
			out = append(out, fmt.Sprintf("%s events are truncated: %s", st.Source, st.Detail))
		}
	}

	if tl.Diff == nil {
		out = append(out, "no point-in-time diff covers this window: "+tl.DiffError)
		return out
	}

	cov := tl.Diff.Coverage
	if len(cov.Paths) > 0 {
		out = append(out, "the point-in-time diff compared "+strings.Join(cov.Paths, ", ")+" only")
	}
	if len(cov.OmittedPaths) > 0 {
		out = append(out, "the point-in-time diff did not compare "+strings.Join(cov.OmittedPaths, ", ")+
			"; changes there are neither reported nor ruled out")
	}
	if len(cov.UnmatchedNodes) > 0 {
		names := make([]string, 0, len(cov.UnmatchedNodes))
		for _, n := range cov.UnmatchedNodes {
			names = append(names, fmt.Sprintf("%s (captured only in %s)", n.Node, n.PresentIn))
		}
		out = append(out, "these nodes were captured at only one end of the window: "+strings.Join(names, ", ")+
			" — an entity absent on them is not evidence of a deletion")
	}
	if tl.Diff.Unattributed > 0 {
		out = append(out, fmt.Sprintf("%d of the reported differences are explained by no changeset — "+
			"they were made out of band", tl.Diff.Unattributed))
	}
	return out
}
