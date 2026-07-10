package change

import (
	"context"

	"github.com/bgovanlu/vnprox/internal/inventory"
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
// planner/executor never branch on locality. Peer routing is NOT
// implemented yet — it is T-304's scope. The current production
// implementation (cmd/vnproxd's hostNodeAgent) only operates on the local
// node's files and must not be handed peer-node steps until T-304 lands;
// see hostNodeAgent's doc comment for the honest state of that constraint.
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
// T-205's executable cluster-scope step is sdn.apply; the guest/SDN-write/fw/
// ipam op families need their own pve.Client write+task methods (a T-101/
// follow-up surface) before the planner will emit steps for them — see
// plan.go's supportedOpTypes and the T-205 report's residual-risk list.
type PVEGateway interface {
	// ApplySDN applies all pending cluster SDN config (PUT /cluster/sdn) and
	// blocks until the resulting task reaches a terminal state, returning a
	// non-nil error if the task fails or times out.
	ApplySDN(ctx context.Context) error
}

// InventoryRefresher forces an immediate, targeted inventory poll after a
// changeset reaches a terminal state (committed/rolled_back/failed) so the
// UI reflects reality at once instead of waiting up to a full poll interval
// (T-104's collect.Collector.RefreshNow satisfies this). A nil refresher
// disables the post-terminal refresh (tests that don't wire collect).
type InventoryRefresher interface {
	RefreshNow(ctx context.Context, scope inventory.Scope) (inventory.Delta, error)
}
