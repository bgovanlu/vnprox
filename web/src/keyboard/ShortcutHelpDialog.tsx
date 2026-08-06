import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import { useAllPaletteActions } from "./actions";
import { SHORTCUTS } from "./shortcuts";

export interface ShortcutHelpDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/** The `?` overlay — renders straight from SHORTCUTS (src/keyboard/shortcuts.ts)
 * so this list can never drift from what useKeyboardShortcuts actually
 * wires up. T-903 adds a second section listing every palette action
 * currently registered (src/keyboard/actions.ts) by whichever page(s) are
 * mounted right now — each reachable via the same `⌘K`/`Ctrl+K` binding
 * the first section already documents, so "the palette binding plus at
 * least the four named verbs' shortcuts where bound" (T-903 AC3) is
 * satisfied by construction rather than by hand-listing verbs here that
 * would drift from what's actually registered. */
export function ShortcutHelpDialog({ open, onOpenChange }: ShortcutHelpDialogProps) {
  const paletteActions = useAllPaletteActions();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent aria-describedby="shortcut-help-description">
        <DialogTitle>Keyboard shortcuts</DialogTitle>
        <DialogDescription id="shortcut-help-description">
          Press <kbd className="rounded border px-1">Esc</kbd> to close. For help with what a screen does
          rather than which keys it takes, press <kbd className="rounded border px-1">F1</kbd>.
        </DialogDescription>
        <dl className="mt-4 grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
          {SHORTCUTS.map((shortcut) => (
            <div key={shortcut.id} className="contents">
              <dt>
                <kbd className="rounded border border-slate-300 bg-slate-100 px-1.5 py-0.5 font-mono text-xs dark:border-slate-700 dark:bg-slate-800">
                  {shortcut.keys}
                </kbd>
              </dt>
              <dd className="text-slate-600 dark:text-slate-300">{shortcut.description}</dd>
            </div>
          ))}
        </dl>
        {paletteActions.length > 0 && (
          <>
            <h3 className="mt-4 text-xs font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
              Available now, via the command palette
            </h3>
            <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
              {paletteActions.map((action) => (
                <div key={action.id} className="contents">
                  <dt>
                    <kbd className="rounded border border-slate-300 bg-slate-100 px-1.5 py-0.5 font-mono text-xs dark:border-slate-700 dark:bg-slate-800">
                      ⌘K
                    </kbd>
                  </dt>
                  <dd className="text-slate-600 dark:text-slate-300">{action.label}</dd>
                </div>
              ))}
            </dl>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
