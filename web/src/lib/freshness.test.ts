import { describe, expect, it } from "vitest";
import { formatAge } from "./freshness";

describe("formatAge", () => {
  const now = 1_700_000_000_000;

  it("reports 'just now' under a minute", () => {
    expect(formatAge(now - 30_000, now)).toBe("just now");
  });

  it("reports minutes between 1 and 59", () => {
    expect(formatAge(now - 5 * 60_000, now)).toBe("5m ago");
    expect(formatAge(now - 59 * 60_000, now)).toBe("59m ago");
  });

  it("reports hours between 1 and 23", () => {
    expect(formatAge(now - 2 * 3_600_000, now)).toBe("2h ago");
    expect(formatAge(now - 23 * 3_600_000, now)).toBe("23h ago");
  });

  it("falls back to a locale date/time beyond a day", () => {
    const timestamp = now - 25 * 3_600_000;
    expect(formatAge(timestamp, now)).toBe(new Date(timestamp).toLocaleString());
  });

  it("never reports a negative age for a timestamp in the future (clock skew)", () => {
    expect(formatAge(now + 10_000, now)).toBe("just now");
  });
});
