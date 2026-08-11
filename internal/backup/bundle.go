// bundle.go is T-1902: `vnproxctl support-bundle`.
//
// One command producing one redacted archive that lets someone diagnose a
// stranger's broken install without SSH.
//
// # What this reuses, and why it is here rather than in a new package
//
// A support bundle is, structurally, T-1901's backup with a harsher
// redaction policy and a narrower scope — so it is the same archive
// container, the same Collector seam, the same Staging area, and the same
// declared secret-class inventory. Concretely:
//
//   - Kind is KindSupportBundle, which the archive reader already accepts
//     and which Restore already REFUSES (ErrWrongKind). A bundle therefore
//     cannot be mistaken for a restorable backup, and that was true before
//     this file existed.
//   - Every entry goes through Staging, so nothing reaches the archive
//     without a recorded digest and an entry the manifest describes.
//   - SecretClasses() is the redaction inventory. It is checked against the
//     live schema by TestSecretClasses_CoverEverySealedColumn, so it cannot
//     rot as the product grows, and AC1's scan is driven from it.
//
// # The three things that make a bundle safe rather than a backup
//
//  1. **It has no store entry.** store/vnprox.db carries the whole audit
//     trail and every sealed credential's ciphertext. A bundle carries
//     derived facts instead — schema version, row counts, the last N
//     redacted changesets — and never the file.
//
//  2. **Its collectors cannot declare a secret.** bundleCollector has no
//     Emits method (see bundlecollect.go). The manifest's secretClasses is
//     therefore empty and includesKeyMaterial false by construction, not by
//     a flag someone might set wrongly.
//
//  3. **Every emitted field is declared or redacted.** bundleschema.go
//     holds the inventory and the reflection walk that enforces it; a field
//     reflection cannot see through (json.RawMessage, any, a map) may not
//     be declared as plainly emitted at all — it has to name a redactor.
//
// # Why --dry-run runs the collectors
//
// AC4 wants dry-run output to match a real run. The way to get that is not
// to write a second function that predicts what the first will do — that is
// two code paths and they diverge. Instead a dry run performs the identical
// collection into an identical 0700 staging directory, builds the plan from
// what Staging actually recorded, and then removes the staging area without
// writing an archive. The plan cannot disagree with the run because it is
// produced by the run.

package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Bundle defaults. Chosen to make an archive an operator can attach to a
// forum post: tens of kilobytes, not megabytes.
const (
	// DefaultBundleChangesets is how many recent changesets a bundle
	// carries. Twenty is roughly "the last week of an active cluster" and
	// comfortably covers "what did you change just before it broke", which
	// is the question a bundle is usually opened to answer.
	DefaultBundleChangesets = 20
	// DefaultBundleFindingEvents is how many finding transitions a bundle
	// carries.
	DefaultBundleFindingEvents = 200
	// DefaultBundleLogLines / DefaultBundleLogBytes bound the log tail from
	// both ends: journalctl is asked for a line count, and whatever comes
	// back is truncated to a byte budget, because a single log line can be
	// arbitrarily long.
	DefaultBundleLogLines = 2000
	DefaultBundleLogBytes = 1 << 20
	// DefaultProbeTimeout bounds every outbound probe. A bundle must be
	// produceable from a node whose peers are all dead without taking
	// minutes.
	DefaultProbeTimeout = 3 * time.Second
)

// Well-known diagnostic paths. Overridable in BundleOptions so tests never
// read the developer's own machine.
const (
	DefaultInterfacesPath    = "/etc/network/interfaces"
	DefaultCorosyncPath      = "/etc/pve/corosync.conf"
	DefaultPVEDir            = "/etc/pve"
	DefaultKernelReleasePath = "/proc/sys/kernel/osrelease"
	DefaultOSReleasePath     = "/etc/os-release"
	DefaultLogUnit           = "vnprox"
)

// arrayIndexPattern strips a TOML array-of-tables index (`oidc.group[0]`)
// down to its allowlist key.
//
//nolint:gochecknoglobals // a compiled pattern.
var arrayIndexPattern = regexp.MustCompile(`\[\d+\]`)

// KeyPathRef ties one on-disk secret file to the SecretClass it belongs to,
// so probeKeyFiles can report "the session encryption key is missing"
// rather than "/etc/vnprox/keys/session.key is missing".
type KeyPathRef struct {
	ClassID string
	Path    string
}

// BundleOptions configures Bundle.
//
//nolint:govet // fieldalignment: an options struct read top-to-bottom by humans; grouping by meaning beats packing a handful of bytes.
type BundleOptions struct {
	// ConfigPath is the vnprox.toml this bundle describes.
	ConfigPath string
	// DBPath is the store to derive facts from. The file itself is never
	// collected.
	DBPath string
	// Listen is [server] listen, used by the port-conflict probe and to
	// find the local daemon's /health.
	Listen string
	// KeyPaths are the declared key files, reported by existence and mode
	// only and never opened.
	KeyPaths []KeyPathRef

	// OutDir / Dest: where the archive goes. Ignored under DryRun.
	OutDir string
	Dest   string

	// Node is the hostname recorded in the manifest; defaults to
	// os.Hostname.
	Node string
	// ToolVersion is recorded in the manifest.
	ToolVersion string

	// Diagnostic source paths. Defaulted from the Default* constants.
	InterfacesPath    string
	CorosyncPath      string
	PVEDir            string
	KernelReleasePath string
	OSReleasePath     string
	LogSource         LogSource

	// Budgets.
	ChangesetLimit int
	FindingLimit   int
	LogTailLines   int
	LogTailBytes   int64
	ProbeTimeout   time.Duration

	// Probe enables the outbound checks (peer reachability, the local
	// daemon's /health, the listen-port bind test). Default on; `--no-probe`
	// turns it off for an operator who wants a bundle that touches nothing
	// but this node's filesystem.
	Probe bool

	// DryRun collects exactly as a real run does and then throws the
	// staging area away without writing an archive.
	DryRun bool

	// Incident (T-2804), when set, adds one entry — incident/timeline.json —
	// to an otherwise ordinary support bundle. Nil (every `vnproxctl
	// support-bundle` invocation) produces exactly the archive this package
	// always produced; the incident collector stages nothing at all.
	Incident *BundleIncident

	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
	// Logger is optional.
	Logger *slog.Logger
}

func (o *BundleOptions) now() time.Time {
	if o.Now == nil {
		return time.Now()
	}
	return o.Now()
}

// applyDefaults fills in everything the caller left blank. Called once, at
// the top of Bundle, so both the dry run and the real run see identical
// options.
func (o *BundleOptions) applyDefaults() error {
	if o.Node == "" {
		h, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("support-bundle: determining node name: %w", err)
		}
		o.Node = h
	}
	if o.InterfacesPath == "" {
		o.InterfacesPath = DefaultInterfacesPath
	}
	if o.CorosyncPath == "" {
		o.CorosyncPath = DefaultCorosyncPath
	}
	if o.PVEDir == "" {
		o.PVEDir = DefaultPVEDir
	}
	if o.KernelReleasePath == "" {
		o.KernelReleasePath = DefaultKernelReleasePath
	}
	if o.OSReleasePath == "" {
		o.OSReleasePath = DefaultOSReleasePath
	}
	if o.LogSource.Path == "" && o.LogSource.Unit == "" {
		o.LogSource.Unit = DefaultLogUnit
	}
	if o.ChangesetLimit <= 0 {
		o.ChangesetLimit = DefaultBundleChangesets
	}
	if o.FindingLimit <= 0 {
		o.FindingLimit = DefaultBundleFindingEvents
	}
	if o.LogTailLines <= 0 {
		o.LogTailLines = DefaultBundleLogLines
	}
	if o.LogTailBytes <= 0 {
		o.LogTailBytes = DefaultBundleLogBytes
	}
	if o.ProbeTimeout <= 0 {
		o.ProbeTimeout = DefaultProbeTimeout
	}
	return nil
}

// BundlePlanEntry is one entry as `--dry-run` reports it: what the file is
// called, what it holds, and what redaction its contents passed.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundlePlanEntry struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Redaction string `json:"redaction"`
	About     string `json:"about"`
}

// BundlePlan is what a bundle contains, produced by the same collection
// pass that produces the archive.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundlePlan struct {
	DryRun bool `json:"dryRun"`
	// ArchivePath is where the archive was (or would be) written.
	ArchivePath string `json:"archivePath"`
	Node        string `json:"node"`
	CollectedAt string `json:"collectedAt"`
	// Collectors is every collector that ran, in order.
	Collectors []string `json:"collectors"`
	// Entries is what they produced, sorted by name.
	Entries []BundlePlanEntry `json:"entries"`
	// Omitted names, for the reader, what a bundle deliberately does not
	// contain. Generated rather than written down, so it cannot describe a
	// policy the code has stopped implementing.
	Omitted []string `json:"omitted"`
}

// BundleResult describes a completed (or dry) support bundle.
//
//nolint:govet // fieldalignment: an options struct read top-to-bottom by humans.
type BundleResult struct {
	// Path is "" for a dry run.
	Path     string
	Bytes    int64
	Manifest Manifest
	Plan     BundlePlan
	Warnings []string
}

// bundleCollectors is the ordered collector set. Each is wrapped in
// sealedCollector, whose Emits() is nil unconditionally.
func bundleCollectors(opts *BundleOptions) []Collector {
	inner := []bundleCollector{
		envCollector{opts},
		configBundleCollector{opts},
		storeFactsCollector{opts},
		changesetsCollector{opts},
		findingsCollector{opts},
		hostNetCollector{opts},
		peersCollector{opts},
		probesCollector{opts},
		logsCollector{opts},
		// T-2804: stages nothing unless opts.Incident is set, so it is in
		// the ordered set unconditionally rather than being conditionally
		// appended — a collector list that changes shape per invocation is
		// a second code path, and --dry-run's whole design is that there is
		// only one.
		incidentCollector{opts},
	}
	out := make([]Collector, 0, len(inner))
	for _, c := range inner {
		out = append(out, sealedCollector{c})
	}
	return out
}

// BundleName is the conventional filename for a support bundle.
//
// Deliberately NOT `vnprox-backup-…`: backup.Prune's retention would
// otherwise consider a bundle a backup and could delete a real one to keep
// a bundle, or vice versa. The two file families never overlap.
func BundleName(node string, t time.Time) string {
	return fmt.Sprintf("vnprox-support-%s-%s.tar.gz", sanitizeKeyName(node), t.UTC().Format("20060102-150405"))
}

// Bundle produces a support bundle.
//
// Ordering: defaults → staging → collect → plan → (dry run stops here) →
// manifest → write. The plan is built from Staging's recorded entries, so
// what --dry-run prints is what the archive contains, not a prediction of
// it.
func Bundle(ctx context.Context, opts BundleOptions) (*BundleResult, error) {
	if err := opts.applyDefaults(); err != nil {
		return nil, err
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

	dest := opts.Dest
	outDir := opts.OutDir
	if dest == "" {
		if outDir == "" {
			return nil, errors.New("support-bundle: neither an output directory nor a destination path was given")
		}
		dest = filepath.Join(outDir, BundleName(opts.Node, opts.now()))
	} else {
		outDir = filepath.Dir(dest)
	}

	// A dry run stages under the OS temp directory rather than the output
	// directory: it must not create the output directory as a side effect
	// of being asked what it would do.
	stagingParent := outDir
	if opts.DryRun {
		stagingParent = ""
	} else if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, fmt.Errorf("support-bundle: creating output directory %s: %w", outDir, err)
	}

	st, err := NewStaging(stagingParent)
	if err != nil {
		return nil, err
	}
	defer func() {
		if rmErr := st.Remove(); rmErr != nil {
			logger.Warn("support-bundle: could not remove staging directory", "error", rmErr)
		}
	}()

	res := &BundleResult{}
	collectors := bundleCollectors(&opts)
	names := make([]string, 0, len(collectors))
	for _, c := range collectors {
		names = append(names, c.Name())
		if collectErr := c.Collect(ctx, st); collectErr != nil {
			return nil, fmt.Errorf("support-bundle: collector %s: %w", c.Name(), collectErr)
		}
	}
	// The readme goes last: it is generated from bundleEntrySchema and from
	// what actually got collected, so it describes this bundle rather than
	// bundles in general.
	if readmeErr := st.WriteFile(entryBundleReadme, RoleMeta, 0o600,
		[]byte(bundleReadme(opts.Node, opts.now(), st.Entries()))); readmeErr != nil {
		return nil, readmeErr
	}

	entries := st.Entries()
	plan := BundlePlan{
		DryRun:      opts.DryRun,
		ArchivePath: dest,
		Node:        opts.Node,
		CollectedAt: opts.now().UTC().Format(time.RFC3339),
		Collectors:  names,
		Omitted:     bundleOmissions(),
	}
	for _, e := range entries {
		d, ok := entryDeclFor(e.Name)
		if !ok {
			// An entry with no declaration is a collector that cannot
			// describe its output. The card says such a collector does not
			// ship, so this is a hard failure rather than a warning.
			return nil, fmt.Errorf("support-bundle: collector produced entry %q, which is not declared in bundleEntrySchema "+
				"— declare it (bundleschema.go) with what redaction its contents passed", e.Name)
		}
		plan.Entries = append(plan.Entries, BundlePlanEntry{
			Name: e.Name, Role: e.Role, Redaction: d.Redaction, About: d.About,
		})
	}
	res.Plan = plan

	if opts.DryRun {
		res.Plan.ArchivePath = dest
		return res, nil
	}

	// declaredSecretClasses over bundle collectors is empty by
	// construction: sealedCollector.Emits() is nil and there is no way to
	// write a bundleCollector that says otherwise. Computing it anyway
	// (rather than hardcoding []string{}) means that if that ever stops
	// being true, the manifest says so instead of lying.
	declared := declaredSecretClasses(collectors)
	m := Manifest{
		Format:              FormatVersion,
		Kind:                KindSupportBundle,
		CreatedAt:           opts.now().UTC().Format(time.RFC3339),
		Tool:                "vnproxctl",
		ToolVersion:         opts.ToolVersion,
		Node:                opts.Node,
		SchemaVersion:       0,
		IncludesKeyMaterial: false,
		SecretClasses:       secretClassIDs(declared),
		Entries:             entries,
	}
	if m.SecretClasses == nil {
		m.SecretClasses = []string{}
	}

	size, err := Write(dest, m, st.Dir())
	if err != nil {
		return nil, err
	}
	res.Path = dest
	res.Bytes = size
	res.Manifest = m
	return res, nil
}

// bundleOmissions is the "what isn't in here" half of the bundle's own
// documentation, generated from the declared inventory so it cannot claim
// to omit something the code has started including.
func bundleOmissions() []string {
	out := []string{
		"the SQLite store itself (" + entryStore + ") — it holds the full audit trail, every rollback snapshot, and the ciphertext of every sealed credential",
		"vnprox.toml verbatim — only allowlisted keys keep their values",
		"/etc/network/interfaces verbatim — only allowlisted options keep their values",
	}
	for _, c := range SecretClassesBy(StorageKeyFile) {
		out = append(out, fmt.Sprintf("the %s — reported by existence and file mode only, never read", c.Name))
	}
	for _, c := range SecretClassesBy(StorageSealedColumn) {
		out = append(out, fmt.Sprintf("the %s (%s) — the column is never selected", c.Name, c.Column))
	}
	for _, c := range SecretClassesBy(StorageExternal) {
		out = append(out, fmt.Sprintf("the %s — %s", c.Name, c.Detail))
	}
	return out
}

// bundleReadme is the human-readable "what's in here and what isn't" the
// card asks for. It is generated from bundleEntrySchema and from the
// entries this run actually produced, so it can neither describe a file
// that is absent nor omit one that is present.
func bundleReadme(node string, at time.Time, entries []Entry) string {
	var b strings.Builder
	b.WriteString("vnprox support bundle\n")
	b.WriteString("=====================\n\n")
	fmt.Fprintf(&b, "node:      %s\n", node)
	fmt.Fprintf(&b, "collected: %s\n\n", at.UTC().Format(time.RFC3339))
	b.WriteString("This archive is intended to be readable by someone who cannot log into this\n")
	b.WriteString("machine, and to be safe to attach to a public forum post. It is redacted by\n")
	b.WriteString("construction: every collector declares what it emits, every emitted field\n")
	b.WriteString("passes an explicit allowlist or a redactor, and the redaction is enforced by\n")
	b.WriteString("tests rather than by review.\n\n")

	b.WriteString("What is in here\n")
	b.WriteString("---------------\n\n")
	for _, e := range entries {
		d, ok := entryDeclFor(e.Name)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "  %-28s %s\n", e.Name, d.About)
		fmt.Fprintf(&b, "  %-28s   redaction: %s\n", "", d.Redaction)
	}

	b.WriteString("\nWhat is deliberately NOT in here\n")
	b.WriteString("--------------------------------\n\n")
	for _, o := range bundleOmissions() {
		fmt.Fprintf(&b, "  * %s\n", o)
	}

	b.WriteString("\nRedaction marker\n")
	b.WriteString("----------------\n\n")
	fmt.Fprintf(&b, "  Anything removed was replaced with the literal string\n    %s\n", Redacted)
	b.WriteString("  so you can grep for what is missing rather than wondering whether it was\n")
	b.WriteString("  removed or simply never there.\n")

	b.WriteString("\nThis is NOT a backup\n")
	b.WriteString("--------------------\n\n")
	b.WriteString("  `vnproxctl restore` refuses this file: it is deliberately incomplete and\n")
	b.WriteString("  restoring it would install a store with no history in it. Take a real\n")
	b.WriteString("  backup with `vnproxctl backup` (see docs/deployment.md).\n")

	b.WriteString("\nWhat is still sensitive\n")
	b.WriteString("-----------------------\n\n")
	b.WriteString("  A bundle contains no credential, but it does describe your network: node\n")
	b.WriteString("  names, interface names, IP addressing, VLAN ids and changeset titles. That\n")
	b.WriteString("  is a map, and it is the price of being diagnosable. Read this file before\n")
	b.WriteString("  you post it.\n")
	return b.String()
}
