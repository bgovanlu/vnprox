# `vnproxctl -o json` — machine-readable output contract

Every `vnproxctl` subcommand that emits data accepts `-o json` (`table` is
the default; see `cmd/vnproxctl/output.go`'s `parseOutputFormat`). This
document is the stable schema half of that contract — the same role
`docs/api.md` plays for the HTTP API — so a script parsing `vnproxctl ...
-o json` has something to depend on other than reading the Go source.

**How this document stays honest.** Each table below is fenced by an HTML
comment pair (`<!-- cli-json:<command>:begin -->` … `:end -->`) that
`cmd/vnproxctl/jsondoc_test.go` parses at test time, decodes that command's
*actual* JSON output from a real (or mock-backed) invocation, and fails the
build if the two disagree in either direction: an undocumented field, a
documented field the command no longer emits, or a type mismatch. This is
the same anti-drift idiom `internal/telemetry`'s `ParseDocTable`/`CompareDoc`
(T-2503 AC6) and `internal/perfbudget`'s identical pair (T-2506 AC2) already
use elsewhere in this repo, adapted to decode real JSON bytes rather than
reflect a single Go struct — see `cmd/vnproxctl/jsondoc.go`'s package doc
comment for why.

**Reading the tables.**

- **Field** is the top-level JSON key. A trailing `?` (`selection?`) means
  the field is `omitempty` on the Go side: two valid invocations of the same
  command can legitimately produce different top-level key sets (e.g.
  `vnproxctl verify --suite=hardware` carries `suite` but not `selection`; a
  `--only` run is the reverse), so its absence from one particular sample is
  not drift.
- **Type** is coarse — `string`, `number`, `boolean`, `null`, `object`,
  `array`, or `array of <type>` — because this document is read by a script
  author deciding how to parse a field, not by something that needs a fully
  expanded nested schema. Several fields here embed a type documented in its
  own package (`internal/verify`'s `Report`, `internal/doctor`'s `Report`,
  `internal/backup`'s `Manifest`/`Plan`/`BundlePlan`) or in `docs/api.md`
  (every `changeset` field below is `docs/api.md`'s Changesets section,
  verbatim) — this document is deliberately the **top-level envelope** each
  command returns, not a second copy of those contracts.
- **Description** says what the field is and, for a command whose JSON
  mirrors an HTTP response 1:1, which route.

**One command is documented differently: `watch`.** Every command above
emits exactly one JSON document per invocation, which is what
`assertDocumentedJSON`/`jsondoc_test.go`'s fenced-table mechanism decodes and
compares. `vnproxctl watch -o json` (T-4010) instead streams
**newline-delimited JSON** — it never terminates on its own (it is a live
view over the WS `"events"` topic) and one event or connection-status
transition becomes one line, not one field of one document. Its shape is
documented in its own section below using the same table format for
readability, but there is no fenced anti-drift pair for it in the source —
`cmd/vnproxctl/watchcmd_test.go`'s `TestRunWatch_NDJSONShape` is that
command's own line-shape regression test instead.

**Commands that do not appear here** emit no structured data to document —
either pure side effects with a one-line confirmation (`vnproxctl telemetry
preview`, which is a deliberate exception: it prints the *exact bytes* the
daemon would receive, which already **is** JSON, so wrapping it in another
JSON envelope would defeat its own "these are the literal bytes sent"
promise), a raw non-JSON document passed through verbatim (`vnproxctl policy
examples`, which prints the shipped example **YAML** policy — there is no
JSON shape to document because the command's job is to print exactly that
file), or shell script text (`vnproxctl completion bash|zsh`).

---

## status

`vnproxctl status -o json` — local daemon health, PVE reachability, and peer
status in one probe (no HTTP route of its own; this command probes three
things directly the way the table output does).

<!-- cli-json:status:begin -->
| Field | Type | Description |
|---|---|---|
| `endpoint` | string | the local daemon health endpoint that was probed |
| `reachable` | boolean | whether the local daemon answered at all |
| `httpStatus?` | string | the HTTP status line, when the daemon answered |
| `fetchError?` | string | why the local daemon probe failed, when it did |
| `latencyMs` | number | round-trip time to the local daemon, milliseconds |
| `daemon` | object | the local daemon's own `GET /health` body (`status`, `version`, `collectors`) |
| `pve` | object | PVE reachability: `{configured, reachable, nodeCount?, error?}` |
| `peers` | array of object | each configured peer's reachability, version and protocol compatibility |
<!-- cli-json:status:end -->

## snapshots list

`vnproxctl snapshots list -o json` — direct SQLite read, works daemon-down.
Emits a **JSON array**; each element:

<!-- cli-json:snapshots list:begin -->
| Field | Type | Description |
|---|---|---|
| `id` | string | snapshot id |
| `kind` | string | `pre_apply` or `manual` |
| `takenAt` | number | unix seconds |
| `changesetId?` | string | the changeset this snapshot was taken for, if any |
| `note?` | string | operator note, if any |
| `nodes` | array of string | nodes this snapshot has captured files for |
<!-- cli-json:snapshots list:end -->

## snapshots restore

`vnproxctl snapshots restore <id> -o json` — direct DB read + local file
write + `ifreload -a`, works daemon-down.

<!-- cli-json:snapshots restore:begin -->
| Field | Type | Description |
|---|---|---|
| `restored` | boolean | always `true` on success (a failure exits non-zero before this is printed) |
| `snapshotId` | string | the snapshot that was restored |
| `node` | string | the node whose file was restored (this host, or `--node`) |
| `interfacesPath` | string | where the file was written |
| `otherNodes` | array | other nodes the snapshot also carries files for (array of string), not restored here |
<!-- cli-json:snapshots restore:end -->

## rollback-now

`vnproxctl rollback-now <changeset> -o json` — direct DB + file write,
bypasses confirm, works daemon-down.

<!-- cli-json:rollback-now:begin -->
| Field | Type | Description |
|---|---|---|
| `changesetId` | string | the changeset that was force-rolled-back |
| `status` | string | `rolled_back` or `failed`, the changeset's new terminal status |
| `snapshotId` | string | the pre-apply snapshot restored from |
| `node` | string | the node whose file was restored |
| `interfacesPath` | string | where the file was written |
| `otherNodes` | array | other nodes the snapshot also carries files for (array of string), not restored here |
<!-- cli-json:rollback-now:end -->

## backup

`vnproxctl backup -o json` — writes an integrity-checked archive of vnprox's
own state; safe against a running daemon.

<!-- cli-json:backup:begin -->
| Field | Type | Description |
|---|---|---|
| `path` | string | the archive path written |
| `bytes` | number | archive size |
| `node` | string | the node the archive was taken on |
| `createdAt` | string | RFC3339 timestamp |
| `schemaVersion` | number | the store schema version archived |
| `includesKeyMaterial` | boolean | whether `--include-keys` was used |
| `secretClasses` | array | which key classes are included (array of string), empty unless `--include-keys` |
| `entries` | number | count of files in the archive's manifest |
| `pruned` | array | archives removed by `--keep` (array of string), usually empty |
| `warnings` | array | non-fatal notes about the archive, e.g. a key file was missing (array of string), usually empty |
<!-- cli-json:backup:end -->

## restore

`vnproxctl restore <archive> -o json` — replaces this node's store from a
backup archive; refuses against a running daemon.

<!-- cli-json:restore:begin -->
| Field | Type | Description |
|---|---|---|
| `archivePath` | string | the archive restored from |
| `manifest` | object | the archive's own manifest (node, createdAt, schemaVersion, includesKeyMaterial, secretClasses, entries) |
| `storePath` | string | the store that was (or, `--dry-run`, would be) replaced |
| `preRestorePath` | string | where the current store was moved aside to |
| `schemaFrom` | number | the archive's schema version |
| `schemaTo` | number | the schema version restored to (forward-migrated) |
| `installConfig` | boolean | whether `--restore-config` was given |
| `configPath?` | string | where the archived config was (or would be) installed |
| `installKeys` | boolean | whether `--restore-keys` was given |
| `keyPaths?` | array of string | which key files were (or would be) installed |
| `notes` | array of string | operator-facing notes about what does/does not carry over |
| `applied` | boolean | `false` for `--dry-run` |
<!-- cli-json:restore:end -->

## support-bundle

`vnproxctl support-bundle -o json` — a REDACTED diagnostic archive; no
`--include-keys` equivalent exists for this command.

<!-- cli-json:support-bundle:begin -->
| Field | Type | Description |
|---|---|---|
| `path` | string | the bundle path written (empty for `--dry-run`) |
| `bytes` | number | bundle size |
| `plan` | object | `internal/backup.BundlePlan`: node, collectedAt, collectors, entries (name/about/redaction), omitted |
<!-- cli-json:support-bundle:end -->

## doctor

`vnproxctl doctor -o json` — preflight/self-check; works before install and
daemon-down (daemon-dependent checks report `skip` rather than `fail` when
unreachable, or run for real with `--live`).

<!-- cli-json:doctor:begin -->
| Field | Type | Description |
|---|---|---|
| `generatedAt` | string | RFC3339 timestamp |
| `version` | string | vnproxctl's reported version |
| `results` | array of object | one entry per check: `{check, status, detail, remediation?}` |
| `summary` | object | pass/warn/fail/skip counts |
<!-- cli-json:doctor:end -->

## verify

`vnproxctl verify -o json` — the hardware-validation suite's report
(`internal/verify.Report`).

<!-- cli-json:verify:begin -->
| Field | Type | Description |
|---|---|---|
| `reportVersion` | number | the artifact schema version (currently 1) |
| `generatedAt` | string | RFC3339 timestamp, when the run finished |
| `suite?` | string | which suite ran (`hardware`\|`multinode`\|`destructive`), absent for a `--only` run |
| `selection?` | array of string | the `--only` check ids, absent for a suite run |
| `environment` | object | what this run attributed itself to (versions, mock/real, etc.) |
| `results` | array of object | one entry per selected check, registry order: `{id, status, evidence, ...}` |
| `summary` | object | pass/fail/skip counts |
<!-- cli-json:verify:end -->

## verify --list

`vnproxctl verify --list -o json` — the check registry, not a run. Emits a
**JSON array**; each element:

<!-- cli-json:verify --list:begin -->
| Field | Type | Description |
|---|---|---|
| `id` | string | check id |
| `area` | string | which subsystem the check covers |
| `suite` | string | which suite(s) include it |
| `precondition` | string | the hardware/config this check needs to run for real |
| `matrixRow` | number | its row in the hardware-validation matrix |
| `minNodes` | number | minimum cluster size the check needs |
<!-- cli-json:verify --list:end -->

## telemetry status

`vnproxctl telemetry status -o json` — local, daemon-independent.

<!-- cli-json:telemetry status:begin -->
| Field | Type | Description |
|---|---|---|
| `enabled` | boolean | whether `[telemetry] enabled = true` |
| `endpoint` | string | the configured collector URL, empty if none |
| `installId` | string | this install's correlator, empty if none generated yet |
<!-- cli-json:telemetry status:end -->

## telemetry send

`vnproxctl telemetry send --report <file> -o json` — submits one report;
refuses unless telemetry is enabled and configured.

<!-- cli-json:telemetry send:begin -->
| Field | Type | Description |
|---|---|---|
| `bytesSent` | number | size of the report submitted |
| `endpoint` | string | the collector URL it was sent to |
<!-- cli-json:telemetry send:end -->

## telemetry reset-id

`vnproxctl telemetry reset-id -o json` — replaces the install-id with a new
random ULID. The previous id is deliberately not returned, printed, logged
or audited.

<!-- cli-json:telemetry reset-id:begin -->
| Field | Type | Description |
|---|---|---|
| `installId` | string | the new install-id |
<!-- cli-json:telemetry reset-id:end -->

## remote topology

`vnproxctl remote topology -o json` — `GET /topology`.

<!-- cli-json:remote topology:begin -->
| Field | Type | Description |
|---|---|---|
| `generatedAt` | number | unix seconds |
| `nodes` | array of object | `{ref, kind, node}` |
| `edges` | array | `{from, to, edgeKind}` per element |
<!-- cli-json:remote topology:end -->

## remote findings

`vnproxctl remote findings -o json` — `GET /findings`.

<!-- cli-json:remote findings:begin -->
| Field | Type | Description |
|---|---|---|
| `items` | array of object | `{id, source, check, severity, detail, nodes, refs?, fixable}` |
<!-- cli-json:remote findings:end -->

## remote drift

`vnproxctl remote drift -o json` — `GET /drift`. Emits a **JSON array**;
each element:

<!-- cli-json:remote drift:begin -->
| Field | Type | Description |
|---|---|---|
| `id` | string | finding id |
| `check` | string | which drift check fired |
| `severity` | string | `error`\|`warning`\|`info` |
| `detail` | string | human-readable explanation |
| `nodes` | array of string | affected nodes |
| `refs?` | array of string | affected entity refs |
| `fixable` | boolean | whether `POST /drift/{id}/fix` can stage a fixing changeset |
<!-- cli-json:remote drift:end -->

## remote audit

`vnproxctl remote audit -o json` — `GET /audit`.

<!-- cli-json:remote audit:begin -->
| Field | Type | Description |
|---|---|---|
| `items` | array of object | `{id, at, username, action, target?, changesetId?, result, detail?}` |
| `nextCursor?` | string | pass as `--cursor` to page further |
| `partial?` | boolean | `true` when one or more cluster nodes were unreachable |
| `failedNodes?` | array of string | which nodes were unreachable, when `partial` |
<!-- cli-json:remote audit:end -->

## changeset (shared shape)

`docs/api.md`'s Changesets section, verbatim — the shape returned by
`vnproxctl remote changesets get\|validate\|apply\|confirm\|rollback\|create`
and embedded in `spec import`'s and `apply`'s own payloads below.

<!-- cli-json:changeset:begin -->
| Field | Type | Description |
|---|---|---|
| `id` | string | changeset id |
| `title` | string | operator-facing title |
| `author` | string | who staged it |
| `status` | string | `draft`\|`validated`\|`awaiting_confirm`\|`applying`\|`committed`\|`rolled_back`\|`failed` |
| `ops` | array | the staged `change.Op` batch (array of object), often empty |
| `plan?` | object | the last validate/apply's execution plan |
| `applyLog?` | array | apply-time log entries (array of object), once applied |
| `confirmDeadline?` | number | unix seconds, while `awaiting_confirm` |
| `findings` | array | validation findings (array of object: `{severity, code, message, ref?, fix?}`), often empty |
| `createdAt` | number | unix seconds |
| `updatedAt` | number | unix seconds |
| `touchesMgmtPath` | boolean | whether any op could affect the management path |
<!-- cli-json:changeset:end -->

## remote changesets list

`vnproxctl remote changesets list -o json` — `GET /changesets`. Emits a
**JSON array** of the [changeset](#changeset-shared-shape) shape above.

<!-- cli-json:remote changesets list:begin -->
| Field | Type | Description |
|---|---|---|
| `id` | string | changeset id |
| `title` | string | operator-facing title |
| `author` | string | who staged it |
| `status` | string | `draft`\|`validated`\|`awaiting_confirm`\|`applying`\|`committed`\|`rolled_back`\|`failed` |
| `ops` | array | the staged `change.Op` batch (array of object), often empty |
| `findings` | array | validation findings (array of object: `{severity, code, message, ref?, fix?}`), often empty |
| `createdAt` | number | unix seconds |
| `updatedAt` | number | unix seconds |
| `touchesMgmtPath` | boolean | whether any op could affect the management path |
<!-- cli-json:remote changesets list:end -->

## remote changesets diff

`vnproxctl remote changesets diff <id> -o json` — `GET /changesets/{id}/diff`.

<!-- cli-json:remote changesets diff:begin -->
| Field | Type | Description |
|---|---|---|
| `files` | array of object | `{node, path, unified, changed}` |
| `ops` | array of object | `{op, target, node, summary}` |
<!-- cli-json:remote changesets diff:end -->

## remote changesets discard

`vnproxctl remote changesets discard <id> -o json` — `DELETE /changesets/{id}`.

<!-- cli-json:remote changesets discard:begin -->
| Field | Type | Description |
|---|---|---|
| `id` | string | the discarded changeset's id |
| `discarded` | boolean | always `true` on success |
<!-- cli-json:remote changesets discard:end -->

## apply --plan

`vnproxctl apply <spec.yaml> --plan -o json` — `POST /spec/import` then
`GET /changesets/{id}/diff`, discarding the preview changeset it necessarily
created. Never applies.

<!-- cli-json:apply --plan:begin -->
| Field | Type | Description |
|---|---|---|
| `changeset` | object | the (already-discarded) preview [changeset](#changeset-shared-shape) |
| `notInSpec` | array of string | entities present live but absent from the spec, reported only |
| `diff` | object | `{files, ops}` — same shape as `remote changesets diff` |
| `pending` | boolean | whether the spec differs from live at all |
<!-- cli-json:apply --plan:end -->

## apply --apply

`vnproxctl apply <spec.yaml> --apply -o json` — imports, applies, polls to
`committed`, auto-confirming non-interactively.

<!-- cli-json:apply --apply:begin -->
| Field | Type | Description |
|---|---|---|
| `changeset` | object | the final [changeset](#changeset-shared-shape), whatever status it ended in |
| `timedOut` | boolean | `true` if `--apply-timeout` elapsed before reaching `committed` |
<!-- cli-json:apply --apply:end -->

## spec export

`vnproxctl spec export -o json` — `GET /spec`. (Default `-o table` output is
the raw YAML document itself, not JSON — see `docs/api.md`'s "Declarative
cluster network spec" section.)

<!-- cli-json:spec export:begin -->
| Field | Type | Description |
|---|---|---|
| `content` | string | the exact YAML document, byte-stable across unchanged-state exports |
| `specVersion` | number | currently always `1` |
<!-- cli-json:spec export:end -->

## spec import

`vnproxctl spec import <file> -o json` — `POST /spec/import`. Stages a draft
changeset and stops; never applies.

<!-- cli-json:spec import:begin -->
| Field | Type | Description |
|---|---|---|
| `id` | string | the staged draft changeset's id |
| `title` | string | operator-facing title |
| `author` | string | who staged it |
| `status` | string | `draft` or `validated` — never `applying`/`committed` |
| `ops` | array | the diff's reconciling `change.Op` batch (array of object), often empty |
| `findings` | array | validation findings (array of object: `{severity, code, message, ref?, fix?}`), often empty |
| `createdAt` | number | unix seconds |
| `updatedAt` | number | unix seconds |
| `touchesMgmtPath` | boolean | whether any op could affect the management path |
| `notInSpec` | array of string | entities present live but absent from the spec, reported only |
<!-- cli-json:spec import:end -->

## spec pin

`vnproxctl spec pin [<file>] -o json` — `GET /spec/pin` (bare) or
`POST /spec/pin <file>`.

<!-- cli-json:spec pin:begin -->
| Field | Type | Description |
|---|---|---|
| `pinned` | boolean | whether a spec is currently pinned |
| `content?` | string | the pinned YAML document, byte-for-byte |
| `pinnedBy?` | string | who pinned it |
| `pinnedAt?` | number | unix seconds |
<!-- cli-json:spec pin:end -->

## spec unpin

`vnproxctl spec unpin -o json` — `DELETE /spec/pin`.

<!-- cli-json:spec unpin:begin -->
| Field | Type | Description |
|---|---|---|
| `pinned` | boolean | always `false` after a successful unpin |
<!-- cli-json:spec unpin:end -->

## policy test

`vnproxctl policy test --changeset=<id> -o json` — `POST /policies/test`;
evaluates without staging anything.

<!-- cli-json:policy test:begin -->
| Field | Type | Description |
|---|---|---|
| `findings` | array of object | `{severity, code, message, ref?}` |
| `rules` | array of object | one entry per evaluated rule: `{ruleId, description, severity, tags, matchedOps, violatingOps}` |
<!-- cli-json:policy test:end -->

## policy lint

`vnproxctl policy lint --policy=<file> -o json` — local, daemon-free
document validation.

<!-- cli-json:policy lint:begin -->
| Field | Type | Description |
|---|---|---|
| `path` | string | the policy file that was linted |
| `rules` | array of object | `{id, severity, description}` for every rule the document declares |
<!-- cli-json:policy lint:end -->

## gitsync status

`vnproxctl gitsync status -o json` — `GET /gitsync/status`. See
`docs/api.md`'s "Git spec sync" section for the field-by-field meaning; the
shape is reproduced here for the anti-drift gate, not restated in prose.

<!-- cli-json:gitsync status:begin -->
| Field | Type | Description |
|---|---|---|
| `enabled` | boolean | whether `[gitsync]` is configured |
| `remote?` | string | the git remote, credential-free |
| `ref?` | string | the tracked ref |
| `path?` | string | the spec file path within the repo |
| `pollIntervalSeconds?` | number | fetch cadence |
| `requireSignedCommits` | boolean | whether unsigned commits are refused |
| `lastFetchedSha?` | string | last fetched commit sha |
| `lastFetchAt?` | number | unix seconds, last attempt |
| `lastSuccessAt?` | number | unix seconds, last successful cycle |
| `lastSigner?` | string | verified signing principal, when required |
| `lastError?` | string | last cycle's failure, if any |
| `planOpCount` | number | ops in the current plan |
| `plan?` | array of string | human-readable summary of the plan's ops |
| `notInSpec?` | array of string | entities present live but absent from the tracked spec |
| `openChangesetId?` | string | the one open sync draft, if any |
| `openChangesetReason?` | string | why it exists |
| `issues?` | array of object | `{check, severity, detail}` |
<!-- cli-json:gitsync status:end -->

## hub publish

`vnproxctl hub publish -o json` — signs an artifact and writes a submission
file. Local file work only.

<!-- cli-json:hub publish:begin -->
| Field | Type | Description |
|---|---|---|
| `submission` | string | path the submission file was written to |
| `type` | string | `blueprint` or `plugin` |
| `id` | string | the artifact's declared id |
| `version` | string | the artifact's declared version |
| `artifactUrl` | string | where the indexed artifact will be fetchable from |
| `signerFingerprint` | string | the Ed25519 key fingerprint that signed it |
<!-- cli-json:hub publish:end -->

## hub index

`vnproxctl hub index -o json` — registry-side: verifies a submission, adds
it to the index, re-signs. Idempotent.

<!-- cli-json:hub index:begin -->
| Field | Type | Description |
|---|---|---|
| `changed` | boolean | whether the index actually changed (re-running the same submission is `false`) |
| `entries` | number | total entries in the index after this run |
| `artifact` | string | path the artifact was written to inside the registry |
| `id` | string | the indexed artifact's id |
| `version` | string | the indexed artifact's version |
| `automatedChecksPassed` | boolean | T-3709's automated vetting verdict |
| `vettingNotes` | array of string | detail behind the vetting verdict |
<!-- cli-json:hub index:end -->

## hub revoke

`vnproxctl hub revoke -o json` — withdraws an artifact, an id's every
version, a signer's every artifact, or (T-3709) one Sigstore log entry.

<!-- cli-json:hub revoke:begin -->
| Field | Type | Description |
|---|---|---|
| `changed` | boolean | whether the index actually changed |
| `revocations` | number | total revocation entries in the index after this run |
| `withdrawnEntries` | number | how many catalog entries this revocation withdrew |
<!-- cli-json:hub revoke:end -->

## hub verify (index)

`vnproxctl hub verify --index <file> --signers <fp,...> -o json` — verifies
a published index exactly as a client does.

<!-- cli-json:hub verify (index):begin -->
| Field | Type | Description |
|---|---|---|
| `signerFingerprint` | string | the index's own signer fingerprint |
| `generatedAt?` | number | unix seconds the index was generated/signed at |
| `entries` | number | total entries, including revoked ones |
| `live` | number | entries not currently revoked |
| `revocations` | number | published revocation entries |
<!-- cli-json:hub verify (index):end -->

## hub verify (sigstore)

`vnproxctl hub verify --sigstore-key-bundle ... -o json` (T-3709) — verifies
a registry's Sigstore-signed key-custody attestation.

<!-- cli-json:hub verify (sigstore):begin -->
| Field | Type | Description |
|---|---|---|
| `transparencyLogEntry` | string | the Rekor transparency-log entry id (pass to `hub revoke --log-entry`) |
| `registryUrl` | string | the attested registry URL |
| `generatedAt?` | number | unix seconds the attestation was generated at |
| `indexSigners` | array of object | `{fingerprint, note}` for every Ed25519 index-signer this attestation vouches for |
<!-- cli-json:hub verify (sigstore):end -->

## hub keygen

`vnproxctl hub keygen --key <path> -o json` — creates an Ed25519 signing key
file (0600, never overwritten).

<!-- cli-json:hub keygen:begin -->
| Field | Type | Description |
|---|---|---|
| `key` | string | the key file path written |
| `fingerprint` | string | the new key's fingerprint |
<!-- cli-json:hub keygen:end -->

## hub mirror

`vnproxctl hub mirror --registry <url> --signers <fp,...> --out <dir> -o json`
(T-4009) — fetches a hosted registry's signed index and every live entry's
artifact, byte-for-byte, into a local directory. Refuses to write anything if
the index does not verify against `--signers`.

<!-- cli-json:hub mirror:begin -->
| Field | Type | Description |
|---|---|---|
| `registry` | string | the hosted registry URL that was mirrored |
| `out` | string | the local directory written |
| `registryUrl` | string | the `file://` URL form to use as `[hub] registry_url` or `hub pull --registry` |
| `entries` | number | total entries in the mirrored index, including revoked ones |
| `live` | number | entries not currently revoked (the ones whose artifacts were fetched) |
| `artifacts` | number | artifact files written |
| `revocations` | number | published revocation entries carried into the mirror |
| `signerFingerprint` | string | the index's own signer fingerprint |
| `warnings` | array of string | non-fatal notes (e.g. an entry whose artifactUrl is not the self-hosted absolute-path form, so it was mirrored but is not offline-consumable) |
<!-- cli-json:hub mirror:end -->

## hub pull

`vnproxctl hub pull --registry <url-or-dir> --signers <fp,...> --type T --id ID --version V --out <file> -o json`
(T-4009) — fetches one artifact through the same signature-verifying path the
daemon uses, from a hosted registry or a `hub mirror` directory.

<!-- cli-json:hub pull:begin -->
| Field | Type | Description |
|---|---|---|
| `registry` | string | the `--registry` value as given |
| `local` | boolean | whether the registry resolved to a local mirror directory (`file://`) rather than a hosted URL |
| `out` | string | the file the artifact's raw bytes were written to |
| `type` | string | `blueprint` or `plugin` |
| `id` | string | the pulled artifact's id |
| `version` | string | the pulled artifact's version |
| `bytes` | number | bytes written |
| `signerFingerprint?` | string | the artifact entry's own signer fingerprint, if signed |
<!-- cli-json:hub pull:end -->

## plugin scaffold

`vnproxctl plugin scaffold <name> -o json` — stamps out a compiling
`findingProducer` plugin skeleton. Local file work only.

<!-- cli-json:plugin scaffold:begin -->
| Field | Type | Description |
|---|---|---|
| `name` | string | the sanitized package/identity token used |
| `manifestId` | string | the scaffolded plugin's manifest id (`com.example.<name>`) |
| `dir` | string | directory the scaffold was written into |
| `files` | array of string | files written, relative to `dir` |
<!-- cli-json:plugin scaffold:end -->

## certs

`vnproxctl certs -o json` (or the pre-T-4011 `--json`, which still works) —
direct pmxcfs read, works daemon-down.

<!-- cli-json:certs:begin -->
| Field | Type | Description |
|---|---|---|
| `inventory` | object | `{clusterCA?, certificates}` — every certificate this node's pmxcfs can see |
| `issues` | array | problems found (array of object: `{check, severity, detail, remediation}`), empty when clean |
<!-- cli-json:certs:end -->

## watch

`vnproxctl watch -o json` (T-4010) — a live view over the WS `"events"`
topic (`internal/topology/hub.go`, frozen at D10:
`docs/adr/0010-platform-api-freeze-at-v3-0.md`). Requires a token with the
`automation` scope; fails fast (see below) otherwise. Runs until
Ctrl-C/SIGTERM or `--max-events` is reached — **newline-delimited JSON, not
a single document** (see the note above this table). Every line has a
top-level `"type"`, either `"event"` or `"status"`.

An `"event"` line passes the wire event's own fields through **verbatim**
under the added `type` tag — this table documents the tag this command
adds, not the frozen envelope itself (that contract is `docs/api.md`'s
WebSocket section and `docs/architecture.md` §13.3; a field appearing here
that isn't in that contract would be a bug in this command, not a new
platform field):

| Field | Type | Description |
|---|---|---|
| `type` | string | always `"event"` on this line shape |
| `event` | string | the wire event's own name, e.g. `changeset.status`, `drift.changed`, `findings.changed`, `audit.appended` |
| *(other fields)* | — | every other field the server's event carried, unmodified — see `docs/api.md`'s WebSocket section's event table for each event name's own payload shape |

A `"status"` line reports a connection-lifecycle transition (connected,
reconnecting, disconnected, reconnected, stopped) — this is this command's
own bookkeeping, not anything the daemon sent, which is exactly why it
needs its own tag: a script consuming the stream can tell "the cluster has
been quiet" from "the stream had a gap" without a second, human-only
channel:

| Field | Type | Description |
|---|---|---|
| `type` | string | always `"status"` on this line shape |
| `status` | string | `connected`\|`reconnecting`\|`disconnected`\|`reconnected`\|`stopped` |
| `at` | string | RFC3339 timestamp of the transition |
| `attempt?` | number | which reconnect attempt this is, for `reconnecting`/`reconnected` |
| `gapSeconds?` | number | how long the connection was down, on `reconnected` |
| `error?` | string | the dial/read/write error that caused this transition, when there was one |

**Fails fast, before ever dialing `/api/ws`,** if the token's `GET
/auth/me` capabilities don't include `automation` — the `"events"` topic's
subscribe protocol is ack-less (docs/api.md: a refused subscribe is
silently dropped, never rejected on the wire), so this command checks the
one place that scope is actually knowable ahead of time rather than opening
a connection that would just never receive anything. Exits `ExitAuth` (4)
with a message naming the missing scope.
