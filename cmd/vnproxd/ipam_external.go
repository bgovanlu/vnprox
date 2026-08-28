// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// buildExternalIPAMClient is T-3104 item 3's production wiring: construct
// the concrete NetBox/phpIPAM write client from a live, netbox/phpipam-type
// entry in PVE's own `GET /cluster/sdn/ipams` (item 2's sdn.ipam.* op
// family), rather than a separate vnprox-side settings surface — the
// natural integration point the task card's ordering (item 2 before item
// 3) implies, and it reuses PVE's own credential storage instead of
// vnprox needing its own.
//
// It always returns (nil, nil) today, and this is deliberate, not a
// placeholder: internal/pve.IPAM's Token field is populated on
// create/update requests only — a *read* (this function's only tool) never
// gets it back, matching this task's own documented read of the capture
// (internal/pve/sdn_ipam.go's package doc comment) and the honest
// consequence internal/ipam/netbox.go's NewNetBoxClient doc comment flags:
// there is currently no mechanism for vnprox to recover a netbox/phpipam
// instance's token after the moment it was typed into the create/update
// form, short of inventing a vnprox-side secret store — exactly the "don't
// fabricate a workaround" case this task's own brief calls out. So this
// function does the useful half of the wiring (find the configured
// instance, decide netbox vs. phpipam, thread URL/Fingerprint through) and
// then declines to construct a client it knows would only fail every write
// with an authentication error, logging why at info level so the gap is
// discoverable rather than silent. docs/status-matrix.md row 14 records
// this as the reason the write path still reports "not configured" even
// once a netbox/phpipam instance exists in PVE.
//
// phpIPAM additionally needs its own "app id" (phpIPAM's per-application
// API identifier — not a field PVE's ipam config carries at all, since it
// is phpIPAM-side configuration, not a NetBox/PVE concept); with no token
// to use it has no way to authenticate) this function has nothing to read
// it from either, which would be a second, independent blocker for phpIPAM
// specifically even once the token problem is solved.
func buildExternalIPAMClient(ctx context.Context, pveClient *pve.Client, logger *slog.Logger) ipam.ExternalIPAMClient {
	if pveClient == nil {
		return nil
	}
	ipams, err := pveClient.ListIPAMs(ctx)
	if err != nil {
		logger.Warn("external ipam: listing configured ipam plugin instances", "error", err)
		return nil
	}
	for _, ip := range ipams {
		switch ip.Type {
		case "netbox":
			logger.Info("external ipam: found a configured netbox instance, but cannot enable sync writes",
				"ipam", ip.ID, "url", ip.URL,
				"reason", "PVE never returns a configured ipam plugin's token on read; there is no mechanism yet for vnprox to recover it after creation")
			return nil
		case "phpipam":
			logger.Info("external ipam: found a configured phpipam instance, but cannot enable sync writes",
				"ipam", ip.ID, "url", ip.URL,
				"reason", "PVE never returns a configured ipam plugin's token on read (same gap as netbox), and phpIPAM also needs an app id PVE's ipam config has no field for")
			return nil
		}
	}
	return nil
}
