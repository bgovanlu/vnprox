// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/bgovanlu/vnprox/internal/store"
)

// ExternalSubnetStore is the subset of *store.ExternalSubnetRepo this package
// needs — declared as an interface (the same "small seam" pattern PVEReader/
// InventorySource use) so the service's external-subnet CRUD stays
// test-doubleable without a real SQLite DB. Nil contributes no external rows
// and makes every external CRUD method a validation error (the route family
// simply isn't available in that degraded wiring).
type ExternalSubnetStore interface {
	Insert(ctx context.Context, e store.ExternalSubnet) error
	Get(ctx context.Context, id string) (store.ExternalSubnet, error)
	List(ctx context.Context) ([]store.ExternalSubnet, error)
	Update(ctx context.Context, e store.ExternalSubnet) error
	Delete(ctx context.Context, id string) error
}

// ExternalSubnet is the app-facing form of one external_subnets row
// (docs/api.md's /ipam/external-subnets shape). It is app-owned intent — IP
// space Proxmox has no knowledge of — never a shadow copy of a PVE SDN
// subnet (T-1203 / docs/features/ipam.md).
type ExternalSubnet struct {
	ID          string `json:"id"`
	CIDR        string `json:"cidr"`
	Label       string `json:"label,omitempty"`
	Source      string `json:"source"`
	Description string `json:"description,omitempty"`
	CreatedBy   string `json:"createdBy"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

func toExternalSubnet(row store.ExternalSubnet) ExternalSubnet {
	return ExternalSubnet{
		ID: row.ID, CIDR: row.CIDR, Label: row.Label, Source: row.Source,
		Description: row.Description, CreatedBy: row.CreatedBy,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

// validExternalSource reports whether s is one of the three allowed
// provenance values (docs/data-model.md's external_subnets.source).
func validExternalSource(s string) bool {
	switch s {
	case store.ExternalSubnetSourceManual, store.ExternalSubnetSourceNetbox, store.ExternalSubnetSourcePhpIPAM:
		return true
	default:
		return false
	}
}

// normalizeExternalCIDR validates and canonicalizes an external subnet's CIDR
// to its network form (e.g. 192.0.2.10/24 -> 192.0.2.0/24), so the unique
// index and cross-cluster overlap check compare canonical networks.
func normalizeExternalCIDR(cidr string) (string, error) {
	cidr = strings.TrimSpace(cidr)
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("ipam: invalid external subnet cidr %q: %w", cidr, err)
	}
	return ipnet.String(), nil
}

// ListExternalSubnets returns every external subnet record.
func (s *Service) ListExternalSubnets(ctx context.Context) ([]ExternalSubnet, error) {
	if s.external == nil {
		return nil, nil
	}
	rows, err := s.external.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("ipam: listing external subnets: %w", err)
	}
	out := make([]ExternalSubnet, 0, len(rows))
	for _, row := range rows {
		out = append(out, toExternalSubnet(row))
	}
	return out, nil
}

// GetExternalSubnet returns one external subnet by id (wrapping
// store.ErrNotFound).
func (s *Service) GetExternalSubnet(ctx context.Context, id string) (ExternalSubnet, error) {
	if s.external == nil {
		return ExternalSubnet{}, fmt.Errorf("ipam: external subnets are not enabled")
	}
	row, err := s.external.Get(ctx, id)
	if err != nil {
		return ExternalSubnet{}, fmt.Errorf("ipam: getting external subnet %s: %w", id, err)
	}
	return toExternalSubnet(row), nil
}

// CreateExternalSubnet validates and inserts a new external subnet record.
// source defaults to "manual" when empty. The CIDR is canonicalized to its
// network form before storage.
func (s *Service) CreateExternalSubnet(ctx context.Context, cidr, label, source, description, createdBy string) (ExternalSubnet, error) {
	if s.external == nil {
		return ExternalSubnet{}, fmt.Errorf("ipam: external subnets are not enabled")
	}
	network, err := normalizeExternalCIDR(cidr)
	if err != nil {
		return ExternalSubnet{}, err
	}
	if source == "" {
		source = store.ExternalSubnetSourceManual
	}
	if !validExternalSource(source) {
		return ExternalSubnet{}, fmt.Errorf("ipam: invalid external subnet source %q", source)
	}
	now := s.now().Unix()
	row := store.ExternalSubnet{
		ID: store.NewULID(), CIDR: network, Label: label, Source: source,
		Description: description, CreatedBy: createdBy, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.external.Insert(ctx, row); err != nil {
		return ExternalSubnet{}, fmt.Errorf("ipam: creating external subnet: %w", err)
	}
	return toExternalSubnet(row), nil
}

// UpdateExternalSubnet rewrites an external subnet's mutable fields. An empty
// cidr/source leaves that field unchanged; a supplied cidr is re-validated
// and canonicalized. Returns store.ErrNotFound if the record is gone.
func (s *Service) UpdateExternalSubnet(ctx context.Context, id, cidr, label, source, description string) (ExternalSubnet, error) {
	if s.external == nil {
		return ExternalSubnet{}, fmt.Errorf("ipam: external subnets are not enabled")
	}
	row, err := s.external.Get(ctx, id)
	if err != nil {
		return ExternalSubnet{}, fmt.Errorf("ipam: getting external subnet %s for update: %w", id, err)
	}
	if strings.TrimSpace(cidr) != "" {
		network, cerr := normalizeExternalCIDR(cidr)
		if cerr != nil {
			return ExternalSubnet{}, cerr
		}
		row.CIDR = network
	}
	row.Label = label
	row.Description = description
	if source != "" {
		if !validExternalSource(source) {
			return ExternalSubnet{}, fmt.Errorf("ipam: invalid external subnet source %q", source)
		}
		row.Source = source
	}
	row.UpdatedAt = s.now().Unix()
	if err := s.external.Update(ctx, row); err != nil {
		return ExternalSubnet{}, fmt.Errorf("ipam: updating external subnet %s: %w", id, err)
	}
	return toExternalSubnet(row), nil
}

// DeleteExternalSubnet removes an external subnet record (idempotent, per the
// store repo's convention).
func (s *Service) DeleteExternalSubnet(ctx context.Context, id string) error {
	if s.external == nil {
		return fmt.Errorf("ipam: external subnets are not enabled")
	}
	if err := s.external.Delete(ctx, id); err != nil {
		return fmt.Errorf("ipam: deleting external subnet %s: %w", id, err)
	}
	return nil
}

// externalSubnetRows builds the GET /ipam/subnets rows for every external
// subnet record: source "external", readOnly (they carry no PVE-IPAM
// allocation set — they are pure records, reserved/released only via the
// dedicated CRUD routes, never ipam.alloc.* ops). Total is derived from the
// CIDR; allocated/observed stay zero (nothing in PVE to enumerate).
func (s *Service) externalSubnetRows(ctx context.Context) []Subnet {
	if s.external == nil {
		return nil
	}
	rows, err := s.external.List(ctx)
	if err != nil {
		// A store read failing for the external rows must not blank the whole
		// subnet list (SDN/bridge rows still render) — matches the per-source
		// tolerance sdnSubnets/allocationsByCIDR already apply.
		return nil
	}
	out := make([]Subnet, 0, len(rows))
	for _, row := range rows {
		total, _, _ := subnetAddrCount(row.CIDR)
		out = append(out, Subnet{
			CIDR:     row.CIDR,
			Source:   "external",
			ReadOnly: true,
			Total:    total,
		})
	}
	return out
}
