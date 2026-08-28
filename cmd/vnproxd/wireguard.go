// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/wireguard"
)

// wgConfDir is where the on-node wg-quick config files are written. It is an
// app-owned, root:root path; the tunnel's sealed private key is decrypted into
// the file just-in-time when it is (re)written and the file itself is 0600.
const wgConfDir = "/etc/wireguard"

// wgCipher is the subset of *store.SessionCipher the WireGuard gateway needs —
// the same AES-256-GCM session-secret cipher sessions.pve_ticket_enc uses
// (docs/security.md's WireGuard credential-storage note), never a second one.
type wgCipher interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(sealed []byte) ([]byte, error)
}

// hostWGGateway is the production change.WGGateway (T-1401): it generates a
// tunnel's keypair on the owning node, seals the private key, persists the
// intent to the local app store, writes the on-node wg-quick config, and execs
// wg/wg-quick with a fixed argv array (no dynamic shell interpolation),
// mirroring the ifreload/lldpctl subprocess convention (docs/security.md's Host
// footprint section). WireGuard's own live state stays authoritative; this
// gateway never shadow-copies it.
//
// NEEDS HARDWARE VALIDATION: the exec paths (wg-quick up/down, wg syncconf)
// touch a real kernel WireGuard module and are exercised only by wireguard_test
// .go's injected no-op runners, never against a live wg. Peer-node wg apply is
// not yet routed (a wg op targeting another node errors clearly) — the same
// single-node scope hostNodeAgent documents until cluster wg routing lands.
type hostWGGateway struct {
	cipher     wgCipher
	repo       *store.WireGuardRepo
	localNode  func() string
	log        *slog.Logger
	writeFile  func(path string, content string) error
	removeFile func(path string) error
	syncTunnel func(ctx context.Context, ifName, confPath string) error
	downTunnel func(ctx context.Context, ifName, confPath string) error
	confDir    string
}

var _ change.WGGateway = (*hostWGGateway)(nil)

func newHostWGGateway(repo *store.WireGuardRepo, cipher wgCipher, localNode func() string, logger *slog.Logger) *hostWGGateway {
	g := &hostWGGateway{repo: repo, cipher: cipher, localNode: localNode, log: logger, confDir: wgConfDir}
	g.writeFile = func(path, content string) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(content), 0o600)
	}
	g.removeFile = func(path string) error {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	g.syncTunnel = func(ctx context.Context, ifName, confPath string) error {
		// wg-quick up brings a down interface up; if already up, wg syncconf
		// applies the new config without dropping the tunnel. Fixed argv.
		if err := exec.CommandContext(ctx, "wg-quick", "up", ifName).Run(); err != nil {
			// Already up: reconcile in place.
			strip := exec.CommandContext(ctx, "wg-quick", "strip", confPath)
			out, sErr := strip.Output()
			if sErr != nil {
				return fmt.Errorf("wg-quick up %s and strip fallback both failed: %w", ifName, err)
			}
			sync := exec.CommandContext(ctx, "wg", "syncconf", ifName, "/dev/stdin")
			sync.Stdin = strings.NewReader(string(out))
			if syncErr := sync.Run(); syncErr != nil {
				return fmt.Errorf("wg syncconf %s: %w", ifName, syncErr)
			}
		}
		return nil
	}
	g.downTunnel = func(ctx context.Context, ifName, _ string) error {
		if err := exec.CommandContext(ctx, "wg-quick", "down", ifName).Run(); err != nil {
			g.log.Warn("wireguard: wg-quick down failed (interface may already be down)", "if", ifName, "error", err)
		}
		return nil
	}
	return g
}

func (g *hostWGGateway) confPath(ifName string) string {
	return filepath.Join(g.confDir, ifName+".conf")
}

func (g *hostWGGateway) ensureLocal(node string) error {
	if g.localNode == nil {
		return nil
	}
	if local := g.localNode(); local != "" && node != local {
		return fmt.Errorf("wireguard: cannot apply a wg op for peer node %q from %q — cluster wg routing is not yet implemented (needs a follow-up task)", node, local)
	}
	return nil
}

// ApplyWgOp implements change.WGGateway.
func (g *hostWGGateway) ApplyWgOp(ctx context.Context, op change.Op) error {
	if err := g.ensureLocal(op.Target.Node); err != nil {
		return err
	}
	switch p := op.Params.(type) {
	case *change.WgTunnelCreateParams:
		return g.createTunnel(ctx, op, p)
	case *change.WgTunnelUpdateParams:
		return g.updateTunnel(ctx, op, p)
	case *change.WgTunnelDeleteParams:
		return g.deleteTunnel(ctx, op)
	case *change.WgPeerAddParams:
		return g.addPeer(ctx, op, p)
	case *change.WgPeerRemoveParams:
		return g.removePeer(ctx, op, p)
	default:
		return fmt.Errorf("wireguard: unsupported op %s", op.Type)
	}
}

func (g *hostWGGateway) createTunnel(ctx context.Context, op change.Op, p *change.WgTunnelCreateParams) error {
	priv, pub, err := wireguard.GenerateKeypair()
	if err != nil {
		return err
	}
	privB64 := wireguard.EncodeKey(priv)
	sealed, err := g.cipher.Encrypt([]byte(privB64))
	if err != nil {
		return fmt.Errorf("wireguard: sealing private key: %w", err)
	}
	tun := store.WireGuardTunnel{
		ID: op.Target.ID, Node: op.Target.Node, IfName: p.IfName,
		PrivateKeyEnc: sealed, PublicKey: wireguard.EncodeKey(pub),
		ListenPort: p.ListenPort, Addresses: p.Addresses, MTU: p.MTU, Carrier: p.Carrier,
		CreatedBy: op.Target.Node, CreatedAt: time.Now().Unix(),
	}
	if err := g.repo.InsertTunnel(ctx, tun); err != nil {
		return err
	}
	// privB64 is the ONLY plaintext copy of the private key past this point;
	// it lives on the stack, is written once into the 0600 config, and is
	// never returned, logged, or stored unsealed.
	return g.renderAndSync(ctx, tun, privB64)
}

func (g *hostWGGateway) updateTunnel(ctx context.Context, op change.Op, p *change.WgTunnelUpdateParams) error {
	tun, err := g.repo.GetTunnel(ctx, op.Target.ID)
	if err != nil {
		return err
	}
	if p.ListenPort != nil {
		tun.ListenPort = *p.ListenPort
	}
	if p.Addresses != nil {
		tun.Addresses = *p.Addresses
	}
	if p.MTU != nil {
		tun.MTU = *p.MTU
	}
	if p.Carrier != nil {
		tun.Carrier = *p.Carrier
	}
	if err := g.repo.UpdateTunnel(ctx, tun); err != nil {
		return err
	}
	return g.rerender(ctx, tun)
}

func (g *hostWGGateway) deleteTunnel(ctx context.Context, op change.Op) error {
	tun, err := g.repo.GetTunnel(ctx, op.Target.ID)
	if err != nil {
		return err
	}
	_ = g.downTunnel(ctx, tun.IfName, g.confPath(tun.IfName))
	if err := g.removeFile(g.confPath(tun.IfName)); err != nil {
		return err
	}
	return g.repo.DeleteTunnel(ctx, op.Target.ID)
}

func (g *hostWGGateway) addPeer(ctx context.Context, op change.Op, p *change.WgPeerAddParams) error {
	tunnelID, _ := splitWgPeerTarget(op.Target.ID)
	psk, err := g.peerPresharedKey(p)
	if err != nil {
		return err
	}
	var sealed []byte
	if psk != "" {
		if sealed, err = g.cipher.Encrypt([]byte(psk)); err != nil {
			return fmt.Errorf("wireguard: sealing preshared key: %w", err)
		}
	}
	if err = g.repo.AddPeer(ctx, store.WireGuardPeer{
		TunnelID: tunnelID, PublicKey: p.PublicKey, Endpoint: p.Endpoint, AllowedIPs: p.AllowedIPs,
		PresharedKeyEnc: sealed, KeepaliveSec: p.KeepaliveSec, External: p.External, ClusterID: p.ClusterID,
	}); err != nil {
		return err
	}
	tun, err := g.repo.GetTunnel(ctx, tunnelID)
	if err != nil {
		return err
	}
	return g.rerender(ctx, tun)
}

func (g *hostWGGateway) removePeer(ctx context.Context, op change.Op, p *change.WgPeerRemoveParams) error {
	tunnelID, _ := splitWgPeerTarget(op.Target.ID)
	if err := g.repo.RemovePeer(ctx, tunnelID, p.PublicKey); err != nil {
		return err
	}
	tun, err := g.repo.GetTunnel(ctx, tunnelID)
	if err != nil {
		return err
	}
	return g.rerender(ctx, tun)
}

// rerender re-decrypts the tunnel's private key, re-renders the config, and
// syncs — used when peers or tunnel attributes change.
func (g *hostWGGateway) rerender(ctx context.Context, tun store.WireGuardTunnel) error {
	privB64, err := g.unsealKey(tun)
	if err != nil {
		return err
	}
	return g.renderAndSync(ctx, tun, privB64)
}

// peerPresharedKey returns the plaintext preshared key for a wg.peer.add op.
// The change engine seals the PSK into WgPeerAddParams.PresharedKeyEnc at
// stage/create time (change.Service.sealOpSecrets) so it never rides
// changesets.ops_json or a changeset read response in the clear (Finding 1); on
// the owning node here it is unsealed just-in-time, then re-sealed into
// wireguard_peers.preshared_key_enc exactly as before. A hand-built op carrying
// the plaintext directly (an op that never passed through the stage path) is
// still honored.
func (g *hostWGGateway) peerPresharedKey(p *change.WgPeerAddParams) (string, error) {
	if len(p.PresharedKeyEnc) > 0 {
		plain, err := g.cipher.Decrypt(p.PresharedKeyEnc)
		if err != nil {
			return "", fmt.Errorf("wireguard: unsealing preshared key from op: %w", err)
		}
		return string(plain), nil
	}
	return p.PresharedKey, nil
}

func (g *hostWGGateway) unsealKey(tun store.WireGuardTunnel) (string, error) {
	plaintext, err := g.cipher.Decrypt(tun.PrivateKeyEnc)
	if err != nil {
		return "", fmt.Errorf("wireguard: unsealing private key for tunnel %s: %w", tun.ID, err)
	}
	return string(plaintext), nil
}

func (g *hostWGGateway) renderAndSync(ctx context.Context, tun store.WireGuardTunnel, privB64 string) error {
	peers, err := g.repo.ListPeers(ctx, tun.ID)
	if err != nil {
		return err
	}
	cfg := wireguard.RenderConfig(storeTunnelToModel(tun), privB64, storePeersToModel(peers))
	path := g.confPath(tun.IfName)
	if err := g.writeFile(path, cfg); err != nil {
		return fmt.Errorf("wireguard: writing config %s: %w", path, err)
	}
	return g.syncTunnel(ctx, tun.IfName, path)
}

// SnapshotWg implements change.WGGateway: serialize the node's tunnels + peers
// (sealed keys verbatim, never decrypted) so a rollback can converge back.
func (g *hostWGGateway) SnapshotWg(ctx context.Context, node string) (string, error) {
	tuns, err := g.repo.ListTunnels(ctx, node)
	if err != nil {
		return "", err
	}
	snap := wgNodeSnapshot{Peers: map[string][]store.WireGuardPeer{}}
	for _, tun := range tuns {
		snap.Tunnels = append(snap.Tunnels, tun)
		peers, listErr := g.repo.ListPeers(ctx, tun.ID)
		if listErr != nil {
			return "", listErr
		}
		snap.Peers[tun.ID] = peers
	}
	b, err := json.Marshal(snap)
	return string(b), err
}

// RestoreWg implements change.WGGateway: reconcile the node's WireGuard state
// back to a SnapshotWg output — tear down tunnels absent from the snapshot,
// re-create ones present in it but missing live (from their exact sealed key
// bytes, so a rolled-back delete restores the identical keypair). Callable
// unattended (no user ticket).
func (g *hostWGGateway) RestoreWg(ctx context.Context, node, snapshot string) error {
	if err := g.ensureLocal(node); err != nil {
		return err
	}
	var want wgNodeSnapshot
	if err := json.Unmarshal([]byte(snapshot), &want); err != nil {
		return err
	}
	wantIDs := map[string]bool{}
	for _, t := range want.Tunnels {
		wantIDs[t.ID] = true
	}
	live, err := g.repo.ListTunnels(ctx, node)
	if err != nil {
		return err
	}
	liveIDs := map[string]bool{}
	for _, tun := range live {
		liveIDs[tun.ID] = true
		if !wantIDs[tun.ID] {
			_ = g.downTunnel(ctx, tun.IfName, g.confPath(tun.IfName))
			if rmErr := g.removeFile(g.confPath(tun.IfName)); rmErr != nil {
				return rmErr
			}
			if delErr := g.repo.DeleteTunnel(ctx, tun.ID); delErr != nil {
				return delErr
			}
		}
	}
	for _, tun := range want.Tunnels {
		if liveIDs[tun.ID] {
			// Present in both — re-sync in case its peers/attributes changed.
			if err := g.reconcilePeers(ctx, tun, want.Peers[tun.ID]); err != nil {
				return err
			}
			if err := g.rerender(ctx, tun); err != nil {
				return err
			}
			continue
		}
		if err := g.repo.InsertTunnel(ctx, tun); err != nil {
			return err
		}
		for _, peer := range want.Peers[tun.ID] {
			if err := g.repo.AddPeer(ctx, peer); err != nil {
				return err
			}
		}
		if err := g.rerender(ctx, tun); err != nil {
			return err
		}
	}
	return nil
}

// reconcilePeers makes tun's live peer set match want (add missing, remove
// extra) for the restore-in-place case.
func (g *hostWGGateway) reconcilePeers(ctx context.Context, tun store.WireGuardTunnel, want []store.WireGuardPeer) error {
	livePeers, err := g.repo.ListPeers(ctx, tun.ID)
	if err != nil {
		return err
	}
	wantKeys := map[string]bool{}
	for _, p := range want {
		wantKeys[p.PublicKey] = true
		if err := g.repo.AddPeer(ctx, p); err != nil {
			return err
		}
	}
	for _, p := range livePeers {
		if !wantKeys[p.PublicKey] {
			if err := g.repo.RemovePeer(ctx, tun.ID, p.PublicKey); err != nil {
				return err
			}
		}
	}
	return nil
}

// wgNodeSnapshot is the opaque per-node snapshot SnapshotWg/RestoreWg exchange.
type wgNodeSnapshot struct {
	Peers   map[string][]store.WireGuardPeer `json:"peers"`
	Tunnels []store.WireGuardTunnel          `json:"tunnels"`
}

func splitWgPeerTarget(id string) (tunnelID, publicKey string) {
	if i := strings.IndexByte(id, '/'); i >= 0 {
		return id[:i], id[i+1:]
	}
	return id, ""
}

func storeTunnelToModel(t store.WireGuardTunnel) wireguard.Tunnel {
	return wireguard.Tunnel{
		ID: t.ID, Node: t.Node, IfName: t.IfName, PublicKey: t.PublicKey,
		ListenPort: t.ListenPort, Addresses: t.Addresses, MTU: t.MTU, Carrier: t.Carrier,
		CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt,
	}
}

func storePeersToModel(peers []store.WireGuardPeer) []wireguard.Peer {
	out := make([]wireguard.Peer, 0, len(peers))
	for _, p := range peers {
		out = append(out, wireguard.Peer{
			TunnelID: p.TunnelID, PublicKey: p.PublicKey, Endpoint: p.Endpoint,
			AllowedIPs: p.AllowedIPs, KeepaliveSec: p.KeepaliveSec, External: p.External, ClusterID: p.ClusterID,
		})
	}
	return out
}

// --- read service (api.WireGuardService + findings.WGProvider) --------------

// wireGuardReadService serves the read-only WireGuard surface: the app-owned
// config from the store merged with WireGuard's live authoritative state
// (a `wg show <if> dump` poll). It never returns a private key. It reads this
// daemon's own node's store; cluster-wide fan-out is a documented follow-up
// (like the corosync check).
type wireGuardReadService struct {
	repo      *store.WireGuardRepo
	localNode func() string
	log       *slog.Logger
	// dump is `wg show <ifName> dump`, injectable/absent in dev (a nil or
	// erroring dump yields config-only status, never a failure).
	dump func(ctx context.Context, ifName string) (string, error)
}

var (
	_ api.WireGuardService   = (*wireGuardReadService)(nil)
	_ change.WgCarrierSource = (*wireGuardReadService)(nil)
	_ interface {
		WireGuardState() []wireguard.ObservedTunnel
	} = (*wireGuardReadService)(nil)
)

func newWireGuardReadService(repo *store.WireGuardRepo, localNode func() string, logger *slog.Logger) *wireGuardReadService {
	return &wireGuardReadService{
		repo: repo, localNode: localNode, log: logger,
		dump: func(ctx context.Context, ifName string) (string, error) {
			out, err := exec.CommandContext(ctx, "wg", "show", ifName, "dump").Output()
			return string(out), err
		},
	}
}

func (s *wireGuardReadService) observed(ctx context.Context, tun store.WireGuardTunnel) (wireguard.ObservedTunnel, bool) {
	if s.dump == nil {
		return wireguard.ObservedTunnel{}, false
	}
	raw, err := s.dump(ctx, tun.IfName)
	if err != nil {
		return wireguard.ObservedTunnel{}, false
	}
	obs, err := wireguard.ParseDump(tun.Node, tun.IfName, raw)
	if err != nil {
		s.log.Warn("wireguard: parsing wg dump", "if", tun.IfName, "error", err)
		return wireguard.ObservedTunnel{}, false
	}
	return obs, true
}

func (s *wireGuardReadService) Tunnels(ctx context.Context) ([]api.WireGuardTunnelView, error) {
	tuns, err := s.repo.ListTunnels(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]api.WireGuardTunnelView, 0, len(tuns))
	for _, tun := range tuns {
		peers, err := s.repo.ListPeers(ctx, tun.ID)
		if err != nil {
			return nil, err
		}
		obs, up := s.observed(ctx, tun)
		byKey := map[string]wireguard.ObservedPeer{}
		for _, op := range obs.Peers {
			byKey[op.PublicKey] = op
		}
		view := api.WireGuardTunnelView{
			ID: tun.ID, Node: tun.Node, IfName: tun.IfName, PublicKey: tun.PublicKey,
			ListenPort: tun.ListenPort, Addresses: tun.Addresses, MTU: tun.MTU, Carrier: tun.Carrier,
			Status: api.WireGuardTunnelStatus{InterfaceUp: up, PeerCount: len(peers)},
		}
		for _, p := range peers {
			pv := api.WireGuardPeerView{
				PublicKey: p.PublicKey, Endpoint: p.Endpoint, AllowedIPs: p.AllowedIPs,
				KeepaliveSec: p.KeepaliveSec, External: p.External,
			}
			if o, ok := byKey[p.PublicKey]; ok {
				pv.ObservedEndpoint = o.Endpoint
				if !o.LastHandshake.IsZero() {
					pv.LastHandshakeUnix = o.LastHandshake.Unix()
				}
				pv.RxBytes, pv.TxBytes = o.RxBytes, o.TxBytes
				pv.EndpointDrifted = p.Endpoint != "" && o.Endpoint != "" && p.Endpoint != o.Endpoint
			}
			view.Peers = append(view.Peers, pv)
		}
		if view.Peers == nil {
			view.Peers = []api.WireGuardPeerView{}
		}
		out = append(out, view)
	}
	return out, nil
}

// TunnelCarriers implements change.WgCarrierSource: the tunnelID->carrier map
// TouchesMgmtPath needs to flag carrier-less wg ops (wg.peer.*,
// wg.tunnel.delete, carrier-less wg.tunnel.update) on an existing
// management-path tunnel (Finding 2 / the mgmt-path interlock). Reads only the
// store config (no live wg poll) — the carrier is app-owned intent, not live
// state. Lists this daemon's own node's store, the same single-node scope the
// rest of this read service documents.
func (s *wireGuardReadService) TunnelCarriers(ctx context.Context) (map[string]change.WgTunnelCarrier, error) {
	tuns, err := s.repo.ListTunnels(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string]change.WgTunnelCarrier, len(tuns))
	for _, t := range tuns {
		out[t.ID] = change.WgTunnelCarrier{Node: t.Node, Carrier: t.Carrier}
	}
	return out, nil
}

func (s *wireGuardReadService) PublicKey(ctx context.Context, id string) (string, error) {
	tun, err := s.repo.GetTunnel(ctx, id)
	if err != nil {
		return "", api.ErrWireGuardNotFound
	}
	return tun.PublicKey, nil
}

func (s *wireGuardReadService) PeerConfig(ctx context.Context, id string) (string, error) {
	tun, err := s.repo.GetTunnel(ctx, id)
	if err != nil {
		return "", api.ErrWireGuardNotFound
	}
	// A generic own-side template a new external peer installs to connect to
	// this tunnel: our public key + endpoint, with the peer's own private key
	// and address left as placeholders (vnprox never holds an external peer's
	// key — the residual-risk note). endpointHost uses the node name; a real
	// deployment substitutes the node's reachable public address.
	endpoint := fmt.Sprintf("%s:%d", tun.Node, tun.ListenPort)
	return wireguard.RenderPeerConfig(storeTunnelToModel(tun), wireguard.Peer{}, endpoint), nil
}

func (s *wireGuardReadService) WireGuardState() []wireguard.ObservedTunnel {
	ctx := context.Background()
	tuns, err := s.repo.ListTunnels(ctx, "")
	if err != nil {
		s.log.Warn("wireguard: listing tunnels for findings", "error", err)
		return nil
	}
	var out []wireguard.ObservedTunnel
	for _, tun := range tuns {
		obs, ok := s.observed(ctx, tun)
		if !ok {
			continue
		}
		peers, err := s.repo.ListPeers(ctx, tun.ID)
		if err != nil {
			continue
		}
		configured := map[string]store.WireGuardPeer{}
		for _, p := range peers {
			configured[p.PublicKey] = p
		}
		for i := range obs.Peers {
			if cp, ok := configured[obs.Peers[i].PublicKey]; ok {
				obs.Peers[i].ConfiguredEnd = cp.Endpoint
				obs.Peers[i].ConfiguredExtern = cp.External
			}
		}
		out = append(out, obs)
	}
	return out
}
