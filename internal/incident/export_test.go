// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/backup"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// recordingBundler captures the BundleOptions the export builds without
// writing an archive. The archive itself is exercised by
// TestExport_ProducesARealArchive below and, for redaction, by
// internal/backup's own AC1 scan.
type recordingBundler struct {
	opts backup.BundleOptions
	err  error
}

func (r *recordingBundler) Bundle(_ context.Context, opts backup.BundleOptions) (*backup.BundleResult, error) {
	r.opts = opts
	if r.err != nil {
		return nil, r.err
	}
	return &backup.BundleResult{Path: opts.Dest, Bytes: 1234}, nil
}

func TestExport_WithoutABundlerIsRefused(t *testing.T) {
	h := newHarness(t)
	id := openWindow(t, h, 1000, 2000)
	if _, err := h.svc.Export(context.Background(), id, ExportOptions{}); !errors.Is(err, ErrExportUnavailable) {
		t.Errorf("Export without a bundler: err = %v, want ErrExportUnavailable", err)
	}
}

// TestExport_CarriesTheTimelineAndNamesItself asserts the document handed to
// the bundler is this incident's timeline, and that the artifact is named so
// nobody mistakes it for a backup or a plain support bundle.
func TestExport_CarriesTheTimelineAndNamesItself(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	rec := &recordingBundler{}
	h.svc.cfg.Bundler = rec

	h.seedInterleavedHistory()
	h.setNow(5000)
	inc, err := h.svc.Open(ctx, OpenRequest{Title: "the write-up", Actor: "brian@pam", StartedAt: 900, EndedAt: 1500})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, noteErr := h.svc.Annotate(ctx, inc.ID, AnnotateRequest{At: 1045, Body: "pulled the cable"}); noteErr != nil {
		t.Fatalf("Annotate: %v", noteErr)
	}

	res, err := h.svc.Export(ctx, inc.ID, ExportOptions{OutDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	doc := rec.opts.Incident
	if doc == nil {
		t.Fatal("the export handed the bundler no incident document")
	}
	if doc.ID != inc.ID || doc.Title != "the write-up" || doc.Status != "open" {
		t.Errorf("document header = %+v, want this incident's own", doc)
	}
	if doc.WindowFrom != 900 || doc.WindowTo != 1500 || doc.WindowLive {
		t.Errorf("document window = (%d, %d, live=%v), want (900, 1500, false)", doc.WindowFrom, doc.WindowTo, doc.WindowLive)
	}
	if doc.EventCount != 11 || len(doc.Events) != 11 {
		t.Fatalf("document carries %d events (count field %d), want the 11 on the timeline", len(doc.Events), doc.EventCount)
	}
	if len(doc.Sources) != len(Sources()) {
		t.Errorf("document carries %d source statuses, want %d", len(doc.Sources), len(Sources()))
	}
	// The operator's own note is in there — it is the one class of event no
	// other subsystem records, so losing it would gut the artifact.
	found := false
	for _, e := range doc.Events {
		if e.Source == string(SourceAnnotation) && e.Summary == "pulled the cable" {
			found = true
		}
	}
	if !found {
		t.Error("the exported timeline does not carry the operator's annotation")
	}

	if !strings.HasPrefix(res.Filename, "vnprox-incident-") || !strings.HasSuffix(res.Filename, ".tar.gz") {
		t.Errorf("artifact name %q does not identify itself as an incident export", res.Filename)
	}
	if strings.Contains(res.Filename, "vnprox-backup-") {
		t.Errorf("artifact name %q is in reach of backup retention", res.Filename)
	}
	if rec.opts.DryRun {
		t.Error("the export asked for a dry run")
	}
}

// TestExport_DropsDiffFieldValuesButKeepsTheirNames gates the one deliberate
// loss in the mapping. An interfaces(5) option VALUE is where a WireGuard
// private key lives; the bundle's host-network collector protects that file
// with an allowlist, and a diff arriving by another route would walk straight
// past it.
func TestExport_DropsDiffFieldValuesButKeepsTheirNames(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	rec := &recordingBundler{}
	h.svc.cfg.Bundler = rec

	const secret = "SEEDMARK-wireguard-private-key-EXPORTTEST"
	h.diff.result = &change.TopologyDiff{
		Added:   []topology.EntityDiff{},
		Removed: []topology.EntityDiff{},
		Modified: []topology.EntityDiff{{
			Ref: "iface:pve1:wg0", Kind: "iface", Node: "pve1", Name: "wg0",
			Change: topology.DiffModified,
			Fields: []topology.FieldChange{
				{Field: "wireguard-private-key", Before: "oldkeyoldkeyoldkey", After: secret},
				{Field: "mtu", Before: "1500", After: "1420"},
			},
			Attribution: topology.DiffAttribution{Attributed: false},
		}},
		Unattributed: 1,
		Coverage: change.DiffCoverage{
			Nodes: []string{"pve1"}, Paths: []string{"/etc/network/interfaces"},
		},
	}
	h.setNow(5000)
	id := openWindow(t, h, 1000, 2000)

	if _, err := h.svc.Export(ctx, id, ExportOptions{OutDir: t.TempDir()}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	encoded, err := json.Marshal(rec.opts.Incident)
	if err != nil {
		t.Fatalf("marshalling the document: %v", err)
	}
	// Control first: the field NAMES are there, so "the secret is absent" is
	// not the trivial consequence of the diff having been dropped whole.
	for _, name := range []string{"wireguard-private-key", "mtu", "iface:pve1:wg0"} {
		if !bytes.Contains(encoded, []byte(name)) {
			t.Fatalf("CONTROL FAILED: the exported document does not mention %q, so it carries no diff at all "+
				"and the assertion below proves nothing", name)
		}
	}
	for _, value := range []string{secret, "oldkeyoldkeyoldkey", "1420"} {
		if bytes.Contains(encoded, []byte(value)) {
			t.Errorf("the exported document carries the changed VALUE %q; a diff must travel as field names only", value)
		}
	}
	if d := rec.opts.Incident.Diff; d == nil || d.Modified != 1 || d.Unattributed != 1 {
		t.Errorf("diff summary = %+v, want one modified and one unattributed entity", d)
	}
}

// TestExport_ProducesARealArchive runs the whole path — timeline, mapping,
// backup.Bundle, a file on disk — so the option plumbing is exercised rather
// than only the mapping.
func TestExport_ProducesARealArchive(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.svc.cfg.Bundler = BundlerFunc(backup.Bundle)
	h.seedInterleavedHistory()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "vnprox.toml")
	if err := os.WriteFile(configPath, []byte("[server]\nlisten = \"127.0.0.1:0\"\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	h.setNow(5000)
	inc, err := h.svc.Open(ctx, OpenRequest{Title: "real archive", StartedAt: 900, EndedAt: 1500})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, noteErr := h.svc.Annotate(ctx, inc.ID, AnnotateRequest{At: 1000, Body: "an observation"}); noteErr != nil {
		t.Fatalf("Annotate: %v", noteErr)
	}

	h.svc.cfg.ExportBase = backup.BundleOptions{
		ConfigPath: configPath, DBPath: h.dbPath, Node: "pve1", ToolVersion: "test",
		InterfacesPath: filepath.Join(dir, "interfaces"), CorosyncPath: filepath.Join(dir, "corosync.conf"),
		PVEDir: filepath.Join(dir, "pve"), KernelReleasePath: filepath.Join(dir, "osrelease"),
		OSReleasePath: filepath.Join(dir, "os-release"),
		LogSource:     backup.LogSource{Path: filepath.Join(dir, "vnprox.log")},
	}
	res, err := h.svc.Export(ctx, inc.ID, ExportOptions{OutDir: filepath.Join(dir, "out")})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	info, err := os.Stat(res.Path)
	if err != nil {
		t.Fatalf("the exported archive is not on disk: %v", err)
	}
	if info.Size() <= 0 || res.Bytes <= 0 {
		t.Fatalf("the exported archive is empty (%d bytes)", info.Size())
	}
	if res.Timeline == nil || len(res.Timeline.Events) != 11 {
		t.Errorf("the export result does not carry the timeline it wrote")
	}
}
