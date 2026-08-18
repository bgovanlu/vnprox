#!/usr/bin/env bash
# T-3203 (T-1808 verbatim) — repeatable API-side scale/perf measurement
# against a real, running vnproxd. Read-only: logs in, takes N samples of
# GET /topology, and reads the collector poll-duration histograms and
# entity counts back out. Touches no PVE config and stages no changeset.
#
# Usage:
#   VNPROX_URL=https://<node>:8007 \
#   VNPROX_USER=root VNPROX_PASSWORD=<temporary password> VNPROX_REALM=pam \
#   ./measure-api.sh
#
# VNPROX_PASSWORD is read once, used for one login call, and never written
# to disk by this script. This project's standing practice (see
# planning/reports/T-3202-scenarios.md) is a temporary `chpasswd` +
# immediate `passwd -l root` bracketing the run when root has no standing
# password — this script does not do that for you; do it in the calling
# shell, matching the pattern already established for this cluster.
#
# Requires: curl, python3 (stdlib json only).
set -euo pipefail

: "${VNPROX_URL:?set VNPROX_URL, e.g. https://192.168.1.9:8007}"
: "${VNPROX_USER:?set VNPROX_USER, e.g. root}"
: "${VNPROX_PASSWORD:?set VNPROX_PASSWORD}"
: "${VNPROX_REALM:=pam}"
SAMPLES="${SAMPLES:-20}"

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT
COOKIEJAR="$WORKDIR/cookies.txt"

echo "== logging in as $VNPROX_USER@$VNPROX_REALM against $VNPROX_URL ==" >&2
LOGIN_RESP=$(curl -sk -c "$COOKIEJAR" -X POST "$VNPROX_URL/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"$VNPROX_USER\",\"password\":\"$VNPROX_PASSWORD\",\"realm\":\"$VNPROX_REALM\"}")
if ! echo "$LOGIN_RESP" | grep -q '"user"'; then
  echo "login failed: $LOGIN_RESP" >&2
  exit 1
fi

CONFIG=$(curl -sk -b "$COOKIEJAR" "$VNPROX_URL/api/v1/config")
VERSION=$(echo "$CONFIG" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("version","unknown"))')

echo "== sampling GET /topology x$SAMPLES ==" >&2
TIMINGS_FILE="$WORKDIR/timings.txt"
: > "$TIMINGS_FILE"
for i in $(seq 1 "$SAMPLES"); do
  curl -sk -b "$COOKIEJAR" -o "$WORKDIR/topo.json" \
    -w "%{time_total} %{time_starttransfer} %{size_download}\n" \
    "$VNPROX_URL/api/v1/topology" >> "$TIMINGS_FILE"
done

echo "== reading /api/v1/metrics for collector poll-duration histograms ==" >&2
METRICS=$(curl -sk -b "$COOKIEJAR" "$VNPROX_URL/api/v1/metrics" || true)

python3 - "$WORKDIR/topo.json" "$TIMINGS_FILE" "$VERSION" <<'PYEOF'
import json
import sys
from collections import Counter

topo_path, timings_path, version = sys.argv[1], sys.argv[2], sys.argv[3]

with open(topo_path) as f:
    topo = json.load(f)
nodes = topo.get("nodes", [])
edges = topo.get("edges", [])
by_kind = Counter(n.get("kind") for n in nodes)
by_layer = Counter(n.get("layer") for n in nodes)

times = []
sizes = []
with open(timings_path) as f:
    for line in f:
        total, starttransfer, size = line.split()
        times.append(float(total) * 1000.0)
        sizes.append(int(size))
times.sort()


def pctl(sorted_vals, p):
    if not sorted_vals:
        return None
    idx = min(len(sorted_vals) - 1, int(len(sorted_vals) * p))
    return sorted_vals[idx]


result = {
    "vnproxVersion": version,
    "samples": len(times),
    "topologyRequestMs": {
        "p50": round(pctl(times, 0.50), 2),
        "p95": round(pctl(times, 0.95), 2),
        "p99": round(pctl(times, 0.99), 2),
        "max": round(max(times), 2),
    },
    "payloadBytes": sizes[0] if sizes else None,
    "entityCounts": {
        "totalNodes": len(nodes),
        "totalEdges": len(edges),
        "byKind": dict(by_kind),
        "byLayer": dict(by_layer),
    },
}
print(json.dumps(result, indent=2))
PYEOF

echo "$METRICS" > "/tmp/vnprox-t3203-metrics-raw.txt" 2>/dev/null || true
echo "== raw /metrics snapshot saved to /tmp/vnprox-t3203-metrics-raw.txt for manual grep of vnprox_collector_poll_duration_seconds_* ==" >&2
