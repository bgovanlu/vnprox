// SPDX-License-Identifier: Apache-2.0

package change

// VidRange is an inclusive VLAN ID range (a single VID has Low == High),
// the wire counterpart of internal/inventory.VidRange for op params (that
// type has no JSON tags of its own, and this package intentionally keeps
// its wire types independent of the inventory package's in-memory ones —
// see doc.go).
type VidRange struct {
	Low  int `json:"low"`
	High int `json:"high"`
}
