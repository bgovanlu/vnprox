// Package switchmock is T-1205's in-memory SwitchDriver test double: a single
// simulated switch whose per-port config and LLDP neighbor are scriptable, and
// which records every write it receives so a test can assert exactly zero
// writes reached it (e.g. after a pre-write identity-check abort — T-1205 AC4).
// It implements internal/switchdrv.SwitchDriver directly (no gNMI transport),
// mirroring the internal/pvemock role for switch hardware vnprox has no live
// access to. Real OpenConfig/gNMI behavior is a needs-hardware-validation item.
package switchmock

import (
	"context"
	"fmt"
	"sync"

	"github.com/bgovanlu/vnprox/internal/switchdrv"
)

// Switch is one simulated switch. It is safe for concurrent use.
type Switch struct {
	ports       map[string]switchdrv.PortConfig
	neighbors   map[string]switchdrv.Neighbor
	writes      []Write
	mu          sync.Mutex
	unreachable bool
}

// Write records one SetPortConfig call the switch received.
type Write struct {
	Port   string
	Config switchdrv.PortConfig
}

// New builds an empty simulated switch.
func New() *Switch {
	return &Switch{
		ports:     map[string]switchdrv.PortConfig{},
		neighbors: map[string]switchdrv.Neighbor{},
	}
}

// SetPort seeds a port's current config.
func (s *Switch) SetPort(port string, cfg switchdrv.PortConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ports[port] = cfg
}

// SetNeighbor seeds a port's live LLDP neighbor (what the switch reports
// seeing). A test simulates a moved cable by seeding a neighbor that differs
// from the one an op was scoped against.
func (s *Switch) SetNeighbor(port string, n switchdrv.Neighbor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.neighbors[port] = n
}

// SetUnreachable toggles whole-switch reachability.
func (s *Switch) SetUnreachable(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unreachable = v
}

// Writes returns a copy of every SetPortConfig call received so far.
func (s *Switch) Writes() []Write {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Write(nil), s.writes...)
}

// CurrentPort returns a port's current stored config.
func (s *Switch) CurrentPort(port string) switchdrv.PortConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ports[port]
}

// --- switchdrv.SwitchDriver ---

func (s *Switch) PortConfig(_ context.Context, port string) (switchdrv.PortConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unreachable {
		return switchdrv.PortConfig{}, fmt.Errorf("switchmock: switch unreachable")
	}
	cfg, ok := s.ports[port]
	if !ok {
		return switchdrv.PortConfig{}, fmt.Errorf("switchmock: no such port %q", port)
	}
	return cfg, nil
}

func (s *Switch) SetPortConfig(_ context.Context, port string, cfg switchdrv.PortConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unreachable {
		return fmt.Errorf("switchmock: switch unreachable")
	}
	s.ports[port] = cfg
	s.writes = append(s.writes, Write{Port: port, Config: cfg})
	return nil
}

func (s *Switch) PortNeighbor(_ context.Context, port string) (switchdrv.Neighbor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unreachable {
		return switchdrv.Neighbor{}, fmt.Errorf("switchmock: switch unreachable")
	}
	return s.neighbors[port], nil
}

func (s *Switch) Close() error { return nil }
