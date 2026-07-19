-- 0017_ingress_targets.sql — T-1406 "Ingress visibility": app-owned,
-- operator-configured reverse-proxy discovery targets only. Per CLAUDE.md's
-- storage rule / docs/architecture.md §7's new-domain invariant, vnprox
-- persists intent + audit only here — a target's own live state (its
-- currently configured routes/backends) is never shadow-copied into this
-- table; GET /ingress/status calls internal/ingress.IngressDiscoverer fresh
-- against every row on every request.
--
-- Discovery iterates exactly this table — there is no network-range scan
-- anywhere in vnprox, and a target the operator never added here is never
-- contacted (T-1406 AC5).
--
-- `credential_enc` is AES-256-GCM ciphertext (nonce||ciphertext||tag), the
-- IDENTICAL session-secret encryption-at-rest primitive
-- `sessions.pve_ticket_enc` / `alert_rules.target_secret_enc` /
-- `wireguard_tunnels.private_key_enc` use — see internal/store/cipher.go's
-- SessionCipher, reused here, NOT a second cipher or key pair. NULL when a
-- target needs no credential (the common case: an operator-network-local
-- proxy's status endpoint with no auth in front of it).
--
-- Migrations are forward-only: once released, never edit this file; a
-- schema change lands as a new NNNN_*.sql with a higher version.

CREATE TABLE IF NOT EXISTS ingress_targets (
  id             TEXT PRIMARY KEY,   -- ULID
  kind           TEXT NOT NULL,      -- 'haproxy' | 'nginx' | 'caddy' | 'traefik'
  address        TEXT NOT NULL,      -- the target's own status/admin endpoint base URL
  credential_enc BLOB,               -- AES-256-GCM ciphertext, NULL when unused
  added_by       TEXT NOT NULL,
  added_at       INTEGER NOT NULL
);
