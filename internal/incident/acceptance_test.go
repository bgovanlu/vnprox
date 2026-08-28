// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"reflect"
	"testing"
)

// acceptance_test.go covers T-2804's acceptance criteria 1, 4 and 5 against
// the real repositories. Criterion 2 (collection is unchanged) needs counting
// fakes and lives in ac2_test.go; criterion 3 (redaction) is asserted by
// internal/backup's own AC1 scan, which now runs over the incident export as
// a second producer rather than getting a parallel copy.

// TestAC1_RetroactiveIncidentMatchesALiveOne is acceptance criterion 1: "an
// incident opened retroactively over a past window contains the same events
// as one opened live at that time, asserted against a seeded event history.
// This proves it is a view."
//
// The two incidents differ in every way an incident CAN differ except the
// window: the live one was opened before anything happened and closed after
// everything had; the retroactive one was opened two hours later, naming the
// same window explicitly. If any part of this feature recorded rather than
// queried, the retroactive timeline would be empty and this test would say so.
func TestAC1_RetroactiveIncidentMatchesALiveOne(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	// The live incident is opened BEFORE the history exists.
	h.setNow(990)
	live, err := h.svc.Open(ctx, OpenRequest{Title: "vmbr0 down", Actor: "brian@pam"})
	if err != nil {
		t.Fatalf("Open(live): %v", err)
	}
	if live.Retroactive {
		t.Error("an incident opened at the start of its own window is marked retroactive")
	}

	h.seedInterleavedHistory()

	// ...and closed after it.
	h.setNow(1100)
	if _, closeErr := h.svc.Close(ctx, live.ID); closeErr != nil {
		t.Fatalf("Close(live): %v", closeErr)
	}

	// Two hours later, somebody opens the same window from memory.
	h.setNow(1100 + 7200)
	retro, err := h.svc.Open(ctx, OpenRequest{
		Title: "vmbr0 down (written up later)", Actor: "someone-else@pam",
		StartedAt: 990, EndedAt: 1100,
	})
	if err != nil {
		t.Fatalf("Open(retroactive): %v", err)
	}
	if !retro.Retroactive {
		t.Error("an incident opened after its window began is not marked retroactive")
	}

	liveTL, err := h.svc.Timeline(ctx, live.ID)
	if err != nil {
		t.Fatalf("Timeline(live): %v", err)
	}
	retroTL, err := h.svc.Timeline(ctx, retro.ID)
	if err != nil {
		t.Fatalf("Timeline(retroactive): %v", err)
	}

	// Control first: the live timeline is not empty. "The two are equal"
	// would otherwise be satisfied by two empty lists.
	if len(machineEvents(liveTL.Events)) != 10 {
		t.Fatalf("CONTROL FAILED: the live incident has %d machine events, want the 10 seeded — "+
			"an equality assertion between two empty timelines proves nothing", len(machineEvents(liveTL.Events)))
	}

	if !reflect.DeepEqual(machineEvents(liveTL.Events), machineEvents(retroTL.Events)) {
		t.Errorf("a retroactively-opened incident does not contain the same events as the live one.\nlive:  %+v\nretro: %+v",
			machineEvents(liveTL.Events), machineEvents(retroTL.Events))
	}
	if liveTL.Window != retroTL.Window {
		t.Errorf("windows differ: live %+v, retro %+v", liveTL.Window, retroTL.Window)
	}
	if !reflect.DeepEqual(liveTL.Sources, retroTL.Sources) {
		t.Errorf("per-source status differs: live %+v, retro %+v", liveTL.Sources, retroTL.Sources)
	}
}

// TestAC1_ANarrowerWindowSeesFewerEvents is the control for the criterion
// above: the equality there must come from the window being the same, not
// from the timeline ignoring the window altogether.
func TestAC1_ANarrowerWindowSeesFewerEvents(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.seedInterleavedHistory()
	h.setNow(9000)

	wide, err := h.svc.Open(ctx, OpenRequest{Title: "wide", StartedAt: 990, EndedAt: 1100})
	if err != nil {
		t.Fatalf("Open(wide): %v", err)
	}
	narrow, err := h.svc.Open(ctx, OpenRequest{Title: "narrow", StartedAt: 1035, EndedAt: 1055})
	if err != nil {
		t.Fatalf("Open(narrow): %v", err)
	}

	wideTL, err := h.svc.Timeline(ctx, wide.ID)
	if err != nil {
		t.Fatalf("Timeline(wide): %v", err)
	}
	narrowTL, err := h.svc.Timeline(ctx, narrow.ID)
	if err != nil {
		t.Fatalf("Timeline(narrow): %v", err)
	}
	if len(narrowTL.Events) >= len(wideTL.Events) {
		t.Fatalf("a 20-second window returned %d events and a 110-second window %d — the window is not being applied",
			len(narrowTL.Events), len(wideTL.Events))
	}
	// Exactly the flow at 1040 and the finding at 1050.
	if got := sourcesOf(narrowTL.Events); !reflect.DeepEqual(got, []Source{SourceFlow, SourceFinding}) {
		t.Errorf("narrow window events = %v, want [flow finding]", got)
	}
}

// TestAC4_AllFiveSourcesOnOneTimelineInOrder is acceptance criterion 4:
// "events from all five sources appear on one timeline in strict
// chronological order, asserted with interleaved timestamps across sources
// rather than same-source runs."
//
// The fixture interleaves deliberately (see seedInterleavedHistory): a
// timeline that sorted within each source and then concatenated the blocks
// would pass a same-source-run fixture and fail this one.
func TestAC4_AllFiveSourcesOnOneTimelineInOrder(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	wantOrder := h.seedInterleavedHistory()

	h.setNow(2000)
	inc, err := h.svc.Open(ctx, OpenRequest{Title: "everything at once", StartedAt: 900, EndedAt: 1500})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// An operator's own note, timestamped INSIDE the machine events rather
	// than at the end, so annotations are ordered by the same rule.
	if _, noteErr := h.svc.Annotate(ctx, inc.ID, AnnotateRequest{
		At: 1045, Author: "brian@pam", Body: "pulled the cable on eno1",
	}); noteErr != nil {
		t.Fatalf("Annotate: %v", noteErr)
	}

	tl, err := h.svc.Timeline(ctx, inc.ID)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}

	// 1. strictly chronological.
	for i := 1; i < len(tl.Events); i++ {
		if tl.Events[i].At < tl.Events[i-1].At {
			t.Fatalf("the timeline is not in chronological order at index %d: %d then %d",
				i, tl.Events[i-1].At, tl.Events[i].At)
		}
	}

	// 2. all five machine sources are present, plus annotations.
	seen := map[Source]int{}
	for _, e := range tl.Events {
		seen[e.Source]++
	}
	for _, src := range Sources() {
		if seen[src] == 0 {
			t.Errorf("no %s event on the timeline; all five sources plus annotations must appear", src)
		}
	}

	// 3. the order is genuinely interleaved — the exact sequence, with the
	// annotation slotted between the flow at 1040 and the finding at 1050.
	want := append([]Source{}, wantOrder[:5]...)
	want = append(want, SourceAnnotation)
	want = append(want, wantOrder[5:]...)
	if got := sourcesOf(tl.Events); !reflect.DeepEqual(got, want) {
		t.Errorf("timeline source order =\n  %v\nwant\n  %v", got, want)
	}
}

// TestAC5_ClosingKeepsTheTimelineAndReopeningShowsIt is acceptance criterion
// 5: "closing an incident does not delete its events; reopening shows the
// same timeline."
func TestAC5_ClosingKeepsTheTimelineAndReopeningShowsIt(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	h.setNow(990)
	inc, err := h.svc.Open(ctx, OpenRequest{Title: "the one that got closed", Actor: "brian@pam"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	h.seedInterleavedHistory()
	if _, noteErr := h.svc.Annotate(ctx, inc.ID, AnnotateRequest{At: 1045, Body: "note that must survive"}); noteErr != nil {
		t.Fatalf("Annotate: %v", noteErr)
	}

	h.setNow(1100)
	before, err := h.svc.Timeline(ctx, inc.ID)
	if err != nil {
		t.Fatalf("Timeline(before close): %v", err)
	}
	if len(before.Events) != 11 {
		t.Fatalf("CONTROL FAILED: %d events before closing, want 11 — the assertions below would be vacuous",
			len(before.Events))
	}

	if _, closeErr := h.svc.Close(ctx, inc.ID); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}
	after, err := h.svc.Timeline(ctx, inc.ID)
	if err != nil {
		t.Fatalf("Timeline(after close): %v", err)
	}
	if !reflect.DeepEqual(before.Events, after.Events) {
		t.Errorf("closing the incident changed its timeline.\nbefore: %+v\nafter:  %+v", before.Events, after.Events)
	}
	if after.Incident.Status != "closed" || after.Incident.ClosedAt != 1100 {
		t.Errorf("after Close, incident = %+v, want closed at 1100", after.Incident)
	}

	// Reopening: same events, and the window is live again.
	h.setNow(1200)
	if _, reopenErr := h.svc.Reopen(ctx, inc.ID); reopenErr != nil {
		t.Fatalf("Reopen: %v", reopenErr)
	}
	reopened, err := h.svc.Timeline(ctx, inc.ID)
	if err != nil {
		t.Fatalf("Timeline(after reopen): %v", err)
	}
	if !reflect.DeepEqual(before.Events, reopened.Events) {
		t.Errorf("reopening did not show the same timeline.\nbefore:   %+v\nreopened: %+v",
			before.Events, reopened.Events)
	}
	if !reopened.Window.Live || reopened.Window.To != 1200 {
		t.Errorf("a reopened incident's window = %+v, want live and running to now (1200)", reopened.Window)
	}

	// Control: the reopened window really is live — an event that happens
	// now shows up, so the equality above is about nothing having been
	// deleted rather than about the timeline being frozen or empty.
	h.seedFinding(1150, "health:mtu_mismatch|bridge:pve1:vmbr0", "new")
	withNew, err := h.svc.Timeline(ctx, inc.ID)
	if err != nil {
		t.Fatalf("Timeline(after a new event): %v", err)
	}
	if len(withNew.Events) != len(before.Events)+1 {
		t.Errorf("CONTROL FAILED: a new event in a reopened incident's window gave %d events, want %d",
			len(withNew.Events), len(before.Events)+1)
	}
}
