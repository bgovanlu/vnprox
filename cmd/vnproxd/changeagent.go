package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
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

// pveSDNGateway realizes the sdn.apply step through the user's client.
type pveSDNGateway struct {
	client *pve.Client
}

func (g *pveSDNGateway) ApplySDN(ctx context.Context) error {
	upid, err := g.client.ApplySDN(ctx)
	if err != nil {
		return err
	}
	node := upidNode(upid)
	if _, err := g.client.WaitTask(ctx, node, upid, pve.WaitOptions{Timeout: 5 * time.Minute}); err != nil {
		return err
	}
	return nil
}

// AllocateIPAMAddress realizes T-405's ipam.alloc.create op: PVE's IPAM
// write is a synchronous API call (no task), unlike ApplySDN above.
// subnetCIDR is recorded on the created entry so internal/ipam's
// per-subnet bucketing (which keys off exactly this field) finds it.
func (g *pveSDNGateway) AllocateIPAMAddress(ctx context.Context, vnet, subnetCIDR string, alloc change.IpamAllocCreateParams) error {
	return g.client.CreateIPAMAllocation(ctx, vnet, pve.IPAMAllocation{
		IP:       allocHostAddr(alloc.CIDR),
		MAC:      alloc.MAC,
		Hostname: alloc.Hostname,
		Subnet:   subnetCIDR,
	})
}

// ReleaseIPAMAddress realizes T-405's ipam.alloc.delete op.
func (g *pveSDNGateway) ReleaseIPAMAddress(ctx context.Context, vnet, subnetCIDR, cidr string) error {
	return g.client.DeleteIPAMAllocation(ctx, vnet, allocHostAddr(cidr), subnetCIDR)
}

// allocHostAddr reduces an ipam.alloc op's CIDR (docs/data-model.md §3:
// "typically a /32 or /128 host route") to the bare host address PVE's
// IPAM plugin API expects. cidr that fails to parse as a CIDR (already a
// bare address, or malformed — schema validation already rejects the
// latter before apply ever reaches here) is passed through unchanged.
func allocHostAddr(cidr string) string {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return cidr
	}
	return ip.String()
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
