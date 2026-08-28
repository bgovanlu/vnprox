// SPDX-License-Identifier: Apache-2.0

package presence_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/presence"
)

// TestPresence_IsPerChangesetAndPerEntity is the first half of T-2805 AC5:
// presence is tracked per changeset AND per entity, and a scope reports only
// the people actually on it.
func TestPresence_IsPerChangesetAndPerEntity(t *testing.T) {
	f := newFixture(t, 15*time.Minute)

	f.svc.ConnOpened("c1", "alice@pam", "sess-alice")
	f.svc.ConnOpened("c2", "bob@pam", "sess-bob")
	f.svc.ConnTopics("c1", []string{"topology", "presence:changeset:cs-1", "presence:entity:bridge:pve1:vmbr0"})
	f.svc.ConnTopics("c2", []string{"presence:changeset:cs-1"})

	cs := f.svc.Scope(presence.ChangesetScope("cs-1"))
	if cs.Count != 2 {
		t.Errorf("changeset scope count = %d, want 2", cs.Count)
	}
	if len(cs.Viewers) != 2 || cs.Viewers[0].User != "alice@pam" || cs.Viewers[1].User != "bob@pam" {
		t.Errorf("changeset viewers = %+v, want alice@pam and bob@pam in order", cs.Viewers)
	}

	ent := f.svc.Scope(presence.EntityScope("bridge:pve1:vmbr0"))
	if ent.Count != 1 || len(ent.Viewers) != 1 || ent.Viewers[0].User != "alice@pam" {
		t.Errorf("entity viewers = %+v, want only alice@pam", ent.Viewers)
	}

	// A scope nobody is on is an ANSWER, not an absence.
	empty := f.svc.Scope(presence.ChangesetScope("cs-nobody"))
	if empty.Count != 0 || len(empty.Viewers) != 0 || empty.Scope != "changeset:cs-nobody" {
		t.Errorf("empty scope = %+v, want a zero-count answer naming the scope", empty)
	}

	all := f.svc.Scopes()
	if len(all) != 2 {
		t.Fatalf("Scopes() = %+v, want the two scopes actually being viewed", all)
	}
	if all[0].Scope != "changeset:cs-1" || all[1].Scope != "entity:bridge:pve1:vmbr0" {
		t.Errorf("Scopes() order/content = %+v", all)
	}
}

// TestPresence_OnePersonWithTwoTabsIsOneViewer: the count is DISTINCT
// people, so a second browser tab does not look like a second colleague.
func TestPresence_OnePersonWithTwoTabsIsOneViewer(t *testing.T) {
	f := newFixture(t, 15*time.Minute)

	f.svc.ConnOpened("c1", "alice@pam", "sess-alice")
	f.svc.ConnOpened("c2", "alice@pam", "sess-alice")
	f.svc.ConnOpened("c3", "alice@pam", "sess-alice-2")
	for _, id := range []string{"c1", "c2", "c3"} {
		f.svc.ConnTopics(id, []string{"presence:changeset:cs-1"})
	}

	got := f.svc.Scope(presence.ChangesetScope("cs-1"))
	if got.Count != 1 || len(got.Viewers) != 1 {
		t.Fatalf("scope = %+v, want exactly one viewer", got)
	}
	if got.Viewers[0].Sessions != 2 {
		t.Errorf("sessions = %d, want 2 (two sessions, three tabs, one person)", got.Viewers[0].Sessions)
	}
}

// TestPresence_UnknownScopesAreNotTracked: a client cannot mint arbitrary
// presence channels by subscribing to whatever it likes.
func TestPresence_UnknownScopesAreNotTracked(t *testing.T) {
	f := newFixture(t, 15*time.Minute)

	f.svc.ConnOpened("c1", "alice@pam", "sess-alice")
	f.svc.ConnTopics("c1", []string{
		"presence:", "presence:changeset:", "presence:entity:", "presence:nonsense:x", "metrics:iface:pve1:eno1",
	})
	if got := f.svc.Scopes(); len(got) != 0 {
		t.Errorf("Scopes() = %+v, want none — every subscribed topic was an invalid scope", got)
	}
}

// TestPresenceEvent_CarriesNoIdentities is T-2805 AC5's WS half, and it is a
// structural assertion rather than a filtering one: the hub fans ONE
// pre-encoded payload out to every subscriber of a topic and has no
// per-subscriber view of capabilities, so any identity on this event would
// reach subscribers who may not see it. The event therefore carries a count
// and nothing else — the same split drift.changed/findings.changed use.
func TestPresenceEvent_CarriesNoIdentities(t *testing.T) {
	f := newFixture(t, 15*time.Minute)

	f.svc.ConnOpened("c1", "alice@pam", "sess-alice")
	f.svc.ConnTopics("c1", []string{"presence:changeset:cs-1"})
	f.svc.ConnOpened("c2", "bob@pam", "sess-bob")
	f.svc.ConnTopics("c2", []string{"presence:changeset:cs-1"})

	if len(f.ws.msgs) == 0 {
		t.Fatal("no presence.changed broadcast at all — presence must ride the existing event stream")
	}
	last := f.ws.msgs[len(f.ws.msgs)-1]
	if last.topic != "presence:changeset:cs-1" {
		t.Errorf("broadcast topic = %q, want presence:changeset:cs-1", last.topic)
	}
	body := string(last.payload)
	if !strings.Contains(body, `"event":"presence.changed"`) {
		t.Errorf("payload = %s, want the flat {\"event\": ...} envelope", body)
	}
	if !strings.Contains(body, `"count":2`) {
		t.Errorf("payload = %s, want count 2", body)
	}
	for _, name := range []string{"alice", "bob", "sess-", "viewers", "holder", "user"} {
		if strings.Contains(body, name) {
			t.Errorf("presence.changed payload contains %q: %s — the WS surface must never carry identities, because the hub cannot filter it per subscriber", name, body)
		}
	}
}

// TestPresence_LeavingBroadcastsTheDepartedScope: presence has to go down as
// well as up, or a departed colleague is shown forever.
func TestPresence_LeavingBroadcastsTheDepartedScope(t *testing.T) {
	f := newFixture(t, 15*time.Minute)

	f.svc.ConnOpened("c1", "alice@pam", "sess-alice")
	f.svc.ConnTopics("c1", []string{"presence:changeset:cs-1"})
	f.svc.ConnTopics("c1", []string{"presence:changeset:cs-2"})

	if got := f.svc.Scope(presence.ChangesetScope("cs-1")); got.Count != 0 {
		t.Errorf("cs-1 count after moving away = %d, want 0 (a subscribe REPLACES the topic set)", got.Count)
	}
	if got := f.svc.Scope(presence.ChangesetScope("cs-2")); got.Count != 1 {
		t.Errorf("cs-2 count = %d, want 1", got.Count)
	}

	var sawCs1Zero bool
	for _, m := range f.ws.msgs {
		if m.topic == "presence:changeset:cs-1" && strings.Contains(string(m.payload), `"count":0`) {
			sawCs1Zero = true
		}
	}
	if !sawCs1Zero {
		t.Error("no presence.changed with count 0 on the departed scope; leaving must be pushed, not only joining")
	}
}

// TestConnClosed_ReleasesThatSessionsLocks is the service-level half of
// T-2805 AC3 (the transport-level half, which drops a real WebSocket, is in
// ws_disconnect_test.go). The control leg is the other session's lock: if
// ConnClosed released everything, this would pass for the wrong reason.
func TestConnClosed_ReleasesThatSessionsLocks(t *testing.T) {
	f := newFixture(t, 15*time.Minute)
	ctx := context.Background()

	f.svc.ConnOpened("c-alice", "alice@pam", "sess-alice")
	f.svc.ConnOpened("c-bob", "bob@pam", "sess-bob")
	if _, err := f.svc.Stage(ctx, "cs-alice", []string{"bridge:pve1:vmbr0"}, alice(), false); err != nil {
		t.Fatalf("alice Stage: %v", err)
	}
	if _, err := f.svc.Stage(ctx, "cs-bob", []string{"bridge:pve2:vmbr0"}, bob(), false); err != nil {
		t.Fatalf("bob Stage: %v", err)
	}

	f.svc.ConnClosed("c-alice")

	locks, err := f.svc.Locks(ctx)
	if err != nil {
		t.Fatalf("Locks: %v", err)
	}
	if len(locks) != 1 {
		t.Fatalf("locks after alice disconnected = %+v, want exactly bob's", locks)
	}
	if locks[0].Holder != "bob@pam" {
		t.Errorf("surviving lock holder = %q, want bob@pam — a disconnect must free only its own session's locks", locks[0].Holder)
	}
}

// TestConnClosed_KeepsLocksWhileAnotherTabIsStillOpen: closing one of two
// tabs is not a disconnect. A lock released on the first tab close would
// yank the entity out from under an operator who is still working.
func TestConnClosed_KeepsLocksWhileAnotherTabIsStillOpen(t *testing.T) {
	f := newFixture(t, 15*time.Minute)
	ctx := context.Background()

	f.svc.ConnOpened("tab-1", "alice@pam", "sess-alice")
	f.svc.ConnOpened("tab-2", "alice@pam", "sess-alice")
	if _, err := f.svc.Stage(ctx, "cs-alice", []string{"bridge:pve1:vmbr0"}, alice(), false); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	f.svc.ConnClosed("tab-1")
	locks, err := f.svc.Locks(ctx)
	if err != nil {
		t.Fatalf("Locks: %v", err)
	}
	if len(locks) != 1 {
		t.Fatalf("locks after closing one of two tabs = %+v, want the lock still held", locks)
	}

	f.svc.ConnClosed("tab-2")
	locks, err = f.svc.Locks(ctx)
	if err != nil {
		t.Fatalf("Locks: %v", err)
	}
	if len(locks) != 0 {
		t.Errorf("locks after the LAST connection closed = %+v, want none", locks)
	}
}

// TestConnOpened_WithoutASessionHoldsNothing: an unauthenticated or
// session-less connection contributes presence but can free no locks —
// fail-closed, and it must not free some other principal's session-less
// locks on the way out.
func TestConnOpened_WithoutASessionHoldsNothing(t *testing.T) {
	f := newFixture(t, 15*time.Minute)
	ctx := context.Background()

	if _, err := f.svc.Stage(ctx, "cs-token", []string{"bridge:pve1:vmbr0"},
		presence.Principal{Username: "automation@pve"}, false); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	f.svc.ConnOpened("c-anon", "", "")
	f.svc.ConnTopics("c-anon", []string{"presence:changeset:cs-token"})
	f.svc.ConnClosed("c-anon")

	locks, err := f.svc.Locks(ctx)
	if err != nil {
		t.Fatalf("Locks: %v", err)
	}
	if len(locks) != 1 {
		t.Errorf("locks after a session-less connection closed = %+v, want the session-less lock untouched (only expiry frees it)", locks)
	}

	// It does still expire.
	f.clock.Advance(16 * time.Minute)
	locks, err = f.svc.Locks(ctx)
	if err != nil {
		t.Fatalf("Locks after TTL: %v", err)
	}
	if len(locks) != 0 {
		t.Errorf("locks after the TTL = %+v, want none", locks)
	}
}

// TestScopeHelpers pins the two scope spellings so no caller concatenates
// the prefixes by hand and drifts from what the hub parses.
func TestScopeHelpers(t *testing.T) {
	if got := presence.ChangesetScope("cs-1"); got != "changeset:cs-1" {
		t.Errorf("ChangesetScope = %q", got)
	}
	if got := presence.EntityScope("bridge:pve1:vmbr0"); got != "entity:bridge:pve1:vmbr0" {
		t.Errorf("EntityScope = %q", got)
	}
	for _, tc := range []struct {
		scope string
		want  bool
	}{
		{"changeset:cs-1", true},
		{"entity:bridge:pve1:vmbr0", true},
		{"changeset:", false},
		{"entity:", false},
		{"", false},
		{"topology", false},
		{"presence:changeset:cs-1", false},
	} {
		if got := presence.ValidScope(tc.scope); got != tc.want {
			t.Errorf("ValidScope(%q) = %v, want %v", tc.scope, got, tc.want)
		}
	}
}
