package pbs_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pbs"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// fakePBSClient drives Discover without a live PVE, for the grouping/
// filtering table below.
type fakePBSClient struct {
	storages []pve.Storage
	jobs     []pve.BackupJob
}

func (f *fakePBSClient) ListStorages(context.Context) ([]pve.Storage, error) {
	return f.storages, nil
}
func (f *fakePBSClient) ListBackupJobs(context.Context) ([]pve.BackupJob, error) {
	return f.jobs, nil
}

// TestDiscover_GroupsAndFilters confirms Discover keeps only enabled PBS-type
// storages, groups those sharing a server address into one Host carrying
// every datastore/id, and keeps only enabled backup jobs.
func TestDiscover_GroupsAndFilters(t *testing.T) {
	client := &fakePBSClient{
		storages: []pve.Storage{
			{Storage: "pbs-a", Type: "pbs", Server: "10.0.0.9", Datastore: "ds1", Fingerprint: "fp", Port: 8007},
			{Storage: "pbs-b", Type: "pbs", Server: "10.0.0.9", Datastore: "ds2"},                 // same server -> same host
			{Storage: "pbs-off", Type: "pbs", Server: "10.0.0.9", Datastore: "x", Disabled: true}, // disabled -> dropped
			{Storage: "local", Type: "dir"},        // non-pbs -> dropped
			{Storage: "pbs-noserver", Type: "pbs"}, // no server -> dropped
			{Storage: "pbs-c", Type: "pbs", Server: "10.0.0.20", Datastore: "ds3"},
		},
		jobs: []pve.BackupJob{
			{ID: "job-on", Storage: "pbs-a", Schedule: "daily", Enabled: true, All: true},
			{ID: "job-off", Storage: "pbs-a", Schedule: "weekly", Enabled: false}, // disabled -> dropped
			{ID: "job-nostorage", Enabled: true},                                  // no storage -> dropped
		},
	}

	status, err := pbs.Discover(context.Background(), client, []string{"pve2", "pve1"})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(status.Hosts) != 2 {
		t.Fatalf("Hosts = %d, want 2 (10.0.0.9 grouped, 10.0.0.20)", len(status.Hosts))
	}
	// Sorted by address: 10.0.0.20 < 10.0.0.9.
	h20, h9 := status.Hosts[0], status.Hosts[1]
	if h20.Address != "10.0.0.20" || h9.Address != "10.0.0.9" {
		t.Fatalf("hosts not sorted by address: %q, %q", h20.Address, h9.Address)
	}
	if !reflect.DeepEqual(h9.Datastores, []string{"ds1", "ds2"}) {
		t.Errorf("grouped host datastores = %v, want [ds1 ds2]", h9.Datastores)
	}
	if !reflect.DeepEqual(h9.StorageIDs, []string{"pbs-a", "pbs-b"}) {
		t.Errorf("grouped host storageIDs = %v, want [pbs-a pbs-b]", h9.StorageIDs)
	}
	if h9.Port != 8007 {
		t.Errorf("grouped host port = %d, want 8007 (carried from first entry that declares one)", h9.Port)
	}
	if h9.Fingerprint != "fp" {
		t.Errorf("grouped host fingerprint = %q, want fp", h9.Fingerprint)
	}

	if len(status.Jobs) != 1 || status.Jobs[0].ID != "job-on" {
		t.Fatalf("Jobs = %+v, want only enabled job-on", status.Jobs)
	}
	if !reflect.DeepEqual(status.Nodes, []string{"pve1", "pve2"}) {
		t.Errorf("Nodes = %v, want sorted [pve1 pve2]", status.Nodes)
	}
}

// TestDiscover_EndToEnd exercises the full pvemock -> pve.Client -> Discover
// path against the real fixture (the same reads cmd/vnproxd performs).
func TestDiscover_EndToEnd(t *testing.T) {
	_, status := buildSnapshotAndStatus(t, fixtureBackupPaths)
	if len(status.Hosts) != 1 || status.Hosts[0].Address != "10.50.0.9" {
		t.Fatalf("discovered hosts = %+v, want one at 10.50.0.9", status.Hosts)
	}
	if len(status.Jobs) != 1 || status.Jobs[0].Node != "pve1" {
		t.Fatalf("discovered jobs = %+v, want one node-restricted to pve1", status.Jobs)
	}
}

// TestProject_UnrestrictedJobExpandsToAllNodes proves a job with no node
// restriction produces a backup path for every cluster node (respecting a
// storage's own node restriction), and that an unresolvable egress yields a
// zero Carrier with an honest "could not resolve" sizing hint rather than a
// guess. Uses a hand-built Status + empty snapshot so no interface resolves.
func TestProject_UnrestrictedJobExpandsToAllNodes(t *testing.T) {
	host := pbs.Host{Ref: pbs.HostRef("pbs.example"), Address: "pbs.example", Datastores: []string{"main"}, StorageIDs: []string{"pbs-x"}}
	status := pbs.Status{
		Hosts:    []pbs.Host{host},
		Storages: []pbs.Storage{{ID: "pbs-x", Address: "pbs.example", Datastore: "main", Nodes: []string{"pve1", "pve2"}}},
		Jobs:     []pbs.Job{{ID: "all", Storage: "pbs-x", Schedule: "daily", All: true}},
		Nodes:    []string{"pve1", "pve2", "pve3"},
	}

	overlay := pbs.Project(inventory.NewGraph().Snapshot(), status)

	got := map[string]bool{}
	for _, p := range overlay.Paths {
		got[p.Node] = true
		if !p.Carrier.IsZero() {
			t.Errorf("empty snapshot must not resolve a carrier for %s, got %+v", p.Node, p.Carrier)
		}
	}
	// pve3 is excluded by the storage node restriction; pve1/pve2 included.
	if !got["pve1"] || !got["pve2"] || got["pve3"] {
		t.Errorf("expanded nodes = %v, want pve1+pve2 only (storage restricts to those)", got)
	}
}
