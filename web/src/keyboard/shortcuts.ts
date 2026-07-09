// Single source of truth for the keyboard bindings documented in
// docs/user-guide.md §6: "`/` search · `1–4` toggle layers · `f` VLAN
// filter · `g` then `t/s/f/i` go to Topology/SDN/Firewall/IPAM · `?` full
// list." Both useKeyboardShortcuts (the runtime handler) and
// ShortcutHelpDialog (the `?` overlay) read from this list, so the two
// can never drift out of sync.
//
// The features these shortcuts control (map search, layer visibility,
// VLAN filtering) don't exist yet — per T-005's task card everything
// except navigation and help is wired to a visible "not yet implemented"
// toast rather than a real action.
export type ShortcutAction =
  | { readonly type: "navigate"; readonly path: string }
  | { readonly type: "placeholder"; readonly feature: string }
  | { readonly type: "help" };

export interface ShortcutDef {
  readonly id: string;
  /** Human-readable key combo for display in the help dialog, e.g. "g t". */
  readonly keys: string;
  readonly description: string;
  readonly action: ShortcutAction;
}

export const SHORTCUTS: readonly ShortcutDef[] = [
  { id: "search", keys: "/", description: "Search", action: { type: "placeholder", feature: "Search" } },
  {
    id: "layer-physical",
    keys: "1",
    description: "Toggle physical layer",
    action: { type: "placeholder", feature: "Layer toggle (Physical)" },
  },
  {
    id: "layer-l2",
    keys: "2",
    description: "Toggle L2 layer",
    action: { type: "placeholder", feature: "Layer toggle (L2)" },
  },
  {
    id: "layer-sdn",
    keys: "3",
    description: "Toggle SDN layer",
    action: { type: "placeholder", feature: "Layer toggle (SDN)" },
  },
  {
    id: "layer-guests",
    keys: "4",
    description: "Toggle guests layer",
    action: { type: "placeholder", feature: "Layer toggle (Guests)" },
  },
  {
    id: "vlan-filter",
    keys: "f",
    description: "Filter by VLAN",
    action: { type: "placeholder", feature: "VLAN filter" },
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
  { id: "help", keys: "?", description: "Show this help", action: { type: "help" } },
] as const;

/** The `t/s/f/i` half of a `g` chord, mapped straight to SHORTCUTS'
 * goto-* entries so there's exactly one place that knows the chord's
 * second-key mapping. */
export const GOTO_CHORD_KEYS: ReadonlyMap<string, string> = new Map(
  SHORTCUTS.filter((s) => s.action.type === "navigate" && s.keys.startsWith("g "))
    .map((s) => [s.keys.slice(2), s.id]),
);
