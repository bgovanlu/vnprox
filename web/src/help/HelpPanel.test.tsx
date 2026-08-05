import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { HelpPanel } from "./HelpPanel";
import { HelpAnchor } from "./HelpAnchor";
import { useHelpStore } from "./store";

function resetHelpStore(): void {
  useHelpStore.setState({ open: false, topicId: null, history: [], query: "" });
}

beforeEach(resetHelpStore);

function openAt(topicId: string): void {
  useHelpStore.getState().openHelp(topicId);
}

describe("HelpPanel", () => {
  it("renders the requested topic's title, summary and section bodies", () => {
    openAt("commit-confirm");
    render(<HelpPanel />);

    const panel = screen.getByRole("dialog");
    expect(within(panel).getByText("Commit-confirm and automatic rollback")).toBeInTheDocument();
    expect(within(panel).getByText(/countdown banner appears/i)).toBeInTheDocument();
    expect(within(panel).getByText("Why it works")).toBeInTheDocument();
  });

  it("renders inline markup as elements, not as literal asterisks", () => {
    openAt("commit-confirm");
    render(<HelpPanel />);

    // "**120 seconds**" must render as bold text, and the raw marker must
    // not survive into what the user reads.
    expect(screen.getByText("120 seconds").tagName).toBe("STRONG");
    expect(screen.queryByText(/\*\*/)).not.toBeInTheDocument();
  });

  it("navigates to a seeAlso topic and back again", async () => {
    const user = userEvent.setup();
    openAt("commit-confirm");
    render(<HelpPanel />);

    await user.click(screen.getByRole("button", { name: "Snapshots and the time machine" }));
    expect(screen.getByText("Snapshots and the time machine")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByText("Commit-confirm and automatic rollback")).toBeInTheDocument();
  });

  it("has no Back control at the entry topic", () => {
    openAt("topology-page");
    render(<HelpPanel />);
    expect(screen.queryByRole("button", { name: "Back" })).not.toBeInTheDocument();
  });

  it("finds a topic by a word that appears only in a section body", async () => {
    const user = userEvent.setup();
    openAt("topology-page");
    render(<HelpPanel />);

    // "squatter" appears in the IPAM page topic's body and nowhere in any
    // title, summary or keyword list — so a hit proves body text is
    // genuinely searched rather than the title index being searched twice.
    await user.type(screen.getByRole("searchbox", { name: "Search help" }), "squatter");

    const results = screen.getByRole("dialog");
    expect(within(results).getByRole("button", { name: /^IPAM/ })).toBeInTheDocument();
    expect(within(results).getAllByText(/matched in/i).length).toBeGreaterThan(0);
  });

  it("says so plainly when a search matches nothing", async () => {
    const user = userEvent.setup();
    openAt("topology-page");
    render(<HelpPanel />);

    await user.type(screen.getByRole("searchbox", { name: "Search help" }), "zzzznotathing");

    expect(screen.getByText(/nothing matches/i)).toBeInTheDocument();
  });

  it("opens a search result as a topic", async () => {
    const user = userEvent.setup();
    openAt("topology-page");
    render(<HelpPanel />);

    await user.type(screen.getByRole("searchbox", { name: "Search help" }), "squatter");
    await user.click(screen.getByRole("button", { name: /^IPAM/ }));

    expect(screen.getByRole("searchbox", { name: "Search help" })).toHaveValue("squatter");
    expect(screen.getByText(/The address plan Proxmox never showed you/)).toBeInTheDocument();
  });

  it("stays closed until something opens it", () => {
    render(<HelpPanel />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});

describe("HelpAnchor", () => {
  it("opens the panel at its own topic", async () => {
    const user = userEvent.setup();
    render(
      <>
        <HelpAnchor topic="path-simulator" />
        <HelpPanel />
      </>,
    );

    await user.click(screen.getByRole("button", { name: "Help: Path simulator" }));

    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText(/Answers 'can A reach B/)).toBeInTheDocument();
  });

  it("returns focus to the anchor when the panel closes", async () => {
    const user = userEvent.setup();
    render(
      <>
        <HelpAnchor topic="path-simulator" />
        <HelpPanel />
      </>,
    );

    const anchor = screen.getByRole("button", { name: "Help: Path simulator" });
    await user.click(anchor);
    await user.keyboard("{Escape}");

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    // Radix restores focus asynchronously on unmount, so this is a
    // waitFor rather than a bare assertion — the guarantee under test is
    // that focus lands back on the anchor, not that it does so synchronously.
    await waitFor(() => {
      expect(anchor).toHaveFocus();
    });
  });
});
