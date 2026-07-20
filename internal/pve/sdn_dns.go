package pve

import (
	"context"
	"fmt"
	"net/url"
)

// SDN DNS management (T-1204). Real PVE's SDN DNS plugin stores DNS *zone*
// (plugin) config in /etc/pve/sdn/dns.cfg and writes individual records
// straight into the backing PowerDNS server per-record. vnprox models the
// two surfaces as: a config-truth zone list (this file's ListSDNDnsZones and
// the zone CRUD, under /cluster/sdn/dns) and a per-zone record set (the
// record CRUD, PowerDNS-shaped). A live "resolved" read
// (ResolveSDNDnsRecords) reads the same records back through the PowerDNS
// server, mirroring GET /sdn/dhcp's Reservation(config)/Lease(observed)
// duality. The exact real-PVE/PowerDNS wire shapes are unconfirmed against
// live hardware — see planning/reports/needs-hardware-validation.md.

// SDNDnsZone is one DNS zone config entry from /etc/pve/sdn/dns.cfg. ID is
// the zone's domain (sent as the "zone" body field on create); DNS names the
// backing PowerDNS plugin instance; TTL is the zone's default record TTL.
type SDNDnsZone struct {
	ID      string       `json:"zone"`
	DNS     string       `json:"dns,omitempty"`
	Type    string       `json:"type,omitempty"`
	Pending PendingState `json:"pending,omitempty"`
	TTL     int          `json:"ttl,omitempty"`
}

// SDNDnsRecord is one A/AAAA/PTR/CNAME/TXT record within a zone. Name is the
// hostname label, Type the record type, Value the record data (an IP for
// A/AAAA/PTR, a target for CNAME, text for TXT).
type SDNDnsRecord struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   int    `json:"ttl,omitempty"`
}

// ListSDNDnsZones calls GET /cluster/sdn/dns: the configured DNS zones.
func (c *Client) ListSDNDnsZones(ctx context.Context) ([]SDNDnsZone, error) {
	var out []SDNDnsZone
	if err := c.do(ctx, "GET", "/cluster/sdn/dns", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListSDNDnsRecords calls GET /cluster/sdn/dns/{zone}/records: the
// config-authoritative record set for one zone.
func (c *Client) ListSDNDnsRecords(ctx context.Context, zone string) ([]SDNDnsRecord, error) {
	var out []SDNDnsRecord
	path := fmt.Sprintf("/cluster/sdn/dns/%s/records", url.PathEscape(zone))
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ResolveSDNDnsRecords calls GET /cluster/sdn/dns/{zone}/resolve: a live read
// of the backing PowerDNS server's current records for the zone (the
// "resolved" half of GET /sdn/dns's config-vs-live duality). Errors when the
// PowerDNS server is unreachable — callers treat that as "no live data",
// never a hard failure.
func (c *Client) ResolveSDNDnsRecords(ctx context.Context, zone string) ([]SDNDnsRecord, error) {
	var out []SDNDnsRecord
	path := fmt.Sprintf("/cluster/sdn/dns/%s/resolve", url.PathEscape(zone))
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateSDNDnsZone calls POST /cluster/sdn/dns.
func (c *Client) CreateSDNDnsZone(ctx context.Context, z SDNDnsZone) error {
	return c.do(ctx, "POST", "/cluster/sdn/dns", requestParams{body: z}, nil)
}

// UpdateSDNDnsZone calls PUT /cluster/sdn/dns/{zone}.
func (c *Client) UpdateSDNDnsZone(ctx context.Context, id string, z SDNDnsZone) error {
	path := fmt.Sprintf("/cluster/sdn/dns/%s", url.PathEscape(id))
	return c.do(ctx, "PUT", path, requestParams{body: z}, nil)
}

// DeleteSDNDnsZone calls DELETE /cluster/sdn/dns/{zone}.
func (c *Client) DeleteSDNDnsZone(ctx context.Context, id string) error {
	path := fmt.Sprintf("/cluster/sdn/dns/%s", url.PathEscape(id))
	return c.do(ctx, "DELETE", path, requestParams{}, nil)
}

// CreateSDNDnsRecord calls POST /cluster/sdn/dns/{zone}/records.
func (c *Client) CreateSDNDnsRecord(ctx context.Context, zone string, r SDNDnsRecord) error {
	path := fmt.Sprintf("/cluster/sdn/dns/%s/records", url.PathEscape(zone))
	return c.do(ctx, "POST", path, requestParams{body: r}, nil)
}

// UpdateSDNDnsRecord calls PUT /cluster/sdn/dns/{zone}/records/{name}/{type}.
func (c *Client) UpdateSDNDnsRecord(ctx context.Context, zone, name, typ string, r SDNDnsRecord) error {
	path := fmt.Sprintf("/cluster/sdn/dns/%s/records/%s/%s", url.PathEscape(zone), url.PathEscape(name), url.PathEscape(typ))
	return c.do(ctx, "PUT", path, requestParams{body: r}, nil)
}

// DeleteSDNDnsRecord calls DELETE /cluster/sdn/dns/{zone}/records/{name}/{type}.
func (c *Client) DeleteSDNDnsRecord(ctx context.Context, zone, name, typ string) error {
	path := fmt.Sprintf("/cluster/sdn/dns/%s/records/%s/%s", url.PathEscape(zone), url.PathEscape(name), url.PathEscape(typ))
	return c.do(ctx, "DELETE", path, requestParams{}, nil)
}
