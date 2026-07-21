-- 0029_plugins.sql — T-1702 plugin SDK registry. One row per installed
-- extension plugin: the SDK's own record of which third-party (or built-in)
-- plugins are installed, what extension points they attach to, the capability
-- scope they were installed with, their transport, and their enabled state.
--
-- App-owned data per CLAUDE.md's storage rule: this is vnprox's own registry of
-- its own extension points, never a shadow copy of any PVE-authoritative config.
-- A plugin never becomes a second mutation path — its declared capabilities are
-- a *ceiling* checked against internal/auth/caps.go's existing vocabulary
-- (docs/security.md's plugin capability-scope model), and the only change-engine
-- surface a plugin can reach is stage-only Create/Validate (internal/change),
-- never Apply/Confirm/Rollback. This table records what was installed and with
-- which capability scope so `GET /plugins` and the audit trail can always answer
-- "what can this plugin touch, and who installed it".
--
-- capabilities_json is the serialized []string of declared capability names
-- (a subset of internal/auth's AllCaps vocabulary); extension_points_json is the
-- serialized []string of extension-point names the plugin attaches to
-- (switchDriver/flowIngestor/findingProducer/ingressDiscoverer/dashboardTile).
-- Neither is a PVE value; both are validated on install against the SDK's fixed
-- vocabularies before a row is ever written.
--
-- Migrations are forward-only: this file, once released, must never be edited
-- again. Schema changes land as a new NNNN_*.sql file with a higher version.

CREATE TABLE IF NOT EXISTS plugins (
  id                    TEXT PRIMARY KEY,            -- stable plugin id (reverse-dns style, e.g. "com.acme.sonic-driver")
  name                  TEXT NOT NULL,               -- human-readable display name
  version               TEXT NOT NULL DEFAULT '',    -- plugin's own semantic version string (opaque to vnprox)
  api_version           TEXT NOT NULL,               -- the SDK interface version the plugin was built against (e.g. "v1")
  extension_points_json TEXT NOT NULL,               -- serialized []string of extension points this plugin attaches to
  capabilities_json     TEXT NOT NULL,               -- serialized []string of declared capability names (the scope ceiling)
  transport             TEXT NOT NULL,               -- "in-process" | "grpc" (out-of-process supervised subprocess)
  endpoint              TEXT NOT NULL DEFAULT '',     -- transport-specific launch hint (subprocess argv path for out-of-process); empty for in-process
  enabled               INTEGER NOT NULL DEFAULT 1,   -- 1 => live, 0 => installed-but-disabled (extension points not dispatched)
  installed_by          TEXT NOT NULL,               -- identity that installed the plugin (for the audit trail)
  installed_at          INTEGER NOT NULL             -- unix seconds, UTC
);

-- GET /plugins lists newest-first; the registry loads all rows at startup.
CREATE INDEX IF NOT EXISTS idx_plugins_installed_at ON plugins (installed_at);
