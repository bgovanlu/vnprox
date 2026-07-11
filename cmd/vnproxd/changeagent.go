package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// Default node-file paths (overridable in tests). interfacesNewPath is the
// staged file ifupdown2 uses (docs/architecture.md §4).
const (
	interfacesFilePath = "/etc/network/interfaces"
	interfacesNewPath  = "/etc/network/interfaces.new"
)

// hostNodeAgent is the production change.NodeAgent: it stages and reloads the
// local node's /etc/network/interfaces as root (the "host writer" the change
// engine's per-node steps drive), and reads it back for snapshots and diffs.
//
// NEEDS HARDWARE VALIDATION: the reload path execs ifreload(8) and rewrites a
// real system file; it is exercised only by changeagent_test.go's temp-dir +
// fake-reload unit test, never against a live ifupdown2. Peer-node staging
// (a coordinating daemon writing another node's file via the peer API) is
// T-304's scope — this single-node-scoped agent operates on local paths for
// whatever node it's handed (correct for a single-node deployment; a cluster
// deployment must not route peer-node file steps here until T-304 lands).
type hostNodeAgent struct {
	reload         func(ctx context.Context) error
	log            *slog.Logger
	interfacesPath string
	pendingPath    string
	mu             sync.Mutex
}

// hostNodeAgent also satisfies peer.HostWriter (StageInterfaces/
// ReloadInterfaces already match; RestoreInterfaces below adds the third
// method), so the same production agent backs both the local change engine
// (change.NodeAgent) and this node's peer API host-write routes (T-301) —
// one implementation of "how vnproxd mutates this node's network files",
// not two.
var (
	_ change.NodeAgent = (*hostNodeAgent)(nil)
	_ peer.HostWriter  = (*hostNodeAgent)(nil)
)

func newHostNodeAgent(logger *slog.Logger) *hostNodeAgent {
	a := &hostNodeAgent{
		interfacesPath: interfacesFilePath,
		pendingPath:    interfacesNewPath,
		log:            logger,
	}
	a.reload = a.execIfreload
	return a
}

// devInterfacesSeed is the fixture newDevNodeAgent seeds an empty sandbox
// with, so ReadInterfaces/diff work out of the box on a fresh `make dev`.
const devInterfacesSeed = `# vnprox dev sandbox — this file stands in for /etc/network/interfaces.
# It is safe to edit or delete; it is re-seeded when missing.

auto lo
iface lo inet loopback

auto eno1
iface eno1 inet manual

auto vmbr0
iface vmbr0 inet static
	address 192.0.2.10/24
	gateway 192.0.2.1
	bridge-ports eno1
	bridge-stp off
	bridge-fd 0
`

// newDevNodeAgent is the sandboxed dev variant of newHostNodeAgent
// ([safety] dev_interfaces_dir; audit-phase-2 F-22): the same staging/
// commit/backup logic, but operating on <dir>/interfaces(.new) instead of
// the real /etc/network files, with the ifreload step replaced by a logged
// no-op — so a `make dev` daemon can exercise the full diff/apply/rollback
// flow without ever being able to reconfigure the developer's machine.
func newDevNodeAgent(dir string, logger *slog.Logger) (*hostNodeAgent, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating dev interfaces sandbox %s: %w", dir, err)
	}
	path := filepath.Join(dir, "interfaces")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if writeErr := os.WriteFile(path, []byte(devInterfacesSeed), 0o644); writeErr != nil {
			return nil, fmt.Errorf("seeding dev interfaces sandbox %s: %w", path, writeErr)
		}
	} else if err != nil {
		return nil, fmt.Errorf("checking dev interfaces sandbox %s: %w", path, err)
	}
	a := &hostNodeAgent{
		interfacesPath: path,
		pendingPath:    filepath.Join(dir, "interfaces.new"),
		log:            logger,
	}
	a.reload = func(context.Context) error {
		logger.Info("change: dev sandbox agent — skipping real ifreload", "interfaces", path)
		return nil
	}
	return a, nil
}

func (a *hostNodeAgent) execIfreload(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "ifreload", "-a")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ifreload -a: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (a *hostNodeAgent) ReadInterfaces(_ context.Context, _ string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, err := os.ReadFile(a.interfacesPath)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", a.interfacesPath, err)
	}
	return string(b), nil
}

func (a *hostNodeAgent) StageInterfaces(_ context.Context, _, content string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.WriteFile(a.pendingPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("staging %s: %w", a.pendingPath, err)
	}
	return nil
}

// ReloadInterfaces applies the staged file and reloads the network, with a
// backup-and-restore so a failed reload leaves the committed file exactly as
// it was (the NodeAgent contract): read the staged file, back up the current
// committed file, move the staged file into place, ifreload; on failure,
// restore the backup and re-reload before returning the error.
func (a *hostNodeAgent) ReloadInterfaces(ctx context.Context, node string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	staged, err := os.ReadFile(a.pendingPath)
	if err != nil {
		return fmt.Errorf("reading staged %s: %w", a.pendingPath, err)
	}
	backup, err := os.ReadFile(a.interfacesPath)
	if err != nil {
		return fmt.Errorf("reading %s for backup: %w", a.interfacesPath, err)
	}
	if err := os.WriteFile(a.interfacesPath, staged, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", a.interfacesPath, err)
	}
	if err := a.reload(ctx); err != nil {
		if restoreErr := os.WriteFile(a.interfacesPath, backup, 0o644); restoreErr != nil {
			a.log.Error("change: failed to restore interfaces after failed reload", "node", node, "error", restoreErr)
		} else if reErr := a.reload(ctx); reErr != nil {
			a.log.Error("change: failed to re-reload after restoring interfaces", "node", node, "error", reErr)
		}
		return fmt.Errorf("reloading network on %s: %w", node, err)
	}
	_ = os.Remove(a.pendingPath)
	return nil
}

func (a *hostNodeAgent) DiscardStaged(_ context.Context, _ string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.Remove(a.pendingPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("discarding staged %s: %w", a.pendingPath, err)
	}
	return nil
}

// RestoreInterfaces implements peer.HostWriter for T-301's
// POST /api/peer/host/restore: write content directly as the committed
// interfaces file and reload, bypassing the normal stage/review flow. This
// is the rollback path (T-304's distributed commit-confirm timers restore
// a known-good pre-apply snapshot under time pressure, not a
// user-reviewed draft), so it reuses StageInterfaces/ReloadInterfaces'
// existing backup-and-restore-on-failure guarantee rather than duplicating
// it.
func (a *hostNodeAgent) RestoreInterfaces(ctx context.Context, node, content string) error {
	if err := a.StageInterfaces(ctx, node, content); err != nil {
		return fmt.Errorf("staging restore content for %s: %w", node, err)
	}
	if err := a.ReloadInterfaces(ctx, node); err != nil {
		return fmt.Errorf("reloading restored interfaces for %s: %w", node, err)
	}
	return nil
}

// pveGatewayProvider builds a change.PVEGateway from the requesting session's
// own PVE client (docs/architecture.md §6: cluster-scope writes use the
// user's ticket). It satisfies api.PVEGatewayProvider.
type pveGatewayProvider struct {
	auth *auth.Service
}

func (p pveGatewayProvider) GatewayFor(ctx context.Context) (change.PVEGateway, bool) {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return nil, false
	}
	client, ok := p.auth.PVEClientFor(id.SessionID)
	if !ok {
		return nil, false
	}
	return &pveSDNGateway{client: client}, true
}

// pveSDNGateway realizes the sdn.zone/vnet/subnet.* stage ops and sdn.apply
// step (T-402) through the user's own client (docs/architecture.md §6).
type pveSDNGateway struct {
	client *pve.Client
}

// SDNStageOp implements change.PVEGateway: dispatches one sdn.zone/vnet/
// subnet create/update/delete op to the matching pve.Client write call.
// Update ops only set the fields their params carry (nil pointer fields
// leave the corresponding wire field zero-valued, matching a partial PUT —
// real PVE/pvemock's update handlers replace the whole object server-side
// per this codebase's existing SDNZoneSpec/SDNVnetSpec/SDNSubnetSpec update
// handlers, so a caller wanting a true partial merge must read-modify-write;
// change.Service's update ops are themselves already the merged intent by
// the time they reach here — see params_sdn.go's *UpdateParams doc
// comments — so this is not a functional gap for the ops this package's
// own validators/projection produce).
func (g *pveSDNGateway) SDNStageOp(ctx context.Context, op change.Op, subnetVnet string) error {
	switch p := op.Params.(type) {
	case *change.SdnZoneCreateParams:
		return g.client.CreateSDNZone(ctx, pve.SDNZone{
			ID: op.Target.ID, Type: p.Type, Bridge: p.Bridge, Controller: p.Controller,
			Nodes: p.Nodes, VrfVxlan: p.VrfVxlan, MTU: p.MTU,
		})
	case *change.SdnZoneUpdateParams:
		z := pve.SDNZone{ID: op.Target.ID}
		if p.Bridge != nil {
			z.Bridge = *p.Bridge
		}
		if p.Controller != nil {
			z.Controller = *p.Controller
		}
		if p.Nodes != nil {
			z.Nodes = *p.Nodes
		}
		if p.VrfVxlan != nil {
			z.VrfVxlan = *p.VrfVxlan
		}
		if p.MTU != nil {
			z.MTU = *p.MTU
		}
		return g.client.UpdateSDNZone(ctx, op.Target.ID, z)
	case *change.SdnZoneDeleteParams:
		return g.client.DeleteSDNZone(ctx, op.Target.ID)

	case *change.SdnVnetCreateParams:
		return g.client.CreateSDNVnet(ctx, pve.SDNVnet{
			ID: pve.SDNVnetID(op.Target.ID), Zone: p.Zone, Alias: p.Alias, Tag: p.Tag, VlanAware: p.VlanAware,
		})
	case *change.SdnVnetUpdateParams:
		id := pve.SDNVnetID(op.Target.ID)
		v := pve.SDNVnet{ID: id}
		if p.Alias != nil {
			v.Alias = *p.Alias
		}
		if p.Tag != nil {
			v.Tag = *p.Tag
		}
		if p.VlanAware != nil {
			v.VlanAware = *p.VlanAware
		}
		return g.client.UpdateSDNVnet(ctx, id, v)
	case *change.SdnVnetDeleteParams:
		return g.client.DeleteSDNVnet(ctx, pve.SDNVnetID(op.Target.ID))

	case *change.SdnSubnetCreateParams:
		start, end := firstDHCPRange(p.DHCPRanges)
		return g.client.CreateSDNSubnet(ctx, pve.SDNVnetID(p.Vnet), pve.SDNSubnet{
			ID: pve.SDNSubnetID(op.Target.ID), Vnet: pve.SDNVnetID(p.Vnet), CIDR: p.CIDR, Gateway: p.Gateway,
			DHCPRangeStart: start, DHCPRangeEnd: end, SNAT: p.SNAT,
		})
	case *change.SdnSubnetUpdateParams:
		vnet := pve.SDNVnetID(subnetVnet)
		s := pve.SDNSubnet{ID: pve.SDNSubnetID(op.Target.ID), Vnet: vnet, CIDR: op.Target.ID}
		if p.Gateway != nil {
			s.Gateway = *p.Gateway
		}
		if p.DHCPRanges != nil {
			s.DHCPRangeStart, s.DHCPRangeEnd = firstDHCPRange(*p.DHCPRanges)
		}
		if p.SNAT != nil {
			s.SNAT = *p.SNAT
		}
		return g.client.UpdateSDNSubnet(ctx, vnet, pve.SDNSubnetID(op.Target.ID), s)
	case *change.SdnSubnetDeleteParams:
		return g.client.DeleteSDNSubnet(ctx, pve.SDNVnetID(subnetVnet), pve.SDNSubnetID(op.Target.ID))

	default:
		return fmt.Errorf("changeagent: SDNStageOp: unsupported op type %q", op.Type)
	}
}

// firstDHCPRange splits ranges[0] ("start-end", change.validDHCPRange's
// shape) into its two endpoints for pve.SDNSubnet's single-range wire
// field. Real PVE (and this codebase's pve.SDNSubnet/pvemock SDNSubnetSpec,
// unmodified by this task) models only one DHCP range per subnet and has no
// DNSZonePrefix wire field at all — change.SdnSubnetCreateParams'
// DNSZonePrefix and any DHCPRanges beyond the first have no PVE-side
// representation to stage yet (a documented, pre-existing gap — see the
// T-402 report).
func firstDHCPRange(ranges []string) (start, end string) {
	if len(ranges) == 0 {
		return "", ""
	}
	parts := strings.SplitN(ranges[0], "-", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// ApplySDN implements change.PVEGateway: PUT /cluster/sdn, wait for the
// task, then read back affectedZones' per-node status for T-402's post-apply
// health verification (docs/features/sdn.md §4). The result (UPID/Node) is
// populated before the wait even starts, so a caller gets a task-log deep
// link regardless of whether the task itself or the health check is what
// ultimately fails.
func (g *pveSDNGateway) ApplySDN(ctx context.Context, affectedZones []string) (change.SDNApplyResult, error) {
	upid, err := g.client.ApplySDN(ctx)
	if err != nil {
		return change.SDNApplyResult{}, err
	}
	node := upidNode(upid)
	result := change.SDNApplyResult{UPID: upid, Node: node}

	if _, err := g.client.WaitTask(ctx, node, upid, pve.WaitOptions{Timeout: 5 * time.Minute}); err != nil {
		return result, err
	}

	for _, zoneID := range affectedZones {
		statuses, err := g.client.GetSDNZoneStatus(ctx, zoneID)
		if err != nil {
			return result, fmt.Errorf("changeagent: reading post-apply status for sdn zone %s: %w", zoneID, err)
		}
		zh := change.SDNZoneHealth{Zone: zoneID}
		for _, st := range statuses {
			zh.Nodes = append(zh.Nodes, change.SDNNodeHealth{Node: st.Node, Status: st.Status, Detail: st.Detail})
		}
		result.Zones = append(result.Zones, zh)
	}
	return result, nil
}

// SDNConfig implements change.PVEGateway: reads the full staged (pending-
// merged) zone/vnet/subnet tree, T-402's pre-apply/rollback snapshot source
// (see that interface method's doc comment for why "staged" and not
// "?running=1").
func (g *pveSDNGateway) SDNConfig(ctx context.Context) (change.SDNConfig, error) {
	zones, err := g.client.ListSDNZones(ctx)
	if err != nil {
		return change.SDNConfig{}, fmt.Errorf("changeagent: listing sdn zones: %w", err)
	}
	vnets, err := g.client.ListSDNVnets(ctx)
	if err != nil {
		return change.SDNConfig{}, fmt.Errorf("changeagent: listing sdn vnets: %w", err)
	}

	var cfg change.SDNConfig
	for _, z := range zones {
		cfg.Zones = append(cfg.Zones, change.SDNZoneConfig{
			ID: z.ID, Type: z.Type, Bridge: z.Bridge, Controller: z.Controller,
			Nodes: z.Nodes, VrfVxlan: z.VrfVxlan, MTU: z.MTU,
		})
	}
	for _, v := range vnets {
		// SDNVnetConfig.ID is internal/change's "<zone>/<vnet>" Ref.ID
		// convention (see pve.SDNVnetID's doc comment), reconstructed here
		// from PVE's own bare vnet id (v.ID) + zone (v.Zone) — not v.ID
		// alone.
		refID := v.Zone + "/" + v.ID
		cfg.Vnets = append(cfg.Vnets, change.SDNVnetConfig{
			ID: refID, Zone: v.Zone, Alias: v.Alias, Tag: v.Tag, VlanAware: v.VlanAware,
		})
		subnets, err := g.client.ListSDNSubnets(ctx, v.ID)
		if err != nil {
			return change.SDNConfig{}, fmt.Errorf("changeagent: listing sdn subnets for vnet %s: %w", v.ID, err)
		}
		for _, s := range subnets {
			var ranges []string
			if s.DHCPRangeStart != "" && s.DHCPRangeEnd != "" {
				ranges = []string{s.DHCPRangeStart + "-" + s.DHCPRangeEnd}
			}
			// SDNSubnetConfig.ID is the CIDR (internal/change's convention
			// throughout — see pve.SDNSubnetID's doc comment), not PVE's
			// own dash-form wire id (s.ID) used only for the URL path
			// segment; Vnet is likewise the "<zone>/<vnet>" Ref.ID form,
			// not PVE's bare s.Vnet.
			cfg.Subnets = append(cfg.Subnets, change.SDNSubnetConfig{
				ID: s.CIDR, Vnet: refID, Gateway: s.Gateway, DHCPRanges: ranges, SNAT: s.SNAT,
			})
		}
	}
	return cfg, nil
}

// upidNode extracts the node segment from a PVE UPID
// ("UPID:<node>:<pid>:...").
func upidNode(upid string) string {
	parts := strings.Split(upid, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}
