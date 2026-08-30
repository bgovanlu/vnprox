// SPDX-License-Identifier: Apache-2.0

package powerdns

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Zone is a PowerDNS zone as returned by GET /zones/{zone}. Only the fields
// vnprox reads are decoded; PowerDNS returns many more (serial, masters,
// dnssec, ...) and unknown fields are ignored rather than rejected, because
// this client must not break when the operator's PowerDNS is a newer version
// than the one it was written against.
type Zone struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Kind   string  `json:"kind"`
	RRSets []RRSet `json:"rrsets"`
}

// RRSet is one PowerDNS resource-record set: every record sharing a name and
// a type. This is PowerDNS's unit of both read and write — there is no
// per-record route — which is why vnprox's own per-record ops become
// read-modify-write on an rrset (see rrset.go's builders).
type RRSet struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	ChangeType string   `json:"changetype,omitempty"`
	Records    []Record `json:"records"`
	TTL        int      `json:"ttl,omitempty"`
}

// Record is one record inside an rrset. Content is the record data in
// PowerDNS's own presentation form — an IP for A/AAAA, a trailing-dot FQDN
// for PTR/CNAME, a quoted string for TXT. Disabled records exist in PowerDNS
// and are not served; vnprox surfaces them rather than hiding them, because a
// disabled PTR looks exactly like a missing one to a resolver and the PTR
// audit must be able to tell those apart.
type Record struct {
	Content  string `json:"content"`
	Disabled bool   `json:"disabled"`
}

// changetype values PowerDNS accepts on a PATCH. There is no "CREATE":
// creating and editing are both REPLACE, which is why every write in this
// package reads the existing rrset first.
const (
	ChangeReplace = "REPLACE"
	ChangeDelete  = "DELETE"
)

// Ping issues the plugin's own health check — `GET` against the bare API
// base, which is PowerdnsPlugin.pm's `on_update_hook`:
// `powerdns_api_request($plugin_config, 'GET', <empty path>)`. vnprox uses it
// for the same purpose: deciding whether this PowerDNS connection works at all,
// separately from whether any particular zone exists.
func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, "GET", "", nil, nil)
}

// Zone reads one zone with its full rrset list: PowerdnsPlugin.pm's
// `get_zone_content`, `GET /zones/$zone`.
//
// zone is the DNS domain. PowerDNS canonicalises zone ids with a trailing dot
// and vnprox may hold either form, so zoneID normalises before building the
// path — see its comment for why guessing wrong is a 404 and not a wrong
// answer.
func (c *Client) Zone(ctx context.Context, zone string) (Zone, error) {
	var out Zone
	path := "/zones/" + url.PathEscape(zoneID(zone))
	if err := c.do(ctx, "GET", path, nil, &out); err != nil {
		return Zone{}, err
	}
	return out, nil
}

// VerifyZone checks a zone exists without pulling its records:
// PowerdnsPlugin.pm's `verify_zone`, `GET /zones/$zone?rrsets=false`. Cheap
// enough to run per poll for a zone vnprox only needs to know the existence
// of.
func (c *Client) VerifyZone(ctx context.Context, zone string) error {
	path := "/zones/" + url.PathEscape(zoneID(zone)) + "?rrsets=false"
	return c.do(ctx, "GET", path, nil, nil)
}

// Patch applies rrset changes to a zone in one request: PowerdnsPlugin.pm's
// `powerdns_api_request($config, 'PATCH', "/zones/$zone", {rrsets => [...]})`.
//
// Every change must carry a ChangeType; PowerDNS silently ignores an rrset
// without one, which would make a vnprox apply report success having done
// nothing. That is checked here rather than trusted, because "the write
// returned 200 and changed nothing" is the failure this whole card exists to
// stop repeating.
func (c *Client) Patch(ctx context.Context, zone string, changes []RRSet) error {
	if len(changes) == 0 {
		return nil
	}
	for i, ch := range changes {
		switch ch.ChangeType {
		case ChangeReplace, ChangeDelete:
		default:
			return fmt.Errorf("powerdns: rrset %d (%s/%s) has changetype %q, want %s or %s",
				i, ch.Name, ch.Type, ch.ChangeType, ChangeReplace, ChangeDelete)
		}
		if ch.Name == "" || ch.Type == "" {
			return fmt.Errorf("powerdns: rrset %d needs both a name and a type (got %q/%q)", i, ch.Name, ch.Type)
		}
	}
	body := struct {
		RRSets []RRSet `json:"rrsets"`
	}{RRSets: changes}
	path := "/zones/" + url.PathEscape(zoneID(zone))
	return c.do(ctx, "PATCH", path, body, nil)
}

// zoneID normalises a domain into the form PowerDNS uses as a zone id: the
// canonical name with a trailing dot. PVE passes whatever the operator typed
// into `dnszone` straight through ("$config->{url}/zones/$zone"), so both
// forms reach real servers today and modern PowerDNS accepts either; vnprox
// sends the canonical one so two zones that differ only in a trailing dot
// cannot become two entries in vnprox's own inventory.
//
// A wrong guess here is a 404, never a wrong answer about a different zone —
// which is why normalising is safe to do unconditionally.
func zoneID(zone string) string {
	z := strings.TrimSpace(zone)
	if z == "" || strings.HasSuffix(z, ".") {
		return z
	}
	return z + "."
}

// Find returns the rrset with this name and type: PowerdnsPlugin.pm's
// `get_zone_rrset`. The comparison is on the canonical trailing-dot form of
// both sides, because PowerDNS always answers with it and a caller holding a
// name from vnprox's own inventory may not.
func (z Zone) Find(name, typ string) (RRSet, bool) {
	want := zoneID(name)
	for _, rr := range z.RRSets {
		if zoneID(rr.Name) == want && rr.Type == typ {
			return rr, true
		}
	}
	return RRSet{}, false
}

// SortRRSets orders a zone's rrsets by name then type. PowerDNS's own
// ordering is not documented as stable, and vnprox turns these into inventory
// entities whose order is compared between polls — an unstable read would
// show as drift that is not there.
func SortRRSets(rrsets []RRSet) {
	sort.SliceStable(rrsets, func(i, j int) bool {
		if rrsets[i].Name != rrsets[j].Name {
			return rrsets[i].Name < rrsets[j].Name
		}
		return rrsets[i].Type < rrsets[j].Type
	})
}
