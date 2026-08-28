// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { reviewLinkFor } from "./reviewLink";

describe("reviewLinkFor", () => {
  it("builds the /changesets/:id/review URL against the given origin", () => {
    expect(reviewLinkFor("cs-123", "https://vnprox.example:8007")).toBe(
      "https://vnprox.example:8007/changesets/cs-123/review",
    );
  });

  it("URL-encodes the changeset id", () => {
    expect(reviewLinkFor("cs 123/weird", "https://vnprox.example")).toBe(
      "https://vnprox.example/changesets/cs%20123%2Fweird/review",
    );
  });

  it("defaults to window.location.origin", () => {
    expect(reviewLinkFor("cs-1")).toBe(`${window.location.origin}/changesets/cs-1/review`);
  });
});
