// T-208 raw editor state machine: load the live file, debounce syntax
// linting as the user types (AC1, <300ms latency budget), and save through
// the ordinary changeset drawer (useDrawerActions — "another editing
// surface", same accumulation path every other editor uses). Kept free of
// any Monaco/DOM concerns so it's testable with plain renderHook, no real
// editor instance required.
import { useCallback, useEffect, useRef, useState } from "react";
import { getRawInterfaces, lintInterfaces, type LintMarker } from "../../api/rawInterfaces";
import type { Changeset, Finding } from "../../api/types";
import { useDrawerActions } from "../useDrawerActions";
import { buildRawReplaceOp, errorFindings, hasHashConflict } from "./rawEditorOps";

/** How long after the last keystroke to wait before re-linting — well
 * under the card's <300ms round-trip budget on top of the debounce
 * itself, so the total "stop typing to red squiggle" latency stays fast. */
export const LINT_DEBOUNCE_MS = 250;

export interface RawEditorState {
  /** True while the initial GET /nodes/{node}/interfaces/raw is in flight
   * (on mount and on every node change). */
  loading: boolean;
  /** True while a save (addOps) is in flight. */
  saving: boolean;
  /** Set if the initial load failed (network/permission error). */
  loadError: string | undefined;
  /** Set if the save itself threw (a network/HTTP-level failure) — distinct
   * from `blockingFindings`, which is a *successful* save whose resulting
   * changeset carries error-severity validation findings. */
  saveError: string | undefined;
  /** The editor's current text. */
  content: string;
  /** The sha256 read alongside content at open/reload time — the
   * conflict-guard baseline sent as the op's baseHash. */
  baseHash: string;
  /** The latest debounced lint result. */
  markers: LintMarker[];
  /** True when the last save's findings included the hash-conflict guard
   * (raw.hash_conflict): the file changed on the server since this editor
   * session opened it. The UI should prompt the user to reload before
   * reapplying their edits (task card: "conflict guard: file hash captured
   * at open, mismatch on save -> reload prompt"). */
  hashConflict: boolean;
  /** Every error-severity finding on the changeset the last save produced,
   * other than the hash-conflict guard (which gets its own dedicated UI
   * above). This is what surfaces T-203's safety interlock in the editor
   * flow itself (AC2: "saving a file that deletes the management bridge
   * -> interlock error surfaces in the editor flow") — those findings are
   * typically attributed to a *synthesized* delta op's own ref (e.g. the
   * bridge internal/change/validate_raw.go's expandRawReplace derived from
   * the diff), not to the raw op's own ref, so this deliberately doesn't
   * filter by ref (see rawEditorOps.errorFindings' doc comment). Empty on
   * a clean save. */
  blockingFindings: Finding[];
  /** The id of the changeset a save landed in (whether or not it also
   * carries blocking findings — the draft still exists and is reviewable
   * in the drawer either way), so the caller can open the drawer/review
   * screen for it. Cleared on every edit/reload. */
  savedChangesetId: string | undefined;
}

export interface RawEditorActions {
  /** Updates the editor's content and (re)schedules a debounced lint. */
  setContent: (content: string) => void;
  /** Builds and saves the iface.raw.replace op via the drawer. */
  save: (title?: string) => Promise<void>;
  /** Re-fetches the live file (the hash-conflict banner's "Reload" action,
   * and available for a manual refresh too), discarding local edits. */
  reload: () => Promise<void>;
}

function initialState(loading: boolean): RawEditorState {
  return {
    loading,
    saving: false,
    loadError: undefined,
    saveError: undefined,
    content: "",
    baseHash: "",
    markers: [],
    hashConflict: false,
    blockingFindings: [],
    savedChangesetId: undefined,
  };
}

export function useRawEditor(node: string | undefined): [RawEditorState, RawEditorActions] {
  const { addOps } = useDrawerActions();
  const [state, setState] = useState<RawEditorState>(() => initialState(node !== undefined));
  const stateRef = useRef(state);
  stateRef.current = state;
  const lintTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const loadSeq = useRef(0);
  const lintSeq = useRef(0);

  const load = useCallback(async (targetNode: string) => {
    const seq = ++loadSeq.current;
    setState((s) => ({ ...s, loading: true, loadError: undefined, hashConflict: false, saveError: undefined, savedChangesetId: undefined }));
    try {
      const file = await getRawInterfaces(targetNode);
      if (seq !== loadSeq.current) return; // superseded by a newer load/node change
      setState((s) => ({ ...s, loading: false, content: file.content, baseHash: file.sha256, markers: [] }));
    } catch (err) {
      if (seq !== loadSeq.current) return;
      setState((s) => ({ ...s, loading: false, loadError: err instanceof Error ? err.message : String(err) }));
    }
  }, []);

  useEffect(() => {
    setState(initialState(node !== undefined));
    if (node) {
      void load(node);
    }
  }, [node, load]);

  const setContent = useCallback(
    (content: string) => {
      setState((s) => ({ ...s, content, hashConflict: false, saveError: undefined }));
      if (lintTimer.current) {
        clearTimeout(lintTimer.current);
      }
      const seq = ++lintSeq.current;
      lintTimer.current = setTimeout(() => {
        lintInterfaces(content)
          .then((result) => {
            if (seq !== lintSeq.current) return; // a newer edit superseded this lint
            setState((s) => ({ ...s, markers: result.errors }));
          })
          .catch(() => {
            // A lint round trip failing (offline, transient error) must
            // not block editing — the server-side parse in Save still
            // gates the actual changeset.
          });
      }, LINT_DEBOUNCE_MS);
    },
    [],
  );

  useEffect(
    () => () => {
      if (lintTimer.current) clearTimeout(lintTimer.current);
    },
    [],
  );

  const save = useCallback(
    async (title?: string) => {
      if (!node) return;
      const { content, baseHash } = stateRef.current;
      setState((s) => ({ ...s, saving: true, saveError: undefined, hashConflict: false, blockingFindings: [] }));
      try {
        const op = buildRawReplaceOp(node, content, baseHash);
        const changeset: Changeset = await addOps([op], title ?? `Raw edit: ${node}`);
        if (hasHashConflict(changeset.findings, node)) {
          setState((s) => ({ ...s, saving: false, hashConflict: true, savedChangesetId: changeset.id }));
          return;
        }
        setState((s) => ({
          ...s,
          saving: false,
          savedChangesetId: changeset.id,
          blockingFindings: errorFindings(changeset.findings),
        }));
      } catch (err) {
        setState((s) => ({ ...s, saving: false, saveError: err instanceof Error ? err.message : String(err) }));
      }
    },
    [addOps, node],
  );

  const reload = useCallback(async () => {
    if (node) {
      await load(node);
    }
  }, [node, load]);

  return [state, { setContent, save, reload }];
}
