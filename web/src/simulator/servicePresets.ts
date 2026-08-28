// SPDX-License-Identifier: Apache-2.0

// Service/proto/port presets for the simulator's request form. Reuses
// `GET /firewall/objects`' built-in macro catalog (internal/fw.KnownMacros,
// already exposed to the frontend as FirewallObjectsResponse.macros — see
// docs/api.md's "Macro: {name, comment?, ports: [{proto?, dport?}]}") as
// the one preset source, per this task's guidance not to invent a second,
// disconnected list: the same names the firewall rule builder's macro
// picker (T-502) will show ("HTTP", "SSH", "Ping", ...) are what a user
// reaches for here too ("can this guest reach that one on HTTP?").
import type { MacroView } from "../api/types";

export interface ServicePreset {
  name: string;
  comment?: string;
  proto?: string;
  /** undefined for a preset with no port at all (e.g. ICMP/Ping) or whose
   * port spec isn't a single plain integer (a range/list) — the simulator
   * only ever asks about one destination port, so a multi-port macro's
   * *first* pair is used as the representative preset, and only when it
   * parses cleanly (never a silently wrong guess). */
  port?: number;
}

/** Parses a RuleView/MacroPortView `dport` string ("80", "465,587",
 * "1024:65535") into the single leading port number, or undefined if it
 * isn't a plain integer (a list/range — not representable by the
 * simulator's single `port` field). */
function firstPlainPort(dport: string | undefined): number | undefined {
  if (!dport) return undefined;
  const n = Number(dport);
  return Number.isInteger(n) && n > 0 ? n : undefined;
}

/** Converts one macro into a preset. Multi-entry macros (e.g. DNS's
 * udp+tcp/53) use their first entry as the representative proto/port —
 * documented, not hidden: the preset's own `comment` still names the full
 * macro so a user picking "DNS" isn't surprised the request only asks
 * about one of the two protocols. */
export function macroToPreset(macro: MacroView): ServicePreset {
  const first = macro.ports[0];
  return {
    name: macro.name,
    comment: macro.comment,
    proto: first?.proto,
    port: firstPlainPort(first?.dport),
  };
}

/** Every macro turned into a preset, alphabetically as the server already
 * sorts internal/fw.KnownMacros. */
export function servicePresetsFromMacros(macros: readonly MacroView[]): ServicePreset[] {
  return macros.map(macroToPreset);
}

/** The literal "any protocol/port" preset — not a macro, but always the
 * first option so clearing back to "no constraint" is one click. */
export const ANY_PRESET: ServicePreset = { name: "Any" };
