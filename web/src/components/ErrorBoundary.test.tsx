// T-905: the built-in default fallback panel (used when a caller doesn't
// supply its own `fallback`) and its density variant.
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ErrorBoundary } from "./ErrorBoundary";

function Boom(): never {
  throw new Error("boom");
}

describe("ErrorBoundary", () => {
  it("renders children normally when nothing has thrown", () => {
    render(
      <ErrorBoundary>
        <p>all good</p>
      </ErrorBoundary>,
    );
    expect(screen.getByText("all good")).toBeInTheDocument();
  });

  it("renders a caller-supplied fallback over the built-in default", () => {
    render(
      <ErrorBoundary fallback={<p>custom fallback</p>}>
        <Boom />
      </ErrorBoundary>,
    );
    expect(screen.getByText("custom fallback")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("renders the built-in default fallback (comfortable density) when no fallback prop is given", () => {
    render(
      <ErrorBoundary>
        <Boom />
      </ErrorBoundary>,
    );
    const alert = screen.getByRole("alert");
    expect(alert).toHaveAttribute("data-density", "comfortable");
    expect(alert.className).toContain("p-6");
  });

  it("compact density tightens the built-in default fallback's padding", () => {
    render(
      <ErrorBoundary density="compact">
        <Boom />
      </ErrorBoundary>,
    );
    const alert = screen.getByRole("alert");
    expect(alert).toHaveAttribute("data-density", "compact");
    expect(alert.className).toContain("p-3");
    expect(alert.className).not.toContain("p-6");
  });

  it("includes the label in the built-in fallback's message when given", () => {
    render(
      <ErrorBoundary label="test-boundary">
        <Boom />
      </ErrorBoundary>,
    );
    expect(screen.getByText(/\(test-boundary\)/)).toBeInTheDocument();
  });
});
