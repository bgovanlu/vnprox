// SPDX-License-Identifier: Apache-2.0

// The flow table for one dry-run bucket. Renders a MicrosegFlowRef list in
// the same column shape FlowExplorer's flow rows use (Time / Direction /
// Peer / Proto / Port / Bytes) so a would-have-blocked flow reads
// identically to how the reviewer already knows flows from the Flow Explorer
// (T-1003) — plus an optional Reason column for the cannotDetermine bucket
// (the evaluator's own "why undecidable" string). Deliberately renders an
// empty tbody (zero rows) when given an empty list rather than an
// error/empty-state component: "checked, none" is a real, meaningful result
// here (zero would-have-blocked is the goal), not a missing/error state
// (T-1603 AC2).
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import type { MicrosegFlowRef } from "../api/types";
import { protoName } from "../flows/proto";
import { formatBytes, formatFlowTime } from "./format";

interface FlowRefTableProps {
  flows: MicrosegFlowRef[];
  /** Show the Reason column (only the cannotDetermine bucket carries a
   * per-flow reason string). */
  showReason?: boolean;
  /** Accessible caption/label for the table, so each bucket's table is
   * distinguishable to a screen reader and to Testing Library. */
  label: string;
}

/** Renders a peer as "subnet (ip)" when a subnet was resolved, else the raw
 * peer IP — the same "show it honestly, never a guessed ref" convention
 * FlowExplorer's endpointLabel follows. */
function peerLabel(flow: MicrosegFlowRef): string {
  if (flow.peerSubnet && flow.peerSubnet !== flow.peerIp) {
    return `${flow.peerSubnet} (${flow.peerIp})`;
  }
  return flow.peerIp;
}

export function FlowRefTable({ flows, showReason = false, label }: FlowRefTableProps) {
  return (
    <Table density="compact" aria-label={label}>
      <TableHeader>
        <TableRow>
          <TableHead>Time</TableHead>
          <TableHead>Direction</TableHead>
          <TableHead>Peer</TableHead>
          <TableHead>Proto</TableHead>
          <TableHead>Port</TableHead>
          <TableHead>Bytes</TableHead>
          {showReason && <TableHead>Reason</TableHead>}
        </TableRow>
      </TableHeader>
      <TableBody>
        {flows.map((flow, i) => (
          <TableRow key={`${String(flow.at)}-${flow.direction}-${flow.peerIp}-${String(flow.port)}-${String(i)}`}>
            <TableCell className="whitespace-nowrap font-mono text-xs">{formatFlowTime(flow.at)}</TableCell>
            <TableCell className="font-mono text-xs">{flow.direction}</TableCell>
            <TableCell className="font-mono text-xs">{peerLabel(flow)}</TableCell>
            <TableCell className="font-mono text-xs">{protoName(flow.proto)}</TableCell>
            <TableCell className="font-mono text-xs">{flow.port > 0 ? flow.port : "—"}</TableCell>
            <TableCell>{formatBytes(flow.bytes)}</TableCell>
            {showReason && <TableCell className="text-xs text-fg-subtle">{flow.reason ?? "—"}</TableCell>}
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
