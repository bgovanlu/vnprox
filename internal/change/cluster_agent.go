package change

import (
	"context"
	"errors"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/peer"
)

// ErrUnknownPeerNode is returned by a PeerLocator when a plan step's node is
// neither the local daemon's own PVE node nor found among the discovered
// cluster peers — a node named in a changeset that no longer exists in the
// cluster (renamed, removed) rather than a transient reachability problem
// (contrast peer.ErrPeerUnreachable).
var ErrUnknownPeerNode = errors.New("change: node is not this daemon's own node or a known cluster peer")

// PeerLocator resolves a PVE node name to its peer API address. It exists so
// ClusterNodeAgent/ClusterTimerAgent don't hard-code a discovery strategy:
// production wiring uses DiscoveringPeerLocator (backed by *peer.Client's
// PVE-cluster-status discovery); tests use a static map.
type PeerLocator interface {
	Peer(ctx context.Context, node string) (peer.Peer, error)
}

// DiscoveringPeerLocator resolves node names by calling peer.Client.Peers
// (PVE `/cluster/status`, docs/architecture.md §5) fresh on every lookup —
// simple and always current at the cost of a discovery round trip per
// unresolved node per apply step. Changesets execute at human timescales
// (seconds between steps at most), so this is deliberately not cached: a
// node that just joined or whose IP just changed must be resolvable on the
// very next apply, not after some cache TTL expires.
type DiscoveringPeerLocator struct {
	client *peer.Client
}

// NewDiscoveringPeerLocator constructs a DiscoveringPeerLocator.
func NewDiscoveringPeerLocator(client *peer.Client) *DiscoveringPeerLocator {
	return &DiscoveringPeerLocator{client: client}
}

func (l *DiscoveringPeerLocator) Peer(ctx context.Context, node string) (peer.Peer, error) {
	peers, err := l.client.Peers(ctx)
	if err != nil {
		return peer.Peer{}, fmt.Errorf("change: discovering peer for node %s: %w", node, err)
	}
	for _, p := range peers {
		if p.Node == node {
			return p, nil
		}
	}
	return peer.Peer{}, fmt.Errorf("%w: %s", ErrUnknownPeerNode, node)
}

// StaticPeerLocator is a fixed node->Peer map, for tests that don't need
// live PVE cluster-status discovery.
type StaticPeerLocator map[string]peer.Peer

func (l StaticPeerLocator) Peer(_ context.Context, node string) (peer.Peer, error) {
	p, ok := l[node]
	if !ok {
		return peer.Peer{}, fmt.Errorf("%w: %s", ErrUnknownPeerNode, node)
	}
	return p, nil
}

// ClusterNodeAgent implements NodeAgent by routing each call to the local
// host-writer when node is this daemon's own PVE node, or through the peer
// API otherwise — the "single NodeAgent value abstracting local and peer
// nodes" apply_seams.go's NodeAgent doc comment describes as T-304's scope.
// It is deliberately dumb about *how* a node is reached; PeerLocator answers
// that.
type ClusterNodeAgent struct {
	localNode func() string
	local     NodeAgent
	client    *peer.Client
	locator   PeerLocator
}

// NewClusterNodeAgent constructs a ClusterNodeAgent. localNode resolves
// this daemon's own PVE node name on every call (calls for it never leave
// the process) rather than fixing it at construction time, because
// production wiring (cmd/vnproxd) only learns its own node name
// asymmetrically, from the collector's first successful PVE cluster-status
// poll (internal/collect.Collector.Status().LocalNode) — it is empty for a
// brief window after startup and a fixed string captured at construction
// would stay wrong forever. Tests pass a fixed closure. local is the
// concrete host-writer for that node (cmd/vnproxd's hostNodeAgent in
// production).
func NewClusterNodeAgent(localNode func() string, local NodeAgent, client *peer.Client, locator PeerLocator) *ClusterNodeAgent {
	return &ClusterNodeAgent{localNode: localNode, local: local, client: client, locator: locator}
}

var _ NodeAgent = (*ClusterNodeAgent)(nil)

func (c *ClusterNodeAgent) ReadInterfaces(ctx context.Context, node string) (string, error) {
	if node == c.localNode() {
		return c.local.ReadInterfaces(ctx, node)
	}
	p, err := c.locator.Peer(ctx, node)
	if err != nil {
		return "", err
	}
	return c.client.Interfaces(ctx, p, node, false)
}

func (c *ClusterNodeAgent) StageInterfaces(ctx context.Context, node, content string) error {
	if node == c.localNode() {
		return c.local.StageInterfaces(ctx, node, content)
	}
	p, err := c.locator.Peer(ctx, node)
	if err != nil {
		return err
	}
	return c.client.StageInterfaces(ctx, p, node, content)
}

func (c *ClusterNodeAgent) ReloadInterfaces(ctx context.Context, node string) error {
	if node == c.localNode() {
		return c.local.ReloadInterfaces(ctx, node)
	}
	p, err := c.locator.Peer(ctx, node)
	if err != nil {
		return err
	}
	return c.client.Ifreload(ctx, p, node)
}

func (c *ClusterNodeAgent) DiscardStaged(ctx context.Context, node string) error {
	if node == c.localNode() {
		return c.local.DiscardStaged(ctx, node)
	}
	p, err := c.locator.Peer(ctx, node)
	if err != nil {
		return err
	}
	return c.client.DiscardStaged(ctx, p, node)
}

// ClusterTimerAgent implements NodeTimerAgent the same way ClusterNodeAgent
// implements NodeAgent: local node -> straight into this daemon's own
// *LocalTimerAgent (no network round trip to itself), any other node -> the
// peer API.
type ClusterTimerAgent struct {
	localNode func() string
	local     *LocalTimerAgent
	client    *peer.Client
	locator   PeerLocator
}

// NewClusterTimerAgent constructs a ClusterTimerAgent. See
// NewClusterNodeAgent's doc comment for why localNode is a resolver, not a
// fixed string.
func NewClusterTimerAgent(localNode func() string, local *LocalTimerAgent, client *peer.Client, locator PeerLocator) *ClusterTimerAgent {
	return &ClusterTimerAgent{localNode: localNode, local: local, client: client, locator: locator}
}

var _ NodeTimerAgent = (*ClusterTimerAgent)(nil)

func (c *ClusterTimerAgent) ArmTimer(ctx context.Context, changesetID, node, content string, deadline int64) (peer.TimerRecord, error) {
	if node == c.localNode() {
		return c.local.ArmTimer(ctx, changesetID, node, content, deadline)
	}
	p, err := c.locator.Peer(ctx, node)
	if err != nil {
		return peer.TimerRecord{}, err
	}
	return c.client.ArmTimer(ctx, p, changesetID, node, content, deadline)
}

func (c *ClusterTimerAgent) CancelTimer(ctx context.Context, changesetID, node string) (peer.TimerRecord, error) {
	if node == c.localNode() {
		return c.local.CancelTimer(ctx, changesetID, node)
	}
	p, err := c.locator.Peer(ctx, node)
	if err != nil {
		return peer.TimerRecord{}, err
	}
	return c.client.CancelTimer(ctx, p, changesetID, node)
}

func (c *ClusterTimerAgent) TimerStatus(ctx context.Context, changesetID, node string) (peer.TimerRecord, error) {
	if node == c.localNode() {
		return c.local.TimerStatus(ctx, changesetID, node)
	}
	p, err := c.locator.Peer(ctx, node)
	if err != nil {
		return peer.TimerRecord{}, err
	}
	return c.client.TimerStatus(ctx, p, changesetID, node)
}
