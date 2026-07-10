// Renders whichever entity editor useEditorLauncherStore currently
// requests, wiring it up with the topology-derived candidate lists
// (entityCandidates.ts) and the target's own EntityDetail. One component,
// mounted once (in TopologyPage — editors open from the map or the
// inspector panel, both there), rather than every call site constructing
// its own editor + candidate-fetch plumbing.
import { useQueries } from "@tanstack/react-query";
import { fetchInventoryDetail } from "../api/topology";
import { useInventoryDetailQuery, useTopologyQuery } from "../topology/queries";
import { BondEditor, type SlaveCandidate } from "./editors/BondEditor";
import { BridgeDeleteDialog } from "./editors/BridgeDeleteDialog";
import { BridgeEditor } from "./editors/BridgeEditor";
import { InterfaceEditor } from "./editors/InterfaceEditor";
import { VlanEditor } from "./editors/VlanEditor";
import {
  attachedGuestNics,
  bondSlaveCandidates,
  bridgePortCandidates,
  reattachTargets,
  vlanParentCandidates,
} from "./entityCandidates";
import { refId } from "./opSummary";
import { useEditorLauncherStore } from "./editorLauncherStore";

/** Fetches each bond-slave candidate's own EntityDetail (in parallel) so
 * the picker can show real link state/speed (docs/features/
 * change-management.md §5), falling back to the map's own coarse
 * up/down status until each detail resolves. */
function useSlaveCandidatesWithDetail(node: string, open: boolean): SlaveCandidate[] {
  const { data: topology } = useTopologyQuery();
  const base = topology ? bondSlaveCandidates(topology, node) : [];
  const details = useQueries({
    queries: base.map((c) => ({
      queryKey: ["inventory", c.ref],
      queryFn: () => fetchInventoryDetail(c.ref),
      enabled: open,
      staleTime: 10_000,
    })),
  });
  return base.map((c, i) => {
    const detail = details[i]?.data;
    const speedMbps = typeof detail?.fields.speedMbps === "number" ? detail.fields.speedMbps : 0;
    const linkUp = typeof detail?.fields.linkUp === "boolean" ? detail.fields.linkUp : c.status !== "down";
    return { name: c.name, label: c.label, linkUp, speedMbps, alreadyEnslaved: c.alreadyEnslaved };
  });
}

export function EditorLauncher() {
  const request = useEditorLauncherStore((s) => s.request);
  const close = useEditorLauncherStore((s) => s.close);
  const { data: topology } = useTopologyQuery();
  const { data: existing } = useInventoryDetailQuery(request?.target);

  const slaveCandidates = useSlaveCandidatesWithDetail(request?.node ?? "", request?.kind === "bond");

  if (!request || !topology) return null;

  const onOpenChange = (open: boolean) => {
    if (!open) close();
  };

  switch (request.kind) {
    case "bridge":
      return (
        <BridgeEditor
          open
          onOpenChange={onOpenChange}
          node={request.node}
          target={request.target}
          existing={existing}
          candidatePorts={bridgePortCandidates(topology, request.node, request.target)}
        />
      );
    case "bridge-delete": {
      if (!request.target) return null;
      const nodesById = new Map(topology.nodes.map((n) => [n.id, n]));
      return (
        <BridgeDeleteDialog
          open
          onOpenChange={onOpenChange}
          node={request.node}
          target={request.target}
          bridgeName={refId(request.target)}
          attachedGuestNics={attachedGuestNics(topology.edges, nodesById, request.target)}
          reattachTargets={reattachTargets(topology, request.node, request.target)}
        />
      );
    }
    case "bond":
      return (
        <BondEditor
          open
          onOpenChange={onOpenChange}
          node={request.node}
          target={request.target}
          existing={existing}
          candidateSlaves={slaveCandidates}
        />
      );
    case "vlan":
      return (
        <VlanEditor
          open
          onOpenChange={onOpenChange}
          node={request.node}
          target={request.target}
          existing={existing}
          candidateParents={vlanParentCandidates(topology, request.node)}
        />
      );
    case "iface":
      if (!request.target) return null;
      return <InterfaceEditor open onOpenChange={onOpenChange} node={request.node} target={request.target} existing={existing} />;
    default:
      return null;
  }
}
