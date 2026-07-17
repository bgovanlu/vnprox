package config

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/flow"
)

func TestLoad_FlowsDefaults_AllDisabled(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"

[pve]
api_url = "https://127.0.0.1:8006"
`
	path := writeTemp(t, "no-flows.toml", toml)

	cfg, err := Load(path, discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if cfg.Flows.SFlowEnabled || cfg.Flows.NetFlowEnabled || cfg.Flows.IPFIXEnabled {
		t.Errorf("expected every flow listener disabled by default, got %+v", cfg.Flows)
	}
	if cfg.Flows.SFlowPort != flow.DefaultSFlowPort {
		t.Errorf("SFlowPort = %d, want default %d", cfg.Flows.SFlowPort, flow.DefaultSFlowPort)
	}
	if cfg.Flows.NetFlowPort != flow.DefaultNetFlowPort {
		t.Errorf("NetFlowPort = %d, want default %d", cfg.Flows.NetFlowPort, flow.DefaultNetFlowPort)
	}
	if cfg.Flows.IPFIXPort != flow.DefaultIPFIXPort {
		t.Errorf("IPFIXPort = %d, want default %d", cfg.Flows.IPFIXPort, flow.DefaultIPFIXPort)
	}
	if cfg.Flows.RetentionMinutes != flow.DefaultRetentionMinutes {
		t.Errorf("RetentionMinutes = %d, want default %d", cfg.Flows.RetentionMinutes, flow.DefaultRetentionMinutes)
	}
	if cfg.Flows.MaxRows != flow.DefaultMaxRows {
		t.Errorf("MaxRows = %d, want default %d", cfg.Flows.MaxRows, flow.DefaultMaxRows)
	}
}

func TestLoad_FlowsOverride(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"

[pve]
api_url = "https://127.0.0.1:8006"

[flows]
sflow_enabled = true
netflow_enabled = true
ipfix_enabled = false
sflow_port = 16343
netflow_port = 12055
ipfix_port = 14739
retention_minutes = 30
max_rows = 500000
`
	path := writeTemp(t, "flows.toml", toml)

	cfg, err := Load(path, discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if !cfg.Flows.SFlowEnabled || !cfg.Flows.NetFlowEnabled || cfg.Flows.IPFIXEnabled {
		t.Errorf("enabled flags = %+v, want sflow+netflow on, ipfix off", cfg.Flows)
	}
	if cfg.Flows.SFlowPort != 16343 || cfg.Flows.NetFlowPort != 12055 || cfg.Flows.IPFIXPort != 14739 {
		t.Errorf("ports = %+v, want overridden values", cfg.Flows)
	}
	if cfg.Flows.RetentionMinutes != 30 {
		t.Errorf("RetentionMinutes = %d, want 30", cfg.Flows.RetentionMinutes)
	}
	if cfg.Flows.MaxRows != 500000 {
		t.Errorf("MaxRows = %d, want 500000", cfg.Flows.MaxRows)
	}
}
