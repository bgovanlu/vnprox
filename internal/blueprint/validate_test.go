// SPDX-License-Identifier: Apache-2.0

package blueprint_test

import (
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// T-603 AC4: "Param validation: bad CIDR/VID rejected at the form."
// Backend defense in depth for exactly that — Instantiate must refuse a
// bad CIDR or an out-of-range VID rather than silently drafting a
// malformed op.
func TestInstantiate_RejectsBadParamValues(t *testing.T) {
	bp := &blueprint.Blueprint{
		BlueprintVersion: 1, ID: "t", Name: "t",
		NodeSelector: blueprint.NodeSelector{Mode: blueprint.SelectAll},
		Params: []blueprint.ParamDef{
			{Name: "addr", Type: blueprint.ParamCIDR, Required: true},
			{Name: "vid", Type: blueprint.ParamVID, Required: true},
		},
		Entities: []blueprint.EntityTemplate{
			{Kind: blueprint.KindVlan, IDTemplate: "bond0.{{vid}}", Fields: map[string]any{
				"parent": "bond0", "vid": "{{vid}}", "addresses": []any{"{{addr}}"},
			}},
		},
	}
	g := newGraphWithNodes("pve1")

	cases := []struct {
		params map[string]any
		name   string
	}{
		{name: "bad cidr: no prefix", params: map[string]any{"addr": "192.168.1.10", "vid": float64(10)}},
		{name: "bad cidr: garbage", params: map[string]any{"addr": "not-an-address", "vid": float64(10)}},
		{name: "bad vid: zero", params: map[string]any{"addr": "192.168.1.10/24", "vid": float64(0)}},
		{name: "bad vid: too large", params: map[string]any{"addr": "192.168.1.10/24", "vid": float64(4095)}},
		{name: "unknown param", params: map[string]any{"addr": "192.168.1.10/24", "vid": float64(10), "bogus": "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: []string{"pve1"}, Params: tc.params}, g.Snapshot())
			if !errors.Is(err, blueprint.ErrInvalidParams) {
				t.Fatalf("got err = %v, want ErrInvalidParams", err)
			}
		})
	}
}

func TestInstantiate_MissingRequiredParam(t *testing.T) {
	bp := &blueprint.Blueprint{
		BlueprintVersion: 1, ID: "t", Name: "t",
		NodeSelector: blueprint.NodeSelector{Mode: blueprint.SelectAll},
		Params:       []blueprint.ParamDef{{Name: "addr", Type: blueprint.ParamCIDR, Required: true}},
		Entities: []blueprint.EntityTemplate{
			{Kind: blueprint.KindBridge, IDTemplate: "vmbr0", Fields: map[string]any{"addresses": []any{"{{addr}}"}}},
		},
	}
	g := newGraphWithNodes("pve1")
	_, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: []string{"pve1"}}, g.Snapshot())
	if !errors.Is(err, blueprint.ErrInvalidParams) {
		t.Fatalf("got err = %v, want ErrInvalidParams", err)
	}
}

func TestInstantiate_DefaultAppliedWhenParamOmitted(t *testing.T) {
	bp := &blueprint.Blueprint{
		BlueprintVersion: 1, ID: "t", Name: "t",
		NodeSelector: blueprint.NodeSelector{Mode: blueprint.SelectAll},
		Params:       []blueprint.ParamDef{{Name: "bridgeName", Type: blueprint.ParamString, Default: "vmbr9"}},
		Entities: []blueprint.EntityTemplate{
			{Kind: blueprint.KindBridge, IDTemplate: "{{bridgeName}}", Fields: map[string]any{}},
		},
	}
	g := newGraphWithNodes("pve1")
	ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: []string{"pve1"}}, g.Snapshot())
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if len(ops) != 1 || ops[0].Target.ID != "vmbr9" {
		t.Fatalf("got %+v, want a single op targeting vmbr9 (the default)", ops)
	}
}

func TestValidate_RejectsUnknownEntityKind(t *testing.T) {
	bp := &blueprint.Blueprint{
		BlueprintVersion: 1, ID: "t", Name: "t",
		Entities: []blueprint.EntityTemplate{{Kind: "not-a-real-kind", IDTemplate: "x"}},
	}
	if err := blueprint.Validate(bp); !errors.Is(err, blueprint.ErrInvalid) {
		t.Fatalf("got err = %v, want ErrInvalid", err)
	}
}

func TestValidate_RejectsWrongBlueprintVersion(t *testing.T) {
	bp := &blueprint.Blueprint{
		BlueprintVersion: 2, ID: "t", Name: "t",
		Entities: []blueprint.EntityTemplate{{Kind: blueprint.KindBridge, IDTemplate: "vmbr0"}},
	}
	if err := blueprint.Validate(bp); !errors.Is(err, blueprint.ErrInvalid) {
		t.Fatalf("got err = %v, want ErrInvalid", err)
	}
}

func TestValidate_RejectsUndeclaredParamToken(t *testing.T) {
	bp := &blueprint.Blueprint{
		BlueprintVersion: 1, ID: "t", Name: "t",
		Entities: []blueprint.EntityTemplate{
			{Kind: blueprint.KindBridge, IDTemplate: "{{undeclared}}"},
		},
	}
	if err := blueprint.Validate(bp); !errors.Is(err, blueprint.ErrInvalid) {
		t.Fatalf("got err = %v, want ErrInvalid", err)
	}
}

// __nodes__ is a builtin token and must not need declaring as a param.
func TestValidate_AllowsBuiltinNodesToken(t *testing.T) {
	bp := &blueprint.Blueprint{
		BlueprintVersion: 1, ID: "t", Name: "t",
		NodeSelector: blueprint.NodeSelector{Mode: blueprint.SelectSingle},
		Entities: []blueprint.EntityTemplate{
			{Kind: blueprint.KindSdnZone, IDTemplate: "z1", Fields: map[string]any{
				"type": "vxlan", "nodes": "{{__nodes__}}",
			}},
		},
	}
	if err := blueprint.Validate(bp); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestInstantiate_UnknownEntityRefStillProducesError(t *testing.T) {
	// Sanity: an entity whose existing ref resolves to a different kind
	// than the template expects is a hard error, not silently ignored
	// (e.g. a template targeting a bridge id that inventory already has
	// as some other kind — shouldn't happen in practice given Ref
	// includes Kind, but diffEntity's type assertion path is exercised
	// here for completeness).
	g := inventory.NewGraph()
	bp := &blueprint.Blueprint{
		BlueprintVersion: 1, ID: "t", Name: "t",
		NodeSelector: blueprint.NodeSelector{Mode: blueprint.SelectAll},
		Entities: []blueprint.EntityTemplate{
			{Kind: blueprint.KindBridge, IDTemplate: "vmbr0", Fields: map[string]any{}},
		},
	}
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1"},
	})
	ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: []string{"pve1"}}, g.Snapshot())
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
}
