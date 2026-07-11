// Capability-gating helpers for editing affordances (docs/user-guide.md §5:
// "Read-only PVE users get a read-only vnprox; users without SDN privileges
// see the SDN cockpit disabled with a tooltip naming the missing
// privilege"; T-207 acceptance criterion 4: "Read-only-capability user sees
// disabled editing affordances with explanatory tooltips everywhere").
//
// GET /auth/me's `caps` is keyed per node (internal/auth.BuildCapabilities:
// "" only as a degraded fallback when the node list couldn't be
// enumerated) — see that Go file's doc comment. Editors always know which
// node they're editing (every entity Ref carries one, except cluster-scoped
// SDN/firewall objects), so capability checks here always resolve against a
// specific node's entry, falling back to the "" cluster-wide entry only
// when that exact node has no entry of its own — never the reverse (an
// unknown node must not silently inherit an unrelated node's grant).
import type { Capabilities, MeResponse } from "../api/types";

const NO_CAPS: Capabilities = {
  netRead: false,
  netWrite: false,
  sdnRead: false,
  sdnWrite: false,
  fwRead: false,
  fwWrite: false,
  guestNet: false,
  audit: false,
};

/** Resolves the effective Capabilities for one node from a session's caps
 * map. Safe to call with `session === undefined` (not yet loaded / logged
 * out) — returns every flag `false`, the safe default that disables every
 * editing affordance rather than fails open. */
export function capsForNode(session: MeResponse | undefined, node: string): Capabilities {
  if (!session) return NO_CAPS;
  return session.caps[node] ?? session.caps[""] ?? NO_CAPS;
}

/** Human-readable name for a capability flag, for the disabled-affordance
 * tooltip (docs/user-guide.md §5: "disabled ... with a tooltip naming the
 * missing privilege"). Mirrors internal/auth/caps.go's own flag naming. */
export const CAP_LABELS: Record<keyof Capabilities, string> = {
  netRead: "network read (Sys.Audit)",
  netWrite: "network write (Sys.Modify)",
  sdnRead: "SDN read (SDN.Audit)",
  sdnWrite: "SDN write (SDN.Allocate)",
  fwRead: "firewall read (Sys.Audit)",
  fwWrite: "firewall write (Sys.Modify)",
  guestNet: "guest network (VM.Config.Network)",
  audit: "audit log (Sys.Audit)",
};

/** The one message every disabled editing affordance's tooltip shows: which
 * PVE privilege is missing, on which node. `undefined` (not disabled) when
 * the session already holds `cap` on `node`. */
export function missingCapTooltip(
  session: MeResponse | undefined,
  node: string,
  cap: keyof Capabilities,
): string | undefined {
  const caps = capsForNode(session, node);
  if (caps[cap]) return undefined;
  return node
    ? `You don't have ${CAP_LABELS[cap]} on ${node}.`
    : `You don't have ${CAP_LABELS[cap]} for this cluster-wide object.`;
}

/** True iff the session holds `cap` on at least one entry of its own `caps`
 * map (including the "" cluster-wide entry, if present). For affordances
 * that act cluster-wide in one call rather than against one specific node
 * (T-605's onboarding walkthrough: confirming protected interfaces spans
 * every node in one PUT; installing lldpd fans out to every peer from one
 * POST) — mirrors NewEntityMenu's own "writableNodes.length === 0 -> hide"
 * check, just as a disable-with-tooltip predicate instead of a visibility
 * one, since a cluster-wide write affordance should always be gated
 * disabled-with-tooltip, never hidden (docs/user-guide.md §5). */
export function hasAnyCap(session: MeResponse | undefined, cap: keyof Capabilities): boolean {
  if (!session) return false;
  return Object.values(session.caps).some((caps) => caps[cap]);
}
