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
type IfaceUpdateParams struct {
	MTU       *int      `json:"mtu,omitempty"`
	Comments  *string   `json:"comments,omitempty"`
	Addresses *[]string `json:"addresses,omitempty"`
	Gateway   *string   `json:"gateway,omitempty"`
	Autostart *bool     `json:"autostart,omitempty"`
}

func (IfaceUpdateParams) isChangeParams() {}
