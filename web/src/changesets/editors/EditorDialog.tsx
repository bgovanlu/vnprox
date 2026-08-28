// SPDX-License-Identifier: Apache-2.0

// Shared chrome for every entity editor (docs/features/change-management.md
// §5: "Form-based editors open from the map or list views; every field has
// inline help written for non-networking-experts"). Handles the Dialog
// mount/title/description/footer so BridgeEditor/BondEditor/VlanEditor/
// InterfaceEditor only need to supply their own fields + submit handler.
import type { ReactNode } from "react";
import { Button } from "../../components/Button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../../components/Dialog";
import { Tooltip } from "../../components/Tooltip";

export interface EditorDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  children: ReactNode;
  onSubmit: () => void;
  submitLabel?: string;
  /** When set, the submit button is disabled and this tooltip explains why
   * (T-207 acceptance criterion 4: capability-gated affordances always
   * carry an explanatory tooltip, not just a bare disabled state). */
  disabledReason?: string;
  /** Error-severity validation findings not attributable to any single
   * form field (findingFields.editorFindingsFor's `general` bucket) —
   * rendered in a banner above the fields. Field-attributable errors go on
   * each <Field>'s own `errors` prop instead (T-207 acceptance criterion 2:
   * "the originating form field"). */
  generalErrors?: string[];
}

/** Inline plain-English help line under a field label (docs/features/
 * change-management.md §5: "inline help written for non-networking-experts,
 * e.g. bond modes explained with 'use this when...'"). */
export function FieldHelp({ children }: { children: ReactNode }) {
  return <p className="mt-0.5 text-[11px] text-fg-subtle">{children}</p>;
}

export function EditorDialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  onSubmit,
  submitLabel = "Add to changeset",
  disabledReason,
  generalErrors,
}: EditorDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] max-w-lg overflow-y-auto" aria-describedby={description ? "editor-description" : undefined}>
        <DialogTitle>{title}</DialogTitle>
        {description && <DialogDescription id="editor-description">{description}</DialogDescription>}
        {generalErrors && generalErrors.length > 0 && (
          <div
            role="alert"
            className="mt-3 rounded-md border border-red-300 bg-red-50 p-2 text-xs text-red-800 dark:border-red-700 dark:bg-red-950 dark:text-red-200"
          >
            <ul className="space-y-0.5">
              {generalErrors.map((msg, i) => (
                <li key={i}>{msg}</li>
              ))}
            </ul>
          </div>
        )}
        <div className="mt-3 space-y-3 text-sm">{children}</div>
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={() => { onOpenChange(false); }}>
            Cancel
          </Button>
          <Tooltip content={disabledReason}>
            <span>
              <Button variant="primary" size="sm" disabled={disabledReason !== undefined} onClick={onSubmit}>
                {submitLabel}
              </Button>
            </span>
          </Tooltip>
        </div>
      </DialogContent>
    </Dialog>
  );
}

export function Field({
  label,
  help,
  errors,
  children,
}: {
  label: string;
  help?: ReactNode;
  /** Error-severity validation messages that originated from this field
   * (findingFields.editorFindingsFor's `byField` bucket). */
  errors?: string[];
  children: ReactNode;
}) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium text-slate-600 dark:text-slate-300">{label}</span>
      {children}
      {errors && errors.length > 0 && (
        <ul className="mt-0.5 space-y-0.5 text-[11px] text-red-600 dark:text-red-400" role="alert">
          {errors.map((msg, i) => (
            <li key={i}>{msg}</li>
          ))}
        </ul>
      )}
      {help && <FieldHelp>{help}</FieldHelp>}
    </label>
  );
}

export const inputClass =
  "w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-800";
