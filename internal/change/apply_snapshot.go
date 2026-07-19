package change

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/store"
)

// interfacesPath is the one node file the T-205 apply engine snapshots and
// restores. Firewall config files are still another op family's concern
// (out of T-402's scope too); T-206 generalizes snapshot storage.
const interfacesPath = "/etc/network/interfaces"

// SDN config snapshot paths (T-402): synthetic, cluster-scoped (Node="")
// snapshotFile entries standing in for real PVE's /etc/pve/sdn/*.cfg —
// vnprox has no raw-text read of those files the way it does for
// /etc/network/interfaces (the PVE API exposes SDN config only as typed
// zone/vnet/subnet objects, never the on-disk cfg text; see PVEGateway's
// SDNConfig doc comment), so each path's Content is a JSON encoding of that
// entity family's SDNConfig slice rather than PVE's native cfg syntax. This
// is the smallest reasonable stand-in that still satisfies "pre-snapshot of
// /etc/pve/sdn/*.cfg" (T-402's card) well enough to restore from — flagged
// in the T-402 report as a documented extension.
const (
	sdnZonesSnapshotPath   = "/etc/pve/sdn/zones.cfg"
	sdnVnetsSnapshotPath   = "/etc/pve/sdn/vnets.cfg"
	sdnSubnetsSnapshotPath = "/etc/pve/sdn/subnets.cfg"
)

// wgStateSnapshotPath is the synthetic, cluster-scoped (Node="") snapshotFile
// path holding T-1401's WireGuard pre-apply state: a JSON object mapping each
// affected node to the opaque WGGateway.SnapshotWg string for that node. It is
// cluster-scoped (Node="") deliberately — a per-node interfaces file already
// occupies that node's snapshotFile slot, and restoreAll keys those by node,
// so a second per-node file would collide; the SDN snapshot files use the
// same cluster-scoped stand-in for the same reason. The private keys inside
// stay sealed (SnapshotWg never emits plaintext), so this snapshot blob is no
// more sensitive than the sealed store rows it mirrors.
const wgStateSnapshotPath = "/var/lib/vnprox/wireguard.state"

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

// captureSnapshotFull is captureSnapshot plus, when plan carries any SDN
// step, the SDN config snapshot (sdnConfigSnapshotFiles) in the *same*
// persisted snapshot row — loadPreSnapshot only ever reads the first "pre"
// row it finds for a changeset, so the node-file and SDN halves of one
// apply's pre-state must live in one row, not two. pveGW is required (and
// its absence is an error) iff plan.hasSDN(); a plan with no SDN step never
// touches it, so a nil pveGW is fine for the (still-common) node-file-only
// case.
func (s *Service) captureSnapshotFull(ctx context.Context, changesetID, kind string, plan Plan, pveGW PVEGateway) ([]snapshotFile, error) {
	files := make([]snapshotFile, 0, len(plan.affectedNodes())+3)
	for _, node := range plan.affectedNodes() {
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

	if plan.hasSDN() {
		if pveGW == nil {
			return nil, fmt.Errorf("change: snapshotting %s changeset %s: no PVE gateway available (no user session)", kind, changesetID)
		}
		cfg, err := pveGW.SDNConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("change: snapshotting SDN config for changeset %s: %w", changesetID, err)
		}
		sdnFiles, err := sdnConfigSnapshotFiles(ctx, s, cfg)
		if err != nil {
			return nil, err
		}
		files = append(files, sdnFiles...)
	}

	if plan.hasWg() && s.wg != nil {
		wgFile, err := s.wgStateSnapshotFile(ctx, plan.wgNodes())
		if err != nil {
			return nil, err
		}
		files = append(files, wgFile)
	}

	if _, err := s.persistSnapshot(ctx, changesetID, kind, "", files); err != nil {
		return nil, err
	}
	return files, nil
}

// sdnConfigSnapshotFiles encodes cfg's three entity families as the
// synthetic cluster-scoped (Node="") snapshotFile entries described by the
// sdn*SnapshotPath constants above, storing each in the blob store exactly
// like captureSnapshot does for a node's interfaces file.
func sdnConfigSnapshotFiles(ctx context.Context, s *Service, cfg SDNConfig) ([]snapshotFile, error) {
	entries := []struct {
		v    any
		path string
	}{
		{v: cfg.Zones, path: sdnZonesSnapshotPath},
		{v: cfg.Vnets, path: sdnVnetsSnapshotPath},
		{v: cfg.Subnets, path: sdnSubnetsSnapshotPath},
	}
	out := make([]snapshotFile, 0, len(entries))
	for _, e := range entries {
		b, err := json.Marshal(e.v)
		if err != nil {
			return nil, fmt.Errorf("change: encoding sdn snapshot %s: %w", e.path, err)
		}
		content := string(b)
		hash, err := s.blobs.Put(ctx, content)
		if err != nil {
			return nil, fmt.Errorf("change: storing sdn snapshot blob %s: %w", e.path, err)
		}
		out = append(out, snapshotFile{Path: e.path, SHA256: hash, Content: content})
	}
	return out, nil
}

// wgStateSnapshotFile captures each wg node's WireGuard state (WGGateway.
// SnapshotWg) into one cluster-scoped snapshotFile (wgStateSnapshotPath),
// stored in the blob store like every other snapshot file.
func (s *Service) wgStateSnapshotFile(ctx context.Context, nodes []string) (snapshotFile, error) {
	state := make(map[string]string, len(nodes))
	for _, node := range nodes {
		snap, err := s.wg.SnapshotWg(ctx, node)
		if err != nil {
			return snapshotFile{}, fmt.Errorf("change: snapshotting WireGuard state on node %s: %w", node, err)
		}
		state[node] = snap
	}
	b, err := json.Marshal(state)
	if err != nil {
		return snapshotFile{}, fmt.Errorf("change: encoding WireGuard state snapshot: %w", err)
	}
	content := string(b)
	hash, err := s.blobs.Put(ctx, content)
	if err != nil {
		return snapshotFile{}, fmt.Errorf("change: storing WireGuard state snapshot blob: %w", err)
	}
	return snapshotFile{Path: wgStateSnapshotPath, SHA256: hash, Content: content}, nil
}

// wgStateFromSnapshot decodes the per-node WireGuard state map out of a loaded
// pre-snapshot's file list. ok is false if the snapshot carries no WireGuard
// state file (a changeset with no wg.* ops).
func wgStateFromSnapshot(files []snapshotFile) (state map[string]string, ok bool) {
	for _, f := range files {
		if f.Path == wgStateSnapshotPath {
			if json.Unmarshal([]byte(f.Content), &state) == nil {
				return state, true
			}
		}
	}
	return nil, false
}

// sdnConfigFromSnapshot decodes an SDNConfig back out of a loaded pre-
// snapshot's file list (loadPreSnapshot/loadSnapshotFiles already hydrated
// Content). ok is false if the snapshot carries no SDN files at all (a
// node-file-only changeset's pre-snapshot).
func sdnConfigFromSnapshot(files []snapshotFile) (cfg SDNConfig, ok bool) {
	for _, f := range files {
		switch f.Path {
		case sdnZonesSnapshotPath:
			if json.Unmarshal([]byte(f.Content), &cfg.Zones) == nil {
				ok = true
			}
		case sdnVnetsSnapshotPath:
			if json.Unmarshal([]byte(f.Content), &cfg.Vnets) == nil {
				ok = true
			}
		case sdnSubnetsSnapshotPath:
			if json.Unmarshal([]byte(f.Content), &cfg.Subnets) == nil {
				ok = true
			}
		}
	}
	return cfg, ok
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
