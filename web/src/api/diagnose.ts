// SPDX-License-Identifier: Apache-2.0

// T-1307's guided diagnosis ladder API call (docs/api.md's "Diagnosis"
// section; internal/api/diagnose.go). `POST /diagnose` is a mutating route
// (it may start a real capture session when `escalateToCapture: true`), so
// — unlike `POST /simulate/verify` above it — it always sends the CSRF
// token, mirroring api/findings.ts's `fixFinding` convention exactly.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { DiagnoseResult } from "./types";

export interface DiagnoseRequest {
  targetRef: string;
  escalateToCapture?: boolean;
}

/** POST /diagnose {targetRef, escalateToCapture?} — runs the guided
 * diagnosis ladder against targetRef and returns one readable, advisory
 * verdict. escalateToCapture defaults to false server-side when omitted —
 * this function never invents a default of its own, the caller decides. */
export function postDiagnose(req: DiagnoseRequest): Promise<DiagnoseResult> {
  return apiFetch<DiagnoseResult>("/diagnose", {
    method: "POST",
    json: req,
    csrfToken: readCsrfCookie(),
  });
}
