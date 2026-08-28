# T-3802 (scan half) — public git history exposure scan

**Date:** 2026-08-27
**Scope:** full git history of `bgovanlu/vnprox`, currently **public** on GitHub
(`visibility: public`, Apache-2.0, pushed 2026-08-25). This is a close-out scan on a live
exposure, not a pre-publication scrub — the finding either changes that framing or confirms it.
**This report is scan-and-report only. No remediation was performed.** No history was rewritten,
no force-push occurred, no files outside `planning/reports/` were touched, no visibility setting
was changed.

## Bottom line

**Nothing live and dangerous was found.** No verified/valid credential, no PVE ticket or API
token, no live signing key, no SSH private key, no cloud-provider key, and — specifically checked,
per CLAUDE.md's standing requirement to verify rather than trust this claim — **no lab root
password ever entered the repository**, in any commit, on any branch. Two TLS private keys exist
in history; both are self-signed, localhost-only, test-only fixtures with clear in-repo provenance,
not real credentials.

The exposure that *does* exist is exactly the low-sensitivity kind the task card anticipated:
homelab RFC1918 addresses (`192.168.1.9`/`.7` = pvecube/pve001, plus ~450 other synthetic
10.0.0.0/8 addresses in SDN test fixtures), a handful of MAC addresses in bond/OVS test fixtures,
and the git authorship trail (see "Personal data," which flags one item — `brian@dev.zoactive.net`
— that is more revealing than the already-expected `bgovanlu@gmail.com` and is worth the owner's
attention even though it is not dangerous).

**Recommendation: accept and document.** See "Recommendation" below for reasoning.

## Method

Two scanners, run against the full history, cross-checked against each other, then supplemented
with targeted `git log -S` / full-history `grep` for the specific strings CLAUDE.md and the task
card name by name (scanners have no rule for "the lab password" or "an internal doc's IP" — those
have to be checked directly). Full commands, tool versions, and the branch-coverage sanity check
are in `planning/reports/evidence/t-3802-scan-methodology.txt`; raw output is in the two files next
to it.

- **gitleaks 8.30.1** (pre-installed, not a repo dependency) — `gitleaks detect --log-opts="--all"`
  against all local refs. 679 non-merge / 853 total commits scanned (merge commits contribute no
  isolated diff under gitleaks' default traversal — confirmed via `git rev-list --all --no-merges`
  = 679, `--merges` = 174, sum = 853). 38 findings, all triaged below.
- **trufflehog 3.97.1** (downloaded as a release binary into the session scratchpad for this scan
  only, not installed into the repo/GOPATH/PATH) — `trufflehog git file://.`, which scans all local
  refs by default. 13,916 chunks / 29.9 MB scanned, **0 verified secrets**, 7 unverified. Both new
  tools are dev-time-only per CLAUDE.md's dependency rule: neither is in `go.mod`, `go.sum`, or
  `package.json`, and neither ships in any built artifact.
- **Branch-coverage check, done first:** `git branch -r` shows only three branches are actually
  public — `origin/main`, `origin/sigstore-in-daemon`, `origin/t-2409-e2e-store-isolation`. The
  ~40 other local branches in this working copy (`worktree-agent-*`, `fix-*`, `t40x-*`,
  `main-latest*`, etc.) turned out to add **zero** commits beyond what those three already reach:
  `git rev-list --all` and `git rev-list` of just the three origin branches both return exactly
  853. So "full local history" and "full public history" are the same 853-commit set today — there
  is no larger private history hiding behind what's already live. The two named branches
  (`sigstore-in-daemon`, `t-2409-e2e-store-isolation`) are each exactly one commit ahead of `main`;
  scoped gitleaks re-runs against each confirm their findings are the same set (or a subset).
- **Targeted checks**, run in addition to the scanners: `git log --all -S"LAB_ROOT_PASSWORD"` and
  `-S"root-password"` (every historical version of `scripts/pve-lab.sh` inspected directly, not
  just diffed); a full-history RFC1918 sweep; a full-history MAC-address sweep; a full-history email
  enumeration; a search for every `.key`/`.pem`/`id_rsa`/`id_ed25519` filename ever committed; a
  check of `internal/pvemock/testdata/cassettes/` provenance (real hardware vs. mock); a search for
  sigstore/cosign key material specifically, since `sigstore-in-daemon` is one of the named branches.

### Method's limits (stated honestly, not just "clean")

- Neither tool inspects compiled binaries beyond built-in text/archive extraction; no `.deb`,
  `.tar.gz`, or other binary artifacts were ever committed (checked separately), so this is believed
  moot but wasn't independently re-proven byte-for-byte.
- trufflehog's live-verification step only covers detectors with a known verification endpoint
  (GitHub, GitLab, AWS, etc.). It cannot verify a PVE ticket/token against a live PVE API (no such
  detector exists) — those had to be confirmed synthetic by reading the test code's own assertions
  instead (e.g. `mustNotHave`/`gone` fields naming the value as a fixture).
- Both tools scan git objects reachable from local refs. If anything were ever force-pushed away and
  fully garbage-collected from every clone, a local scan wouldn't see it — and GitHub's server-side
  dangling-object retention was not queried (no API access to that from this environment). There is
  no evidence any such force-push/GC has ever happened on this repo; this is a limit of the method,
  not a specific suspicion.
- This is a point-in-time scan as of 2026-08-27; it does not cover anything committed after it ran.

## Findings

### 1. Live credentials — **none found**

| # | Severity | What | Where (commit : path) | Live/valid? |
|---|----------|------|------------------------|-------------|
| 1 | Low | Self-signed TLS keypair, test-only | `28621ab4` : `testdata/certs/dev-key.pem` / `dev-cert.pem` | Not applicable — CN=`vnprox-dev`, SAN=`localhost`/`127.0.0.1`, self-issued, used only to stand up an HTTPS listener in unit tests (`cmd/vnproxd/*_test.go`, `scripts/hub-registry-harness.sh`). Trusts nothing, is trusted by nothing, protects no real endpoint. |
| 2 | Low | Self-signed client-cert keypair, test-only | `03afcbe1` : `testdata/k8s/kubeconfig-clientcert.yaml` | Not applicable — file's own header comment states it is "a throwaway self-signed pair generated solely for this fixture ... not a real credential of any kind." |
| 3 | None (false positive) | GitLab PAT-shaped string | `a47b723e` : `internal/gitsync/propose_test.go:31` | Dead by design — value is `glpat-VNPROXPUSHMARKER-do-not-log-me` with an inline `//nolint:gosec // a test marker, not a real credential` comment. trufflehog additionally attempted live verification against GitLab's API and got a negative result. |
| 4 | None (false positive) | "square-access-token"-shaped base64 | `bf95b64b` : `internal/gitsync/testdata/signed-commit.{ed25519,wrongnamespace}.sig` | SSH-signature fixture blobs (`gitsync`'s commit-signature verification tests), coincidentally entropy-matching an unrelated rule. |
| 5 | None (false positive) | ~30 "generic-api-key"-shaped strings | Various `testdata/clusters/*.yaml`, `testdata/ceph/*.yaml`, `internal/demo/dataset/cluster.yaml`, `internal/*/redact_test.go`, etc. | Fake PVE API-token UUIDs and fake WireGuard/session keys embedded in `pvemock` cluster fixtures and redaction-logic tests. `internal/demo/dataset/cluster.yaml` in particular backs `vnproxd --demo`, documented as running with "no PVE and no network access." |
| 6 | None (false positive) | GPG key fingerprint | `packaging/install.sh:122` | `VNPROX_RELEASE_KEY_FPR` — a **public** fingerprint pinned as a trust anchor for the apt repo (same pattern as Docker's install script). The comment states this is the real production key's fingerprint on purpose; fingerprints are meant to be public and this is not the private key. |
| 7 | None (false positive) | Fingerprint-shaped hex strings | `planning/reports/evidence/hub-registry-verification-2026-08-24.txt` | Fingerprints of a **throwaway** sigstore key pair generated in `/tmp` during a test harness run; the transcript's own text says the keys themselves were "never committed" — only their fingerprints (hashes) are recorded as evidence. |

No AWS (`AKIA...`), Slack (`xox...`), or other cloud-provider key shapes were found anywhere in
history. No `.key`/`.pem`/`.p12`/`.pfx`/`id_rsa`/`id_ed25519` file was ever committed other than
the two test-fixture PEMs above (verified via `git log --all --oneline --name-only` over those
extensions across all 853 commits). `internal/pvemock/testdata/cassettes/` — the record/replay
machinery added in `88d59168` (T-2502) — contains only `mock-*` directories; the introducing
commit's own message states cassettes are recorded from `pvemock` (the mock server), not real
Proxmox hardware, and that request headers/bodies (where any credential would live) are never
collected by the recorder in any form. `sigstore-in-daemon` — one of the two branches named
explicitly in the task — was searched specifically for cosign/sigstore private-key material
(`BEGIN ENCRYPTED SIGSTORE`, `BEGIN COSIGN`, `cosign.key`, `BEGIN OPENSSH PRIVATE KEY`): zero hits.

### 2. Lab root password — **claim verified, not just trusted**

CLAUDE.md states `LAB_ROOT_PASSWORD` "lives only in scratchpad and must never have entered the
repo." This was checked directly rather than assumed:

- `git log --all -S"LAB_ROOT_PASSWORD"` returns exactly **one** commit (`08e233bf`), which is one of
  only two commits that ever touch `scripts/pve-lab.sh` at all (the other, `107b4e3d`, introduced
  the file).
- Every occurrence of the string in both historical versions of the file is either a comment (e.g.
  "REQUIRED by `up`; no default, never in git") or code that reads it with an **empty default**
  (`LAB_ROOT_PASSWORD="${LAB_ROOT_PASSWORD:-}"`) and then `die`s if it's still empty
  (`[ -n "$LAB_ROOT_PASSWORD" ] || die "..."`).
- No literal password value was ever assigned to the variable in any commit
  (`git log --all -p -- scripts/pve-lab.sh | grep "LAB_ROOT_PASSWORD=[^\"$]"` — zero hits).
- A broader `-S"root-password"` sweep (the TOML key the script writes into the PVE answer file)
  returns the same single commit — the value written there is always `${LAB_ROOT_PASSWORD}`, the
  shell variable, never a literal.

**Verdict: the claim holds.** The lab root password has never been in the repository, on any
branch, at any point in its 853-commit history.

### 3. Internal IPs, hostnames, MAC addresses — **present, homelab-only, not dangerous**

| Category | Severity | Count | Notes |
|---|---|---|---|
| `192.168.1.9` (pvecube) | Low | 21 commits | Concentrated in `planning/reports/evidence/*.txt` (pvesh transcripts), planning docs, and `CLAUDE.md` itself — CLAUDE.md's own text already flags this file as knowingly carrying the IP at HEAD. |
| `192.168.1.7` (pve001) | Low | 20 commits | Same pattern as above. |
| Other RFC1918 addresses (10.0.0.0/8, 172.16–31.0.0/12, 192.168.0.0/16 excl. `.1.`) | Low | ~450 distinct addresses | Overwhelmingly `testdata/clusters/*.yaml` and `internal/flow/hostsample/testdata/` — sequential, clearly synthetic SDN/network-fixture addressing (`10.0.0.1`…`10.0.0.254` etc.), not real infrastructure. |
| MAC addresses | Low | 527 distinct | All in `testdata/*.yaml` or Go test files (bond/OVS fixtures). Spot-checked OUI prefixes (`00:11:22`, `aa:bb:cc`, `08:00:27` = the well-known VirtualBox test OUI) — nothing suggesting a real physical NIC. |
| `root@pvecube.localdomain` | Low | 1 file | `.localdomain` — not internet-resolvable, and pvecube is already a named, expected reference throughout the repo and docs. |

None of these addresses are internet-reachable — they are private (RFC1918) ranges behind the
homelab's own network boundary, consistent with the task's framing of this category as low
sensitivity by default.

### 4. Personal data — **as expected, plus one item worth a specific flag**

- `bgovanlu@gmail.com` — the owner's email, appears on 2 commits plus the GitHub noreply alias
  `35938535+bgovanlu@users.noreply.github.com` (13 commits). This is normal, expected public git
  authorship and is **not** treated as a leak, per the task's own instruction.
- **`brian@dev.zoactive.net` — 838 of 853 commits (98%)** are authored under this address, making
  it the *dominant* identity in the public history, not `bgovanlu@gmail.com`. This is a different,
  more revealing detail than the expected owner email: it names a real first name ("brian" —
  already known from local `git config`, so not new information on its own) and a personal/work
  domain (`dev.zoactive.net`) that is not otherwise associated with the `vnprox` project's public
  identity. It is not a security leak (nothing here is a credential or grants any access), but it
  is a personal-data item the owner may not have consciously chosen to attach to the public commit
  history under this project's public-facing name, and it's worth flagging distinctly rather than
  folding into the "expected" bucket.
- All other email-shaped strings in history resolve to test fixtures using reserved,
  non-routable TLDs: `*.test`, `*.invalid`, `*.example` (RFC 2606) — e.g. `fixture@vnprox.test`,
  `hunter2@switch-1.example` (a redaction-test fixture; the "hunter2" is the well-known joke
  placeholder password, not a real one), `s3cret@github.com` (a redaction-test URL-with-credentials
  fixture). `packaging@vnprox.io` and `security@vnprox.com` appear to be intentional, real public
  contact addresses for the project (consistent with `SECURITY.md`'s disclosure-contact language)
  rather than a leak.
- No other real names, phone numbers, physical addresses, or similarly identifying data were found.

### 5. Anything else a security reviewer would flag

- Two SSH **public** keys appear in test fixtures (`ssh-ed25519 AAAA...`, `ssh-rsa AAAA...`, both
  inside `internal/gitsync`'s signature-verification testdata). Public keys are not secrets by
  design; no concern.
- `Authorization: Bearer vnpx_9f3a7c21d8e64b05` appears once — it's a redaction-test fixture
  (`internal/backup/redact_test.go`, target domain `hooks.example`), not a real token.
- `CONTRIBUTING.md` (dated 2026-08-13, still asserting the repo is private/404s for anonymous
  requests) is stale relative to the repo's actual public status since 2026-08-25 — this is a
  documentation-accuracy issue, not a secrets-exposure one, and is already scoped to a different
  card per `planning/tasks/phase-38.md`'s T-3803.

## Findings summary by severity

| Severity | Count | Examples |
|---|---|---|
| Critical / High (live, dangerous credential) | **0** | — |
| Medium | 0 | — |
| Low | ~1000 individual matches, ~10 categories | Homelab RFC1918 IPs/hostnames, synthetic test-fixture MACs, two self-signed test-only TLS keys, the `brian@dev.zoactive.net` authorship trail |
| Informational / false positive | 33 of gitleaks' 38, 6 of trufflehog's 7 | Redaction-test fixtures, demo-mode fake tokens, a public GPG fingerprint, throwaway sigstore-key fingerprints in an evidence transcript |

## Recommendation

**Accept and document**, not fresh-cut or history rewrite.

Reasoning: the task card frames "accept and document" as the right outcome "if a scan finds only
low-sensitivity items — homelab RFC1918 IPs, no live credentials." That is precisely what this scan
found, checked against every category CLAUDE.md and the card name specifically, with two
independent scanners agreeing and a set of targeted greps covering what scanners structurally can't
rule-match (the lab password, specific IPs, cassette provenance). A history rewrite here would cost
real things — breaking every existing clone URL, star, and fork of the already-public repo — to buy
essentially nothing, since there is nothing live to remove. The one item worth the owner's explicit
attention is not a secret at all: it's that `brian@dev.zoactive.net`, not `bgovanlu@gmail.com`, is
the name attached to 98% of the public commit history, which is a decision about public identity
rather than a security exposure, and belongs to the owner to confirm is intentional. If the owner
wants that changed going forward, that's a `.gitconfig`/`user.email` change for future commits, not
a reason to rewrite 853 commits of otherwise-clean history.

This recommendation covers the *scan* only — the "prove it's clean going forward" half of the card
(a `job_secrets` job in `scripts/ci-local.sh` wired to gitleaks/trufflehog, per acceptance criterion
4) was explicitly out of scope for this run and is left for the remediation half.

## Evidence checked in

- `planning/reports/evidence/gitleaks-full-history.json` — full gitleaks JSON output (38 findings).
- `planning/reports/evidence/gitleaks-full-history.stdout.txt` — full gitleaks verbose transcript.
- `planning/reports/evidence/trufflehog-full-history.jsonl` — full trufflehog JSON-lines output
  (7 findings, 0 verified).
- `planning/reports/evidence/trufflehog-full-history.stderr.txt` — trufflehog's run summary
  (chunks/bytes scanned, verified-vs-unverified counts).
- `planning/reports/evidence/t-3802-scan-methodology.txt` — every command run, in order, with tool
  versions and the branch-coverage sanity check, so this scan is re-derivable rather than asserted.
