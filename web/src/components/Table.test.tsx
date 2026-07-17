// T-905 acceptance criterion 1: "a Vitest test on Table.tsx (or the shared
// density context) asserts compact/comfortable render distinctly."
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { DensityProvider } from "./density";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./Table";

function sampleTable(density?: "compact" | "comfortable") {
  return (
    <Table density={density} data-testid="table">
      <TableHeader>
        <TableRow>
          <TableHead data-testid="head-cell">Name</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow>
          <TableCell data-testid="body-cell">vmbr0</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  );
}

describe("Table density", () => {
  it("defaults to comfortable spacing (px-3 py-2, text-sm) when no density is set", () => {
    render(sampleTable());
    expect(screen.getByTestId("table")).toHaveAttribute("data-density", "comfortable");
    expect(screen.getByTestId("table").className).toContain("text-sm");
    expect(screen.getByTestId("head-cell").className).toContain("px-3");
    expect(screen.getByTestId("body-cell").className).toContain("px-3");
  });

  it("compact density tightens padding and text size, distinctly from comfortable", () => {
    render(sampleTable("compact"));
    const table = screen.getByTestId("table");
    expect(table).toHaveAttribute("data-density", "compact");
    expect(table.className).toContain("text-xs");
    expect(table.className).not.toContain("text-sm");

    const headCell = screen.getByTestId("head-cell");
    expect(headCell.className).toContain("px-2");
    expect(headCell.className).not.toContain("px-3");

    const bodyCell = screen.getByTestId("body-cell");
    expect(bodyCell.className).toContain("px-2");
    expect(bodyCell.className).not.toContain("px-3");
  });

  it("nested TableHead/TableCell inherit density from the ambient DensityProvider without an explicit prop on the Table itself", () => {
    render(<DensityProvider density="compact">{sampleTable()}</DensityProvider>);
    expect(screen.getByTestId("table")).toHaveAttribute("data-density", "compact");
    expect(screen.getByTestId("head-cell").className).toContain("px-2");
  });

  it("an explicit density prop on Table wins over an outer ambient DensityProvider", () => {
    render(<DensityProvider density="compact">{sampleTable("comfortable")}</DensityProvider>);
    expect(screen.getByTestId("table")).toHaveAttribute("data-density", "comfortable");
    expect(screen.getByTestId("head-cell").className).toContain("px-3");
  });
});
