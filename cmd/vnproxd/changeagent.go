package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/change"
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

func newHostNodeAgent(logger *slog.Logger) *hostNodeAgent {
	a := &hostNodeAgent{
		interfacesPath: interfacesFilePath,
		pendingPath:    interfacesNewPath,
		log:            logger,
	}
	a.reload = a.execIfreload
	return a
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

// upidNode extracts the node segment from a PVE UPID
// ("UPID:<node>:<pid>:...").
func upidNode(upid string) string {
	parts := strings.Split(upid, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}
