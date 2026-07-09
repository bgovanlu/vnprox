import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// Exercises the actual persistence mechanism behind acceptance criterion 1
// ("dark/light toggle persists across a reload"): zustand's `persist`
// middleware round-tripping through localStorage. A real "reload" can't
// happen inside a single Vitest/jsdom process (the module graph is
// cached), so a fresh `import()` after `vi.resetModules()` stands in for
// it — this re-runs theme.ts's top-level `create(persist(...))` call
// exactly as a real page load would re-run it, reading whatever is in
// localStorage at that moment.
describe("theme store persistence", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.resetModules();
    document.documentElement.classList.remove("dark");
    document.documentElement.style.colorScheme = "";
  });

  afterEach(() => {
    localStorage.clear();
  });

  it("defaults to dark", async () => {
    const { useThemeStore } = await import("./theme");
    expect(useThemeStore.getState().theme).toBe("dark");
  });

  it("persists a theme change to localStorage", async () => {
    const { useThemeStore } = await import("./theme");
    useThemeStore.getState().toggleTheme();
    expect(useThemeStore.getState().theme).toBe("light");

    const stored = localStorage.getItem("vnprox.theme");
    expect(stored).toBeTruthy();
    expect(JSON.parse(stored ?? "{}")).toMatchObject({ state: { theme: "light" } });
  });

  it("hydrates from localStorage on a fresh module load (simulated reload)", async () => {
    localStorage.setItem("vnprox.theme", JSON.stringify({ state: { theme: "light" }, version: 0 }));

    const { useThemeStore } = await import("./theme");
    expect(useThemeStore.getState().theme).toBe("light");
  });

  it("round-trips through a toggle and a simulated reload", async () => {
    const first = await import("./theme");
    first.useThemeStore.getState().toggleTheme();
    expect(first.useThemeStore.getState().theme).toBe("light");

    vi.resetModules();
    const second = await import("./theme");
    expect(second.useThemeStore.getState().theme).toBe("light");
  });

  it("applyThemeClass toggles the html element's dark class and color-scheme", async () => {
    const { applyThemeClass } = await import("./theme");

    applyThemeClass("dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(document.documentElement.style.colorScheme).toBe("dark");

    applyThemeClass("light");
    expect(document.documentElement.classList.contains("dark")).toBe(false);
    expect(document.documentElement.style.colorScheme).toBe("light");
  });
});
