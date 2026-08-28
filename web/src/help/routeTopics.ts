// SPDX-License-Identifier: Apache-2.0

// Router path → help topic. The coverage gate (coverage.test.ts) parses
// App.tsx for every `path="…"` literal and fails if one is missing a key
// here, so adding a route without adding its help is a build failure, not
// a documentation debt someone notices six months later.
import { matchPath } from "react-router-dom";

export const ROUTE_HELP: Readonly<Record<string, string>> = {
  "/login": "login-page",
  // App.tsx's `<Route index>` under AppShell — the Dashboard. Keyed as "/"
  // because that is the pathname the browser reports for it.
  "/": "dashboard-page",
  "/topology": "topology-page",
  "/management": "management-page",
  "/guests": "guests-page",
  "/guest": "guest-ego-page",
  "/sdn": "sdn-page",
  "/firewall": "firewall-page",
  "/ipam": "ipam-page",
  "/flows": "flows-page",
  "/conntrack": "conntrack-page",
  "/route-explorer": "route-explorer-page",
  "/firewall/compiled": "compiled-ruleset-page",
  "/edge": "edge-page",
  "/diagnose": "diagnose-page",
  "/analysis": "analysis-page",
  "/ports": "ports-page",
  "/wireguard": "wireguard-page",
  "/cabling": "cabling-plan-page",
  "/blueprints": "blueprints-page",
  "/config-as-code": "config-as-code-page",
  "/governance": "governance-page",
  "/hub": "hub-page",
  "/history": "history-page",
  "/incidents": "incidents-page",
  "/audit": "audit-page",
  "/tools": "tools-page",
  "/changesets/:id/review": "changeset-review-page",
  "/settings": "settings-page",
  "/settings/alert-rules": "alert-rules-page",
  "/settings/certificates": "certificates-page",
  "/settings/federation": "settings-federation-page",
  "/settings/platform": "platform-panel-page",
  "/embed/map": "embed-map-page",
  "/embed/dashboard": "embed-dashboard-page",
  "/embed/posture": "embed-posture-page",
};

/** The topic to open when someone asks for help without naming one — i.e.
 * "help with wherever I am". Falls back to the safety model, which is the
 * thing a lost user most needs to know is true. */
export const DEFAULT_HELP_TOPIC = "safety-model";

/** Resolves a pathname to its topic id. Exact match first, then react-
 * router's own pattern matcher for parameterized routes (so
 * `/changesets/abc123/review` finds `/changesets/:id/review`), then the
 * longest matching prefix — `/settings/alert-rules` must beat `/settings`,
 * hence longest-first rather than first-found. */
export function helpTopicForPath(pathname: string): string {
  const exact = ROUTE_HELP[pathname];
  if (exact !== undefined) {
    return exact;
  }

  const entries = Object.entries(ROUTE_HELP).sort(([a], [b]) => b.length - a.length);
  for (const [pattern, topicId] of entries) {
    if (pattern.includes(":") && matchPath(pattern, pathname) !== null) {
      return topicId;
    }
  }
  for (const [pattern, topicId] of entries) {
    if (pattern !== "/" && pathname.startsWith(`${pattern}/`)) {
      return topicId;
    }
  }
  return DEFAULT_HELP_TOPIC;
}
