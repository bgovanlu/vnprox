#!/usr/bin/env bash
#
# scripts/hub-registry-harness.sh — T-3709: prove, against a REAL local
# daemon, that the hub registry's verifier actually fails on bad input and
# that revocation is actually honoured. Not a unit test and not a mock: this
# script builds the real vnproxd/vnproxctl/pvemock binaries, publishes the
# repo's real seed blueprints through the real `vnproxctl hub publish`/
# `hub index` pipeline, serves the resulting tree over plain local HTTP, and
# points a real running vnproxd at it via [hub] registry_url — exactly the
# self-hosted-registry path docs/hub-registry.md documents for an operator.
#
# Why this exists: CLAUDE.md's mock rule ("a registry mock and the client
# that talks to it, both derived from one reading of the design, will agree
# with each other forever") applies here in a second way. internal/api's own
# hub_registry_test.go already covers tamper/revoke assertions in-process,
# at the Go test boundary, with a real internal/hubreg+internal/hub stack.
# This script covers the boundary that matters operationally: a real daemon
# PROCESS, a real registry directory on disk, real HTTP between them, and
# the real `vnproxctl hub` CLI a publisher and a registry maintainer
# actually run — not go test's in-process httptest server.
#
# What it demonstrates, in order (T-3709 deliverables d-h):
#   d. publish the seed blueprints for real, install one successfully
#   e. tamper a published ARTIFACT (leave the index alone) -> install
#      refused, with the artifact-signature error class
#   f. tamper the INDEX itself (edit an entry after signing) -> refused too,
#      with a DIFFERENT error class (whole-catalog rejection, not one entry)
#   g. revoke one entry by name, re-sign -> gone from GET /hub/index, install
#      refused, even though the artifact's OWN bundle signature still
#      verifies (checked directly, off the registry gate, to show the two
#      are genuinely different checks)
#   h. revoke by signer fingerprint -> every entry that signer produced is
#      gone, not just the one revoked by name in (g)
#
# Nothing here is destructive to any real system: every process, port, key
# and file this script touches lives under a throwaway temp directory and is
# torn down on exit. No key is ever written under the repository.
#
# Usage:
#   scripts/hub-registry-harness.sh run
#
# Env overrides:
#   VNPROX_HARNESS_KEEP=1   don't delete the temp work dir on exit (prints
#                           its path so a failure can be inspected by hand)
#
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$PWD"
GO="${GO:-go}"

usage() {
  cat <<'EOF'
scripts/hub-registry-harness.sh - end-to-end proof of T-2803's registry
signature/revocation gate against a real daemon and a real self-hosted
registry.

  scripts/hub-registry-harness.sh run
        Build the real binaries, publish the repo's seed blueprints through
        the real hub publish/index pipeline, serve them over local HTTP,
        point a real vnproxd at the result, and walk through: a good
        install, a tampered artifact, a tampered index, a by-entry
        revocation and a by-signer revocation. Prints a transcript to
        stdout suitable for `> planning/reports/evidence/<name>.txt`.
EOF
}

if [ "${1:-}" != "run" ]; then
  usage
  exit 0
fi

# --- workspace -------------------------------------------------------------
WORK="$(mktemp -d "${TMPDIR:-/tmp}/vnprox-hub-harness.XXXXXX")"
BIN="$WORK/bin"
mkdir -p "$BIN" "$WORK/keys" "$WORK/seeds" "$WORK/submissions" "$WORK/registry"
# Small Go helper programs need to live INSIDE the module tree (not /tmp) so
# they may import internal/... packages at all -- Go's internal-package
# visibility rule is based on import path, not cwd. var/ is gitignored
# (.gitignore: "/var/"), so this can never end up committed even if cleanup
# is interrupted, and cleanup() always removes it on exit regardless.
HELPERS="$ROOT/var/hub-harness-helpers.$$"
mkdir -p "$HELPERS"

PIDS=()
cleanup() {
  local pid
  for pid in "${PIDS[@]:-}"; do
    [ -n "$pid" ] && kill "$pid" >/dev/null 2>&1 || true
  done
  for pid in "${PIDS[@]:-}"; do
    [ -n "$pid" ] && wait "$pid" 2>/dev/null || true
  done
  if [ "${VNPROX_HARNESS_KEEP:-0}" = "1" ]; then
    echo ">> VNPROX_HARNESS_KEEP=1: leaving $WORK in place ($HELPERS is still removed -- it is repo-tree scratch, not evidence)"
  else
    rm -rf "$WORK"
  fi
  rm -rf "$HELPERS"
}
trap cleanup EXIT

section() { printf '\n=== %s ===\n' "$1"; }

free_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

wait_tcp() {
  local host="$1" port="$2" deadline=$((SECONDS + 20))
  while (( SECONDS < deadline )); do
    if (exec 3<>"/dev/tcp/$host/$port") 2>/dev/null; then
      exec 3>&- 3<&- 2>/dev/null || true
      return 0
    fi
    sleep 0.2
  done
  echo "timed out waiting for $host:$port" >&2
  return 1
}

wait_health() {
  local url="$1" deadline=$((SECONDS + 20)) code
  while (( SECONDS < deadline )); do
    code="$(curl -sk -o /dev/null -w '%{http_code}' "$url" 2>/dev/null || true)"
    [ "$code" = "200" ] && return 0
    sleep 0.2
  done
  echo "timed out waiting for $url to answer 200" >&2
  return 1
}

json_field() {
  # json_field <file-or-'-'> <top-level-key> — robust scalar extraction
  # without a jq dependency (python3 is already required below for the
  # static file server, so it costs nothing extra).
  python3 -c '
import json, sys
src = sys.argv[1]
data = json.load(open(src)) if src != "-" else json.load(sys.stdin)
v = data.get(sys.argv[2], "")
print(v if not isinstance(v, (dict, list)) else json.dumps(v))
' "$1" "$2"
}

start_bg() {
  # start_bg <log-file> <cmd...> — runs in a new session so it survives
  # this function returning, logs to <log-file>, and its pid is recorded
  # in PIDS for cleanup.
  local log="$1"; shift
  setsid "$@" >"$log" 2>&1 < /dev/null &
  PIDS+=("$!")
}

echo "vnprox hub registry harness — T-3709"
echo "run at: $(date -u +%FT%TZ)"
echo "repo:   $(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown) ($(git -C "$ROOT" branch --show-current 2>/dev/null || echo unknown))"
echo "work:   $WORK"

# =============================================================================
# 1. Build the real binaries
# =============================================================================
section "1. Build the real vnproxd / vnproxctl / pvemock binaries"
( cd "$ROOT" && "$GO" build -o "$BIN/vnproxd" ./cmd/vnproxd )
( cd "$ROOT" && "$GO" build -o "$BIN/vnproxctl" ./cmd/vnproxctl )
( cd "$ROOT" && "$GO" build -o "$BIN/pvemock" ./cmd/pvemock )
echo "built: $BIN/vnproxd $BIN/vnproxctl $BIN/pvemock"

# =============================================================================
# 2. Throwaway keys — generated fresh, live only under $WORK, never the repo
# =============================================================================
section "2. Generate a throwaway index key and publisher key (temp dir only, never committed)"
PUB_KEYGEN_OUT="$("$BIN/vnproxctl" hub keygen --key "$WORK/keys/publisher.key")"
IDX_KEYGEN_OUT="$("$BIN/vnproxctl" hub keygen --key "$WORK/keys/index.key")"
echo "$PUB_KEYGEN_OUT"
echo "$IDX_KEYGEN_OUT"
PUB_FP="$(echo "$PUB_KEYGEN_OUT" | awk '/fingerprint:/ {print $2}')"
IDX_FP="$(echo "$IDX_KEYGEN_OUT" | awk '/fingerprint:/ {print $2}')"
[ -n "$PUB_FP" ] && [ -n "$IDX_FP" ] || { echo "failed to parse a keygen fingerprint" >&2; exit 1; }
echo "publisher key fingerprint: $PUB_FP"
echo "index key fingerprint:     $IDX_FP"

# =============================================================================
# 3. Publish the repo's real seed blueprints through the REAL CLI pipeline
# =============================================================================
section "3. Dump the repo's seed blueprints (internal/hub/seed) to bundle files"
cat > "$HELPERS/dumpseeds.go" <<'EOF'
// Throwaway harness helper: writes each real internal/hub/seed blueprint to
// an unsigned bundle file, exactly what cmd/vnproxctl's own
// TestHubCLI_SeedBlueprintsPublishReviewIndex does with writeSeedBundle, so
// `hub publish` signs the SAME real, multi-entity content that test does —
// never a hand-written fixture.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/hub/seed"
)

func main() {
	dir := os.Args[1]
	for _, bp := range seed.Seeds() {
		bundle := blueprint.Bundle{BundleVersion: blueprint.CurrentBundleVersion, Blueprint: *bp}
		raw, err := json.Marshal(bundle)
		if err != nil {
			panic(err)
		}
		path := filepath.Join(dir, bp.ID+".bundle.json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			panic(err)
		}
		fmt.Println(bp.ID)
	}
}
EOF
( cd "$ROOT" && "$GO" run "$HELPERS/dumpseeds.go" "$WORK/seeds" )
mapfile -t SEED_IDS < <(ls "$WORK/seeds" | sed 's/\.bundle\.json$//' | sort)
echo "seed blueprints: ${SEED_IDS[*]}"
[ "${#SEED_IDS[@]}" -ge 4 ] || { echo "expected >=4 seed blueprints, got ${#SEED_IDS[@]}" >&2; exit 1; }

section "3b. Publish + index every seed through the real 'vnproxctl hub publish'/'hub index'"
for id in "${SEED_IDS[@]}"; do
  "$BIN/vnproxctl" hub publish \
    --artifact "$WORK/seeds/$id.bundle.json" --type blueprint --version 1.0.0 \
    --key "$WORK/keys/publisher.key" --publisher vnprox-harness \
    --description "T-3709 harness seed: $id" \
    --out "$WORK/submissions/$id.json" >/dev/null
  "$BIN/vnproxctl" hub index \
    --root "$WORK/registry" --submission "$WORK/submissions/$id.json" \
    --key "$WORK/keys/index.key"
done
"$BIN/vnproxctl" hub verify --index "$WORK/registry/index.json" --signers "$IDX_FP"

# Keep a pristine copy of the correctly-signed index for the "tamper the
# index" step's recovery (§6b below) — the point of that step is to show a
# corrupted index is refused and RECOVERABLE, not to leave the registry
# broken for the rest of the run.
cp "$WORK/registry/index.json" "$WORK/index.pristine.json"

# Pick which seeds play which role, by name (not order-dependent).
INSTALL_ID="${SEED_IDS[0]}"     # (d) installed successfully
TAMPER_ARTIFACT_ID="${SEED_IDS[1]}" # (e) artifact tampered after publish
TAMPER_INDEX_ID="${SEED_IDS[2]}"    # (f) index entry tampered after signing
REVOKE_BY_ID="${SEED_IDS[2]}"       # (g) revoked by (type,id) — same as (f)'s entry, once the index is restored
REVOKE_BY_SIGNER_REMAINING="${SEED_IDS[3]}" # (h) revoked only via --signer, never named directly
echo "roles: install=$INSTALL_ID tamper-artifact=$TAMPER_ARTIFACT_ID tamper-index/revoke-by-id=$TAMPER_INDEX_ID revoke-by-signer-victim=$REVOKE_BY_SIGNER_REMAINING"

# =============================================================================
# 4. Serve the published registry tree over plain local HTTP
# =============================================================================
section "4. Serve the published registry tree over plain local HTTP"
start_bg "$WORK/registry-http.log" python3 -u -m http.server 0 --bind 127.0.0.1 --directory "$WORK/registry"
sleep 0.5
REGISTRY_PORT="$(sed -n 's/.*port \([0-9][0-9]*\).*/\1/p' "$WORK/registry-http.log" | head -1)"
[ -n "$REGISTRY_PORT" ] || { echo "could not determine the registry HTTP server's port"; cat "$WORK/registry-http.log"; exit 1; }
REGISTRY_URL="http://127.0.0.1:$REGISTRY_PORT"
wait_tcp 127.0.0.1 "$REGISTRY_PORT"
echo "registry served at $REGISTRY_URL"
curl -s "$REGISTRY_URL/index.json" | json_field - entries >/dev/null 2>&1 || true
echo "GET $REGISTRY_URL/index.json -> $(curl -s -o /dev/null -w '%{http_code}' "$REGISTRY_URL/index.json")"

# =============================================================================
# 5. Start pvemock, then a REAL vnproxd pointed at the registry above
# =============================================================================
section "5. Start pvemock and a real vnproxd, [hub] registry_url pointed at the server above"
PVEMOCK_PORT="$(free_port)"
start_bg "$WORK/pvemock.log" "$BIN/pvemock" --addr "127.0.0.1:$PVEMOCK_PORT" --fixture "$ROOT/testdata/clusters/single-node.yaml"
wait_tcp 127.0.0.1 "$PVEMOCK_PORT"
echo "pvemock listening on 127.0.0.1:$PVEMOCK_PORT (testdata/clusters/single-node.yaml)"

VNPROXD_PORT="$(free_port)"
CFG="$WORK/vnprox.toml"
cp "$ROOT/testdata/dev.toml" "$CFG"
declare -A REPL=(
  [listen]="127.0.0.1:$VNPROXD_PORT"
  [tls_cert]="$ROOT/testdata/certs/dev-cert.pem"
  [tls_key]="$ROOT/testdata/certs/dev-key.pem"
  [api_url]="http://127.0.0.1:$PVEMOCK_PORT"
  [protected_path]="$WORK/dev-protected.json"
  [dev_interfaces_dir]="$WORK/dev-host"
  [db_path]="$WORK/vnprox.db"
  [session_key_file]="$WORK/session.key"
  [secret_path]="$WORK/cluster.secret"
  [key_file]="$WORK/metrics.key"
  [signing_key_file]="$WORK/blueprint-signing.key"
  [trusted_signers_dir]="$WORK/trusted-signers"
)
for key in "${!REPL[@]}"; do
  value="${REPL[$key]}"
  sed -i -E "s#^([[:space:]]*)${key}[[:space:]]*=.*#\1${key} = \"${value}\"#" "$CFG"
done
cat >> "$CFG" <<EOF

# T-3709 harness: the real [hub] section, pointed at the self-hosted
# registry this script just published — the exact operator-facing shape
# docs/hub-registry.md's "Operator setup" documents.
[hub]
registry_url = "$REGISTRY_URL"
index_signers = ["$IDX_FP"]
EOF

BASE="https://127.0.0.1:$VNPROXD_PORT/api/v1"
COOKIES="$WORK/cookies.txt"

start_daemon() {
  start_bg "$WORK/vnproxd.log" "$BIN/vnproxd" --config "$CFG"
  ( cd "$ROOT" && wait_health "$BASE/health" )
}
stop_daemon() {
  local last="${PIDS[-1]}"
  kill "$last" >/dev/null 2>&1 || true
  wait "$last" 2>/dev/null || true
  PIDS=("${PIDS[@]:0:${#PIDS[@]}-1}")
}

start_daemon
echo "vnproxd healthy at $BASE (config: $CFG)"

api_login() {
  local code
  code="$(curl -sk -c "$COOKIES" -o "$WORK/login.json" -w '%{http_code}' \
    -H 'Content-Type: application/json' \
    -d '{"username":"root@pam","password":"vnprox-mock"}' \
    "$BASE/auth/login")"
  [ "$code" = "200" ] || { echo "login failed: HTTP $code"; cat "$WORK/login.json"; exit 1; }
  CSRF="$(awk -F'\t' '$6=="vnprox_csrf"{print $7}' "$COOKIES")"
  [ -n "$CSRF" ] || { echo "no CSRF cookie after login"; exit 1; }
}
api_get() {
  curl -sk -b "$COOKIES" -w '\n%{http_code}' "$BASE$1"
}
api_post() {
  curl -sk -b "$COOKIES" -c "$COOKIES" -H "X-VNPROX-CSRF: $CSRF" \
    -H 'Content-Type: application/json' -d "$2" -w '\n%{http_code}' "$BASE$1"
}
split_body_code() { # sets BODY and CODE from a curl "\n%{http_code}" response
  CODE="$(echo "$1" | tail -1)"
  BODY="$(echo "$1" | sed '$d')"
}

api_login
echo "logged in as root@pam, CSRF token acquired"

# =============================================================================
# (d) Real install, through the real client, real gate, real trust store
# =============================================================================
section "(d) GET /hub/index and install $INSTALL_ID for real"
resp="$(api_get "/hub/index?type=blueprint")"; split_body_code "$resp"
echo "GET /hub/index -> $CODE"
echo "$BODY" | python3 -c '
import json, sys
items = json.load(sys.stdin).get("items", [])
print(f"  {len(items)} entr(ies) offered:")
for e in items:
    t, i, v = e["type"], e["id"], e["version"]
    fp = e.get("signerFingerprint", "")[:16]
    print(f"    {t:<10} {i:<32} {v:<8} signer={fp}...")
'
[ "$CODE" = "200" ] || { echo "expected 200"; exit 1; }

resp="$(api_post "/hub/install" "{\"type\":\"blueprint\",\"id\":\"$INSTALL_ID\",\"version\":\"1.0.0\",\"trustNewKey\":true}")"
split_body_code "$resp"
echo "POST /hub/install {id: $INSTALL_ID, trustNewKey: true} -> $CODE"
echo "  status: $(echo "$BODY" | json_field - status)"
[ "$CODE" = "201" ] || { echo "expected 201 (imported)"; echo "$BODY"; exit 1; }
echo "  RESULT: real install succeeded — the publisher key is now trusted by this daemon."

# =============================================================================
# (e) Tamper a published ARTIFACT, index left untouched
# =============================================================================
section "(e) Tamper the published ARTIFACT for $TAMPER_ARTIFACT_ID (index.json untouched)"
ARTIFACT_PATH="$WORK/registry/artifacts/blueprint/$TAMPER_ARTIFACT_ID/1.0.0.json"
[ -f "$ARTIFACT_PATH" ] || { echo "missing artifact file $ARTIFACT_PATH"; exit 1; }
cat > "$HELPERS/tamperbyte.go" <<'EOF'
// Flips one byte inside the published bundle's blueprint.name field —
// literally a single-byte XOR — while leaving the JSON well-formed, so the
// daemon's HTTP fetch and JSON decode both succeed and the failure that
// surfaces is purely a signature mismatch, not a parse error.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	path := os.Args[1]
	raw, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		panic(err)
	}
	bp, ok := doc["blueprint"].(map[string]any)
	if !ok {
		panic("artifact has no top-level blueprint object")
	}
	name, _ := bp["name"].(string)
	if name == "" {
		panic("blueprint.name is empty, nothing to flip")
	}
	b := []byte(name)
	before := b[0]
	b[0] ^= 0x20
	bp["name"] = string(b)
	fmt.Printf("flipped blueprint.name[0]: %q -> %q\n", string(before), string(b[0]))
	out, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil { //nolint:gosec // harness scratch file
		panic(err)
	}
}
EOF
( cd "$ROOT" && "$GO" run "$HELPERS/tamperbyte.go" "$ARTIFACT_PATH" )

resp="$(api_post "/hub/install" "{\"type\":\"blueprint\",\"id\":\"$TAMPER_ARTIFACT_ID\",\"version\":\"1.0.0\"}")"
split_body_code "$resp"
echo "POST /hub/install {id: $TAMPER_ARTIFACT_ID} (tampered artifact bytes) -> $CODE"
echo "  status: $(echo "$BODY" | json_field - status)"
STATUS_E="$(echo "$BODY" | json_field - status)"
[ "$STATUS_E" = "invalidSignature" ] || { echo "expected status=invalidSignature, got $STATUS_E"; echo "$BODY"; exit 1; }
echo "  RESULT: REFUSED — status=invalidSignature (the artifact's own Ed25519 signature no longer verifies over its tampered bytes)."

# =============================================================================
# (f) Tamper the INDEX itself, after it was signed
# =============================================================================
section "(f) Tamper the INDEX for $TAMPER_INDEX_ID (edit an entry after signing)"
cat > "$HELPERS/tamperindex.go" <<'EOF'
// Edits one catalog entry's description field in an already-signed
// index.json, leaving the top-level "signature" block untouched — the
// index's canonical bytes (which the signature covers) now disagree with
// what is recorded, so ANY client that re-verifies the whole document must
// reject it whole, not just the edited entry.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	path, id := os.Args[1], os.Args[2]
	raw, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		panic(err)
	}
	entries, _ := doc["entries"].([]any)
	found := false
	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok || entry["id"] != id {
			continue
		}
		entry["description"] = "EDITED AFTER SIGNING by the T-3709 harness"
		found = true
	}
	if !found {
		panic("entry id not found in index.json: " + id)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil { //nolint:gosec // harness scratch file
		panic(err)
	}
	fmt.Println("edited entry", id, "description in-place; signature block left untouched")
}
EOF
( cd "$ROOT" && "$GO" run "$HELPERS/tamperindex.go" "$WORK/registry/index.json" "$TAMPER_INDEX_ID" )

# The client caches a verified index for 60s (hub.DefaultCacheTTL); restart
# the daemon so the very next fetch is guaranteed to be against the
# tampered bytes, not a cached-good copy.
stop_daemon
start_daemon
echo "vnproxd restarted (forces a fresh, uncached index fetch)"

resp="$(api_get "/hub/index")"; split_body_code "$resp"
echo "GET /hub/index (index tampered) -> $CODE"
echo "  body: $BODY"
[ "$CODE" = "502" ] || { echo "expected 502 registry_unavailable"; exit 1; }
HAS_ITEMS="$(echo "$BODY" | python3 -c 'import json,sys; d=json.load(sys.stdin); print("yes" if "items" in d else "no")')"
[ "$HAS_ITEMS" = "no" ] || { echo "a failed index verification returned a catalog anyway"; exit 1; }
echo "  RESULT: REFUSED whole — HTTP 502, zero entries returned (never a partial catalog)."
echo "  This is a DIFFERENT failure than (e): (e) was a per-artifact invalidSignature status inside"
echo "  a 200 install response; this is the whole index failing to verify at all, before any entry is shown."

resp="$(api_post "/hub/install" "{\"type\":\"blueprint\",\"id\":\"$TAMPER_INDEX_ID\",\"version\":\"1.0.0\"}")"
split_body_code "$resp"
echo "POST /hub/install {id: $TAMPER_INDEX_ID} (index still tampered) -> $CODE (want 404: no verified catalog to find it in)"
[ "$CODE" = "404" ] || { echo "expected 404"; exit 1; }

# Recover: restore the pristine, correctly-signed index and restart once
# more. The point of (f) is that this is refused AND recoverable, not that
# the registry is now permanently broken.
cp "$WORK/index.pristine.json" "$WORK/registry/index.json"
stop_daemon
start_daemon
resp="$(api_get "/hub/index")"; split_body_code "$resp"
COUNT_AFTER_RECOVERY="$(echo "$BODY" | python3 -c 'import json,sys;print(len(json.load(sys.stdin).get("items",[])))')"
echo "index restored + vnproxd restarted -> GET /hub/index -> $CODE, $COUNT_AFTER_RECOVERY entr(ies) (back to normal)"
[ "$CODE" = "200" ] && [ "$COUNT_AFTER_RECOVERY" = "${#SEED_IDS[@]}" ] || { echo "recovery did not restore the full catalog"; exit 1; }

# =============================================================================
# (g) Revoke ONE entry by (type, id), re-sign, verify it's gone AND that the
#     artifact's own bundle signature is untouched — a different check.
# =============================================================================
section "(g) Revoke $REVOKE_BY_ID by (type, id); confirm its OWN artifact signature still verifies"
"$BIN/vnproxctl" hub revoke --root "$WORK/registry" --key "$WORK/keys/index.key" \
  --type blueprint --id "$REVOKE_BY_ID" --version 1.0.0 \
  --reason "T-3709 harness: demonstrate by-entry revocation"

stop_daemon
start_daemon

resp="$(api_get "/hub/index")"; split_body_code "$resp"
PRESENT="$(echo "$BODY" | python3 -c "import json,sys; print(any(e[\"id\"]==\"$REVOKE_BY_ID\" for e in json.load(sys.stdin).get(\"items\",[])))")"
echo "GET /hub/index after revocation -> $CODE, $REVOKE_BY_ID offered = $PRESENT"
[ "$PRESENT" = "False" ] || { echo "revoked entry is still offered"; exit 1; }

resp="$(api_post "/hub/install" "{\"type\":\"blueprint\",\"id\":\"$REVOKE_BY_ID\",\"version\":\"1.0.0\"}")"
split_body_code "$resp"
echo "POST /hub/install {id: $REVOKE_BY_ID} (revoked) -> $CODE (want 404)"
[ "$CODE" = "404" ] || { echo "expected 404 for a revoked entry"; exit 1; }

cat > "$HELPERS/verifyartifact.go" <<'EOF'
// Verifies a published blueprint bundle's OWN Ed25519 signature directly —
// bypassing the registry client and the revocation gate entirely — to show
// that revocation is a CATALOG decision, not a claim that the artifact's
// bytes were forged. A revoked artifact's signature is expected to still
// verify; that is the whole point of the distinction T-3709 (g) asks for.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/bgovanlu/vnprox/internal/blueprint"
)

func main() {
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	var bundle blueprint.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		panic(err)
	}
	verified, fingerprint, err := blueprint.VerifyBundle(bundle)
	if err != nil {
		fmt.Printf("verified=false err=%v\n", err)
		os.Exit(0)
	}
	fmt.Printf("verified=%v signerFingerprint=%s\n", verified, fingerprint)
}
EOF
REVOKED_ARTIFACT_PATH="$WORK/registry/artifacts/blueprint/$REVOKE_BY_ID/1.0.0.json"
VERIFY_OUT="$(cd "$ROOT" && "$GO" run "$HELPERS/verifyartifact.go" "$REVOKED_ARTIFACT_PATH")"
echo "direct bundle-signature check on the revoked artifact's OWN bytes (no registry, no gate): $VERIFY_OUT"
echo "$VERIFY_OUT" | grep -q '^verified=true' || { echo "expected the revoked artifact's own signature to still verify"; exit 1; }
echo "  RESULT: the revoked artifact's signature verifies (verified=true) but the catalog refuses it (404) —"
echo "  proof that revocation and signature validity are genuinely separate checks, as docs/hub-registry.md claims."

# =============================================================================
# (h) Revoke by SIGNER FINGERPRINT — every remaining live entry from that
#     signer disappears, not just one named by id.
# =============================================================================
section "(h) Revoke by --signer $PUB_FP (key-compromise path) — every remaining entry from that signer is withdrawn"
resp="$(api_get "/hub/index")"; split_body_code "$resp"
BEFORE_COUNT="$(echo "$BODY" | python3 -c 'import json,sys;print(len(json.load(sys.stdin).get("items",[])))')"
echo "entries offered before the by-signer revocation: $BEFORE_COUNT"

"$BIN/vnproxctl" hub revoke --root "$WORK/registry" --key "$WORK/keys/index.key" \
  --signer "$PUB_FP" \
  --reason "T-3709 harness: demonstrate by-signer revocation (publisher key compromise)"

stop_daemon
start_daemon

resp="$(api_get "/hub/index")"; split_body_code "$resp"
AFTER_COUNT="$(echo "$BODY" | python3 -c 'import json,sys;print(len(json.load(sys.stdin).get("items",[])))')"
echo "GET /hub/index after by-signer revocation -> $CODE, $AFTER_COUNT entr(ies) offered (want 0: every seed shares the one publisher key)"
[ "$AFTER_COUNT" = "0" ] || { echo "expected every entry to be withdrawn after a by-signer revocation"; exit 1; }

resp="$(api_post "/hub/install" "{\"type\":\"blueprint\",\"id\":\"$REVOKE_BY_SIGNER_REMAINING\",\"version\":\"1.0.0\"}")"
split_body_code "$resp"
echo "POST /hub/install {id: $REVOKE_BY_SIGNER_REMAINING} — never individually revoked, only via --signer -> $CODE (want 404)"
[ "$CODE" = "404" ] || { echo "expected 404: --signer revocation must cover entries never named by id"; exit 1; }
echo "  RESULT: $REVOKE_BY_SIGNER_REMAINING was never revoked by name — it is withdrawn ONLY because its signer was —"
echo "  the case docs/hub-registry.md calls out as 'the one that matters in a real compromise'."

section "Summary"
echo "d. real install:                 PASS (201 imported, $INSTALL_ID)"
echo "e. tampered artifact refused:     PASS (200 invalidSignature, $TAMPER_ARTIFACT_ID)"
echo "f. tampered index refused:        PASS (502 registry_unavailable, whole catalog, zero entries) + recovered"
echo "g. revoke by entry:               PASS (offered=False, install=404) + own signature still verifies"
echo "h. revoke by signer fingerprint:  PASS (0 entries offered, an un-named entry also refused)"
echo
echo "vnproxd log:   $WORK/vnproxd.log"
echo "pvemock log:   $WORK/pvemock.log"
echo "registry log:  $WORK/registry-http.log"
echo "registry dir:  $WORK/registry"
