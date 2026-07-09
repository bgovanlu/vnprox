package change

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	snapshotKindPre  = "pre"
	snapshotKindPost = "post"
)

// snapshotFile is one file's captured content within a snapshot's files_json.
//
// The data-model comment names this column's shape [{node,path,sha256,
// content_zstd}]; T-205 stores the plaintext content under `content` (plus
// its sha256 for integrity/dedup keying) so the pre-state can be restored
// byte-identically today. T-206 owns the zstd-compressed, hash-deduplicated
// blob storage that replaces the inline content — see the T-205 report.
type snapshotFile struct {
	Node    string `json:"node"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Content string `json:"content"`
}

// captureSnapshot reads each node's current interfaces file via the agent and
// persists them as one immutable snapshot row of the given kind linked to
// changesetID, returning the captured files for immediate in-memory use
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
		sum := sha256.Sum256([]byte(content))
		files = append(files, snapshotFile{
			Node:    node,
			Path:    interfacesPath,
			SHA256:  hex.EncodeToString(sum[:]),
			Content: content,
		})
	}

	filesJSON, err := json.Marshal(files)
	if err != nil {
		return nil, fmt.Errorf("change: marshaling snapshot files for changeset %s: %w", changesetID, err)
	}
	row := store.Snapshot{
		ID:          store.NewULID(),
		ChangesetID: nullString(changesetID),
		TakenAt:     s.now().Unix(),
		Kind:        kind,
		FilesJSON:   string(filesJSON),
	}
	if err := s.snapshots.Insert(ctx, row); err != nil {
		return nil, fmt.Errorf("change: persisting %s snapshot for changeset %s: %w", kind, changesetID, err)
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
		var files []snapshotFile
		if err := json.Unmarshal([]byte(row.FilesJSON), &files); err != nil {
			return nil, fmt.Errorf("change: decoding pre-snapshot for changeset %s: %w", changesetID, err)
		}
		return files, nil
	}
	return nil, fmt.Errorf("change: no pre-snapshot found for changeset %s", changesetID)
}
