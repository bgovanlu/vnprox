import { useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { useToast } from "../components/Toast";
import { GOTO_CHORD_KEYS, SHORTCUTS } from "./shortcuts";

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
 * Only navigation (`g` + t/s/f/i) and `?` (help) do something real today;
 * every other binding shows a "not yet implemented" toast, since the
 * features they'd control (search, layer visibility, VLAN filter) don't
 * exist yet — see this hook's task card, T-005, for why.
 */
export function useKeyboardShortcuts(options: { onOpenHelp: () => void }): void {
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
      }
    }

    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
      clearChord();
    };
  }, []);
}
