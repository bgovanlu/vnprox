// SPDX-License-Identifier: Apache-2.0

// Plugin lifecycle API calls (T-1702; docs/api.md's Plugins section,
// internal/api/plugins.go).
//
// THERE IS NO INSTALL ROUTE HERE, and this module must never grow one.
// `/api/v1/plugins` serves exactly four operations — list, enable, disable,
// uninstall. Installing a plugin loads code / spawns a supervised subprocess
// and is a Hub operation (`POST /hub/install`, already wired in
// `web/src/hub/`), which verifies the artifact's Ed25519 signature against
// the same trust store blueprint bundles use, refuses a manifest whose
// declared capability scope disagrees with the catalog listing
// (`capabilityMismatch`, T-2104/T-2904 — the one status no trust flag
// overrides), and only then registers through the registry, which
// re-validates the scope independently.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { Plugin, PluginsListResponse } from "./types";

/** GET /plugins (`netRead`) — installed plugins, newest first.
 *
 * Rejects with `ApiError` 404 `not_found` on a daemon where the plugin
 * registry is not wired at all: `mountPluginRoutes` returns early for a nil
 * service, so the route simply is not mounted and the router's own
 * `NotFound` handler answers. That is a different fact from "no plugins are
 * installed" and the caller must render it as one. */
export function fetchPlugins(): Promise<Plugin[]> {
  return apiFetch<PluginsListResponse>("/plugins").then((r) => r.items);
}

/** POST /plugins/{id}/enable (`netWrite` + CSRF) — resume dispatching to an
 * installed-but-disabled plugin's extension points. 204; 404 for an unknown
 * id. Enabling never re-runs signature verification because it never
 * re-installs anything: the manifest that was verified at install time is the
 * one still in the registry. */
export async function enablePlugin(id: string): Promise<void> {
  await apiFetch(`/plugins/${encodeURIComponent(id)}/enable`, { method: "POST", csrfToken: readCsrfCookie() });
}

/** POST /plugins/{id}/disable (`netWrite` + CSRF) — stop dispatching without
 * uninstalling. 204; 404 for an unknown id. */
export async function disablePlugin(id: string): Promise<void> {
  await apiFetch(`/plugins/${encodeURIComponent(id)}/disable`, { method: "POST", csrfToken: readCsrfCookie() });
}

/** DELETE /plugins/{id} (`netWrite` + CSRF) — uninstall, tearing down any
 * out-of-process subprocess. 204; 404 for an unknown id. */
export async function uninstallPlugin(id: string): Promise<void> {
  await apiFetch(`/plugins/${encodeURIComponent(id)}`, { method: "DELETE", csrfToken: readCsrfCookie() });
}
