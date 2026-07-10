// Which entity editor (if any) is currently requested to open, and with
// what target — the map (drag-drop, "new" toolbar) and the inspector panel
// (Edit/Delete buttons) both funnel through this instead of each owning
// their own editor-open boolean, so exactly one editor can be open at a
// time app-wide (matching the modal Dialog each editor renders).
import { create } from "zustand";

export type EditorKind = "bridge" | "bridge-delete" | "bond" | "vlan" | "iface";

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
