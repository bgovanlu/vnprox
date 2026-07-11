// Pure formatting helpers shared by the rule/resolved-view tables — kept
// separate from the presentational components so they're trivially unit
// testable without rendering React.
import type { MacroPortView, RuleView } from "../api/types";

/** A one-line human summary of what a rule matches on (proto/ports,
 * source/dest, interface) — everything except its action/comment, which
 * the table renders in their own columns. */
export function ruleMatchLabel(r: RuleView): string {
  const parts: string[] = [];
  if (r.macro) {
    parts.push(r.macro);
  } else if (r.proto) {
    parts.push(r.dport ? `${r.proto}/${r.dport}` : r.proto);
  } else if (r.dport) {
    parts.push(`port ${r.dport}`);
  }
  if (r.source) parts.push(`from ${r.source}`);
  if (r.dest) parts.push(`to ${r.dest}`);
  if (r.iface) parts.push(`on ${r.iface}`);
  return parts.length > 0 ? parts.join(" ") : "any";
}

/** One macro port pair rendered as "tcp/80" (or just the protocol when it
 * carries no port, e.g. icmp). */
export function macroPortLabel(p: MacroPortView): string {
  if (!p.proto) return p.dport ?? "";
  return p.dport ? `${p.proto}/${p.dport}` : p.proto;
}

/** The macro expansion preview line (docs/features/firewall.md §2's macro
 * picker "with expansion preview"): "HTTP -> tcp/80". Falls back to a
 * plain "name (unknown macro)" when the build's macro catalog doesn't know
 * it — an honest label rather than silently showing nothing. */
export function macroExpansionLabel(name: string, ports: MacroPortView[] | undefined): string {
  if (!ports || ports.length === 0) {
    return `${name} (unknown macro)`;
  }
  return `${name} → ${ports.map(macroPortLabel).join(", ")}`;
}
