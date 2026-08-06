// The step engine every zone wizard is built on: a left-hand column with
// the current step's fields (plus a plain-English "what this actually
// does" intro, docs/features/sdn.md §2), a right-hand live preview pane
// (WizardPreviewPane), and Back/Next/Cancel/Create-draft navigation.
//
// Nothing here ever touches the changeset drawer — `onFinish` is the only
// place a wizard is allowed to draft ops (see each wizard component's own
// `handleFinish`), and Cancel/closing the dialog just discards this
// component's own local state via unmount, which is how T-403 acceptance
// criterion 5 ("abandoning a wizard leaves no draft residue") holds: there
// is nothing to clean up because nothing was ever sent to the server until
// the user reaches the last step and clicks Create draft.
import { useEffect, useState, type ReactNode } from "react";
import { Button } from "../../components/Button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../../components/Dialog";
import { ErrorBoundary } from "../../components/ErrorBoundary";
import { wizardStrings } from "./strings";

export interface WizardStep {
  id: string;
  title: string;
  content: ReactNode;
  /** Disables "Next"/"Create draft" with this reason shown when false. */
  isValid: boolean;
  invalidReason?: string;
}

export interface WizardShellProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  intro: string;
  steps: WizardStep[];
  preview: ReactNode;
  onFinish: () => void;
  finishing?: boolean;
}

export function WizardShell({ open, onOpenChange, title, intro, steps, preview, onFinish, finishing }: WizardShellProps) {
  const [stepIndex, setStepIndex] = useState(0);

  // Reset to the first step every time the wizard (re)opens — a
  // previously-abandoned run never leaves the next run mid-flight.
  useEffect(() => {
    if (open) setStepIndex(0);
  }, [open]);

  const step = steps[stepIndex];
  const isLast = stepIndex === steps.length - 1;
  const isFirst = stepIndex === 0;

  function handleNext(): void {
    if (isLast) {
      onFinish();
      return;
    }
    setStepIndex((i) => Math.min(i + 1, steps.length - 1));
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        widthClassName="max-w-6xl"
        className="max-h-[90vh] overflow-y-auto"
        aria-describedby="wizard-shell-description"
      >
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription id="wizard-shell-description">{intro}</DialogDescription>

        <div className="mt-3 flex flex-wrap items-center gap-1 text-xs text-slate-500 dark:text-slate-400">
          {steps.map((s, i) => (
            <span key={s.id} className="flex items-center gap-1">
              {i > 0 && <span aria-hidden="true">→</span>}
              <span
                className={
                  i === stepIndex
                    ? "font-semibold text-slate-800 dark:text-slate-100"
                    : i < stepIndex
                      ? "text-slate-600 line-through decoration-1 dark:text-slate-400"
                      : ""
                }
              >
                {s.title}
              </span>
            </span>
          ))}
        </div>

        <div className="mt-4 grid grid-cols-1 gap-4 lg:grid-cols-2">
          <div className="min-w-0 space-y-3 text-sm" data-testid="wizard-step-content">
            {step?.content}
          </div>
          <div className="min-w-0" style={{ height: "26rem" }}>
            {/* The preview drives the real React Flow canvas; if it throws
                (a graph shape the headless tests never render), degrade to a
                note rather than blanking the whole wizard — the form on the
                left stays fully usable. */}
            <ErrorBoundary
              label="wizard-preview"
              fallback={
                <div className="flex h-full items-center justify-center rounded-lg border border-slate-200 p-4 text-center text-xs text-slate-400 dark:border-slate-800">
                  Preview unavailable for this configuration — the form still works; continue to draft your changes.
                </div>
              }
            >
              {preview}
            </ErrorBoundary>
          </div>
        </div>

        <p className="mt-4 text-[11px] text-slate-500 dark:text-slate-400">{wizardStrings.common.draftNotice}</p>

        <div className="mt-3 flex items-center justify-between gap-2">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              onOpenChange(false);
            }}
          >
            {wizardStrings.common.cancelButton}
          </Button>
          <div className="flex items-center gap-2">
            {!isFirst && (
              <Button
                variant="secondary"
                size="sm"
                onClick={() => {
                  setStepIndex((i) => Math.max(i - 1, 0));
                }}
              >
                {wizardStrings.common.backButton}
              </Button>
            )}
            <Button
              variant="primary"
              size="sm"
              disabled={!step?.isValid || finishing}
              title={!step?.isValid ? step?.invalidReason : undefined}
              onClick={handleNext}
            >
              {isLast ? wizardStrings.common.finishButton : wizardStrings.common.nextButton}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
