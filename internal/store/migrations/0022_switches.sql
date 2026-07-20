-- 0022_switches.sql — T-1205 "guarded switch config push": app-owned registry
-- of the physical switches vnprox may push port configuration to. Per CLAUDE.md's
-- storage rule / docs/architecture.md's new-domain invariant, vnprox persists
-- only app-owned intent + credentials here — a switch's live port/VLAN/LACP
-- state stays authoritative on the switch itself and is read through the
-- SwitchDriver (internal/switchdrv), never shadow-copied into this table.
--
-- `credentials_enc` is AES-256-GCM ciphertext (nonce||ciphertext||tag), the
-- IDENTICAL session-secret encryption-at-rest primitive sessions.pve_ticket_enc /
-- alert_rules.target_secret_enc use — internal/store/cipher.go's SessionCipher,
-- reused here, NOT a second cipher or key (docs/security.md's switch
-- credential-storage note). This repository stores/returns the opaque sealed
-- bytes only; internal/api and cmd/vnproxd own sealing/unsealing.
--
-- `enabled` defaults to 0 (false): switch push is per-switch explicit opt-in.
-- Combined with the daemon-level [switches] enabled flag (config), no push is
-- possible unless BOTH are true — the feature ships dark by construction.
--
-- Migrations are forward-only: once released, never edit this file; a schema
-- change lands as a new NNNN_*.sql with a higher version.
--
-- NOTE (T-1205): numbered 0022 per the phase-12 migration reservation, even
-- though this branch's local base is at a lower migration — see
-- planning/reports/T-1205.md's base-mismatch note.

CREATE TABLE IF NOT EXISTS switches (
  id              TEXT PRIMARY KEY,   -- app-store id (ULID)
  name            TEXT NOT NULL,      -- operator-facing label
  mgmt_addr       TEXT NOT NULL,      -- management address the driver connects to
  driver_type     TEXT NOT NULL,      -- "openconfig" (gNMI); vendor drivers are a future extension
  credentials_enc BLOB,               -- AES-256-GCM ciphertext, NULL if none configured yet
  enabled         INTEGER NOT NULL DEFAULT 0,  -- 0|1; per-switch push opt-in, dark by default
  added_by        TEXT NOT NULL,
  added_at        INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_switches_name ON switches (name);
