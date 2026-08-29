// SPDX-License-Identifier: Apache-2.0

// Linux/OVS link-layer glyphs: bond, bridge (both with an OVS variant —
// internal/inventory/ref.go's KindOVSBond/KindOVSBridge are distinct kinds
// from KindBond/KindBridge, and were separately tinted by the KIND_ACCENT
// map T-4302 has since deleted in favour of these very glyphs), and VLAN.
import { IconShell, type PictogramProps } from "./Icon";
import { isDetailed } from "./sizing";

/** A small hollow ring in the top-right corner — the one mark that turns a
 * plain bond/bridge glyph into its OVS sibling, kept identical across both
 * so "this is the OVS variant of X" reads as one consistent rule rather
 * than two different treatments. Detailed only: at inline size there is no
 * room to add a mark without it either vanishing or overwhelming the base
 * shape, so OvsBondIcon/OvsBridgeIcon fall back to their plain sibling's
 * silhouette at 16px — a deliberate, documented loss of the OVS distinction
 * at the smallest size, not an oversight. */
function OvsMark() {
  return <circle cx={19} cy={5} r={1.1} />;
}

/** A Linux bond: internal/inventory/ref.go's `KindBond`. Drawn as a funnel
 * — several member links converging into one aggregated trunk — the
 * "many-become-one" shape that is bonding's actual behavior, deliberately
 * unlike BridgeIcon's box (a bridge joins links *through* a device; a bond
 * merges them into a single logical one). Detailed marks every leaf's
 * origin with a terminal dot; inline drops those three dots (they crowd at
 * 16px) and keeps only the trunk's terminal, since the funnel outline alone
 * already reads as "converging". */
export function BondIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <path d="M4 6 L11 12 M4 12 L11 12 M4 18 L11 12 L20 12" />
      {detailed && (
        <>
          <circle cx={4} cy={6} r={1} fill="currentColor" stroke="none" />
          <circle cx={4} cy={12} r={1} fill="currentColor" stroke="none" />
          <circle cx={4} cy={18} r={1} fill="currentColor" stroke="none" />
        </>
      )}
      <circle cx={20} cy={12} r={1.2} fill="currentColor" stroke="none" />
    </IconShell>
  );
}

/** An OVS bond: `KindOVSBond` — BondIcon's funnel plus the OVS corner
 * mark. See OvsMark's doc comment for why the mark is detailed-only. */
export function OvsBondIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <path d="M4 6 L11 12 M4 12 L11 12 M4 18 L11 12 L20 12" />
      {detailed && (
        <>
          <circle cx={4} cy={6} r={1} fill="currentColor" stroke="none" />
          <circle cx={4} cy={12} r={1} fill="currentColor" stroke="none" />
          <circle cx={4} cy={18} r={1} fill="currentColor" stroke="none" />
          <OvsMark />
        </>
      )}
      <circle cx={20} cy={12} r={1.2} fill="currentColor" stroke="none" />
    </IconShell>
  );
}

/** A Linux bridge: `KindBridge`. Drawn as a device body with ports stubbed
 * off it — the "joins links through a device" shape, unlike BondIcon's
 * pure convergence with no body. Detailed draws three stubs off the top
 * edge and one off the bottom (four attached links); inline keeps two top
 * stubs and drops the bottom one — the body-plus-stubs silhouette survives
 * on two stubs alone, and a third+fourth are the "interior lines that are
 * mud at 16px" the roadmap card warns about. */
export function BridgeIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <rect x={7} y={9} width={10} height={6} rx={1} />
      {detailed ? (
        <>
          <line x1={9} y1={9} x2={9} y2={5} />
          <line x1={12} y1={9} x2={12} y2={5} />
          <line x1={15} y1={9} x2={15} y2={5} />
          <line x1={12} y1={15} x2={12} y2={19} />
        </>
      ) : (
        <>
          <line x1={9} y1={9} x2={9} y2={6} />
          <line x1={15} y1={9} x2={15} y2={6} />
        </>
      )}
    </IconShell>
  );
}

/** An OVS bridge: `KindOVSBridge` — BridgeIcon's body-plus-stubs plus the
 * OVS corner mark. See OvsMark's doc comment for why the mark is
 * detailed-only. */
export function OvsBridgeIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <rect x={7} y={9} width={10} height={6} rx={1} />
      {detailed ? (
        <>
          <line x1={9} y1={9} x2={9} y2={5} />
          <line x1={12} y1={9} x2={12} y2={5} />
          <line x1={15} y1={9} x2={15} y2={5} />
          <line x1={12} y1={15} x2={12} y2={19} />
          <OvsMark />
        </>
      ) : (
        <>
          <line x1={9} y1={9} x2={9} y2={6} />
          <line x1={15} y1={9} x2={15} y2={6} />
        </>
      )}
    </IconShell>
  );
}

/** A VLAN: `KindVlan`. Drawn as a tag — the universal "labeled/segmented"
 * metaphor, and deliberately flat (no body, no stubs) so it never competes
 * with BridgeIcon/BondIcon's device-shaped silhouettes. Detailed adds the
 * tag's punch-hole; inline drops it (a ~2px circle inside an already-small
 * shape disappears/muddies faster than it reads), keeping the tag outline
 * alone — still unambiguously a tag at 16px. */
export function VlanIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <path d="M6 12 L11 5 L19 5 L19 19 L11 19 Z" />
      {detailed && <circle cx={9} cy={12} r={1} fill="currentColor" stroke="none" />}
    </IconShell>
  );
}
