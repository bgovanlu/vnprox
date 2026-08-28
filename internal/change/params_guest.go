// SPDX-License-Identifier: Apache-2.0

package change

// GuestNicUpdateParams is op "guest.nic.update" (docs/data-model.md §3:
// "reattach bridge/vnet, vid, rate, firewall flag"; docs/features/
// change-management.md §6 additionally documents disconnect/connect via
// LinkDown). Target is the GuestNic being changed (Ref{Kind: KindGuestNic,
// ...}). BridgeOrVnet names the new bridge/vnet by name (not a full Ref —
// always resolved against the guest's own node); all fields are optional
// so a single op can carry just the one change a UI action produced (e.g.
// bulk reattach only sets BridgeOrVnet, a single firewall toggle only sets
// Firewall).
type GuestNicUpdateParams struct {
	BridgeOrVnet *string `json:"bridgeOrVnet,omitempty"`
	Vid          *int    `json:"vid,omitempty"`
	RateMbps     *int    `json:"rateMbps,omitempty"`
	Firewall     *bool   `json:"firewall,omitempty"`
	LinkDown     *bool   `json:"linkDown,omitempty"`
}

func (GuestNicUpdateParams) isChangeParams() {}
