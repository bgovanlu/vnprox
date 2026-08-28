// SPDX-License-Identifier: Apache-2.0

package switchdrv

import (
	"context"
	"fmt"
	"sort"
)

// OpenConfig is the vnprox-first switch driver: it maps a bounded PortConfig
// to and from the OpenConfig models a gNMI-capable switch exposes
// (openconfig-interfaces / openconfig-vlan / openconfig-lacp). The path/payload
// construction here is pure and unit-tested; the gNMI wire transport against
// real hardware is deliberately abstracted behind Transport and is a
// needs-hardware-validation item — a nil Transport (the default) yields
// ErrTransportUnavailable, so a daemon with no real switch reachability degrades
// cleanly rather than pretending to push.
//
// Only three OpenConfig subtrees are ever touched — VLAN membership, the port
// description, and LACP — matching PortConfig exactly. There is no code path
// here that constructs any other OpenConfig path, so the "no full-config push"
// scoping is structural, not merely conventional.
type OpenConfigDriver struct {
	transport Transport
}

// Transport is the gNMI Set/Get surface OpenConfigDriver rides on. It is the
// single seam a real gNMI client (or a hardware-in-the-loop harness) plugs
// into; internal/switchmock does not use it (it implements SwitchDriver
// directly for a faster in-memory double).
type Transport interface {
	// SetPortOpenConfig issues a gNMI Set of the OpenConfig update payload for
	// port.
	SetPortOpenConfig(ctx context.Context, port string, update map[string]any) error
	// GetPortOpenConfig issues a gNMI Get of port's interface/vlan/lacp subtree.
	GetPortOpenConfig(ctx context.Context, port string) (map[string]any, error)
	// GetPortNeighbor reads port's LLDP neighbor via the switch's
	// openconfig-lldp model.
	GetPortNeighbor(ctx context.Context, port string) (Neighbor, error)
}

// NewOpenConfigDriver builds an OpenConfigDriver over transport. A nil
// transport is permitted (every method then returns ErrTransportUnavailable),
// so cmd/vnproxd can wire the driver dark until a real gNMI client exists.
func NewOpenConfigDriver(transport Transport) *OpenConfigDriver {
	return &OpenConfigDriver{transport: transport}
}

// buildOpenConfigUpdate renders cfg as the OpenConfig Set payload for port. It
// is pure (no I/O) and exhaustively unit-tested — the mapping is the load-
// bearing part of this driver, independent of the gNMI transport.
func buildOpenConfigUpdate(port string, cfg PortConfig) map[string]any {
	tagged := append([]int(nil), cfg.Tagged...)
	sort.Ints(tagged)

	vlanTrunk := make([]int, 0, len(tagged))
	for _, v := range tagged {
		if v != cfg.Untagged {
			vlanTrunk = append(vlanTrunk, v)
		}
	}

	ethSwitchedVLAN := map[string]any{
		"config": map[string]any{
			"interface-mode": vlanInterfaceMode(cfg),
			"native-vlan":    cfg.Untagged,
			"trunk-vlans":    vlanTrunk,
		},
	}

	return map[string]any{
		"openconfig-interfaces:interface": []any{
			map[string]any{
				"name": port,
				"config": map[string]any{
					"name":        port,
					"description": cfg.Description,
				},
				"openconfig-if-ethernet:ethernet": map[string]any{
					"openconfig-vlan:switched-vlan": ethSwitchedVLAN,
					"config": map[string]any{
						"openconfig-if-aggregate:aggregate-id": "",
					},
				},
			},
		},
		"openconfig-lacp:lacp": map[string]any{
			"interfaces": map[string]any{
				"interface": []any{
					map[string]any{
						"name": port,
						"config": map[string]any{
							"name":         port,
							"lacp-mode":    string(cfg.LACP.Mode),
							"interval":     lacpInterval(cfg.LACP.Rate),
							"lacp-enabled": cfg.LACP.Mode != LACPOff && cfg.LACP.Mode != "",
						},
					},
				},
			},
		},
	}
}

// vlanInterfaceMode picks the OpenConfig interface-mode for cfg: TRUNK when the
// port carries tagged VLANs, ACCESS otherwise.
func vlanInterfaceMode(cfg PortConfig) string {
	for _, v := range cfg.Tagged {
		if v != cfg.Untagged {
			return "TRUNK"
		}
	}
	return "ACCESS"
}

// lacpInterval maps an LACPRate to the OpenConfig lacp interval enum.
func lacpInterval(r LACPRate) string {
	if r == LACPRateFast {
		return "FAST"
	}
	return "SLOW"
}

// PortConfig reads port's config via the transport's OpenConfig Get.
func (d *OpenConfigDriver) PortConfig(ctx context.Context, port string) (PortConfig, error) {
	if d.transport == nil {
		return PortConfig{}, ErrTransportUnavailable
	}
	raw, err := d.transport.GetPortOpenConfig(ctx, port)
	if err != nil {
		return PortConfig{}, fmt.Errorf("switchdrv: openconfig get for port %s: %w", port, err)
	}
	return parseOpenConfigPort(raw)
}

// SetPortConfig writes cfg to port via the transport's OpenConfig Set.
func (d *OpenConfigDriver) SetPortConfig(ctx context.Context, port string, cfg PortConfig) error {
	if d.transport == nil {
		return ErrTransportUnavailable
	}
	return d.transport.SetPortOpenConfig(ctx, port, buildOpenConfigUpdate(port, cfg))
}

// PortNeighbor reads port's LLDP neighbor via the transport.
func (d *OpenConfigDriver) PortNeighbor(ctx context.Context, port string) (Neighbor, error) {
	if d.transport == nil {
		return Neighbor{}, ErrTransportUnavailable
	}
	return d.transport.GetPortNeighbor(ctx, port)
}

// Close is a no-op for the transport-injected driver (the transport owns its
// own connection lifecycle).
func (d *OpenConfigDriver) Close() error { return nil }

// parseOpenConfigPort is buildOpenConfigUpdate's inverse over the subtree this
// driver itself writes (a real device may return a superset; unrelated leaves
// are ignored). It exists so a round-trip through a Transport double is lossless
// for the three bounded attribute groups.
func parseOpenConfigPort(raw map[string]any) (PortConfig, error) {
	var cfg PortConfig
	ifaces, _ := raw["openconfig-interfaces:interface"].([]any)
	if len(ifaces) > 0 {
		if m, ok := ifaces[0].(map[string]any); ok {
			if c, ok := m["config"].(map[string]any); ok {
				cfg.Description, _ = c["description"].(string)
			}
			if eth, ok := m["openconfig-if-ethernet:ethernet"].(map[string]any); ok {
				if sw, ok := eth["openconfig-vlan:switched-vlan"].(map[string]any); ok {
					if c, ok := sw["config"].(map[string]any); ok {
						cfg.Untagged = toInt(c["native-vlan"])
						cfg.Tagged = toIntSlice(c["trunk-vlans"])
					}
				}
			}
		}
	}
	if lacp, ok := raw["openconfig-lacp:lacp"].(map[string]any); ok {
		if ifs, ok := lacp["interfaces"].(map[string]any); ok {
			if list, ok := ifs["interface"].([]any); ok && len(list) > 0 {
				if m, ok := list[0].(map[string]any); ok {
					if c, ok := m["config"].(map[string]any); ok {
						if mode, ok := c["lacp-mode"].(string); ok && mode != "" {
							cfg.LACP.Mode = LACPMode(mode)
						}
						if iv, ok := c["interval"].(string); ok && iv == "FAST" {
							cfg.LACP.Rate = LACPRateFast
						} else if ok {
							cfg.LACP.Rate = LACPRateSlow
						}
					}
				}
			}
		}
	}
	return cfg, nil
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func toIntSlice(v any) []int {
	list, ok := v.([]any)
	if !ok {
		if ints, ok := v.([]int); ok {
			return ints
		}
		return nil
	}
	out := make([]int, 0, len(list))
	for _, e := range list {
		out = append(out, toInt(e))
	}
	return out
}
