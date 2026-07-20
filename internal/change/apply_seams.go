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

// QosGateway performs node-local tc/HTB mutations for T-1505's qos.shape.*
// op family: it renders (internal/qos.RenderTC/RenderTCTeardown) and execs
// each shape's on-node tc commands, and persists the shape's own intent row
// (store.QosShapeRepo) alongside. Like NodeAgent (and unlike the
// ticket-scoped PVEGateway), it is a daemon-level (root) dependency injected
// once at Service construction — a shape is node-local state with no PVE
// API surface of its own, so it needs no user ticket, and — crucially —
// that means its rollback works on the unattended commit-confirm-timeout /
// crash-recovery path too, the same way NodeAgent's interfaces-file restore
// does (T-205's existing inverse-order rollback contract this card's task
// text calls for).
//
// A nil QosGateway makes qos.shape.* ops unexecutable (execStep errors), the
// same "nil dependency -> that op family isn't wired" degradation the other
// seams use.
type QosGateway interface {
	// ApplyQosOp applies one qos.shape.* op on op.Target.Node: renders the
	// shape's tc/HTB commands and execs them, and persists/updates/removes
	// its qos_shapes row accordingly.
	ApplyQosOp(ctx context.Context, op Op) error

	// SnapshotQos captures node's full set of currently-stored shapes as an
	// opaque string, for the pre-apply snapshot.
	SnapshotQos(ctx context.Context, node string) (string, error)

	// RestoreQos reconciles node's shape set back to a SnapshotQos output:
	// shapes present live but absent from the snapshot are torn down (tc
	// class/filter removed, store row deleted); shapes in the snapshot but
	// missing live are re-applied from their exact stored fields. Callable
	// unattended — no user ticket needed.
	RestoreQos(ctx context.Context, node, snapshot string) error
}

// WGGateway performs node-local WireGuard mutations for T-1401's wg.* op
// family. Like NodeAgent (and unlike the ticket-scoped PVEGateway), it is a
// daemon-level (root) dependency injected once at Service construction: the
// keypair is generated on and never leaves the owning node, the private key is
// sealed with the same SessionCipher session tickets use, and — crucially —
// because it needs no user ticket, its rollback works on the unattended
// commit-confirm-timeout / crash-recovery paths too, so a wg.tunnel.create
// that times out un-confirmed reverts fully (tunnel + generated keypair
// removed, no orphaned key material — T-1401 AC6), unlike the PVEGateway
// families' same-request-only rollback.
//
// A nil WGGateway makes wg.* ops unexecutable (execStep errors), the same
// "nil dependency -> that op family isn't wired" degradation the other seams
// use — a daemon that never wires WireGuard simply can't apply a wg.* op.
type WGGateway interface {
	// ApplyWgOp applies one wg.* op on op.Target.Node. For wg.tunnel.create it
	// generates the keypair on-node, seals+stores it, writes the on-node
	// config, and brings the interface up (fixed-argv wg/wg-quick exec); for
	// the other ops it reconciles config + store accordingly. External peers
	// (WgPeerAddParams.External) are stored and rendered into this tunnel's own
	// config, but this method never issues a call against the external peer's
	// own side (T-1401 AC5).
	ApplyWgOp(ctx context.Context, op Op) error

	// SnapshotWg captures node's full WireGuard state (every tunnel + its
	// peers, private keys kept sealed/verbatim, plus the on-node config) as an
	// opaque string, for the pre-apply snapshot. Never exposes a plaintext
	// private key.
	SnapshotWg(ctx context.Context, node string) (string, error)

	// RestoreWg reconciles node's WireGuard state back to a SnapshotWg output:
	// tunnels present live but absent from the snapshot are torn down (config
	// removed, interface brought down, store row + sealed key deleted); tunnels
	// in the snapshot but missing live are re-created from their exact sealed
	// key bytes (so a rolled-back delete restores the identical keypair, never
	// a freshly generated one). Callable unattended — no user ticket needed.
	RestoreWg(ctx context.Context, node, snapshot string) error
}

// PVEGateway performs cluster-scope PVE API mutations under the *user's own*
// ticket (docs/architecture.md §6, D3: "PVE ACLs enforced by PVE; no
// privilege escalation through vnprox"). It is passed per Apply/Rollback
// call, constructed by the API layer from the requesting session's live
// *pve.Client (auth.Service.PVEClientFor), so PVE authorizes every write as
// the logged-in operator.
//
// T-205's executable cluster-scope step was sdn.apply alone; T-402 adds the
// sdn.zone/vnet/subnet.* write family (SDNStageOp) and the read-back
// SDNConfig needs for its own pre-apply/rollback snapshot; T-502 adds the
// full fw.* firewall op family; T-405 adds the ipam.alloc.* family
// (AllocateIPAMAddress/ReleaseIPAMAddress) below. The remaining guest op
// family still needs its own pve.Client write+task methods (a follow-up
// surface) before the planner will emit steps for it — see apply_plan.go's
// nodeFileOpTypes/sdnStageOpTypes/fwOpTypes/ipamOpTypes and the T-205
// report's residual-risk list.
//
// IMPORTANT, flagged limitation shared by sdn.apply and every fw.* method
// (see T-502's completion report for the full discussion): every PVE-API
// method on this interface runs under the *user's* PVE ticket, which only
// exists for the duration of the synchronous Apply()/Rollback() call that
// received a live pveGW. Unlike NodeAgent (root-level host access, callable
// by the daemon at any time — including the unattended commit-confirm-
// timeout and crash-recovery paths), these methods cannot be invoked by the
// *unattended* rollback paths (autoRollback, ArmPendingRollbacks'
// interrupted-apply recovery): there is no live ticket to authenticate with
// once the originating HTTP request has ended. Both SDN and fw.* therefore
// only implement SAME-REQUEST rollback (a later step's failure rolls back
// an earlier SDN/fw.* step within the same Apply() call, while pveGW is
// still valid — see apply_exec.go's rollbackAfterFailure/undoFwTargets/
// restoreSDN) — an SDN- or fw.*-only changeset that reaches
// awaiting_confirm and then times out (or the daemon crashes mid-window) is
// NOT automatically reverted. This is a pre-existing architectural gap
// neither task introduces, only inherits and makes more visible; see the
// T-502 report for the flagged follow-up (e.g. a narrowly-scoped
// daemon-level PVE token for unattended firewall/SDN rollback).
type PVEGateway interface {
	// SDNStageOp performs one cluster-scope sdn.zone/vnet/subnet
	// create/update/delete op against PVE's staged (pending) SDN config —
	// docs/data-model.md §3's "(1) cluster-scope PVE API calls" planner
	// step, which orders before per-node file steps and before the
	// trailing sdn.apply. op.Type must be one of the sdn.zone/vnet/subnet.*
	// ops (BuildPlan never emits a StepSDNStage for any other op type).
	// subnetVnet is consulted only for sdn.subnet.update/delete (whose
	// params carry no vnet field of their own — see params_sdn.go's doc
	// comments on why the subnet target's own Ref.ID is just the CIDR):
	// the executor resolves it from the changeset's own ops or the live
	// inventory snapshot (apply_sdn.go's resolveSubnetVnet) before calling.
	// It is ignored for every other op type.
	SDNStageOp(ctx context.Context, op Op, subnetVnet string) error

	// ApplySDN applies all pending cluster SDN config (PUT /cluster/sdn),
	// blocks until the resulting task reaches a terminal state, and then
	// verifies each of affectedZones' per-node realization status is
	// healthy (docs/features/sdn.md §4: "post-apply verification that each
	// node's status reports the zone healthy"). It always returns a
	// populated SDNApplyResult (UPID/Node set as soon as the task starts),
	// even alongside a non-nil error, so a failing step can still record a
	// task-log deep link.
	ApplySDN(ctx context.Context, affectedZones []string) (SDNApplyResult, error)

	// SDNConfig returns the current *staged* (pending-merged) zone/vnet/
	// subnet configuration — real PVE's actual /etc/pve/sdn/*.cfg content
	// (docs/features/sdn.md §4's "pre-snapshot of /etc/pve/sdn/*.cfg"):
	// PVE stages every zone/vnet/subnet create/update/delete into those
	// cfg files immediately, before any apply flushes them into the live
	// dnsmasq/FRR/bridge config (the same staged-vs-running distinction
	// T-401's `?running=1` convention reads). Called both before any
	// mutation (captures the restore target — staged and running are
	// identical at that point, absent a stray edit from outside this
	// changeset) and, by rollback, to read the current state to diff
	// against that target (apply_sdn.go's sdnRestoreOps).
	SDNConfig(ctx context.Context) (SDNConfig, error)

	// FirewallRuleFields fetches the live content of the rule currently at
	// pos in the ruleset named by ref, for fw.rule.move's apply-time
	// position revalidation (T-502 acceptance criterion 3). Returns
	// *ErrFwRuleNotFound if pos doesn't currently exist.
	FirewallRuleFields(ctx context.Context, ref inventory.Ref, pos int) (FwRuleFields, error)

	// ApplyFwOp executes one fw.* op against the PVE firewall API scope
	// named by op.Target.
	ApplyFwOp(ctx context.Context, op Op) error

	// SnapshotFirewallScope captures ref's full current ruleset content
	// (rules/options/aliases/ipsets, plus security groups for the cluster
	// scope) as an opaque string, for docs/architecture.md §4's "affected
	// firewall files" snapshot and as RestoreFirewallScope's input.
	SnapshotFirewallScope(ctx context.Context, ref inventory.Ref) (string, error)

	// RestoreFirewallScope reconciles ref's live ruleset back to a
	// snapshot SnapshotFirewallScope captured earlier (same-request
	// rollback only — see this interface's doc comment above).
	RestoreFirewallScope(ctx context.Context, ref inventory.Ref, snapshot string) error

	// FirewallCompileStatus reports node's current pve-firewall compiled
	// status (docs/features/firewall.md §3's post-apply verification).
	FirewallCompileStatus(ctx context.Context, node string) (FwCompileStatus, error)

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

// SDNApplyResult is ApplySDN's outcome: the underlying PVE task's identity
// (for a task-log deep link, docs/features/sdn.md §4: "failures link
// straight to the failing node's task log") plus the post-apply per-zone
// health read, one entry per zone ApplySDN was asked to verify.
type SDNApplyResult struct {
	UPID  string
	Node  string
	Zones []SDNZoneHealth
}

// SDNZoneHealth is one zone's post-apply per-node status, as read from PVE's
// GET /cluster/sdn/zones/{zone}/status.
type SDNZoneHealth struct {
	Zone  string
	Nodes []SDNNodeHealth
}

// SDNNodeHealth is one node's realization status for a zone. Status is
// "ok"|"pending"|"error" (docs/api.md's NodeStatus shape, GET /sdn).
type SDNNodeHealth struct {
	Node, Status, Detail string
}

// Healthy reports whether every zone/node in the result reports status
// "ok" — ApplySDN's own post-apply verification gate.
func (r SDNApplyResult) Healthy() bool {
	for _, z := range r.Zones {
		for _, n := range z.Nodes {
			if n.Status != "ok" {
				return false
			}
		}
	}
	return true
}

// firstUnhealthy returns the first non-ok (zone, node) health entry, for
// building *ErrSDNZoneUnhealthy. ok is false if every entry is healthy.
func (r SDNApplyResult) firstUnhealthy() (zone string, node SDNNodeHealth, ok bool) {
	for _, z := range r.Zones {
		for _, n := range z.Nodes {
			if n.Status != "ok" {
				return z.Zone, n, true
			}
		}
	}
	return "", SDNNodeHealth{}, false
}

// SDNConfig is the full cluster SDN configuration (running or staged,
// depending on the call site) this package's own copy of docs/data-model.md
// §1's SdnZone/SdnVnet/SdnSubnet field set — deliberately not
// internal/inventory's Entity-shaped types (which carry Ref/Pending/
// NodeStatus bookkeeping this seam has no use for) and not internal/pve's
// wire types (which would leak a PVE dependency into this package, unlike
// every other type in this file). Used for the pre-apply/rollback snapshot
// (apply_snapshot.go's sdnConfigSnapshotFiles) and the ops-diff rollback
// restores from (apply_sdn.go's sdnRestoreOps).
type SDNConfig struct {
	Zones      []SDNZoneConfig      `json:"zones"`
	Vnets      []SDNVnetConfig      `json:"vnets"`
	Subnets    []SDNSubnetConfig    `json:"subnets"`
	DnsZones   []SDNDnsZoneConfig   `json:"dnsZones,omitempty"`
	DnsRecords []SDNDnsRecordConfig `json:"dnsRecords,omitempty"`
}

// SDNDnsZoneConfig mirrors SdnDnsZoneCreateParams' field set plus the zone's
// own domain id (T-1204).
type SDNDnsZoneConfig struct {
	ID  string `json:"id"`
	DNS string `json:"dns,omitempty"`
	TTL int    `json:"ttl,omitempty"`
}

// SDNDnsRecordConfig mirrors SdnDnsRecordCreateParams' field set. ID is the
// "<zone>/<name>/<type>" composite Ref.ID.
type SDNDnsRecordConfig struct {
	ID    string `json:"id"`
	Zone  string `json:"zone"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   int    `json:"ttl,omitempty"`
}

// SDNZoneConfig mirrors SdnZoneCreateParams' field set (the params struct
// already has everything a zone's identity needs).
type SDNZoneConfig struct {
	ID         string   `json:"id"`
	Type       string   `json:"type"`
	Bridge     string   `json:"bridge,omitempty"`
	Controller string   `json:"controller,omitempty"`
	Nodes      []string `json:"nodes,omitempty"`
	ExitNodes  []string `json:"exitNodes,omitempty"`
	Peers      []string `json:"peers,omitempty"`
	VrfVxlan   int      `json:"vrfVxlan,omitempty"`
	MTU        int      `json:"mtu,omitempty"`
}

// SDNVnetConfig mirrors SdnVnetCreateParams' field set.
type SDNVnetConfig struct {
	ID        string `json:"id"`
	Zone      string `json:"zone"`
	Alias     string `json:"alias,omitempty"`
	Tag       int    `json:"tag,omitempty"`
	VlanAware bool   `json:"vlanAware,omitempty"`
}

// SDNSubnetConfig mirrors SdnSubnetCreateParams' field set.
type SDNSubnetConfig struct {
	ID            string   `json:"id"`
	Vnet          string   `json:"vnet"`
	Gateway       string   `json:"gateway,omitempty"`
	DNSZonePrefix string   `json:"dnsZonePrefix,omitempty"`
	DHCPRanges    []string `json:"dhcpRanges,omitempty"`
	SNAT          bool     `json:"snat,omitempty"`
}

// FwCompileStatus is one node's pve-firewall compile-loop result, the
// PVEGateway-level counterpart of pve.FirewallCompileStatus (kept as its
// own type here so this package doesn't need to import internal/pve just
// for this one shape — the same "small interface/seam-local type" pattern
// this file already uses throughout).
type FwCompileStatus struct {
	Message string
	OK      bool
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
