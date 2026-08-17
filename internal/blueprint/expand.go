package blueprint

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// expandedEntity is one concrete entity a blueprint expands to: an
// identity (Ref), the template Kind that produced it (for adapters.go's
// kind dispatch), and its fully-substituted Fields.
type expandedEntity struct {
	fields map[string]any
	ref    inventory.Ref
	kind   Kind
}

// expand walks bp.Entities in order, expanding each across the node
// selector in effect (the entity's own override, or bp's blueprint-level
// default), substituting {{param}} placeholders with bindings.
func expand(bp *Blueprint, bindings map[string]any, nodes []string) ([]expandedEntity, error) {
	var out []expandedEntity
	for i, et := range bp.Entities {
		sel := bp.NodeSelector
		if et.NodeSelector != nil {
			sel = *et.NodeSelector
		}
		switch sel.Mode {
		case SelectAll:
			for _, node := range nodes {
				e, err := expandOne(et, bindings, node)
				if err != nil {
					return nil, fmt.Errorf("entities[%d] (%s) on node %s: %w", i, et.Kind, node, err)
				}
				out = append(out, e)
			}
		case SelectSingle, "":
			e, err := expandOne(et, bindings, "")
			if err != nil {
				return nil, fmt.Errorf("entities[%d] (%s): %w", i, et.Kind, err)
			}
			out = append(out, e)
		default:
			return nil, fmt.Errorf("entities[%d] (%s): unknown node selector mode %q", i, et.Kind, sel.Mode)
		}
	}
	return out, nil
}

func expandOne(et EntityTemplate, bindings map[string]any, node string) (expandedEntity, error) {
	fields, err := substituteFields(et.Fields, bindings)
	if err != nil {
		return expandedEntity{}, err
	}
	idAny, err := substituteValue(et.IDTemplate, bindings)
	if err != nil {
		return expandedEntity{}, fmt.Errorf("idTemplate: %w", err)
	}
	id, ok := idAny.(string)
	if !ok {
		id = stringify(idAny)
	}
	if id == "" {
		return expandedEntity{}, fmt.Errorf("idTemplate resolved to an empty id")
	}
	ref, err := refFor(et.Kind, node, id)
	if err != nil {
		return expandedEntity{}, err
	}
	return expandedEntity{ref: ref, kind: et.Kind, fields: fields}, nil
}

// refFor builds the inventory.Ref a Kind/node/id combination identifies.
// The cluster-scoped SDN kinds ignore node (their Ref.Node is always "" —
// docs/data-model.md §1: "Node is empty for cluster-scoped entities");
// passing a non-empty node for one is a template authoring error, not a
// runtime one, so it is rejected here rather than silently dropped.
func refFor(kind Kind, node, id string) (inventory.Ref, error) {
	switch kind {
	case KindBridge:
		return inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: id}, nil
	case KindBond:
		return inventory.Ref{Kind: inventory.KindBond, Node: node, ID: id}, nil
	case KindVlan:
		return inventory.Ref{Kind: inventory.KindVlan, Node: node, ID: id}, nil
	case KindSdnZone:
		if node != "" {
			return inventory.Ref{}, fmt.Errorf("sdn-zone is cluster-scoped and cannot use nodeSelector.mode=all")
		}
		return inventory.Ref{Kind: inventory.KindSDNZone, ID: id}, nil
	case KindSdnVnet:
		if node != "" {
			return inventory.Ref{}, fmt.Errorf("sdn-vnet is cluster-scoped and cannot use nodeSelector.mode=all")
		}
		return inventory.Ref{Kind: inventory.KindSDNVnet, ID: id}, nil
	case KindSdnSubnet:
		if node != "" {
			return inventory.Ref{}, fmt.Errorf("sdn-subnet is cluster-scoped and cannot use nodeSelector.mode=all")
		}
		return inventory.Ref{Kind: inventory.KindSDNSubnet, ID: id}, nil
	case KindSdnController:
		if node != "" {
			return inventory.Ref{}, fmt.Errorf("sdn-controller is cluster-scoped and cannot use nodeSelector.mode=all")
		}
		return inventory.Ref{Kind: inventory.KindSDNController, ID: id}, nil
	default:
		return inventory.Ref{}, fmt.Errorf("unknown entity kind %q", kind)
	}
}
