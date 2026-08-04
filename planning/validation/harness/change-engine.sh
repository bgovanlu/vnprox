#!/usr/bin/env bash
# planning/validation/harness/change-engine.sh — the change engine's local
# paths section (T-1801). MUTATES=1.
#
# Covers the PUT-encoding/staging/reload half of
# `planning/reports/needs-hardware-validation.md`'s "## PVE API behavior"
# heading ("PUT request encoding") plus the T-304 "real elapsed-time
# behavior of the per-node local timer across an actual ifreload" item:
# both need a genuine stage -> reload round trip against the real PVE
# network-config API to observe, not just a GET.
#
# Safety: this script re-applies an interface's CURRENT mtu value back to
# itself (read it, PUT the identical value, reload). It changes nothing
# about the interface's configuration in steady state — the point is to
# observe PVE's staging ("interfaces.new") and reload (ifreload) mechanics
# firsthand, not to change the target's config. It still triggers a real
# ifreload on the target node, which briefly reprograms the interface even
# when the value is unchanged, so:
#   - PVE_TARGET_IFACE has NO default. You must name a specific, non-
#     management interface explicitly. Do not point this at the interface
#     (or bridge/bond carrying it) that your management session depends on
#     (docs/architecture.md's management-path guidance) — an ifreload of
#     the live mgmt path can transiently drop your connection.
#   - Run this only against a node you can also reach out-of-band (IPMI/
#     console) if something goes wrong, per planning/validation/README.md's
#     recovery guidance.
#
# Usage:
#   PVE_TARGET_IFACE=eno2 ssh pvecube 'bash -s' \
#     < planning/validation/harness/change-engine.sh --i-understand-this-mutates \
#     > change-engine-evidence.json
# Against pvemock (single-node.yaml's eno2 is an unused spare NIC, safe by
# fixture design — see internal/pvemock/README.md):
#   PVE_API_BASE_URL=http://localhost:8006/api2/json PVE_TARGET_IFACE=eno2 \
#     bash planning/validation/harness/change-engine.sh --i-understand-this-mutates

SECTION="change-engine"
MUTATES=1

# shellcheck source=lib/common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/common.sh"

harness_require_mutation_flag "${1:-}"

: "${PVE_TARGET_IFACE:?set PVE_TARGET_IFACE to a specific, non-management interface before running this script (see the header comment)}"

NODE="$(harness_local_node)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

# --- change-engine-01: baseline capture ------------------------------------
harness_item "change-engine-01" \
	"PVE API behavior > PUT request encoding (baseline capture)" \
	'api_get "/nodes/$NODE/network/$PVE_TARGET_IFACE" | tee "$WORKDIR/baseline.json"
mtu="$(grep -o "\"mtu\"[[:space:]]*:[[:space:]]*[0-9]*" "$WORKDIR/baseline.json" | head -1 | grep -o "[0-9]*$")"
if [ -z "$mtu" ]; then mtu=1500; fi
printf "%s" "$mtu" > "$WORKDIR/mtu.txt"
echo "baseline_mtu=$mtu"'

# --- change-engine-02: staged write, identical value -----------------------
harness_item "change-engine-02" \
	"PVE API behavior > PUT request encoding (staged write)" \
	'mtu="$(cat "$WORKDIR/mtu.txt")"
if command -v pvesh >/dev/null 2>&1; then
	pvesh set "/nodes/$NODE/network/$PVE_TARGET_IFACE" -mtu "$mtu" --output-format json 2>&1
else
	_api_http_login
	curl -sS ${PVE_API_INSECURE:+-k} -b "PVEAuthCookie=$_PVE_TICKET" -H "CSRFPreventionToken: $_PVE_CSRF" \
		-H "Content-Type: application/json" -X PUT \
		-d "{\"mtu\":$mtu}" \
		-o /dev/null -w "http_status=%{http_code}\n" \
		"$PVE_API_BASE_URL/nodes/$NODE/network/$PVE_TARGET_IFACE"
fi'

# --- change-engine-03: staging is visible as pending -----------------------
harness_item "change-engine-03" \
	"Distributed rollback / local-timer protocol (T-304) > interfaces.new staging semantics" \
	'api_get "/nodes/$NODE/network/$PVE_TARGET_IFACE"'

# --- change-engine-04: reload (ifreload) and measure elapsed time ---------
harness_item "change-engine-04" \
	"Distributed rollback / local-timer protocol (T-304) > real elapsed-time behavior of an actual ifreload" \
	'start_ts="$(date -u +%s)"
if command -v pvesh >/dev/null 2>&1; then
	upid="$(pvesh set "/nodes/$NODE/network" --output-format json 2>&1)"
	echo "reload_task=$upid"
else
	_api_http_login
	upid="$(curl -sS ${PVE_API_INSECURE:+-k} -b "PVEAuthCookie=$_PVE_TICKET" -H "CSRFPreventionToken: $_PVE_CSRF" \
		-X PUT "$PVE_API_BASE_URL/nodes/$NODE/network")"
	echo "reload_response=$upid"
	task="$(printf "%s" "$upid" | grep -o "\"data\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | head -1 | sed -E "s/.*:[[:space:]]*\"([^\"]*)\"/\1/")"
	tries=0
	while [ "$tries" -lt 30 ]; do
		status="$(curl -sS ${PVE_API_INSECURE:+-k} -b "PVEAuthCookie=$_PVE_TICKET" "$PVE_API_BASE_URL/nodes/$NODE/tasks/$task/status" 2>&1)"
		printf "%s" "$status" | grep -q "\"status\":\"stopped\"" && break
		tries=$((tries + 1))
		sleep 1
	done
	echo "final_task_status=$status"
fi
end_ts="$(date -u +%s)"
echo "elapsed_seconds=$((end_ts - start_ts))"'

# --- change-engine-05: post-reload confirm clean ---------------------------
harness_item "change-engine-05" \
	"PVE API behavior > PUT request encoding (post-reload, no pending)" \
	'api_get "/nodes/$NODE/network/$PVE_TARGET_IFACE"'

harness_emit
