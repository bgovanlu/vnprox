// T-1806: pure logic behind `make check`'s npm-audit gate. Kept dependency-
// free (no new packages — see CLAUDE.md's "no new major dependencies without
// a note") and separate from check-audit-allowlist.mjs (the CLI wrapper that
// actually shells out to `npm audit`) so it can be unit-tested with fixture
// JSON instead of a real npm audit run.
//
// The mechanism this replaces: `npm audit --audit-level=high` failed the
// build permanently on every push, because vnprox's transitive dependency
// tree carries advisories (brace-expansion, postcss, dompurify via
// monaco-editor, react-router) with no non-breaking fix available. A
// permanent red X trains everyone to ignore CI. Instead: every high/critical
// advisory must be named in audit-allowlist.json with a rationale and an
// expiry date; an advisory that isn't allowlisted, or whose allowlist entry
// has expired, fails the build exactly as an un-allowlisted one would.

const HIGH_SEVERITIES = new Set(["high", "critical"]);
const GHSA_RE = /\/advisories\/(GHSA-[a-zA-Z0-9-]+)/;
const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

/**
 * Parse and validate the allowlist file's contents (already-read JSON text).
 * Throws with a descriptive message on any structural problem — a malformed
 * allowlist must fail loudly, not silently allow everything through.
 *
 * @param {string} raw
 * @returns {Map<string, {id: string, package: string, rationale: string, expires: string}>}
 */
export function parseAllowlist(raw) {
  let entries;
  try {
    entries = JSON.parse(raw);
  } catch (err) {
    throw new Error(`audit-allowlist.json is not valid JSON: ${err.message}`);
  }
  if (!Array.isArray(entries)) {
    throw new Error("audit-allowlist.json must be a JSON array of entries");
  }

  const byId = new Map();
  for (const entry of entries) {
    if (entry === null || typeof entry !== "object") {
      throw new Error(`audit-allowlist.json entry ${JSON.stringify(entry)} is not an object`);
    }
    for (const field of ["id", "package", "rationale", "expires"]) {
      if (typeof entry[field] !== "string" || entry[field].length === 0) {
        throw new Error(
          `audit-allowlist.json entry ${JSON.stringify(entry)} is missing a non-empty "${field}"`,
        );
      }
    }
    if (!/^GHSA-/.test(entry.id)) {
      throw new Error(`audit-allowlist.json entry has a non-GHSA id: ${entry.id}`);
    }
    if (!DATE_RE.test(entry.expires)) {
      throw new Error(
        `audit-allowlist.json entry ${entry.id} has a malformed "expires" (want YYYY-MM-DD): ${entry.expires}`,
      );
    }
    if (byId.has(entry.id)) {
      throw new Error(`audit-allowlist.json has a duplicate entry for ${entry.id}`);
    }
    byId.set(entry.id, entry);
  }
  return byId;
}

/**
 * Extract every high/critical root advisory from an `npm audit --json`
 * report. A "root" advisory is one whose `via` entry is the GHSA record
 * itself, not a string reference to another package that merely depends on
 * the vulnerable one (npm audit's report nests both in the same shape).
 *
 * @param {unknown} report parsed `npm audit --json` output
 * @returns {{id: string, package: string, severity: string, title: string}[]}
 */
export function advisoriesFromReport(report) {
  const advisories = [];
  const vulnerabilities =
    report && typeof report === "object" && report.vulnerabilities && typeof report.vulnerabilities === "object"
      ? report.vulnerabilities
      : {};

  for (const [pkgName, vuln] of Object.entries(vulnerabilities)) {
    if (!vuln || typeof vuln !== "object" || !HIGH_SEVERITIES.has(vuln.severity)) continue;
    const via = Array.isArray(vuln.via) ? vuln.via : [];
    for (const entry of via) {
      if (typeof entry === "string") continue; // reference to another dependent package, not a root advisory
      const match = GHSA_RE.exec(entry?.url ?? "");
      if (!match) continue;
      advisories.push({
        id: match[1],
        package: pkgName,
        severity: vuln.severity,
        title: entry.title ?? "(no title)",
      });
    }
  }
  return advisories;
}

/**
 * Evaluate found advisories against the allowlist as of `today`.
 *
 * @param {{id: string, package: string, severity: string, title: string}[]} advisories
 * @param {Map<string, {id: string, package: string, rationale: string, expires: string}>} allowlist
 * @param {string} today YYYY-MM-DD
 * @returns {{failures: string[], staleEntryIds: string[]}}
 */
export function evaluate(advisories, allowlist, today) {
  const failures = [];
  const seen = new Set();

  for (const adv of advisories) {
    seen.add(adv.id);
    const entry = allowlist.get(adv.id);
    if (!entry) {
      failures.push(
        `${adv.id} (${adv.package}, ${adv.severity}): not in audit-allowlist.json — ${adv.title}`,
      );
      continue;
    }
    if (entry.expires < today) {
      failures.push(
        `${adv.id} (${adv.package}): allowlist entry expired ${entry.expires} (today ${today}) — ` +
          "renew the rationale/expiry in web/audit-allowlist.json or fix the dependency",
      );
    }
  }

  const staleEntryIds = [...allowlist.keys()].filter((id) => !seen.has(id));
  return { failures, staleEntryIds };
}
