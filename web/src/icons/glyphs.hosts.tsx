// SPDX-License-Identifier: Apache-2.0

// Physical/virtual compute and connector glyphs: a PVE host, a guest
// (VM/container — see NodeIcon's doc comment for why the two share one
// glyph), a physical NIC, a bare port jack, and a physical switch chassis.
// Kind strings match internal/inventory/ref.go's Kind constants where one
// exists (see registry.ts).
import { IconShell, type PictogramProps } from "./Icon";
import { isDetailed } from "./sizing";

/** A PVE host/node: internal/inventory/ref.go's `KindNode`. Drawn as a
 * multi-unit rack tower — a wide, tall chassis with rack-unit dividers —
 * the silhouette that reads as "a host machine" and stays clearly taller
 * and narrower than SwitchIcon's wide, short chassis. Detailed: two
 * dividers (three rack units) plus a status LED dot per unit; inline drops
 * the LEDs (they read as stray specks at 16px) and collapses to a single
 * divider so the silhouette itself — not interior dots — carries the
 * "stacked units" reading. */
export function NodeIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <rect x={5} y={3} width={14} height={18} rx={2} />
      {detailed ? (
        <>
          <line x1={5} y1={9} x2={19} y2={9} />
          <line x1={5} y1={15} x2={19} y2={15} />
          <circle cx={8} cy={6} r={1} fill="currentColor" stroke="none" />
          <circle cx={8} cy={12} r={1} fill="currentColor" stroke="none" />
          <circle cx={8} cy={18} r={1} fill="currentColor" stroke="none" />
        </>
      ) : (
        <line x1={5} y1={12} x2={19} y2={12} />
      )}
    </IconShell>
  );
}

/** A guest (VM or container): internal/inventory/ref.go's `KindGuest` — the
 * data model has no separate container Kind (a guest's qemu/lxc distinction
 * lives only inside its interior-agent source, InteriorTab.tsx, never as a
 * kind-level split), so one glyph honestly covers both rather than
 * inventing a distinction the app doesn't model. Drawn as a monitor-on-a-
 * stand — deliberately a *console*, not another rack chassis, so it never
 * reads as a smaller NodeIcon. Detailed adds the stand's base bar; inline
 * drops it (a 1px bar is invisible noise at 16px) and keeps just the
 * screen + neck, which is enough silhouette on its own. */
export function GuestIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <rect x={4} y={5} width={16} height={11} rx={1.5} />
      <line x1={12} y1={16} x2={12} y2={19} />
      {detailed && <line x1={8} y1={19} x2={16} y2={19} />}
    </IconShell>
  );
}

/** A physical NIC: internal/inventory/ref.go's `KindPhysNic`. Drawn as a
 * jack (see PortIcon) with an added card-edge bracket on its left — the
 * same jack because a physnic *is* a port, plus the bracket because this
 * one lives on a NIC card rather than a bare faceplate (PortIcon has no
 * bracket). This is the pair most at risk of reading as duplicates at small
 * size; the bracket is what has to survive. Detailed keeps both jack pins
 * and the full three-sided bracket; inline keeps only the bracket's top-left
 * corner (an "L", still legibly a bracket) and drops to a single pin. */
export function PhysNicIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      {detailed ? (
        <path d="M3 5 H7 M3 5 V19 M3 19 H7" />
      ) : (
        <path d="M3 6 H6 M3 6 V10" />
      )}
      <rect x={9} y={8} width={8} height={8} rx={1} />
      {detailed ? (
        <>
          <line x1={11.5} y1={16} x2={11.5} y2={18} />
          <line x1={14.5} y1={16} x2={14.5} y2={18} />
        </>
      ) : (
        <line x1={13} y1={16} x2={13} y2={17.5} />
      )}
    </IconShell>
  );
}

/** A bare port/jack: no dedicated inventory Kind (a port is rendered inline
 * — PortsPage.tsx, PortBody.tsx's `PortJack`, SwitchFaceplate.tsx's faceplate
 * cells — never as its own Ref), included because the roadmap card names it
 * explicitly. Just the socket, no card bracket (that is what separates it
 * from PhysNicIcon) — a single jack with two contact pins. Inline drops the
 * pins to a single center dot rather than trying to render two ~1px ticks
 * inside an 8px box. */
export function PortIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <rect x={8} y={8} width={8} height={8} rx={1} />
      {detailed ? (
        <>
          <line x1={10.5} y1={13} x2={10.5} y2={16} />
          <line x1={13.5} y1={13} x2={13.5} y2={16} />
        </>
      ) : (
        <circle cx={12} cy={12} r={1} fill="currentColor" stroke="none" />
      )}
    </IconShell>
  );
}

/** A guest NIC: `KindGuestNic` — a kind EntityNode.tsx's since-deleted
 * `KIND_ACCENT` tinted separately from every other (T-4302 removed the map;
 * this glyph is what replaced it), and portMedia.ts's `jackKindForEntity`/
 * `PortBody.tsx`'s `PortJack` already draw it as a *dashed* jack (a guest
 * NIC has no physical socket, docs/features/topology.md §2's "virtual =
 * dashed" convention). This glyph reuses that exact real/virtual convention
 * rather than inventing a new one: same socket as PortIcon, dashed instead
 * of solid. Inline drops to the same single center dot PortIcon does, on a
 * dashed (not solid) box — the dash is what has to survive at 16px, since
 * that is the one thing distinguishing it from PortIcon at a glance. */
export function GuestNicIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <rect x={8} y={8} width={8} height={8} rx={1} strokeDasharray="2 1.5" />
      {detailed ? (
        <>
          <line x1={10.5} y1={13} x2={10.5} y2={16} />
          <line x1={13.5} y1={13} x2={13.5} y2={16} />
        </>
      ) : (
        <circle cx={12} cy={12} r={1} fill="currentColor" stroke="none" />
      )}
    </IconShell>
  );
}

/** A physical switch device — no dedicated inventory Kind of its own
 * either (`KindSwitchPort` names one of its ports, docs/data-model.md §3 —
 * the switch itself is app-owned intent in the `switches` store table, see
 * internal/switchdrv), included for the same "named explicitly, and the
 * concept is real" reason as PortIcon (web/src/topology/SwitchFaceplate.tsx,
 * ports/PortsPage.tsx). Drawn wide and short — the opposite proportions
 * from NodeIcon's tall tower — with a row of port ticks along the bottom
 * edge, the chassis-plus-faceplate silhouette. Detailed draws five ticks;
 * inline draws three, spaced to still read as "a row", not "some marks". */
export function SwitchIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  const xs = detailed ? [6, 9.5, 13, 16.5, 20] : [7.5, 12, 16.5];
  return (
    <IconShell {...props}>
      <rect x={3} y={7} width={18} height={9} rx={1.5} />
      {xs.map((x) => (
        <line key={x} x1={x} y1={16} x2={x} y2={18.5} />
      ))}
    </IconShell>
  );
}
