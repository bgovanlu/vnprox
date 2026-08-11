// export.go is the card's "close and export produces one artifact — the
// timeline plus a support bundle — through the existing redaction path".
//
// "Through the existing redaction path" is implemented literally: this file
// maps a Timeline onto backup.BundleIncident and hands it to backup.Bundle,
// which stages it like every other entry, declares every field in
// bundleschema.go, and scrubs it with the same redactor. There is no second
// archive writer, no second manifest, and no second idea of what is safe —
// which is why T-1902's own AC1 scan can run over this artifact unchanged
// (internal/backup's bundleProducers table).

package incident

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bgovanlu/vnprox/internal/backup"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// Bundler is the export's one seam. backup.Bundle satisfies it via
// BundlerFunc; a test substitutes a recorder without writing an archive.
type Bundler interface {
	Bundle(ctx context.Context, opts backup.BundleOptions) (*backup.BundleResult, error)
}

// BundlerFunc adapts a function to Bundler — cmd/vnproxd wires
// incident.BundlerFunc(backup.Bundle).
type BundlerFunc func(ctx context.Context, opts backup.BundleOptions) (*backup.BundleResult, error)

// Bundle implements Bundler.
func (f BundlerFunc) Bundle(ctx context.Context, opts backup.BundleOptions) (*backup.BundleResult, error) {
	return f(ctx, opts)
}

// ExportOptions configures one export.
//
// The support-bundle configuration itself (store path, config path, log
// source, ...) lives in Config.ExportBase rather than here: it is a property
// of the daemon, known once at the composition root, and internal/api has no
// business assembling it per request.
type ExportOptions struct {
	// OutDir is where the archive is written; empty means a fresh temporary
	// directory, which is what the HTTP route uses (it streams the file and
	// removes the directory).
	OutDir string
}

// ExportResult is a produced artifact.
//
//nolint:govet // fieldalignment: a small result struct read top-to-bottom by humans.
type ExportResult struct {
	Path     string
	Filename string
	Bytes    int64
	Timeline *Timeline
}

// ExportName is the artifact's filename.
//
// Deliberately not `vnprox-backup-…` and not `vnprox-support-…`: the first
// would put it in reach of backup retention (backup.BundleName's own comment
// explains why that matters), and the second would make an incident export
// indistinguishable from a plain support bundle in a directory listing, which
// is the one thing an operator attaching it to a ticket needs to see.
func ExportName(incidentID string, t time.Time) string {
	return fmt.Sprintf("vnprox-incident-%s-%s.tar.gz", incidentID, t.UTC().Format("20060102-150405"))
}

// Export assembles the incident's timeline and produces one artifact.
//
// It works on a closed incident and an open one alike: an export is a
// snapshot of the view at the moment it was taken, and refusing to produce
// one before the incident is closed would mean the operator who most needs to
// send it — mid-incident, asking for help — cannot.
func (s *Service) Export(ctx context.Context, id string, opts ExportOptions) (*ExportResult, error) {
	if s.cfg.Bundler == nil {
		return nil, ErrExportUnavailable
	}
	tl, err := s.Timeline(ctx, id)
	if err != nil {
		return nil, err
	}

	outDir := opts.OutDir
	if outDir == "" {
		dir, mkErr := os.MkdirTemp("", "vnprox-incident-export-")
		if mkErr != nil {
			return nil, fmt.Errorf("incident: preparing an export directory for %s: %w", id, mkErr)
		}
		outDir = dir
	}

	name := ExportName(id, s.cfg.Now())
	bundleOpts := s.cfg.ExportBase
	bundleOpts.Incident = bundleIncident(tl)
	bundleOpts.Dest = filepath.Join(outDir, name)
	bundleOpts.DryRun = false

	res, err := s.cfg.Bundler.Bundle(ctx, bundleOpts)
	if err != nil {
		return nil, fmt.Errorf("incident: exporting %s: %w", id, err)
	}
	s.cfg.Logger.Info("incident exported", "incidentId", id, "path", res.Path, "bytes", res.Bytes)
	return &ExportResult{Path: res.Path, Filename: filepath.Base(res.Path), Bytes: res.Bytes, Timeline: tl}, nil
}

// bundleIncident maps a Timeline onto the export document.
//
// Two deliberate losses, both stated in bundleincident.go and both asserted
// by tests rather than only described:
//
//   - Per-field before/after VALUES of a diff entry are dropped; only the
//     names of the changed options survive. An interfaces(5) option value is
//     exactly where a WireGuard private key lives, and the bundle's own
//     host-network collector protects that file with an allowlist a diff
//     arriving by another route would walk straight past.
//   - Event ids are dropped. They are a rendering/ordering detail of the live
//     view, not something a reader of an archive can resolve.
func bundleIncident(tl *Timeline) *backup.BundleIncident {
	doc := &backup.BundleIncident{
		ID:            tl.Incident.ID,
		Title:         tl.Incident.Title,
		Status:        tl.Incident.Status,
		OpenedBy:      tl.Incident.OpenedBy,
		OpenedAt:      tl.Incident.OpenedAt,
		StartedAt:     tl.Incident.StartedAt,
		EndedAt:       tl.Incident.EndedAt,
		ClosedAt:      tl.Incident.ClosedAt,
		Retroactive:   tl.Incident.Retroactive,
		WindowFrom:    tl.Window.From,
		WindowTo:      tl.Window.To,
		WindowLive:    tl.Window.Live,
		EventCount:    len(tl.Events),
		Events:        make([]backup.BundleIncidentEvent, 0, len(tl.Events)),
		Sources:       make([]backup.BundleIncidentSource, 0, len(tl.Sources)),
		Caveats:       append([]string{}, tl.Caveats...),
		DiffError:     tl.DiffError,
		DiffErrorCode: tl.DiffErrorCode,
	}
	for _, e := range tl.Events {
		doc.Events = append(doc.Events, backup.BundleIncidentEvent{
			At: e.At, Source: string(e.Source), Kind: e.Kind, Summary: e.Summary,
			Actor: e.Actor, Node: e.Node, Ref: e.Ref, Result: e.Result,
			ChangesetID: e.ChangesetID, CaptureID: e.CaptureID,
		})
	}
	for _, st := range tl.Sources {
		doc.Sources = append(doc.Sources, backup.BundleIncidentSource{
			Source: string(st.Source), Status: st.Status, Count: st.Count, Detail: st.Detail,
		})
	}
	if tl.Diff != nil {
		doc.Diff = bundleDiff(tl.Diff)
	}
	return doc
}

func bundleDiff(d *change.TopologyDiff) *backup.BundleIncidentDiff {
	out := &backup.BundleIncidentDiff{
		FromAt:         d.From.At,
		ToAt:           d.To.At,
		FromSnapshotID: d.From.SnapshotID,
		ToSnapshotID:   d.To.SnapshotID,
		Added:          len(d.Added),
		Removed:        len(d.Removed),
		Modified:       len(d.Modified),
		Unattributed:   d.Unattributed,
		ComparedPaths:  append([]string{}, d.Coverage.Paths...),
		OmittedPaths:   append([]string{}, d.Coverage.OmittedPaths...),
		Entries:        []backup.BundleIncidentDiffEntry{},
	}
	for _, n := range d.Coverage.UnmatchedNodes {
		out.UnmatchedNodes = append(out.UnmatchedNodes,
			fmt.Sprintf("%s (captured only in %s)", n.Node, n.PresentIn))
	}
	for _, group := range [][]topology.EntityDiff{d.Added, d.Removed, d.Modified} {
		for _, e := range group {
			out.Entries = append(out.Entries, bundleDiffEntry(e))
		}
	}
	return out
}

func bundleDiffEntry(e topology.EntityDiff) backup.BundleIncidentDiffEntry {
	entry := backup.BundleIncidentDiffEntry{
		Change:      string(e.Change),
		Ref:         e.Ref,
		Kind:        e.Kind,
		Node:        e.Node,
		Name:        e.Name,
		Attributed:  e.Attribution.Attributed,
		ChangesetID: e.Attribution.ChangesetID,
		Actor:       e.Attribution.Actor,
		Fields:      make([]string, 0, len(e.Fields)),
	}
	for _, f := range e.Fields {
		// The NAME only. See this function's caller for why the value never
		// travels.
		entry.Fields = append(entry.Fields, f.Field)
	}
	return entry
}
