// SPDX-License-Identifier: Apache-2.0

// maintenance.go implements T-4007's declared node maintenance window: a
// {start, end} pair, scoped to one node, that internal/findings suppresses
// that node's findings/alerts against — visibly (see findings/maintenance.go
// for how a suppressed finding stays on screen) and only for as long as the
// window is actually active.
//
// WHY THIS IS NOT A PolicyRule, unlike T-4006's freeze window. A freeze
// window has to be an ordinary PolicyRule because it is enforced by the
// SAME EvaluatePolicy path every other deny/warn rule runs through — "no
// second enforcement point" is that card's own constraint. A maintenance
// window enforces nothing: it says nothing about whether a changeset may
// apply, and it is never passed to EvaluatePolicy at all. Reusing PolicySet
// for it would either silently start blocking applies during "maintenance"
// (never asked for) or force every PolicyRule consumer to grow a
// maintenance-shaped special case. What IS reused from T-4006, faithfully,
// is the TIME representation and its timezone discipline: Start/End are
// absolute unix instants, resolved once at declare time from an operator-
// supplied local wall-clock start/end plus a MANDATORY IANA zone —
// DeclareMaintenanceWindow refuses an empty zone exactly as
// PolicySet.Validate refuses a rule naming a wall-clock time.* fact with
// none (policy_eval.go's localTimeFactFields check). And the CALENDAR VIEW:
// GET /calendar (Calendar, below) renders a cluster's maintenance windows
// alongside its freeze windows and pending schedules on one timeline,
// because both are, in the card's own words, "declared time ranges" an
// operator wants to see together.
//
// Cluster-aware by construction: Node is any node name this cluster's
// inventory or a peer's knows about — nothing here assumes localhost, and
// internal/findings' suppression check (a plain Node-string comparison
// against a finding's own Nodes) works identically whether the finding was
// raised locally or fanned in from a peer.

package change

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// maintenanceTimeLayout is the wall-clock format DeclareMaintenanceWindow
// accepts for StartLocal/EndLocal: a floating local time with no offset,
// resolved against the mandatory Zone — the same "local wall clock, zone
// supplies the offset" contract findings.QuietHours and T-4006's
// time.minuteOfDay/time.weekday facts already use in this codebase.
const maintenanceTimeLayout = "2006-01-02T15:04:05"

// maxMaintenanceReasonLen bounds the stored reason — a justification, not a
// blob store, mirroring maxFreezeOverrideReasonLen/maxAckReasonLen.
const maxMaintenanceReasonLen = 1000

// MaintenanceWindow is one declared node maintenance window, as the API and
// GET /calendar render it.
//
//nolint:govet // fieldalignment: wire shape: field order is the documented JSON contract, matching FreezeWindowView's own precedent.
type MaintenanceWindow struct {
	ID        string `json:"id"`
	Node      string `json:"node"`
	Reason    string `json:"reason,omitempty"`
	CreatedBy string `json:"createdBy"`
	// Zone is the IANA name the window was DECLARED in (kept for display/
	// audit fidelity — Start/End below are already resolved absolute
	// instants, so evaluation never needs to re-load it).
	Zone      string `json:"zone"`
	Start     int64  `json:"start"`
	End       int64  `json:"end"`
	CreatedAt int64  `json:"createdAt"`
}

// Active reports whether the window is suppressing at instant now — a
// half-open interval [Start, End): a finding evaluated at exactly Start is
// suppressed, one evaluated at exactly End is not. That is AC3's whole
// contract ("a window's suppression stops automatically the instant its
// end passes") stated as an inequality: the boundary instant belongs to
// "maintenance is over", never to one extra instant of silence.
func (w MaintenanceWindow) Active(now time.Time) bool {
	t := now.Unix()
	return t >= w.Start && t < w.End
}

// MaintenanceWindowInput is DeclareMaintenanceWindow's request shape: a
// node-scoped {start, end} pair declared in local wall clock plus a
// mandatory zone, per this file's own doc comment on why the zone can never
// be inferred.
type MaintenanceWindowInput struct {
	Node       string `json:"node"`
	Reason     string `json:"reason,omitempty"`
	Zone       string `json:"zone"`
	StartLocal string `json:"startLocal"`
	EndLocal   string `json:"endLocal"`
}

// ErrMaintenanceWindowNotConfigured is returned by every maintenance-window
// method when this Service was built with no maintenance-window store
// wired.
type ErrMaintenanceWindowNotConfigured struct{}

func (e *ErrMaintenanceWindowNotConfigured) Error() string {
	return "change: maintenance-window storage is not configured on this Service"
}

// ErrMaintenanceWindowInvalid is returned by DeclareMaintenanceWindow for
// every statically-decidable defect in the request, naming the offending
// field the same way *PolicyLoadError names one for a policy document.
type ErrMaintenanceWindowInvalid struct {
	Field string
	Msg   string
}

func (e *ErrMaintenanceWindowInvalid) Error() string {
	return fmt.Sprintf("change: maintenance window: field %q: %s", e.Field, e.Msg)
}

// DeclareMaintenanceWindow records a new node-scoped maintenance window,
// attributed to actor. It is audited under its own action,
// `change.maintenance_declare` — an operator filtering the audit log for
// "who put a node into maintenance, and when" must find every declaration
// without knowing which finding responses imply one, the same reasoning
// InvokeFreezeOverride's doc comment gives for its own audit action.
//
// The zone is REQUIRED (in.Zone == "" is refused before anything is parsed
// or written) and must be a loadable IANA name; StartLocal/EndLocal must
// parse as maintenanceTimeLayout in that zone, and the resolved end instant
// must be strictly after the resolved start instant. All four are
// statically decidable, so all four fail closed with
// *ErrMaintenanceWindowInvalid rather than declaring a window nothing can
// ever suppress correctly.
func (s *Service) DeclareMaintenanceWindow(ctx context.Context, actor string, in MaintenanceWindowInput) (MaintenanceWindow, error) {
	if s.maintenanceWindows == nil {
		return MaintenanceWindow{}, &ErrMaintenanceWindowNotConfigured{}
	}
	node := strings.TrimSpace(in.Node)
	if node == "" {
		return MaintenanceWindow{}, &ErrMaintenanceWindowInvalid{Field: "node", Msg: "a maintenance window must name exactly one node"}
	}
	reason := strings.TrimSpace(in.Reason)
	if len(reason) > maxMaintenanceReasonLen {
		return MaintenanceWindow{}, &ErrMaintenanceWindowInvalid{Field: "reason", Msg: fmt.Sprintf("exceeds %d characters", maxMaintenanceReasonLen)}
	}
	if strings.TrimSpace(in.Zone) == "" {
		// T-4006's line, held here too: no UTC fallback, no server-local
		// guess. A maintenance window that silently meant UTC would suppress
		// findings at the wrong wall-clock hour precisely when it matters.
		return MaintenanceWindow{}, &ErrMaintenanceWindowInvalid{Field: "zone", Msg: "an explicit IANA timezone is required (no UTC or server-local fallback)"}
	}
	loc, err := time.LoadLocation(in.Zone)
	if err != nil {
		return MaintenanceWindow{}, &ErrMaintenanceWindowInvalid{Field: "zone", Msg: fmt.Sprintf("%q: %v", in.Zone, err)}
	}
	start, err := time.ParseInLocation(maintenanceTimeLayout, in.StartLocal, loc)
	if err != nil {
		return MaintenanceWindow{}, &ErrMaintenanceWindowInvalid{Field: "startLocal", Msg: fmt.Sprintf("must be %q: %v", maintenanceTimeLayout, err)}
	}
	end, err := time.ParseInLocation(maintenanceTimeLayout, in.EndLocal, loc)
	if err != nil {
		return MaintenanceWindow{}, &ErrMaintenanceWindowInvalid{Field: "endLocal", Msg: fmt.Sprintf("must be %q: %v", maintenanceTimeLayout, err)}
	}
	if !end.After(start) {
		return MaintenanceWindow{}, &ErrMaintenanceWindowInvalid{Field: "endLocal", Msg: "must be after startLocal — a zero-length or backwards window suppresses nothing, which is never what declaring one means"}
	}

	now := s.now()
	row := store.MaintenanceWindow{
		ID: store.NewULID(), Node: node, Reason: reason, CreatedBy: actor,
		Zone: in.Zone, Start: start.Unix(), End: end.Unix(), CreatedAt: now.Unix(),
	}
	if err := s.maintenanceWindows.Create(ctx, row); err != nil {
		return MaintenanceWindow{}, fmt.Errorf("change: declaring maintenance window for node %s: %w", node, err)
	}
	w := maintenanceWindowFromRow(row)
	s.appendAudit(ctx, actor, "change.maintenance_declare", "declared", node, map[string]any{
		"windowId": w.ID, "reason": reason, "zone": in.Zone, "start": w.Start, "end": w.End,
	})
	s.log.Info("change: maintenance window declared", "window_id", w.ID, "node", node, "actor", actor, "start", w.Start, "end", w.End)
	return w, nil
}

func maintenanceWindowFromRow(row store.MaintenanceWindow) MaintenanceWindow {
	return MaintenanceWindow{
		ID: row.ID, Node: row.Node, Reason: row.Reason, CreatedBy: row.CreatedBy,
		Zone: row.Zone, Start: row.Start, End: row.End, CreatedAt: row.CreatedAt,
	}
}

// MaintenanceWindows returns every declared maintenance window, expired
// ones included — the same "let the caller decide what's still active"
// contract store.MaintenanceWindowRepo.List documents. This is the read
// GET /calendar and findings.MaintenanceProvider both consume; a nil store
// (not configured) reports an empty slice rather than an error, the same
// "absent feature is a no-op" convention Calendar's own doc comment
// documents for FreezeWindows/Schedules.
func (s *Service) MaintenanceWindows(ctx context.Context) ([]MaintenanceWindow, error) {
	if s.maintenanceWindows == nil {
		return nil, nil
	}
	rows, err := s.maintenanceWindows.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("change: listing maintenance windows: %w", err)
	}
	out := make([]MaintenanceWindow, 0, len(rows))
	for _, row := range rows {
		out = append(out, maintenanceWindowFromRow(row))
	}
	return out, nil
}

// DeleteMaintenanceWindow removes a declared window before its own end —
// an operator finishing early, or correcting a mistaken declaration. Not an
// error to delete an absent one, mirroring store.MaintenanceWindowRepo
// .Delete's own "always clearable" contract. Audited like the declaration
// itself, so "who ended maintenance on this node, and when" is answerable
// from the log alone.
func (s *Service) DeleteMaintenanceWindow(ctx context.Context, actor, id string) error {
	if s.maintenanceWindows == nil {
		return &ErrMaintenanceWindowNotConfigured{}
	}
	if err := s.maintenanceWindows.Delete(ctx, id); err != nil {
		return fmt.Errorf("change: clearing maintenance window %s: %w", id, err)
	}
	s.appendAudit(ctx, actor, "change.maintenance_clear", "cleared", id, nil)
	return nil
}

// MaintenanceState is one node's current maintenance-window read model —
// the visibility pattern T-4006's own report flagged as missing
// (TwoPersonState.breakGlass's "what's on record right now", generalized to
// maintenance): whether Node is inside a declared window at this instant,
// and which one. Built the same way TwoPersonState is: a live read against
// server-side state, never a second opinion cached anywhere.
//
//nolint:govet // fieldalignment: wire shape, matching TwoPersonState's own precedent.
type MaintenanceState struct {
	Node   string             `json:"node"`
	Window *MaintenanceWindow `json:"window,omitempty"`
	Active bool               `json:"active"`
}

// MaintenanceState reports node's current maintenance state. When more than
// one declared window covers node at once (an operator error, but not one
// this layer refuses), the window ending SOONEST is reported — the more
// urgent "why", and a deterministic choice so two callers asking at the
// same instant never disagree.
func (s *Service) MaintenanceState(ctx context.Context, node string) (MaintenanceState, error) {
	state := MaintenanceState{Node: node}
	windows, err := s.MaintenanceWindows(ctx)
	if err != nil {
		return MaintenanceState{}, err
	}
	now := s.now()
	for i := range windows {
		w := windows[i]
		if w.Node != node || !w.Active(now) {
			continue
		}
		if state.Window == nil || w.End < state.Window.End {
			wCopy := w
			state.Window = &wCopy
			state.Active = true
		}
	}
	return state, nil
}
