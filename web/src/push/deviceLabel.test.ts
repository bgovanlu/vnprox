// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { guessDeviceLabel } from "./deviceLabel";

describe("guessDeviceLabel", () => {
  it("recognizes an iPhone running Safari", () => {
    const ua =
      "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1";
    expect(guessDeviceLabel(ua)).toBe("iPhone — Safari");
  });

  it("recognizes Android Chrome", () => {
    const ua = "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Mobile Safari/537.36";
    expect(guessDeviceLabel(ua)).toBe("Android — Chrome");
  });

  it("recognizes desktop Firefox on Linux", () => {
    const ua = "Mozilla/5.0 (X11; Linux x86_64; rv:126.0) Gecko/20100101 Firefox/126.0";
    expect(guessDeviceLabel(ua)).toBe("Linux — Firefox");
  });

  it("recognizes Edge, not the Chrome/Safari it also mentions", () => {
    const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36 Edg/125.0.0.0";
    expect(guessDeviceLabel(ua)).toBe("Windows — Edge");
  });

  it("falls back to a generic label when nothing is recognized", () => {
    expect(guessDeviceLabel("some-unknown-agent/1.0")).toBe("This device");
  });
});
