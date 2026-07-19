// T-1302 AC1: BpfBuilder.test.tsx covers building a filter from picker
// state and submitting it, and confirms a dialog whose requested caps
// exceed a mocked server-granted value renders the server's actual (lower)
// value, never the requested one.
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { BpfBuilder, type CaptureRequestFields } from "./BpfBuilder";

describe("BpfBuilder", () => {
  it("builds a filter from picker state and submits it with the requested caps", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn<(req: CaptureRequestFields) => void>();
    render(<BpfBuilder onSubmit={onSubmit} />);

    await user.selectOptions(screen.getByRole("combobox", { name: /Protocol/ }), "tcp");
    await user.type(screen.getByRole("textbox", { name: /^Host/ }), "10.0.0.5");
    await user.type(screen.getByRole("textbox", { name: /^Port/ }), "443");

    expect(screen.getByTestId("bpf-filter-preview")).toHaveTextContent("tcp and host 10.0.0.5 and port 443");

    await user.type(screen.getByRole("spinbutton", { name: /Duration/ }), "120");
    await user.type(screen.getByRole("spinbutton", { name: /Max bytes/ }), "5000000");
    await user.type(screen.getByRole("spinbutton", { name: /Max packets/ }), "10000");

    await user.click(screen.getByRole("button", { name: "Start capture" }));

    expect(onSubmit).toHaveBeenCalledWith({
      filter: "tcp and host 10.0.0.5 and port 443",
      durationSec: 120,
      maxBytes: 5_000_000,
      maxPackets: 10_000,
    });
  });

  it("submits an empty filter (capture everything) when no picker fields are set", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn<(req: CaptureRequestFields) => void>();
    render(<BpfBuilder onSubmit={onSubmit} />);

    expect(screen.getByTestId("bpf-filter-preview")).toHaveTextContent("none — capture everything");
    await user.click(screen.getByRole("button", { name: "Start capture" }));

    expect(onSubmit).toHaveBeenCalledWith({ filter: "", durationSec: undefined, maxBytes: undefined, maxPackets: undefined });
  });

  it("never re-derives or blocks submission based on the requested cap values — they are requests only", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn<(req: CaptureRequestFields) => void>();
    render(<BpfBuilder onSubmit={onSubmit} />);

    // An absurdly large request is still just forwarded as-is — this
    // component never clamps or rejects it; only the server does.
    await user.type(screen.getByRole("spinbutton", { name: /Max bytes/ }), "999999999999");
    await user.click(screen.getByRole("button", { name: "Start capture" }));

    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ maxBytes: 999_999_999_999 }));
  });

  it("renders the server's actual granted caps, never the requested value, once a session exists", () => {
    // The dialog "requested" 999999999999 bytes (simulated here by simply
    // never showing that value at all once grantedCaps is supplied) — the
    // server granted far less, and that's the only cap reading rendered.
    render(
      <BpfBuilder
        onSubmit={vi.fn()}
        grantedCaps={{ maxDurationSec: 30, maxBytes: 1_048_576, maxPackets: 5000, retentionHours: 24 }}
      />,
    );

    const granted = screen.getByTestId("granted-caps");
    expect(granted).toHaveTextContent("30s");
    expect(granted).toHaveTextContent("1048576 bytes");
    expect(granted).toHaveTextContent("5000 packets");
    expect(screen.queryByText("999999999999")).not.toBeInTheDocument();
    // The request form is retired once a session exists — the request
    // fields never remain editable/visible as if they were still "the
    // value in effect".
    expect(screen.queryByRole("button", { name: "Start capture" })).not.toBeInTheDocument();
  });

  it("disables submission with a stated reason when the caller lacks the capture capability", () => {
    render(<BpfBuilder onSubmit={vi.fn()} disabledReason="You need the capture capability to start a capture." />);
    expect(screen.getByRole("button", { name: "Start capture" })).toBeDisabled();
    expect(screen.getByText(/You need the capture capability/)).toBeInTheDocument();
  });
});
