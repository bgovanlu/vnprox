// The demo-mode bypass must be OFF unless explicitly opted into with
// VITE_AUTH_STUB=true (audit finding F-18a: real auth landed in T-105, so
// default-on stub scaffolding is stale). AUTH_STUB_ENABLED is a module-level
// constant read from import.meta.env at import time, so each case stubs the
// env and re-imports the module fresh.
import { afterEach, describe, expect, it, vi } from "vitest";

async function flagWithEnv(value: string | undefined): Promise<boolean> {
  vi.resetModules();
  vi.stubEnv("VITE_AUTH_STUB", value);
  const mod = await import("./authStub");
  return mod.AUTH_STUB_ENABLED;
}

describe("AUTH_STUB_ENABLED", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it("is off when VITE_AUTH_STUB is unset (the make dev / production default)", async () => {
    expect(await flagWithEnv(undefined)).toBe(false);
  });

  it("is off when VITE_AUTH_STUB=false", async () => {
    expect(await flagWithEnv("false")).toBe(false);
  });

  it("is on only for the exact string 'true'", async () => {
    expect(await flagWithEnv("true")).toBe(true);
  });

  it("is off for near-misses like '1' or 'TRUE' (explicit opt-in means the exact documented value)", async () => {
    expect(await flagWithEnv("1")).toBe(false);
    expect(await flagWithEnv("TRUE")).toBe(false);
  });
});
