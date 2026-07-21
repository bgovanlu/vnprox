package ha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/store"
)

// defaultAuditReplicationLimit bounds one replication pass's audit-log slice —
// the audit log grows unbounded, so it replicates incrementally (rows since the
// standby's high-water mark) rather than in full each pass. changesets/
// schedules/tokens are small bounded sets and replicate whole each pass
// (idempotent upserts).
const defaultAuditReplicationLimit = 500

// blobRecord is one content-addressed snapshot blob carried in a Batch: its
// sha256 digest and the plaintext (re-compressed idempotently on apply via
// BlobRepo.Put). Snapshot pre-state is replicated because a standby that
// promotes must be able to complete an in-flight changeset's rollback from the
// byte-exact pre-apply files — the enumerated {changesets, schedules, tokens,
// audit} set alone would leave a promoted standby unable to roll back.
type blobRecord struct {
	Hash    string
	Content string
}

// snapshotRecord bundles a snapshots row with its snapshot_files references so
// the standby can reconstruct the pre-apply restore point verbatim.
type snapshotRecord struct {
	Files []store.SnapshotFileRef
	Row   store.Snapshot
}

// Batch is one replication payload the active pushes to the standby. Lease is
// the active's current lease view (the heartbeat that keeps the standby from
// promoting); the row slices are the safety-critical app state. AuditSince is
// the id the audit slice starts after (informational; the standby applies by
// id regardless).
type Batch struct {
	Changesets []store.Changeset
	Schedules  []store.ChangesetSchedule
	Tokens     []store.APIToken
	Snapshots  []snapshotRecord
	Blobs      []blobRecord
	Audit      []store.AuditEntry
	Lease      Lease
	AuditSince int64
}

// Ack is the standby's reply to a push: its current lease term (so the active
// detects a superseding term and demotes) and its audit high-water mark after
// applying (so the active measures replication lag and advances its cursor).
type Ack struct {
	Role       string
	Term       int64
	AuditMaxID int64
}

// Replicator is the transport carrying a Batch from the active to the standby.
// Production wires a peer-channel-backed implementation (transport.go); the
// two-daemon harness wires an in-memory link with an injectable partition
// switch. Any error means the peer was unreachable this pass (a partition or a
// dead peer) — the active stays active and treats replication as degraded.
type Replicator interface {
	Push(ctx context.Context, batch Batch) (Ack, error)
}

// SnapshotSource reads this daemon's local replicable state (the active side of
// a pass). AuditHighWater is this daemon's own max audit id, used to compute
// replication lag against the standby's ack.
type SnapshotSource interface {
	Gather(ctx context.Context, sinceAuditID int64) (Batch, error)
	AuditHighWater(ctx context.Context) (int64, error)
}

// Applier writes a received Batch into this daemon's local store (the standby
// side). It never applies rows it should not — the Manager only calls Apply for
// a batch whose sender term is current-or-newer (the fencing check).
type Applier interface {
	Apply(ctx context.Context, batch Batch) error
	AuditHighWater(ctx context.Context) (int64, error)
}

// inFlightReplicatedStatuses are the changeset statuses whose pre-apply
// snapshots a promoted standby may still need to act on: an in-flight
// awaiting_confirm (a pending rollback/confirm), an applying changeset (crash
// recovery), or a committed one still inside its manual-rollback window.
var inFlightReplicatedStatuses = []string{"applying", "awaiting_confirm", "committed"}

// StoreReplication implements both SnapshotSource (active) and Applier
// (standby) over the app-store repositories. One instance is constructed per
// daemon (it reads its own store when active, writes its own store when
// standby).
type StoreReplication struct {
	changesets *store.ChangesetRepo
	schedules  *store.ChangeScheduleRepo
	tokens     *store.APITokenRepo
	snapshots  *store.SnapshotRepo
	blobs      *store.BlobRepo
	audit      *store.AuditRepo
}

// StoreReplicationRepos bundles the repositories StoreReplication reads/writes.
type StoreReplicationRepos struct {
	Changesets *store.ChangesetRepo
	Schedules  *store.ChangeScheduleRepo
	Tokens     *store.APITokenRepo
	Snapshots  *store.SnapshotRepo
	Blobs      *store.BlobRepo
	Audit      *store.AuditRepo
}

// NewStoreReplication constructs a StoreReplication.
func NewStoreReplication(r StoreReplicationRepos) *StoreReplication {
	return &StoreReplication{
		changesets: r.Changesets, schedules: r.Schedules, tokens: r.Tokens,
		snapshots: r.Snapshots, blobs: r.Blobs, audit: r.Audit,
	}
}

// Gather collects this daemon's replicable state: every changeset and api
// token (bounded, upserted whole), every pending schedule, the pre-apply
// snapshots (+ their blobs) of in-flight changesets, and the audit rows since
// sinceAuditID.
func (s *StoreReplication) Gather(ctx context.Context, sinceAuditID int64) (Batch, error) {
	var b Batch

	changesets, err := s.changesets.List(ctx, "")
	if err != nil {
		return Batch{}, fmt.Errorf("ha: gathering changesets: %w", err)
	}
	b.Changesets = changesets

	pending, err := s.schedules.ListByStatus(ctx, store.ScheduleStatusPending)
	if err != nil {
		return Batch{}, fmt.Errorf("ha: gathering pending schedules: %w", err)
	}
	b.Schedules = pending

	tokens, err := s.tokens.List(ctx)
	if err != nil {
		return Batch{}, fmt.Errorf("ha: gathering api tokens: %w", err)
	}
	b.Tokens = tokens

	if err = s.gatherSnapshots(ctx, changesets, &b); err != nil {
		return Batch{}, err
	}

	audit, err := s.audit.ListSince(ctx, sinceAuditID, defaultAuditReplicationLimit)
	if err != nil {
		return Batch{}, fmt.Errorf("ha: gathering audit tail: %w", err)
	}
	b.Audit = audit
	b.AuditSince = sinceAuditID

	return b, nil
}

// gatherSnapshots collects the pre-apply snapshots (and their blobs) of every
// in-flight changeset, deduplicating blobs by hash across snapshots.
func (s *StoreReplication) gatherSnapshots(ctx context.Context, changesets []store.Changeset, b *Batch) error {
	inFlight := map[string]bool{}
	for _, st := range inFlightReplicatedStatuses {
		inFlight[st] = true
	}
	seenBlob := map[string]bool{}
	for _, cs := range changesets {
		if !inFlight[cs.Status] {
			continue
		}
		rows, err := s.snapshots.List(ctx, cs.ID)
		if err != nil {
			return fmt.Errorf("ha: gathering snapshots for changeset %s: %w", cs.ID, err)
		}
		for _, row := range rows {
			files, blobErr := s.snapshotFilesAndBlobs(ctx, row, seenBlob, b)
			if blobErr != nil {
				return blobErr
			}
			b.Snapshots = append(b.Snapshots, snapshotRecord{Row: row, Files: files})
		}
	}
	return nil
}

// snapshotFilesAndBlobs decodes a snapshot row's file references and appends
// each not-yet-seen blob's plaintext to the batch.
func (s *StoreReplication) snapshotFilesAndBlobs(ctx context.Context, row store.Snapshot, seenBlob map[string]bool, b *Batch) ([]store.SnapshotFileRef, error) {
	files, err := decodeSnapshotFileRefs(row)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.SHA256 == "" || seenBlob[f.SHA256] {
			continue
		}
		content, getErr := s.blobs.Get(ctx, f.SHA256)
		if getErr != nil {
			return nil, fmt.Errorf("ha: reading blob %s for snapshot %s: %w", f.SHA256, row.ID, getErr)
		}
		seenBlob[f.SHA256] = true
		b.Blobs = append(b.Blobs, blobRecord{Hash: f.SHA256, Content: content})
	}
	return files, nil
}

// Apply writes a received batch into this daemon's store idempotently, in
// foreign-key order: changesets first (snapshots/schedules reference them),
// then blobs (content-addressed) and the immutable snapshots that reference
// them, then the upserted schedule/token rows, then the append-only audit rows.
func (s *StoreReplication) Apply(ctx context.Context, batch Batch) error {
	for _, cs := range batch.Changesets {
		if err := s.changesets.Upsert(ctx, cs); err != nil {
			return fmt.Errorf("ha: applying replicated changeset %s: %w", cs.ID, err)
		}
	}
	for _, bl := range batch.Blobs {
		if _, err := s.blobs.Put(ctx, bl.Content); err != nil {
			return fmt.Errorf("ha: applying replicated blob %s: %w", bl.Hash, err)
		}
	}
	for _, sr := range batch.Snapshots {
		if err := s.applySnapshot(ctx, sr); err != nil {
			return err
		}
	}
	for _, sch := range batch.Schedules {
		if err := s.schedules.Upsert(ctx, sch); err != nil {
			return fmt.Errorf("ha: applying replicated schedule %s: %w", sch.ChangesetID, err)
		}
	}
	for _, tok := range batch.Tokens {
		if err := s.tokens.Upsert(ctx, tok); err != nil {
			return fmt.Errorf("ha: applying replicated token %s: %w", tok.ID, err)
		}
	}
	for _, e := range batch.Audit {
		if err := s.audit.UpsertReplicated(ctx, e); err != nil {
			return fmt.Errorf("ha: applying replicated audit %d: %w", e.ID, err)
		}
	}
	return nil
}

// applySnapshot inserts a replicated snapshot (and its file refs) if absent.
// Snapshots are immutable, so an already-present id is left untouched.
func (s *StoreReplication) applySnapshot(ctx context.Context, sr snapshotRecord) error {
	if _, err := s.snapshots.Get(ctx, sr.Row.ID); err == nil {
		return nil // already replicated; immutable, nothing to do
	} else if !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("ha: checking replicated snapshot %s: %w", sr.Row.ID, err)
	}
	if err := s.snapshots.Insert(ctx, sr.Row); err != nil {
		return fmt.Errorf("ha: applying replicated snapshot %s: %w", sr.Row.ID, err)
	}
	if err := s.snapshots.InsertFiles(ctx, sr.Files); err != nil {
		return fmt.Errorf("ha: applying replicated snapshot files for %s: %w", sr.Row.ID, err)
	}
	return nil
}

// AuditHighWater returns this daemon's max audit id.
func (s *StoreReplication) AuditHighWater(ctx context.Context) (int64, error) {
	return s.audit.MaxAuditID(ctx)
}

// decodeSnapshotFileRefs extracts the {node,path,sha256} references from a
// snapshot row's files_json — enough to reconstruct snapshot_files on the
// standby (the content itself travels as a blobRecord). Mirrors
// internal/change's own files_json shape (snapshotFile); Content is not carried
// here (it lives in the blob store, keyed by sha256).
func decodeSnapshotFileRefs(row store.Snapshot) ([]store.SnapshotFileRef, error) {
	var wire []struct {
		Node   string `json:"node"`
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal([]byte(row.FilesJSON), &wire); err != nil {
		return nil, fmt.Errorf("ha: decoding files_json for snapshot %s: %w", row.ID, err)
	}
	refs := make([]store.SnapshotFileRef, len(wire))
	for i, f := range wire {
		refs[i] = store.SnapshotFileRef{SnapshotID: row.ID, Node: f.Node, Path: f.Path, SHA256: f.SHA256}
	}
	return refs, nil
}

// storeLeaseStore adapts *store.HALeaseRepo to LeaseStore, mapping
// store.ErrNotFound to ErrNoLease.
type storeLeaseStore struct {
	repo *store.HALeaseRepo
}

// NewStoreLeaseStore wraps a *store.HALeaseRepo as a LeaseStore.
func NewStoreLeaseStore(repo *store.HALeaseRepo) LeaseStore { return &storeLeaseStore{repo: repo} }

func (s *storeLeaseStore) Get(ctx context.Context) (Lease, error) {
	row, err := s.repo.Get(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return Lease{}, ErrNoLease
	}
	if err != nil {
		return Lease{}, err
	}
	return Lease{Holder: row.Holder, Term: row.Term, ExpiresAt: row.ExpiresAt, AcquiredAt: row.AcquiredAt, UpdatedAt: row.UpdatedAt}, nil
}

func (s *storeLeaseStore) Set(ctx context.Context, l Lease) error {
	return s.repo.Set(ctx, store.HALease{Holder: l.Holder, Term: l.Term, ExpiresAt: l.ExpiresAt, AcquiredAt: l.AcquiredAt, UpdatedAt: l.UpdatedAt})
}
