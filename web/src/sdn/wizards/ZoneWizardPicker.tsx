// SPDX-License-Identifier: Apache-2.0

// Entry point for the five guided zone wizards (docs/features/sdn.md §2:
// "One wizard per zone type"). Picking a card mounts that wizard's own
// dialog; closing/cancelling either just clears `active` back to
// undefined — no server-side state exists to clean up either way (see
// WizardShell.tsx's doc comment on acceptance criterion 5).
import { useEffect, useState } from "react";
import { Button } from "../../components/Button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../../components/Dialog";
import { ErrorBoundary } from "../../components/ErrorBoundary";
import { EvpnZoneWizard } from "./EvpnZoneWizard";
import { QinqZoneWizard } from "./QinqZoneWizard";
import { SimpleZoneWizard } from "./SimpleZoneWizard";
import { wizardStrings } from "./strings";
import { VlanZoneWizard } from "./VlanZoneWizard";
import { VxlanZoneWizard } from "./VxlanZoneWizard";

export type WizardKind = "simple" | "vlan" | "qinq" | "vxlan" | "evpn";

const CARDS: { kind: WizardKind; title: string; blurb: string }[] = [
  { kind: "simple", title: wizardStrings.simple.title, blurb: wizardStrings.simple.intro },
  { kind: "vlan", title: wizardStrings.vlan.title, blurb: wizardStrings.vlan.intro },
  { kind: "qinq", title: wizardStrings.qinq.title, blurb: wizardStrings.qinq.intro },
  { kind: "vxlan", title: wizardStrings.vxlan.title, blurb: wizardStrings.vxlan.intro },
  { kind: "evpn", title: wizardStrings.evpn.title, blurb: wizardStrings.evpn.intro },
];

export interface ZoneWizardPickerProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** T-903: when set, opening the picker jumps straight to that wizard
   * instead of showing the type-picker grid — the command palette's "New
   * VLAN zone" verb (SdnPage.tsx) uses this to skip a step a user who
   * already named their intent via the palette shouldn't have to repeat.
   * The toolbar's own "+ New zone (guided)" button leaves this undefined,
   * so it's unaffected and still shows the grid. */
  initialActive?: WizardKind;
}

export function ZoneWizardPicker({ open, onOpenChange, initialActive }: ZoneWizardPickerProps) {
  const [active, setActive] = useState<WizardKind | undefined>(undefined);

  // Only re-applies `initialActive` on the open transition itself (not on
  // every render) — once the user is inside a wizard, this component's own
  // `active` state owns which one is showing, e.g. after "Close" clears it
  // back to the grid.
  useEffect(() => {
    if (open) setActive(initialActive);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deliberately excludes `initialActive` from re-triggering while already open
  }, [open]);

  function closeAll(): void {
    setActive(undefined);
    onOpenChange(false);
  }

  return (
    <>
      <Dialog
        open={open && active === undefined}
        onOpenChange={(o) => {
          if (!o) onOpenChange(false);
        }}
      >
        <DialogContent widthClassName="max-w-2xl" aria-describedby="zone-wizard-picker-description">
          <DialogTitle>{wizardStrings.picker.title}</DialogTitle>
          <DialogDescription id="zone-wizard-picker-description">{wizardStrings.picker.description}</DialogDescription>
          <div className="mt-4 grid grid-cols-1 gap-2 sm:grid-cols-2">
            {CARDS.map((card) => (
              <button
                key={card.kind}
                type="button"
                className="rounded-lg border border-slate-200 p-3 text-left text-sm transition-colors hover:border-accent-500 hover:bg-accent-50 dark:border-slate-700 dark:hover:border-accent-500 dark:hover:bg-accent-950"
                onClick={() => {
                  setActive(card.kind);
                }}
              >
                <div className="font-medium text-slate-800 dark:text-slate-100">{card.title}</div>
                <div className="mt-0.5 line-clamp-2 text-xs text-fg-subtle">{card.blurb}</div>
              </button>
            ))}
          </div>
        </DialogContent>
      </Dialog>

      {/* Safety net: if a wizard crashes outright (not just its preview),
          show a recoverable message instead of blanking the entire app. */}
      {active !== undefined && (
        <ErrorBoundary
          label={`zone-wizard:${active}`}
          fallback={
            <Dialog open onOpenChange={(o) => { if (!o) closeAll(); }}>
              <DialogContent aria-describedby="zone-wizard-error-description">
                <DialogTitle>Something went wrong</DialogTitle>
                <DialogDescription id="zone-wizard-error-description">
                  This wizard hit an unexpected error and couldn&apos;t finish rendering. Close it and try again, or
                  use a different zone type.
                </DialogDescription>
                <div className="mt-4 flex justify-end">
                  <Button variant="secondary" size="sm" onClick={closeAll}>
                    Close
                  </Button>
                </div>
              </DialogContent>
            </Dialog>
          }
        >
          {active === "simple" && <SimpleZoneWizard open onOpenChange={closeAll} />}
          {active === "vlan" && <VlanZoneWizard open onOpenChange={closeAll} />}
          {active === "qinq" && <QinqZoneWizard open onOpenChange={closeAll} />}
          {active === "vxlan" && <VxlanZoneWizard open onOpenChange={closeAll} />}
          {active === "evpn" && <EvpnZoneWizard open onOpenChange={closeAll} />}
        </ErrorBoundary>
      )}
    </>
  );
}
