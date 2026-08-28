// SPDX-License-Identifier: Apache-2.0

// One virtual switch (a Proxmox bridge) drawn as a switch faceplate.
//
// T-3503 rewrote this from a card-with-text-sections into an actual device
// model: a chassis with a name plate and a status LED, a port field whose
// ports are drawn ports (RJ45 jacks, SFP cages — see PortBody.tsx), an uplink
// bay set apart from the access-port field the way it is on real hardware,
// bond members drawn as one joined group carrying their LACP state, and the
// logical overlays (VLAN sub-interfaces, realized SDN VNets) as silkscreened
// bands below the metal rather than as more port-shaped things.
//
// Still purely presentational — every "what does this click mean" decision
// (open the inspector / expand a guest group) is a callback owned by
// SwitchView/TopologyPage, so this file, like EntityNode, stays a
// straightforward renderer branching on a SwitchModel.
import type { ReactNode } from "react";
import clsx from "clsx";
import type { EntityStatus, FindingBadge } from "../api/types";
import { badgeAriaParts } from "./a11yBridge";
import {
  findingBadgeClass,
  findingChipText,
  findingDetailText,
  hasOpenFinding,
  isMgmtBadge,
  MGMT_BADGE_CLASS,
  MGMT_BADGE_LABEL,
  mgmtBadgeClass,
  parseFindingBadge,
  shouldPulse,
} from "./findingBadges";
import { PortJack, StatusLed } from "./PortBody";
import { bodyForNic, portPhrases, speedMarking, type PortBodyKind } from "./portMedia";
import { useReducedMotion } from "../lib/useReducedMotion";
import type { SwitchAccessPort, SwitchModel, SwitchPortNic, SwitchUplink } from "./switchModel";

const BOND_KINDS = new Set(["bond", "ovs-bond"]);

/** The port field's height cap, in the same rem the port cells are sized in.
 * Applied unconditionally rather than behind a port-count threshold: a switch
 * with four ports is shorter than the cap and is unaffected, a switch with 48
 * scrolls, and there is no count at which the layout changes character. The
 * section's silkscreen always prints the FULL port count, so a scrolled field
 * never silently under-reports how many ports a switch has (T-3503 AC2). */
const PORT_FIELD_MAX_H = "max-h-[13.5rem]";

/** T-905 AC4: the kind/status/badge phrase list for a faceplate entity —
 * "<kind>, status <status>, <badge phrases>" — reusing `badgeAriaParts`
 * (a11yBridge.ts) so the mgmt/corosync/mgmt-path/drift phrasing matches the
 * Graph view's `entityAriaLabel` verbatim instead of a second hand-rolled
 * copy. Rendered into a visually-hidden `<span>` wired via `aria-describedby`
 * (see `A11yDesc` below) — deliberately NOT folded into the button's own
 * `aria-label`: several existing Vitest/Playwright specs (SwitchView.render
 * .test.tsx, command-palette.spec.ts) locate these buttons by their current
 * plain-label accessible *name* (`getByLabelText("eno9a")`,
 * `getByLabel("vmbr0 switch")`), so the name stays exactly as before and
 * this adds richer detail via the separate, standard accessible-description
 * channel instead of changing it.
 *
 * T-3503 appends the drawn-but-not-written facts — media type and negotiated
 * speed — through `extra`, for the same reason the LEDs carry a glyph and not
 * just a hue: everything the faceplate says in pictures it must also say in
 * words. */
function switchAriaDescription(
  kind: string,
  status: EntityStatus,
  badges: readonly string[],
  findings?: readonly FindingBadge[],
  extra?: readonly string[],
): string {
  return [kind, `status ${status}`, ...badgeAriaParts(badges, findings), ...(extra ?? [])].join(", ");
}

/** T-3501: one finding chip — glyph + source name, coloured by severity,
 * with the finding's own detail text as its hover title (AC4: "the operator
 * should not have to leave the map"). Shared by every badge-rendering site
 * in this file (chassis header, uplink module, and — via NicPort/AccessPort
 * — any NIC/guest-nic a future producer names directly) so the chip looks
 * and behaves identically wherever it appears. */
function FindingChip({ token, findings }: { token: string; findings: readonly FindingBadge[] | undefined }) {
  const parsed = parseFindingBadge(token);
  if (!parsed) return null;
  return (
    <span
      title={findingDetailText(parsed, findings)}
      className={clsx("rounded px-1 py-0.5 text-[10px] font-medium", findingBadgeClass(parsed.severity))}
    >
      {findingChipText(parsed)}
    </span>
  );
}

/** The hidden description span `switchAriaDescription` feeds, referenced by
 * a sibling button's `aria-describedby={id}` — one small component so every
 * call site below wires the pattern identically. */
function A11yDesc({ id, text }: { id: string; text: string }) {
  return (
    <span id={id} className="sr-only">
      {text}
    </span>
  );
}

/** Whether an access port / VNet / VLAN slot should dim under the active
 * VLAN filter — passed down from SwitchView so a single filter decision is
 * consistent across every slot on the faceplate. */
export type DimFn = (vid: number | undefined) => boolean;

export interface SwitchFaceplateProps {
  model: SwitchModel;
  selectedId: string | undefined;
  /** Layer toggles reinterpreted as faceplate sections (docs/features/
   * topology.md §2): phys=uplink bay, l2=VLAN band, sdn=VNet band,
   * guest=access ports. The switch header itself always shows. */
  showUplinks: boolean;
  showVlans: boolean;
  showPorts: boolean;
  showVnets: boolean;
  /** True when a VLAN filter is active and this whole switch does not carry
   * it — the faceplate greys out but stays visible (never removed). */
  dimmed: boolean;
  dimVid: DimFn;
  stale: boolean;
  onSelect: (ref: string) => void;
  /** A collapsed guest-group access port was clicked — expand it. */
  onExpandGroup: (groupId: string) => void;
}

function NeighborTag({ neighbor }: { neighbor: SwitchPortNic["neighbor"] }) {
  if (!neighbor) return null;
  return (
    <span className="flex max-w-full items-center justify-center gap-0.5 text-[9px] leading-tight text-fg-subtle">
      <span aria-hidden>↔</span>
      <span className="truncate">{neighbor.label}</span>
      {/* T-2004: text-slate-400 dark:text-slate-400 (identical in both
          themes) measured 2.63:1 against a white card in light mode — the
          value only clears AA against a dark card, which is what dark mode
          actually sits on. slate-600 in light mode clears it with margin
          (7.58:1) while leaving dark mode (6.78:1) untouched. */}
      {neighbor.port && <span className="truncate text-slate-600 dark:text-slate-400">{neighbor.port}</span>}
    </span>
  );
}

/** The shared geometry of every drawn port cell: a fixed-width column with
 * the LED and speed marking on the top line (where a switch silkscreens
 * them), the jack in the middle, and the port's identity beneath it —
 * legible at rest, which is T-3503's explicit requirement, so no hover or
 * selection is needed to read `enp1s0` off the faceplate. */
function PortCell({
  body,
  status,
  speedMbps,
  marking,
  children,
  className,
}: {
  body: PortBodyKind;
  status: EntityStatus;
  speedMbps?: number;
  /** The identity line under the jack. */
  marking: ReactNode;
  /** Anything below the identity line (VLAN tag, neighbor, chips). */
  children?: ReactNode;
  className?: string;
}) {
  const speed = speedMarking(speedMbps);
  return (
    <span className={clsx("flex w-full flex-col items-center gap-0.5", className)}>
      <span className="flex h-3 w-full items-center justify-center gap-1">
        <StatusLed status={status} />
        {/* The negotiated speed is silkscreened above the port, as on real
            hardware. Absent — not "0", not a stale figure — when the link is
            down and the kernel reports no speed. */}
        {speed && (
          <span className="text-[8px] font-semibold uppercase leading-none tracking-wider text-slate-600 dark:text-slate-400">
            {speed}
          </span>
        )}
      </span>
      <PortJack kind={body} status={status} />
      {marking}
      {children}
    </span>
  );
}

function NicPort({
  nic,
  onSelect,
  onExpandGroup,
}: {
  nic: SwitchPortNic;
  onSelect: (ref: string) => void;
  onExpandGroup: (groupId: string) => void;
}) {
  // Called unconditionally, before the isGroup early return below (Rules of
  // Hooks) — a collapsed phys-group pill never carries its own findings, so
  // the pulse computation is simply unused on that branch.
  const nicReducedMotion = useReducedMotion();
  // T-1907: a collapsed phys-group pill standing in for `count` real NICs —
  // same "dashed border, click to expand" affordance as a guest-group
  // access port, reusing onExpandGroup instead of onSelect.
  if (nic.isGroup) {
    return (
      <button
        type="button"
        aria-label={`Expand ${String(nic.count ?? "")} collapsed NICs`}
        data-entity-ref={nic.ref}
        onClick={() => {
          onExpandGroup(nic.ref);
        }}
        className="flex w-[4.25rem] flex-col items-center gap-0.5 rounded border border-dashed border-slate-400 bg-slate-100 px-1 py-1 text-center text-slate-600 hover:border-slate-600 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-300 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500"
      >
        <StatusLed status={nic.status} />
        <span className="text-sm font-semibold leading-none">+{nic.count ?? "…"}</span>
        <span className="font-mono text-[9px] leading-tight">{nic.label}</span>
        <span className="text-[8px] uppercase leading-none tracking-wide">expand</span>
      </button>
    );
  }
  const onMgmtPath = nic.badges.includes("mgmt-path");
  const descId = `${nic.ref}-a11y-desc`;
  const nicHasFinding = hasOpenFinding(nic.badges);
  const nicPulses = nicHasFinding && shouldPulse(nic.badges) && !nicReducedMotion;
  const body = bodyForNic(nic.mediaPort);
  return (
    <>
      <button
        type="button"
        aria-label={nic.label}
        aria-describedby={descId}
        title={onMgmtPath ? MGMT_BADGE_LABEL["mgmt-path"] : nic.label}
        data-entity-ref={nic.ref}
        onClick={() => {
          onSelect(nic.ref);
        }}
        className={clsx(
          "flex w-[4.25rem] flex-col items-center rounded border bg-white px-1 py-1 text-center hover:border-accent-500 dark:bg-slate-900",
          "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500",
          onMgmtPath ? "border-amber-400 dark:border-amber-700" : "border-slate-300 dark:border-slate-600",
          // T-3501: a NIC directly named by an open finding gets the same
          // dashed-outline/severity-gated-pulse treatment as a bridge chassis
          // (below) — see findingBadges.ts's shouldPulse doc comment.
          nicHasFinding && "border-dashed",
          nicPulses && "animate-pulse",
        )}
      >
        <PortCell
          body={body}
          status={nic.status}
          speedMbps={nic.speedMbps}
          marking={
            <span className="max-w-full truncate font-mono text-[10px] font-medium leading-tight text-slate-700 dark:text-slate-200">
              {nic.label}
            </span>
          }
        >
          {!nic.active && (
            // T-4204: "standby" is a normal, expected state for the inactive
            // half of an active-backup bond — informational, not a warning
            // — so it takes the status scale's `info` rung rather than
            // amber (which the T-2004 comment this replaced only ever
            // treated as a contrast problem to solve, not a severity
            // question to ask).
            <span className="rounded bg-status-info-soft px-1 text-[8px] uppercase leading-tight text-status-info">
              standby
            </span>
          )}
          {onMgmtPath && (
            <span className={clsx("rounded px-1 text-[8px] uppercase leading-tight", MGMT_BADGE_CLASS)}>
              mgmt-path
            </span>
          )}
          {nic.badges.map((b) => (
            <FindingChip key={b} token={b} findings={nic.findings} />
          ))}
          <NeighborTag neighbor={nic.neighbor} />
        </PortCell>
      </button>
      <A11yDesc
        id={descId}
        text={switchAriaDescription("NIC", nic.status, nic.badges, nic.findings, portPhrases(body, nic.speedMbps))}
      />
    </>
  );
}

/** The LACP/MII state of a bond, from its members' slave flags — "2/2 up" —
 * so a bond's aggregation health is on the faceplate rather than only in the
 * inspector. Undefined for a bond with no member NICs projected (nothing
 * honest to say). */
function lacpMarking(uplink: SwitchUplink): string | undefined {
  if (uplink.members.length === 0) return undefined;
  const active = uplink.members.filter((m) => m.active).length;
  return `${String(active)}/${String(uplink.members.length)} up`;
}

/** The bond's mode off its "mode=802.3ad" badge (project.go's badgesOf), or
 * undefined when the bond reported none. */
function bondMode(badges: readonly string[]): string | undefined {
  for (const b of badges) {
    const [key, value] = b.split("=", 2);
    if (key === "mode" && value !== undefined && value !== "") return value;
  }
  return undefined;
}

function UplinkModule({
  uplink,
  onSelect,
  onExpandGroup,
}: {
  uplink: SwitchUplink;
  onSelect: (ref: string) => void;
  onExpandGroup: (groupId: string) => void;
}) {
  // A bare NIC uplink (single member, itself a NIC) renders as one port cell
  // — this includes a T-1907 phys-group pill directly port-of the bridge,
  // which is never a bond, so it always takes this branch too; a bond
  // renders as a joined group wrapping its member NICs.
  const bareNic = uplink.members[0];
  if (!BOND_KINDS.has(uplink.kind) && bareNic) {
    return <NicPort nic={bareNic} onSelect={onSelect} onExpandGroup={onExpandGroup} />;
  }
  const uplinkDescId = `${uplink.ref}-a11y-desc`;
  const lacp = lacpMarking(uplink);
  const mode = bondMode(uplink.badges);
  return (
    // The bond is one physical-looking group, not N adjacent ports: a common
    // frame, a shared header, and a rail drawn under the member jacks tying
    // them together (the `before:` bar below). T-3503's requirement is that
    // an operator can see at a glance that these ports are aggregated, which
    // adjacency alone never conveyed.
    <div className="rounded-md border border-sky-300 bg-sky-50/70 p-1.5 dark:border-sky-800 dark:bg-sky-950/40">
      <button
        type="button"
        aria-label={uplink.label}
        aria-describedby={uplinkDescId}
        data-entity-ref={uplink.ref}
        onClick={() => {
          onSelect(uplink.ref);
        }}
        className="mb-1 flex w-full flex-wrap items-center gap-1 text-left text-[11px] font-semibold text-sky-800 hover:underline dark:text-sky-200 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500"
      >
        <StatusLed status={uplink.status} />
        <span className="font-mono">{uplink.label}</span>
        {lacp && (
          <span className="rounded bg-sky-200 px-1 text-[9px] font-medium uppercase tracking-wide text-sky-900 dark:bg-sky-900 dark:text-sky-100">
            {lacp}
          </span>
        )}
        {uplink.badges.map((b) => {
          if (b === "drift") return null; // T-3501: legacy wire-compat token, never its own chip.
          const parsed = parseFindingBadge(b);
          if (parsed) return <FindingChip key={b} token={b} findings={uplink.findings} />;
          return (
            <span
              key={b}
              title={isMgmtBadge(b) ? MGMT_BADGE_LABEL[b] : undefined}
              className={clsx(
                "rounded px-1 text-[9px]",
                isMgmtBadge(b) ? MGMT_BADGE_CLASS : "bg-sky-200 text-sky-800 dark:bg-sky-900 dark:text-sky-200",
              )}
            >
              {b}
            </span>
          );
        })}
      </button>
      <A11yDesc
        id={uplinkDescId}
        text={switchAriaDescription(
          uplink.kind,
          uplink.status,
          uplink.badges,
          uplink.findings,
          [mode ? `bond mode ${mode}` : undefined, lacp ? `${lacp} members` : undefined].filter(
            (s): s is string => s !== undefined,
          ),
        )}
      />
      {/* The aggregation rail: one bar behind the member jacks, so the group
          reads as a single link made of several ports. */}
      <div className="relative flex flex-wrap gap-1.5 before:absolute before:inset-x-1 before:top-1/2 before:h-[2px] before:-translate-y-1/2 before:rounded-full before:bg-sky-300 before:content-[''] dark:before:bg-sky-800">
        {uplink.members.map((m) => (
          <span key={m.ref} className="relative">
            <NicPort nic={m} onSelect={onSelect} onExpandGroup={onExpandGroup} />
          </span>
        ))}
      </div>
    </div>
  );
}

function AccessPort({
  port,
  selected,
  dimmed,
  onSelect,
  onExpandGroup,
}: {
  port: SwitchAccessPort;
  selected: boolean;
  dimmed: boolean;
  onSelect: (ref: string) => void;
  onExpandGroup: (groupId: string) => void;
}) {
  if (port.isGroup) {
    return (
      <button
        type="button"
        aria-label={`Expand ${String(port.count ?? "")} collapsed guests`}
        data-entity-ref={port.ref}
        onClick={() => {
          onExpandGroup(port.ref);
        }}
        className={clsx(
          "flex w-[4.25rem] flex-col items-center justify-center gap-0.5 rounded border border-dashed border-emerald-400 bg-emerald-50 px-1 py-2 text-emerald-700 hover:border-emerald-600 dark:border-emerald-700 dark:bg-emerald-950 dark:text-emerald-300",
          "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500",
          dimmed && "opacity-25",
        )}
      >
        <span className="text-sm font-semibold leading-none">+{port.count ?? "…"}</span>
        <span className="text-[8px] uppercase leading-none tracking-wide">guests</span>
      </button>
    );
  }
  // Guest label is "name/netK"; the VMID is the port number silkscreened
  // above the jack, the guest name and NIC key are the port's identity
  // beneath it.
  const [guestName, nicKey] = port.label.includes("/") ? port.label.split("/", 2) : [port.label, ""];
  const portDescId = `${port.ref}-a11y-desc`;
  return (
    <>
      <button
        type="button"
        aria-label={port.label}
        aria-describedby={portDescId}
        title={port.label}
        data-entity-ref={port.ref}
        onClick={() => {
          onSelect(port.ref);
        }}
        className={clsx(
          "flex w-[4.25rem] flex-col items-center rounded border bg-white px-1 py-1 text-center hover:border-accent-500 dark:bg-slate-900",
          "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500",
          selected ? "border-accent-600 ring-2 ring-accent-500" : "border-slate-300 dark:border-slate-600",
          dimmed && "opacity-25",
        )}
      >
        <span className="flex w-full flex-col items-center gap-0.5">
          <span className="flex h-3 w-full items-center justify-center gap-1">
            <StatusLed status={port.status} />
            <span className="font-mono text-[9px] font-semibold leading-none text-slate-700 dark:text-slate-200">
              {port.vmid ?? "—"}
            </span>
          </span>
          <PortJack kind="virtual" status={port.status} />
          <span className="max-w-full truncate text-[10px] leading-tight text-slate-700 dark:text-slate-200">
            {guestName}
          </span>
          {/* T-2004: both spans here were sub-AA in light mode only —
              text-slate-400 dark:text-slate-400 (identical both themes, 2.63:1
              on a white card) and text-violet-500 dark:text-violet-300
              (4.4:1, just under the 4.5:1 floor). slate-600 (7.58:1) and
              violet-700 (7.3:1) clear both with margin; dark mode values
              (6.78:1 / 9.61:1) were already fine and are unchanged. */}
          <span className="text-[9px] leading-tight text-slate-600 dark:text-slate-400">
            {nicKey}
            {port.vid !== undefined && <span className="ml-0.5 text-violet-700 dark:text-violet-300">·{port.vid}</span>}
          </span>
          {/* T-3504: the guest NIC's firewall, where the `fwbr<vmid>i<netid>`
              bridge used to be drawn as an empty chassis of its own. The
              bridge's name is in the title and in the accessible description
              — the marking has to stay this small to fit a port cell, and a
              two-letter chip nobody can expand would be worse than the empty
              box it replaced. */}
          {port.firewall && (
            <span
              title={`firewalled by ${port.firewall}`}
              className="rounded bg-rose-100 px-1 text-[8px] font-semibold uppercase leading-tight tracking-wide text-rose-800 dark:bg-rose-950 dark:text-rose-300"
            >
              fw
            </span>
          )}
          {port.badges.map((b) => (
            <FindingChip key={b} token={b} findings={port.findings} />
          ))}
        </span>
      </button>
      {/* The firewall bridge's name reaches a screen reader through
          badgeAriaParts' verbatim badge list ("badges: firewall=fwbr103i0"),
          not through an `extra` phrase here — saying it twice would be the
          cost of nicer wording in one view only, and the Graph view reads the
          same badge through the same helper. Phrasing it properly belongs in
          a11yBridge.ts, where both views would get it (T-3505). */}
      <A11yDesc id={portDescId} text={switchAriaDescription("guest-nic", port.status, port.badges, port.findings)} />
    </>
  );
}

/** A silkscreened section of the chassis — the small etched labels a real
 * faceplate carries above each bank of ports. */
function Bay({ label, note, children }: { label: string; note?: string; children: ReactNode }) {
  return (
    <div className="border-t border-slate-200 px-3 py-2 dark:border-slate-800">
      {/* T-2004: text-slate-400 dark:text-slate-400 (identical both themes)
          measured 2.63:1 against a white card — passes only in dark mode.
          slate-600 in light mode clears AA with margin (7.58:1). */}
      <div className="mb-1.5 flex items-baseline gap-1.5 text-[10px] font-semibold uppercase tracking-wider text-slate-600 dark:text-slate-400">
        <span>{label}</span>
        {note && <span className="font-normal normal-case tracking-normal">{note}</span>}
      </div>
      {children}
    </div>
  );
}

/** A logical overlay drawn as a band across the chassis rather than as more
 * ports: VLAN sub-interfaces and realized SDN VNets are not sockets, and
 * T-3503's device model would be lying if it drew them as such. The coloured
 * rail on the left is the band's identity. */
function OverlayBand({ label, rail, children }: { label: string; rail: string; children: ReactNode }) {
  return (
    <div className="flex items-center gap-2 border-t border-slate-200 px-3 py-1.5 dark:border-slate-800">
      <span aria-hidden className={clsx("h-5 w-1 shrink-0 rounded-full", rail)} />
      <span className="w-14 shrink-0 text-[9px] font-semibold uppercase leading-tight tracking-wider text-slate-600 dark:text-slate-400">
        {label}
      </span>
      <span className="flex flex-wrap gap-1">{children}</span>
    </div>
  );
}

export function SwitchFaceplate({
  model,
  selectedId,
  showUplinks,
  showVlans,
  showPorts,
  showVnets,
  dimmed,
  dimVid,
  stale,
  onSelect,
  onExpandGroup,
}: SwitchFaceplateProps) {
  const selected = selectedId === model.ref;
  const chassisDescId = `${model.ref}-a11y-desc`;
  const reducedMotion = useReducedMotion();
  // T-3501: dashed outline stays source-agnostic (any open finding earns
  // it — the pre-existing affordance), but the pulse is now gated on
  // severity instead of firing uniformly for every open finding regardless
  // of what actually fired. See findingBadges.ts's shouldPulse doc comment
  // for why a legacy-badge-only entity still pulses (nothing here regresses
  // an un-upgraded producer to motionless).
  const chassisHasFinding = hasOpenFinding(model.badges);
  const chassisPulses = chassisHasFinding && shouldPulse(model.badges);
  const realPortCount = model.accessPorts.filter((p) => !p.isGroup).length;
  return (
    <div
      className={clsx(
        "flex flex-col overflow-hidden rounded-lg border bg-white shadow-sm dark:bg-slate-900",
        reducedMotion ? "transition-none" : "transition-opacity",
        selected ? "border-accent-600 ring-2 ring-accent-500" : "border-slate-300 dark:border-slate-700",
        dimmed && "opacity-40",
        // Staleness is desaturation ONLY — deliberately no opacity. `opacity`
        // fades this faceplate's text along with its chrome, and its port /
        // VLAN badges are 9px tints that clear AA at full strength with very
        // little headroom: at 0.6 axe measured 90 nodes between 2.76:1 and
        // 4.48:1 against a 4.5:1 floor, and even 0.75 left most of them
        // failing. Greying them out made a switch least readable exactly when
        // it was reporting that its data had stopped refreshing. `grayscale`
        // carries the same "this is not live" signal without touching the
        // contrast of a single glyph. See e2e/a11y.spec.ts, which now waits
        // for a stale entity and measures it instead of forcing it opaque.
        stale && "grayscale",
        // T-905/T-3501: an open finding gets a dashed outline (additive,
        // matching EntityNode's own convention — this container had no such
        // treatment pre-T-3501, only the pulse below, which left the two
        // views' reduced-motion fallback inconsistent: EntityNode's dashed
        // border survived motion being disabled, this chassis had nothing
        // to fall back to). Pulses (Tailwind's `animate-pulse`) only when
        // motion is allowed AND severity warrants it.
        chassisHasFinding && "border-dashed",
        chassisHasFinding && chassisPulses && !reducedMotion && "animate-pulse",
      )}
    >
      {/* Name plate — the dark bezel strip a switch carries its model
          marking on. aria-label stays the plain "<name> switch"
          (SwitchView.render.test.tsx / command-palette.spec.ts locate this
          button by that exact/substring text); kind/status/badge detail is
          additive via aria-describedby, per this file's switchAriaDescription
          doc comment. */}
      <button
        type="button"
        aria-label={`${model.name} switch`}
        aria-describedby={chassisDescId}
        data-entity-ref={model.ref}
        onClick={() => {
          onSelect(model.ref);
        }}
        className="flex items-center gap-2 border-b border-slate-800/10 bg-slate-800 px-3 py-2 text-left hover:bg-slate-700 dark:border-slate-700 dark:bg-slate-950 dark:hover:bg-slate-900 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500"
      >
        <StatusLed status={model.status} className="h-2.5 w-2.5 ring-slate-500" />
        <span className="font-mono text-sm font-semibold tracking-tight text-white">{model.name}</span>
        {/* The device-kind marking, etched the way a model number is: on the
            plate, in the plate's own muted ink. slate-400 on slate-800
            measures 6.9:1 (light) and on slate-950 8.9:1 (dark) — both well
            clear of AA, unlike the slate-400-on-white this replaced. */}
        <span className="rounded-sm border border-slate-600 px-1 text-[9px] uppercase leading-tight tracking-wider text-slate-300">
          {model.kind}
        </span>
        <span className="ml-auto flex flex-wrap items-center gap-1">
          {model.badges.map((b) => {
            // T-3501: the legacy bare "drift" badge stays wire-present for
            // back-compat (findingBadges.ts) but is never its own chip any
            // more — the literal word "drift" printed here regardless of
            // what actually fired (a carrier error, a health warning, ...)
            // was that task's core defect. A "finding:<source>:<severity>"
            // token renders as FindingChip instead; every other badge
            // (mgmt/corosync/mgmt-path/qos-shaped/plain) is unchanged.
            if (b === "drift") return null;
            const parsed = parseFindingBadge(b);
            if (parsed) return <FindingChip key={b} token={b} findings={model.findings} />;
            return (
              <span
                key={b}
                title={isMgmtBadge(b) ? MGMT_BADGE_LABEL[b] : undefined}
                className={clsx("rounded px-1 py-0.5 text-[10px]", mgmtBadgeClass(b))}
              >
                {b}
              </span>
            );
          })}
        </span>
      </button>
      <A11yDesc
        id={chassisDescId}
        text={switchAriaDescription(`${model.kind} switch`, model.status, model.badges, model.findings)}
      />

      {showUplinks && model.uplinks.length > 0 && (
        // The uplink bay: its own recessed strip, tinted and set apart from
        // the access-port field below, the way a switch's uplink bank is
        // physically separated from its access bank.
        <Bay label="Uplink bay">
          <div className="flex flex-wrap gap-1.5 rounded-md bg-slate-100/70 p-1.5 dark:bg-slate-800/50">
            {model.uplinks.map((u) => (
              <UplinkModule key={u.ref} uplink={u} onSelect={onSelect} onExpandGroup={onExpandGroup} />
            ))}
          </div>
        </Bay>
      )}

      {showPorts && model.accessPorts.length > 0 && (
        // The access-port field. `flex-wrap` + a fixed cell width gives the
        // real-hardware "ports run in rows and wrap to the next row" look at
        // any card width, and PORT_FIELD_MAX_H caps the field so a 48-port
        // node scrolls its ports instead of stretching the chassis past the
        // viewport (T-3503 AC2). The count in the silkscreen is the full
        // count, always, so scrolling never hides how many there are.
        <Bay label={`Access ports (${String(realPortCount)})`}>
          <div className={clsx("flex flex-wrap gap-1.5 overflow-y-auto pr-0.5", PORT_FIELD_MAX_H)}>
            {model.accessPorts.map((p) => (
              <AccessPort
                key={p.ref}
                port={p}
                selected={selectedId === p.ref}
                dimmed={dimVid(p.vid) && !p.isGroup}
                onSelect={onSelect}
                onExpandGroup={onExpandGroup}
              />
            ))}
          </div>
        </Bay>
      )}

      {showVlans && model.vlans.length > 0 && (
        <OverlayBand label="VLAN" rail="bg-violet-400 dark:bg-violet-600">
          {model.vlans.map((v) => (
            <button
              key={v.ref}
              type="button"
              aria-label={v.label}
              data-entity-ref={v.ref}
              onClick={() => {
                onSelect(v.ref);
              }}
              className={clsx(
                "flex items-center gap-1 rounded border border-violet-300 bg-violet-50 px-1.5 py-0.5 text-[11px] font-mono text-violet-700 hover:border-violet-500 dark:border-violet-800 dark:bg-violet-950 dark:text-violet-300",
                "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500",
                dimVid(v.vid) && "opacity-25",
              )}
            >
              <StatusLed status={v.status} />
              {v.label}
            </button>
          ))}
        </OverlayBand>
      )}

      {showVnets && model.vnets.length > 0 && (
        <OverlayBand label="VNet" rail="bg-teal-400 dark:bg-teal-600">
          {model.vnets.map((v) => (
            <button
              key={v.ref}
              type="button"
              aria-label={v.label}
              data-entity-ref={v.ref}
              onClick={() => {
                onSelect(v.ref);
              }}
              className={clsx(
                "flex items-center gap-1 rounded border border-teal-300 bg-teal-50 px-1.5 py-0.5 text-[11px] text-teal-700 hover:border-teal-500 dark:border-teal-800 dark:bg-teal-950 dark:text-teal-300",
                "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500",
                dimVid(v.tag) && "opacity-25",
              )}
            >
              <StatusLed status={v.status} />
              <span className="font-medium">{v.label}</span>
              {/* T-2004: text-teal-500 dark:text-teal-400 measured 2.32:1
                  in light mode against this button's bg-teal-50 — badly
                  sub-AA. Reusing the button's own text-teal-700/
                  dark:text-teal-300 (the ".label" color above) clears it
                  with margin (5.14:1 light, 9.82:1 dark, both unchanged
                  from the existing label color). */}
              {v.tag !== undefined && <span className="text-teal-700 dark:text-teal-300">·{v.tag}</span>}
            </button>
          ))}
        </OverlayBand>
      )}
    </div>
  );
}
