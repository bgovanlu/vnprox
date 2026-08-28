// SPDX-License-Identifier: Apache-2.0

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CertificatesPage } from "./CertificatesPage";
import { daysUntil, expiryLabel, type CertReport } from "./api";

const apiFetch = vi.hoisted(() => vi.fn());
vi.mock("../api/client", () => ({ apiFetch }));

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <CertificatesPage />
    </QueryClientProvider>,
  );
}

function report(overrides: Partial<CertReport> = {}): CertReport {
  return {
    inventory: {
      scannedAt: "2026-08-06T10:00:00Z",
      clusterCA: {
        kind: "cluster-ca",
        path: "/etc/pve/pve-root-ca.pem",
        subject: "Proxmox Virtual Environment",
        issuer: "Proxmox Virtual Environment",
        serial: "58551F",
        notBefore: "2023-10-24T07:49:04Z",
        notAfter: "2033-10-21T07:49:04Z",
        fingerprint: "a".repeat(64),
        keyAlgorithm: "RSA",
        keyBits: 4096,
        signatureAlgorithm: "SHA256-RSA",
        sans: [],
        isCA: true,
        selfSigned: true,
      },
      certificates: [
        {
          kind: "node-leaf",
          node: "pvecube",
          path: "/etc/pve/nodes/pvecube/pve-ssl.pem",
          subject: "pvecube.localdomain.",
          issuer: "Proxmox Virtual Environment",
          serial: "02",
          notBefore: "2025-10-09T05:12:19Z",
          notAfter: "2027-10-09T05:12:19Z",
          fingerprint: "b".repeat(64),
          keyAlgorithm: "RSA",
          keyBits: 2048,
          signatureAlgorithm: "SHA256-RSA",
          sans: [
            { type: "dns", value: "pvecube" },
            { type: "ip", value: "192.168.100.99" },
          ],
          isCA: false,
          selfSigned: false,
        },
      ],
    },
    issues: [],
    ...overrides,
  };
}

beforeEach(() => {
  apiFetch.mockReset();
});

describe("CertificatesPage", () => {
  it("renders each certificate with its expiry and the names it covers", async () => {
    apiFetch.mockResolvedValue(report());
    renderPage();

    expect(await screen.findByText("Certificates")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "pvecube" })).toBeInTheDocument();
    expect(screen.getByText("pvecube.localdomain.")).toBeInTheDocument();
    // The SAN list is the whole point of the page — an operator has to be
    // able to see which identities a certificate can actually prove.
    expect(screen.getByText("192.168.100.99")).toBeInTheDocument();
    expect(screen.getByText("2027-10-09")).toBeInTheDocument();
  });

  it("says plainly when nothing is wrong", async () => {
    apiFetch.mockResolvedValue(report());
    renderPage();

    expect(await screen.findByText("No problems found")).toBeInTheDocument();
  });

  it("puts problems above the inventory, with the fix command", async () => {
    apiFetch.mockResolvedValue(
      report({
        issues: [
          {
            check: "cert_san_mismatch",
            severity: "error",
            node: "pvecube",
            path: "/etc/pve/nodes/pvecube/pve-ssl.pem",
            detail: "covers neither this node's peer address (192.168.1.9) nor its node name",
            remediation: "on pvecube: pvecm updatecerts -f",
          },
        ],
      }),
    );
    renderPage();

    const heading = await screen.findByText("1 problem");
    expect(heading).toBeInTheDocument();
    expect(screen.getByText("1 blocking")).toBeInTheDocument();
    expect(screen.getByText("cert_san_mismatch")).toBeInTheDocument();
    expect(screen.getByText(/pvecm updatecerts -f/)).toBeInTheDocument();

    // Problems must precede the inventory in document order — a mismatch
    // buried under a dozen healthy rows is how T-1906-bug-01 stayed invisible.
    const problems = screen.getByText("1 problem");
    const inventory = screen.getByRole("heading", { name: "Cluster CA" });
    expect(problems.compareDocumentPosition(inventory) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("distinguishes blocking errors from warnings", async () => {
    apiFetch.mockResolvedValue(
      report({
        issues: [
          {
            check: "cert_expiring",
            severity: "warning",
            node: "pvecube",
            detail: "expires soon",
            remediation: "on pvecube: pvecm updatecerts -f",
          },
        ],
      }),
    );
    renderPage();

    expect(await screen.findByText("1 problem")).toBeInTheDocument();
    expect(screen.queryByText(/blocking/)).not.toBeInTheDocument();
  });

  it("points at the CLI when the daemon cannot be reached", async () => {
    apiFetch.mockRejectedValue(new Error("network"));
    renderPage();

    await screen.findByText(/Could not read the certificate inventory/);
    // The daemon being unreachable is exactly the case a certificate problem
    // causes, so the fallback has to name the tool that still works.
    expect(screen.getByText(/vnproxctl certs/)).toBeInTheDocument();
  });
});

describe("expiry helpers", () => {
  const now = new Date("2026-08-06T00:00:00Z");

  it("counts whole days remaining", () => {
    expect(daysUntil("2026-08-16T00:00:00Z", now)).toBe(10);
    expect(daysUntil("2026-08-05T00:00:00Z", now)).toBe(-1);
  });

  it("says 'expired' rather than showing a negative number", () => {
    // A reader scanning a table should not have to notice a minus sign to
    // see that something is broken.
    expect(expiryLabel("2026-01-01T00:00:00Z", now)).toBe("expired");
    expect(expiryLabel("2026-08-06T12:00:00Z", now)).toBe("expires today");
    expect(expiryLabel("2026-08-07T12:00:00Z", now)).toBe("1 day left");
    expect(expiryLabel("2026-09-06T00:00:00Z", now)).toBe("31 days left");
  });

  it("handles an unparseable date without throwing", () => {
    expect(expiryLabel("not-a-date", now)).toBe("unknown");
  });
});
