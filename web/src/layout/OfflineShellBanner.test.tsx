// SPDX-License-Identifier: Apache-2.0

import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { OfflineShellBanner } from "./OfflineShellBanner";

const useOnlineStatus = vi.fn(() => true);
const useLastSuccessAt = vi.fn((): number | null => null);

vi.mock("../lib/freshness", async () => {
  const actual = await vi.importActual<typeof import("../lib/freshness")>("../lib/freshness");
  return {
    ...actual,
    useOnlineStatus: () => useOnlineStatus(),
    useLastSuccessAt: () => useLastSuccessAt(),
  };
});

afterEach(() => {
  vi.clearAllMocks();
  useOnlineStatus.mockReturnValue(true);
  useLastSuccessAt.mockReturnValue(null);
});

describe("OfflineShellBanner", () => {
  it("renders nothing while online", () => {
    useOnlineStatus.mockReturnValue(true);
    const { container } = render(<OfflineShellBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  it("says nothing has loaded yet when offline with no cached data", () => {
    useOnlineStatus.mockReturnValue(false);
    useLastSuccessAt.mockReturnValue(null);
    render(<OfflineShellBanner />);
    expect(screen.getByRole("status")).toHaveTextContent(/no data has loaded yet/i);
  });

  it("labels cached data with its age when offline", () => {
    useOnlineStatus.mockReturnValue(false);
    useLastSuccessAt.mockReturnValue(Date.now() - 5 * 60_000);
    render(<OfflineShellBanner />);
    expect(screen.getByRole("status")).toHaveTextContent(/showing data from 5m ago/i);
    expect(screen.getByRole("status")).toHaveTextContent(/do not treat them as current/i);
  });
});
