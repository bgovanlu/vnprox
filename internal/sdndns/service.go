// SPDX-License-Identifier: Apache-2.0

package sdndns

import (
	"context"
	"fmt"
	"sync"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// TopologyReader is the SDN configuration a DNS view is derived from. These
// are the same three reads the SDN poll already performs; the DNS view needs
// them because the domains live on the SDN zone, not under /cluster/sdn/dns.
type TopologyReader interface {
	ListSDNZones(ctx context.Context) ([]pve.SDNZone, error)
	ListSDNVnets(ctx context.Context) ([]pve.SDNVnet, error)
	ListSDNSubnets(ctx context.Context, vnet string) ([]pve.SDNSubnet, error)
}

// Service is the read-side entry point: it derives the DNS domains from SDN
// configuration and reads each one's records from PowerDNS.
//
// It exists alongside Reader rather than inside it because the two have
// different callers. internal/collect already holds the SDN zones, vnets and
// subnets from its own poll and passes them to DeriveZones directly — making
// it re-read them here would double the SDN request count every cycle. The
// API's GET /sdn/dns has no such poll behind it and needs the reads.
type Service struct {
	topo   TopologyReader
	reader *Reader
	// plugins is memoised for the lifetime of one Zones/Records pair, so a
	// view over twenty domains does not re-read the same plugin config twenty
	// times. It is refreshed on every Zones call: a stale url or key is how a
	// pinned client ends up talking to a server the operator has moved.
	//
	// It sits before mu, not after, for fieldalignment: the map is a pointer
	// word and the mutex holds none.
	plugins map[string]pve.SDNDnsPlugin
	mu      sync.Mutex
}

// NewService builds a Service. dial may be nil (powerdns.New is used).
func NewService(topo TopologyReader, p PVEReader, dial Dialer) *Service {
	return &Service{topo: topo, reader: NewReader(p, dial)}
}

// Zones derives every DNS domain vnprox can read, and the ones it could not.
// It refreshes the plugin-instance cache Records then uses.
func (s *Service) Zones(ctx context.Context) ([]Zone, []Skip, error) {
	plugins, err := s.reader.Plugins(ctx)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	s.plugins = plugins
	s.mu.Unlock()

	// No PowerDNS connection configured means no DNS view at all — and, more
	// usefully, nothing to report as broken. Returning early also skips three
	// SDN reads that could not produce a readable domain regardless.
	if len(plugins) == 0 {
		return nil, nil, nil
	}

	zones, err := s.topo.ListSDNZones(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("sdndns: listing sdn zones: %w", err)
	}
	vnets, err := s.topo.ListSDNVnets(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("sdndns: listing sdn vnets: %w", err)
	}
	var subnets []pve.SDNSubnet
	for _, v := range vnets {
		subs, err := s.topo.ListSDNSubnets(ctx, v.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("sdndns: listing subnets of vnet %s: %w", v.ID, err)
		}
		subnets = append(subnets, subs...)
	}

	list := make([]pve.SDNDnsPlugin, 0, len(plugins))
	for _, p := range plugins {
		list = append(list, p)
	}
	derived, skipped := DeriveZones(zones, vnets, subnets, list)
	return derived, skipped, nil
}

// Records reads one domain's records. The zone must have come from Zones —
// its Plugin names the connection to use, and a Plugin this Service has not
// seen is an error rather than a silent empty result.
func (s *Service) Records(ctx context.Context, zone Zone) ([]Record, error) {
	s.mu.Lock()
	plugin, ok := s.plugins[zone.Plugin]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("sdndns: zone %s names dns plugin %q, which is not configured", zone.Domain, zone.Plugin)
	}
	return s.reader.Records(ctx, zone, plugin)
}
