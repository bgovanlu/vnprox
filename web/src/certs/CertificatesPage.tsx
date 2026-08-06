import { useMemo } from "react";
import { EmptyState } from "../components/EmptyState";
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
        <span className="text-xs font-semibold uppercase tracking-wide text-slate-600 dark:text-slate-300">
          {issue.severity}
        </span>
        <code className="font-mono text-xs text-slate-500 dark:text-slate-400">{issue.check}</code>
        {issue.node !== undefined && issue.node !== "" && (
          <span className="text-xs text-slate-500 dark:text-slate-400">on {issue.node}</span>
        )}
      </div>
      <p className="mt-1 text-sm text-slate-800 dark:text-slate-100">{issue.detail}</p>
      <p className="mt-2 text-xs text-slate-600 dark:text-slate-300">
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
  return "text-slate-600 dark:text-slate-300";
}

function CertRow({ cert }: { cert: Certificate }) {
  return (
    <tr className="border-t border-slate-200 align-top dark:border-slate-800">
      <td className="py-2 pr-4 text-sm text-slate-900 dark:text-slate-100">
        {CERT_KIND_LABEL[cert.kind]}
        <div className="font-mono text-xs text-slate-500 dark:text-slate-400">{cert.path}</div>
      </td>
      <td className="py-2 pr-4 text-sm text-slate-700 dark:text-slate-200">{cert.subject}</td>
      <td className={`py-2 pr-4 text-sm ${expiryTone(cert.notAfter)}`}>
        {new Date(cert.notAfter).toISOString().slice(0, 10)}
        <div className="text-xs">{expiryLabel(cert.notAfter)}</div>
      </td>
      <td className="py-2 pr-4 text-sm text-slate-600 dark:text-slate-300">
        {cert.keyAlgorithm}
        {cert.keyBits > 0 ? `-${String(cert.keyBits)}` : ""}
        <div className="text-xs text-slate-500 dark:text-slate-400">{cert.signatureAlgorithm}</div>
      </td>
      <td className="py-2 text-sm text-slate-600 dark:text-slate-300">
        {cert.sans.length === 0 ? (
          <span className="text-slate-500 dark:text-slate-400">none</span>
        ) : (
          <ul className="flex flex-wrap gap-1">
            {cert.sans.map((san) => (
              <li
                key={`${san.type}:${san.value}`}
                className="rounded border border-slate-200 px-1.5 py-0.5 font-mono text-xs dark:border-slate-700"
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
  const { data, isLoading, error } = useCertsQuery();

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
    return <p className="text-sm text-slate-500 dark:text-slate-400">Loading certificates…</p>;
  }
  if (error) {
    return (
      <EmptyState
        title="Could not read the certificate inventory"
        description="The daemon could not be reached. On the node itself, `vnproxctl certs` reads the same data directly from /etc/pve and works with the daemon down."
      />
    );
  }

  const issues = data?.issues ?? [];
  const errorCount = issues.filter((i) => i.severity === "error").length;
  const clusterCA = data?.inventory.clusterCA;

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="flex items-center gap-2 text-xl font-semibold">
          Certificates
          <HelpAnchor topic="certificates-page" />
        </h1>
        <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
          The TLS certificates this cluster's nodes present to each other and to you. vnprox reads them from
          /etc/pve, which every node shares — so this is the whole cluster's view, even from one node, and
          even when peers are unreachable.
        </p>
      </div>

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
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
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
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
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
                <tr className="text-xs uppercase tracking-wide text-slate-500 dark:text-slate-400">
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
        <p className="text-xs text-slate-500 dark:text-slate-400">
          Read from /etc/pve at {new Date(data.inventory.scannedAt).toLocaleString()}. vnprox never renews or
          replaces a certificate — Proxmox owns that, and each problem above names the command that does it.
        </p>
      )}
    </div>
  );
}
