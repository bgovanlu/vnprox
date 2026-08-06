// Client types and query for `GET /certs` (T-2304), the cluster-wide TLS
// certificate inventory.
//
// These mirror internal/certs' JSON exactly. Note what is deliberately absent:
// there is no field carrying PEM, DER, or file contents, because the Go type
// has none either — so a certificate's private key cannot reach this client by
// construction rather than by filtering.
import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "../api/client";

export type CertKind = "cluster-ca" | "node-leaf" | "custom" | "daemon";

export interface CertSAN {
  type: "dns" | "ip";
  value: string;
}

export interface Certificate {
  kind: CertKind;
  node?: string;
  path: string;
  subject: string;
  issuer: string;
  serial: string;
  notBefore: string;
  notAfter: string;
  fingerprint: string;
  keyAlgorithm: string;
  keyBits: number;
  signatureAlgorithm: string;
  sans: CertSAN[];
  isCA: boolean;
  selfSigned: boolean;
}

export interface CertFileError {
  path: string;
  node?: string;
  error: string;
}

export interface CertIssue {
  check: string;
  severity: "error" | "warning";
  node?: string;
  path?: string;
  detail: string;
  remediation: string;
}

export interface CertInventory {
  scannedAt: string;
  clusterCA?: Certificate;
  certificates: Certificate[];
  errors?: CertFileError[];
}

export interface CertReport {
  inventory: CertInventory;
  issues: CertIssue[];
}

export const CERTS_QUERY_KEY = ["certs"] as const;

export function useCertsQuery() {
  return useQuery({
    queryKey: CERTS_QUERY_KEY,
    queryFn: () => apiFetch<CertReport>("/certs"),
    // The daemon rescans pmxcfs every few minutes; certificates change on the
    // order of months. Polling faster would buy nothing.
    refetchInterval: 60_000,
  });
}

/** Whole days until `notAfter`, negative once expired. */
export function daysUntil(notAfter: string, now: Date = new Date()): number {
  const end = new Date(notAfter).getTime();
  if (Number.isNaN(end)) {
    return Number.NaN;
  }
  return Math.floor((end - now.getTime()) / 86_400_000);
}

/** A short, plain-language expiry phrase. Deliberately says "expired" rather
 * than a negative day count — an operator reading a table should not have to
 * notice a minus sign to see that something is broken. */
export function expiryLabel(notAfter: string, now: Date = new Date()): string {
  const days = daysUntil(notAfter, now);
  if (Number.isNaN(days)) {
    return "unknown";
  }
  if (days < 0) {
    return "expired";
  }
  if (days === 0) {
    return "expires today";
  }
  if (days === 1) {
    return "1 day left";
  }
  return `${String(days)} days left`;
}

export const CERT_KIND_LABEL: Record<CertKind, string> = {
  "cluster-ca": "Cluster CA",
  "node-leaf": "Node certificate",
  custom: "Custom (pveproxy)",
  daemon: "vnprox listener",
};
