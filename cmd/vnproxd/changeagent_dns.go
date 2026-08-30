// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/powerdns"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// The apply path for sdn.dns.record.* ops (T-4112).
//
// These three ops used to call pve.Client methods on
// /cluster/sdn/dns/{zone}/records..., a URL space that does not exist on any
// PVE. Applying such a changeset would have 404'd at the moment of writing to
// the network — the one place in this product where a wrong guess is most
// expensive. Nothing caught it because internal/pvemock served the invented
// routes, so mock and caller agreed with each other and with nothing real.
//
// The records live in the PowerDNS server the SDN zone's `dns` (or, for PTRs,
// `reversedns`) plugin instance points at. Writing one is therefore:
//
//  1. derive the DNS domains from SDN configuration and find the one this op
//     names, which also tells us which PowerDNS connection serves it;
//  2. read the existing rrset, because PowerDNS's write unit is the whole
//     rrset and there is no per-record route;
//  3. PATCH the one rrset that changed.
//
// Step 2 is why every verb below reads before it writes, and why "the record
// is already exactly as asked" is a distinct, no-request outcome rather than
// a redundant write — internal/powerdns/rrset.go carries the same behaviour
// PowerdnsPlugin.pm has, so a record PVE wrote and a record vnprox wrote are
// indistinguishable afterwards.

type dnsRecordVerb int

const (
	// dnsRecordAdd appends a value to the rrset, leaving any siblings alone
	// (PowerdnsPlugin.pm's add_a_record).
	dnsRecordAdd dnsRecordVerb = iota
	// dnsRecordReplace sets a single-valued rrset's value, and refuses if
	// the rrset holds more than one record — see powerdns.ErrAmbiguousUpdate
	// for why guessing there would silently destroy records.
	dnsRecordReplace
	// dnsRecordRemove drops a value, deleting the rrset when nothing is left
	// (PowerdnsPlugin.pm's del_a_record).
	dnsRecordRemove
)

// dnsRecordWrite is one record mutation: what to write, and how.
type dnsRecordWrite struct {
	value string
	ttl   int
	verb  dnsRecordVerb
}

// applyDNSRecord realizes one sdn.dns.record.* op against PowerDNS.
//
// zone is the DNS domain, name the label relative to it (or "@" for the
// apex), typ the record type. A delete carries no value, so it removes the
// rrset outright rather than removing one value from it: vnprox's record
// identity is "<zone>/<name>/<type>", which names an rrset and not a record
// within one, and deleting "the record" can only mean the thing that identity
// refers to.
func (g *pveGateway) applyDNSRecord(ctx context.Context, zone, name, typ string, w dnsRecordWrite) error {
	if zone == "" || name == "" || typ == "" {
		return fmt.Errorf("changeagent: dns record op needs a zone, name and type (got %q/%q/%q)", zone, name, typ)
	}

	plugin, domain, err := g.dnsPluginFor(ctx, zone)
	if err != nil {
		return err
	}
	_, reader := g.dnsSeams()

	existingZone, err := reader.Zone(ctx, domain, plugin)
	if err != nil {
		return err
	}
	fqdn := dnsFQDN(name, domain)
	existing, _ := existingZone.Find(fqdn, typ)

	ttl := powerdns.Config{TTL: plugin.TTL}.EffectiveTTL(w.ttl)

	var change powerdns.RRSet
	var needsWrite bool
	switch w.verb {
	case dnsRecordAdd:
		change, needsWrite = powerdns.AddContent(existing, fqdn, typ, w.value, ttl)
	case dnsRecordReplace:
		change, needsWrite, err = powerdns.ReplaceSingle(existing, fqdn, typ, w.value, ttl)
		if err != nil {
			return fmt.Errorf("changeagent: updating dns record %s/%s/%s: %w", zone, name, typ, err)
		}
	case dnsRecordRemove:
		if len(existing.Records) == 0 {
			// Nothing there. Not an error: a delete whose target is already
			// gone has achieved what it asked for, and failing here would
			// make a rollback that re-runs the delete fail too.
			return nil
		}
		change, needsWrite = powerdns.DeleteRRSet(fqdn, typ), true
	default:
		return fmt.Errorf("changeagent: unknown dns record verb %d", w.verb)
	}

	if !needsWrite {
		return nil
	}
	return reader.Patch(ctx, domain, plugin, []powerdns.RRSet{change})
}

// dnsPluginFor finds the PowerDNS connection serving a domain, and returns
// the domain in its canonical form.
//
// It derives the domain list from SDN configuration rather than trusting the
// op's zone string, because a domain vnprox cannot derive is a domain no PVE
// zone registers records in: writing to it would put records somewhere PVE
// will never read them, which is the same class of mistake as the invented
// routes this replaced. The error says which domains ARE known, since the
// usual cause is a trailing dot or a zone whose `dns` field is unset.
func (g *pveGateway) dnsPluginFor(ctx context.Context, domain string) (pve.SDNDnsPlugin, string, error) {
	svc, rdr := g.dnsSeams()
	zones, _, err := svc.Zones(ctx)
	if err != nil {
		return pve.SDNDnsPlugin{}, "", fmt.Errorf("changeagent: resolving dns zone %s: %w", domain, err)
	}
	plugins, err := rdr.Plugins(ctx)
	if err != nil {
		return pve.SDNDnsPlugin{}, "", fmt.Errorf("changeagent: resolving dns zone %s: %w", domain, err)
	}

	want := dnsCanonical(domain)
	known := make([]string, 0, len(zones))
	for _, z := range zones {
		known = append(known, z.Domain)
		if z.Domain != want {
			continue
		}
		plugin, ok := plugins[z.Plugin]
		if !ok {
			return pve.SDNDnsPlugin{}, "", fmt.Errorf(
				"changeagent: dns zone %s names plugin %q, which is not configured under /cluster/sdn/dns", domain, z.Plugin)
		}
		return plugin, z.Domain, nil
	}
	return pve.SDNDnsPlugin{}, "", fmt.Errorf(
		"changeagent: no SDN zone registers dns domain %s (vnprox knows %v) — "+
			"a domain PVE does not register is one it will never read records from", domain, known)
}

// dnsFQDN turns a record's zone-relative label into the fully-qualified name
// PowerDNS uses as an rrset name. "@" is the apex.
func dnsFQDN(name, domain string) string {
	d := dnsCanonical(domain)
	if name == "" || name == "@" {
		return d
	}
	return dnsCanonical(name + "." + d)
}

func dnsCanonical(s string) string {
	if s == "" || s[len(s)-1] == '.' {
		return s
	}
	return s + "."
}
