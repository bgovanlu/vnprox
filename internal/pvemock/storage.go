// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"net/http"
	"strings"
)

// storage.go implements T-1206's two read-only routes backing PBS network
// awareness: GET /storage (cluster-wide storage.cfg entries) and GET
// /cluster/backup (cluster-wide vzdump backup jobs). Both serve directly off
// the loaded Fixture — storage/backup config never changes at runtime in
// this mock (no handler anywhere mutates it, matching the read-only
// invariant internal/pbs itself enforces: PVE owns storage.cfg, this mock
// included) — so, like ceph placement, there is no mutable nodeState field
// to thread through NewState: the handlers below read srv.state.fixture
// directly. Comma-list fields (content, nodes, vmid) are emitted in PVE's
// comma-separated-string convention; boolean fields (disable, enabled, all)
// in PVE's numeric 0/1 convention — the exact wire forms internal/pve's
// commaList/pveBool decoders expect.

type storageRowWire struct {
	Storage     string `json:"storage"`
	Type        string `json:"type"`
	Server      string `json:"server,omitempty"`
	Datastore   string `json:"datastore,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Content     string `json:"content,omitempty"`
	Nodes       string `json:"nodes,omitempty"`
	Port        int    `json:"port,omitempty"`
	Disable     int    `json:"disable,omitempty"`
}

// handleStorageList serves GET /storage from the fixture's cluster-wide
// storage list. A fixture with no storage: block serves an empty array.
func (srv *Server) handleStorageList(w http.ResponseWriter, _ *http.Request) {
	out := make([]storageRowWire, 0, len(srv.state.fixture.Storage))
	for _, s := range srv.state.fixture.Storage {
		out = append(out, storageRowWire{
			Storage:     s.Storage,
			Type:        s.Type,
			Server:      s.Server,
			Datastore:   s.Datastore,
			Fingerprint: s.Fingerprint,
			Content:     strings.Join(s.Content, ","),
			Nodes:       strings.Join(s.Nodes, ","),
			Port:        s.Port,
			Disable:     boolToInt(s.Disable),
		})
	}
	writeData(w, http.StatusOK, out)
}

type backupJobRowWire struct {
	ID       string `json:"id"`
	Storage  string `json:"storage,omitempty"`
	Node     string `json:"node,omitempty"`
	Schedule string `json:"schedule,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Comment  string `json:"comment,omitempty"`
	VMID     string `json:"vmid,omitempty"`
	Enabled  int    `json:"enabled"`
	All      int    `json:"all,omitempty"`
}

// handleBackupJobs serves GET /cluster/backup from the fixture's cluster-wide
// backup-job list. A fixture with no backup_jobs: block serves an empty array.
func (srv *Server) handleBackupJobs(w http.ResponseWriter, _ *http.Request) {
	out := make([]backupJobRowWire, 0, len(srv.state.fixture.BackupJobs))
	for _, j := range srv.state.fixture.BackupJobs {
		out = append(out, backupJobRowWire{
			ID:       j.ID,
			Storage:  j.Storage,
			Node:     j.Node,
			Schedule: j.Schedule,
			Mode:     j.Mode,
			Comment:  j.Comment,
			VMID:     strings.Join(j.VMIDs, ","),
			Enabled:  boolToInt(j.Enabled),
			All:      boolToInt(j.All),
		})
	}
	writeData(w, http.StatusOK, out)
}
