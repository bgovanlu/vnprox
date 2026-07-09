package change

import (
	"errors"
	"testing"
)

// wantTransitions is this test's own, independently-derived expectation of
// the legal (from, to) status graph (docs/architecture.md §4,
// docs/features/change-management.md §4, docs/data-model.md §2's status
// column comment) — deliberately written out by hand rather than
// reflecting allowedTransitions back at itself, so a bug in that map is
// actually caught.
func wantTransitions() map[[2]Status]bool {
	want := map[[2]Status]bool{}
	set := func(from, to Status) { want[[2]Status{from, to}] = true }

	// draft: may be validated, sent to apply, or discarded.
	set(StatusDraft, StatusValidated)
	set(StatusDraft, StatusApplying)
	set(StatusDraft, StatusDiscarded)
	// validated: editing invalidates back to draft; may also go straight
	// to apply, or be discarded.
	set(StatusValidated, StatusDraft)
	set(StatusValidated, StatusApplying)
	set(StatusValidated, StatusDiscarded)
	// applying: succeeds into the confirm window, or fails outright.
	set(StatusApplying, StatusAwaitingConfirm)
	set(StatusApplying, StatusFailed)
	// awaiting_confirm: user confirms (committed) or the deadline elapses /
	// manual rollback (rolled_back); failed when that rollback could not
	// fully restore every node (T-205).
	set(StatusAwaitingConfirm, StatusCommitted)
	set(StatusAwaitingConfirm, StatusRolledBack)
	set(StatusAwaitingConfirm, StatusFailed)
	// committed/rolled_back/failed/discarded: terminal, no outgoing edges.

	return want
}

// TestChangeset_StateMachine_ExhaustiveTable is T-201 acceptance criterion
// 2: every (state, action) pair — here every (from, to) pair across all 8
// statuses, i.e. 64 combinations — is asserted allowed or denied.
func TestChangeset_StateMachine_ExhaustiveTable(t *testing.T) {
	want := wantTransitions()
	if len(AllStatuses) != 8 {
		t.Fatalf("AllStatuses has %d entries, want 8 — this test assumes exactly the 8 documented statuses", len(AllStatuses))
	}

	for _, from := range AllStatuses {
		for _, to := range AllStatuses {
			c := Changeset{Status: from}
			got := c.CanTransition(to)
			wantOK := want[[2]Status{from, to}]
			if got != wantOK {
				t.Errorf("CanTransition: %s -> %s = %v, want %v", from, to, got, wantOK)
			}

			// Transition itself must agree with CanTransition, and must
			// never mutate c on a denied transition.
			cc := c
			err := cc.Transition(to, 999)
			if wantOK {
				if err != nil {
					t.Errorf("Transition(%s -> %s) = %v, want nil", from, to, err)
				} else if cc.Status != to || cc.UpdatedAt != 999 {
					t.Errorf("Transition(%s -> %s): got Status=%s UpdatedAt=%d, want Status=%s UpdatedAt=999", from, to, cc.Status, cc.UpdatedAt, to)
				}
			} else {
				if err == nil {
					t.Errorf("Transition(%s -> %s) = nil, want an error", from, to)
				}
				var illegal *ErrIllegalTransition
				if !errors.As(err, &illegal) {
					t.Errorf("Transition(%s -> %s) error type = %T, want *ErrIllegalTransition", from, to, err)
				} else if illegal.From != from || illegal.To != to {
					t.Errorf("ErrIllegalTransition = {From:%s To:%s}, want {From:%s To:%s}", illegal.From, illegal.To, from, to)
				}
				if cc.Status != from {
					t.Errorf("Transition(%s -> %s) denied but mutated Status to %s", from, to, cc.Status)
				}
			}
		}
	}
}

func TestChangeset_Editable(t *testing.T) {
	editable := map[Status]bool{
		StatusDraft: true, StatusValidated: true,
		StatusApplying: false, StatusAwaitingConfirm: false,
		StatusCommitted: false, StatusRolledBack: false,
		StatusFailed: false, StatusDiscarded: false,
	}
	for _, s := range AllStatuses {
		c := Changeset{Status: s}
		if got := c.Editable(); got != editable[s] {
			t.Errorf("Changeset{Status: %s}.Editable() = %v, want %v", s, got, editable[s])
		}
	}
}

// TestChangeset_NoTransitionsOutOfTerminalStates double-checks the four
// terminal statuses specifically (already covered by the exhaustive table
// above, but spelled out explicitly since it's the safety-relevant
// property: once committed/rolled_back/failed/discarded, nothing about the
// changeset's own status can ever change again).
func TestChangeset_NoTransitionsOutOfTerminalStates(t *testing.T) {
	terminal := []Status{StatusCommitted, StatusRolledBack, StatusFailed, StatusDiscarded}
	for _, from := range terminal {
		for _, to := range AllStatuses {
			c := Changeset{Status: from}
			if c.CanTransition(to) {
				t.Errorf("terminal status %s must not permit any transition, but CanTransition(%s) = true", from, to)
			}
		}
	}
}
