package plugintemplate

import "github.com/bgovanlu/vnprox/internal/plugin"

// ManifestID is this plugin's stable identity: reverse-DNS style, the same
// convention the SDK's own fixture plugin uses ("com.vnprox.sample",
// internal/plugin/plugintest/samples.go). Renaming it after install orphans
// the previously-installed plugins row — pick it once.
const ManifestID = "com.example.plugintemplate"

// Manifest describes this plugin to plugin.Registry.Install (internal/plugin/
// registry.go): its identity, the frozen v1 SDK version it is built against,
// its transport, the one extension point it attaches to, and the capability
// scope that point's entry ceiling requires.
//
// It declares exactly plugin.ExtFindingProducer and exactly "netRead" — the
// minimum extensionPointMinCap for that point (internal/plugin/caps.go).
// Declaring a broader scope than an implementation needs is refused nowhere
// by the SDK, but it is a bad habit for a plugin author: the scope is what
// GET /audit shows an operator as "what this plugin could touch", so it
// should say only what is true.
func Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:              ManifestID,
		Name:            "Plugin Template",
		Version:         "0.1.0",
		APIVersion:      plugin.APIVersion,
		Transport:       plugin.TransportInProcess,
		ExtensionPoints: []plugin.ExtensionPoint{plugin.ExtFindingProducer},
		Capabilities:    []string{"netRead"},
	}
}

// Registration bundles Manifest with this plugin's concrete FindingProducer —
// the exact shape plugin.Registry.Install expects (internal/plugin/
// manifest.go's Registration type). A real plugin with more than one
// extension point would fill in the matching field for each one here and
// list every point in Manifest.ExtensionPoints; Registration.validate()
// refuses a declared point with no matching implementation, and refuses an
// implementation for a point that isn't declared.
func Registration() plugin.Registration {
	return plugin.Registration{
		Manifest:        Manifest(),
		FindingProducer: NewProducer(),
	}
}
