package inventory

// SdnIpam is one configured PVE SDN IPAM plugin instance (T-3104): a
// netbox/phpipam/pve plugin object at /cluster/sdn/ipams, captured
// read-only from a live PVE 9.2.4 node — planning/reports/evidence/
// pve-9.2.4-sdn-schema.txt. Cluster-scoped (empty Node, like every other
// sdn-* entity). ID is the instance's own id; Type is one of
// "netbox"|"phpipam"|"pve" (validate_schema.go's validSdnIpamTypes mirrors
// this exact enum, guarded by TestValidSdnIpamTypesMatchTheCapturedEnum
// against drifting from the capture).
//
// Token is deliberately absent from this entity: real PVE's GET
// /cluster/sdn/ipams (and this package's read-only ingest of it) has no
// evidence of ever echoing a configured secret back — the capture's `pvesh
// get /cluster/sdn/ipams` response for the fixture's own "pve" instance
// carries no token field, consistent with how PVE treats other plugin
// secrets (e.g. the SDN DNS plugin's own key) as write-only. Modelling
// Token here would imply vnprox can read back a value it in fact never
// receives — see internal/pve/sdn_ipam.go's package doc comment for the
// production-wiring consequence this has for T-3104 item 3.
//
// Unlike SdnFabric (deliberately not a live-polled inventory entity — see
// KindSDNFabric's doc comment), an ipam instance IS live-polled here,
// mirroring SdnController's precedent rather than Fabric's: a zone's `ipam`
// field is a live reference other ops need to validate against (the
// in-use-on-delete block, this task's acceptance criterion 2, needs
// exactly this: a live SdnZone.IPAM field to scan against —
// checkSdnIpamDeletable, internal/change/validate_referential.go).
type SdnIpam struct {
	Ref
	rawSrc
	ID          string
	Type        string
	Pending     string
	URL         string
	Fingerprint string
	Section     int
}

func (i *SdnIpam) GetRef() Ref { return i.Ref }
func (i *SdnIpam) clone() Entity {
	cp := *i
	return &cp
}
func (i *SdnIpam) fieldMap() map[string]string {
	return map[string]string{
		"id": i.ID, "type": i.Type, "pending": i.Pending, "url": i.URL,
	}
}
