import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { PageHeader } from "./PageHeader";
import { TabsContent, TabsList, TabsRoot, TabsTrigger } from "./Tabs";

describe("PageHeader", () => {
  it("renders the title as the page's single h1", () => {
    render(<PageHeader title="Guests" />);
    const headings = screen.getAllByRole("heading", { level: 1 });
    expect(headings).toHaveLength(1);
    expect(headings[0]).toHaveTextContent("Guests");
  });

  it("renders an optional description under the title", () => {
    render(<PageHeader title="Home" description="Network at a glance." />);
    expect(screen.getByText("Network at a glance.")).toBeInTheDocument();
  });

  it("omits the description paragraph entirely when none is given", () => {
    const { container } = render(<PageHeader title="IPAM" />);
    expect(container.querySelectorAll("p")).toHaveLength(0);
  });

  it("renders actions in a right-aligned slot", () => {
    render(
      <PageHeader
        title="History"
        actions={<button type="button">Take snapshot</button>}
      />,
    );
    expect(screen.getByRole("button", { name: "Take snapshot" })).toBeInTheDocument();
  });

  it("renders a tabs slot below the title/actions row", () => {
    render(
      <TabsRoot defaultValue="policies">
        <PageHeader
          title="Governance"
          tabs={
            <TabsList aria-label="Governance sections">
              <TabsTrigger value="policies">Policies</TabsTrigger>
            </TabsList>
          }
        />
      </TabsRoot>,
    );
    expect(screen.getByRole("tab", { name: "Policies" })).toBeInTheDocument();
  });
});

describe("Tabs (shared underlined wrapper)", () => {
  function ThreeTabs() {
    return (
      <TabsRoot defaultValue="a">
        <PageHeader
          title="Demo"
          tabs={
            <TabsList aria-label="Demo tabs">
              <TabsTrigger value="a">Alpha</TabsTrigger>
              <TabsTrigger value="b">Bravo</TabsTrigger>
              <TabsTrigger value="c">Charlie</TabsTrigger>
            </TabsList>
          }
        />
        <TabsContent value="a">Alpha panel</TabsContent>
        <TabsContent value="b">Bravo panel</TabsContent>
        <TabsContent value="c">Charlie panel</TabsContent>
      </TabsRoot>
    );
  }

  it("shows the default tab's panel and hides the others", () => {
    render(<ThreeTabs />);
    expect(screen.getByText("Alpha panel")).toBeVisible();
    expect(screen.queryByText("Bravo panel")).not.toBeInTheDocument();
    expect(screen.queryByText("Charlie panel")).not.toBeInTheDocument();
  });

  it("clicking a trigger switches the visible panel", async () => {
    const user = userEvent.setup();
    render(<ThreeTabs />);

    await user.click(screen.getByRole("tab", { name: "Bravo" }));

    expect(screen.getByText("Bravo panel")).toBeVisible();
    expect(screen.queryByText("Alpha panel")).not.toBeInTheDocument();
  });

  it("supports Radix's default arrow-key keyboard navigation between triggers", async () => {
    const user = userEvent.setup();
    render(<ThreeTabs />);

    const alpha = screen.getByRole("tab", { name: "Alpha" });
    alpha.focus();
    expect(alpha).toHaveFocus();

    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", { name: "Bravo" })).toHaveFocus();

    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", { name: "Charlie" })).toHaveFocus();

    // Radix's tabs default to automatic activation: focus moves the
    // selection along with it, no separate Enter/Space needed.
    expect(screen.getByText("Charlie panel")).toBeVisible();

    await user.keyboard("{ArrowLeft}");
    expect(screen.getByRole("tab", { name: "Bravo" })).toHaveFocus();
  });

  it("marks the active trigger's underline via data-state, not a raw color class", () => {
    render(<ThreeTabs />);
    const alpha = screen.getByRole("tab", { name: "Alpha" });
    const bravo = screen.getByRole("tab", { name: "Bravo" });
    expect(alpha).toHaveAttribute("data-state", "active");
    expect(bravo).toHaveAttribute("data-state", "inactive");
    // The underline/muted-label styling is expressed through
    // data-[state=active]: variants in the class list (Tabs.tsx), never a
    // second, competing className toggled in JS.
    expect(alpha.className).toContain("data-[state=active]:border-accent-600");
  });
});
