// Which capture dialog (if any) is currently requested to open, and for
// which target — the map's right-click menu and the inspector panel's
// "Capture" button both funnel through this instead of each owning their
// own dialog-open boolean, mirroring changesets/editorLauncherStore.ts's
// exact pattern (one editor open at a time app-wide; here, one capture
// dialog). CaptureDialog is mounted once (TopologyPage.tsx, alongside
// <EditorLauncher />) and reads this store to decide what to render.
import { create } from "zustand";

export interface CaptureRequest {
  /** The inventory Ref of the bridge/bond/guest-NIC/SDN-VNet to capture on. */
  targetRef: string;
  /** The PVE node the target lives on ("" for a cluster-scoped SDN-VNet). */
  node: string;
  /** A human-readable label for the dialog title (falls back to targetRef
   * when the caller doesn't have a friendlier one on hand). */
  label?: string;
}

interface CaptureLauncherState {
  request: CaptureRequest | undefined;
  open: (request: CaptureRequest) => void;
  close: () => void;
}

export const useCaptureLauncherStore = create<CaptureLauncherState>((set) => ({
  request: undefined,
  open: (request) => { set({ request }); },
  close: () => { set({ request: undefined }); },
}));

/** Entity kinds the capture affordance is offered on (docs/api.md's
 * Captures section: "targetRef is the inventory Ref of a bridge/bond/
 * guest-NIC/SDN-VNet to capture on") — the map right-click menu and the
 * inspector's header button both gate on this exact set. */
const CAPTURABLE_KINDS = new Set(["bridge", "ovs-bridge", "bond", "ovs-bond", "guest-nic", "sdn-vnet"]);

export function isCapturableEntityKind(kind: string): boolean {
  return CAPTURABLE_KINDS.has(kind);
}
