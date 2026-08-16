# Phase 23 — Certificate management

**Premise.** Every cross-node thing vnprox does — changeset application, distributed
rollback timers, host-writer calls, federated reads — rides peer-API TLS, which
T-1906 pins to the PVE cluster CA and fails closed. The product therefore has a
hard dependency on the cluster's certificates being correct, and until now no
way to see them, no way to know when one is about to expire, and one confirmed
latent failure (`T-1906-bug-01`) that would take every peer down at once on a
multi-node cluster.

**Ground truth, verified on `pvecube` (pve-manager/9.2.4) before designing:**

| Fact | Value |
|---|---|
| `/etc/pve/nodes/<node>/pve-ssl.pem` | in pmxcfs — **every node's leaf cert is readable from any node** |
| `/etc/pve/pve-root-ca.pem` | cluster CA, RSA-4096, 10-year (2023-10-24 → 2033-10-21) |
| `pve-ssl.pem` (this node) | RSA-2048, 2-year (2025-10-09 → 2027-10-09) |
| its SANs | `127.0.0.1`, `::1`, `localhost`, **`192.168.100.99`**, `pvecube`, `pvecube.localdomain.` |
| node's actual address | `192.168.1.9` — **absent from the SAN list** |
| `pve-ssl.key` | also in pmxcfs, mode 0640 — vnprox must never read or emit it |
| `pveproxy-ssl.pem` | absent here (the optional custom/ACME cert) |

Two consequences shape the whole design:

1. **No peer fan-out is needed.** A complete cluster-wide certificate inventory is
   a local directory walk, because pmxcfs already replicated it. That makes the
   inventory available *precisely when peers are unreachable* — which is when a
   certificate problem is the likely cause. Same reasoning
   `host.DefaultCorosyncConfPath` already established.
2. **Private keys sit in the same directory as the certificates.** Every read path
   in this phase must be structurally incapable of touching them.

---

## T-2301 — `internal/certs`: inventory

**kind:** implementation
**depends on:** —

- `Certificate`: kind, node, path, subject CN, issuer CN, serial, notBefore/notAfter,
  key algorithm and bits, signature algorithm, DNS and IP SANs, SHA-256 fingerprint,
  `isCA`.
- `Scan` walks the cluster CA, every `/etc/pve/nodes/*/pve-ssl.pem`, every
  `pveproxy-ssl.pem` that exists, and the daemon's own configured serving cert.
- Chain verification of each leaf against the cluster CA.

**Acceptance**

1. A scan of a fixture tree returns every certificate with correct parsed fields,
   including the trailing-dot FQDN form real PVE emits (`pvecube.localdomain.`).
2. **No private key is ever read, parsed, logged, or returned.** The scanner
   considers only the certificate filenames it knows; a test plants a recognisable
   key blob next to a certificate and asserts no byte of it reaches any output
   field, and that the scanner never opens the file.
3. A malformed or truncated PEM yields a per-file error, never a panic and never a
   partial record presented as complete.
4. Scanning is read-only: no file in the tree is modified, created, or removed.

## T-2302 — Certificate findings (`source: "cert"`)

**kind:** implementation
**depends on:** T-2301

All detection-only (`Fixable` false). None of these has a changeset op that could
fix it — the remediation is a PVE command, named in the finding.

- `cert_expired` (error) / `cert_expiring` (warning, default 30 days).
  Hysteresis-exempt: an expiry date is a fact, not a noisy counter.
- `cert_san_mismatch` (error) — **the `T-1906-bug-01` check.** A node's leaf cert
  whose SANs cover neither the address vnprox would dial that node at nor its PVE
  node name.
- `cert_not_chained` (error) — a leaf that does not verify against the cluster CA.
- `cert_ca_mismatch` (error) — a leaf issued by a different CA than the cluster CA
  now in use (a half-completed `pvecm updatecerts`).
- `cert_weak_key` (warning) — RSA below 2048 bits, or a SHA-1 signature.
- `cert_missing` (warning) — a cluster member with no leaf certificate in pmxcfs.

**Acceptance**

1. Each check fires on a fixture reproducing its condition and stays silent
   otherwise.
2. `cert_san_mismatch` fires on the **real pvecube SAN set against 192.168.1.9**,
   held as a fixture — this phase's premise is that this exact case is currently
   undetected, so it is a regression test, not a hypothetical.
3. Every finding names the remediation command for its condition.
4. No finding's detail text can contain key material.

## T-2303 — Peer dialling: fix `T-1906-bug-01`, don't just report it

**kind:** implementation
**depends on:** T-2301

Detection alone leaves the cluster broken. The dial path must work against a
correct-but-hostname-only certificate.

- Keep pinning the cluster CA. **Do not** relax to the system pool — that
  reinstates exactly what T-1906 closed.
- Resolve a **verification name** per peer and set it as `ServerName`: prefer the
  PVE node name (authoritative, from cluster status) where the peer's known
  certificate covers it, then its FQDN form, then the dial IP. The connection still
  goes to the IP; only the identity checked against the certificate changes. This is
  `curl --resolve`, and it is available without a network round-trip because pmxcfs
  already holds the peer's certificate.
- Where **no** candidate identity is covered, fail closed as now — but raise
  `cert_san_mismatch` at startup, before the first peer call, so the operator learns
  it from a named finding rather than an opaque handshake error.

**Acceptance**

1. A peer whose certificate carries only DNS SANs verifies successfully when dialled
   by IP, via the resolved `ServerName`.
2. A peer presenting a certificate from a **different** CA is still rejected —
   the ServerName resolution must not weaken the pin. Adversarial test.
3. A peer presenting a valid cluster-CA certificate for a **different node** is
   rejected — resolving a name must not let node A's certificate authenticate node B.
4. The resolved name is chosen from the *authoritative* node name, never from an
   arbitrary SAN in the presented certificate.

## T-2304 — API and CLI

**kind:** implementation
**depends on:** T-2301, T-2302

- `GET /api/v1/certs` — the inventory plus per-certificate check results. Additive;
  no existing contract changes (v3.0 platform freeze).
- `vnproxctl certs` — the same view on the console, for when the UI is exactly what
  a certificate problem has made unreachable.

**Acceptance**

1. The response contains no key material and no file contents, only parsed fields.
2. `vnproxctl certs` works against a daemon whose peers are all untrusted.
3. Route is capability-gated on ordinary network read.

## T-2305 — UI, help, docs

**kind:** implementation
**depends on:** T-2304

- A **Certificates** screen under Settings: per node, per certificate, with expiry
  as a plain date and a days-remaining figure, the SAN list, and the chain verdict.
- A help topic — **required**: phase 22's coverage gate fails the build for a route
  with no help.
- `docs/api.md`, `docs/security.md`, `docs/features/monitoring.md` (the new check
  family), `CHANGELOG.md`.

**Acceptance**

1. `web/src/help/coverage.test.ts` passes, i.e. the new route has help.
2. The screen renders a mismatch prominently rather than as one row among many.

---

## Deliberately out of scope: renewal

vnprox will **not** renew or reissue certificates, and this is a decision, not an
omission.

PVE already owns this: `pvecm updatecerts -f` regenerates a node's leaf from the
cluster CA, and `pvenode acme cert order` drives ACME. Both restart `pveproxy`.
Wrapping them would put a hypervisor-restarting action behind a vnprox button while
adding nothing PVE doesn't already do better, and a certificate is not network
config — there is no `/etc/network/interfaces` to diff and no pre-change snapshot
the change engine could roll back to, so it could not go through the change engine
either (the same category argument `docs/features/ipam.md` §7 makes for external
IPAM writes).

What this phase does instead is name the exact command for each condition in the
finding's remediation text, so the operator runs it knowing precisely why.

---

## Phase 23 — delivery record (2026-08-06)

| Card | State | Note |
|---|---|---|
| `T-2301` | ● Shipped | `internal/certs` inventory: a local pmxcfs directory walk (no peer fan-out needed — the reasoning the phase premise gives), paths built only from a fixed filename allowlist, no raw-key-bytes field on the certificate type |
| `T-2302` | ● Shipped | `cert` finding family: expiry, name coverage, chain to the cluster CA, key strength, missing/unreadable certificates — each names the exact PVE command that fixes it |
| `T-2303` | ● Shipped | Closes `T-1906-bug-01` for real, not just reports it: peers are now verified against the PVE node name where the certificate covers it (still dialling by IP, still pinning the cluster CA), so a stale IP-SAN no longer fails every peer closed at once. `cert_san_mismatch` now raises at startup instead of surfacing as an opaque handshake error later. Three properties held by adversarial tests (CA pin still enforced; candidate names come from PVE's node name, not the presented cert; an FQDN candidate must be rooted at the node name) |
| `T-2304` | ● Shipped | `GET /certs` API and `vnproxctl certs`; the CLI reads the same pmxcfs data with the daemon down |
| `T-2305` | ● Shipped | Settings → Certificates screen, plus the required help topic (phase 22's coverage gate enforces it) |

All five cards landed in one squash commit (`d919ecc`, 2026-08-06: "certs: cluster certificate
management, and fix pinned peer TLS against a stale SAN (T-2301..T-2305)"), merged the same day
(`6c0957e`), and are described together in `CHANGELOG.md`'s `v3.5.0` entry ("Certificate
management for the cluster") and the `T-1906-bug-01` fix note directly beneath it.
`docs/project-status.md` §2 records "23 — Certificate management … 5/5 ● Shipped." No commit
message or doc consulted separates the five cards' fates from one another, so this table reflects
one bundled delivery rather than five independently-verified ones — a distinction worth naming
even though the evidence for all five points the same way. Certificate renewal/reissue is
deliberately out of scope (see above), not a card this phase left undone.
