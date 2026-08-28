// SPDX-License-Identifier: Apache-2.0

// T-3104 acceptance criterion 1: "Every IP-entry field in the UI offers
// next-free; a test enumerates the fields so a new one added later without
// the picker is caught." This is a source-derived gate, in the same style
// as web/src/help/coverage.test.ts's screen-coverage checks: rather than
// hand-listing today's fields (which would only re-assert the current file
// list and say nothing about a field added tomorrow), it scans every editor
// under web/src/changesets/editors/ for a <Field label="..."> whose label
// is IP-address-shaped (matches /address(es)?/i, excluding "MAC address" —
// a MAC is not an IPAM-tracked value) and asserts the containing file
// imports one of the two sanctioned next-free mechanisms:
//
//   - AddressSuggest / NextFreePicker (the component pairing BridgeEditor,
//     VlanEditor, and InterfaceEditor all use as of T-3104), or
//   - useIpamAllocationsQuery (the lower-level hook SubnetStep.tsx uses
//     directly for its own gateway-specific auto-fill/live-grid-sync logic
//     — see that file's doc comment for why it doesn't use the bare
//     component).
//
// web/src/sdn/wizards/SubnetStep.tsx is checked by name against the hook
// requirement separately: it lives outside changesets/editors/, and its own
// "Address range (CIDR)" field is the subnet itself, not a next-free
// target — it's SubnetStep's *gateway* field the hook backs, per that
// file's own doc comment (T-3104 confirmed this already satisfies the "SDN
// subnet-gateway field" acceptance criterion; nothing there needed
// changing).
//
// ANTI-VACUITY (web/src/help/coverage.test.ts's convention): a floor and
// per-file assertions keep a regex that stops matching from passing
// silently — if this ever finds zero address fields, or the SubnetStep
// check no longer applies, the test fails loudly instead of reporting full
// coverage of an empty set.
import { readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

const SRC = resolve(__dirname, "..");
const EDITORS_DIR = join(SRC, "changesets", "editors");

const NEXT_FREE_IMPORT_RE = /\b(AddressSuggest|NextFreePicker|useIpamAllocationsQuery)\b/;

// Matches `<Field label="...">` (or a multi-prop opening tag with label as
// the first attribute — every editor in this codebase writes it first) for
// a label containing "address"/"addresses", case-insensitive.
const ADDRESS_FIELD_RE = /<Field\s+label="([^"]*\baddress(?:es)?\b[^"]*)"/gi;

function isIpEntryLabel(label: string): boolean {
  return !/mac/i.test(label);
}

function editorFiles(): string[] {
  return readdirSync(EDITORS_DIR)
    .filter((f) => f.endsWith(".tsx") && !f.endsWith(".test.tsx"))
    .map((f) => join(EDITORS_DIR, f));
}

function addressFieldLabels(source: string): string[] {
  return [...source.matchAll(ADDRESS_FIELD_RE)]
    .map((m) => m[1])
    .filter((label): label is string => label !== undefined && isIpEntryLabel(label));
}

describe("next-free coverage (T-3104 acceptance criterion 1)", () => {
  const files = editorFiles();

  it("finds address-entry fields to check (anti-vacuity floor)", () => {
    expect(files.length).toBeGreaterThan(0);
    const total = files.reduce((n, f) => n + addressFieldLabels(readFileSync(f, "utf8")).length, 0);
    // BridgeEditor, VlanEditor, InterfaceEditor each have one today.
    expect(total).toBeGreaterThanOrEqual(3);
  });

  for (const file of files) {
    const source = readFileSync(file, "utf8");
    const labels = addressFieldLabels(source);
    if (labels.length === 0) continue;
    const name = file.split("/").pop() ?? file;
    it(`${name} wires a next-free affordance for its "${labels.join('", "')}" field(s)`, () => {
      expect(NEXT_FREE_IMPORT_RE.test(source)).toBe(true);
    });
  }

  it("SubnetStep.tsx's SDN subnet-gateway field uses the shared allocation query", () => {
    const file = join(SRC, "sdn", "wizards", "SubnetStep.tsx");
    const source = readFileSync(file, "utf8");
    expect(NEXT_FREE_IMPORT_RE.test(source)).toBe(true);
  });
});
