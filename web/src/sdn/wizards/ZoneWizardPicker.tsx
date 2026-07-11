// Entry point for the five guided zone wizards (docs/features/sdn.md §2:
// "One wizard per zone type"). Picking a card mounts that wizard's own
// dialog; closing/cancelling either just clears `active` back to
// undefined — no server-side state exists to clean up either way (see
// WizardShell.tsx's doc comment on acceptance criterion 5).
import { useState } from "react";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../../components/Dialog";
import { EvpnZoneWizard } from "./EvpnZoneWizard";
import { QinqZoneWizard } from "./QinqZoneWizard";
import { SimpleZoneWizard } from "./SimpleZoneWizard";
import { wizardStrings } from "./strings";
import { VlanZoneWizard } from "./VlanZoneWizard";
import { VxlanZoneWizard } from "./VxlanZoneWizard";

type WizardKind = "simple" | "vlan" | "qinq" | "vxlan" | "evpn";

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
}

export function ZoneWizardPicker({ open, onOpenChange }: ZoneWizardPickerProps) {
  const [active, setActive] = useState<WizardKind | undefined>(undefined);

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
                <div className="mt-0.5 line-clamp-2 text-xs text-slate-500 dark:text-slate-400">{card.blurb}</div>
              </button>
            ))}
          </div>
        </DialogContent>
      </Dialog>

      {active === "simple" && <SimpleZoneWizard open onOpenChange={closeAll} />}
      {active === "vlan" && <VlanZoneWizard open onOpenChange={closeAll} />}
      {active === "qinq" && <QinqZoneWizard open onOpenChange={closeAll} />}
      {active === "vxlan" && <VxlanZoneWizard open onOpenChange={closeAll} />}
      {active === "evpn" && <EvpnZoneWizard open onOpenChange={closeAll} />}
    </>
  );
}
