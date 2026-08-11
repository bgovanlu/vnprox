package main

import (
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/config"
)

// protectedClassesFromConfig converts the [[changesets.protected_class]]
// entries (T-2604) into the change engine's own type at the composition
// root — the one place that knows about both packages, exactly the role
// authServiceAdapter's RequireCap plays for capability names.
//
// It deliberately does NOT validate: change.NewService does, so that a
// mistyped class name fails startup with the op-type vocabulary in hand
// rather than being silently reshaped into something that matches nothing.
func protectedClassesFromConfig(in []config.ProtectedClassConfig) []change.ProtectedClass {
	if len(in) == 0 {
		return nil
	}
	out := make([]change.ProtectedClass, 0, len(in))
	for _, c := range in {
		out = append(out, change.ProtectedClass{Class: c.Class, Approvals: c.Approvals})
	}
	return out
}
