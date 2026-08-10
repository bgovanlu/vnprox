package verify

// adapters.go wires the probe interfaces to the real world.
//
// The PVE adapter is the seam T-2502 pays for: it is a thin conversion over
// *pve.Client, so a test can point the whole suite at a cassette replay
// server through the exact client the production path uses. The alternative —
// a fake ClusterProbe in every test — would prove only that the checks agree
// with the fakes, which is the failure mode T-2108 documented four instances
// of and the reason cassettes exist at all.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// PVEAdapter satisfies ClusterProbe over the real typed PVE client.
type PVEAdapter struct {
	Client *pve.Client
}

// Nodes reads cluster membership.
func (a PVEAdapter) Nodes(ctx context.Context) ([]Node, error) {
	entries, err := a.Client.ClusterStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading cluster status: %w", err)
	}
	out := make([]Node, 0, len(entries))
	for _, e := range entries {
		if e.Type != "node" {
			continue
		}
		out = append(out, Node{
			Name:    e.Name,
			Address: e.IP,
			Online:  e.Online,
			Local:   e.Local,
		})
	}
	return out, nil
}

// PVEVersion reports the cluster's PVE release.
//
// GET /version is not in the typed client's surface, so this reads what the
// typed surface does expose. A cluster whose version cannot be established
// reports the empty string and the caller writes "unknown" — the honest
// answer, not a guess.
func (a PVEAdapter) PVEVersion(ctx context.Context) (string, error) {
	entries, err := a.Client.ClusterStatus(ctx)
	if err != nil {
		return "", fmt.Errorf("reading cluster status for a version: %w", err)
	}
	for _, e := range entries {
		if e.Type == "cluster" && e.Name != "" {
			return "cluster " + e.Name, nil
		}
	}
	return "", nil
}

// Interfaces reads one node's interfaces.
func (a PVEAdapter) Interfaces(ctx context.Context, node string) ([]Iface, error) {
	ifaces, err := a.Client.ListNodeNetwork(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("reading %s's interfaces: %w", node, err)
	}
	out := make([]Iface, 0, len(ifaces))
	for _, i := range ifaces {
		out = append(out, Iface{
			Name:      i.Iface,
			Type:      i.Type,
			Method:    i.Method,
			Address:   i.Address,
			BondMode:  i.BondMode,
			Slaves:    i.Slaves,
			Comments:  i.Comments,
			MTU:       i.MTU,
			VlanAware: i.BridgeVlanAware,
			Autostart: i.Autostart,
		})
	}
	return out, nil
}

// LocalHost satisfies HostProbe for commands and files on the machine
// vnproxctl is running on.
//
// It is deliberately localhost-only, and it says so rather than pretending
// otherwise: reading a peer node's /proc would mean either an SSH dependency
// this CLI does not have or a peer route that does not exist. A check asked
// for a node other than this one gets an error naming the limitation, which
// the check turns into a skip — never a silent read of the wrong node's
// state, which would be a confidently wrong hardware result.
type LocalHost struct {
	// Node is this machine's cluster node name.
	Node string
}

func (h LocalHost) check(node string) error {
	if node == "" || node == h.Node {
		return nil
	}
	return fmt.Errorf("this vnproxctl can only read host state on %s, not on %s: run the suite on %s as well", h.Node, node, node)
}

// Run executes a read-only command.
func (h LocalHost) Run(ctx context.Context, node, name string, args ...string) (string, error) {
	if err := h.check(node); err != nil {
		return "", err
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s is not on PATH: %w", name, err)
	}
	out, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

// ReadFile reads a file on this node.
func (h LocalHost) ReadFile(_ context.Context, node, path string) ([]byte, error) {
	if err := h.check(node); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path) //nolint:gosec // the paths are fixed literals in checks_*.go, not caller input
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return raw, nil
}
