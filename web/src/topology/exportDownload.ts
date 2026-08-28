// SPDX-License-Identifier: Apache-2.0

// Browser-only glue for T-906's map export: turns an ExportScene (export.ts
// — pure, Vitest-tested) into an actual file download. Deliberately kept
// separate from export.ts per the task card ("a testable module ...
// independent of the browser download mechanism") — everything here touches
// the DOM (Blob, object URLs, an <a download> click, an <img>/<canvas> pair
// for PNG rasterization) and is exercised by the Playwright suite
// (web/e2e/map-export.spec.ts) instead of Vitest/jsdom, which lacks a real
// Image decoder and canvas rasterizer.
import { renderSvg, type ExportScene, type RenderSvgOptions } from "./export";

function triggerBlobDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

/** A timestamped base filename matching the convention docs/api.md's
 * `GET /export/doc` already establishes (`vnprox-network-<timestamp>`), so
 * both export mechanisms read as the same product feature to a user's
 * downloads folder. */
export function exportFilenameBase(): string {
  return `vnprox-map-${new Date().toISOString().replace(/[:.]/g, "-")}`;
}

function svgDimensions(svg: string): { width: number; height: number } {
  const w = /width="([\d.]+)"/.exec(svg);
  const h = /height="([\d.]+)"/.exec(svg);
  return { width: w ? Number(w[1]) : 800, height: h ? Number(h[1]) : 600 };
}

/** Base64-encodes an arbitrary (UTF-8) string for a `data:` URI — avoids
 * `btoa`'s Latin1-only restriction (an entity label can be non-ASCII). Used
 * instead of `URL.createObjectURL` for the rasterization <img> below: this
 * app's CSP is `img-src 'self' data:` (internal/api/middleware.go) with no
 * `blob:` — a blob: URL silently fails to load as an <img> source under
 * that policy, so a `data:` URI is the only CSP-compliant way to feed the
 * SVG into <canvas> for PNG rasterization. */
function toBase64(input: string): string {
  const bytes = new TextEncoder().encode(input);
  let binary = "";
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary);
}

/** Builds the SVG for `scene`, triggers a same-origin file download, and
 * returns the SVG string (so a caller/test can additionally inspect what
 * was downloaded without re-parsing the Blob). */
export function downloadSvg(scene: ExportScene, opts: RenderSvgOptions, filenameBase = exportFilenameBase()): string {
  const svg = renderSvg(scene, opts);
  triggerBlobDownload(new Blob([svg], { type: "image/svg+xml" }), `${filenameBase}.svg`);
  return svg;
}

function loadImage(src: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => {
      resolve(img);
    };
    img.onerror = () => {
      reject(new Error("Failed to rasterize the exported SVG"));
    };
    img.src = src;
  });
}

/** Rasterizes `scene` (via the same renderSvg the SVG export uses, so the
 * two file formats are always the same picture) to a PNG and triggers a
 * download. 2x scale for a legible raster at typical zoom-to-fit map sizes
 * (AC2's "non-empty image blob ... byte-size/dimension sanity check"). */
export async function downloadPng(
  scene: ExportScene,
  opts: RenderSvgOptions,
  filenameBase = exportFilenameBase(),
): Promise<void> {
  const svg = renderSvg(scene, opts);
  const { width, height } = svgDimensions(svg);
  const svgDataUrl = `data:image/svg+xml;base64,${toBase64(svg)}`;
  const img = await loadImage(svgDataUrl);
  const scale = 2;
  const canvas = document.createElement("canvas");
  canvas.width = Math.max(1, Math.round(width * scale));
  canvas.height = Math.max(1, Math.round(height * scale));
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("2D canvas context unavailable for PNG export");
  ctx.scale(scale, scale);
  ctx.drawImage(img, 0, 0, width, height);
  const blob = await new Promise<Blob | null>((resolve) => {
    canvas.toBlob(resolve, "image/png");
  });
  if (!blob) throw new Error("PNG rasterization produced no data");
  triggerBlobDownload(blob, `${filenameBase}.png`);
}
