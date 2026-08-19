// apply_sdn_foreign.go implements T-3101-followup-01's fix (debt-sweep
// 2026-08-19, item 2): PVE's PUT /cluster/sdn (sdn.apply) commits ALL
// pending SDN state cluster-wide, not just what a changeset's own ops
// staged. An operator who stages an SDN edit in the PVE GUI and leaves it
// unapplied has it silently swept in and committed by the next unrelated
// changeset an operator approves through vnprox — never validated, never
// diffed, never in vnprox's audit trail as an applied mutation, and not
// covered by vnprox's own rollback reasoning. This inverts CLAUDE.md's
// core guarantee ("never apply network changes outside the change
// engine") from the inside: nothing here bypasses the change engine to
// write config; a mutation that never entered it gets applied BY it.
//
// THE OWNER'S DECISION (planning/tasks/debt-sweep-2026-08-19.md, not
// re-litigated here): "surface and confirm", not block, not lock-taking.
// Detect foreign pending SDN state before apply, show it on the review
// screen as an explicit "this apply will also commit ..." list, and
// require an explicit, SERVER-RECORDED operator acknowledgement before
// apply proceeds — never a client-supplied boolean (review.go's
// ReviewApprove/ReviewReject set this precedent: an authorization
// decision must be readable back from a row a prior, separately-audited
// call wrote).
//
// WHY "FOREIGN" CAN ONLY MEAN "TIMING", NOT ATTRIBUTION. Real PVE 9.2.4
// tracks pending SDN state cluster-wide with no concept of who staged it —
// evidence: planning/reports/evidence/pve-9.2.4-sdn-pending-state.txt,
// gathered read-only against pvecube, quoting PVE::Network::SDN's own
// has_pending_changes(), a bare boolean over the whole cluster with no
// per-session/per-author dimension at all. There is no PVE call that
// answers "is this specific pending zone/vnet/subnet edit mine". So this
// file's detection point is TIMING: SDNPendingForeign (apply_seams.go) is
// called from beginApply, before this changeset's own SDNStageOp calls run
// — the same "before any mutation" moment apply_snapshot.go's
// captureSnapshotFull calls SDNConfig for the pre-apply snapshot. Every
// entry PVE reports at that moment necessarily predates and is therefore
// not caused by this changeset; it is foreign by construction, not by
// inference.
//
// THE "HONEST DIFF" REQUIREMENT (CLAUDE.md, this card's own text): the
// operator must see precisely what they are additionally committing.
// AcknowledgeSDNForeignPending never accepts a client-supplied entry
// list — it re-detects live itself and records exactly that. beginApply's
// gate (isSDNForeignPendingCovered) then requires the CURRENT live-
// detected set to be covered, entry-for-entry (kind+id+state+fields, not
// merely "something was acknowledged at some point"), by the last
// recorded acknowledgement — a foreign edit that appeared after the
// operator last looked is not silently waved through by a stale ack.

package change

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/store"
)

// ErrSDNForeignPendingUnacknowledged is returned by Apply when this
// changeset carries SDN ops (plan.hasSDN()) and PVE currently reports
// foreign pending SDN state (SDNPendingForeign, apply_seams.go) that is
// not fully covered by this changeset's last recorded acknowledgement.
// This is the owner's "surface and confirm" decision, not a block: the
// changeset is left exactly where it was (beginApply checks this before
// any status transition, snapshot, or mutation — the same placement as
// ErrApprovalRequired/ErrTwoPersonRequired), and apply may proceed the
// moment AcknowledgeSDNForeignPending records an acknowledgement covering
// Entries. The API layer maps this to a new, documented 422 with the
// stable code sdn_foreign_pending_unacknowledged (docs/api.md).
type ErrSDNForeignPendingUnacknowledged struct {
	ID      string
	Entries []SDNPendingEntry
}

func (e *ErrSDNForeignPendingUnacknowledged) Error() string {
	return fmt.Sprintf(
		"change: changeset %s's apply would also commit %d foreign pending SDN change(s) never entered through vnprox's change engine; acknowledge them first",
		e.ID, len(e.Entries),
	)
}

// sdnPendingEntryKeys returns a sorted, deterministic set of canonical JSON
// strings, one per entry — the comparison unit isSDNForeignPendingCovered
// uses. encoding/json sorts map keys when marshaling a map, so two entries
// with identical Kind/ID/State/Fields always produce identical strings
// regardless of Go map iteration order or slice order.
func sdnPendingEntryKeys(entries []SDNPendingEntry) ([]string, error) {
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			return nil, fmt.Errorf("change: encoding sdn pending entry for comparison: %w", err)
		}
		keys = append(keys, string(b))
	}
	sort.Strings(keys)
	return keys, nil
}

// detectSDNForeignPending calls pveGW.SDNPendingForeign and filters to only
// the entries actually out of sync (State != ""). Real PVE's own
// "?pending=1" view (and internal/pve's sdnPendingEntries) already omits
// in-sync objects, but PVEGateway is a seam other code (tests, a future
// implementation) can satisfy differently, so this does not trust that
// invariant blindly. Returned in a stable (kind, then id) order so the
// review screen and the acknowledgement record are deterministic.
func detectSDNForeignPending(ctx context.Context, pveGW PVEGateway) ([]SDNPendingEntry, error) {
	if pveGW == nil {
		return nil, nil
	}
	all, err := pveGW.SDNPendingForeign(ctx)
	if err != nil {
		return nil, fmt.Errorf("change: detecting foreign pending sdn state: %w", err)
	}
	out := make([]SDNPendingEntry, 0, len(all))
	for _, e := range all {
		if e.State == "" {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// SDNForeignPending is the review-screen read: a live, fresh call to
// pveGW.SDNPendingForeign for changeset id, scoped so it returns (nil, nil)
// — nothing to show, no PVE round trip — for a changeset whose plan
// carries no SDN ops at all; there is nothing that changeset's apply could
// sweep in. When the plan DOES carry SDN ops, pveGW is required: a nil
// gateway is reported as an error (the same "no PVE gateway available (no
// user session)" refusal apply_snapshot.go's captureSnapshotFull already
// uses for the identical reason) rather than silently answering "nothing
// foreign" when foreign state might well exist and simply couldn't be
// checked.
func (s *Service) SDNForeignPending(ctx context.Context, id string, pveGW PVEGateway) ([]SDNPendingEntry, error) {
	cs, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	plan, err := BuildPlan(cs.Ops)
	if err != nil {
		return nil, err
	}
	if !plan.hasSDN() {
		return nil, nil
	}
	if pveGW == nil {
		return nil, fmt.Errorf("change: checking foreign pending sdn state for changeset %s: no PVE gateway available (no user session)", id)
	}
	return detectSDNForeignPending(ctx, pveGW)
}

// AcknowledgeSDNForeignPending records the operator's acknowledgement of
// whatever foreign pending SDN state PVE reports RIGHT NOW — a fresh,
// server-side, live call, never a client-supplied list (review.go's
// ReviewApprove/ReviewReject precedent). The returned entries are exactly
// what was just recorded, for the API layer to echo back so the review
// screen can confirm precisely what was signed off on.
func (s *Service) AcknowledgeSDNForeignPending(ctx context.Context, id, actor string, pveGW PVEGateway) ([]SDNPendingEntry, error) {
	if s.sdnPendingAcks == nil {
		return nil, &ErrReviewNotConfigured{}
	}
	entries, err := s.SDNForeignPending(ctx, id, pveGW)
	if err != nil {
		return nil, err
	}
	entriesJSON, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("change: encoding sdn pending ack for changeset %s: %w", id, err)
	}
	if err := s.sdnPendingAcks.Upsert(ctx, store.ChangesetSDNPendingAck{
		ChangesetID:    id,
		AcknowledgedBy: actor,
		EntriesJSON:    string(entriesJSON),
		AcknowledgedAt: s.now().Unix(),
	}); err != nil {
		return nil, fmt.Errorf("change: recording sdn pending ack for changeset %s: %w", id, err)
	}
	s.appendAudit(ctx, actor, "changeset.sdn_pending_ack", "acknowledged", id, map[string]any{"entryCount": len(entries)})
	return entries, nil
}

// clearSDNPendingAck removes any recorded foreign-SDN-pending
// acknowledgement for changesetID — called on every UpdateDraft
// (service.go), mirroring clearApproval (review.go): the ops just changed,
// so any prior acknowledgement (recorded against the old ops, which
// determine what beginApply even treats as "this changeset's own" via
// timing) is stale.
func (s *Service) clearSDNPendingAck(ctx context.Context, changesetID string) {
	if s.sdnPendingAcks == nil {
		return
	}
	if err := s.sdnPendingAcks.Clear(ctx, changesetID); err != nil {
		s.log.Error("change: clearing sdn pending ack after edit", "changeset_id", changesetID, "error", err)
	}
}

// isSDNForeignPendingCovered reports whether current (the live-detected
// foreign-pending set, already filtered to State != "" by
// detectSDNForeignPending) is fully covered by changesetID's last recorded
// acknowledgement — every entry in current must appear, byte-for-byte
// (kind, id, state, and fields all matching), in the acknowledged set. A
// changeset with no recorded acknowledgement covers only the empty set.
//
// Deliberately NOT set-equality: an acknowledgement that covered MORE than
// what's currently pending (some foreign edit was applied or reverted
// since) still covers current just fine — only a NEW entry appearing since
// the ack must force re-acknowledgement, per the "honest diff" requirement
// this file's package doc comment states.
func (s *Service) isSDNForeignPendingCovered(ctx context.Context, changesetID string, current []SDNPendingEntry) (bool, error) {
	if len(current) == 0 {
		return true, nil
	}
	if s.sdnPendingAcks == nil {
		return false, nil
	}
	ack, err := s.sdnPendingAcks.Get(ctx, changesetID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("change: reading sdn pending ack for changeset %s: %w", changesetID, err)
	}
	var acked []SDNPendingEntry
	if unmarshalErr := json.Unmarshal([]byte(ack.EntriesJSON), &acked); unmarshalErr != nil {
		return false, fmt.Errorf("change: decoding sdn pending ack for changeset %s: %w", changesetID, unmarshalErr)
	}
	ackedKeys, err := sdnPendingEntryKeys(acked)
	if err != nil {
		return false, err
	}
	ackedSet := make(map[string]bool, len(ackedKeys))
	for _, k := range ackedKeys {
		ackedSet[k] = true
	}
	currentKeys, err := sdnPendingEntryKeys(current)
	if err != nil {
		return false, err
	}
	for _, k := range currentKeys {
		if !ackedSet[k] {
			return false, nil
		}
	}
	return true, nil
}
