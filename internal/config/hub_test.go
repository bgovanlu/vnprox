package config

import "testing"

// TestLoad_HubTrustUnsigned is T-2904's config-layer assertion: unsigned-trust
// for hub installs is a server config decision, off by default — a config that
// says nothing about it (every production config in existence) gets false, and
// only the literal key turns it on.
func TestLoad_HubTrustUnsigned(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{name: "absent defaults to off", body: "", want: false},
		{name: "empty [hub] section defaults to off", body: "[hub]\n", want: false},
		{name: "explicitly off", body: "[hub]\ntrust_unsigned = false\n", want: false},
		{name: "explicitly on", body: "[hub]\ntrust_unsigned = true\n", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, "vnprox.toml", peerTOML(t, tc.body))
			cfg, err := Load(path, discardLogger())
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Hub.TrustUnsigned != tc.want {
				t.Fatalf("Hub.TrustUnsigned = %v, want %v", cfg.Hub.TrustUnsigned, tc.want)
			}
		})
	}
}
