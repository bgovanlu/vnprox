// One virtual switch (a Proxmox bridge) rendered as a switch faceplate:
// a status-lit header, an uplink bay (bonds/NICs + their LLDP neighbors), a
// VLAN sub-interface strip, a grid of guest access ports, and a strip of the
// SDN VNets realized on the bridge. Purely presentational — every "what does
// this click mean" decision (open the inspector / expand a guest group) is a
// callback owned by SwitchView/TopologyPage, so this file, like EntityNode,
// stays a straightforward renderer branching on a SwitchModel.
import type { ReactNode } from "react";
import clsx from "clsx";
import type { EntityStatus, FindingBadge } from "../api/types";
import { badgeAriaParts } from "./a11yBridge";
import {
  findingBadgeClass,
  findingChipText,
  findingDetailText,
  hasOpenFinding,
  parseFindingBadge,
  shouldPulse,
} from "./findingBadges";
import { useReducedMotion } from "../lib/useReducedMotion";
import type { SwitchAccessPort, SwitchModel, SwitchPortNic, SwitchUplink } from "./switchModel";

const BOND_KINDS = new Set(["bond", "ovs-bond"]);

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
 * channel instead of changing it. */
function switchAriaDescription(
  kind: string,
  status: EntityStatus,
  badges: readonly string[],
  findings?: readonly FindingBadge[],
): string {
  return [kind, `status ${status}`, ...badgeAriaParts(badges, findings)].join(", ");
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

// T-702: distinct treatment for the management-path badge vocabulary
// (docs/features/topology.md §3), mirroring EntityNode's amber marker so
// the same entity reads the same way in both views.
const MGMT_BADGE_LABEL: Record<string, string> = {
  mgmt: "management IP",
  corosync: "corosync link",
  "mgmt-path": "on the management path",
};

function isMgmtBadge(badge: string): boolean {
  return badge in MGMT_BADGE_LABEL;
}

function mgmtBadgeClass(badge: string): string {
  return isMgmtBadge(badge)
    ? "bg-amber-200/70 text-amber-800 dark:bg-amber-900/60 dark:text-amber-200"
    : "bg-slate-200/70 text-slate-600 dark:bg-slate-700/70 dark:text-slate-300";
}

// Status LED colors, matching EntityNode's STATUS_CLASSES vocabulary so a
// port reads the same here as the same entity does in the graph view.
const LED_CLASS: Record<EntityStatus, string> = {
  ok: "bg-emerald-500",
  down: "bg-red-500",
  degraded: "bg-amber-500",
  unknown: "bg-slate-400 ring-1 ring-slate-300 dark:ring-slate-600",
};

function Led({ status, className }: { status: EntityStatus; className?: string }) {
  return <span className={clsx("inline-block h-2 w-2 shrink-0 rounded-full", LED_CLASS[status], className)} />;
}

/** Whether an access port / VNet / VLAN slot should dim under the active
 * VLAN filter — passed down from SwitchView so a single filter decision is
 * consistent across every slot on the faceplate. */
export type DimFn = (vid: number | undefined) => boolean;

export interface SwitchFaceplateProps {
  model: SwitchModel;
  selectedId: string | undefined;
  /** Layer toggles reinterpreted as faceplate sections (docs/features/
   * topology.md §2): phys=uplink bay, l2=VLAN strip, sdn=VNet strip,
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
    <span className="ml-1 inline-flex items-center gap-0.5 text-[10px] text-slate-500 dark:text-slate-400">
      <span aria-hidden>↔</span>
      <span className="truncate">{neighbor.label}</span>
      {/* T-2004: text-slate-400 dark:text-slate-400 (identical in both
          themes) measured 2.63:1 against a white card in light mode — the
          value only clears AA against a dark card, which is what dark mode
          actually sits on. slate-600 in light mode clears it with margin
          (7.58:1) while leaving dark mode (6.78:1) untouched. */}
      {neighbor.port && <span className="text-slate-600 dark:text-slate-400">{neighbor.port}</span>}
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
        className="flex items-center gap-1.5 rounded border border-dashed border-slate-400 bg-slate-100 px-2 py-1 text-left text-xs text-slate-600 hover:border-slate-600 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-300 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500"
      >
        <Led status={nic.status} />
        <span className="font-mono font-medium">{nic.label}</span>
        <span className="text-[9px] uppercase tracking-wide">click to expand</span>
      </button>
    );
  }
  const onMgmtPath = nic.badges.includes("mgmt-path");
  const descId = `${nic.ref}-a11y-desc`;
  const nicHasFinding = hasOpenFinding(nic.badges);
  const nicPulses = nicHasFinding && shouldPulse(nic.badges) && !nicReducedMotion;
  return (
    <>
      <button
        type="button"
        aria-label={nic.label}
        aria-describedby={descId}
        title={onMgmtPath ? MGMT_BADGE_LABEL["mgmt-path"] : undefined}
        data-entity-ref={nic.ref}
        onClick={() => {
          onSelect(nic.ref);
        }}
        className={clsx(
          "flex items-center gap-1.5 rounded border bg-white px-2 py-1 text-left text-xs hover:border-accent-500 dark:bg-slate-900",
          "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500",
          onMgmtPath ? "border-amber-400 dark:border-amber-700" : "border-slate-300 dark:border-slate-600",
          // T-3501: a NIC directly named by an open finding gets the same
          // dashed-outline/severity-gated-pulse treatment as a bridge chassis
          // (below) — see findingBadges.ts's shouldPulse doc comment.
          nicHasFinding && "border-dashed",
          nicPulses && "animate-pulse",
        )}
      >
        <Led status={nic.status} />
        <span className="font-mono font-medium text-slate-700 dark:text-slate-200">{nic.label}</span>
        {!nic.active && (
          // T-2004: text-amber-700 on bg-amber-100 measured 4.52:1 in light
          // mode — technically over the 4.5:1 AA floor but with essentially
          // no headroom on 9px text, one step from failing outright.
          // amber-800 clears it with margin (6.36:1); dark mode was already
          // fine (10.37:1) and is unchanged.
          <span className="rounded bg-amber-100 px-1 text-[9px] uppercase text-amber-800 dark:bg-amber-950 dark:text-amber-300">
            standby
          </span>
        )}
        {onMgmtPath && (
          <span className="rounded bg-amber-200/70 px-1 text-[9px] uppercase text-amber-800 dark:bg-amber-900/60 dark:text-amber-200">
            mgmt-path
          </span>
        )}
        {nic.badges.map((b) => (
          <FindingChip key={b} token={b} findings={nic.findings} />
        ))}
        <NeighborTag neighbor={nic.neighbor} />
      </button>
      <A11yDesc id={descId} text={switchAriaDescription("NIC", nic.status, nic.badges, nic.findings)} />
    </>
  );
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
  // A bare NIC uplink (single member, itself a NIC) renders as one port chip
  // — this includes a T-1907 phys-group pill directly port-of the bridge,
  // which is never a bond, so it always takes this branch too; a bond
  // renders as a labeled bay wrapping its member NICs.
  const bareNic = uplink.members[0];
  if (!BOND_KINDS.has(uplink.kind) && bareNic) {
    return <NicPort nic={bareNic} onSelect={onSelect} onExpandGroup={onExpandGroup} />;
  }
  const uplinkDescId = `${uplink.ref}-a11y-desc`;
  return (
    <div className="rounded-md border border-sky-300 bg-sky-50/60 p-1.5 dark:border-sky-800 dark:bg-sky-950/40">
      <button
        type="button"
        aria-label={uplink.label}
        aria-describedby={uplinkDescId}
        data-entity-ref={uplink.ref}
        onClick={() => {
          onSelect(uplink.ref);
        }}
        className="mb-1 flex items-center gap-1.5 text-left text-[11px] font-semibold text-sky-800 hover:underline dark:text-sky-200 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500"
      >
        <Led status={uplink.status} />
        <span className="font-mono">{uplink.label}</span>
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
                isMgmtBadge(b)
                  ? "bg-amber-200/70 text-amber-800 dark:bg-amber-900/60 dark:text-amber-200"
                  : "bg-sky-200/70 text-sky-800 dark:bg-sky-900 dark:text-sky-200",
              )}
            >
              {b}
            </span>
          );
        })}
      </button>
      <A11yDesc
        id={uplinkDescId}
        text={switchAriaDescription(uplink.kind, uplink.status, uplink.badges, uplink.findings)}
      />
      <div className="flex flex-wrap gap-1">
        {uplink.members.map((m) => (
          <NicPort key={m.ref} nic={m} onSelect={onSelect} onExpandGroup={onExpandGroup} />
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
          "flex min-w-[56px] flex-col items-center justify-center rounded border border-dashed border-emerald-400 bg-emerald-50 px-2 py-1 text-emerald-700 hover:border-emerald-600 dark:border-emerald-700 dark:bg-emerald-950 dark:text-emerald-300",
          "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500",
          dimmed && "opacity-25",
        )}
      >
        <span className="text-sm font-semibold">+{port.count ?? "…"}</span>
        <span className="text-[9px] uppercase tracking-wide">guests</span>
      </button>
    );
  }
  // Guest label is "name/netK"; show the guest name prominently, the NIC key
  // small, the VMID as the physical "port number" up top.
  const [guestName, nicKey] = port.label.includes("/") ? port.label.split("/", 2) : [port.label, ""];
  const portDescId = `${port.ref}-a11y-desc`;
  return (
    <>
      <button
        type="button"
        aria-label={port.label}
        aria-describedby={portDescId}
        data-entity-ref={port.ref}
        onClick={() => {
          onSelect(port.ref);
        }}
        className={clsx(
          "flex min-w-[56px] flex-col items-center rounded border bg-white px-2 py-1 text-center hover:border-accent-500 dark:bg-slate-900",
          "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500",
          selected ? "border-accent-600 ring-2 ring-accent-500" : "border-slate-300 dark:border-slate-600",
          dimmed && "opacity-25",
        )}
      >
        <span className="flex items-center gap-1">
          <Led status={port.status} />
          <span className="font-mono text-[11px] font-semibold text-slate-700 dark:text-slate-200">
            {port.vmid ?? "—"}
          </span>
        </span>
        <span className="max-w-[72px] truncate text-[10px] text-slate-600 dark:text-slate-300">{guestName}</span>
        {/* T-2004: both spans here were sub-AA in light mode only —
            text-slate-400 dark:text-slate-400 (identical both themes, 2.63:1
            on a white card) and text-violet-500 dark:text-violet-300
            (4.4:1, just under the 4.5:1 floor). slate-600 (7.58:1) and
            violet-700 (7.3:1) clear both with margin; dark mode values
            (6.78:1 / 9.61:1) were already fine and are unchanged. */}
        <span className="text-[9px] text-slate-600 dark:text-slate-400">
          {nicKey}
          {port.vid !== undefined && <span className="ml-0.5 text-violet-700 dark:text-violet-300">·{port.vid}</span>}
        </span>
      </button>
      <A11yDesc id={portDescId} text={switchAriaDescription("guest-nic", port.status, port.badges, port.findings)} />
    </>
  );
}

function Section({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="border-t border-slate-200 px-3 py-2 dark:border-slate-800">
      {/* T-2004: text-slate-400 dark:text-slate-400 (identical both themes)
          measured 2.63:1 against a white card — passes only in dark mode.
          slate-600 in light mode clears AA with margin (7.58:1). */}
      <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-slate-600 dark:text-slate-400">
        {label}
      </div>
      {children}
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
      {/* Chassis header — aria-label stays the plain "<name> switch"
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
        className="flex items-center gap-2 bg-indigo-50 px-3 py-2 text-left hover:bg-indigo-100 dark:bg-indigo-950/60 dark:hover:bg-indigo-900/60 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500"
      >
        <Led status={model.status} className="h-2.5 w-2.5" />
        <span className="font-mono text-sm font-semibold text-slate-800 dark:text-slate-100">{model.name}</span>
        {/* T-2004: text-slate-400 dark:text-slate-400 (identical both
            themes) measured 2.35:1 against this header's bg-indigo-50 —
            slate-600 clears AA with margin (6.78:1); dark mode (6.39:1
            against the dark header tint) was already fine. */}
        <span className="text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-400">{model.kind}</span>
        <span className="ml-auto flex flex-wrap gap-1">
          {model.badges.map((b) => {
            // T-3501: the legacy bare "drift" badge stays wire-present for
            // back-compat (findingBadges.ts) but is never its own chip any
            // more — the literal word "drift" printed here regardless of
            // what actually fired (a carrier error, a health warning, ...)
            // was this task's core defect. A "finding:<source>:<severity>"
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
        <Section label="Uplink">
          <div className="flex flex-wrap gap-1.5">
            {model.uplinks.map((u) => (
              <UplinkModule key={u.ref} uplink={u} onSelect={onSelect} onExpandGroup={onExpandGroup} />
            ))}
          </div>
        </Section>
      )}

      {showVlans && model.vlans.length > 0 && (
        <Section label="VLAN sub-interfaces">
          <div className="flex flex-wrap gap-1">
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
                  dimVid(v.vid) && "opacity-25",
                )}
              >
                <Led status={v.status} />
                {v.label}
              </button>
            ))}
          </div>
        </Section>
      )}

      {showPorts && model.accessPorts.length > 0 && (
        <Section label={`Access ports (${String(model.accessPorts.filter((p) => !p.isGroup).length)})`}>
          <div className="flex flex-wrap gap-1.5">
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
        </Section>
      )}

      {showVnets && model.vnets.length > 0 && (
        <Section label="Realized VNets">
          <div className="flex flex-wrap gap-1">
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
                  dimVid(v.tag) && "opacity-25",
                )}
              >
                <Led status={v.status} />
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
          </div>
        </Section>
      )}
    </div>
  );
}
