// SPDX-License-Identifier: Apache-2.0

import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Skeleton } from "./Skeleton";

describe("Skeleton", () => {
  it("is aria-hidden — the loading state is announced elsewhere, not by the placeholder shape", () => {
    const { container } = render(<Skeleton />);
    expect(container.querySelector("[aria-hidden]")).not.toBeNull();
  });

  it("renders a single block by default", () => {
    const { container } = render(<Skeleton />);
    expect(container.querySelectorAll("[aria-hidden]")).toHaveLength(1);
  });

  it("variant=text with lines>1 stacks N blocks, the last one narrower", () => {
    const { container } = render(<Skeleton variant="text" lines={3} />);
    const blocks = container.querySelectorAll("[aria-hidden]");
    expect(blocks).toHaveLength(3);
    expect((blocks[2] as HTMLElement).style.width).toBe("70%");
    expect((blocks[0] as HTMLElement).style.width).toBe("");
  });

  it("uses Tailwind's built-in animate-pulse, caught by index.css's global reduced-motion gate", () => {
    const { container } = render(<Skeleton />);
    expect(container.querySelector(".animate-pulse")).not.toBeNull();
  });

  it("circle and rect variants get distinct shape classes", () => {
    const { container: c1 } = render(<Skeleton variant="circle" />);
    expect(c1.querySelector("[aria-hidden]")?.className).toContain("rounded-full");
    const { container: c2 } = render(<Skeleton variant="rect" />);
    expect(c2.querySelector("[aria-hidden]")?.className).toContain("rounded-md");
  });
});
