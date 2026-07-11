package change

import (
	"context"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// NodeAgent performs node-local /etc/network/interfaces(5) file operations
// and the interfaces reload for one node. It is the "host writer" seam the
// apply engine drives for the per-node stage-file and reload steps
// (docs/architecture.md §4, docs/data-model.md §3's "(2) per-node interface
// file staging, (3) per-node ifreload").
//
// It is deliberately a daemon-level (root) dependency, injected once at
// Service construction rather than per request, because the commit-confirm
// auto-rollback timer fires with no live user session yet must still restore
// files and reload the node (docs/features/change-management.md §4: "the
// rollback timer runs on the node's daemon"). vnproxd runs as root and the
// peer API (docs/api.md's `/api/peer/host/stage-interfaces`,
// `/api/peer/host/ifreload`, `/api/peer/host/restore`) is designed for
// exactly this host-level path. Cluster-scope PVE writes (SDN, guests) still
// flow through the user's own ticket — see PVEGateway.
//
// Every method takes a node name because vnprox is cluster-aware: the
// intended production shape (docs/architecture.md §1, §5) is that a
// coordinating daemon stages/reloads a peer node's file via the peer API,
// with a single NodeAgent value abstracting local and peer nodes so the
// planner/executor never branch on locality. T-304's ClusterNodeAgent
// (cluster_agent.go) is that single value in production: it routes each
// call to cmd/vnproxd's local hostNodeAgent when node is this daemon's own
// PVE node, or through a *peer.Client otherwise. hostNodeAgent itself still
// only operates on local paths — do not hand it a peer-node step directly.
type NodeAgent interface {
	// ReadInterfaces returns node's current, committed /etc/network/interfaces
	// content (never the staged interfaces.new). It is the byte-exact source
	// both the pre-apply snapshot and the diff are taken from.
	ReadInterfaces(ctx context.Context, node string) (string, error)

	// StageInterfaces writes content to node's staged interfaces.new without
	// touching the live file, mirroring ifupdown2's staged-apply model. It
	// must not activate anything; only ReloadInterfaces does.
	StageInterfaces(ctx context.Context, node, content string) error

	// ReloadInterfaces atomically applies node's staged interfaces.new (moving
	// it over the live file) and reloads the network (ifreload -a equivalent).
	// On any failure it must leave the committed file and running state exactly
	// as they were before the call (never half-applied) and return a non-nil
	// error, so the executor can treat the step as cleanly failed and roll
	// back the rest.
	ReloadInterfaces(ctx context.Context, node string) error

	// DiscardStaged drops node's staged interfaces.new, if any, leaving the
	// committed file untouched (ifupdown2's "revert"). Used to clean up a
	// staged-but-not-yet-reloaded file when an earlier step in the same node's
	// stage→reload pair is being rolled back.
	DiscardStaged(ctx context.Context, node string) error
}

// PVEGateway performs cluster-scope PVE API mutations under the *user's own*
// ticket (docs/architecture.md §6, D3: "PVE ACLs enforced by PVE; no
// privilege escalation through vnprox"). It is passed per Apply/Rollback
// call, constructed by the API layer from the requesting session's live
// *pve.Client (auth.Service.PVEClientFor), so PVE authorizes every write as
// the logged-in operator.
//
// T-205's executable cluster-scope step is sdn.apply; T-405 adds the ipam
// family (AllocateIPAMAddress/ReleaseIPAMAddress) below. The guest/SDN-write/
// fw op families still need their own pve.Client write+task methods (a
// T-101/follow-up surface) before the planner will emit steps for them —
// see plan.go's supportedOpTypes and the T-205 report's residual-risk list.
type PVEGateway interface {
	// ApplySDN applies all pending cluster SDN config (PUT /cluster/sdn) and
	// blocks until the resulting task reaches a terminal state, returning a
	// non-nil error if the task fails or times out.
	ApplySDN(ctx context.Context) error

	// AllocateIPAMAddress reserves an address inside vnet's IPAM (T-405's
	// ipam.alloc.create op — docs/features/ipam.md §3). Real PVE resolves
	// which configured IPAM plugin instance backs vnet server-side, so this
	// method (like ReleaseIPAMAddress) is vnet-scoped, not
	// ipam-instance-scoped. subnetCIDR is the op's target subnet (the
	// allocation's owning subnet, docs/data-model.md's IpAllocation ->
	// SdnSubnet relation) — passed through so the created IPAM entry
	// records which subnet it belongs to (internal/ipam's per-subnet
	// bucketing keys off exactly this field).
	AllocateIPAMAddress(ctx context.Context, vnet, subnetCIDR string, alloc IpamAllocCreateParams) error

	// ReleaseIPAMAddress releases cidr from vnet's IPAM (T-405's
	// ipam.alloc.delete op). subnetCIDR: see AllocateIPAMAddress.
	ReleaseIPAMAddress(ctx context.Context, vnet, subnetCIDR, cidr string) error
}

// NodeTimerAgent is Service's seam onto T-304's local-timer protocol
// (docs/features/change-management.md §4): arm, cancel, or inspect one
// node's distributed commit-confirm rollback timer for a changeset, routed
// to whichever daemon is actually responsible for that node. It is
// NodeAgent's counterpart for the "single value abstracting local and peer
// nodes" pattern — ClusterTimerAgent (cluster_agent.go) is the production
// implementation, calling straight into a local *LocalTimerAgent for this
// daemon's own node or through *peer.Client for a peer's.
//
// A nil NodeTimerAgent on Config disables the whole per-node protocol: Apply
// falls back to T-205's original single coordinator-side timer (correct and
// sufficient for a single-node deployment, and for any test that doesn't
// need multi-node distributed-rollback coverage).
type NodeTimerAgent interface {
	// ArmTimer arms node's local rollback timer for changesetID: content is
	// the byte-exact pre-apply state to restore if the timer fires
	// uncancelled by deadline (unix seconds).
	ArmTimer(ctx context.Context, changesetID, node, content string, deadline int64) (peer.TimerRecord, error)

	// CancelTimer stops node's timer for changesetID (the changeset was
	// confirmed, or the coordinator is restoring it itself and doesn't want
	// a redundant later self-restore). Idempotent.
	CancelTimer(ctx context.Context, changesetID, node string) (peer.TimerRecord, error)

	// TimerStatus returns node's current record for changesetID — the
	// reconciliation-on-reconnect read (Service.Reconcile).
	TimerStatus(ctx context.Context, changesetID, node string) (peer.TimerRecord, error)
}

// InventoryRefresher forces an immediate, targeted inventory poll after a
// changeset reaches a terminal state (committed/rolled_back/failed) so the
// UI reflects reality at once instead of waiting up to a full poll interval
// (T-104's collect.Collector.RefreshNow satisfies this). A nil refresher
// disables the post-terminal refresh (tests that don't wire collect).
type InventoryRefresher interface {
	RefreshNow(ctx context.Context, scope inventory.Scope) (inventory.Delta, error)
}
