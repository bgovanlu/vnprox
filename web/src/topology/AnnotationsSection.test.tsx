// SPDX-License-Identifier: Apache-2.0

// T-907 AC3 (Vitest half): "pinning a sticky note to an entity ref
// persists it; reloading the topology page re-renders the note at the same
// entity; deleting it removes it." The e2e half (web/e2e/saved-views.spec.ts)
// exercises this against the real backend; this file exercises the
// component's own list/create/delete wiring against a mocked API module.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Annotation } from "../api/types";
import { AnnotationsSection, ORPHAN_BADGE } from "./AnnotationsSection";

const REF = "bridge:pve1:vmbr0";
const OTHER_REF = "bridge:pve2:vmbr0";

let annotations: Annotation[];

/** note builds a full Annotation, defaulting T-2806's read-time-derived
 * fields (`expired`/`orphaned`) to the ordinary case: a live note on an
 * entity that still exists. */
function note(partial: Partial<Annotation> & Pick<Annotation, "id" | "ref" | "content">): Annotation {
  return {
    createdBy: "alice@pve",
    createdAt: 1,
    updatedAt: 1,
    expiresAt: 0,
    expired: false,
    orphaned: false,
    ...partial,
  };
}

vi.mock("../api/annotations", () => ({
  fetchAnnotations: vi.fn(() => Promise.resolve({ items: annotations })),
  createAnnotation: vi.fn((ref: string, content: string, expiresAt?: number) => {
    const created: Annotation = {
      id: `new-${String(annotations.length)}`,
      ref,
      content,
      createdBy: "alice@pve",
      createdAt: 100,
      updatedAt: 100,
      expiresAt: expiresAt ?? 0,
      expired: false,
      orphaned: false,
    };
    annotations = [...annotations, created];
    return Promise.resolve(created);
  }),
  deleteAnnotation: vi.fn((id: string) => {
    annotations = annotations.filter((a) => a.id !== id);
    return Promise.resolve(undefined);
  }),
}));

function renderSection(ref: string = REF) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <AnnotationsSection entityRef={ref} />
    </QueryClientProvider>,
  );
}

describe("AnnotationsSection", () => {
  it("shows an empty state when the entity has no notes", async () => {
    annotations = [];
    renderSection();
    await screen.findByText("No notes pinned to this entity yet.");
  });

  it("lists only the selected entity's own notes, not another entity's", async () => {
    annotations = [
      note({ id: "a1", ref: REF, content: "check VLAN tags" }),
      note({ id: "a2", ref: OTHER_REF, content: "unrelated note", createdBy: "bob@pve", createdAt: 2, updatedAt: 2 }),
    ];
    renderSection(REF);
    await screen.findByText("check VLAN tags");
    expect(screen.queryByText("unrelated note")).not.toBeInTheDocument();
  });

  it("pins a new note: typing content and clicking Pin note creates it and clears the draft", async () => {
    annotations = [];
    const user = userEvent.setup();
    renderSection();

    const textarea = screen.getByPlaceholderText("Pin a note to this entity…");
    await user.type(textarea, "double-check before Friday");
    await user.click(screen.getByRole("button", { name: "Pin note" }));

    await screen.findByText("double-check before Friday");
    expect(textarea).toHaveValue("");
  });

  it("the Pin note button is disabled until the draft has non-whitespace content", async () => {
    annotations = [];
    const user = userEvent.setup();
    renderSection();

    const pinButton = screen.getByRole("button", { name: "Pin note" });
    expect(pinButton).toBeDisabled();

    const textarea = screen.getByPlaceholderText("Pin a note to this entity…");
    await user.type(textarea, "   ");
    expect(pinButton).toBeDisabled();

    await user.type(textarea, "real content");
    expect(pinButton).not.toBeDisabled();
  });

  it("deleting a note removes it from the list", async () => {
    annotations = [note({ id: "a1", ref: REF, content: "temporary note" })];
    const user = userEvent.setup();
    renderSection();

    const rendered = await screen.findByText("temporary note");
    const item = rendered.closest("li");
    if (!item) throw new Error("note has no <li> wrapper");
    await user.click(within(item).getByRole("button", { name: /Delete note/ }));

    await waitFor(() => {
      expect(screen.queryByText("temporary note")).not.toBeInTheDocument();
    });
    await screen.findByText("No notes pinned to this entity yet.");
  });

  // T-2806 AC6, render path: the inspector's note list. This is a separate
  // path from the canvas overlay (AnnotationLayer.test.tsx) and from the
  // doc export's renderers, and is asserted on its own — one assertion per
  // path, so a regression in one cannot hide behind another.
  it("escapes note text in the inspector: markup renders as literal text, not as an element", async () => {
    const hostile = `<img src=x onerror="alert(1)">`;
    annotations = [note({ id: "a1", ref: REF, content: hostile })];
    const { container } = renderSection();

    await screen.findByText(hostile);
    expect(container.querySelector("img")).toBeNull();
  });

  // T-2806 AC2: the note on a deleted entity is shown, and shown as such.
  it("labels a note whose entity no longer exists", async () => {
    annotations = [
      note({ id: "a1", ref: REF, content: "removed: vendor switch could not trunk VLAN 40", orphaned: true }),
    ];
    renderSection();

    await screen.findByText("removed: vendor switch could not trunk VLAN 40");
    expect(screen.getByText(ORPHAN_BADGE)).toBeInTheDocument();
  });

  it("shows a note's expiry date, and nothing at all for a note that never expires", async () => {
    // 2033-05-18T03:33:20Z
    annotations = [
      note({ id: "a1", ref: REF, content: "temporary", expiresAt: 2_000_000_000 }),
      note({ id: "a2", ref: REF, content: "permanent", createdAt: 2, updatedAt: 2 }),
    ];
    renderSection();

    await screen.findByText("temporary");
    expect(screen.getByText("expires 2033-05-18")).toBeInTheDocument();
    expect(screen.getAllByText(/^expires /)).toHaveLength(1);
  });

  it("pins a note with the chosen expiry, sending an absolute instant the daemon judges on each read", async () => {
    annotations = [];
    const user = userEvent.setup();
    const now = new Date("2026-01-01T00:00:00Z");
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(now);
    try {
      renderSection();
      await user.type(screen.getByPlaceholderText("Pin a note to this entity…"), "temporary uplink");
      await user.selectOptions(screen.getByLabelText("Note expiry"), "7");
      await user.click(screen.getByRole("button", { name: "Pin note" }));

      await screen.findByText("temporary uplink");
      const { createAnnotation } = await import("../api/annotations");
      expect(createAnnotation).toHaveBeenCalledWith(REF, "temporary uplink", Math.floor(now.getTime() / 1000) + 7 * 86_400);
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows who pinned each note", async () => {
    annotations = [note({ id: "a1", ref: REF, content: "note text", createdBy: "bob@pve" })];
    renderSection();
    await screen.findByText("bob@pve");
  });
});
