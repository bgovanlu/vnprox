// SPDX-License-Identifier: Apache-2.0

import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PICTOGRAM_KINDS, PICTOGRAMS, getPictogram, type PictogramKind } from "./registry";
import { UnknownPictogram } from "./UnknownPictogram";

// T-4205's three sizes: ~16px inline icon, ~32-48px canvas node glyph,
// ~96px+ illustration seed. 16 and 40 straddle Icon.tsx's INLINE_THRESHOLD
// (20) so both the simplified and detailed branch of every glyph get
// exercised; 96 re-renders the detailed branch at the illustration-seed
// scale, since that variant is meant to be identical to the node-glyph one
// (only the simplification is size-gated, not the detail level above it).
const SIZES = [16, 40, 96] as const;

/** Every declared kind's full set of glyph modules, for the "no kind was
 * dropped from PICTOGRAMS without the type catching it" assertion below —
 * kept as a literal array (not derived from the Record's keys, which is
 * exactly the thing under test) so a future edit that silently deletes an
 * entry from the `PICTOGRAMS` object literal — while leaving the
 * `PictogramKind` union alone, which `Record<PictogramKind, ...>` would
 * still catch at compile time via `tsc`, but this suite exists so the same
 * mistake is caught at test time too, without relying on a type-check step
 * having run. */
const EXPECTED_KINDS: PictogramKind[] = [
  "node",
  "physnic",
  "bond",
  "ovs-bond",
  "bridge",
  "ovs-bridge",
  "vlan",
  "vxlan",
  "sdn-zone",
  "sdn-vnet",
  "sdn-subnet",
  "sdn-fabric",
  "gateway",
  "guest",
  "guest-nic",
  "lldp-neighbor",
  "wg-tunnel",
  "wg-peer",
  "static-route",
  "fw-ruleset",
  "firewall-group",
  "switch",
  "port",
];

const HARDCODED_COLOR = /#[0-9a-fA-F]{3,8}\b|\brgb\(|\brgba\(|\bhsl\(/;

describe("registry completeness", () => {
  it("declares exactly the expected set of kinds, in both directions", () => {
    expect(new Set(PICTOGRAM_KINDS)).toEqual(new Set(EXPECTED_KINDS));
    expect(PICTOGRAM_KINDS).toHaveLength(EXPECTED_KINDS.length);
  });

  it("has a component for every declared kind", () => {
    for (const kind of EXPECTED_KINDS) {
      expect(typeof PICTOGRAMS[kind]).toBe("function");
    }
  });

  it("falls back to UnknownPictogram for a kind it doesn't cover", () => {
    expect(getPictogram("some-future-kind-nobody-drew-yet")).toBe(UnknownPictogram);
    // And resolves every real kind to its own component, not the fallback.
    for (const kind of EXPECTED_KINDS) {
      expect(getPictogram(kind)).toBe(PICTOGRAMS[kind]);
      expect(getPictogram(kind)).not.toBe(UnknownPictogram);
    }
  });
});

describe.each(EXPECTED_KINDS)("%s pictogram", (kind) => {
  const Component = PICTOGRAMS[kind];

  it.each(SIZES)("renders valid, monochrome SVG at %ipx", (size) => {
    const { container } = render(<Component size={size} />);
    const svg = container.querySelector("svg");
    expect(svg).not.toBeNull();
    if (!svg) return;

    expect(svg.getAttribute("viewBox")).toBe("0 0 24 24");
    expect(svg.getAttribute("width")).toBe(String(size));
    expect(svg.getAttribute("height")).toBe(String(size));
    // currentColor-only contract (T-4205): the shell's own stroke, and
    // every element in the glyph, must never carry a literal color.
    expect(svg.getAttribute("stroke")).toBe("currentColor");
    expect(HARDCODED_COLOR.test(svg.outerHTML)).toBe(false);

    for (const el of Array.from(svg.querySelectorAll("*"))) {
      const fill = el.getAttribute("fill");
      if (fill !== null) {
        expect(["none", "currentColor"]).toContain(fill);
      }
      const stroke = el.getAttribute("stroke");
      if (stroke !== null) {
        expect(["none", "currentColor"]).toContain(stroke);
      }
    }

    // Decorative by default: no title element, aria-hidden, no accessible
    // role competing with an adjacent text label (Icon.tsx's PictogramProps
    // doc comment).
    expect(svg.getAttribute("aria-hidden")).toBe("true");
    expect(svg.querySelector("title")).toBeNull();
  });

  it("renders an accessible name when `title` is given, and only then", () => {
    const { container } = render(<Component size={24} title={`a ${kind}`} />);
    const svg = container.querySelector("svg");
    expect(svg).not.toBeNull();
    if (!svg) return;
    expect(svg.getAttribute("role")).toBe("img");
    expect(svg.getAttribute("aria-hidden")).toBeNull();
    expect(svg.querySelector("title")?.textContent).toBe(`a ${kind}`);
  });
});

describe("UnknownPictogram", () => {
  it("renders the same monochrome SVG contract as every declared kind", () => {
    const { container } = render(<UnknownPictogram size={24} />);
    const svg = container.querySelector("svg");
    expect(svg).not.toBeNull();
    if (!svg) return;
    expect(svg.getAttribute("viewBox")).toBe("0 0 24 24");
    expect(svg.getAttribute("stroke")).toBe("currentColor");
    expect(HARDCODED_COLOR.test(svg.outerHTML)).toBe(false);
  });
});
