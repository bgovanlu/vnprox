// SPDX-License-Identifier: Apache-2.0

import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Changeset } from "../../api/types";
import { useRawEditor, LINT_DEBOUNCE_MS } from "./useRawEditor";

const { getRawInterfacesMock, lintInterfacesMock } = vi.hoisted(() => ({
  getRawInterfacesMock: vi.fn(),
  lintInterfacesMock: vi.fn(),
}));
vi.mock("../../api/rawInterfaces", () => ({
  getRawInterfaces: getRawInterfacesMock,
  lintInterfaces: lintInterfacesMock,
}));

const { addOpsMock } = vi.hoisted(() => ({ addOpsMock: vi.fn() }));
vi.mock("../useDrawerActions", () => ({
  useDrawerActions: () => ({ addOps: addOpsMock, replaceOps: vi.fn(), amendLastOps: vi.fn() }),
}));

function baseChangeset(overrides: Partial<Changeset> = {}): Changeset {
  return {
    id: "cs1",
    title: "t",
    author: "alice",
    status: "draft",
    ops: [],
    findings: [],
    createdAt: 0,
    updatedAt: 0,
    ...overrides,
  };
}

beforeEach(() => {
  getRawInterfacesMock.mockReset();
  lintInterfacesMock.mockReset();
  addOpsMock.mockReset();
  lintInterfacesMock.mockResolvedValue({ errors: [] });
});

describe("useRawEditor: loading", () => {
  it("loads the live file on mount and exposes content/baseHash", async () => {
    getRawInterfacesMock.mockResolvedValue({ node: "pve1", content: "auto lo\n", sha256: "hash1" });

    const { result } = renderHook(() => useRawEditor("pve1"));
    expect(result.current[0].loading).toBe(true);

    await waitFor(() => { expect(result.current[0].loading).toBe(false); });
    expect(result.current[0].content).toBe("auto lo\n");
    expect(result.current[0].baseHash).toBe("hash1");
    expect(getRawInterfacesMock).toHaveBeenCalledWith("pve1");
  });

  it("surfaces a load error without crashing", async () => {
    getRawInterfacesMock.mockRejectedValue(new Error("boom"));
    const { result } = renderHook(() => useRawEditor("pve1"));
    await waitFor(() => { expect(result.current[0].loading).toBe(false); });
    expect(result.current[0].loadError).toBe("boom");
  });

  it("does nothing when node is undefined", () => {
    const { result } = renderHook(() => useRawEditor(undefined));
    expect(result.current[0].loading).toBe(false);
    expect(getRawInterfacesMock).not.toHaveBeenCalled();
  });
});

describe("useRawEditor: debounced lint", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    getRawInterfacesMock.mockResolvedValue({ node: "pve1", content: "", sha256: "h" });
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("lints after the debounce window, not immediately", async () => {
    const { result } = renderHook(() => useRawEditor("pve1"));
    await act(async () => {
      await vi.runOnlyPendingTimersAsync();
    });

    act(() => {
      result.current[1].setContent("auto vmbr0\n");
    });
    expect(lintInterfacesMock).not.toHaveBeenCalled();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(LINT_DEBOUNCE_MS);
    });
    expect(lintInterfacesMock).toHaveBeenCalledWith("auto vmbr0\n");
  });

  it("only lints the final content of a rapid burst of edits", async () => {
    lintInterfacesMock.mockImplementation((content: string) =>
      Promise.resolve({ errors: content === "final" ? [] : [{ line: 1, message: "stale" }] }),
    );
    const { result } = renderHook(() => useRawEditor("pve1"));
    await act(async () => {
      await vi.runOnlyPendingTimersAsync();
    });

    act(() => {
      result.current[1].setContent("a");
      result.current[1].setContent("ab");
      result.current[1].setContent("final");
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(LINT_DEBOUNCE_MS);
    });

    expect(lintInterfacesMock).toHaveBeenCalledTimes(1);
    expect(lintInterfacesMock).toHaveBeenCalledWith("final");
    expect(result.current[0].markers).toEqual([]);
  });
});

describe("useRawEditor: save", () => {
  beforeEach(() => {
    getRawInterfacesMock.mockResolvedValue({ node: "pve1", content: "auto lo\n", sha256: "hash1" });
  });

  it("saves via addOps with the built op and records the changeset id on success", async () => {
    addOpsMock.mockResolvedValue(baseChangeset({ id: "cs42", findings: [] }));
    const { result } = renderHook(() => useRawEditor("pve1"));
    await waitFor(() => { expect(result.current[0].loading).toBe(false); });

    act(() => {
      result.current[1].setContent("auto lo\niface lo inet loopback\n");
    });
    await act(async () => {
      await result.current[1].save("my title");
    });

    expect(addOpsMock).toHaveBeenCalledWith(
      [{ op: "iface.raw.replace", target: "node:pve1:pve1", params: { content: "auto lo\niface lo inet loopback\n", baseHash: "hash1" } }],
      "my title",
    );
    expect(result.current[0].savedChangesetId).toBe("cs42");
    expect(result.current[0].hashConflict).toBe(false);
  });

  it("flags a hash conflict instead of treating it as a normal save success", async () => {
    addOpsMock.mockResolvedValue(
      baseChangeset({
        id: "cs43",
        findings: [{ severity: "error", code: "raw.hash_conflict", message: "changed", ref: "node:pve1:pve1" }],
      }),
    );
    const { result } = renderHook(() => useRawEditor("pve1"));
    await waitFor(() => { expect(result.current[0].loading).toBe(false); });

    await act(async () => {
      await result.current[1].save();
    });

    expect(result.current[0].hashConflict).toBe(true);
    // The changeset itself was still created (the conflict is a validation
    // finding, not an addOps failure) — savedChangesetId is set either way
    // so a future "open in drawer" affordance works regardless.
    expect(result.current[0].savedChangesetId).toBe("cs43");
    expect(result.current[0].blockingFindings).toEqual([]);
  });

  it("reload() re-fetches the live file and clears the conflict flag", async () => {
    addOpsMock.mockResolvedValue(
      baseChangeset({ findings: [{ severity: "error", code: "raw.hash_conflict", message: "changed", ref: "node:pve1:pve1" }] }),
    );
    const { result } = renderHook(() => useRawEditor("pve1"));
    await waitFor(() => { expect(result.current[0].loading).toBe(false); });
    await act(async () => {
      await result.current[1].save();
    });
    expect(result.current[0].hashConflict).toBe(true);

    getRawInterfacesMock.mockResolvedValue({ node: "pve1", content: "auto lo\n# reloaded\n", sha256: "hash2" });
    await act(async () => {
      await result.current[1].reload();
    });

    expect(result.current[0].hashConflict).toBe(false);
    expect(result.current[0].content).toBe("auto lo\n# reloaded\n");
    expect(result.current[0].baseHash).toBe("hash2");
  });

  it("surfaces a parse-error finding as a blocking finding, not saveError", async () => {
    addOpsMock.mockResolvedValue(
      baseChangeset({ findings: [{ severity: "error", code: "raw.parse_error", message: "line 3: bad", ref: "node:pve1:pve1" }] }),
    );
    const { result } = renderHook(() => useRawEditor("pve1"));
    await waitFor(() => { expect(result.current[0].loading).toBe(false); });

    await act(async () => {
      await result.current[1].save();
    });

    expect(result.current[0].saveError).toBeUndefined();
    expect(result.current[0].hashConflict).toBe(false);
    expect(result.current[0].blockingFindings).toEqual([
      { severity: "error", code: "raw.parse_error", message: "line 3: bad", ref: "node:pve1:pve1" },
    ]);
  });

  it("surfaces a safety-interlock finding attributed to a synthesized delta op's own ref (AC2)", async () => {
    // internal/change/validate_raw.go attributes T-203's safety findings to
    // the *synthesized* op the raw edit implies (e.g. a bridge.delete for
    // the removed management-bridge stanza), not to the raw op's own
    // node:pve1:pve1 ref — this must still surface in the editor flow.
    addOpsMock.mockResolvedValue(
      baseChangeset({
        findings: [
          {
            severity: "error",
            code: "safety.protected_interface",
            message: "this changeset would remove or re-address 10.10.0.1/24",
            ref: "bridge:pve1:vmbr0",
          },
        ],
      }),
    );
    const { result } = renderHook(() => useRawEditor("pve1"));
    await waitFor(() => { expect(result.current[0].loading).toBe(false); });

    await act(async () => {
      await result.current[1].save();
    });

    expect(result.current[0].blockingFindings).toHaveLength(1);
    expect(result.current[0].blockingFindings.at(0)?.code).toBe("safety.protected_interface");
    expect(result.current[0].hashConflict).toBe(false);
  });

  it("a clean save clears any prior blocking findings", async () => {
    addOpsMock.mockResolvedValue(baseChangeset({ findings: [] }));
    const { result } = renderHook(() => useRawEditor("pve1"));
    await waitFor(() => { expect(result.current[0].loading).toBe(false); });

    await act(async () => {
      await result.current[1].save();
    });

    expect(result.current[0].blockingFindings).toEqual([]);
    expect(result.current[0].savedChangesetId).toBeDefined();
  });
});
