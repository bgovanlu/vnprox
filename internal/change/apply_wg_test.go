package change_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/wireguard"
)

// fakeWGGateway is a store-backed WGGateway double used by the WireGuard
// lifecycle/rollback tests (T-1401 AC6). It mirrors the production gateway's
// key custody exactly: a wg.tunnel.create generates the keypair on-node
// (GenerateKeypair), seals the private key with the same SessionCipher session
// tickets use, and stores only the sealed form + the derived public key — the
// plaintext private key never leaves this method or lands anywhere unsealed.
// It also tracks a fake on-node config per tunnel so a test can assert the
// node's config is torn down on rollback (no orphaned material on the node).
type fakeWGGateway struct {
	repo    *store.WireGuardRepo
	cipher  *store.SessionCipher
	configs map[string]string // tunnelID -> rendered on-node config
}

func newFakeWGGateway(t *testing.T, db *store.DB) *fakeWGGateway {
	t.Helper()
	key := make([]byte, store.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand key: %v", err)
	}
	cipher, err := store.NewSessionCipher(key)
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}
	return &fakeWGGateway{repo: store.NewWireGuardRepo(db), cipher: cipher, configs: map[string]string{}}
}

func (g *fakeWGGateway) ApplyWgOp(ctx context.Context, op change.Op) error {
	switch p := op.Params.(type) {
	case *change.WgTunnelCreateParams:
		priv, pub, err := wireguard.GenerateKeypair()
		if err != nil {
			return err
		}
		sealed, err := g.cipher.Encrypt([]byte(wireguard.EncodeKey(priv)))
		if err != nil {
			return err
		}
		tun := store.WireGuardTunnel{
			ID: op.Target.ID, Node: op.Target.Node, IfName: p.IfName,
			PrivateKeyEnc: sealed, PublicKey: wireguard.EncodeKey(pub),
			ListenPort: p.ListenPort, Addresses: p.Addresses, MTU: p.MTU, Carrier: p.Carrier,
			CreatedBy: "root@pam", CreatedAt: 1,
		}
		if err := g.repo.InsertTunnel(ctx, tun); err != nil {
			return err
		}
		// Render+"write" the on-node config from the just-decrypted key.
		g.configs[tun.ID] = wireguard.RenderConfig(wireguard.Tunnel{
			ID: tun.ID, Node: tun.Node, IfName: tun.IfName, PublicKey: tun.PublicKey,
			ListenPort: tun.ListenPort, Addresses: tun.Addresses, MTU: tun.MTU, Carrier: tun.Carrier,
		}, wireguard.EncodeKey(priv), nil)
		return nil
	case *change.WgTunnelDeleteParams:
		delete(g.configs, op.Target.ID)
		return g.repo.DeleteTunnel(ctx, op.Target.ID)
	case *change.WgPeerAddParams:
		tunnelID, _ := splitPeerTarget(op.Target.ID)
		return g.repo.AddPeer(ctx, store.WireGuardPeer{
			TunnelID: tunnelID, PublicKey: p.PublicKey, Endpoint: p.Endpoint,
			AllowedIPs: p.AllowedIPs, KeepaliveSec: p.KeepaliveSec, External: p.External, ClusterID: p.ClusterID,
		})
	case *change.WgPeerRemoveParams:
		tunnelID, _ := splitPeerTarget(op.Target.ID)
		return g.repo.RemovePeer(ctx, tunnelID, p.PublicKey)
	default:
		return fmt.Errorf("fakeWGGateway: unsupported op %s", op.Type)
	}
}

func splitPeerTarget(id string) (tunnelID, publicKey string) {
	if i := strings.IndexByte(id, '/'); i >= 0 {
		return id[:i], id[i+1:]
	}
	return id, ""
}

// wgSnapState is the opaque per-node snapshot fakeWGGateway serializes. The
// sealed private key travels verbatim (never decrypted), so a restored
// (rolled-back) delete restores the identical keypair.
type wgSnapState struct {
	Tunnels []store.WireGuardTunnel          `json:"tunnels"`
	Peers   map[string][]store.WireGuardPeer `json:"peers"`
}

func (g *fakeWGGateway) SnapshotWg(ctx context.Context, node string) (string, error) {
	tuns, err := g.repo.ListTunnels(ctx, node)
	if err != nil {
		return "", err
	}
	st := wgSnapState{Peers: map[string][]store.WireGuardPeer{}}
	for _, tun := range tuns {
		st.Tunnels = append(st.Tunnels, tun)
		peers, err := g.repo.ListPeers(ctx, tun.ID)
		if err != nil {
			return "", err
		}
		st.Peers[tun.ID] = peers
	}
	b, err := json.Marshal(st)
	return string(b), err
}

func (g *fakeWGGateway) RestoreWg(ctx context.Context, node, snapshot string) error {
	var want wgSnapState
	if err := json.Unmarshal([]byte(snapshot), &want); err != nil {
		return err
	}
	wantIDs := map[string]bool{}
	for _, tun := range want.Tunnels {
		wantIDs[tun.ID] = true
	}
	// Tear down live tunnels absent from the snapshot (rollback of a create).
	live, err := g.repo.ListTunnels(ctx, node)
	if err != nil {
		return err
	}
	for _, tun := range live {
		if !wantIDs[tun.ID] {
			delete(g.configs, tun.ID)
			if err := g.repo.DeleteTunnel(ctx, tun.ID); err != nil {
				return err
			}
		}
	}
	// Re-create snapshot tunnels missing live (rollback of a delete), with
	// their exact sealed key bytes.
	liveIDs := map[string]bool{}
	for _, tun := range live {
		liveIDs[tun.ID] = true
	}
	for _, tun := range want.Tunnels {
		if liveIDs[tun.ID] {
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
		g.configs[tun.ID] = "restored"
	}
	return nil
}

// TestApply_WgTunnelLifecycle_CreateConfirm is T-1401 AC6's happy path plus
// its key-custody safety-analysis assertions (AC1/AC2): a wg.tunnel.create
// flows through the ordinary stage→validate→apply→confirm lifecycle, the
// private key is stored only sealed and never surfaces in the changeset's
// apply log, and the stored public key is exactly the one derived from the
// generated private key.
func TestApply_WgTunnelLifecycle_CreateConfirm(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	gw := newFakeWGGateway(t, h.db)
	svc := newService(t, change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: h.timers.New, WG: gw,
	})
	ctx := context.Background()

	ops := opsFromJSON(t, `[{"op":"wg.tunnel.create","target":"wg-tunnel:pve1:tun1","params":{"ifName":"wg0","listenPort":51820,"addresses":["10.10.0.1/24"],"mtu":1420,"carrier":"vmbr9"}}]`)
	cs, err := svc.Create(ctx, "root@pam", "wg tunnel", ops)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Validate(ctx, cs.ID, "root@pam"); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	applied, err := svc.Apply(ctx, cs.ID, "root@pam", nil, 0)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status = %s, want awaiting_confirm", applied.Status)
	}

	// The tunnel and a real, sealed keypair now exist on the node.
	tun, err := gw.repo.GetTunnel(ctx, "tun1")
	if err != nil {
		t.Fatalf("GetTunnel: %v", err)
	}
	plaintext, err := gw.cipher.Decrypt(tun.PrivateKeyEnc)
	if err != nil {
		t.Fatalf("private key does not decrypt: %v", err)
	}
	rawPriv, err := wireguard.DecodeKey(string(plaintext))
	if err != nil {
		t.Fatalf("decoded private key invalid: %v", err)
	}
	derivedPub, _ := wireguard.PublicKeyFor(rawPriv)
	if wireguard.EncodeKey(derivedPub) != tun.PublicKey {
		t.Fatalf("stored public key is not derived from the stored private key")
	}

	// AC1: the private key must never appear in the changeset apply log.
	got, _ := svc.Get(ctx, cs.ID)
	if strings.Contains(string(got.ApplyLog), string(plaintext)) || strings.Contains(string(got.ApplyLog), wireguard.EncodeKey(rawPriv)) {
		t.Fatal("private key leaked into the changeset apply log")
	}

	confirmed, err := svc.Confirm(ctx, cs.ID, "root@pam")
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if confirmed.Status != change.StatusCommitted {
		t.Fatalf("status = %s, want committed", confirmed.Status)
	}
	if _, err := gw.repo.GetTunnel(ctx, "tun1"); err != nil {
		t.Fatalf("tunnel should survive a committed apply: %v", err)
	}
}

// TestApply_WgTunnelLifecycle_RollbackOnTimeout is T-1401 AC6's rollback path:
// a wg.tunnel.create that reaches awaiting_confirm and then times out
// un-confirmed is fully reverted on the unattended auto-rollback path — the
// tunnel, its generated keypair, and its on-node config are all gone, with no
// orphaned key material left in the store.
func TestApply_WgTunnelLifecycle_RollbackOnTimeout(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	gw := newFakeWGGateway(t, h.db)
	svc := newService(t, change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: h.timers.New, WG: gw,
	})
	ctx := context.Background()

	ops := opsFromJSON(t, `[{"op":"wg.tunnel.create","target":"wg-tunnel:pve1:tun1","params":{"ifName":"wg0","listenPort":51820,"addresses":["10.10.0.1/24"],"carrier":"vmbr9"}}]`)
	cs, err := svc.Create(ctx, "root@pam", "wg tunnel", ops)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := gw.repo.GetTunnel(ctx, "tun1"); err != nil {
		t.Fatalf("tunnel should exist after apply: %v", err)
	}

	// Deadline elapses with no confirm -> auto-rollback.
	h.timers.fireLatest(t)

	rolled, err := svc.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get after rollback: %v", err)
	}
	if rolled.Status != change.StatusRolledBack {
		t.Fatalf("status = %s, want rolled_back", rolled.Status)
	}

	// No orphaned key material or on-node config remains.
	if tuns, _ := gw.repo.ListTunnels(ctx, "pve1"); len(tuns) != 0 {
		t.Fatalf("tunnel row not removed on rollback: %d remain (orphaned key material)", len(tuns))
	}
	if len(gw.configs) != 0 {
		t.Fatalf("on-node config not torn down on rollback: %v", gw.configs)
	}
}
