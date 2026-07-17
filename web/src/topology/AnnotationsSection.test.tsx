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
import { AnnotationsSection } from "./AnnotationsSection";

const REF = "bridge:pve1:vmbr0";
const OTHER_REF = "bridge:pve2:vmbr0";

let annotations: Annotation[];

vi.mock("../api/annotations", () => ({
  fetchAnnotations: vi.fn(() => Promise.resolve({ items: annotations })),
  createAnnotation: vi.fn((ref: string, content: string) => {
    const created: Annotation = {
      id: `new-${String(annotations.length)}`,
      ref,
      content,
      createdBy: "alice@pve",
      createdAt: 100,
      updatedAt: 100,
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
      { id: "a1", ref: REF, content: "check VLAN tags", createdBy: "alice@pve", createdAt: 1, updatedAt: 1 },
      { id: "a2", ref: OTHER_REF, content: "unrelated note", createdBy: "bob@pve", createdAt: 2, updatedAt: 2 },
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
    annotations = [{ id: "a1", ref: REF, content: "temporary note", createdBy: "alice@pve", createdAt: 1, updatedAt: 1 }];
    const user = userEvent.setup();
    renderSection();

    const note = await screen.findByText("temporary note");
    const item = note.closest("li");
    if (!item) throw new Error("note has no <li> wrapper");
    await user.click(within(item).getByRole("button", { name: /Delete note/ }));

    await waitFor(() => {
      expect(screen.queryByText("temporary note")).not.toBeInTheDocument();
    });
    await screen.findByText("No notes pinned to this entity yet.");
  });

  it("shows who pinned each note", async () => {
    annotations = [{ id: "a1", ref: REF, content: "note text", createdBy: "bob@pve", createdAt: 1, updatedAt: 1 }];
    renderSection();
    await screen.findByText("bob@pve");
  });
});
