// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"bytes"
	"context"
	"encoding/csv"
	"sort"
	"strconv"
)

// csvHeader is AllocationsCSV's fixed column order.
var csvHeader = []string{"ip", "state", "confidence", "hostname", "mac", "vmid", "sources"}

// AllocationsCSV builds docs/features/ipam.md §3's "CSV export per subnet":
// one row per non-free address (a /16's worth of "free" rows would be
// 65,000+ lines of pure noise — the export is deliberately sparse, mirroring
// the grid's own sparse internal representation, not a literal dump of the
// full address space) regardless of the subnet's paging status — the export
// always covers the whole subnet, never just one page.
func (s *Service) AllocationsCSV(ctx context.Context, cidr string) ([]byte, error) {
	rs, err := s.resolveSubnet(ctx, cidr)
	if err != nil {
		return nil, err
	}
	cellMap, _ := mergeSubnet(rs.allocs, rs.obs, rs.known, rs.gateway)

	ips := make([]string, 0, len(cellMap))
	for ip := range cellMap {
		ips = append(ips, ip)
	}
	sort.Strings(ips) // lexical is good enough for a CSV export's row order

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(csvHeader); err != nil {
		return nil, err
	}
	for _, ip := range ips {
		c := cellMap[ip]
		if c.State == CellFree {
			continue
		}
		vmid := ""
		if c.VMID > 0 {
			vmid = strconv.Itoa(c.VMID)
		}
		row := []string{
			c.IP, string(c.State), string(c.Confidence), c.Hostname, c.MAC, vmid,
			joinSources(c.Sources),
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func joinSources(sources []string) string {
	if len(sources) == 0 {
		return ""
	}
	out := sources[0]
	for _, s := range sources[1:] {
		out += ";" + s
	}
	return out
}
