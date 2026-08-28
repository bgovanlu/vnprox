// SPDX-License-Identifier: Apache-2.0

// The Monaco leaf: this is the one module in the raw-editor feature that
// imports `monaco-editor` itself. It is only ever reached through
// `React.lazy(() => import("./MonacoRawEditor"))` (RawEditorPanel.tsx), so
// Vite gives it its own chunk — the code-split AC4 requires ("Monaco loads
// only when the editor opens"). Do not import this module eagerly from
// anywhere; doing so would pull Monaco into the main bundle and defeat the
// whole point (see bundleSplit.test.ts, which asserts this at build time).
import { useEffect, useRef } from "react";
// Imports the standalone editor API directly (not the `monaco-editor`
// package root, which is `editor.main` — it eagerly pulls in *every*
// bundled language's full contribution, including the CSS/HTML/JSON/
// TypeScript language *services*, none of which this Monarch-only custom
// language needs). `editor.api` still carries Monaco's core editor engine
// itself (an inherent, unavoidable cost of embedding Monaco at all — it is
// not further splittable), but skips those ~40 extra language bundles.
import * as monaco from "monaco-editor/esm/vs/editor/editor.api.js";
import type { LintMarker } from "../../api/rawInterfaces";
import { INTERFACES_LANGUAGE_ID, registerInterfacesLanguage } from "./interfacesLanguage";

const LINT_MARKERS_OWNER = "vnprox-interfaces-lint";

export interface MonacoRawEditorProps {
  value: string;
  onChange: (value: string) => void;
  markers: LintMarker[];
  readOnly?: boolean;
}

/** Default export (not named) because this is the React.lazy() target. */
export default function MonacoRawEditor({ value, onChange, markers, readOnly = false }: MonacoRawEditorProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  // Created once per mount; `value`/`markers`/`readOnly` are pushed into
  // the already-created editor imperatively by the effects below, rather
  // than by recreating the editor on every prop change (which would lose
  // cursor position/undo history on every keystroke).
  useEffect(() => {
    if (!containerRef.current) return;
    registerInterfacesLanguage(monaco);
    const editor = monaco.editor.create(containerRef.current, {
      value,
      language: INTERFACES_LANGUAGE_ID,
      readOnly,
      automaticLayout: true,
      minimap: { enabled: false },
      fontSize: 13,
      scrollBeyondLastLine: false,
    });
    editorRef.current = editor;
    const subscription = editor.onDidChangeModelContent(() => {
      onChangeRef.current(editor.getValue());
    });
    return () => {
      subscription.dispose();
      editor.dispose();
      editorRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Keep the model's text in sync with external content changes (initial
  // load, reload-after-hash-conflict) without fighting the user's own
  // typing: only push `value` in when it actually differs from what the
  // model already holds (an onChange round trip would otherwise re-set the
  // identical value on every keystroke and reset the cursor/undo stack).
  useEffect(() => {
    const editor = editorRef.current;
    if (editor && editor.getValue() !== value) {
      editor.setValue(value);
    }
  }, [value]);

  useEffect(() => {
    const editor = editorRef.current;
    const model = editor?.getModel();
    if (!editor || !model) return;
    monaco.editor.setModelMarkers(
      model,
      LINT_MARKERS_OWNER,
      markers.map((m) => {
        const line = Math.min(Math.max(m.line, 1), model.getLineCount());
        return {
          severity: monaco.MarkerSeverity.Error,
          startLineNumber: line,
          startColumn: 1,
          endLineNumber: line,
          endColumn: model.getLineMaxColumn(line) || 1,
          message: m.message,
        };
      }),
    );
  }, [markers]);

  useEffect(() => {
    editorRef.current?.updateOptions({ readOnly });
  }, [readOnly]);

  return (
    <div
      ref={containerRef}
      className="h-full min-h-[420px] w-full rounded-md border border-slate-300 dark:border-slate-700"
    />
  );
}
