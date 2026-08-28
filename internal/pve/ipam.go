// SPDX-License-Identifier: Apache-2.0

package pve

import (
	"context"
	"fmt"
)

// IPAM is one configured IPAM plugin instance, as listed by
// GET /cluster/sdn/ipams. PVE ships a built-in "pve" IPAM; NetBox and
// phpIPAM instances appear here too when configured in PVE (vnprox reads
// through the plugin transparently — docs/features/ipam.md §1).
//
// Pending/Token/Fingerprint/Section (T-3104, sdn_ipam.go's write path) are
// additive. Pending was originally documented as mirroring SDNFabric/
// SDNController's staged-vs-running state; the debt-sweep 2026-08-19
// follow-up ("SDNController.Pending and SDNFabric.Pending have the same
// gap") found that claim wrong on two counts, confirmed directly against
// pvecube's own perl source (planning/reports/evidence/
// pve-9.2.4-sdn-pending-state.txt §6): first, the same T-401-era trap
// (this field decodes a "pending" key the DEFAULT view never actually
// carries against real PVE); second, unlike SDNFabric/SDNController there
// is no "?pending=1" escape hatch to fall back on either —
// PVE::API2::Network::SDN/Ipams.pm accepts no `pending` parameter at all
// (grep-confirmed, zero hits), because an IPAM plugin instance is simply
// not part of PVE's pending/running SDN commit cycle the way
// zones/vnets/subnets/controllers/fabrics are. So this field is
// permanently PendingNone against real PVE, with no fix available — not
// merely unobserved, but describing a distinction real PVE itself does not
// draw. internal/inventory.FromPVESDNIpams does not propagate it for this
// reason (see that function's doc comment). Token is write-only: the capture gives no
// evidence GET ever echoes a configured secret back (see
// internal/inventory/sdn_ipam.go's doc comment), so this field is only ever
// populated by a caller constructing a create/update request — a value
// read off ListIPAMs/GetIPAMStatus will never have it set.
type IPAM struct {
	ID          string       `json:"ipam"`
	Type        string       `json:"type"` // pve|netbox|phpipam
	URL         string       `json:"url,omitempty"`
	Pending     PendingState `json:"pending,omitempty"`
	Token       string       `json:"token,omitempty"`
	Fingerprint string       `json:"fingerprint,omitempty"`
	Section     int          `json:"section,omitempty"`
}

// IPAMEntry is one allocation row of GET /cluster/sdn/ipams/{ipam}/status:
// a currently allocated (or gateway-reserved) address inside an SDN
// subnet. This is the primary data source for vnprox's /ipam/* read views
// (docs/api.md: subnet utilization counts and the allocation grid).
type IPAMEntry struct {
	Zone     string
	Vnet     string
	Subnet   string // CIDR
	IP       string
	MAC      string
	Hostname string
	VMID     int
	Gateway  bool
}

// ipamEntryWire mirrors the endpoint's wire shape (PVE reports the gateway
// marker as a 0/1 int, like cluster/status's online/quorate flags);
// IPAMEntry is the converted, ergonomic form callers see.
type ipamEntryWire struct {
	Zone     string `json:"zone"`
	Vnet     string `json:"vnet"`
	Subnet   string `json:"subnet"`
	IP       string `json:"ip"`
	MAC      string `json:"mac,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	VMID     int    `json:"vmid,omitempty"`
	Gateway  int    `json:"gateway,omitempty"`
}

func (w ipamEntryWire) toEntry() IPAMEntry {
	return IPAMEntry{
		Zone:     w.Zone,
		Vnet:     w.Vnet,
		Subnet:   w.Subnet,
		IP:       w.IP,
		MAC:      w.MAC,
		Hostname: w.Hostname,
		VMID:     w.VMID,
		Gateway:  w.Gateway != 0,
	}
}

// ListIPAMs calls GET /cluster/sdn/ipams: the configured IPAM plugin
// instances.
func (c *Client) ListIPAMs(ctx context.Context) ([]IPAM, error) {
	var out []IPAM
	if err := c.do(ctx, "GET", "/cluster/sdn/ipams", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetIPAMStatus calls GET /cluster/sdn/ipams/{ipam}/status: the named IPAM
// instance's current allocation set (one IPAMEntry per allocated or
// gateway-reserved address, across all SDN subnets it manages).
func (c *Client) GetIPAMStatus(ctx context.Context, ipam string) ([]IPAMEntry, error) {
	var wire []ipamEntryWire
	path := fmt.Sprintf("/cluster/sdn/ipams/%s/status", ipam)
	if err := c.do(ctx, "GET", path, requestParams{}, &wire); err != nil {
		return nil, err
	}
	out := make([]IPAMEntry, 0, len(wire))
	for _, w := range wire {
		out = append(out, w.toEntry())
	}
	return out, nil
}

// CreateIPAMAllocation is T-405's write path for ipam.alloc.create: POST
// /cluster/sdn/vnets/{vnet}/ips reserves ip inside vnet's IPAM. Real PVE
// resolves which configured IPAM plugin instance (built-in "pve", NetBox,
// phpIPAM) backs vnet from the vnet's zone config server-side — callers
// never name the plugin explicitly (docs/features/ipam.md §1: "vnprox
// reads through PVE's plugin transparently"), which is why this method
// (and its DeleteIPAMAllocation counterpart) is vnet-scoped rather than
// ipam-instance-scoped like ListIPAMs/GetIPAMStatus above.
func (c *Client) CreateIPAMAllocation(ctx context.Context, vnet string, alloc IPAMAllocation) error {
	path := fmt.Sprintf("/cluster/sdn/vnets/%s/ips", vnet)
	body := map[string]string{"ip": alloc.IP}
	if alloc.MAC != "" {
		body["mac"] = alloc.MAC
	}
	if alloc.Hostname != "" {
		body["hostname"] = alloc.Hostname
	}
	if alloc.Zone != "" {
		body["zone"] = alloc.Zone
	}
	if alloc.Subnet != "" {
		body["subnet"] = alloc.Subnet
	}
	return c.do(ctx, "POST", path, requestParams{body: body}, nil)
}

// DeleteIPAMAllocation is T-405's write path for ipam.alloc.delete: DELETE
// /cluster/sdn/vnets/{vnet}/ips releases ip from vnet's IPAM. subnet
// disambiguates when the same host address is legitimately allocated in
// more than one subnet the vnet carries (rare, but the CIDR is the
// referential identity ipam.alloc ops key off — docs/data-model.md §3).
func (c *Client) DeleteIPAMAllocation(ctx context.Context, vnet, ip, subnet string) error {
	path := fmt.Sprintf("/cluster/sdn/vnets/%s/ips", vnet)
	body := map[string]string{"ip": ip}
	if subnet != "" {
		body["subnet"] = subnet
	}
	return c.do(ctx, "DELETE", path, requestParams{body: body}, nil)
}

// IPAMAllocation is CreateIPAMAllocation's request shape: the address being
// reserved plus the optional identifying metadata PVE's IPAM plugins record
// alongside it (docs/data-model.md §3's ipam.alloc.create params).
type IPAMAllocation struct {
	IP       string
	MAC      string
	Hostname string
	Zone     string
	Subnet   string
}
