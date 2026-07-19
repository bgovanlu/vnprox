-- 0016_wireguard.sql — T-1401 "WireGuard tunnel engine": app-owned tunnel and
-- peer intent. Per CLAUDE.md's storage rule / docs/architecture.md §7's
-- new-domain invariant, vnprox persists intent + audit only here — WireGuard's
-- own on-node state (the live interface, handshake ages, transfer counters,
-- the endpoint a peer is actually reaching us from) stays authoritative and is
-- never shadow-copied into these tables.
--
-- `wireguard_tunnels.private_key_enc` is AES-256-GCM ciphertext
-- (nonce||ciphertext||tag), the IDENTICAL session-secret encryption-at-rest
-- primitive sessions.pve_ticket_enc / alert_rules.target_secret_enc use — see
-- internal/store/cipher.go's SessionCipher, reused here, NOT a second cipher
-- or key pair (docs/security.md's WireGuard credential-storage note). The
-- private key is generated on the owning node and written once, sealed; only
-- the derived `public_key` (plaintext) is ever returned by an API response.
-- A tunnel's key is never regenerated in place — key rotation is delete +
-- recreate, always two ordinary audited changeset ops.
--
-- `wireguard_peers.preshared_key_enc` is the same sealed form when a peer uses
-- an optional preshared key, NULL otherwise. `external` marks a peer vnprox
-- does not own (a road-warrior, or a cluster vnprox does not manage): modeled
-- read-only, config-export-only, never targeted by an apply step of vnprox's
-- own. `cluster_id` links a federation-managed internal peer (the T-1201 seam,
-- not yet in this repo).
--
-- Migrations are forward-only: once released, never edit this file; a schema
-- change lands as a new NNNN_*.sql with a higher version.

CREATE TABLE IF NOT EXISTS wireguard_tunnels (
  id              TEXT PRIMARY KEY,   -- ULID
  node            TEXT NOT NULL,      -- owning PVE node
  if_name         TEXT NOT NULL,      -- e.g. "wg0"
  private_key_enc BLOB NOT NULL,      -- AES-256-GCM ciphertext, never returned by any API
  public_key      TEXT NOT NULL,      -- base64, the exportable half
  listen_port     INTEGER NOT NULL DEFAULT 0,
  addresses_json  TEXT NOT NULL DEFAULT '[]',  -- JSON []string of this interface's own CIDRs
  mtu             INTEGER NOT NULL DEFAULT 0,
  carrier         TEXT NOT NULL DEFAULT '',     -- underlying iface the endpoint rides on (mgmt-path interlock input)
  created_by      TEXT NOT NULL,
  created_at      INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_wireguard_tunnels_node_if ON wireguard_tunnels (node, if_name);

CREATE TABLE IF NOT EXISTS wireguard_peers (
  tunnel_id         TEXT NOT NULL REFERENCES wireguard_tunnels(id) ON DELETE CASCADE,
  public_key        TEXT NOT NULL,    -- the peer's own public key, its identity within the tunnel
  endpoint          TEXT NOT NULL DEFAULT '',
  allowed_ips_json  TEXT NOT NULL DEFAULT '[]',
  preshared_key_enc BLOB,             -- AES-256-GCM ciphertext, NULL when unused
  keepalive_sec     INTEGER NOT NULL DEFAULT 0,
  external          INTEGER NOT NULL DEFAULT 0,  -- 0|1
  cluster_id        TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (tunnel_id, public_key)
);
