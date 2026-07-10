package change

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/store"
)

// interfacesPath is the one node file the T-205 apply engine snapshots and
// restores. SDN/firewall config files are other op families' concern (and
// out of T-205's executable scope); T-206 generalizes snapshot storage.
const interfacesPath = "/etc/network/interfaces"

// Snapshot kinds (store.Snapshot.Kind, docs/data-model.md §2: pre|post|
// manual|scheduled).
const (
	snapshotKindPre       = "pre"
	snapshotKindPost      = "post"
	snapshotKindManual    = "manual"
	snapshotKindScheduled = "scheduled"
)

// snapshotFile is one file's captured content within a snapshot's files_json.
//
// files_json persists only {node,path,sha256} (Content is `json:"-"`); the
// actual bytes live in the content-addressed, zstd-compressed `blobs` table
// (internal/store.BlobRepo), keyed by sha256, so identical file content
// across snapshots is stored once — T-206 completes the move away from
// T-205's interim inline-plaintext shape (see planning/reports/T-205.md §3
// note 5). Content is populated on read by loadSnapshotFiles/hydrateFiles,
// never by unmarshaling files_json directly.
type snapshotFile struct {
	Node    string `json:"node"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Content string `json:"-"`
}

// captureSnapshot reads each node's current interfaces file via the agent,
// stores each file's content once in the blob store (deduplicated by
// sha256), and persists the snapshot row (files_json) plus its
// snapshot_files blob references, linked to changesetID. It returns the
// captured files (with Content populated) for immediate in-memory use
// (restore) so the caller need not re-read them from the DB. It is called
// before any mutation (kind "pre") and after a successful commit (kind
// "post").
func (s *Service) captureSnapshot(ctx context.Context, changesetID, kind string, nodes []string) ([]snapshotFile, error) {
	files := make([]snapshotFile, 0, len(nodes))
	for _, node := range nodes {
		content, err := s.nodes.ReadInterfaces(ctx, node)
		if err != nil {
			return nil, fmt.Errorf("change: snapshotting %s on node %s: %w", interfacesPath, node, err)
		}
		hash, err := s.blobs.Put(ctx, content)
		if err != nil {
			return nil, fmt.Errorf("change: storing snapshot blob for %s on node %s: %w", interfacesPath, node, err)
		}
		files = append(files, snapshotFile{Node: node, Path: interfacesPath, SHA256: hash, Content: content})
	}

	if _, err := s.persistSnapshot(ctx, changesetID, kind, "", files); err != nil {
		return nil, err
	}
	return files, nil
}

// persistSnapshot writes the snapshot row (files_json, no inline content)
// and its snapshot_files blob references, returning the new snapshot's id.
// The blobs themselves must already have been stored (captureSnapshot calls
// s.blobs.Put itself; CreateManualSnapshot does the same before calling
// this).
func (s *Service) persistSnapshot(ctx context.Context, changesetID, kind, note string, files []snapshotFile) (string, error) {
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return "", fmt.Errorf("change: marshaling snapshot files: %w", err)
	}
	id := store.NewULID()
	row := store.Snapshot{
		ID:          id,
		ChangesetID: nullString(changesetID),
		TakenAt:     s.now().Unix(),
		Kind:        kind,
		FilesJSON:   string(filesJSON),
		Note:        nullString(note),
	}
	if err := s.snapshots.Insert(ctx, row); err != nil {
		return "", fmt.Errorf("change: persisting %s snapshot: %w", kind, err)
	}
	refs := make([]store.SnapshotFileRef, len(files))
	for i, f := range files {
		refs[i] = store.SnapshotFileRef{SnapshotID: id, Node: f.Node, Path: f.Path, SHA256: f.SHA256}
	}
	if err := s.snapshots.InsertFiles(ctx, refs); err != nil {
		return "", fmt.Errorf("change: persisting snapshot_files for %s snapshot %s: %w", kind, id, err)
	}
	return id, nil
}

// decodeSnapshotFiles decodes a snapshot row's files_json into
// {node,path,sha256} entries (Content left empty — see hydrateFiles). A
// pre-0002 row that still carries T-205's interim inline `content` field
// decodes with Content populated, so hydrateFiles can skip the blob fetch
// for it (those rows predate the blobs table and have no blob to fetch).
func decodeSnapshotFiles(row store.Snapshot) ([]snapshotFile, error) {
	var wire []struct {
		Node    string `json:"node"`
		Path    string `json:"path"`
		SHA256  string `json:"sha256"`
		Content string `json:"content"` // legacy T-205 inline shape only
	}
	if err := json.Unmarshal([]byte(row.FilesJSON), &wire); err != nil {
		return nil, fmt.Errorf("change: decoding files for snapshot %s: %w", row.ID, err)
	}
	files := make([]snapshotFile, len(wire))
	for i, f := range wire {
		files[i] = snapshotFile{Node: f.Node, Path: f.Path, SHA256: f.SHA256, Content: f.Content}
	}
	return files, nil
}

// hydrateFiles fetches each file's content from the blob store, populating
// Content in place. Files whose Content is already set (legacy inline rows,
// see decodeSnapshotFiles) are left as-is.
func (s *Service) hydrateFiles(ctx context.Context, files []snapshotFile) error {
	for i := range files {
		if files[i].Content != "" {
			continue
		}
		content, err := s.blobs.Get(ctx, files[i].SHA256)
		if err != nil {
			return fmt.Errorf("change: reading blob %s for %s on node %s: %w", files[i].SHA256, files[i].Path, files[i].Node, err)
		}
		files[i].Content = content
	}
	return nil
}

// loadSnapshotFiles decodes and hydrates the full file list (with content)
// for one snapshot id.
func (s *Service) loadSnapshotFiles(ctx context.Context, snapshotID string) ([]snapshotFile, error) {
	row, err := s.snapshots.Get(ctx, snapshotID)
	if err != nil {
		return nil, fmt.Errorf("change: loading snapshot %s: %w", snapshotID, err)
	}
	files, err := decodeSnapshotFiles(row)
	if err != nil {
		return nil, err
	}
	if err := s.hydrateFiles(ctx, files); err != nil {
		return nil, err
	}
	return files, nil
}

// loadPreSnapshot fetches and decodes the "pre" snapshot for changesetID —
// the byte-exact file state to restore on rollback (including the daemon-
// restart auto-rollback path, which has only the DB to work from).
func (s *Service) loadPreSnapshot(ctx context.Context, changesetID string) ([]snapshotFile, error) {
	rows, err := s.snapshots.List(ctx, changesetID)
	if err != nil {
		return nil, fmt.Errorf("change: loading snapshots for changeset %s: %w", changesetID, err)
	}
	for _, row := range rows {
		if row.Kind != snapshotKindPre {
			continue
		}
		files, err := decodeSnapshotFiles(row)
		if err != nil {
			return nil, err
		}
		if err := s.hydrateFiles(ctx, files); err != nil {
			return nil, err
		}
		return files, nil
	}
	return nil, fmt.Errorf("change: no pre-snapshot found for changeset %s", changesetID)
}
