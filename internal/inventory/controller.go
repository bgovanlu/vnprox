// SPDX-License-Identifier: Apache-2.0

package inventory

import "strconv"

// SdnController is one PVE SDN controller (T-3102): a BGP/EVPN/faucet/isis
// underlay-control-plane object at /cluster/sdn/controllers, captured
// read-only from a live PVE 9.2.4 node — planning/reports/evidence/
// pve-9.2.4-sdn-schema.txt. Cluster-scoped (empty Node, like every other
// sdn-* entity). ID is the controller's own id; Type is one of
// "bgp"|"evpn"|"faucet"|"isis" (validate_schema.go's validSdnControllerTypes
// mirrors this exact enum, guarded by TestValidSdnControllerTypesMatchTheCapturedEnum
// against drifting from the capture).
//
// Unlike SdnFabric (deliberately not a live-polled inventory entity — see
// KindSDNFabric's doc comment), a controller IS live-polled here, mirroring
// SdnDnsZone/SdnDnsRecord's precedent rather than Fabric's: a zone's
// `controller` field is a live reference other ops need to validate against
// (existence on create/update targeting an id that must be a real
// controller is a documented, deliberately-scoped-out gap — see
// validate_referential.go's doc comment on checkSdnControllerDeletable —
// but the in-use-on-delete block, T-3102 acceptance criterion 5, needs
// exactly this: a live SdnZone.Controller field to scan against).
type SdnController struct {
	Ref
	rawSrc
	ID         string
	Type       string
	Pending    string
	BgpMode    string
	Fabric     string
	Loopback   string
	Node       string
	IsisNet    string
	IsisDomain string
	// PeerGroupName defaults to "VTEP" on real PVE when unset (evpn-only);
	// carried verbatim (never defaulted here — that is PVE's own behaviour
	// to apply, not this read model's).
	PeerGroupName           string
	RouteMapIn              string
	RouteMapOut             string
	Nodes                   []string
	Peers                   []string
	IsisIfaces              []string
	ASN                     int
	EbgpMultihop            int
	Ebgp                    bool
	BgpMultipathAsPathRelax bool
}

func (c *SdnController) GetRef() Ref { return c.Ref }
func (c *SdnController) clone() Entity {
	cp := *c
	cp.Nodes = append([]string(nil), c.Nodes...)
	cp.Peers = append([]string(nil), c.Peers...)
	cp.IsisIfaces = append([]string(nil), c.IsisIfaces...)
	return &cp
}
func (c *SdnController) fieldMap() map[string]string {
	return map[string]string{
		"id": c.ID, "type": c.Type, "pending": c.Pending,
		"asn": strconv.Itoa(c.ASN), "fabric": c.Fabric,
	}
}
