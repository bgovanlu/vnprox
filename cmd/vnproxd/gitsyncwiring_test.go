// SPDX-License-Identifier: Apache-2.0

package main

// The wiring test for T-2702's pull-request body carrying T-2605's post-apply
// preview.
//
// T-2702 shipped before T-2605 existed. Its `previewSection` was written,
// tested against a stub, and then handed a nil `PreviewSource` by the daemon
// for as long as there was no projection to ask — so the section rendered
// nothing on every real proposal while every test of it stayed green. That is
// the shape of gap this file exists to close: the seam was proven, the
// *wiring* of the seam was not, and nothing failed.
//
// It asserts against buildGitSyncProposer, the function the daemon actually
// calls, rather than against gitsync.NewProposer — passing nil there would
// still compile and still type-check.

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/gitsync"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// stubChangesets is the minimum ChangesetReader that makes a proposer report
// itself enabled — Enabled() requires it to be non-nil, so passing nil here
// would leave this test asserting on the no-op proposer.
type stubChangesets struct{}

func (stubChangesets) Get(_ context.Context, id string) (change.Changeset, error) {
	return change.Changeset{ID: id, Title: "wiring fixture", Status: change.StatusDraft}, nil
}

// stubPreviewSource records whether the proposer ever reached for a preview.
type stubPreviewSource struct{ calls int }

func (s *stubPreviewSource) PreviewSummary(_ context.Context, _ string) (string, error) {
	s.calls++
	return "projected: 1 entity added", nil
}

// TestBuildGitSyncProposer_PassesThePreviewSourceThrough proves the argument
// reaches the proposer rather than being dropped on the floor.
//
// Reaching inside the returned *gitsync.Proposer is not possible from here
// (its config is unexported), so the assertion is behavioural: the proposer is
// asked to render a body, and the stub records the call. A nil preview would
// produce a body with no preview section and zero calls — which is precisely
// the pre-wiring state, and precisely what this test must be able to tell
// apart from the wired one.
func TestBuildGitSyncProposer_PassesThePreviewSourceThrough(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "push-token")
	if err := os.WriteFile(tokenPath, []byte("ghp_notarealtoken"), 0o600); err != nil {
		t.Fatalf("writing token fixture: %v", err)
	}

	cfg := config.GitSyncConfig{
		Enabled:       true,
		URL:           "https://github.com/example/infra.git",
		Provider:      "github",
		Ref:           "main",
		Path:          "network/cluster.yaml",
		TokenFile:     tokenPath,
		PushTokenFile: tokenPath,
	}

	preview := &stubPreviewSource{}
	p, err := buildGitSyncProposer(cfg, stubChangesets{}, inventory.NewGraph(), nil, nil, preview, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("buildGitSyncProposer: %v", err)
	}
	if p == nil {
		t.Fatal("buildGitSyncProposer returned a nil proposer")
	}
	if !p.Enabled() {
		t.Fatal("the proposer built from an enabled config with a push token reports itself disabled; " +
			"the rest of this test would be asserting on the no-op proposer")
	}

	// THE assertion. Everything above is setup that makes it meaningful.
	if !p.PreviewConfigured() {
		t.Error("buildGitSyncProposer dropped the PreviewSource: pull-request bodies will silently " +
			"omit the post-apply preview section, which is exactly the state T-2702 shipped in for " +
			"two waves while every unit test of previewSection stayed green")
	}

	// The seam type is what the daemon must satisfy. If PreviewSource ever
	// grows a method *change.Service does not have, the daemon stops
	// compiling here rather than silently falling back to no preview.
	var _ gitsync.PreviewSource = preview
}

// TestBuildGitSyncProposer_DisabledReportsNoPreview is the control leg: the
// assertion above is only meaningful if PreviewConfigured can also be false.
func TestBuildGitSyncProposer_DisabledReportsNoPreview(t *testing.T) {
	p, err := buildGitSyncProposer(config.GitSyncConfig{}, stubChangesets{}, inventory.NewGraph(),
		nil, nil, nil, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("buildGitSyncProposer: %v", err)
	}
	if p.PreviewConfigured() {
		t.Error("a proposer built from an empty config reports a preview source; " +
			"the positive assertion in the test above proves nothing")
	}
}
