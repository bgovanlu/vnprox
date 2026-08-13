package docexport_test

// T-2806's half of the config-doc export: the operator's own notes and
// canvas regions, which are most useful to exactly the reader who cannot
// see the map (AC4), and the per-render-path escaping of the free text they
// contain (AC6).
//
// AC6 is asserted ONE ASSERTION PER PATH, and each path is escaped by its
// own renderer rather than by a shared helper — html.go uses
// html.EscapeString, markdown.go uses its own mdText, svg.go escapes at the
// point it writes the <text> element. That is deliberate: a single shared
// escape function would make all three tests pass or fail together, so a
// mutation to one renderer could be masked by the others.

import (
	"context"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/annotate"
	"github.com/bgovanlu/vnprox/internal/docexport"
)

// hostileNote is the classic operator-authored injection payload: a note
// one operator typed, rendered into a standalone HTML document another
// operator opens in a browser.
const (
	hostileNote   = `<script>alert("pwned")</script> and a | pipe`
	hostileRegion = `<img src=x onerror="alert(1)">`
)

// fakeAnnotations records what the export asked for. The recorded flag is
// the point of TestExport_AsksForLiveAnnotationsOnly below.
type fakeAnnotations struct {
	notes           []annotate.Note
	regions         []annotate.Region
	notesErr        error
	regionsErr      error
	notesInclude    []bool
	regionsInclude  []bool
	notesCallCount  int
	regionCallCount int
}

func (f *fakeAnnotations) Notes(_ context.Context, includeExpired bool) ([]annotate.Note, error) {
	f.notesCallCount++
	f.notesInclude = append(f.notesInclude, includeExpired)
	return f.notes, f.notesErr
}

func (f *fakeAnnotations) Regions(_ context.Context, includeExpired bool) ([]annotate.Region, error) {
	f.regionCallCount++
	f.regionsInclude = append(f.regionsInclude, includeExpired)
	return f.regions, f.regionsErr
}

func annotatedData(t *testing.T, src *fakeAnnotations) docexport.Data {
	t.Helper()
	svc := buildService(t, fixtureThreeNodeVlan)
	svc.Annotations = src
	return svc.Build(context.Background())
}

// TestExport_AnnotationsAppearInBothFormats is T-2806 AC4.
func TestExport_AnnotationsAppearInBothFormats(t *testing.T) {
	src := &fakeAnnotations{
		notes: []annotate.Note{
			{
				ID: "n1", Ref: "bridge:pve1:vmbr0", Content: "temporary until the switch swap",
				CreatedBy: "alice@pve", CreatedAt: 1_700_000_000, ExpiresAt: 1_800_000_000,
			},
			{
				ID: "n2", Ref: "bridge:pve1:vmbr9", Content: "removed: vendor switch could not trunk VLAN 40",
				CreatedBy: "bob@pve", CreatedAt: 1_700_000_500, Orphaned: true,
			},
		},
		regions: []annotate.Region{
			{ID: "r1", Label: "vendor-managed, do not touch", CreatedBy: "alice@pve", CreatedAt: 1_700_000_000, W: 10, H: 10},
		},
	}
	data := annotatedData(t, src)

	if len(data.Annotations) != 2 || len(data.Regions) != 1 {
		t.Fatalf("Data carried %d annotations / %d regions, want 2 / 1", len(data.Annotations), len(data.Regions))
	}

	md := docexport.Markdown(data)
	htmlDoc := docexport.HTML(data)

	for _, want := range []string{
		docexport.HeadingAnnotations,
		docexport.RegionsSubheading,
		"temporary until the switch swap",
		"removed: vendor switch could not trunk VLAN 40",
		"bridge:pve1:vmbr0",
		"alice@pve",
		"vendor-managed, do not touch",
		// The orphan must be labelled as such, not silently listed: the
		// reader needs to know the entity is gone (T-2806 AC2).
		docexport.OrphanedMarker,
		// And a note with no expiry says so rather than showing a blank.
		docexport.NeverExpiresMarker,
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown export missing %q", want)
		}
		if !strings.Contains(htmlDoc, want) {
			t.Errorf("html export missing %q", want)
		}
	}

	// The embedded SVG carries the region labels too, so the diagram in the
	// document names the same groupings the map does.
	if !strings.Contains(htmlDoc, "vendor-managed") {
		t.Error("html export's embedded SVG missing the region label")
	}
}

// TestExport_AsksForLiveAnnotationsOnly pins the read-time-expiry contract
// at the export boundary (T-2806 AC3): the document is a display surface,
// so it must ask internal/annotate for the live view and must not carry any
// expiry logic of its own. Asserting the ARGUMENT rather than the output is
// what makes this a structural guarantee — an export that computed expiry
// itself would be a second clock.
func TestExport_AsksForLiveAnnotationsOnly(t *testing.T) {
	src := &fakeAnnotations{}
	annotatedData(t, src)

	if src.notesCallCount != 1 || src.regionCallCount != 1 {
		t.Fatalf("Notes called %d times, Regions %d times; want 1 each", src.notesCallCount, src.regionCallCount)
	}
	for _, got := range src.notesInclude {
		if got {
			t.Error("export asked for expired notes — an expired note must never reach a document")
		}
	}
	for _, got := range src.regionsInclude {
		if got {
			t.Error("export asked for expired regions")
		}
	}
}

// TestExport_AnnotationReadFailureDegradesTheSection: a failed annotation
// read costs the annotation section, never the whole export — the same
// treatment an unavailable SDN tree already gets.
func TestExport_AnnotationReadFailureDegradesTheSection(t *testing.T) {
	src := &fakeAnnotations{notesErr: context.DeadlineExceeded, regionsErr: context.DeadlineExceeded}
	data := annotatedData(t, src)

	if len(data.Annotations) != 0 || len(data.Regions) != 0 {
		t.Fatalf("failed read produced %d notes / %d regions, want none", len(data.Annotations), len(data.Regions))
	}
	md := docexport.Markdown(data)
	if !strings.Contains(md, "## "+docexport.HeadingAnnotations) {
		t.Error("markdown export lost its annotation section after a failed read")
	}
	if !strings.Contains(docexport.HTML(data), ">"+docexport.HeadingAnnotations+"<") {
		t.Error("html export lost its annotation section after a failed read")
	}
}

// TestExport_NoAnnotationSourceStillExports: a daemon with no annotation
// layer wired renders the section as empty rather than failing.
func TestExport_NoAnnotationSourceStillExports(t *testing.T) {
	svc := buildService(t, fixtureThreeNodeVlan)
	data := svc.Build(context.Background())
	if len(data.Annotations) != 0 || len(data.Regions) != 0 {
		t.Fatalf("nil source produced %d notes / %d regions, want none", len(data.Annotations), len(data.Regions))
	}
	if !strings.Contains(docexport.Markdown(data), "## "+docexport.HeadingAnnotations) {
		t.Error("markdown export missing the annotation section with no source wired")
	}
}

// --- AC6: one assertion per render path -----------------------------------

func hostileData(t *testing.T) docexport.Data {
	t.Helper()
	return annotatedData(t, &fakeAnnotations{
		notes: []annotate.Note{{
			ID: "n1", Ref: "bridge:pve1:vmbr0", Content: hostileNote,
			CreatedBy: hostileNote, CreatedAt: 1_700_000_000,
		}},
		regions: []annotate.Region{{
			ID: "r1", Label: hostileRegion, CreatedBy: "alice@pve", CreatedAt: 1_700_000_000, W: 10, H: 10,
		}},
	})
}

// AC6 path 1 of 3 in this package: the standalone HTML document.
func TestExport_AnnotationTextIsEscapedInHTML(t *testing.T) {
	htmlDoc := docexport.HTML(hostileData(t))

	for _, forbidden := range []string{"<script>alert", "</script> and a", `<img src=x`} {
		if strings.Contains(htmlDoc, forbidden) {
			t.Errorf("html export contains unescaped %q — the note text is live markup", forbidden)
		}
	}
	if !strings.Contains(htmlDoc, "&lt;script&gt;alert(&#34;pwned&#34;)&lt;/script&gt;") {
		t.Errorf("html export did not escape the note text; got:\n%s", excerpt(htmlDoc, "Operator annotations"))
	}
}

// AC6 path 2 of 3: the Markdown document. Markdown's hazards are not
// HTML's — raw HTML passes straight through most renderers, and an
// unescaped pipe silently corrupts the table — so this path is asserted on
// its own terms, against its own escape.
func TestExport_AnnotationTextIsEscapedInMarkdown(t *testing.T) {
	md := docexport.Markdown(hostileData(t))

	if strings.Contains(md, "<script>") || strings.Contains(md, "<img") {
		t.Errorf("markdown export contains raw HTML — it would render live in any HTML view:\n%s",
			excerpt(md, "Operator annotations"))
	}
	if !strings.Contains(md, "&lt;script&gt;") {
		t.Errorf("markdown export did not entity-escape the note text:\n%s", excerpt(md, "Operator annotations"))
	}
	// The pipe must be escaped, or the note invents table columns.
	if !strings.Contains(md, `and a \| pipe`) {
		t.Errorf("markdown export did not escape the pipe in the note text:\n%s", excerpt(md, "Operator annotations"))
	}
}

// AC6 path 3 of 3: the inline SVG topology render embedded in the HTML
// document. It is a separate renderer writing separate elements, so it
// gets its own assertion and its own mutation.
func TestExport_RegionLabelIsEscapedInSVG(t *testing.T) {
	data := hostileData(t)
	svg := docexport.RenderSVGWithRegions(data.Topology, data.Regions)

	if strings.Contains(svg, "<img") {
		t.Errorf("svg render contains raw markup from a region label:\n%s", svg)
	}
	if !strings.Contains(svg, "&lt;img src=x") {
		t.Errorf("svg render did not escape the region label:\n%s", svg)
	}
}

// TestExport_MultilineNoteDoesNotBreakTheMarkdownTable: a note is typed in
// a textarea, so it can contain newlines; a raw newline would terminate the
// table row and drop the rest of the note out of the document.
func TestExport_MultilineNoteDoesNotBreakTheMarkdownTable(t *testing.T) {
	data := annotatedData(t, &fakeAnnotations{
		notes: []annotate.Note{{
			ID: "n1", Ref: "bridge:pve1:vmbr0", CreatedBy: "alice@pve", CreatedAt: 1_700_000_000,
			Content: "line one\nline two",
		}},
	})
	md := docexport.Markdown(data)
	if !strings.Contains(md, "line one line two") {
		t.Errorf("markdown export split a multi-line note across rows:\n%s", excerpt(md, "Operator annotations"))
	}
}

// excerpt returns the part of doc from marker onward, capped, for readable
// failure output.
func excerpt(doc, marker string) string {
	i := strings.Index(doc, marker)
	if i < 0 {
		return doc
	}
	end := i + 600
	if end > len(doc) {
		end = len(doc)
	}
	return doc[i:end]
}
