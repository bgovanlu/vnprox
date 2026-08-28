// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"log/slog"
	"os"
	"sort"

	"github.com/BurntSushi/toml"
)

// ErrDemoRealEndpoint is the named error for T-2801 AC3's first direction:
// demo mode cannot be enabled against a real PVE endpoint.
//
// A demo daemon that had been pointed at a production cluster would show a
// demo banner over real data and refuse real changes with a "would have"
// message — an operator reading that screen would be wrong about what their
// cluster is doing, in both directions at once. That is strictly worse than
// having no demo mode, so this is a refusal to start, not a warning.
var ErrDemoRealEndpoint = fmt.Errorf("%w: demo mode cannot be enabled against a configured PVE endpoint", ErrInvalidConfig)

// demoPVEKeys are the [pve] keys whose presence means "this config names a
// PVE endpoint or an identity to reach one with". Listed by their TOML
// spelling because that is what an operator sees and what the error has to
// name back to them.
//
// The whole section is covered rather than api_url alone. A config that
// sets only token_file still declares an identity minted against a real
// cluster, and a demo daemon reading it would either use it (the failure
// this exists to prevent) or silently ignore it (a config file whose
// contents are a lie). Refusing is the only honest option.
var demoPVEKeys = []string{
	"api_url",
	"token_file",
	"dev_ticket_username",
	"dev_ticket_password",
	"dev_ticket_realm",
}

// LoadDemo loads path as the configuration for a demo daemon
// (`vnproxd --demo`, T-2801).
//
// It differs from Load in exactly two ways, and both are the point:
//
//  1. It REFUSES, with ErrDemoRealEndpoint, any config that configures a
//     PVE endpoint. This is AC3's "demo mode cannot be enabled against a
//     real PVE endpoint", enforced before a single collector is built.
//  2. It sets Config.Demo, which is what turns on the API's write refusal
//     and the UI's banner.
//
// The PVE endpoint a demo daemon actually uses is supplied afterwards by
// the caller (cmd/vnproxd, from internal/demo) — never from a file.
func LoadDemo(path string, logger *slog.Logger) (*Config, error) {
	if logger == nil {
		logger = slog.Default()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading demo config file %s: %w", path, err)
	}
	return loadDemoBytes(data, path, logger)
}

// LoadDemoBytes is LoadDemo over a document already in memory. It exists
// for the zero-argument `vnproxd --demo` path, which synthesizes its own
// config, and for tests.
func LoadDemoBytes(data []byte, path string, logger *slog.Logger) (*Config, error) {
	if logger == nil {
		logger = slog.Default()
	}
	return loadDemoBytes(data, path, logger)
}

func loadDemoBytes(data []byte, path string, logger *slog.Logger) (*Config, error) {
	if err := refuseConfiguredPVEEndpoint(data, path); err != nil {
		return nil, err
	}
	cfg, err := loadBytes(data, path, logger)
	if err != nil {
		return nil, err
	}
	cfg.Demo = true
	// Blanked, not left at its default. loadBytes fills PVE.APIURL with
	// DefaultPVEAPIURL (https://127.0.0.1:8006) when the file says nothing,
	// and that default is a real, dialable address — on a Proxmox node it is
	// pveproxy itself. A demo daemon must not carry it even unused: the
	// caller overwrites this section with internal/demo's unresolvable
	// address, and a zero value here means a caller that forgot to gets an
	// immediate "APIURL is required" from pve.New rather than a client
	// pointed at the local hypervisor.
	cfg.PVE = PVEConfig{}
	return cfg, nil
}

// refuseConfiguredPVEEndpoint reports ErrDemoRealEndpoint if data sets any
// [pve] key.
//
// It re-decodes rather than inspecting the resolved Config because by then
// the defaults have been applied and "the operator wrote api_url" is
// indistinguishable from "nobody wrote anything". The distinction is the
// entire check.
func refuseConfiguredPVEEndpoint(data []byte, path string) error {
	var raw rawConfig
	meta, err := toml.Decode(string(data), &raw)
	if err != nil {
		// Let loadBytes produce the parse error, with its own message.
		return nil
	}
	var set []string
	for _, key := range demoPVEKeys {
		if meta.IsDefined("pve", key) {
			set = append(set, "pve."+key)
		}
	}
	if len(set) == 0 {
		return nil
	}
	sort.Strings(set)
	return fmt.Errorf("%w: %s sets %v; a demo daemon runs against the embedded synthetic cluster and must not be given a way to reach a real one — remove the [pve] section, or start without --demo",
		ErrDemoRealEndpoint, path, set)
}
