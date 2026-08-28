// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/telemetry"
	"github.com/bgovanlu/vnprox/internal/telemetrycollector"
)

func TestRun_Help(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"help"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "vnproxtelemetryd") {
		t.Errorf("stdout = %q, want usage text", stdout.String())
	}
}

func TestRun_UnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"not-a-real-subcommand"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected an error for an unknown subcommand")
	}
}

func TestRun_ReportOnEmptyStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "telemetry.db")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"report", "--db", dbPath}, &stdout, &stderr); err != nil {
		t.Fatalf("run report: %v (stderr: %s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "nothing has arrived yet") {
		t.Errorf("report on an empty store = %q, want it to say nothing has arrived", stdout.String())
	}
}

func TestRun_ReportAfterInsert(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "telemetry.db")
	seedOneSubmission(t, dbPath)

	var stdout, stderr bytes.Buffer
	if err := run([]string{"report", "--db", dbPath, "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("run report --json: %v (stderr: %s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"totalSubmissions": 1`) {
		t.Errorf("report --json = %s, want totalSubmissions: 1", stdout.String())
	}
	if !strings.Contains(stdout.String(), "pve-manager/9.2.4") {
		t.Errorf("report --json = %s, want the seeded pveVersion", stdout.String())
	}
}

func TestRun_RetentionRunDeletesOldRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "telemetry.db")
	seedOneSubmission(t, dbPath)

	var stdout, stderr bytes.Buffer
	// The seeded row is from 2020; a 1h window makes it eligible for
	// deletion immediately, demonstrating retention without waiting for a
	// real window to elapse.
	if err := run([]string{"retention-run", "--db", dbPath, "--window", "1h"}, &stdout, &stderr); err != nil {
		t.Fatalf("run retention-run: %v (stderr: %s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "deleted=1") {
		t.Errorf("retention-run output = %q, want deleted=1", stdout.String())
	}

	var reportOut bytes.Buffer
	if err := run([]string{"report", "--db", dbPath}, &reportOut, &stderr); err != nil {
		t.Fatalf("run report after retention: %v", err)
	}
	if !strings.Contains(reportOut.String(), "nothing has arrived yet") {
		t.Errorf("report after retention = %q, want the store to be empty", reportOut.String())
	}
}

func TestRun_RevokeDeletesByInstallID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "telemetry.db")
	installID := seedOneSubmission(t, dbPath)

	var stdout, stderr bytes.Buffer
	if err := run([]string{"revoke", "--db", dbPath, "--install-id", installID}, &stdout, &stderr); err != nil {
		t.Fatalf("run revoke: %v (stderr: %s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "deleted 1 submission") {
		t.Errorf("revoke output = %q, want it to say one submission was deleted", stdout.String())
	}

	var reportOut bytes.Buffer
	if err := run([]string{"report", "--db", dbPath}, &reportOut, &stderr); err != nil {
		t.Fatalf("run report after revoke: %v", err)
	}
	if !strings.Contains(reportOut.String(), "nothing has arrived yet") {
		t.Errorf("report after revoke = %q, want the store to be empty", reportOut.String())
	}
}

func TestRun_RevokeRequiresInstallID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "telemetry.db")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"revoke", "--db", dbPath}, &stdout, &stderr); err == nil {
		t.Fatal("expected an error when --install-id is omitted")
	}
}

// seedOneSubmission opens dbPath, inserts one sample submission dated in
// 2020 (so retention tests can use a short window without a controllable
// clock), and returns its install-id.
func seedOneSubmission(t *testing.T, dbPath string) string {
	t.Helper()
	ctx := t.Context()
	store, err := telemetrycollector.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	const installID = "01HZY0Z1QW8V9N7M3K5R2T4B6D"
	payload := telemetry.Payload{
		PayloadVersion: telemetry.PayloadVersion,
		InstallID:      installID,
		VnproxVersion:  "3.0.3",
		PVEVersion:     "pve-manager/9.2.4",
		Kernel:         "6.8.12-4-pve",
		NICPCIIDs:      []string{"0x8086:0x1521"},
		NodeCount:      2,
		Suite:          "hardware",
		Checks: []telemetry.CheckVerdict{
			{ID: "drift.config_vs_live", Status: "pass", DurationMS: 100},
		},
	}

	if err := store.Insert(ctx, payload, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	return installID
}
