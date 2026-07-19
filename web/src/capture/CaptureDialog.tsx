// The capture dialog (T-1302): the whole UX layer over T-1301's capture
// engine. Mounted once (TopologyPage.tsx, alongside <EditorLauncher />) and
// driven by useCaptureLauncherStore — the map's right-click menu and the
// inspector's "Capture" button both just call `open({targetRef, node})`.
//
// Three phases, one dialog:
//  1. Request — BpfBuilder builds a filter + cap *request*, submitted to
//     POST /captures.
//  2. Live — polls GET /captures/{id} while the group is running, rendering
//     byte/packet/remaining-time status; a Stop button ends it early.
//  3. Result — once every session is terminal, a per-session Decode button
//     fetches the pcap and decodes it in-browser (CaptureDecoder.ts), a
//     Download link opens the same file in real Wireshark, and a group of
//     ≥2 sessions renders side-by-side (docs/api.md's Captures section:
//     sessions sharing one groupId are the correlation key this view aligns
//     panes by).
//
// This component never evaluates or enforces a cap itself — every duration/
// bytes/packets value it ever renders post-start comes from the server's
// response (`group.caps` / `session.caps`), never from what BpfBuilder's
// request fields asked for (T-1302 AC1 / AC5's regression: no client-side
// cap enforcement, ever).
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import { Button } from "../components/Button";
import { useSession } from "../api/useSession";
import { missingCapTooltip } from "../changesets/capabilities";
import { captureDownloadUrl, fetchCapture, fetchCaptureFile, startCapture, stopCapture } from "../api/captures";
import type { CaptureGroup, CaptureSession } from "../api/types";
import { BpfBuilder, type CaptureRequestFields } from "./BpfBuilder";
import { decodePcap, type DecodedPacket } from "./CaptureDecoder";
import { useCaptureLauncherStore } from "./captureLauncherStore";
import { PacketList } from "./PacketList";

/** How often the dialog re-polls GET /captures/{id} while the group is
 * still running — a live status readout, not a websocket subscription (no
 * capture-specific WS event exists; this matches the polling convention
 * other lightweight status widgets in this codebase use, e.g. latMeshQueries). */
const POLL_INTERVAL_MS = 1500;

function formatBytes(n: number): string {
  if (n < 1024) return `${String(n)} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MiB`;
}

/** Session status line: bytes/packets so far, and (while running) time
 * remaining against the server-granted duration cap — every number here is
 * read from `session`, never from a request field. */
function SessionStatusLine({ session }: { session: CaptureSession }) {
  const elapsedSec = session.status === "running" ? Math.max(0, Math.floor(Date.now() / 1000) - session.startedAt) : undefined;
  const remainingSec =
    elapsedSec !== undefined && session.caps.maxDurationSec > 0
      ? Math.max(0, session.caps.maxDurationSec - elapsedSec)
      : undefined;
  return (
    <p className="text-xs text-slate-500 dark:text-slate-400" data-testid={`session-status-${session.id}`}>
      {session.node || "(cluster)"} · {session.status} · {String(session.packets)} packets · {formatBytes(session.fileBytes)}
      {remainingSec !== undefined && <> · {String(remainingSec)}s remaining</>}
    </p>
  );
}

function SessionPane({ groupId, session }: { groupId: string; session: CaptureSession }) {
  const [packets, setPackets] = useState<DecodedPacket[] | undefined>(undefined);
  const [decodeError, setDecodeError] = useState<string | undefined>(undefined);
  const [decoding, setDecoding] = useState(false);
  const terminal = session.status !== "running";

  async function handleDecode(): Promise<void> {
    setDecoding(true);
    setDecodeError(undefined);
    try {
      const buf = await fetchCaptureFile(groupId, session.id);
      const result = decodePcap(buf);
      if (!result.headerValid) {
        setDecodeError("Not a recognized pcap file.");
        return;
      }
      setPackets(result.packets);
    } catch (err) {
      setDecodeError(err instanceof Error ? err.message : "Could not fetch the capture file.");
    } finally {
      setDecoding(false);
    }
  }

  return (
    <div className="space-y-2 rounded border border-slate-200 p-2 dark:border-slate-700" data-testid={`session-pane-${session.id}`}>
      <SessionStatusLine session={session} />
      {session.filter && (
        <p className="font-mono text-[11px] text-slate-400">filter: {session.filter}</p>
      )}
      <div className="flex gap-2">
        <Button size="sm" variant="secondary" disabled={!terminal || decoding} onClick={() => { void handleDecode(); }}>
          {decoding ? "Decoding…" : "Decode"}
        </Button>
        <a
          href={captureDownloadUrl(groupId, session.id)}
          download={`${session.id}.pcap`}
          className={
            terminal
              ? "inline-flex h-8 items-center rounded-md bg-slate-200 px-2.5 text-sm text-slate-900 hover:bg-slate-300 dark:bg-slate-800 dark:text-slate-100 dark:hover:bg-slate-700"
              : "pointer-events-none inline-flex h-8 items-center rounded-md bg-slate-200/50 px-2.5 text-sm text-slate-400 dark:bg-slate-800/50"
          }
          aria-disabled={!terminal}
        >
          Download pcap
        </a>
      </div>
      {decodeError && <p className="text-xs text-amber-600 dark:text-amber-400">{decodeError}</p>}
      {packets && <PacketList packets={packets} paneLabel={session.node || undefined} />}
    </div>
  );
}

export function CaptureDialog() {
  const request = useCaptureLauncherStore((s) => s.request);
  const closeLauncher = useCaptureLauncherStore((s) => s.close);
  const { data: session } = useSession();
  const queryClient = useQueryClient();
  const [groupId, setGroupId] = useState<string | undefined>(undefined);

  const node = request?.node ?? "";
  const disabledReason = missingCapTooltip(session, node, "capture");

  const groupQuery = useQuery<CaptureGroup>({
    queryKey: ["capture-group", groupId],
    queryFn: () => fetchCapture(groupId ?? ""),
    enabled: groupId !== undefined,
    refetchInterval: (q) => (q.state.data?.status === "running" ? POLL_INTERVAL_MS : false),
  });

  const startMutation = useMutation({
    mutationFn: (req: CaptureRequestFields) => {
      if (!request) throw new Error("no capture target selected");
      return startCapture({ targetRef: request.targetRef, ...req });
    },
    onSuccess: (group) => {
      setGroupId(group.id);
      queryClient.setQueryData(["capture-group", group.id], group);
    },
  });

  const stopMutation = useMutation({
    mutationFn: () => stopCapture(groupId ?? ""),
    onSuccess: (group) => {
      queryClient.setQueryData(["capture-group", group.id], group);
    },
  });

  function handleOpenChange(open: boolean): void {
    if (!open) {
      setGroupId(undefined);
      startMutation.reset();
      stopMutation.reset();
      closeLauncher();
    }
  }

  const group = groupQuery.data;
  const sessions = group?.sessions ?? [];
  const isMultiPoint = sessions.length > 1;

  return (
    <Dialog open={request !== undefined} onOpenChange={handleOpenChange}>
      <DialogContent widthClassName="max-w-3xl" density="compact">
        <DialogTitle>Capture{request ? ` on ${request.label ?? request.targetRef}` : ""}</DialogTitle>
        <DialogDescription>
          {request?.node ? `Node: ${request.node}. ` : ""}
          Payload bytes only ever live in the downloadable pcap file — vnprox never stores or displays packet contents
          anywhere else.
        </DialogDescription>

        {!group && (
          <BpfBuilder
            onSubmit={(req) => { startMutation.mutate(req); }}
            submitting={startMutation.isPending}
            disabledReason={disabledReason}
          />
        )}

        {startMutation.isError && (
          <p className="text-xs text-red-600 dark:text-red-400">
            {startMutation.error instanceof Error ? startMutation.error.message : "Could not start the capture."}
          </p>
        )}

        {group && (
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <BpfBuilder onSubmit={() => { /* retired once a group exists */ }} grantedCaps={group.caps} />
            </div>
            <div className="flex items-center gap-2">
              <span className="text-xs font-medium" data-testid="capture-group-status">
                Group status: {group.status}
              </span>
              {group.status === "running" && (
                <Button size="sm" variant="destructive" disabled={stopMutation.isPending} onClick={() => { stopMutation.mutate(); }}>
                  Stop
                </Button>
              )}
            </div>

            {isMultiPoint ? (
              <div data-testid="capture-side-by-side" className="grid grid-cols-2 gap-3">
                {sessions.map((s) => (
                  <SessionPane key={s.id} groupId={group.id} session={s} />
                ))}
              </div>
            ) : (
              sessions.map((s) => <SessionPane key={s.id} groupId={group.id} session={s} />)
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
