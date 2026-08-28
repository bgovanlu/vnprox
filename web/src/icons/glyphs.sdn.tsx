// SPDX-License-Identifier: Apache-2.0

// PVE SDN glyphs: VXLAN (a zone `type` value, docs/api.md's SdnZone.type
// enum — not its own inventory Kind, but named explicitly by the roadmap
// card as a distinct overlay-encapsulation concept from a plain zone),
// fabric, zone, vnet, subnet (all real Kinds — internal/inventory/ref.go's
// KindSDNFabric/KindSDNZone/KindSDNVnet/KindSDNSubnet), a gateway (a field
// on SdnSubnet/SdnZone/etc., not a Kind of its own, but named explicitly by
// the roadmap card and drawable as a distinct concept — see T-4605's "zones
// as regions, vnets as lanes, gateways as glyphs"), and an LLDP-discovered
// neighbor (`KindLldpNeighbor`).
import { IconShell, type PictogramProps } from "./Icon";
import { isDetailed } from "./sizing";

/** A VXLAN overlay: not a Kind (see module doc comment) but a distinct
 * visual concept from VlanIcon's flat tag — drawn as a tunnel cross-section,
 * two ring "walls" joined by conduit lines with an encapsulated payload dot
 * in the middle, the encapsulation-in-encapsulation reading VXLAN actually
 * is. Detailed keeps top+bottom conduit lines and the payload dot; inline
 * drops to a single centerline (no dot) — the two rings alone already read
 * as "a pipe", and a lone center line is what keeps it a pipe rather than
 * two unrelated ovals. */
export function VxlanIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <ellipse cx={7} cy={12} rx={3} ry={6} />
      <ellipse cx={17} cy={12} rx={3} ry={6} />
      {detailed ? (
        <>
          <line x1={7} y1={7} x2={17} y2={7} />
          <line x1={7} y1={17} x2={17} y2={17} />
          <circle cx={12} cy={12} r={1.2} fill="currentColor" stroke="none" />
        </>
      ) : (
        <line x1={7} y1={12} x2={17} y2={12} />
      )}
    </IconShell>
  );
}

/** An SDN fabric: `KindSDNFabric`, PVE 9's underlay-routing object family
 * (docs/api.md, planning/reports/evidence/pve-9.2.4-sdn-schema.txt). Drawn
 * as a small complete mesh — three nodes with every pairwise link, i.e. a
 * triangle — the "routed underlay mesh" reading, deliberately distinct from
 * ZoneIcon's single bounded region. A first version used four corner nodes
 * plus both diagonals; rendered at 32-96px (see this set's gallery
 * screenshot) the crossing diagonals inside a square outline reads as a
 * cancel/close glyph, not a mesh — a real false-affordance risk this shape
 * avoids by construction (three points' pairwise links are already a
 * closed triangle, no diagonal ever crosses another edge). Detailed marks
 * each vertex with a terminal dot; inline drops them — three ~2px dots on
 * an already-small triangle read as noise — and keeps the bare triangle,
 * still legibly "three linked nodes" from its outline alone. */
export function FabricIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <path d="M12 5 L5 18 L19 18 Z" />
      {detailed && (
        <>
          <circle cx={12} cy={5} r={1.8} fill="currentColor" stroke="none" />
          <circle cx={5} cy={18} r={1.8} fill="currentColor" stroke="none" />
          <circle cx={19} cy={18} r={1.8} fill="currentColor" stroke="none" />
        </>
      )}
    </IconShell>
  );
}

/** An SDN zone: `KindSDNZone`. Drawn as a dashed bounded region containing
 * member entities — "dashed" deliberately echoing the product's existing
 * convention for "observed boundary" styling (STATUS_CLASSES's `unknown`
 * dashed treatment, EntityNode.tsx) rather than inventing a new one — with
 * small dots inside standing for the vnets/guests it contains, kept on one
 * level row (not two-high-one-low) so they read as a row of members, not
 * — as an earlier two-up-one-down arrangement did at 32-96px — a face.
 * Detailed draws three member dots; inline collapses to one centered dot,
 * since three ~2px dots inside a 16px dashed box read as noise, not
 * membership. */
export function ZoneIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <rect x={4} y={4} width={16} height={16} rx={4} strokeDasharray="3 3" />
      {detailed ? (
        <>
          <circle cx={9} cy={14.5} r={1} fill="currentColor" stroke="none" />
          <circle cx={12} cy={14.5} r={1} fill="currentColor" stroke="none" />
          <circle cx={15} cy={14.5} r={1} fill="currentColor" stroke="none" />
        </>
      ) : (
        <circle cx={12} cy={12} r={1.3} fill="currentColor" stroke="none" />
      )}
    </IconShell>
  );
}

/** An SDN vnet: `KindSDNVnet`. Drawn as dashed lane markings — "dashed"
 * because a vnet is virtual wiring, not a physical link (the same
 * solid-vs-dashed convention PortJack/jackKindForEntity already draws on
 * for real-vs-virtual ports, portMedia.ts) — with short end ticks closing
 * off each lane. Detailed draws two lanes; inline draws one, centered —
 * a single dashed lane with end ticks is still unambiguously "a virtual
 * segment", and a second parallel line at 16px reads as clutter, not a
 * second lane. */
export function VnetIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      {detailed ? (
        <>
          <line x1={5} y1={9} x2={19} y2={9} strokeDasharray="3 2" />
          <line x1={5} y1={15} x2={19} y2={15} strokeDasharray="3 2" />
          <line x1={5} y1={7.5} x2={5} y2={10.5} />
          <line x1={19} y1={7.5} x2={19} y2={10.5} />
          <line x1={5} y1={13.5} x2={5} y2={16.5} />
          <line x1={19} y1={13.5} x2={19} y2={16.5} />
        </>
      ) : (
        <>
          <line x1={5} y1={12} x2={19} y2={12} strokeDasharray="3 2" />
          <line x1={5} y1={10} x2={5} y2={14} />
          <line x1={19} y1={10} x2={19} y2={14} />
        </>
      )}
    </IconShell>
  );
}

/** An SDN subnet: `KindSDNSubnet`. Drawn as a bordered address block
 * subdivided into a cell grid — deliberately echoing T-4602's address heat
 * grid, the view this glyph will eventually sit beside. Detailed divides it
 * 3x3 (nine cells, four interior lines); inline divides it 2x2 (four cells,
 * two interior lines) — the exact "four interior lines is mud at 16px" case
 * the roadmap card names, and the reason this glyph halves its own detail
 * rather than just thinning strokes. */
export function SubnetIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <rect x={4} y={4} width={16} height={16} rx={1} />
      {detailed ? (
        <>
          <line x1={9.33} y1={4} x2={9.33} y2={20} />
          <line x1={14.67} y1={4} x2={14.67} y2={20} />
          <line x1={4} y1={9.33} x2={20} y2={9.33} />
          <line x1={4} y1={14.67} x2={20} y2={14.67} />
        </>
      ) : (
        <>
          <line x1={12} y1={4} x2={12} y2={20} />
          <line x1={4} y1={12} x2={20} y2={12} />
        </>
      )}
    </IconShell>
  );
}

/** A gateway: not a Kind of its own (it is a field — SdnSubnet.gateway and
 * similar — see module doc comment) but named explicitly by the roadmap
 * card and by T-4605's SDN zone map ("gateways as glyphs"). Drawn as two
 * gate posts with an arrow passing between them — traffic crossing a
 * boundary. Detailed adds the arrowhead; inline drops it (two extra
 * diagonal strokes crowd the 16px box) and leaves a plain line through the
 * posts — the posts-plus-crossing-line silhouette alone still reads as
 * "gateway", just without the directional arrow. */
export function GatewayIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <line x1={8} y1={4} x2={8} y2={20} />
      <line x1={16} y1={4} x2={16} y2={20} />
      <line x1={4} y1={12} x2={20} y2={12} />
      {detailed && <path d="M17 9 L20 12 L17 15" />}
    </IconShell>
  );
}

/** An LLDP-discovered neighbor: `KindLldpNeighbor` — an external device the
 * LLDP collector observed, never one vnprox owns or configures. Drawn as a
 * dashed diamond, the same "dashed = observed, not asserted" convention
 * ZoneIcon and the product's own unknown-status styling already use, with a
 * center dot standing for the device itself. Detailed keeps the dot; inline
 * drops it — the dashed diamond outline alone is a small enough shape that
 * an added center dot at 16px reads as a stray pixel, not a device marker. */
export function LldpNeighborIcon(props: PictogramProps) {
  const detailed = isDetailed(props.size);
  return (
    <IconShell {...props}>
      <path d="M12 4 L20 12 L12 20 L4 12 Z" strokeDasharray="2.5 2.5" />
      {detailed && <circle cx={12} cy={12} r={1.2} fill="currentColor" stroke="none" />}
    </IconShell>
  );
}
