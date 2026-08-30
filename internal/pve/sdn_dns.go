// SPDX-License-Identifier: Apache-2.0

package pve

import (
	"context"
	"fmt"
	"net/url"
)

// SDN DNS plugin configuration (T-1204, corrected by T-4112).
//
// # What /cluster/sdn/dns actually is
//
// It is a flat list of PowerDNS *server connections* — url, key, ttl,
// fingerprint — and NOT a list of DNS zones. `pvesh usage /cluster/sdn/dns/{dns}`
// on PVE 9.2.4 reports get/set/delete and nothing else; both
// `/cluster/sdn/dns/{dns}/records` and `/cluster/sdn/dns/{dns}/resolve` answer
// `no such resource` (planning/reports/evidence/pve-9.2.4-sdn-dns-surface.txt).
//
// This file used to carry five methods on those two invented sub-paths, under
// a doc comment that called the shapes "unconfirmed against live hardware".
// That understated it: the URL space did not exist, `internal/collect` called
// one of them once per zone on every poll cycle, and nothing ever failed
// because internal/pvemock served the invented routes too.
//
// # Where the records are, and where the domains are
//
// PVE keeps no record copy. `PowerdnsPlugin.pm` writes each record straight
// into the backing PowerDNS server, so records are read from PowerDNS —
// internal/powerdns is the client, internal/sdndns joins the two halves.
//
// The DNS *domains* are not here either: they are fields on the SDN zone
// (`dnszone`, `dns`, `reversedns` — see SDNZone), plus the reverse zones
// derived from each subnet's CIDR. A zone names which of these plugin
// instances to write through; this file only reads the connection details.

// SDNDnsPlugin is one /cluster/sdn/dns entry: a PowerDNS server vnprox and
// PVE both write records into.
//
// ID is the object's own identifier and decodes from `dns` — the field name
// `pvesh create /cluster/sdn/dns --dns ...` uses and that
// `PVE::API2::Network::SDN::Dns`'s config helper sets on every read. It was
// tagged `json:"zone"` before T-4112 and therefore decoded EMPTY against real
// PVE, which no test caught because internal/pvemock emitted `zone` too.
//
// Key is an API secret. It is read (GET /cluster/sdn/dns/{dns} returns the
// plugin config unredacted, behind SDN.Allocate) because vnprox cannot talk
// to PowerDNS without it, and it must never be logged or serialized into a
// changeset, a snapshot, or an API response — see internal/sdndns, which is
// the only package that holds one.
type SDNDnsPlugin struct {
	ID          string       `json:"dns"`
	Type        string       `json:"type,omitempty"`
	URL         string       `json:"url,omitempty"`
	Key         string       `json:"key,omitempty"`
	Fingerprint string       `json:"fingerprint,omitempty"`
	Digest      string       `json:"digest,omitempty"`
	Pending     PendingState `json:"pending,omitempty"`
	TTL         int          `json:"ttl,omitempty"`
	// ReverseMaskV6 forces a different prefix length for the IPv6 reverse
	// zone name (PowerdnsPlugin.pm's `reversemaskv6`). PVE 9.2.4's create
	// usage block also advertises a `reversev6mask` spelling; only
	// `reversemaskv6` is a declared plugin property and only it is read by
	// get_reversedns_zone, so that is the one decoded here.
	ReverseMaskV6 int `json:"reversemaskv6,omitempty"`
}

// ListSDNDnsPlugins calls GET /cluster/sdn/dns: every configured PowerDNS
// server connection. A cluster with no DNS plugin configured returns an empty
// list, which is not an error.
func (c *Client) ListSDNDnsPlugins(ctx context.Context) ([]SDNDnsPlugin, error) {
	var out []SDNDnsPlugin
	if err := c.do(ctx, "GET", "/cluster/sdn/dns", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSDNDnsPlugin calls GET /cluster/sdn/dns/{dns}: one plugin instance's
// full configuration, including the url and key vnprox needs to reach
// PowerDNS.
//
// The index route's declared return schema names only `dns` and `type`, so
// this per-instance read is what vnprox relies on for the connection details
// rather than assuming the list carries them.
func (c *Client) GetSDNDnsPlugin(ctx context.Context, id string) (SDNDnsPlugin, error) {
	var out SDNDnsPlugin
	path := fmt.Sprintf("/cluster/sdn/dns/%s", url.PathEscape(id))
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return SDNDnsPlugin{}, err
	}
	if out.ID == "" {
		out.ID = id
	}
	return out, nil
}

// CreateSDNDnsPlugin calls POST /cluster/sdn/dns. PVE requires dns, type, url
// and key; a create missing any of them is a 400 from PVE's own parameter
// verification rather than something this client screens for.
func (c *Client) CreateSDNDnsPlugin(ctx context.Context, p SDNDnsPlugin) error {
	return c.do(ctx, "POST", "/cluster/sdn/dns", requestParams{body: p}, nil)
}

// UpdateSDNDnsPlugin calls PUT /cluster/sdn/dns/{dns}.
func (c *Client) UpdateSDNDnsPlugin(ctx context.Context, id string, p SDNDnsPlugin) error {
	path := fmt.Sprintf("/cluster/sdn/dns/%s", url.PathEscape(id))
	return c.do(ctx, "PUT", path, requestParams{body: p}, nil)
}

// DeleteSDNDnsPlugin calls DELETE /cluster/sdn/dns/{dns}.
func (c *Client) DeleteSDNDnsPlugin(ctx context.Context, id string) error {
	path := fmt.Sprintf("/cluster/sdn/dns/%s", url.PathEscape(id))
	return c.do(ctx, "DELETE", path, requestParams{}, nil)
}
