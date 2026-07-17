// Single source of truth for the keyboard bindings documented in
// docs/user-guide.md §6: "`/` search · `1–4` toggle layers · `f` VLAN
// filter · `g` then `t/s/f/i` go to Topology/SDN/Firewall/IPAM · `⌘K`/
// `Ctrl+K` command palette · `?` full list." Both useKeyboardShortcuts (the
// runtime handler) and ShortcutHelpDialog (the `?` overlay) read from this
// list, so the two can never drift out of sync.
//
// T-005 wired everything except navigation and help to a "not yet
// implemented" toast, since the features they'd control (search, layer
// visibility, VLAN filtering) didn't exist yet. T-107 (the topology map)
// makes them real: the "topology-*" action types below are dispatched to
// whichever handlers the topology page currently has registered via
// src/keyboard/topologyShortcutTarget.ts — useKeyboardShortcuts falls back
// to a toast when nothing is registered (i.e. the user isn't on the
// Topology view), since these shortcuts are meaningless anywhere else.
//
// T-903: "command-palette" is dispatched specially too, like "help" —
// useKeyboardShortcuts intercepts ⌘K/Ctrl+K before the generic single-key
// SHORTCUTS lookup below even runs (that lookup only ever compares against
// `event.key`, which can't itself encode a modifier), so this entry exists
// purely so ShortcutHelpDialog has one canonical place to read the binding
// from — same "never drift out of sync" reasoning as every other row.
import type { Layer } from "../api/types";

export type ShortcutAction =
  | { readonly type: "navigate"; readonly path: string }
  | { readonly type: "placeholder"; readonly feature: string }
  | { readonly type: "topology-toggle-layer"; readonly layer: Layer }
  | { readonly type: "topology-vlan-filter" }
  | { readonly type: "topology-search" }
  | { readonly type: "command-palette" }
  | { readonly type: "help" };

export interface ShortcutDef {
  readonly id: string;
  /** Human-readable key combo for display in the help dialog, e.g. "g t". */
  readonly keys: string;
  readonly description: string;
  readonly action: ShortcutAction;
}

export const SHORTCUTS: readonly ShortcutDef[] = [
  { id: "search", keys: "/", description: "Search", action: { type: "topology-search" } },
  {
    id: "layer-physical",
    keys: "1",
    description: "Toggle physical layer",
    action: { type: "topology-toggle-layer", layer: "phys" },
  },
  {
    id: "layer-l2",
    keys: "2",
    description: "Toggle L2 layer",
    action: { type: "topology-toggle-layer", layer: "l2" },
  },
  {
    id: "layer-sdn",
    keys: "3",
    description: "Toggle SDN layer",
    action: { type: "topology-toggle-layer", layer: "sdn" },
  },
  {
    id: "layer-guests",
    keys: "4",
    description: "Toggle guests layer",
    action: { type: "topology-toggle-layer", layer: "guest" },
  },
  {
    id: "vlan-filter",
    keys: "f",
    description: "Filter by VLAN",
    action: { type: "topology-vlan-filter" },
  },
  {
    id: "goto-topology",
    keys: "g t",
    description: "Go to Topology",
    action: { type: "navigate", path: "/topology" },
  },
  { id: "goto-sdn", keys: "g s", description: "Go to SDN", action: { type: "navigate", path: "/sdn" } },
  {
    id: "goto-firewall",
    keys: "g f",
    description: "Go to Firewall",
    action: { type: "navigate", path: "/firewall" },
  },
  { id: "goto-ipam", keys: "g i", description: "Go to IPAM", action: { type: "navigate", path: "/ipam" } },
  {
    id: "command-palette",
    keys: "⌘K / Ctrl+K",
    description: "Open command palette",
    action: { type: "command-palette" },
  },
  { id: "help", keys: "?", description: "Show this help", action: { type: "help" } },
] as const;

/** The `t/s/f/i` half of a `g` chord, mapped straight to SHORTCUTS'
 * goto-* entries so there's exactly one place that knows the chord's
 * second-key mapping. */
export const GOTO_CHORD_KEYS: ReadonlyMap<string, string> = new Map(
  SHORTCUTS.filter((s) => s.action.type === "navigate" && s.keys.startsWith("g "))
    .map((s) => [s.keys.slice(2), s.id]),
);
