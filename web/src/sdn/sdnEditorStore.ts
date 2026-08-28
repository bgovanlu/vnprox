// SPDX-License-Identifier: Apache-2.0

// Which SDN editor (if any) is currently open, and with what target — the
// SDN cockpit's own counterpart of changesets/editorLauncherStore.ts, kept
// separate (not reusing that store) because SDN entities are cluster-scoped
// (no node) and every SDN editor opens from exactly one place (SdnPage),
// unlike the topology editors' store, which needs to be shared between the
// map and the inspector panel.
import { create } from "zustand";
import type { SdnSubnet, SdnVnet, SdnZone } from "../api/types";

export type SdnEditorRequest =
  | { kind: "zone-create" }
  | { kind: "zone-edit"; zone: SdnZone }
  | { kind: "zone-delete"; zone: SdnZone }
  | { kind: "vnet-create"; zoneId: string }
  | { kind: "vnet-edit"; vnet: SdnVnet }
  | { kind: "vnet-delete"; vnet: SdnVnet }
  | { kind: "subnet-create"; vnetId: string }
  | { kind: "subnet-edit"; subnet: SdnSubnet }
  | { kind: "subnet-delete"; subnet: SdnSubnet };

interface SdnEditorState {
  request: SdnEditorRequest | undefined;
  open: (request: SdnEditorRequest) => void;
  close: () => void;
}

export const useSdnEditorStore = create<SdnEditorState>((set) => ({
  request: undefined,
  open: (request) => { set({ request }); },
  close: () => { set({ request: undefined }); },
}));
