// SPDX-License-Identifier: Apache-2.0

import { useMemo } from "react";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { HelpAnchor } from "../help/HelpAnchor";
import {
  CERT_KIND_LABEL,
  daysUntil,
  expiryLabel,
  useCertsQuery,
  type Certificate,
  type CertIssue,
} from "./api";

// T-2305: the Certificates screen.
//
// Ordering principle: problems first, inventory second. Every cluster has a
// dozen certificates and almost always zero problems, so a page that led with
// the table would bury the one row that matters on the day it appears — which
// is exactly what happened to T-1906-bug-01, a real SAN mismatch nobody could
// see because nothing surfaced it.

function severityClass(severity: CertIssue["severity"]): string {
  return severity === "error"
    ? "border-red-300 bg-red-50 dark:border-red-900 dark:bg-red-950/40"
    : "border-amber-300 bg-amber-50 dark:border-amber-900 dark:bg-amber-950/40";
}

function IssueCard({ issue }: { issue: CertIssue }) {
  return (
    <li className={`rounded-md border p-3 ${severityClass(issue.severity)}`}>
      <div className="flex flex-wrap items-baseline gap-2">
        <span className="text-xs font-semibold uppercase tracking-wide text-fg-muted">
          {issue.severity}
        </span>
        <code className="font-mono text-xs text-fg-muted">{issue.check}</code>
        {issue.node !== undefined && issue.node !== "" && (
          <span className="text-xs text-fg-muted">on {issue.node}</span>
        )}
      </div>
      <p className="mt-1 text-sm text-slate-800 dark:text-slate-100">{issue.detail}</p>
      <p className="mt-2 text-xs text-fg-muted">
        <span className="font-semibold">To fix: </span>
        <code className="font-mono">{issue.remediation}</code>
      </p>
    </li>
  );
}

function expiryTone(notAfter: string): string {
  const days = daysUntil(notAfter);
  if (days < 0) {
    return "text-red-600 dark:text-red-400";
  }
  if (days <= 30) {
    return "text-amber-600 dark:text-amber-400";
  }
  return "text-fg-muted";
}

function CertRow({ cert }: { cert: Certificate }) {
  return (
    <tr className="border-t border-border align-top">
      <td className="py-2 pr-4 text-sm text-fg">
        {CERT_KIND_LABEL[cert.kind]}
        <div className="font-mono text-xs text-fg-muted">{cert.path}</div>
      </td>
      <td className="py-2 pr-4 text-sm text-fg-body">{cert.subject}</td>
      {/* T-4213: masked out of the visual gate. The daemon mints its
          self-signed certificates at boot, so `notAfter` is "boot + 1 year" —
          both the date and the day count differ between any two runs. Same
          class as the audit feed: server-generated content the act of testing
          creates, which no browser-side clock can stabilise. */}
      <td className={`py-2 pr-4 text-sm ${expiryTone(cert.notAfter)}`} data-volatile-time>
        {new Date(cert.notAfter).toISOString().slice(0, 10)}
        <div className="text-xs">{expiryLabel(cert.notAfter)}</div>
      </td>
      <td className="py-2 pr-4 text-sm text-fg-muted">
        {cert.keyAlgorithm}
        {cert.keyBits > 0 ? `-${String(cert.keyBits)}` : ""}
        <div className="text-xs text-fg-muted">{cert.signatureAlgorithm}</div>
      </td>
      <td className="py-2 text-sm text-fg-muted">
        {cert.sans.length === 0 ? (
          <span className="text-fg-muted">none</span>
        ) : (
          <ul className="flex flex-wrap gap-1">
            {cert.sans.map((san) => (
              <li
                key={`${san.type}:${san.value}`}
                className="rounded border border-border px-1.5 py-0.5 font-mono text-xs"
              >
                {san.value}
              </li>
            ))}
          </ul>
        )}
      </td>
    </tr>
  );
}

export function CertificatesPage() {
  const { data, isLoading, error, refetch } = useCertsQuery();

  const byNode = useMemo(() => {
    const groups = new Map<string, Certificate[]>();
    for (const cert of data?.inventory.certificates ?? []) {
      if (cert.kind === "cluster-ca") {
        continue;
      }
      const key = cert.node ?? "";
      groups.set(key, [...(groups.get(key) ?? []), cert]);
    }
    return [...groups.entries()].sort(([a], [b]) => a.localeCompare(b));
  }, [data]);

  if (isLoading) {
    return <p className="text-sm text-fg-muted">Loading certificates…</p>;
  }
  if (error) {
    return (
      <EmptyState
        icon="node"
        variant="failed"
        title="Could not read the certificate inventory"
        description="The daemon could not be reached. On the node itself, `vnproxctl certs` reads the same data directly from /etc/pve and works with the daemon down."
        action={
          <Button variant="secondary" size="sm" onClick={() => void refetch()}>
            Retry
          </Button>
        }
      />
    );
  }

  const issues = data?.issues ?? [];
  const errorCount = issues.filter((i) => i.severity === "error").length;
  const clusterCA = data?.inventory.clusterCA;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={
          <>
            Certificates
            <HelpAnchor topic="certificates-page" />
          </>
        }
        description="The TLS certificates this cluster's nodes present to each other and to you. vnprox reads them from
          /etc/pve, which every node shares — so this is the whole cluster's view, even from one node, and
          even when peers are unreachable."
      />

      <section>
        <h2 className="text-lg font-semibold">
          {issues.length === 0
            ? "No problems found"
            : `${String(issues.length)} problem${issues.length === 1 ? "" : "s"}`}
          {errorCount > 0 && (
            <span className="ml-2 rounded bg-red-600 px-2 py-0.5 text-xs font-semibold text-white">
              {errorCount} blocking
            </span>
          )}
        </h2>
        {issues.length === 0 ? (
          <p className="mt-1 text-sm text-fg-muted">
            Every certificate chains to the cluster CA, covers the address its peers reach it at, and is not
            close to expiring.
          </p>
        ) : (
          <ul className="mt-3 flex flex-col gap-2">
            {issues.map((issue) => (
              <IssueCard key={`${issue.check}:${issue.node ?? ""}:${issue.path ?? ""}`} issue={issue} />
            ))}
          </ul>
        )}
      </section>

      {clusterCA && (
        <section>
          <h2 className="text-lg font-semibold">Cluster CA</h2>
          <p className="mt-1 text-sm text-fg-muted">
            Every node certificate below must be issued by this CA — it is the sole trust anchor vnprox pins
            for peer-to-peer traffic.
          </p>
          <table className="mt-3 w-full table-auto text-left">
            <tbody>
              <CertRow cert={clusterCA} />
            </tbody>
          </table>
        </section>
      )}

      {byNode.map(([node, certs]) => (
        <section key={node}>
          <h2 className="text-lg font-semibold">{node === "" ? "Unattributed" : node}</h2>
          <div className="mt-2 overflow-x-auto">
            <table className="w-full table-auto text-left">
              <thead>
                <tr className="text-xs uppercase tracking-wide text-fg-muted">
                  <th className="pb-1 pr-4 font-semibold">Certificate</th>
                  <th className="pb-1 pr-4 font-semibold">Subject</th>
                  <th className="pb-1 pr-4 font-semibold">Expires</th>
                  <th className="pb-1 pr-4 font-semibold">Key</th>
                  <th className="pb-1 font-semibold">Names it covers</th>
                </tr>
              </thead>
              <tbody>
                {certs.map((cert) => (
                  <CertRow key={cert.path} cert={cert} />
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ))}

      {data?.inventory.scannedAt !== undefined && (
        <p className="text-xs text-fg-muted">
          Read from /etc/pve at {new Date(data.inventory.scannedAt).toLocaleString()}. vnprox never renews or
          replaces a certificate — Proxmox owns that, and each problem above names the command that does it.
        </p>
      )}
    </div>
  );
}
