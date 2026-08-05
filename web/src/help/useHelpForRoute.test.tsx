import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it } from "vitest";
import { HelpPanel } from "./HelpPanel";
import { useHelpForRoute } from "./useHelpForRoute";
import { useHelpStore } from "./store";

function Harness() {
  const openHelp = useHelpForRoute();
  return (
    <button type="button" onClick={openHelp}>
      Help
    </button>
  );
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Harness />
      <HelpPanel />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  useHelpStore.setState({ open: false, topicId: null, history: [], query: "" });
});

describe("useHelpForRoute", () => {
  it("opens help for the screen the user is actually on", async () => {
    const user = userEvent.setup();
    renderAt("/firewall");

    await user.click(screen.getByRole("button", { name: "Help" }));

    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("Firewall")).toBeInTheDocument();
  });

  it("opens a different topic on a different screen", async () => {
    const user = userEvent.setup();
    renderAt("/ipam");

    await user.click(screen.getByRole("button", { name: "Help" }));

    expect(screen.getByText("IPAM")).toBeInTheDocument();
  });

  it("resolves a parameterized route to its screen's topic", async () => {
    const user = userEvent.setup();
    renderAt("/changesets/cs-42/review");

    await user.click(screen.getByRole("button", { name: "Help" }));

    expect(screen.getByText("Changeset review")).toBeInTheDocument();
  });
});
