#!/usr/bin/env bash
# planning/validation/harness/sdn.sh — SDN section (T-1801). Read-only.
#
# Covers `planning/reports/needs-hardware-validation.md`'s "## SDN subnet
# gateway (T-701)" and "## SDN object naming and VNI (issue #3)" headings:
# zone/vnet/subnet shapes, per-zone status (pending/applied/error), and the
# gateway IPAM-registration timing question. Does not attempt the
# SNAT-without-gateway / gateway-outside-CIDR rejection checks or the
# naming/VNI validation checks from those sections — both require staging
# a new object, which is change-engine territory (MUTATES=1) and out of scope
# for this read-only baseline; T-1802 can add a MUTATES=1 sdn-mutate.sh
# companion using change-engine.sh's pattern if it needs them.
#
# Usage:
#   ssh pvecube 'bash -s' < planning/validation/harness/sdn.sh > sdn-evidence.json
# Against pvemock (three-node-vlan.yaml or evpn-lab.yaml have real SDN
# content; single-node.yaml has none, which is still schema-valid — an
# empty zones/vnets list is itself evidence):
#   PVE_API_BASE_URL=http://localhost:8006/api2/json \
#     bash planning/validation/harness/sdn.sh

SECTION="sdn"
MUTATES=0

# shellcheck source=lib/common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/common.sh"

# --- sdn-01: zones list + per-zone status ----------------------------------
harness_item "sdn-01" \
	"SDN subnet gateway (T-701) > zone status realization" \
	'zones="$(api_get /cluster/sdn/zones)"
echo "--- /cluster/sdn/zones ---"
echo "$zones"
for z in $(printf "%s" "$zones" | grep -o "\"zone\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | sed -E "s/.*:[[:space:]]*\"([^\"]*)\"/\1/"); do
	echo "--- /cluster/sdn/zones/$z/status ---"
	api_get "/cluster/sdn/zones/$z/status"
done'

# --- sdn-02: vnets list -----------------------------------------------------
harness_item "sdn-02" \
	"SDN object naming and VNI (issue #3) > vnet id/VNI shape as PVE actually stores it" \
	'api_get /cluster/sdn/vnets'

# --- sdn-03: subnets + gateway marker ---------------------------------------
harness_item "sdn-03" \
	"SDN subnet gateway (T-701) > gateway IPAM registration timing" \
	'vnets="$(api_get /cluster/sdn/vnets)"
for v in $(printf "%s" "$vnets" | grep -o "\"vnet\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | sed -E "s/.*:[[:space:]]*\"([^\"]*)\"/\1/"); do
	echo "--- /cluster/sdn/vnets/$v/subnets ---"
	api_get "/cluster/sdn/vnets/$v/subnets"
done'

# --- sdn-04: cluster-wide SDN apply status ----------------------------------
harness_item "sdn-04" \
	"SDN subnet gateway (T-701) > cluster-wide SDN realization state" \
	'api_get /cluster/sdn'

harness_emit
