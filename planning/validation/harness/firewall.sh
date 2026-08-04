#!/usr/bin/env bash
# planning/validation/harness/firewall.sh — firewall section (T-1801).
#
# `planning/reports/needs-hardware-validation.md` has no dedicated
# "Firewall" heading yet (its firewall-related items are folded into the
# T-1805 revert-ticket section, owned by that card, and a k8s finding-
# precision item). This section exists so T-1802 has a read-only baseline
# confirmation of the firewall API surface pvemock models
# (internal/pvemock/README.md's "Firewall" bullet: cluster/node/guest-scope
# rules, options, aliases, ipsets, security groups) before T-1804 layers
# the mutating firewall-lockout scenarios on top. Entirely read-only.
#
# Usage:
#   ssh pvecube 'bash -s' < planning/validation/harness/firewall.sh > firewall-evidence.json
# Against pvemock:
#   PVE_API_BASE_URL=http://localhost:8006/api2/json \
#     bash planning/validation/harness/firewall.sh

SECTION="firewall"
MUTATES=0

# shellcheck source=lib/common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/common.sh"

NODE="$(harness_local_node)"

# --- firewall-01: pve-firewall service state -------------------------------
harness_item "firewall-01" \
	"Firewall > pve-firewall service state" \
	'if command -v pve-firewall >/dev/null 2>&1; then
	pve-firewall status 2>&1
elif command -v systemctl >/dev/null 2>&1; then
	systemctl show pve-firewall.service -p ActiveState,SubState 2>&1
else
	echo "neither pve-firewall(8) nor systemctl present on this host"
fi'

# --- firewall-02: cluster-scope options + rules -----------------------------
harness_item "firewall-02" \
	"Firewall > cluster-scope options/rules wire shape" \
	'echo "--- /cluster/firewall/options ---"; api_get /cluster/firewall/options
echo "--- /cluster/firewall/rules ---"; api_get /cluster/firewall/rules'

# --- firewall-03: node-scope rules ------------------------------------------
harness_item "firewall-03" \
	"Firewall > node-scope rules wire shape" \
	'api_get "/nodes/$NODE/firewall/rules"'

# --- firewall-04: security groups, aliases, ipsets --------------------------
harness_item "firewall-04" \
	"Firewall > groups/aliases/ipsets wire shape" \
	'echo "--- /cluster/firewall/groups ---"; api_get /cluster/firewall/groups
echo "--- /cluster/firewall/aliases ---"; api_get /cluster/firewall/aliases
echo "--- /cluster/firewall/ipset ---"; api_get /cluster/firewall/ipset'

harness_emit
