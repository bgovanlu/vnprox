// Which entity editor (if any) is currently requested to open, and with
// what target — the map (drag-drop, "new" toolbar) and the inspector panel
// (Edit/Delete buttons) both funnel through this instead of each owning
// their own editor-open boolean, so exactly one editor can be open at a
// time app-wide (matching the modal Dialog each editor renders).
import { create } from "zustand";

export type EditorKind = "bridge" | "bridge-delete" | "bond" | "vlan" | "iface" | "iface-rename";

/** Which entity editor (if any) opens for a given inventory kind — shared by
 * the inspector's "Edit" button and the Management page's per-carrier "Edit"
 * button. Kinds with no editor of their own (guests, SDN objects, LLDP
 * neighbors, ...) return undefined (no Edit affordance). */
const EDITOR_KIND_BY_INVENTORY_KIND: Partial<Record<string, EditorKind>> = {
  bridge: "bridge",
  "ovs-bridge": "bridge",
  bond: "bond",
  "ovs-bond": "bond",
  vlan: "vlan",
  physnic: "iface",
};

export function editorKindForInventoryKind(kind: string | undefined): EditorKind | undefined {
  return kind ? EDITOR_KIND_BY_INVENTORY_KIND[kind] : undefined;
}

export interface EditorRequest {
  kind: EditorKind;
  node: string;
  /** Existing entity's Ref string — undefined for "create new". */
  target?: string;
}

interface EditorLauncherState {
  request: EditorRequest | undefined;
  open: (request: EditorRequest) => void;
  close: () => void;
}

export const useEditorLauncherStore = create<EditorLauncherState>((set) => ({
  request: undefined,
  open: (request) => { set({ request }); },
  close: () => { set({ request: undefined }); },
}));
