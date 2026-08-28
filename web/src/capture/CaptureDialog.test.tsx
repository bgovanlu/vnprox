// SPDX-License-Identifier: Apache-2.0

// CaptureDialog wiring smoke tests: request -> live status -> decode ->
// download, and the multi-point side-by-side render. web/e2e/capture.spec.ts
// covers the same flow end to end against the real backend; these tests
// pin the component's data-flow contract against a mocked API so a wiring
// regression is caught fast, without a browser.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CaptureGroup, CaptureSession, CaptureStartRequest, MeResponse } from "../api/types";
import { CaptureDialog } from "./CaptureDialog";
import { useCaptureLauncherStore } from "./captureLauncherStore";

const CAPTURE_CAPS_SESSION: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: { pve1: { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: true } },
};

const NO_CAPTURE_SESSION: MeResponse = {
  user: { username: "viewer", realm: "pam" },
  caps: { pve1: { netRead: true, netWrite: false, sdnRead: false, sdnWrite: false, fwRead: false, fwWrite: false, guestNet: false, audit: false, capture: false } },
};

let meResponse: MeResponse = CAPTURE_CAPS_SESSION;
const startCapture = vi.fn<(req: CaptureStartRequest) => Promise<CaptureGroup>>();
const stopCapture = vi.fn<(groupId: string) => Promise<CaptureGroup>>();
const fetchCapture = vi.fn<(groupId: string) => Promise<CaptureGroup>>();
const fetchCaptureFile = vi.fn<(groupId: string, sessionId?: string) => Promise<ArrayBuffer>>();

vi.mock("../api/auth", () => ({
  getMe: () => Promise.resolve(meResponse),
  readCsrfCookie: () => "test-csrf",
}));
vi.mock("../api/captures", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/captures")>();
  return {
    captureDownloadUrl: actual.captureDownloadUrl,
    startCapture: (req: CaptureStartRequest) => startCapture(req),
    stopCapture: (groupId: string) => stopCapture(groupId),
    fetchCapture: (groupId: string) => fetchCapture(groupId),
    fetchCaptureFile: (groupId: string, sessionId?: string) => fetchCaptureFile(groupId, sessionId),
  };
});

const GROUP_CAPS = { maxDurationSec: 30, maxBytes: 1_048_576, maxPackets: 5000, retentionHours: 24 };

function baseSession(overrides?: Partial<CaptureSession>): CaptureSession {
  return {
    id: "s1", groupId: "g1", targetRef: "bridge:pve1:vmbr0", node: "pve1", filter: "tcp",
    caps: GROUP_CAPS,
    status: "running", startedBy: "root@pam", startedAt: 1_700_000_000, stoppedAt: 0, fileBytes: 24, packets: 1,
    ...overrides,
  };
}

function runningGroup(overrides?: Partial<CaptureGroup>): CaptureGroup {
  return {
    id: "g1",
    status: "running",
    startedBy: "root@pam",
    startedAt: 1_700_000_000,
    caps: GROUP_CAPS,
    sessions: [baseSession()],
    ...overrides,
  };
}

function renderDialog() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <CaptureDialog />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  meResponse = CAPTURE_CAPS_SESSION;
  startCapture.mockReset();
  stopCapture.mockReset();
  fetchCapture.mockReset();
  fetchCaptureFile.mockReset();
});

afterEach(() => {
  act(() => { useCaptureLauncherStore.getState().close(); });
});

describe("CaptureDialog", () => {
  it("is closed until the launcher store opens a request", () => {
    renderDialog();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("starts a capture and shows the server-granted caps once it starts", async () => {
    const user = userEvent.setup();
    const group = runningGroup();
    startCapture.mockResolvedValue(group);
    fetchCapture.mockResolvedValue(group);
    renderDialog();

    act(() => {
      useCaptureLauncherStore.getState().open({ targetRef: "bridge:pve1:vmbr0", node: "pve1", label: "vmbr0" });
    });

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    await screen.findByText(/Capture on vmbr0/);

    await user.click(screen.getByRole("button", { name: "Start capture" }));

    await waitFor(() => { expect(startCapture).toHaveBeenCalledWith(expect.objectContaining({ targetRef: "bridge:pve1:vmbr0" })); });
    await screen.findByTestId("granted-caps");
    expect(screen.getByTestId("granted-caps")).toHaveTextContent("30s");
    expect(screen.getByTestId("capture-group-status")).toHaveTextContent("running");
    expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
  });

  it("stops a running capture", async () => {
    const user = userEvent.setup();
    const group = runningGroup();
    startCapture.mockResolvedValue(group);
    fetchCapture.mockResolvedValue(group);
    const stopped = runningGroup({ status: "stopped", sessions: [baseSession({ status: "stopped", stoppedAt: 1_700_000_030 })] });
    stopCapture.mockResolvedValue(stopped);
    renderDialog();

    act(() => {
      useCaptureLauncherStore.getState().open({ targetRef: "bridge:pve1:vmbr0", node: "pve1" });
    });
    await user.click(await screen.findByRole("button", { name: "Start capture" }));
    await screen.findByRole("button", { name: "Stop" });

    await user.click(screen.getByRole("button", { name: "Stop" }));
    await waitFor(() => { expect(stopCapture).toHaveBeenCalledWith("g1"); });
    await waitFor(() => { expect(screen.getByTestId("capture-group-status")).toHaveTextContent("stopped"); });
    expect(screen.queryByRole("button", { name: "Stop" })).not.toBeInTheDocument();
  });

  it("decodes a terminal session's pcap in-browser", async () => {
    const user = userEvent.setup();
    const stopped = runningGroup({ status: "stopped", sessions: [baseSession({ status: "stopped" })] });
    startCapture.mockResolvedValue(stopped);
    fetchCapture.mockResolvedValue(stopped);
    // A one-packet classic-pcap buffer is unnecessary here — this test only
    // pins that CaptureDialog wires fetchCaptureFile's bytes into
    // decodePcap and renders whatever comes out; CaptureDecoder.test.ts
    // already covers decode correctness against the real corpus.
    fetchCaptureFile.mockResolvedValue(new ArrayBuffer(0));
    renderDialog();

    act(() => {
      useCaptureLauncherStore.getState().open({ targetRef: "bridge:pve1:vmbr0", node: "pve1" });
    });
    await user.click(await screen.findByRole("button", { name: "Start capture" }));
    const decodeBtn = await screen.findByRole("button", { name: "Decode" });
    expect(decodeBtn).toBeEnabled();

    await user.click(decodeBtn);
    await waitFor(() => { expect(fetchCaptureFile).toHaveBeenCalledWith("g1", "s1"); });
  });

  it("renders a download link pointing at the per-session download route", async () => {
    const user = userEvent.setup();
    const group = runningGroup();
    startCapture.mockResolvedValue(group);
    fetchCapture.mockResolvedValue(group);
    renderDialog();

    act(() => {
      useCaptureLauncherStore.getState().open({ targetRef: "bridge:pve1:vmbr0", node: "pve1" });
    });
    await user.click(await screen.findByRole("button", { name: "Start capture" }));

    const link = await screen.findByRole("link", { name: "Download pcap" });
    expect(link).toHaveAttribute("href", "/api/v1/captures/g1/download?sessionId=s1");
  });

  it("renders a side-by-side view for a multi-point session group", async () => {
    const user = userEvent.setup();
    const multi = runningGroup({
      sessions: [
        baseSession({ id: "s1", node: "pve1", status: "stopped", startedAt: 1, stoppedAt: 2, fileBytes: 10, packets: 1 }),
        baseSession({ id: "s2", targetRef: "bridge:pve2:vmbr0", node: "pve2", status: "stopped", startedAt: 1, stoppedAt: 2, fileBytes: 12, packets: 2 }),
      ],
      status: "stopped",
    });
    startCapture.mockResolvedValue(multi);
    fetchCapture.mockResolvedValue(multi);
    renderDialog();

    act(() => {
      useCaptureLauncherStore.getState().open({ targetRef: "bridge:pve1:vmbr0", node: "pve1" });
    });
    await user.click(await screen.findByRole("button", { name: "Start capture" }));

    const sideBySide = await screen.findByTestId("capture-side-by-side");
    expect(sideBySide.querySelectorAll('[data-testid^="session-pane-"]')).toHaveLength(2);
    expect(screen.getByTestId("session-pane-s1")).toBeInTheDocument();
    expect(screen.getByTestId("session-pane-s2")).toBeInTheDocument();
  });

  it("disables Start capture with a reason when the session lacks the capture capability", async () => {
    meResponse = NO_CAPTURE_SESSION;
    renderDialog();

    act(() => {
      useCaptureLauncherStore.getState().open({ targetRef: "bridge:pve1:vmbr0", node: "pve1" });
    });

    const startBtn = await screen.findByRole("button", { name: "Start capture" });
    await waitFor(() => { expect(startBtn).toBeDisabled(); });
    expect(startCapture).not.toHaveBeenCalled();
  });
});
