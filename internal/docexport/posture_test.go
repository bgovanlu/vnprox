// SPDX-License-Identifier: Apache-2.0

package docexport_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/docexport"
	"github.com/bgovanlu/vnprox/internal/posture"
)

// curatedPostureReport is T-1607 AC4's fixture: a qualified score with one
// not-evaluated factor and a multi-point trend, exercising the score line, the
// factor table (including an n/a sub-score), and the trend sparkline.
func curatedPostureReport() docexport.PostureReport {
	return docexport.PostureReport{
		Latest: posture.Posture{
			Overall:    69,
			Qualified:  true,
			ComputedAt: 1_700_000_000,
			Factors: []posture.Factor{
				{Name: posture.FactorSPOF, Weight: 30, Value: 2, ScorePct: 90, Contribution: 31.7, Evaluated: true, Detail: "2 SPOFs"},
				{Name: posture.FactorSegmentation, Weight: 25, Value: 0.75, ScorePct: 25, Contribution: 7.35, Evaluated: true, Detail: "1 of 4 segmented"},
				{Name: posture.FactorExposedPorts, Weight: 20, Value: 1, ScorePct: 90, Contribution: 21.1, Evaluated: true, Detail: "1 exposed port"},
				{Name: posture.FactorAnomalyRate, Weight: 15, ScorePct: posture.NotEvaluatedScore, Evaluated: false, Caveat: "anomaly rate not evaluated: cold start"},
				{Name: posture.FactorDriftHygiene, Weight: 10, Value: 1.5, ScorePct: 77, Contribution: 9.05, Evaluated: true, Detail: "3 open drift findings"},
			},
		},
		Trend: []docexport.PostureTrendPoint{
			{ComputedAt: 1_699_000_000, Overall: 60},
			{ComputedAt: 1_699_500_000, Overall: 65},
			{ComputedAt: 1_700_000_000, Overall: 69},
		},
	}
}

func TestPostureExport_MarkdownGolden(t *testing.T) {
	md := docexport.PostureMarkdown(curatedPostureReport())

	for _, section := range []string{docexport.HeadingPosture, docexport.HeadingPostureFactors, docexport.HeadingPostureTrend} {
		if !strings.Contains(md, "## "+section) {
			t.Errorf("markdown missing section heading %q", section)
		}
	}
	if !strings.Contains(md, "Overall: 69 / 100") {
		t.Errorf("markdown missing overall score line")
	}
	if !strings.Contains(md, "partial") {
		t.Errorf("markdown missing the partial/qualified honesty note")
	}
	// Every named factor appears in the table.
	for _, name := range []string{posture.FactorSPOF, posture.FactorSegmentation, posture.FactorExposedPorts, posture.FactorAnomalyRate, posture.FactorDriftHygiene} {
		if !strings.Contains(md, name) {
			t.Errorf("markdown factor table missing %q", name)
		}
	}
	// The not-evaluated factor renders "n/a", never a misleading number.
	if !strings.Contains(md, "n/a") {
		t.Errorf("markdown missing n/a for the not-evaluated factor")
	}
	// The caveat (honesty channel) is surfaced.
	if !strings.Contains(md, "cold start") {
		t.Errorf("markdown missing the not-evaluated factor's caveat")
	}
	// Trend points render.
	if !strings.Contains(md, "| 60 |") || !strings.Contains(md, "| 69 |") {
		t.Errorf("markdown trend table missing point rows")
	}
}

func TestPostureExport_HTMLGoldenStandalone(t *testing.T) {
	htmlDoc := docexport.PostureHTML(curatedPostureReport())

	for _, section := range []string{docexport.HeadingPosture, docexport.HeadingPostureFactors, docexport.HeadingPostureTrend} {
		if !strings.Contains(htmlDoc, ">"+section+"<") {
			t.Errorf("html missing section heading %q", section)
		}
	}
	if !strings.Contains(htmlDoc, "Overall: 69 / 100") {
		t.Errorf("html missing overall score line")
	}
	if !strings.Contains(htmlDoc, "<svg") {
		t.Errorf("html missing embedded trend <svg> sparkline")
	}
	if !strings.Contains(htmlDoc, "n/a") {
		t.Errorf("html missing n/a for the not-evaluated factor")
	}

	// CSP-style standalone check (mirrors T-605 AC3): no external resource
	// references, no <script>, no <link>.
	if m := externalRefRE.FindString(htmlDoc); m != "" {
		t.Errorf("html contains an external resource reference: %q", m)
	}
	if strings.Contains(htmlDoc, "<script") {
		t.Errorf("html must not contain any <script> tag")
	}
	if strings.Contains(htmlDoc, "<link") {
		t.Errorf("html must not contain any <link> tag")
	}
}
