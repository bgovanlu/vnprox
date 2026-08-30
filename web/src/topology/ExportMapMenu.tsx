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
import { resolveSceneTheme, documentTokenReader } from "./canvasPalette";
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

  // T-4301 remainder: resolve the palette off the live document at click
  // time, so the exported file carries the colours the user was looking at —
  // including demo mode, which `html.demo` re-points and which no
  // light/dark boolean could have expressed.
  //
  // Resolving from the document is exactly right here and it is worth saying
  // why, since it would be wrong in general: `theme` is not a free choice, it
  // is `useThemeStore`'s current value threaded down from TopologyPage, so it
  // always equals the document's own theme. There is no "export the dark
  // version of a light page" path to get wrong.
  //
  // At click time and not in a memo: an export is a once-in-a-while action,
  // and `getComputedStyle` is the style recalculation canvasPalette.ts warns
  // against putting anywhere that repeats.
  function paletteNow(): RenderSvgOptions["palette"] {
    return resolveSceneTheme(documentTokenReader(document.documentElement), theme === "dark");
  }

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
    const opts: RenderSvgOptions = { captionLines, palette: paletteNow() };
    downloadSvg(scene, opts);
  }

  function handlePng(): void {
    const scene = sceneOrWarn();
    if (!scene) return;
    const opts: RenderSvgOptions = { captionLines, palette: paletteNow() };
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
          className="z-50 min-w-[10rem] rounded-md border border-border bg-white p-1 shadow-lg dark:bg-slate-900"
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
