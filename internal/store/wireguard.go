// wireguard.go implements T-1401's WireGuard tunnel/peer storage
// (docs/data-model.md §2, migration 0016_wireguard.sql). App-owned intent +
// audit only per CLAUDE.md's storage rule. PrivateKeyEnc / PresharedKeyEnc are
// AES-256-GCM ciphertext (nonce||ciphertext||tag, see cipher.go's
// SessionCipher) — this repository stores/returns the opaque sealed bytes only;
// internal/api and cmd/vnproxd's WGGateway own sealing/unsealing, exactly like
// AlertRuleRepo does for target_secret_enc. The private key is never returned
// by any API response, log line, or audit detail (docs/security.md's WireGuard
// credential-storage note).

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// WireGuardTunnel is one row of the wireguard_tunnels table.
type WireGuardTunnel struct {
	ID            string
	Node          string
	IfName        string
	PublicKey     string
	Carrier       string
	CreatedBy     string
	PrivateKeyEnc []byte
	Addresses     []string
	ListenPort    int
	MTU           int
	CreatedAt     int64
}

// WireGuardPeer is one row of the wireguard_peers table.
type WireGuardPeer struct {
	TunnelID        string
	PublicKey       string
	Endpoint        string
	ClusterID       string
	AllowedIPs      []string
	PresharedKeyEnc []byte
	KeepaliveSec    int
	External        bool
}

// WireGuardRepo is the wireguard_tunnels / wireguard_peers repository.
type WireGuardRepo struct {
	db *DB
}

// NewWireGuardRepo constructs a WireGuardRepo.
func NewWireGuardRepo(db *DB) *WireGuardRepo { return &WireGuardRepo{db: db} }

func marshalStrings(ss []string) (string, error) {
	if ss == nil {
		ss = []string{}
	}
	b, err := json.Marshal(ss)
	if err != nil {
		return "", fmt.Errorf("store: marshaling string slice: %w", err)
	}
	return string(b), nil
}

func unmarshalStrings(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("store: unmarshaling string slice %q: %w", raw, err)
	}
	return out, nil
}

// InsertTunnel creates a new wireguard_tunnels row. ID is caller-assigned
// (typically store.NewULID()).
func (r *WireGuardRepo) InsertTunnel(ctx context.Context, t WireGuardTunnel) error {
	addrsJSON, err := marshalStrings(t.Addresses)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO wireguard_tunnels
			(id, node, if_name, private_key_enc, public_key, listen_port, addresses_json, mtu, carrier, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Node, t.IfName, t.PrivateKeyEnc, t.PublicKey, t.ListenPort, addrsJSON, t.MTU, t.Carrier, t.CreatedBy, t.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting wireguard tunnel %s: %w", t.ID, err)
	}
	return nil
}

// GetTunnel returns one tunnel by id, or ErrNotFound.
func (r *WireGuardRepo) GetTunnel(ctx context.Context, id string) (WireGuardTunnel, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, node, if_name, private_key_enc, public_key, listen_port, addresses_json, mtu, carrier, created_by, created_at
		FROM wireguard_tunnels WHERE id = ?`, id)
	t, err := scanTunnel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return WireGuardTunnel{}, ErrNotFound
	}
	return t, err
}

// ListTunnels returns every tunnel, or every tunnel on one node when node is
// non-empty, ordered by node then if_name for a stable listing.
func (r *WireGuardRepo) ListTunnels(ctx context.Context, node string) ([]WireGuardTunnel, error) {
	q := `SELECT id, node, if_name, private_key_enc, public_key, listen_port, addresses_json, mtu, carrier, created_by, created_at
		FROM wireguard_tunnels`
	var args []any
	if node != "" {
		q += ` WHERE node = ?`
		args = append(args, node)
	}
	q += ` ORDER BY node ASC, if_name ASC`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listing wireguard tunnels: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []WireGuardTunnel
	for rows.Next() {
		t, err := scanTunnel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing wireguard tunnels: %w", err)
	}
	return out, nil
}

// DeleteTunnel removes a tunnel by id (cascading to its peers). It is not an
// error to delete an already-absent one — rollback of a create must converge
// even if a prior step already removed it.
func (r *WireGuardRepo) DeleteTunnel(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM wireguard_tunnels WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting wireguard tunnel %s: %w", id, err)
	}
	return nil
}

// UpdateTunnel overwrites a tunnel's mutable fields (never its keypair — key
// rotation is delete+recreate, per the migration doc comment). Returns
// ErrNotFound if the tunnel doesn't exist.
func (r *WireGuardRepo) UpdateTunnel(ctx context.Context, t WireGuardTunnel) error {
	addrsJSON, err := marshalStrings(t.Addresses)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE wireguard_tunnels SET
			listen_port = ?, addresses_json = ?, mtu = ?, carrier = ?
		WHERE id = ?`,
		t.ListenPort, addrsJSON, t.MTU, t.Carrier, t.ID,
	)
	if err != nil {
		return fmt.Errorf("store: updating wireguard tunnel %s: %w", t.ID, err)
	}
	return checkRowAffected(res, "store: updating wireguard tunnel %s", t.ID)
}

// AddPeer inserts or replaces a peer (keyed by tunnel_id + public_key).
func (r *WireGuardRepo) AddPeer(ctx context.Context, p WireGuardPeer) error {
	ipsJSON, err := marshalStrings(p.AllowedIPs)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO wireguard_peers
			(tunnel_id, public_key, endpoint, allowed_ips_json, preshared_key_enc, keepalive_sec, external, cluster_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tunnel_id, public_key) DO UPDATE SET
			endpoint = excluded.endpoint, allowed_ips_json = excluded.allowed_ips_json,
			preshared_key_enc = excluded.preshared_key_enc, keepalive_sec = excluded.keepalive_sec,
			external = excluded.external, cluster_id = excluded.cluster_id`,
		p.TunnelID, p.PublicKey, p.Endpoint, ipsJSON, p.PresharedKeyEnc, p.KeepaliveSec, boolToInt(p.External), p.ClusterID,
	)
	if err != nil {
		return fmt.Errorf("store: adding wireguard peer %s to tunnel %s: %w", p.PublicKey, p.TunnelID, err)
	}
	return nil
}

// RemovePeer deletes one peer by (tunnel_id, public_key). Not an error if
// absent (rollback convergence).
func (r *WireGuardRepo) RemovePeer(ctx context.Context, tunnelID, publicKey string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM wireguard_peers WHERE tunnel_id = ? AND public_key = ?`, tunnelID, publicKey); err != nil {
		return fmt.Errorf("store: removing wireguard peer %s from tunnel %s: %w", publicKey, tunnelID, err)
	}
	return nil
}

// ListPeers returns every peer of a tunnel, ordered by public_key.
func (r *WireGuardRepo) ListPeers(ctx context.Context, tunnelID string) ([]WireGuardPeer, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT tunnel_id, public_key, endpoint, allowed_ips_json, preshared_key_enc, keepalive_sec, external, cluster_id
		FROM wireguard_peers WHERE tunnel_id = ? ORDER BY public_key ASC`, tunnelID)
	if err != nil {
		return nil, fmt.Errorf("store: listing wireguard peers for tunnel %s: %w", tunnelID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []WireGuardPeer
	for rows.Next() {
		var p WireGuardPeer
		var ipsJSON string
		var external int
		if scanErr := rows.Scan(&p.TunnelID, &p.PublicKey, &p.Endpoint, &ipsJSON, &p.PresharedKeyEnc, &p.KeepaliveSec, &external, &p.ClusterID); scanErr != nil {
			return nil, fmt.Errorf("store: scanning wireguard peer: %w", scanErr)
		}
		p.External = external != 0
		if p.AllowedIPs, err = unmarshalStrings(ipsJSON); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing wireguard peers for tunnel %s: %w", tunnelID, err)
	}
	return out, nil
}

// TunnelIDForCluster returns the id of the tunnel carrying a peer annotated as
// federation cluster clusterID (`wireguard_peers.cluster_id`) — the peer-level
// half of the tunnel<->cluster linkage whose cluster-level half is
// `clusters.wg_tunnel_id`. `internal/federation.Service` uses this to derive a
// cluster's effective linkage when no explicit `wg_tunnel_id` override is
// stored, so the two columns can never disagree about which tunnel a federated
// cluster is reached over (see docs/data-model.md's `clusters` entry).
//
// Returns ("", nil) — not an error — when no peer carries the annotation: "not
// tunnel-linked" is an ordinary state, not a failure. An empty clusterID never
// matches, since ” is also the default for untagged peers. When more than one
// tunnel carries a peer for the same cluster (an operator having tagged the
// same far side on two tunnels, e.g. mid-migration), the lowest tunnel id wins
// — arbitrary but deterministic, so the derived linkage never flaps between
// reads; an operator who needs the other one sets the explicit override.
func (r *WireGuardRepo) TunnelIDForCluster(ctx context.Context, clusterID string) (string, error) {
	if clusterID == "" {
		return "", nil
	}
	var tunnelID string
	err := r.db.QueryRowContext(ctx, `
		SELECT tunnel_id FROM wireguard_peers
		WHERE cluster_id = ? ORDER BY tunnel_id ASC LIMIT 1`, clusterID).Scan(&tunnelID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: resolving wireguard tunnel for cluster %s: %w", clusterID, err)
	}
	return tunnelID, nil
}

func scanTunnel(row rowScanner) (WireGuardTunnel, error) {
	var t WireGuardTunnel
	var addrsJSON string
	if err := row.Scan(&t.ID, &t.Node, &t.IfName, &t.PrivateKeyEnc, &t.PublicKey, &t.ListenPort, &addrsJSON, &t.MTU, &t.Carrier, &t.CreatedBy, &t.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WireGuardTunnel{}, err
		}
		return WireGuardTunnel{}, fmt.Errorf("store: scanning wireguard tunnel: %w", err)
	}
	var err error
	if t.Addresses, err = unmarshalStrings(addrsJSON); err != nil {
		return WireGuardTunnel{}, err
	}
	return t, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
