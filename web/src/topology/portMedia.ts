// T-3503: the two pure decisions behind a drawn port — which jack to draw,
// and what speed marking to silkscreen above it. Kept in their own module,
// framework-free, for the same reason switchModel.ts is: they are the part
// that can be wrong *silently* (a faceplate that draws an SFP cage for a
// copper port still renders, still passes axe, and still looks like a
// switch), so they are exhaustively unit-testable without rendering.
//
// Why the media type drives the drawing and the speed does not: on a NIC with
// no carrier, Linux reports the speed as unknown (-1 in
// /sys/class/net/<if>/speed, SPEED_UNKNOWN from ETHTOOL_GSET) but still
// reports the port type — pvecube's enp2s0/enp4s0 answer `Port: Twisted Pair`
// with no link at all. Media is a property of the socket; speed is a property
// of the link. Keying the drawn body off speed would flip a drawn RJ45 into a
// drawn SFP cage the moment a cable was unplugged. See
// planning/reports/evidence/pve-9.2.4-nic-media-and-speed.txt.

/** Which jack to draw. `unknown` is deliberately its own body rather than a
 * defaulted RJ45: on a node this daemon has not run a host poll against (a
 * peer — see internal/topology/doc.go), or on a driver whose ETHTOOL_GSET
 * fails, we do not know what the socket is, and drawing a confident RJ45
 * would be an invention. `virtual` is the access-port jack a guest NIC gets:
 * a guest NIC has no socket at all, and its shape says so. */
export type PortBodyKind = "rj45" | "sfp" | "unknown" | "virtual";

/** The kernel PORT_* values `internal/host`'s ethtool ioctl maps to strings
 * (linux/ethtool.h), split by what they physically are. Anything not listed —
 * including the empty string, which is what an unattempted or failed ioctl
 * yields — is `unknown`, never guessed from speed or driver name. */
const FIBRE_MEDIA: ReadonlySet<string> = new Set(["fibre", "da"]);
const COPPER_MEDIA: ReadonlySet<string> = new Set(["tp", "mii", "aui", "bnc"]);

/** The jack for a physical NIC, from its reported media port. */
export function bodyForNic(mediaPort: string | undefined): PortBodyKind {
  if (mediaPort === undefined || mediaPort === "") return "unknown";
  if (FIBRE_MEDIA.has(mediaPort)) return "sfp";
  if (COPPER_MEDIA.has(mediaPort)) return "rj45";
  // "none" (PORT_NONE) and "other" (PORT_OTHER) are the driver explicitly
  // declining to say — same honest answer as no reading at all.
  return "unknown";
}

/** The negotiated speed as a faceplate marking ("1G", "100M", "10G"), or
 * undefined when nothing reported one. Switch silkscreen uses these short
 * forms, and they stay legible at the 9px this renders at. Sub-gigabit
 * speeds keep their Mbps figure (100M, 10M); gigabit and above round to
 * whole gigabits, which every real link negotiates to. */
export function speedMarking(speedMbps: number | undefined): string | undefined {
  if (speedMbps === undefined || speedMbps <= 0) return undefined;
  if (speedMbps < 1000) return `${String(speedMbps)}M`;
  const gbps = speedMbps / 1000;
  return `${Number.isInteger(gbps) ? String(gbps) : gbps.toFixed(1)}G`;
}

/** The spoken form of a drawn jack, so a screen reader hears what a sighted
 * operator sees in the silhouette. `virtual` says nothing — a guest NIC's
 * access port having no physical socket is not a fact about the network. */
const BODY_PHRASE: Record<PortBodyKind, string | undefined> = {
  rj45: "copper RJ45",
  sfp: "fibre or direct-attach SFP",
  unknown: "media type unknown",
  virtual: undefined,
};

/** The accessible-description phrases for a physical port: what it is, and
 * how fast it came up. Both omitted when unknown rather than filled with a
 * placeholder — everything the faceplate says in pictures it must also say
 * in words, and that includes saying nothing when it knows nothing. */
export function portPhrases(body: PortBodyKind, speedMbps: number | undefined): string[] {
  const out: string[] = [];
  const phrase = BODY_PHRASE[body];
  if (phrase) out.push(phrase);
  const speed = speedMarking(speedMbps);
  if (speed) out.push(`link speed ${speed}`);
  return out;
}

/** Which drawn jack an entity of this topology kind gets, if any.
 *
 * A physnic gets the body its reported media type earns; a guest NIC always
 * gets the `virtual` one (its access port is a real port of a real virtual
 * switch, but there is no socket behind it); everything else — a bridge, a
 * bond, a VNet — is not a port and gets none.
 *
 * This lives here, in the framework-free module both renderers already
 * import, rather than being spelled out once in `EntityNode.tsx` (the v1 DOM
 * node), once in `canvasDraw.ts` (the v2 canvas) and once in
 * `SwitchFaceplate.tsx`. The precedent for splitting a constant across the
 * two renderers is `MGMT_BADGE_LABEL`/`MGMT_BADGE_PHRASE`, which differ in
 * content — this does not: three copies of one mapping is three places for
 * the two views to start disagreeing about what an entity is, which is the
 * exact failure T-3505 exists to prevent.
 */
export function jackKindForEntity(kind: string, mediaPort: string | undefined): PortBodyKind | undefined {
  if (kind === "physnic") return bodyForNic(mediaPort);
  if (kind === "guest-nic") return "virtual";
  return undefined;
}
