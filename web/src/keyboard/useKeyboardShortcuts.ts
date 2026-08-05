import { useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { useToast } from "../components/Toast";
import { GOTO_CHORD_KEYS, SHORTCUTS } from "./shortcuts";
import { useTopologyShortcutTargetStore } from "./topologyShortcutTarget";

const CHORD_TIMEOUT_MS = 1200;

function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || target.isContentEditable;
}

/**
 * Wires up the global keyboard bindings from docs/user-guide.md §6.
 * Mount once, near the app root (see src/layout/AppShell.tsx).
 *
 * Navigation (`g` + t/s/f/i), `?` (help), and `⌘K`/`Ctrl+K` (command
 * palette) always do something real. The topology-specific bindings (`/`,
 * `1`-`4`, `f`) are dispatched to whichever handlers the Topology page has
 * registered via src/keyboard/topologyShortcutTarget.ts; when nothing is
 * registered (any other route), they show a toast explaining the shortcut
 * only works on the Topology view, rather than silently doing nothing.
 */
export function useKeyboardShortcuts(options: {
  onOpenHelp: () => void;
  onOpenPalette: () => void;
  /** T-2204: `F1` — contextual online help for the current screen. Distinct
   * from `?`/onOpenHelp, which keeps its existing meaning (the keyboard
   * shortcut list). */
  onOpenPageHelp: () => void;
}): void {
  const navigate = useNavigate();
  const { toast } = useToast();
  const pendingChord = useRef<string | undefined>(undefined);
  const chordTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Keep the latest callbacks in refs so the window listener effect
  // below doesn't need to re-run (and re-attach) on every render.
  const optionsRef = useRef(options);
  optionsRef.current = options;
  const navigateRef = useRef(navigate);
  navigateRef.current = navigate;
  const toastRef = useRef(toast);
  toastRef.current = toast;

  useEffect(() => {
    function clearChord(): void {
      pendingChord.current = undefined;
      if (chordTimer.current) {
        clearTimeout(chordTimer.current);
        chordTimer.current = undefined;
      }
    }

    function handleKeyDown(event: KeyboardEvent): void {
      if (event.defaultPrevented) return;

      // T-903: ⌘K (mac) / Ctrl+K (everywhere else) opens the command
      // palette. Checked before the "ignore any modified keystroke" guard
      // below (which exists for every *other* binding in this file, all of
      // which are deliberately unmodified single keys/chords) since this
      // is the one binding that's defined *by* its modifier. Fires
      // regardless of focus (including inside a text field), matching the
      // conventional cross-app meaning of this combo — a command palette
      // is meant to be reachable from anywhere, including mid-edit.
      const isPaletteChord = (event.metaKey || event.ctrlKey) && !event.altKey && event.key.toLowerCase() === "k";
      if (isPaletteChord) {
        event.preventDefault();
        optionsRef.current.onOpenPalette();
        return;
      }

      // T-2204: F1 opens contextual help for the current screen. Checked
      // alongside the palette chord, above the "unmodified single key"
      // guards below, for the same reason: a function key can't collide
      // with typing, so help stays reachable mid-edit — which is exactly
      // when someone is most likely to want it (halfway through a form
      // they don't understand).
      if (event.key === "F1") {
        event.preventDefault();
        optionsRef.current.onOpenPageHelp();
        return;
      }

      if (event.ctrlKey || event.altKey || event.metaKey) return;
      if (isEditableTarget(event.target)) return;

      if (pendingChord.current === "g") {
        clearChord();
        const shortcutId = GOTO_CHORD_KEYS.get(event.key);
        if (shortcutId) {
          const shortcut = SHORTCUTS.find((s) => s.id === shortcutId);
          if (shortcut?.action.type === "navigate") {
            event.preventDefault();
            void navigateRef.current(shortcut.action.path);
          }
        }
        // Any other second key: silently drop the chord (per this hook's
        // doc comment — simplest correct behavior for an undocumented
        // combination) rather than also reprocessing it as a fresh
        // single-key shortcut.
        return;
      }

      if (event.key === "g") {
        pendingChord.current = "g";
        chordTimer.current = setTimeout(clearChord, CHORD_TIMEOUT_MS);
        return;
      }

      if (event.key === "?") {
        event.preventDefault();
        optionsRef.current.onOpenHelp();
        return;
      }

      const shortcut = SHORTCUTS.find((s) => s.keys === event.key);
      if (!shortcut) return;

      if (shortcut.action.type === "placeholder") {
        event.preventDefault();
        toastRef.current({
          title: `${shortcut.action.feature} — not yet implemented`,
          description: `The "${shortcut.keys}" shortcut is wired up, but this feature is scaffolding only so far.`,
        });
        return;
      }

      if (
        shortcut.action.type === "topology-toggle-layer" ||
        shortcut.action.type === "topology-vlan-filter" ||
        shortcut.action.type === "topology-search"
      ) {
        const target = useTopologyShortcutTargetStore.getState().target;
        if (!target) {
          event.preventDefault();
          toastRef.current({
            title: `"${shortcut.keys}" only works on the Topology view`,
            description: "Go to Topology (press \"g\" then \"t\") and try again.",
          });
          return;
        }
        event.preventDefault();
        switch (shortcut.action.type) {
          case "topology-toggle-layer":
            target.toggleLayer(shortcut.action.layer);
            break;
          case "topology-vlan-filter":
            target.openVlanFilter();
            break;
          case "topology-search":
            target.openSearch();
            break;
        }
      }
    }

    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
      clearChord();
    };
  }, []);
}
