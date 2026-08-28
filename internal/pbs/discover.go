// SPDX-License-Identifier: Apache-2.0

package pbs

import (
	"context"
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// pbsClient is the read-only subset of *pve.Client Discover needs — declared
// as an interface so tests can drive Discover with a fake and so this
// package's dependency on internal/pve stays a small, explicit seam. Both
// methods are plain GETs (see internal/pve/storage.go); there is deliberately
// no write method here to reach for.
type pbsClient interface {
	ListStorages(ctx context.Context) ([]pve.Storage, error)
	ListBackupJobs(ctx context.Context) ([]pve.BackupJob, error)
}

// Discover reads PVE's own storage config (GET /storage) and backup jobs
// (GET /cluster/backup) through the shared read-only *pve.Client, keeping
// only storage.cfg entries of type "pbs" (enabled ones) and enabled backup
// jobs. PBS storages sharing a server address collapse into one Host
// carrying every datastore/storage-id seen for that address. nodes is the
// caller-supplied cluster node list (cmd/vnproxd passes the same
// ClusterStatus-derived list every other read-only discovery uses) — needed
// to expand a backup job with no node restriction to "every node". A cluster
// with no PBS storage configured contributes zero Hosts, never an error.
func Discover(ctx context.Context, client pbsClient, nodes []string) (Status, error) {
	if client == nil {
		return Status{}, fmt.Errorf("pbs: discover: nil pve client")
	}

	storages, err := client.ListStorages(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("pbs: discovering storage config: %w", err)
	}
	jobs, err := client.ListBackupJobs(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("pbs: discovering backup jobs: %w", err)
	}

	pbsStorages := make([]Storage, 0)
	// hostByAddr aggregates storage entries sharing a server address into one
	// Host; addrOrder preserves first-seen order before the final sort.
	hostByAddr := map[string]*Host{}
	var addrOrder []string
	for _, s := range storages {
		if s.Type != "pbs" || s.Disabled || s.Server == "" {
			continue
		}
		pbsStorages = append(pbsStorages, Storage{
			ID:          s.Storage,
			Address:     s.Server,
			Datastore:   s.Datastore,
			Fingerprint: s.Fingerprint,
			Nodes:       append([]string(nil), s.Nodes...),
			Port:        s.Port,
		})
		h, ok := hostByAddr[s.Server]
		if !ok {
			h = &Host{Ref: HostRef(s.Server), Address: s.Server, Port: s.Port}
			hostByAddr[s.Server] = h
			addrOrder = append(addrOrder, s.Server)
		}
		if h.Port == 0 && s.Port != 0 {
			h.Port = s.Port
		}
		if h.Fingerprint == "" && s.Fingerprint != "" {
			h.Fingerprint = s.Fingerprint
		}
		if s.Datastore != "" {
			h.Datastores = appendUnique(h.Datastores, s.Datastore)
		}
		h.StorageIDs = appendUnique(h.StorageIDs, s.Storage)
	}

	hosts := make([]Host, 0, len(addrOrder))
	for _, addr := range addrOrder {
		h := hostByAddr[addr]
		sort.Strings(h.Datastores)
		sort.Strings(h.StorageIDs)
		hosts = append(hosts, *h)
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].Address < hosts[j].Address })

	var enabledJobs []Job
	for _, j := range jobs {
		if !j.Enabled || j.Storage == "" {
			continue
		}
		enabledJobs = append(enabledJobs, Job{
			ID:       j.ID,
			Storage:  j.Storage,
			Node:     j.Node,
			Schedule: j.Schedule,
			VMIDs:    append([]string(nil), j.VMIDs...),
			All:      j.All,
		})
	}
	sort.Slice(enabledJobs, func(i, j int) bool { return enabledJobs[i].ID < enabledJobs[j].ID })

	nodesCopy := append([]string(nil), nodes...)
	sort.Strings(nodesCopy)

	return Status{Hosts: hosts, Storages: pbsStorages, Jobs: enabledJobs, Nodes: nodesCopy}, nil
}

// appendUnique appends v to s only if not already present.
func appendUnique(s []string, v string) []string {
	for _, e := range s {
		if e == v {
			return s
		}
	}
	return append(s, v)
}
