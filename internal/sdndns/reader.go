// SPDX-License-Identifier: Apache-2.0

package sdndns

import (
	"context"
	"fmt"
	"sync"

	"github.com/bgovanlu/vnprox/internal/powerdns"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// PVEReader is the slice of *pve.Client this package needs. A seam, so the
// PowerDNS half can be exercised without a PVE server and so callers can see
// exactly which PVE reads a DNS refresh performs.
type PVEReader interface {
	ListSDNDnsPlugins(ctx context.Context) ([]pve.SDNDnsPlugin, error)
	GetSDNDnsPlugin(ctx context.Context, id string) (pve.SDNDnsPlugin, error)
}

// Dialer builds a PowerDNS client from a plugin instance's configuration.
// Production passes powerdns.New; tests pass a dialer that points at an
// httptest server, which is the only way to exercise this package without
// either a real PowerDNS or a mock of the exact thing under test.
type Dialer func(powerdns.Config) (*powerdns.Client, error)

// Reader reads SDN DNS records: PVE for the configuration, PowerDNS for the
// records themselves.
//
// It caches one *powerdns.Client per plugin instance, keyed by the connection
// details rather than by the instance id — a plugin whose url, key or
// fingerprint changed must get a new client, or vnprox would keep talking to
// the old server (or keep pinning the old certificate) until the daemon
// restarted.
type Reader struct {
	// Field order is fieldalignment's: the pointer-bearing fields pack ahead
	// of the mutex, which contains none.
	pve    PVEReader
	dial   Dialer
	byKey  map[string]*powerdns.Client
	dialed map[string]error
	mu     sync.Mutex
}

// NewReader builds a Reader over a PVE client. dial may be nil, in which case
// powerdns.New is used.
func NewReader(p PVEReader, dial Dialer) *Reader {
	if dial == nil {
		dial = powerdns.New
	}
	return &Reader{
		pve:    p,
		dial:   dial,
		byKey:  map[string]*powerdns.Client{},
		dialed: map[string]error{},
	}
}

// Plugins reads every configured PowerDNS connection, with its url and key.
//
// The index route's declared schema names only `dns` and `type`, so each
// instance is re-read individually rather than trusting the list to carry the
// connection details. A cluster with no DNS plugin configured yields an empty
// map and no error — the ordinary case, not a failure.
func (r *Reader) Plugins(ctx context.Context) (map[string]pve.SDNDnsPlugin, error) {
	list, err := r.pve.ListSDNDnsPlugins(ctx)
	if err != nil {
		return nil, fmt.Errorf("sdndns: listing dns plugin instances: %w", err)
	}
	out := make(map[string]pve.SDNDnsPlugin, len(list))
	for _, p := range list {
		id := p.ID
		if id == "" {
			continue
		}
		full, err := r.pve.GetSDNDnsPlugin(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("sdndns: reading dns plugin instance %s: %w", id, err)
		}
		out[id] = full
	}
	return out, nil
}

// Records reads one zone's records from the PowerDNS server that serves it.
//
// A zone PowerDNS does not have is a 404, which the caller can recognise with
// powerdns.IsNotFound and report as a configuration problem rather than as an
// outage — the two are different states, and collapsing them is what left
// T-4109's PTR audit reporting `ptr_zone_unreadable` for everything.
func (r *Reader) Records(ctx context.Context, zone Zone, plugin pve.SDNDnsPlugin) ([]Record, error) {
	client, err := r.client(plugin)
	if err != nil {
		return nil, err
	}
	z, err := client.Zone(ctx, zone.Domain)
	if err != nil {
		return nil, fmt.Errorf("sdndns: reading zone %s from plugin %s: %w", zone.Domain, plugin.ID, err)
	}
	return FromZone(zone.Domain, z), nil
}

// Patch applies rrset changes to a zone through its plugin's PowerDNS server.
// This is the only write path in this package, and it is reached only from
// the change engine's apply step.
func (r *Reader) Patch(ctx context.Context, domain string, plugin pve.SDNDnsPlugin, changes []powerdns.RRSet) error {
	client, err := r.client(plugin)
	if err != nil {
		return err
	}
	if err := client.Patch(ctx, domain, changes); err != nil {
		return fmt.Errorf("sdndns: patching zone %s through plugin %s: %w", domain, plugin.ID, err)
	}
	return nil
}

// Zone reads a raw PowerDNS zone, for the read half of a read-modify-write.
// The change engine needs the existing rrset before it can build a REPLACE
// (internal/powerdns/rrset.go's builders all take one).
func (r *Reader) Zone(ctx context.Context, domain string, plugin pve.SDNDnsPlugin) (powerdns.Zone, error) {
	client, err := r.client(plugin)
	if err != nil {
		return powerdns.Zone{}, err
	}
	z, err := client.Zone(ctx, domain)
	if err != nil {
		return powerdns.Zone{}, fmt.Errorf("sdndns: reading zone %s from plugin %s: %w", domain, plugin.ID, err)
	}
	return z, nil
}

// client returns the cached PowerDNS client for a plugin instance, dialing
// one on first use.
//
// A dial failure is cached alongside the successes. Building a client fails
// only for reasons that will not change until the operator edits the plugin
// config — an empty url, an unparseable fingerprint — so retrying it once per
// zone per poll would produce identical errors at the cost of the log volume
// that hides real ones.
func (r *Reader) client(plugin pve.SDNDnsPlugin) (*powerdns.Client, error) {
	key := plugin.ID + "\x00" + plugin.URL + "\x00" + plugin.Key + "\x00" + plugin.Fingerprint
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.byKey[key]; ok {
		return c, nil
	}
	if err, ok := r.dialed[key]; ok {
		return nil, err
	}
	c, err := r.dial(powerdns.Config{
		URL:         plugin.URL,
		Key:         plugin.Key,
		Fingerprint: plugin.Fingerprint,
		TTL:         plugin.TTL,
	})
	if err != nil {
		err = fmt.Errorf("sdndns: connecting to the powerdns server for plugin %s: %w", plugin.ID, err)
		r.dialed[key] = err
		return nil, err
	}
	r.byKey[key] = c
	return c, nil
}
