// SPDX-License-Identifier: Apache-2.0

package spec

import "fmt"

// Version is the only Spec schema version this package produces and accepts.
// It is emitted as the document's top-level `specVersion` field and checked
// on import (docs/data-model.md §5). A future breaking schema change bumps
// this; additive fields do not.
const Version = 1

// Spec is one declarative cluster network document (docs/data-model.md §5).
//
// Field order below IS the serialized YAML field order and is load-bearing:
// two Marshal calls over identical live state must be byte-identical, so the
// document is a tree of typed structs (never a map[string]any) and every
// slice Export builds is sorted by a stable key. There is intentionally no
// timestamp — a churning field would defeat the git-diff property; callers
// get freshness from the changeset/commit metadata, not the document.
//
// The struct fields are ordered for a readable, stable schema (identity
// first), not for memory packing, so govet's fieldalignment check is
// suppressed on the spec value types — the serialization contract outranks
// struct padding here.
//
//nolint:govet // fieldalignment: field order is the stable YAML schema, deliberately not memory-optimized.
type Spec struct {
	SpecVersion int        `yaml:"specVersion"`
	Nodes       []NodeSpec `yaml:"nodes,omitempty"`
	SDN         *SDNSpec   `yaml:"sdn,omitempty"`
}

// NodeSpec is one cluster node's declared node-local network intent.
//
//nolint:govet // fieldalignment: field order is the stable YAML schema.
type NodeSpec struct {
	Name    string       `yaml:"name"`
	Bonds   []BondSpec   `yaml:"bonds,omitempty"`
	Bridges []BridgeSpec `yaml:"bridges,omitempty"`
	VLANs   []VLANSpec   `yaml:"vlans,omitempty"`
}

// BondSpec is a Linux bond's declared config (the fields
// change.BondCreateParams/BondUpdateParams can actually set and
// inventory.Bond exposes a declared baseline for; Comments/MIIMon are not
// diffable and so are omitted, mirroring blueprint's adapters.go).
//
//nolint:govet // fieldalignment: field order is the stable YAML schema.
type BondSpec struct {
	Name           string   `yaml:"name"`
	Mode           string   `yaml:"mode,omitempty"`
	Slaves         []string `yaml:"slaves,omitempty"`
	LACPRate       string   `yaml:"lacpRate,omitempty"`
	XmitHashPolicy string   `yaml:"xmitHashPolicy,omitempty"`
	MTU            int      `yaml:"mtu,omitempty"`
}

// BridgeSpec is a Linux bridge's declared config. Vids are rendered as their
// inventory.VidRange string forms ("100", "2-4094"), sorted, so the document
// stays readable and diffs stably.
//
//nolint:govet // fieldalignment: field order is the stable YAML schema.
type BridgeSpec struct {
	Name      string   `yaml:"name"`
	Ports     []string `yaml:"ports,omitempty"`
	VlanAware bool     `yaml:"vlanAware,omitempty"`
	Vids      []string `yaml:"vids,omitempty"`
	Addresses []string `yaml:"addresses,omitempty"`
	Gateway   string   `yaml:"gateway,omitempty"`
	MTU       int      `yaml:"mtu,omitempty"`
	STP       bool     `yaml:"stp,omitempty"`
	Comments  string   `yaml:"comments,omitempty"`
}

// VLANSpec is a plain 802.1q VLAN sub-interface's declared config. Parent
// and Vid form the interface's identity and are always emitted.
//
//nolint:govet // fieldalignment: field order is the stable YAML schema.
type VLANSpec struct {
	Name      string   `yaml:"name"`
	Parent    string   `yaml:"parent"`
	Vid       int      `yaml:"vid"`
	Addresses []string `yaml:"addresses,omitempty"`
	MTU       int      `yaml:"mtu,omitempty"`
}

// SDNSpec is the cluster-scoped SDN portion of the document.
//
//nolint:govet // fieldalignment: field order is the stable YAML schema.
type SDNSpec struct {
	Zones   []ZoneSpec   `yaml:"zones,omitempty"`
	Vnets   []VnetSpec   `yaml:"vnets,omitempty"`
	Subnets []SubnetSpec `yaml:"subnets,omitempty"`
}

// ZoneSpec is a cluster-scoped SDN zone's declared config.
//
//nolint:govet // fieldalignment: field order is the stable YAML schema.
type ZoneSpec struct {
	ID         string   `yaml:"id"`
	Type       string   `yaml:"type"`
	Bridge     string   `yaml:"bridge,omitempty"`
	Controller string   `yaml:"controller,omitempty"`
	IPAM       string   `yaml:"ipam,omitempty"`
	Nodes      []string `yaml:"nodes,omitempty"`
	ExitNodes  []string `yaml:"exitNodes,omitempty"`
	Peers      []string `yaml:"peers,omitempty"`
	VrfVxlan   int      `yaml:"vrfVxlan,omitempty"`
	MTU        int      `yaml:"mtu,omitempty"`
}

// VnetSpec is a cluster-scoped SDN vnet's declared config. ID is the
// "zone/vnet" path (inventory.SdnVnet.Ref.ID).
//
//nolint:govet // fieldalignment: field order is the stable YAML schema.
type VnetSpec struct {
	ID        string `yaml:"id"`
	Zone      string `yaml:"zone"`
	Alias     string `yaml:"alias,omitempty"`
	Tag       int    `yaml:"tag,omitempty"`
	VlanAware bool   `yaml:"vlanAware,omitempty"`
}

// SubnetSpec is a cluster-scoped SDN subnet's declared config. ID is the
// subnet CIDR (inventory.SdnSubnet.Ref.ID).
//
//nolint:govet // fieldalignment: field order is the stable YAML schema.
type SubnetSpec struct {
	ID            string   `yaml:"id"`
	Vnet          string   `yaml:"vnet"`
	Gateway       string   `yaml:"gateway,omitempty"`
	DNSZonePrefix string   `yaml:"dnsZonePrefix,omitempty"`
	DHCPRanges    []string `yaml:"dhcpRanges,omitempty"`
	SNAT          bool     `yaml:"snat,omitempty"`
}

// validateVersion rejects a parsed Spec whose specVersion this package does
// not understand — a document with no specVersion (0) or a future one is a
// hard error, not silently reconciled against, so an operator never applies
// a spec written for a schema this daemon can't fully honor.
func validateVersion(s Spec) error {
	if s.SpecVersion != Version {
		return fmt.Errorf("spec: unsupported specVersion %d (this daemon supports %d)", s.SpecVersion, Version)
	}
	return nil
}
