// SPDX-License-Identifier: Apache-2.0

// T-906: the Topology page toolbar's "Export map" control, present on both
// Graph and Switch views (TopologyPage.tsx wires `getScene` to whichever
// view is current — see export.ts's sceneFromFlowElements/
// sceneFromSwitchTopology). Distinct from ToolsPage.tsx's "Export
// documentation" (GET /export/doc): that exports a prose as-built document
// server-side; this exports the rendered map itself, client-side only —
// see export.ts's header comment.
import * as RadixDropdown from "@radix-ui/react-dropdown-menu";
import { useState } from "react";
import { Button } from "../components/Button";
import { useToast } from "../components/Toast";
import type { ExportScene, RenderSvgOptions } from "./export";
import { downloadPng, downloadSvg } from "./exportDownload";

export interface ExportMapMenuProps {
  /** Computed lazily (only on click) rather than eagerly every render —
   * building the switch-view grid layout or filtering a large FlowElements
   * set is wasted work on every keystroke/pan if nobody ever exports. */
  getScene: () => ExportScene;
  captionLines: string[];
  theme?: "light" | "dark";
}

export function ExportMapMenu({ getScene, captionLines, theme }: ExportMapMenuProps) {
  const { toast } = useToast();
  const [busy, setBusy] = useState(false);

  function sceneOrWarn(): ExportScene | undefined {
    const scene = getScene();
    if (scene.nodes.length === 0) {
      toast({
        title: "Nothing to export",
        description: "No visible entities match the current layer/VLAN filters.",
        variant: "error",
      });
      return undefined;
    }
    return scene;
  }

  function handleSvg(): void {
    const scene = sceneOrWarn();
    if (!scene) return;
    const opts: RenderSvgOptions = { captionLines, theme };
    downloadSvg(scene, opts);
  }

  function handlePng(): void {
    const scene = sceneOrWarn();
    if (!scene) return;
    const opts: RenderSvgOptions = { captionLines, theme };
    setBusy(true);
    downloadPng(scene, opts)
      .catch(() => {
        toast({ title: "PNG export failed", description: "Try the SVG export instead.", variant: "error" });
      })
      .finally(() => {
        setBusy(false);
      });
  }

  return (
    <RadixDropdown.Root>
      <RadixDropdown.Trigger asChild>
        <Button size="sm" variant="secondary" disabled={busy} aria-label="Export map">
          Export map ▾
        </Button>
      </RadixDropdown.Trigger>
      <RadixDropdown.Portal>
        <RadixDropdown.Content
          align="end"
          sideOffset={6}
          className="z-50 min-w-[10rem] rounded-md border border-slate-200 bg-white p-1 shadow-lg dark:border-slate-700 dark:bg-slate-900"
        >
          <RadixDropdown.Item
            className="cursor-pointer rounded px-2 py-1.5 text-sm outline-none hover:bg-slate-100 dark:hover:bg-slate-800"
            onSelect={handleSvg}
          >
            Download SVG
          </RadixDropdown.Item>
          <RadixDropdown.Item
            className="cursor-pointer rounded px-2 py-1.5 text-sm outline-none hover:bg-slate-100 dark:hover:bg-slate-800"
            onSelect={handlePng}
          >
            Download PNG
          </RadixDropdown.Item>
        </RadixDropdown.Content>
      </RadixDropdown.Portal>
    </RadixDropdown.Root>
  );
}
