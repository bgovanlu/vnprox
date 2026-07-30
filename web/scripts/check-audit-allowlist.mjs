#!/usr/bin/env node
// T-1806: CLI wrapper around auditAllowlist.mjs's pure logic. Replaces
// `npm audit --audit-level=high` in `make check` (see ../../Makefile). Run
// from web/: `node scripts/check-audit-allowlist.mjs`.

import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { advisoriesFromReport, evaluate, parseAllowlist } from "./auditAllowlist.mjs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ALLOWLIST_PATH = path.join(__dirname, "..", "audit-allowlist.json");

function runNpmAudit() {
  try {
    const stdout = execFileSync("npm", ["audit", "--json"], {
      encoding: "utf8",
      maxBuffer: 32 * 1024 * 1024,
    });
    return JSON.parse(stdout);
  } catch (err) {
    // `npm audit` exits non-zero the moment it finds anything, including at
    // low/moderate severity — but stdout still carries the full JSON report.
    if (err.stdout) {
      return JSON.parse(err.stdout);
    }
    throw err;
  }
}

function todayUTC() {
  return new Date().toISOString().slice(0, 10);
}

function main() {
  const allowlistRaw = readFileSync(ALLOWLIST_PATH, "utf8");
  const allowlist = parseAllowlist(allowlistRaw);

  const report = runNpmAudit();
  const advisories = advisoriesFromReport(report);
  const { failures, staleEntryIds } = evaluate(advisories, allowlist, todayUTC());

  if (failures.length > 0) {
    console.error("npm audit: unaccepted or expired high/critical advisories found:\n");
    for (const f of failures) console.error(`  - ${f}`);
    console.error(
      "\nTo accept a new advisory: add an entry (id, package, rationale, expires) to " +
        "web/audit-allowlist.json. To renew an expired one: bump its expires date after " +
        "re-confirming the rationale still holds. See docs/development.md's CI section.",
    );
    process.exitCode = 1;
    return;
  }

  if (staleEntryIds.length > 0) {
    console.warn(
      `note: audit-allowlist.json has entries npm audit no longer reports (fixed upstream?): ` +
        `${staleEntryIds.join(", ")} — consider removing them.`,
    );
  }

  const n = advisories.length;
  console.log(
    `npm audit: ${n} high/critical advisor${n === 1 ? "y" : "ies"} found, all allowlisted and unexpired.`,
  );
}

main();
