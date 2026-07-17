// T-907: VlanFilterInput must reflect programmatic `value` changes (loading
// a saved view or a shareable-URL view sets the VLAN filter outside this
// component's own Apply/Clear handlers) — the bug this test pins was found
// via web/e2e/saved-views.spec.ts's AC1 case: loading a saved view updated
// the store's vlanFilter but the input kept showing its stale local draft.
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { VlanFilterInput } from "./VlanFilterInput";

describe("VlanFilterInput", () => {
  it("reflects an external value change (e.g. loading a saved view)", () => {
    const { rerender } = render(<VlanFilterInput value={undefined} onChange={() => undefined} />);
    expect(screen.getByLabelText("VLAN")).toHaveValue("");

    rerender(<VlanFilterInput value={20} onChange={() => undefined} />);
    expect(screen.getByLabelText("VLAN")).toHaveValue("20");

    rerender(<VlanFilterInput value={undefined} onChange={() => undefined} />);
    expect(screen.getByLabelText("VLAN")).toHaveValue("");
  });

  it("does not clobber an in-progress, unsubmitted keystroke with an unrelated external value", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<VlanFilterInput value={undefined} onChange={() => undefined} />);
    const input = screen.getByLabelText("VLAN");
    await user.type(input, "30");
    expect(input).toHaveValue("30");

    // Re-rendering with the SAME external value (nothing actually changed)
    // must not disturb the user's in-progress typing.
    rerender(<VlanFilterInput value={undefined} onChange={() => undefined} />);
    expect(input).toHaveValue("30");
  });

  it("submitting Apply calls onChange with the parsed VID", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<VlanFilterInput value={undefined} onChange={onChange} />);
    await user.type(screen.getByLabelText("VLAN"), "20");
    await user.click(screen.getByRole("button", { name: "Apply" }));
    expect(onChange).toHaveBeenCalledWith(20);
  });

  it("Clear resets the draft and calls onChange(undefined)", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<VlanFilterInput value={20} onChange={onChange} />);
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(onChange).toHaveBeenCalledWith(undefined);
    expect(screen.getByLabelText("VLAN")).toHaveValue("");
  });
});
