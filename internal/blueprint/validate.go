package blueprint

import (
	"fmt"
	"net"
)

// minVID/maxVID mirror internal/change/validate_schema.go's unexported
// minVID/maxVID (1-4094) — duplicated rather than imported since that
// package doesn't export them and this package's param validation is a
// distinct, earlier check (docs/features/blueprints.md §1: "fill
// parameters (with validation ...)" happens at the param form, before
// expansion even produces ops for the change engine's own schema
// validator to see).
const (
	minVID = 1
	maxVID = 4094
)

// Validate checks a Blueprint's structural shape: version, required
// top-level fields, known entity kinds, known param types, unique param
// names, and that every "{{name}}" token an entity template references
// names a declared param (or the builtin "__nodes__"). It does not
// validate instantiate-time param values — see ResolveParams for that.
func Validate(bp *Blueprint) error {
	if bp.BlueprintVersion != CurrentBlueprintVersion {
		return fmt.Errorf("%w: blueprintVersion %d is not supported (only %d)", ErrInvalid, bp.BlueprintVersion, CurrentBlueprintVersion)
	}
	if bp.ID == "" {
		return fmt.Errorf("%w: id is required", ErrInvalid)
	}
	if bp.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if len(bp.Entities) == 0 {
		return fmt.Errorf("%w: at least one entity is required", ErrInvalid)
	}
	if bp.NodeSelector.Mode != "" && bp.NodeSelector.Mode != SelectAll && bp.NodeSelector.Mode != SelectSingle {
		return fmt.Errorf("%w: nodeSelector.mode %q is not one of all|single", ErrInvalid, bp.NodeSelector.Mode)
	}

	paramNames := map[string]bool{}
	for i, p := range bp.Params {
		if p.Name == "" {
			return fmt.Errorf("%w: params[%d].name is required", ErrInvalid, i)
		}
		if paramNames[p.Name] {
			return fmt.Errorf("%w: duplicate param name %q", ErrInvalid, p.Name)
		}
		paramNames[p.Name] = true
		if !knownParamTypes[p.Type] {
			return fmt.Errorf("%w: params[%d] (%s) has unknown type %q", ErrInvalid, i, p.Name, p.Type)
		}
		if p.AddressSuggest && p.Type != ParamCIDR && p.Type != ParamIP {
			return fmt.Errorf("%w: params[%d] (%s) sets addressSuggest but is not type cidr or ip", ErrInvalid, i, p.Name)
		}
	}

	for i, et := range bp.Entities {
		if !knownKinds[et.Kind] {
			return fmt.Errorf("%w: entities[%d] has unknown kind %q", ErrInvalid, i, et.Kind)
		}
		if et.IDTemplate == "" {
			return fmt.Errorf("%w: entities[%d] (%s) idTemplate is required", ErrInvalid, i, et.Kind)
		}
		if et.NodeSelector != nil && et.NodeSelector.Mode != SelectAll && et.NodeSelector.Mode != SelectSingle {
			return fmt.Errorf("%w: entities[%d] nodeSelector.mode %q is not one of all|single", ErrInvalid, i, et.NodeSelector.Mode)
		}
		if err := validateTokens(et.IDTemplate, paramNames); err != nil {
			return fmt.Errorf("%w: entities[%d] (%s) idTemplate: %w", ErrInvalid, i, et.Kind, err)
		}
		if err := validateFieldTokens(et.Fields, paramNames); err != nil {
			return fmt.Errorf("%w: entities[%d] (%s): %w", ErrInvalid, i, et.Kind, err)
		}
	}
	return nil
}

func validateFieldTokens(fields map[string]any, paramNames map[string]bool) error {
	for k, v := range fields {
		if err := validateValueTokens(v, paramNames); err != nil {
			return fmt.Errorf("fields.%s: %w", k, err)
		}
	}
	return nil
}

func validateValueTokens(v any, paramNames map[string]bool) error {
	switch val := v.(type) {
	case string:
		return validateTokens(val, paramNames)
	case []any:
		for _, e := range val {
			if err := validateValueTokens(e, paramNames); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, e := range val {
			if err := validateValueTokens(e, paramNames); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateTokens(s string, paramNames map[string]bool) error {
	for _, name := range tokenNames(s) {
		if name == builtinNodes {
			continue
		}
		if !paramNames[name] {
			return fmt.Errorf("references undeclared param %q", name)
		}
	}
	return nil
}

// validateParamValue checks one resolved param value against its
// declared ParamType (T-603 AC4: "bad CIDR/VID rejected"). Returns a
// wrapped ErrInvalidParams on failure.
func validateParamValue(def ParamDef, v any) error {
	switch def.Type {
	case ParamString, ParamIface:
		s, ok := v.(string)
		if !ok || (def.Required && s == "") {
			return fmt.Errorf("%w: %s must be a non-empty string", ErrInvalidParams, def.Name)
		}
	case ParamBool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%w: %s must be a boolean", ErrInvalidParams, def.Name)
		}
	case ParamInt:
		if _, err := toInt(v); err != nil {
			return fmt.Errorf("%w: %s must be an integer: %v", ErrInvalidParams, def.Name, err)
		}
	case ParamVID:
		n, err := toInt(v)
		if err != nil {
			return fmt.Errorf("%w: %s must be an integer: %v", ErrInvalidParams, def.Name, err)
		}
		if n < minVID || n > maxVID {
			return fmt.Errorf("%w: %s vid %d is out of range %d-%d", ErrInvalidParams, def.Name, n, minVID, maxVID)
		}
	case ParamVIDList:
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%w: %s must be an array of vids", ErrInvalidParams, def.Name)
		}
		for _, e := range arr {
			n, err := toInt(e)
			if err != nil {
				return fmt.Errorf("%w: %s: %v", ErrInvalidParams, def.Name, err)
			}
			if n < minVID || n > maxVID {
				return fmt.Errorf("%w: %s vid %d is out of range %d-%d", ErrInvalidParams, def.Name, n, minVID, maxVID)
			}
		}
	case ParamCIDR:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("%w: %s must be a CIDR string", ErrInvalidParams, def.Name)
		}
		if _, _, err := net.ParseCIDR(s); err != nil {
			return fmt.Errorf("%w: %s %q is not a valid CIDR: %v", ErrInvalidParams, def.Name, s, err)
		}
	case ParamIP:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("%w: %s must be an IP address string", ErrInvalidParams, def.Name)
		}
		if net.ParseIP(s) == nil {
			return fmt.Errorf("%w: %s %q is not a valid IP address", ErrInvalidParams, def.Name, s)
		}
	case ParamNodeList:
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%w: %s must be an array of node names", ErrInvalidParams, def.Name)
		}
		for _, e := range arr {
			if s, ok := e.(string); !ok || s == "" {
				return fmt.Errorf("%w: %s contains a non-string/empty node name", ErrInvalidParams, def.Name)
			}
		}
	default:
		return fmt.Errorf("%w: %s has unknown type %q", ErrInvalidParams, def.Name, def.Type)
	}
	return nil
}
