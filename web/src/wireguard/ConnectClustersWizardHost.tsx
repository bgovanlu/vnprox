// Mounts the single "connect two clusters" wizard instance app-wide,
// driven by wgWizardStore — mirrors mgmt/MgmtWizardHost.tsx's identical
// one-instance pattern exactly.
import { ConnectClustersWizard } from "./ConnectClustersWizard";
import { useWgWizardStore } from "./wgWizardStore";

export function ConnectClustersWizardHost() {
  const request = useWgWizardStore((s) => s.request);
  const close = useWgWizardStore((s) => s.close);

  if (!request) return null;
  return (
    <ConnectClustersWizard
      // Remount on source-node change so all per-run wizard state (and the
      // generated tunnel id — see ConnectClustersWizard.tsx) resets.
      key={request.sourceNode ?? ""}
      initialSourceNode={request.sourceNode}
      open
      onOpenChange={(o) => {
        if (!o) close();
      }}
    />
  );
}
