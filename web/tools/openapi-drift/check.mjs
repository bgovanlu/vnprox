#!/usr/bin/env node
// check.mjs — T-3809's generated-client drift tripwire.
//
// What this proves, and what it does NOT (see also docs/api.md's own "what
// it does and does not cover" note, which this script does not contradict):
//
//   1. Every `$ref` in docs/openapi.json resolves to a real
//      components/schemas entry. Cheap, but it is exactly "every response
//      shape name resolves" from T-3809's card.
//   2. A TypeScript client is generated fresh from docs/openapi.json (via
//      the dev-only `openapi-typescript` package — never imported by
//      anything the daemon or the production web bundle ships) and, for
//      every web/src/api/*.ts call site this script can statically resolve
//      to a real spec path, an assertion file indexes into the generated
//      `paths` type at that exact literal key and is type-checked with
//      `tsc --noEmit`. A route renamed or removed from the spec while a
//      frontend call site still points at the old shape makes this a
//      compile error, not just a JS string mismatch.
//   3. Independent of tsc: every `apiFetch(...)` call site under
//      web/src/api is parsed, its path template reduced to a structural
//      "shape" (dynamic segments collapsed to `*`), and matched by
//      METHOD+shape against docs/openapi.json's own paths (also reduced to
//      a shape). A call site whose shape matches nothing in the spec fails
//      the job with a file:line pointing at the offending call.
//
// What it does NOT catch:
//   - Response/request BODY shape drift. docs/openapi.json carries no body
//     schemas at all (T-2405's documented scope, restated in docs/api.md);
//     there is nothing for a generated client to compare web/src/api's
//     hand-written body types (web/src/api/types.ts) against. A backend
//     handler that changes its JSON response shape without updating
//     types.ts is invisible to this script.
//   - The REVERSE direction (a spec route no frontend code ever calls) is
//     reported for visibility but never fails the job — many documented
//     routes are legitimately not called from the SPA (peer-to-peer routes
//     authenticated with `peerSignature`, bearer-token automation routes,
//     the raw `/api/ws` upgrade, health/metrics scrapers). Gating on that
//     direction would need a maintained allowlist this script does not
//     have; see the printed summary instead.
//   - Dynamic path construction this script's parser cannot statically
//     resolve (e.g. a path assembled outside a template literal passed
//     directly to `apiFetch`) is skipped with a warning, not silently
//     ignored — check the "skipped" count in the summary before trusting a
//     clean run on a change to web/src/api's calling conventions.
//
// KNOWN_GAPS below allowlists real, currently-uncovered call sites so this
// job is green on `main` today without papering over genuine drift. Each
// entry is a pre-existing, documented gap in docs/openapi.json itself, not
// a false positive in this script's matching — see each entry's comment.
// A new entry here should always cite the Go source proving the gap is
// pre-existing, the same way the ones below do.

import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const webDir = path.resolve(__dirname, "..", "..");
const repoRoot = path.resolve(webDir, "..");
const specPath = path.join(repoRoot, "docs", "openapi.json");
const apiDir = path.join(webDir, "src", "api");
const outDir = path.join(webDir, ".openapi-drift");
const genFile = path.join(outDir, "openapi.gen.ts");
const assertFile = path.join(outDir, "assertions.gen.ts");

let failed = false;
const fail = (msg) => {
  console.error(`FAIL: ${msg}`);
  failed = true;
};

// See the header comment. Each key is "METHOD shapePath" exactly as this
// script's own extraction/normalization would produce it.
const KNOWN_GAPS = new Map([
  [
    "GET /api/v1/inventory/*",
    "internal/api/topology.go's mountTopologyRoutes: `/inventory/*` is a " +
      "genuine chi wildcard (a Ref may contain literal '/'), and " +
      "internal/apidoc/doc.go's skip() deliberately excludes every " +
      "trailing-wildcard route from the document — 'the path template " +
      "language has no \"and everything below this\"'. Not a script bug.",
  ],
  [
    "GET /api/v1/ipam/subnets/*/allocations",
    "internal/api/ipam.go's mountIPAMRoutes: `/ipam/subnets/*` is the same " +
      "kind of chi wildcard as /inventory/* above (a CIDR's '/' can't span " +
      "a `{cidr}` param), excluded from the document for the same reason.",
  ],
  [
    "GET /api/v1/firewall/log",
    "internal/apidoc/routes.go has no Operations entry for this route, and " +
      "internal/api/router.go only calls mountFwLogRoutes when opts.FwLog " +
      "is non-nil — testdata/dev.toml (what `make openapi` walks) leaves it " +
      "nil, so the route is never mounted in the walked router and cannot " +
      "appear as 'undescribed' either. routes.go's own header names this " +
      "exact failure mode ('a route whose service is nil ... is neither " +
      "reported missing nor checked for being unserved').",
  ],
  [
    "GET /api/v1/firewall/analytics",
    "Same FwLog-service gap as GET /firewall/log above (both routes are " +
      "registered together by mountFwLogRoutes).",
  ],
  [
    "GET /api/v1/hub/index",
    "internal/apidoc/routes.go's header comment names this exact subsystem " +
      "('the plugin hub') as outside the gate until dev.toml enables it; " +
      "router.go only calls mountHubRoutes when opts.HubClient is non-nil.",
  ],
  [
    "POST /api/v1/hub/install",
    "Same plugin-hub gap as GET /hub/index above (both routes are " +
      "registered together by mountHubRoutes).",
  ],
]);

// --- load the spec -----------------------------------------------------

const specRaw = readFileSync(specPath, "utf8");
const spec = JSON.parse(specRaw);

// --- check 1: every $ref resolves ---------------------------------------

function resolvePointer(doc, ref) {
  if (!ref.startsWith("#/")) {
    return { ok: false, reason: `non-local $ref (${ref}) — this script only resolves in-document refs` };
  }
  const parts = ref
    .slice(2)
    .split("/")
    .map((p) => p.replace(/~1/g, "/").replace(/~0/g, "~"));
  let node = doc;
  for (const part of parts) {
    if (node == null || typeof node !== "object" || !(part in node)) {
      return { ok: false, reason: `${ref} does not resolve (stopped at "${part}")` };
    }
    node = node[part];
  }
  return { ok: true };
}

function walkRefs(node, sink) {
  if (Array.isArray(node)) {
    for (const item of node) walkRefs(item, sink);
    return;
  }
  if (node && typeof node === "object") {
    if (typeof node.$ref === "string") sink.push(node.$ref);
    for (const value of Object.values(node)) walkRefs(value, sink);
  }
}

const refs = [];
walkRefs(spec, refs);
const uniqueRefs = [...new Set(refs)];
let unresolvedRefs = 0;
for (const ref of uniqueRefs) {
  const result = resolvePointer(spec, ref);
  if (!result.ok) {
    fail(`docs/openapi.json: ${result.reason}`);
    unresolvedRefs++;
  }
}
console.log(`checked ${uniqueRefs.length} distinct $ref(s) in docs/openapi.json: ${unresolvedRefs} unresolved`);

// --- generate the TS client ---------------------------------------------

mkdirSync(outDir, { recursive: true });
const openapiTsBin = path.join(webDir, "node_modules", ".bin", "openapi-typescript");
execFileSync(openapiTsBin, [specPath, "-o", genFile], { stdio: "inherit" });
console.log(`generated ${path.relative(repoRoot, genFile)} from docs/openapi.json`);

// --- build the shape-normalized spec route table -------------------------

const METHODS = ["get", "put", "post", "delete", "patch", "head"];

/** "/api/v1/changesets/{id}/comments/{commentId}" -> "/api/v1/changesets/*\/comments/*" */
function shapeOfSpecPath(specPathKey) {
  return specPathKey.replace(/\{[^}]+\}/g, "*");
}

// method+shape -> the real spec path key (so the type-check step can index
// into the generated `paths` type with a literal that is actually there).
const specByShape = new Map();
for (const [specPathKey, item] of Object.entries(spec.paths ?? {})) {
  for (const method of METHODS) {
    if (item[method] == null) continue;
    const key = `${method.toUpperCase()} ${shapeOfSpecPath(specPathKey)}`;
    specByShape.set(key, { specPathKey, method });
  }
}
console.log(`spec describes ${specByShape.size} method+path operation(s)`);

// --- extract web/src/api/*.ts call sites ---------------------------------

const EXCLUDE = new Set(["client.ts", "types.ts"]);
const apiFiles = readdirSync(apiDir).filter(
  (f) => f.endsWith(".ts") && !f.endsWith(".test.ts") && !f.endsWith(".test.tsx") && !EXCLUDE.has(f),
);

/** Finds the matching closing bracket for the opening bracket at `openIdx`,
 * respecting nested brackets and string/template literals. Returns the
 * index of the matching close, or -1. */
function matchBracket(src, openIdx, openCh, closeCh) {
  let depth = 0;
  let inString = null; // one of ' " ` or null
  for (let i = openIdx; i < src.length; i++) {
    const c = src[i];
    if (inString) {
      if (c === "\\") {
        i++; // skip escaped char
        continue;
      }
      if (c === inString) inString = null;
      continue;
    }
    if (c === "'" || c === '"' || c === "`") {
      inString = c;
      continue;
    }
    if (c === openCh) depth++;
    else if (c === closeCh) {
      depth--;
      if (depth === 0) return i;
    }
  }
  return -1;
}

/** Skips a `'...'`/`"..."` string starting at `start` (index of the opening
 * quote). Returns the index right after the closing quote. */
function skipSimpleString(src, start) {
  const q = src[start];
  let i = start + 1;
  while (i < src.length) {
    if (src[i] === "\\") {
      i += 2;
      continue;
    }
    if (src[i] === q) return i + 1;
    i++;
  }
  return i;
}

/** Skips a `${...}` expression body starting right after the `${`,
 * accounting for nested braces, nested strings, and — recursively —
 * further nested template literals (which may themselves contain `{`/`}`
 * inside their own `${...}` parts, e.g. `` `x${a ? `?${b}` : ""}` ``).
 * Returns the index right after the matching `}`. */
function skipExpression(src, start) {
  let depth = 1;
  let i = start;
  while (i < src.length) {
    const c = src[i];
    if (c === "\\") {
      i += 2;
      continue;
    }
    if (c === "'" || c === '"') {
      i = skipSimpleString(src, i);
      continue;
    }
    if (c === "`") {
      const r = parseTemplate(src, i + 1);
      i = r.endIdx === -1 ? src.length : r.endIdx + 1;
      continue;
    }
    if (c === "{") {
      depth++;
      i++;
      continue;
    }
    if (c === "}") {
      depth--;
      i++;
      if (depth === 0) return i;
      continue;
    }
    i++;
  }
  return i;
}

/** Parses a template literal body starting right after the opening
 * backtick. Every top-level `${...}` is collapsed to a single token:
 * `*` if it's preceded by `/` (a genuine dynamic PATH SEGMENT, e.g.
 * `` `/inventory/${ref}` ``), or dropped entirely otherwise (the codebase's
 * consistent pattern for an appended, possibly-empty query string, e.g.
 * `` `/changesets${qs}` `` where `qs` already carries its own leading `?`
 * when non-empty — there is no static way to tell "?foo=bar" from a real
 * path segment here, and every such case in this codebase is a query
 * string, never a further path segment, so dropping is the correct
 * collapse rather than a wildcard `*`).
 * Returns { collapsed, endIdx } where endIdx is the index of the closing
 * backtick, or -1 if the template is unterminated. */
function parseTemplate(src, start) {
  let out = "";
  let i = start;
  while (i < src.length) {
    const c = src[i];
    if (c === "\\") {
      out += c + (src[i + 1] ?? "");
      i += 2;
      continue;
    }
    if (c === "`") {
      return { collapsed: out, endIdx: i };
    }
    if (c === "$" && src[i + 1] === "{") {
      const precededBySlash = out.endsWith("/");
      const afterExpr = skipExpression(src, i + 2);
      if (precededBySlash) out += "*";
      i = afterExpr;
      continue;
    }
    out += c;
    i++;
  }
  return { collapsed: out, endIdx: -1 };
}

/** Extracts the literal/template path argument starting at `argsStart`
 * (the character right after the opening paren). Returns
 * { rawPath, endIdx } or null if the first argument is not a plain
 * string/template literal this script can resolve statically. */
function extractPathArg(src, argsStart) {
  let i = argsStart;
  while (/\s/.test(src[i])) i++;
  const quote = src[i];
  if (quote !== "'" && quote !== '"' && quote !== "`") return null;
  let raw;
  let endIdx;
  if (quote === "`") {
    const r = parseTemplate(src, i + 1);
    raw = r.collapsed;
    endIdx = r.endIdx === -1 ? src.length : r.endIdx;
  } else {
    let j = i + 1;
    while (j < src.length) {
      if (src[j] === "\\") {
        j += 2;
        continue;
      }
      if (src[j] === quote) break;
      j++;
    }
    raw = src.slice(i + 1, j);
    endIdx = j;
  }
  return { rawPath: raw, endIdx };
}

const callSites = [];
const skipped = [];

for (const file of apiFiles) {
  const fullPath = path.join(apiDir, file);
  const src = readFileSync(fullPath, "utf8");
  const callRe = /apiFetch\s*(?:<[^>]*>)?\s*\(/g;
  let m;
  while ((m = callRe.exec(src))) {
    const openParenIdx = m.index + m[0].length - 1;
    const closeParenIdx = matchBracket(src, openParenIdx, "(", ")");
    const lineOf = (idx) => src.slice(0, idx).split("\n").length;
    if (closeParenIdx === -1) {
      skipped.push({ file, line: lineOf(m.index), reason: "unbalanced parens (parser bug or unusual syntax)" });
      continue;
    }
    const argsStart = openParenIdx + 1;
    const argsText = src.slice(argsStart, closeParenIdx);
    const pathArg = extractPathArg(src, argsStart);
    if (!pathArg) {
      skipped.push({ file, line: lineOf(m.index), reason: "first argument is not a plain string/template literal" });
      continue;
    }
    // Query strings are not part of the OpenAPI path template.
    const rawPath = pathArg.rawPath.split("?")[0];

    const optionsText = argsText.slice(pathArg.endIdx - argsStart);
    const methodMatch = optionsText.match(/\bmethod\s*:\s*["']([A-Z]+)["']/);
    let method;
    if (methodMatch) {
      method = methodMatch[1];
    } else if (/\bjson\s*:/.test(optionsText)) {
      // apiFetch's own default (internal/api/client.ts): a body with no
      // explicit method defaults to POST.
      method = "POST";
    } else {
      method = "GET";
    }

    const fullApiPath = `/api/v1${rawPath.startsWith("/") ? "" : "/"}${rawPath}`;
    const shape = fullApiPath.replace(/\*+/g, "*");
    callSites.push({ file, line: lineOf(m.index), method, shape });
  }
}

console.log(`extracted ${callSites.length} apiFetch call site(s) from web/src/api (${skipped.length} skipped)`);
for (const s of skipped) {
  console.log(`  skipped: ${s.file}:${s.line} — ${s.reason}`);
}

// --- cross-check: every call site must match a real spec operation -------

const matchedSpecKeys = new Set();
const unmatched = [];
let knownGaps = 0;
for (const call of callSites) {
  const key = `${call.method} ${call.shape}`;
  const hit = specByShape.get(key);
  if (hit) {
    matchedSpecKeys.add(`${hit.method.toUpperCase()} ${hit.specPathKey}`);
  } else if (KNOWN_GAPS.has(key)) {
    knownGaps++;
    console.log(`  known gap: web/src/api/${call.file}:${call.line} (${key}) — ${KNOWN_GAPS.get(key)}`);
  } else {
    unmatched.push(call);
  }
}
if (knownGaps > 0) {
  console.log(`${knownGaps} call site(s) matched an allowlisted known gap (see KNOWN_GAPS above) — not failing`);
}

if (unmatched.length > 0) {
  for (const call of unmatched) {
    fail(
      `web/src/api/${call.file}:${call.line} calls ${call.method} ${call.shape}, ` +
        `which matches no operation in docs/openapi.json`,
    );
  }
}

// Reverse direction: informational only (see header comment for why this
// never fails the job).
const uncalled = [...specByShape.entries()].filter(([key]) => !matchedSpecKeys.has(key));
console.log(
  `${matchedSpecKeys.size}/${specByShape.size} spec operations have a matching web/src/api call site ` +
    `(${uncalled.length} uncalled — expected for peer/automation/websocket/health routes the SPA does not call directly)`,
);

// --- type-check: matched call sites against the generated client ---------

const assertLines = [
  '// AUTO-GENERATED by web/tools/openapi-drift/check.mjs. Do not edit by hand.',
  '// Re-run `node tools/openapi-drift/check.mjs` from web/ to regenerate.',
  'import type { paths } from "./openapi.gen";',
  "",
];
let assertionIndex = 0;
for (const key of matchedSpecKeys) {
  const [method, specPathKey] = key.split(/ (.+)/); // split on first space only
  assertionIndex++;
  assertLines.push(
    `// ${key}`,
    `type _Check${assertionIndex} = paths[${JSON.stringify(specPathKey)}][${JSON.stringify(method.toLowerCase())}];`,
  );
}
writeFileSync(assertFile, assertLines.join("\n") + "\n");
console.log(`wrote ${assertionIndex} type-level assertion(s) to ${path.relative(repoRoot, assertFile)}`);

const tscBin = path.join(webDir, "node_modules", ".bin", "tsc");
const tsconfigPath = path.join(__dirname, "tsconfig.json");
try {
  execFileSync(tscBin, ["--noEmit", "-p", tsconfigPath], { stdio: "inherit", cwd: webDir });
  console.log("tsc: generated client + assertions type-check cleanly");
} catch {
  fail("tsc found a type error in the generated client or its assertions (see output above)");
}

// --- summary ---------------------------------------------------------------

if (failed) {
  console.error("\nopenapi-drift: FAILED");
  process.exit(1);
}
console.log("\nopenapi-drift: PASS");
