// SPDX-License-Identifier: Apache-2.0

// The guided BPF filter builder (T-1302): host/port/protocol pickers
// compose a filter string (bpf.ts's buildBpfFilter), submitted to
// POST /captures alongside *requested* duration/bytes/packets caps.
//
// Every cap field here is a request only — this component never evaluates,
// clamps, or enforces a cap itself (docs/api.md's Captures section: "a
// request may ask for a lower value, never a higher one... the client may
// request lower, never higher — never enforce caps client-side"). Once a
// session exists, `grantedCaps` (always read from the server's response,
// never echoed from what this form asked for) is the only cap readout this
// component renders — see the grantedCaps prop's own doc comment.
import { useState } from "react";
import { Button } from "../components/Button";
import { Field, inputClass } from "../changesets/editors/EditorDialog";
import { buildBpfFilter, EMPTY_BPF_PICKER_STATE, type BpfPickerState, type BpfProtocol } from "./bpf";
import type { CaptureCaps } from "../api/types";

export interface CaptureRequestFields {
  filter: string;
  durationSec?: number;
  maxBytes?: number;
  maxPackets?: number;
  /** Additional target Refs (typically on other nodes) to capture the same
   * logical flow at, correlated into one group — docs/api.md's Captures
   * section `peerTargets`. Empty when the operator didn't ask for a
   * multi-point capture. */
  peerTargets?: string[];
}

export interface BpfBuilderProps {
  onSubmit: (req: CaptureRequestFields) => void;
  submitting?: boolean;
  submitLabel?: string;
  /** Disables the submit button with an explanatory tooltip-free reason
   * shown as plain text (e.g. missing the `capture` capability) — matching
   * the read-only-affordance convention other editors use, simplified here
   * since this isn't wrapped in EditorDialog's Tooltip chrome. */
  disabledReason?: string;
  /**
   * The server's actual granted caps for the running/most recent session in
   * this dialog, once one exists — always what's rendered, regardless of
   * what duration/bytes/packets fields above requested (T-1302 AC1: a
   * dialog whose requested caps exceed a mocked server-granted value must
   * render the server's actual, lower value, never the requested one).
   * `undefined` before any session has started.
   */
  grantedCaps?: CaptureCaps;
}

const PROTOCOL_OPTIONS: { value: BpfProtocol; label: string }[] = [
  { value: "", label: "Any protocol" },
  { value: "tcp", label: "TCP" },
  { value: "udp", label: "UDP" },
  { value: "icmp", label: "ICMP" },
  { value: "arp", label: "ARP" },
];

export function BpfBuilder({ onSubmit, submitting = false, submitLabel = "Start capture", disabledReason, grantedCaps }: BpfBuilderProps) {
  const [picker, setPicker] = useState<BpfPickerState>(EMPTY_BPF_PICKER_STATE);
  const [durationSec, setDurationSec] = useState("");
  const [maxBytes, setMaxBytes] = useState("");
  const [maxPackets, setMaxPackets] = useState("");
  const [peerTargetsInput, setPeerTargetsInput] = useState("");

  const filter = buildBpfFilter(picker);

  function parsePositiveInt(v: string): number | undefined {
    if (v.trim() === "") return undefined;
    const n = Number(v);
    return Number.isFinite(n) && n > 0 ? Math.floor(n) : undefined;
  }

  function parsePeerTargets(v: string): string[] | undefined {
    const refs = v.split(",").map((s) => s.trim()).filter(Boolean);
    return refs.length > 0 ? refs : undefined;
  }

  function handleSubmit(): void {
    onSubmit({
      filter,
      durationSec: parsePositiveInt(durationSec),
      maxBytes: parsePositiveInt(maxBytes),
      maxPackets: parsePositiveInt(maxPackets),
      peerTargets: parsePeerTargets(peerTargetsInput),
    });
  }

  return (
    <div className="space-y-3 text-sm">
      <div className="grid grid-cols-3 gap-2">
        <Field label="Protocol">
          <select
            className={inputClass}
            value={picker.protocol}
            disabled={!!grantedCaps}
            onChange={(e) => { setPicker((p) => ({ ...p, protocol: e.target.value as BpfProtocol })); }}
          >
            {PROTOCOL_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
          </select>
        </Field>
        <Field label="Host" help="A single IP address.">
          <input
            className={inputClass}
            placeholder="10.0.0.5"
            value={picker.host}
            disabled={!!grantedCaps}
            onChange={(e) => { setPicker((p) => ({ ...p, host: e.target.value })); }}
          />
        </Field>
        <Field label="Port" help="A single port number.">
          <input
            className={inputClass}
            placeholder="443"
            value={picker.port}
            disabled={!!grantedCaps}
            onChange={(e) => { setPicker((p) => ({ ...p, port: e.target.value })); }}
          />
        </Field>
      </div>

      <Field label="Filter">
        <code
          data-testid="bpf-filter-preview"
          className="block rounded border border-slate-200 bg-slate-50 px-2 py-1 font-mono text-xs text-slate-600 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300"
        >
          {filter || "(none — capture everything on this interface)"}
        </code>
      </Field>

      <div className="grid grid-cols-3 gap-2">
        <Field label="Duration (s)" help="Request only — the server may cap this lower.">
          <input
            type="number" min={1} className={inputClass} placeholder="server default"
            value={durationSec} disabled={!!grantedCaps}
            onChange={(e) => { setDurationSec(e.target.value); }}
          />
        </Field>
        <Field label="Max bytes" help="Request only — the server may cap this lower.">
          <input
            type="number" min={1} className={inputClass} placeholder="server default"
            value={maxBytes} disabled={!!grantedCaps}
            onChange={(e) => { setMaxBytes(e.target.value); }}
          />
        </Field>
        <Field label="Max packets" help="Request only — the server may cap this lower.">
          <input
            type="number" min={1} className={inputClass} placeholder="server default"
            value={maxPackets} disabled={!!grantedCaps}
            onChange={(e) => { setMaxPackets(e.target.value); }}
          />
        </Field>
      </div>

      {!grantedCaps && (
        <Field
          label="Also capture on (optional)"
          help="Comma-separated target refs on other nodes — captures the same flow simultaneously, correlated for side-by-side comparison."
        >
          <input
            className={inputClass}
            placeholder="bridge:pve2:vmbr0"
            value={peerTargetsInput}
            onChange={(e) => { setPeerTargetsInput(e.target.value); }}
          />
        </Field>
      )}

      {!grantedCaps && (
        <Button variant="primary" size="sm" disabled={submitting || !!disabledReason} onClick={handleSubmit}>
          {submitLabel}
        </Button>
      )}
      {disabledReason && !grantedCaps && (
        <p className="text-xs text-amber-600 dark:text-amber-400">{disabledReason}</p>
      )}

      {grantedCaps && (
        <div
          data-testid="granted-caps"
          className="rounded border border-emerald-300 bg-emerald-50 px-2 py-1.5 text-xs text-emerald-800 dark:border-emerald-800 dark:bg-emerald-950 dark:text-emerald-200"
        >
          Server granted: {String(grantedCaps.maxDurationSec)}s · {String(grantedCaps.maxBytes)} bytes ·{" "}
          {String(grantedCaps.maxPackets)} packets · retained {String(grantedCaps.retentionHours)}h
        </div>
      )}
    </div>
  );
}
