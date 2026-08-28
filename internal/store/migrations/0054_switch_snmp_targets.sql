-- 0054_switch_snmp_targets.sql — T-4013's read-only SNMP switch-counter
-- poller: per-switch, operator-opted-in SNMPv2c poll configuration.
--
-- Distinct from 0022_switches.sql's `switches` table on purpose: that table
-- is T-1205's guarded-push registry (an OpenConfig/gNMI driver identity,
-- required before any switch.port.* changeset op can target it). This
-- table has nothing to do with that write path — internal/ifcounters, its
-- only reader/writer, never imports internal/switchdrv (see
-- internal/ifcounters/noswitchdrv_test.go) — and it can name a switch
-- vnprox has only ever seen via an LLDP neighbor relationship, never
-- registered for push at all. Per CLAUDE.md's storage rule, this is
-- app-owned intent + credentials only: the switch's live counters stay
-- authoritative on the switch itself, polled fresh every tick
-- (internal/ifcounters.Service, "current state, not a ring" — see that
-- package's doc.go), never persisted here.
--
-- `community_enc` is AES-256-GCM ciphertext (nonce||ciphertext||tag), the
-- IDENTICAL session-secret encryption-at-rest primitive
-- switches.credentials_enc / sessions.pve_ticket_enc use
-- (internal/store/cipher.go's SessionCipher) — not a second cipher or key.
-- Registered in internal/backup/secrets.go's secret-class inventory
-- (TestSecretClasses_CoverEverySealedColumn enforces that mechanically).
--
-- `enabled` defaults to 0 (false): SNMP polling is per-switch explicit
-- opt-in, off by default (T-4013's card: "Off by default; an operator opts
-- in per switch with a community string").
--
-- Migrations are forward-only: once released, never edit this file; a
-- schema change lands as a new NNNN_*.sql with a higher version.

CREATE TABLE IF NOT EXISTS switch_snmp_targets (
  id             TEXT PRIMARY KEY,   -- app-store id (ULID)
  chassis_id     TEXT NOT NULL,      -- matches an LLDP neighbor's ChassisID; the only key Service.Tick looks switches up by
  chassis_id_type TEXT NOT NULL DEFAULT '', -- LLDP ChassisIDType, kept alongside chassis_id for operator-facing display/disambiguation only
  mgmt_addr      TEXT NOT NULL DEFAULT '',  -- operator-pinned SNMP target address; empty means "use the LLDP-advertised MgmtIP"
  port           INTEGER NOT NULL DEFAULT 161, -- UDP port (snmp.DefaultPort)
  community_enc  BLOB,               -- AES-256-GCM ciphertext, NULL if never configured
  enabled        INTEGER NOT NULL DEFAULT 0,  -- 0|1; per-switch poll opt-in, dark by default
  added_by       TEXT NOT NULL,
  added_at       INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_switch_snmp_targets_chassis_id ON switch_snmp_targets (chassis_id);
