// SPDX-License-Identifier: Apache-2.0

// WireGuard, routing, and firewall glyphs. `KindWgTunnel`/`KindWgPeer`
// (internal/inventory/ref.go) are vnprox's own managed WireGuard tunnels —
// a different management plane from an SDN fabric's `wireguard` protocol
// (see glyphs.sdn.tsx's FabricIcon doc comment and SdnFabric's own doc
// comment in api/types.ts), so this pair gets its own distinct visual
// family rather than reusing FabricIcon's mesh. `KindStaticRoute` and
// `KindFwRuleset` are real Kinds; the firewall "group" concept
// (ObjectUsageView.kind === "group", api/types.ts) is not its own Kind but
// is a real, distinct object the firewall pages render.
import { IconShell, type PictogramProps } from "./Icon";
import { isDetailed } from "./sizing";

/** A WireGuard tunnel: `KindWgTunnel`. Drawn as two endpoint terminals
 * joined by a jagged conduit — deliberately a zigzag rather than
 * VxlanIcon's smooth parallel-pipe, so a point-to-point encrypted link
 * never reads as the same shape as an encapsulation tunnel. Detailed draws
 * the full zigzag; inline replaces it with a plain dashed line between the
 * same two terminals — the zigzag's five segments are the "mud at 16px"
 * case, but two dots joined by *any* line still reads as "a link between
 * two points", so the simplification keeps the core reading. */
export function WgTunnelIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <circle cx={5} cy={12} r={2} />
      <circle cx={19} cy={12} r={2} />
      {detailed ? (
        <path d="M7 12 L9 8 L11 16 L13 8 L15 16 L17 12" />
      ) : (
        <line x1={7} y1={12} x2={17} y2={12} strokeDasharray="2 2" />
      )}
    </IconShell>
  );
}

/** A WireGuard peer: `KindWgPeer` — one endpoint of a tunnel, so drawn as a
 * single shield (an identity/trust boundary) rather than WgTunnelIcon's
 * two-terminal link. Curved at the base, unlike FirewallRuleIcon/
 * FirewallGroupIcon's straight-edged pentagons, so the two "shield" families
 * carry a silhouette difference on top of their different interior marks —
 * this is the pair most at risk of reading as the same glyph at a glance;
 * see this set's test/report for that call-out. Detailed adds a center dot
 * standing for the peer's key; inline drops it, leaving the shield alone. */
export function WgPeerIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <path d="M12 3 L19 6 V11 C19 16 16 19 12 21 C8 19 5 16 5 11 V6 Z" />
      {detailed && <circle cx={12} cy={12} r={1.3} fill="currentColor" stroke="none" />}
    </IconShell>
  );
}

/** A static route: `KindStaticRoute`. Drawn as a waypoint path — a start
 * terminal, one elbow bend, and a directed end — the "traffic takes this
 * specific path" reading. Detailed ends in an arrowhead; inline replaces
 * the two-stroke arrowhead with a plain end dot (matching the start dot),
 * which keeps the elbow's "a path with two ends" reading without the
 * arrowhead's extra strokes crowding the corner. */
export function RouteIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <path d="M5 6 L5 14 L17 14" />
      <circle cx={5} cy={6} r={1.2} fill="currentColor" stroke="none" />
      {detailed ? <path d="M14 11 L17 14 L14 17" /> : <circle cx={17} cy={14} r={1.2} fill="currentColor" stroke="none" />}
    </IconShell>
  );
}

// Shared straight-edged pentagon both firewall glyphs below draw from —
// kept as one literal path string (not a component: react-refresh flags a
// non-component export sitting alongside component exports in the same
// file) so FirewallRuleIcon's single shield and FirewallGroupIcon's front
// shield are pixel-identical, and only FirewallGroupIcon's second, offset
// shield tells them apart at a glance.
const SHIELD_PATH = "M12 4 L18 7 V13 L12 20 L6 13 V7 Z";

/** A firewall ruleset: `KindFwRuleset` — one rule list, drawn as the
 * straight-edged shield plus a single horizontal rule line, the "one
 * policy" reading. Detailed keeps the line; inline drops it and leaves the
 * bare shield — still legible as "firewall", just without the "single rule"
 * detail, which is an acceptable loss since the shield-vs-double-shield
 * distinction from FirewallGroupIcon (see that icon's own inline behavior)
 * is what actually needs to survive at small size. */
export function FirewallRuleIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <path d={SHIELD_PATH} />
      {detailed && <line x1={9} y1={10.5} x2={15} y2={10.5} />}
    </IconShell>
  );
}

/** A firewall security group: `ObjectUsageView.kind === "group"`
 * (api/types.ts) — a named, reusable rule list referenced by other rules,
 * so drawn as two overlapping shields (a *set* of rules) against
 * FirewallRuleIcon's single shield (*one* rule). Unlike every other glyph
 * in this set, both sizes render the same two outlines: there is no
 * interior line to drop, and the second shield's offset is itself the
 * signal this glyph exists to carry, so simplifying it away at 16px would
 * remove the one thing that distinguishes it from FirewallRuleIcon. */
export function FirewallGroupIcon(props: PictogramProps) {
  return (
    <IconShell {...props}>
      <path d="M10 2 L16 5 V11 L10 18 L4 11 V5 Z" />
      <path d={SHIELD_PATH} />
    </IconShell>
  );
}
