package change

// IfaceUpdateParams is op "iface.update" (docs/data-model.md §3's `iface`
// group): mtu, comments, addresses, gateway, autostart. Target is the
// interface being updated — typically a physnic (bonds/bridges/vlans have
// their own dedicated *.update ops with the same shape of fields, since
// they carry additional type-specific settings iface.update doesn't need
// to know about). Fields are pointers so a partial update (only some
// fields present) is distinguishable from "clear this field to its zero
// value" — an absent field leaves the current value untouched, a present
// field (even `null`, which decodes to a nil pointer of the *slice*
// element, e.g. an explicit `"addresses": null`) means "set it".
//
// RemoveAddress/RemoveGateway (T-703) explicitly clear the stanza's
// address/gateway options — the wire counterparts of internal/change/
// ifaces.IfaceUpdate's same-named fields (which ifaces.DecodeOp has decoded
// under exactly these JSON names since T-204; this package's strict Op
// decoder just never admitted them until the dedicated-management-VLAN flow
// needed "take the address and default route OFF the old carrier" as a
// changeset op). Each is only honored when its value-setting sibling is
// absent (Addresses/Gateway nil), mirroring ifaces.mutateIfaceUpdate's own
// precedence.
type IfaceUpdateParams struct {
	MTU           *int      `json:"mtu,omitempty"`
	Comments      *string   `json:"comments,omitempty"`
	Addresses     *[]string `json:"addresses,omitempty"`
	Gateway       *string   `json:"gateway,omitempty"`
	Autostart     *bool     `json:"autostart,omitempty"`
	RemoveAddress bool      `json:"removeAddress,omitempty"`
	RemoveGateway bool      `json:"removeGateway,omitempty"`
}

func (IfaceUpdateParams) isChangeParams() {}

// IfaceRawReplaceParams is op "iface.raw.replace" (docs/features/
// change-management.md §7): the raw editor's save. Content is the entire
// new /etc/network/interfaces text for Target's node, applied wholesale
// (internal/change/ifaces.IfaceRawReplace) rather than as an AST patch.
// BaseHash is the sha256 hex digest of the file's content as read when the
// editor was opened (GET /nodes/{node}/interfaces/raw's "sha256" field,
// round-tripped verbatim) — the conflict guard: Service compares it against
// the live file's current hash at validate time, and a mismatch (someone
// else changed the file since this editor session opened it) produces a
// blocking finding instead of silently clobbering the intervening edit. An
// empty BaseHash skips the check (accepted for programmatic/test callers
// that don't have a prior read to compare against; the web editor always
// sends one).
type IfaceRawReplaceParams struct {
	Content  string `json:"content"`
	BaseHash string `json:"baseHash,omitempty"`
}

func (IfaceRawReplaceParams) isChangeParams() {}
