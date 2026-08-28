// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"strings"
	"testing"
)

func TestAnalyze_IPAMPoolForecast(t *testing.T) {
	// A subnet whose allocation climbs steadily toward exhaustion.
	growing := linearAggs("10.0.0.0/24", KindIPAMPool, 21, 50, 2)
	// A subnet that sits flat — must not produce a finding.
	stable := linearAggs("10.0.1.0/24", KindIPAMPool, 21, 30, 0)

	findings := Analyze(append(growing, stable...), 90)
	if len(findings) != 1 {
		t.Fatalf("Analyze returned %d findings, want exactly 1 (only the growing pool)", len(findings))
	}
	f := findings[0]
	if f.Check != CheckIPAMForecast {
		t.Errorf("check = %q, want %q", f.Check, CheckIPAMForecast)
	}
	if f.Ref != "10.0.0.0/24" {
		t.Errorf("ref = %q, want the growing pool 10.0.0.0/24", f.Ref)
	}
	if !strings.Contains(f.Detail, "10.0.0.0/24") || !strings.Contains(f.Detail, "IPAM pool") {
		t.Errorf("detail %q must name the pool", f.Detail)
	}
	wantDate := f.CrossesAt.Format("2006-01-02")
	if !strings.Contains(f.Detail, wantDate) {
		t.Errorf("detail %q must name the projected exhaustion date %s", f.Detail, wantDate)
	}
	if f.HorizonDays != 90 {
		t.Errorf("horizonDays = %d, want 90", f.HorizonDays)
	}
}

func TestAnalyze_LinkForecastCheckAndOrdering(t *testing.T) {
	link := linearAggs("iface:pve1:vmbr1", KindLink, 21, 50, 2)
	pool := linearAggs("10.0.0.0/24", KindIPAMPool, 21, 50, 2)

	findings := Analyze(append(link, pool...), 90)
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2", len(findings))
	}
	// Deterministic order: kind ascending ("ipam_pool" < "link").
	if findings[0].Kind != KindIPAMPool || findings[1].Kind != KindLink {
		t.Errorf("order = [%s, %s], want [ipam_pool, link]", findings[0].Kind, findings[1].Kind)
	}
	if findings[1].Check != CheckLinkForecast {
		t.Errorf("link check = %q, want %q", findings[1].Check, CheckLinkForecast)
	}
	if !strings.Contains(findings[1].Detail, "link iface:pve1:vmbr1") {
		t.Errorf("link detail %q must name the link ref", findings[1].Detail)
	}
}

func TestAnalyze_StableNetworkSilent(t *testing.T) {
	aggs := append(
		linearAggs("iface:pve1:vmbr0", KindLink, 21, 20, 0),
		linearAggs("10.0.2.0/24", KindIPAMPool, 21, 10, 0)...,
	)
	if findings := Analyze(aggs, 90); len(findings) != 0 {
		t.Errorf("Analyze on a flat corpus returned %d findings, want 0", len(findings))
	}
}
