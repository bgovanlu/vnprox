// Mounts the single management-redundancy wizard instance app-wide, driven
// by mgmtWizardStore — every launch point (finding, inspector, New menu)
// just calls the store's open(), the same one-instance pattern
// EditorLauncher uses for the entity editors.
import { MgmtRedundancyWizard } from "./MgmtRedundancyWizard";
import { useMgmtWizardStore } from "./mgmtWizardStore";

export function MgmtWizardHost() {
  const request = useMgmtWizardStore((s) => s.request);
  const close = useMgmtWizardStore((s) => s.close);

  if (!request) return null;
  return (
    <MgmtRedundancyWizard
      // Remount on node change so all per-run wizard state resets.
      key={request.node}
      node={request.node}
      open
      onOpenChange={(o) => {
        if (!o) close();
      }}
    />
  );
}
