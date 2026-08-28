// SPDX-License-Identifier: Apache-2.0

package pve

import (
	"context"
	"fmt"
	"strings"
)

// storage.go: read-only access to PVE's own knowledge of its storage and
// backup-job configuration (T-1206 PBS network awareness, docs/features/
// topology.md §1/§2). Both methods are plain GETs against PVE's existing
// cluster API surface (the same *Client every other read-only method here
// already uses) — vnprox never gets a Proxmox-Backup-Server API client of
// its own and never stores a PBS credential. A PBS storage's presence,
// server address, datastore, port, and TLS fingerprint are all things PVE
// already knows and reports about its own /etc/pve/storage.cfg; the backup
// jobs that target it are likewise PVE's own /etc/pve/vzdump.cron /
// /etc/pve/jobs.cfg. This is discovery of PVE's own knowledge of itself,
// not a PBS integration.
//
// Real PVE's exact GET /storage / GET /cluster/backup response shapes are
// not independently verified against real hardware in this environment
// (docs/development.md: "you do not have a live Proxmox cluster, develop
// against internal/pvemock fixtures") — the wire shapes below are a
// best-effort modeling of PVE's documented API surface, exercised
// end-to-end against internal/pvemock's fixture-driven implementation of the
// same two routes. Flagged in planning/reports/needs-hardware-validation.md.

// storageWire mirrors one GET /storage row. Only the fields T-1206's PBS
// awareness needs are decoded; PVE returns many more (path, pool, export,
// prune-backups, ...) this read-only feature has no use for. content/nodes
// arrive as PVE's comma-separated-string convention (see commaList), disable
// as its numeric-boolean convention (see pveBool).
type storageWire struct {
	Storage     string    `json:"storage"`
	Type        string    `json:"type"`
	Server      string    `json:"server,omitempty"`
	Datastore   string    `json:"datastore,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Content     commaList `json:"content,omitempty"`
	Nodes       commaList `json:"nodes,omitempty"`
	Port        int       `json:"port,omitempty"`
	Disable     pveBool   `json:"disable,omitempty"`
}

// Storage is one storage.cfg entry PVE reports (GET /storage), converted
// from storageWire's PVE wire conventions to ergonomic Go types. Only Type
// == "pbs" entries carry meaningful Server/Datastore/Fingerprint; every
// other storage type populates just Storage/Type/Content/Nodes (this
// feature ignores non-PBS storages entirely — see internal/pbs.Discover).
// Nodes is the storage's node restriction (empty means "available on all
// nodes", real PVE's own semantics).
type Storage struct {
	Storage     string
	Type        string
	Server      string
	Datastore   string
	Fingerprint string
	Content     []string
	Nodes       []string
	Port        int
	Disabled    bool
}

// ListStorages calls GET /storage: every configured storage.cfg entry,
// cluster-wide (storage.cfg is shared cluster config). Returns an empty
// slice, not an error, for a cluster with no storages configured at all —
// the same "absence is not failure" convention every other read here
// follows.
func (c *Client) ListStorages(ctx context.Context) ([]Storage, error) {
	var wire []storageWire
	if err := c.do(ctx, "GET", "/storage", requestParams{}, &wire); err != nil {
		return nil, fmt.Errorf("pve: reading storage config: %w", err)
	}
	out := make([]Storage, len(wire))
	for i, w := range wire {
		out[i] = Storage{
			Storage:     w.Storage,
			Type:        w.Type,
			Server:      strings.TrimSpace(w.Server),
			Datastore:   strings.TrimSpace(w.Datastore),
			Fingerprint: strings.TrimSpace(w.Fingerprint),
			Content:     []string(w.Content),
			Nodes:       []string(w.Nodes),
			Port:        w.Port,
			Disabled:    bool(w.Disable),
		}
	}
	return out, nil
}

// backupJobWire mirrors one GET /cluster/backup row (a vzdump backup job).
// vmid arrives as PVE's comma-separated-string convention; enabled/all as
// its numeric-boolean convention. Only the fields relevant to "which nodes
// back up to which storage, on what schedule" are decoded.
type backupJobWire struct {
	ID       string    `json:"id"`
	Storage  string    `json:"storage,omitempty"`
	Node     string    `json:"node,omitempty"`
	Schedule string    `json:"schedule,omitempty"`
	Mode     string    `json:"mode,omitempty"`
	Comment  string    `json:"comment,omitempty"`
	Pool     string    `json:"pool,omitempty"`
	VMID     commaList `json:"vmid,omitempty"`
	Enabled  pveBool   `json:"enabled,omitempty"`
	All      pveBool   `json:"all,omitempty"`
}

// BackupJob is one vzdump backup job PVE reports (GET /cluster/backup),
// converted from backupJobWire. Node is the job's node restriction (empty
// means "runs for guests on every node"). All true means "back up every
// guest" (VMIDs then empty); otherwise VMIDs is the explicit selection.
// Enabled false means the job is administratively disabled and is skipped.
//
// Note on Enabled: real PVE defaults a job to enabled and always returns the
// field, so a decoded zero-value false genuinely means "disabled" rather
// than "unspecified". internal/pbs treats only enabled jobs as producing a
// backup path.
type BackupJob struct {
	ID       string
	Storage  string
	Node     string
	Schedule string
	Mode     string
	Comment  string
	Pool     string
	VMIDs    []string
	Enabled  bool
	All      bool
}

// ListBackupJobs calls GET /cluster/backup: every configured vzdump backup
// job, cluster-wide. Returns an empty slice, not an error, for a cluster
// with no backup jobs configured.
func (c *Client) ListBackupJobs(ctx context.Context) ([]BackupJob, error) {
	var wire []backupJobWire
	if err := c.do(ctx, "GET", "/cluster/backup", requestParams{}, &wire); err != nil {
		return nil, fmt.Errorf("pve: reading backup jobs: %w", err)
	}
	out := make([]BackupJob, len(wire))
	for i, w := range wire {
		out[i] = BackupJob{
			ID:       w.ID,
			Storage:  strings.TrimSpace(w.Storage),
			Node:     strings.TrimSpace(w.Node),
			Schedule: strings.TrimSpace(w.Schedule),
			Mode:     w.Mode,
			Comment:  w.Comment,
			Pool:     strings.TrimSpace(w.Pool),
			VMIDs:    []string(w.VMID),
			Enabled:  bool(w.Enabled),
			All:      bool(w.All),
		}
	}
	return out, nil
}
