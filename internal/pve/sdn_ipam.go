// SPDX-License-Identifier: Apache-2.0

package pve

import (
	"context"
	"fmt"
)

// SDN IPAM plugin-instance write path (T-3104), mirroring sdn_fabric.go's
// structure: like fabrics/controllers, an ipam instance's writes stage a
// pending edit only — realized by the same PUT /cluster/sdn this package's
// ApplySDN already issues (internal/change/apply_sdn.go never gives ipam
// instances a bespoke apply path — see planning/reports/
// T-3101-followup-01.md for why vnprox does not take the `--lock-token`
// this create call also accepts, the same discipline sdn_fabric.go/
// sdn_controller.go already follow).
//
// This file adds the write methods to the read-only IPAM type ipam.go
// already defines (ListIPAMs/GetIPAMStatus), rather than introducing a
// parallel SDNIpam type/ListSDNIpams name the way sdn_fabric.go/
// sdn_controller.go each introduce their own new type: ipam.go's read side
// predates T-3104 and already named the type/methods IPAM/ListIPAMs, so
// splitting one PVE object family across two Go names (IPAM for reads,
// SDNIpam for writes) would be worse than the naming mismatch with this
// file's own filename.
//
// Production wiring (cmd/vnproxd's pveGateway.SDNStageOp) constructs the
// concrete external-IPAM HTTP client (internal/ipam/netbox.go,
// internal/ipam/phpipam.go) from a netbox/phpipam-type IPAM value read back
// via ListIPAMs — except Token, which (per this package's ipam.go doc
// comment) a read never returns. See internal/ipam/netbox.go's package doc
// comment for how T-3104 item 3 handles that honestly rather than
// fabricating a workaround.

// CreateIPAM calls POST /cluster/sdn/ipams. Deliberately does not send
// --lock-token — see this file's package doc comment.
func (c *Client) CreateIPAM(ctx context.Context, ip IPAM) error {
	return c.do(ctx, "POST", "/cluster/sdn/ipams", requestParams{body: ip}, nil)
}

// UpdateIPAM calls PUT /cluster/sdn/ipams/{ipam}.
func (c *Client) UpdateIPAM(ctx context.Context, id string, ip IPAM) error {
	path := fmt.Sprintf("/cluster/sdn/ipams/%s", id)
	return c.do(ctx, "PUT", path, requestParams{body: ip}, nil)
}

// DeleteIPAM calls DELETE /cluster/sdn/ipams/{ipam}.
func (c *Client) DeleteIPAM(ctx context.Context, id string) error {
	path := fmt.Sprintf("/cluster/sdn/ipams/%s", id)
	return c.do(ctx, "DELETE", path, requestParams{}, nil)
}

// ListIPAMsRunning calls GET /cluster/sdn/ipams?running=1: the last-applied
// (realized) ipam instance set, for the same staged-vs-running diff
// ListSDNFabrics/ListSDNControllers' own running variants support
// elsewhere in this package.
func (c *Client) ListIPAMsRunning(ctx context.Context) ([]IPAM, error) {
	var out []IPAM
	if err := c.do(ctx, "GET", "/cluster/sdn/ipams", requestParams{query: runningQuery}, &out); err != nil {
		return nil, err
	}
	return out, nil
}
