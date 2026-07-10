package pve

import (
	"context"
	"fmt"
)

// IPAM is one configured IPAM plugin instance, as listed by
// GET /cluster/sdn/ipams. PVE ships a built-in "pve" IPAM; NetBox and
// phpIPAM instances appear here too when configured in PVE (vnprox reads
// through the plugin transparently — docs/features/ipam.md §1).
type IPAM struct {
	ID   string `json:"ipam"`
	Type string `json:"type"` // pve|netbox|phpipam
	URL  string `json:"url,omitempty"`
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
