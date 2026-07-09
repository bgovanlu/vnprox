import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import { SHORTCUTS } from "./shortcuts";

export interface ShortcutHelpDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/** The `?` overlay — renders straight from SHORTCUTS (src/keyboard/shortcuts.ts)
 * so this list can never drift from what useKeyboardShortcuts actually
 * wires up. */
export function ShortcutHelpDialog({ open, onOpenChange }: ShortcutHelpDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent aria-describedby="shortcut-help-description">
        <DialogTitle>Keyboard shortcuts</DialogTitle>
        <DialogDescription id="shortcut-help-description">
          Press <kbd className="rounded border px-1">Esc</kbd> to close.
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
      </DialogContent>
    </Dialog>
  );
}
