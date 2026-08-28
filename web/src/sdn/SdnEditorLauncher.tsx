// SPDX-License-Identifier: Apache-2.0

// Renders whichever SDN editor sdnEditorStore currently requests — mounted
// once in SdnPage, mirroring changesets/EditorLauncher.tsx's dispatcher
// pattern for the topology editors.
import { attachedGuestNics } from "../changesets/entityCandidates";
import { buildSdnSubnetDeleteOp, buildSdnZoneDeleteOp } from "../changesets/opBuilders";
import { useTopologyQuery } from "../topology/queries";
import { SdnConfirmDeleteDialog } from "./editors/SdnConfirmDeleteDialog";
import { SdnSubnetEditor } from "./editors/SdnSubnetEditor";
import { SdnVnetDeleteDialog } from "./editors/SdnVnetDeleteDialog";
import { SdnVnetEditor } from "./editors/SdnVnetEditor";
import { SdnZoneEditor } from "./editors/SdnZoneEditor";
import { vnetReattachTargets } from "./sdnCandidates";
import { useSdnEditorStore } from "./sdnEditorStore";

export function SdnEditorLauncher() {
  const request = useSdnEditorStore((s) => s.request);
  const close = useSdnEditorStore((s) => s.close);
  const { data: topology } = useTopologyQuery();

  if (!request) return null;
  const onOpenChange = (open: boolean) => {
    if (!open) close();
  };

  switch (request.kind) {
    case "zone-create":
      return <SdnZoneEditor open onOpenChange={onOpenChange} />;
    case "zone-edit":
      return <SdnZoneEditor open onOpenChange={onOpenChange} existing={request.zone} />;
    case "zone-delete":
      return (
        <SdnConfirmDeleteDialog
          open
          onOpenChange={onOpenChange}
          title={`Delete SDN zone ${request.zone.id}`}
          description="Every VNet and subnet inside this zone is removed too, once applied."
          op={buildSdnZoneDeleteOp(`sdn-zone::${request.zone.id}`)}
          changesetTitle={`Delete sdn zone ${request.zone.id}`}
        />
      );
    case "vnet-create":
      return <SdnVnetEditor open onOpenChange={onOpenChange} zoneId={request.zoneId} />;
    case "vnet-edit":
      return <SdnVnetEditor open onOpenChange={onOpenChange} zoneId={request.vnet.zone} existing={request.vnet} />;
    case "vnet-delete": {
      const target = `sdn-vnet::${request.vnet.id}`;
      const nodesById = new Map((topology?.nodes ?? []).map((n) => [n.id, n]));
      return (
        <SdnVnetDeleteDialog
          open
          onOpenChange={onOpenChange}
          target={target}
          vnetId={request.vnet.id}
          attachedGuestNics={topology ? attachedGuestNics(topology.edges, nodesById, target) : []}
          reattachTargets={topology ? vnetReattachTargets(topology.nodes, request.vnet.id) : []}
        />
      );
    }
    case "subnet-create":
      return <SdnSubnetEditor open onOpenChange={onOpenChange} vnetId={request.vnetId} />;
    case "subnet-edit":
      return <SdnSubnetEditor open onOpenChange={onOpenChange} vnetId={request.subnet.vnet} existing={request.subnet} />;
    case "subnet-delete":
      return (
        <SdnConfirmDeleteDialog
          open
          onOpenChange={onOpenChange}
          title={`Delete subnet ${request.subnet.cidr || request.subnet.id}`}
          description="Any IPAM allocations in this subnet are orphaned once applied."
          op={buildSdnSubnetDeleteOp(`sdn-subnet::${request.subnet.id}`)}
          changesetTitle={`Delete sdn subnet ${request.subnet.id}`}
        />
      );
    default:
      return null;
  }
}
