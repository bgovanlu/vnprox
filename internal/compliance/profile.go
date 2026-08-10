// Package compliance maps vnprox's existing evidence — the unified findings
// stream's checks (internal/findings), T-1607's named posture factors
// (internal/posture), and T-2601's installed policy rules (internal/change)
// — onto named controls, and reports each control's status with the
// underlying evidence attached.
//
// WHAT THIS IS NOT. It is not a certification, an attestation, or an
// assessment against any published framework. vnprox ships ONE general
// profile describing ordinary network hygiene; the deliverable is the
// mapping FORMAT, so an operator can express their own organisation's
// controls in terms of evidence vnprox already produces. No output of this
// package asserts compliance with anything.
//
// DESIGN CONSTRAINT, inherited from T-2601: a profile is declarative data,
// not a language. There is no expression interpreter here. An evidence item
// is a fixed `{kind, …}` record naming one check, one posture factor, or one
// policy rule (or rule tag); anything a profile wants to say that those
// shapes cannot express is a decision to record, not a language feature to
// add.
//
// THE SAFETY PROPERTY. A control with no mapped evidence reports
// StatusUnmapped — never StatusPass. Every renderer round-trips through a
// parser that recovers the status it rendered, and a standing test asserts
// no format can render an unmapped control as passing. This is the
// difference between a compliance feature and a compliance liability: a
// report that silently upgrades "we have nothing to say about this" into
// "this passed" is worse than no report.
//
// READ-ONLY. Nothing in this package stages, validates, applies, or mutates
// anything. It reports.
package compliance

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProfileFormatVersion is the profile document's schema version — a single
// integer bumped only on a breaking shape change, the same convention
// change.PolicyFormatVersion uses. It is deliberately NOT the profile's own
// content version (Profile.Version), which is what a report cites when it
// says "assessed against general-network-hygiene 1.0.0".
const ProfileFormatVersion = 1

// EvidenceKind is the closed set of evidence a control may map. There is no
// kind that takes an expression: each names one thing vnprox already
// produces.
type EvidenceKind string

const (
	// EvidenceCheck names one findings-stream check
	// (findings.AllCheckNames). Satisfied when no open finding for that
	// check reaches the item's FailAt severity.
	EvidenceCheck EvidenceKind = "check"
	// EvidencePosture names one posture factor (posture.Factor.Name) and a
	// minimum sub-score. A factor posture itself reports as not evaluated
	// is not evaluated here either — T-1607's honesty channel is carried
	// through, never flattened into a pass.
	EvidencePosture EvidenceKind = "posture"
	// EvidencePolicy names one T-2601 policy rule, by id or by tag.
	EvidencePolicy EvidenceKind = "policy"
)

// Evidence is one mapped evidence item. Exactly one selector is meaningful
// per kind; Validate rejects the rest, so a profile cannot half-say
// something.
//
//nolint:govet // fieldalignment: this is the profile document's shape; field order is the documented YAML/JSON contract, not packing — the same precedent internal/findings.Finding sets.
type Evidence struct {
	Kind EvidenceKind `yaml:"kind" json:"kind"`
	// Check is the findings-stream check name (kind: check).
	Check string `yaml:"check,omitempty" json:"check,omitempty"`
	// FailAt is the lowest finding severity that fails this item
	// (kind: check). Empty means DefaultFailAt.
	FailAt string `yaml:"failAt,omitempty" json:"failAt,omitempty"`
	// Factor is the posture factor name (kind: posture).
	Factor string `yaml:"factor,omitempty" json:"factor,omitempty"`
	// MinScore is the factor sub-score at or above which the item is
	// satisfied (kind: posture), 0..100.
	MinScore int `yaml:"minScore,omitempty" json:"minScore,omitempty"`
	// Rule is a policy rule id (kind: policy).
	Rule string `yaml:"rule,omitempty" json:"rule,omitempty"`
	// Tag selects every installed policy rule carrying it (kind: policy).
	// A general profile cannot know a cluster's rule ids, so tags are how
	// it names a CLASS of rule the organisation is expected to install.
	Tag string `yaml:"tag,omitempty" json:"tag,omitempty"`
	// Note explains, in the profile, why this item evidences the control.
	// It is carried into the report — an auditor reading "check
	// mgmt_single_path" should not have to guess the argument.
	Note string `yaml:"note,omitempty" json:"note,omitempty"`
}

// DefaultFailAt is the severity a check-evidence item fails at when the
// profile does not say. Warning, not error: an informational finding
// (trunk_unused_vlans) is a note, but a warning is something an operator was
// asked to look at.
const DefaultFailAt = "warning"

// Name returns the selector this item is about, for display and for the
// report's evidence naming. It is what "the failing check is named" means.
func (e Evidence) Name() string {
	switch e.Kind {
	case EvidenceCheck:
		return e.Check
	case EvidencePosture:
		return e.Factor
	case EvidencePolicy:
		if e.Rule != "" {
			return e.Rule
		}
		return "tag:" + e.Tag
	default:
		return ""
	}
}

// Key is the stable "kind:name" identifier a rendered report uses for an
// evidence item, and the token its parser reads back.
func (e Evidence) Key() string { return string(e.Kind) + ":" + e.Name() }

// Control is one named control: what it asserts, why, and which evidence
// vnprox has for it. Evidence MAY be empty — that is the honest way to ship
// a control vnprox cannot speak to, and it reports StatusUnmapped.
type Control struct {
	ID    string `yaml:"id" json:"id"`
	Title string `yaml:"title" json:"title"`
	// Statement is what the control asserts, in the profile author's own
	// words.
	Statement string `yaml:"statement" json:"statement"`
	// UnmappedReason is required when Evidence is empty: a control with no
	// evidence must say WHY vnprox cannot evidence it, or the reader
	// cannot tell "nothing to check" from "nobody wrote the mapping".
	UnmappedReason string     `yaml:"unmappedReason,omitempty" json:"unmappedReason,omitempty"`
	Evidence       []Evidence `yaml:"evidence,omitempty" json:"evidence,omitempty"`
}

// Profile is a whole compliance profile document.
//
//nolint:govet // fieldalignment: document shape; field order is the documented YAML/JSON contract.
type Profile struct {
	// FormatVersion is the schema version (ProfileFormatVersion).
	FormatVersion int    `yaml:"formatVersion" json:"formatVersion"`
	ID            string `yaml:"id" json:"id"`
	Title         string `yaml:"title" json:"title"`
	// Version is this document's own content version, cited by every
	// report rendered from it.
	Version     string `yaml:"version" json:"version"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Notice is the profile's standing statement about what a report from
	// it does and does not claim. Required, and rendered in every output
	// format: the arc risk register's mitigation for "compliance reporting
	// is read as certification" is that the artifact says so itself.
	Notice   string    `yaml:"notice" json:"notice"`
	Controls []Control `yaml:"controls" json:"controls"`
}

// ProfileError is the single error type every profile load/validation
// failure takes, so the message always names the file, the offending
// control, and the offending field — the same shape change.PolicyLoadError
// established for policy documents.
type ProfileError struct {
	File      string
	ControlID string
	Field     string
	Msg       string
}

func (e *ProfileError) Error() string {
	var b strings.Builder
	b.WriteString("compliance: profile")
	if e.File != "" {
		fmt.Fprintf(&b, " file %s", e.File)
	}
	if e.ControlID != "" {
		fmt.Fprintf(&b, ": control %q", e.ControlID)
	}
	if e.Field != "" {
		fmt.Fprintf(&b, ": field %q", e.Field)
	}
	fmt.Fprintf(&b, ": %s", e.Msg)
	return b.String()
}

// knownSeverities is the closed severity vocabulary a check-evidence item's
// FailAt may use — internal/findings' own, duplicated (not imported) to keep
// this package decoupled from the findings engine, exactly as
// internal/posture duplicates its baselineSource constant.
//
//nolint:gochecknoglobals // a read-only vocabulary table
var knownSeverities = map[string]int{"info": 0, "warning": 1, "error": 2}

// ParseProfile decodes and validates one profile document. file is used only
// to build error messages.
func ParseProfile(file string, data []byte) (Profile, error) {
	var p Profile
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return Profile{}, &ProfileError{File: file, Msg: "cannot be parsed: " + err.Error()}
	}
	if err := p.Validate(file); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// Validate checks p for every statically-decidable defect, returning the
// first as a *ProfileError naming file, control, and field.
//
// It deliberately does NOT check that a named check exists in
// findings.AllCheckNames: a profile may legitimately name a check from a
// plugin or a newer daemon, and refusing to load the whole document for one
// unknown selector would take the other controls down with it. An evidence
// item whose selector nothing produces is reported per-control at
// evaluation time instead — as not evaluated, never as a pass.
func (p Profile) Validate(file string) error {
	if p.FormatVersion != 0 && p.FormatVersion != ProfileFormatVersion {
		return &ProfileError{File: file, Field: "formatVersion", Msg: fmt.Sprintf(
			"unsupported profile format version %d (this build understands %d)", p.FormatVersion, ProfileFormatVersion)}
	}
	if strings.TrimSpace(p.ID) == "" {
		return &ProfileError{File: file, Field: "id", Msg: "profile id is required"}
	}
	if strings.TrimSpace(p.Title) == "" {
		return &ProfileError{File: file, Field: "title", Msg: "profile title is required"}
	}
	if strings.TrimSpace(p.Version) == "" {
		return &ProfileError{File: file, Field: "version", Msg: "profile version is required (every rendered report cites it)"}
	}
	if strings.TrimSpace(p.Notice) == "" {
		return &ProfileError{File: file, Field: "notice", Msg: "profile notice is required: a report that does not state what it does not claim is the failure mode this feature exists to avoid"}
	}
	if len(p.Controls) == 0 {
		return &ProfileError{File: file, Field: "controls", Msg: "a profile with no controls reports nothing"}
	}
	seen := map[string]bool{}
	for i, c := range p.Controls {
		if err := validateControl(file, i, c, seen); err != nil {
			return err
		}
		seen[c.ID] = true
	}
	return nil
}

func validateControl(file string, i int, c Control, seen map[string]bool) error {
	at := func(field string) string { return fmt.Sprintf("controls[%d].%s", i, field) }
	fail := func(field, msg string, args ...any) error {
		return &ProfileError{File: file, ControlID: c.ID, Field: at(field), Msg: fmt.Sprintf(msg, args...)}
	}

	if strings.TrimSpace(c.ID) == "" {
		return fail("id", "control id is required")
	}
	if seen[c.ID] {
		return fail("id", "duplicate control id %q", c.ID)
	}
	if strings.TrimSpace(c.Title) == "" {
		return fail("title", "control title is required")
	}
	if strings.TrimSpace(c.Statement) == "" {
		return fail("statement", "control statement is required (it is what the report tells the reader the control asserts)")
	}
	if len(c.Evidence) == 0 {
		if strings.TrimSpace(c.UnmappedReason) == "" {
			return fail("unmappedReason", "a control with no mapped evidence must say why vnprox cannot evidence it; it will report %q, and the reader must be able to tell that from an unwritten mapping", StatusUnmapped)
		}
		return nil
	}
	if strings.TrimSpace(c.UnmappedReason) != "" {
		return fail("unmappedReason", "unmappedReason is only meaningful on a control with no evidence")
	}
	for j, e := range c.Evidence {
		if err := validateEvidence(file, c.ID, at(fmt.Sprintf("evidence[%d]", j)), e); err != nil {
			return err
		}
	}
	return nil
}

func validateEvidence(file, controlID, at string, e Evidence) error {
	fail := func(field, msg string, args ...any) error {
		return &ProfileError{File: file, ControlID: controlID, Field: at + "." + field, Msg: fmt.Sprintf(msg, args...)}
	}

	// Selectors belonging to another kind are a typo, not a nuance: a
	// `{kind: check, factor: segmentation}` item would silently ignore the
	// factor, which is exactly the class of mistake that makes a control
	// look evidenced when it is not.
	type selector struct {
		name string
		set  bool
	}
	all := []selector{
		{"check", e.Check != ""},
		{"factor", e.Factor != ""},
		{"rule", e.Rule != ""},
		{"tag", e.Tag != ""},
	}
	allow := func(names ...string) error {
		permitted := map[string]bool{}
		for _, n := range names {
			permitted[n] = true
		}
		for _, s := range all {
			if s.set && !permitted[s.name] {
				return fail(s.name, "%q is not a selector of evidence kind %q", s.name, e.Kind)
			}
		}
		return nil
	}

	switch e.Kind {
	case EvidenceCheck:
		if err := allow("check"); err != nil {
			return err
		}
		if e.Check == "" {
			return fail("check", "a check evidence item must name a check")
		}
		if e.FailAt != "" {
			if _, ok := knownSeverities[e.FailAt]; !ok {
				return fail("failAt", "unknown severity %q (known: error, info, warning)", e.FailAt)
			}
		}
		if e.MinScore != 0 {
			return fail("minScore", "minScore is only meaningful on posture evidence")
		}
	case EvidencePosture:
		if err := allow("factor"); err != nil {
			return err
		}
		if e.Factor == "" {
			return fail("factor", "a posture evidence item must name a factor")
		}
		if e.MinScore < 0 || e.MinScore > 100 {
			return fail("minScore", "minScore must be within 0..100, got %d", e.MinScore)
		}
		if e.FailAt != "" {
			return fail("failAt", "failAt is only meaningful on check evidence")
		}
	case EvidencePolicy:
		if err := allow("rule", "tag"); err != nil {
			return err
		}
		if (e.Rule == "") == (e.Tag == "") {
			return fail("rule", "a policy evidence item must name exactly one of rule or tag")
		}
		if e.FailAt != "" || e.MinScore != 0 {
			return fail("failAt", "failAt/minScore are not meaningful on policy evidence")
		}
	default:
		return fail("kind", "unknown evidence kind %q (known: %s, %s, %s)", e.Kind, EvidenceCheck, EvidencePolicy, EvidencePosture)
	}
	return nil
}

// MappedChecks returns every check name any control in p maps, sorted and
// deduplicated. It is the numerator of the report's unmapped-check list.
func (p Profile) MappedChecks() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range p.Controls {
		for _, e := range c.Evidence {
			if e.Kind == EvidenceCheck && e.Check != "" && !seen[e.Check] {
				seen[e.Check] = true
				out = append(out, e.Check)
			}
		}
	}
	sort.Strings(out)
	return out
}

// builtinProfiles holds the shipped profile documents. ONE general profile,
// deliberately: the format is the deliverable, and a directory full of
// framework-named files would be the certification claim this feature must
// not make.
//
//go:embed profiles/*.yaml
var builtinProfiles embed.FS

// GeneralProfileID is the id of the one shipped profile.
const GeneralProfileID = "general-network-hygiene"

// LoadBuiltins parses every shipped profile. An unparsable shipped profile
// is a build defect, so it is returned as an error rather than skipped —
// TestBuiltinProfiles_Parse fails on it before it can ship.
func LoadBuiltins() ([]Profile, error) {
	entries, err := fs.ReadDir(builtinProfiles, "profiles")
	if err != nil {
		return nil, fmt.Errorf("compliance: reading built-in profiles: %w", err)
	}
	out := make([]Profile, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".yaml") {
			continue
		}
		name := path.Join("profiles", ent.Name())
		data, readErr := builtinProfiles.ReadFile(name)
		if readErr != nil {
			return nil, fmt.Errorf("compliance: reading built-in profile %s: %w", name, readErr)
		}
		p, parseErr := ParseProfile(name, data)
		if parseErr != nil {
			return nil, fmt.Errorf("compliance: built-in profile %s is unusable: %w", name, parseErr)
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
