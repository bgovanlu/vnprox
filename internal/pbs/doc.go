// Package pbs implements T-1206's Proxmox Backup Server network awareness:
// read-only discovery of the PBS hosts PVE already knows about (its own
// storage.cfg entries of type "pbs") and the backup jobs targeting them,
// projected onto the topology map as pbs-host nodes and node->PBS
// backup-path edges plus a plain-English datastore-network sizing hint.
//
// Read-only forever, by construction. This package has no write surface of
// any kind: no changeset op, no PVE write call, no PBS API client, no stored
// PBS credential. Discover issues only GETs through the shared, read-only
// *pve.Client (GET /storage, GET /cluster/backup) — it reads PVE's own
// knowledge of its PBS storages and backup schedule, never PBS itself.
// Project is a pure function of an inventory snapshot plus that discovered
// Status: it resolves each backing-up node's egress interface toward its PBS
// server and reuses internal/topology.ResolvePhysicalPath (T-702's NIC-path
// resolver) to report the backup path's bottleneck link speed, guessing
// nothing it cannot resolve. See zerowrite_test.go for the enforced
// zero-write invariant.
package pbs
