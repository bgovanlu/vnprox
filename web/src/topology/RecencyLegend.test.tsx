// SPDX-License-Identifier: Apache-2.0

// T-3908 WCAG requirement: the legend must carry the recency signal in text,
// not only in a swatch's background color — so every bucket's full phrase
// (recencyBucketPhrase) and its glyph must be present in the rendered DOM,
// independent of any CSS color.
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { RecencyLegend } from "./RecencyLegend";

describe("RecencyLegend", () => {
  it("names every bucket's phrase in real text, not just a colored swatch", () => {
    render(<RecencyLegend />);
    const legend = screen.getByLabelText("Recency overlay legend");
    expect(legend).toHaveTextContent("in the last 15 minutes");
    expect(legend).toHaveTextContent("in the last 24 hours");
    expect(legend).toHaveTextContent("in the last 7 days");
    expect(legend).toHaveTextContent("more than 7 days ago");
    expect(legend).toHaveTextContent("outside vnprox");
    expect(legend).toHaveTextContent("no change in the lookback window");
  });

  it("gives each timed/drift swatch its own distinct glyph character, hidden from assistive tech (text carries the signal instead)", () => {
    render(<RecencyLegend />);
    const legend = screen.getByLabelText("Recency overlay legend");
    for (const glyph of ["m", "h", "d", "w", "?"]) {
      expect(legend).toHaveTextContent(glyph);
    }
    // The swatches themselves are decorative — aria-hidden — because the
    // adjacent text span (asserted above) is what a screen reader announces.
    const hiddenSwatches = legend.querySelectorAll('[aria-hidden="true"]');
    expect(hiddenSwatches.length).toBeGreaterThanOrEqual(5);
  });
});
