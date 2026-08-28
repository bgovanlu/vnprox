// SPDX-License-Identifier: Apache-2.0

package change

// T-3104 SDN IPAM plugin-instance op params, mirroring params_sdn_fabric.go
// and params_sdn_controller.go's field-set/doc-comment density and
// pointer-field Update convention. This is the configured IPAM *plugin
// object* itself (its connection config), not an allocation — see
// op.go's OpSdnIpamCreate doc comment. A plugin instance's Target carries
// its cluster-scoped identity (Ref{Kind: KindSDNIpam, ID: "<ipamID>"}, ID
// matching the captured `--ipam` pattern `[a-zA-Z][a-zA-Z0-9]*[a-zA-Z0-9]` —
// planning/reports/evidence/pve-9.2.4-sdn-schema.txt).
//
// Type is conditional-schema, the same "one struct, schema-validated
// combination" choice SdnFabricCreateParams/SdnControllerCreateParams
// already make — except, unlike those two, the capture's `pvesh usage
// create /cluster/sdn/ipams` gives no "Conditional options:" breakdown per
// type at all: it lists --fingerprint/--ipam/--lock-token/--section/
// --token/--type/--url as one flat parameter list with no per-type
// grouping (real PVE's `pve` built-in plugin plausibly needs none of
// url/token/section/fingerprint, since it has nothing external to connect
// to, while `netbox`/`phpipam` plausibly need at least url+token to reach
// their API — but this is this task's own inference from each plugin's
// stated purpose, not a fact read off the capture the way Fabric's
// per-protocol field list was). validate_schema.go's conditional arm
// enforces this inferred combination (pve: none of url/token/section/
// fingerprint set; netbox/phpipam: url and token both required, section/
// fingerprint optional), documented there as an inference rather than a
// captured fact, and pvemock's own sdnIpamTypeError (internal/pvemock/
// sdn_ipam.go) enforces the identical rule server-side so the two can never
// quietly disagree — the same discipline sdnFabricProtocolError/
// sdnControllerTypeError already keep with their own validators.
//
// --lock-token is deliberately never sent — see params_sdn_fabric.go's doc
// comment and planning/reports/T-3101-followup-01.md for why.

// SdnIpamCreateParams is op "sdn.ipam.create".
type SdnIpamCreateParams struct {
	Type        string `json:"type"`
	URL         string `json:"url,omitempty"`
	Token       string `json:"token,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Section     int    `json:"section,omitempty"`
}

func (SdnIpamCreateParams) isChangeParams() {}

// SdnIpamUpdateParams is op "sdn.ipam.update": a partial update. Type is
// NOT editable — an assumption, not a confirmed fact: the capture has no
// `pvesh usage ... -v`'s `set` form for this path at all (only `get`/
// `create`), so whether real PVE's own PUT even allows changing an ipam
// plugin's type is unconfirmed. This mirrors SdnFabricUpdateParams'
// Protocol immutability and SdnControllerUpdateParams' Type immutability on
// the same reasoning: changing type changes which of every other field is
// even legal, and create+delete already covers "I want a different plugin
// type" the same way it does for a fabric/controller, pending hardware
// validation of the real `set` usage block. Token has no separate "clear"
// affordance beyond setting it to the empty string on the wire (unlike a
// pointer-vs-omitted distinction elsewhere in this file) because — per this
// package's own doc comment on the write-only-secret question — vnprox
// cannot read an existing token back to diff against in the first place;
// an update always sends whatever the caller's form currently holds.
type SdnIpamUpdateParams struct {
	URL         *string `json:"url,omitempty"`
	Token       *string `json:"token,omitempty"`
	Fingerprint *string `json:"fingerprint,omitempty"`
	Section     *int    `json:"section,omitempty"`
}

func (SdnIpamUpdateParams) isChangeParams() {}

// SdnIpamDeleteParams is op "sdn.ipam.delete".
type SdnIpamDeleteParams struct{}

func (SdnIpamDeleteParams) isChangeParams() {}
